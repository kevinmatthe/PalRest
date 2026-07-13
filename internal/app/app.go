package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"reflect"
	"sync"
	"time"

	"github.com/kevinmatt/palworld-playtime-guard/internal/analytics"
	"github.com/kevinmatt/palworld-playtime-guard/internal/api"
	"github.com/kevinmatt/palworld-playtime-guard/internal/config"
	"github.com/kevinmatt/palworld-playtime-guard/internal/guard"
	"github.com/kevinmatt/palworld-playtime-guard/internal/observation"
	"github.com/kevinmatt/palworld-playtime-guard/internal/palworld"
	"github.com/kevinmatt/palworld-playtime-guard/internal/policy"
	"github.com/kevinmatt/palworld-playtime-guard/internal/poller"
	"github.com/kevinmatt/palworld-playtime-guard/internal/store"
	"go.yaml.in/yaml/v3"
)

type App struct {
	configPath  string
	configMu    sync.RWMutex
	config      config.Config
	repo        *store.Repository
	policies    *policy.Service
	guard       *guard.Service
	analytics   *analytics.Service
	poller      *poller.Poller
	httpServer  *http.Server
	pollerDone  chan struct{}
	watcherDone chan struct{}
	closeOnce   sync.Once
	closeErr    error
}

func New(configPath string) (*App, error) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	withoutPolicy, err := withoutPolicySection(data)
	if err != nil {
		return nil, err
	}
	cfg, err := config.Parse(withoutPolicy, os.LookupEnv)
	if err != nil {
		return nil, err
	}
	repo, err := store.Open(context.Background(), cfg.Storage.Path)
	if err != nil {
		return nil, err
	}
	if _, readErr := repo.PolicyDocument(context.Background()); errors.Is(readErr, store.ErrNotFound) {
		cfg, err = config.Parse(data, os.LookupEnv)
		if err != nil {
			_ = repo.Close()
			return nil, err
		}
	} else if readErr != nil {
		_ = repo.Close()
		return nil, readErr
	}
	policies, err := policy.New(repo, cfg.Policy)
	if err != nil {
		_ = repo.Close()
		return nil, err
	}
	cfg.Policy = policies.Policy()
	location, err := time.LoadLocation(cfg.Policy.Timezone)
	if err != nil {
		_ = repo.Close()
		return nil, fmt.Errorf("load policy timezone: %w", err)
	}
	analyticsService := analytics.New(repo, cfg.Server.MaxObservationGap.Duration, location)
	openPlayers, analyticsAt, err := repo.OpenAnalyticsPlayers(context.Background())
	if err != nil {
		_ = repo.Close()
		return nil, err
	}
	if err := analyticsService.Restore(analyticsAt, openPlayers); err != nil {
		_ = repo.Close()
		return nil, err
	}
	guardService := guard.New(repo, policies, cfg.Server.MaxObservationGap.Duration, cfg.Enforcement.KickRetryInitial.Duration, cfg.Enforcement.KickRetryMax.Duration)
	client := palworld.New(cfg.Server.BaseURL, cfg.Password(), cfg.Server.RequestTimeout.Duration)
	playerObservations := observation.New(repo, cfg.Server.MaxObservationGap.Duration, observation.DefaultMovementThreshold,
		observation.DefaultMaxSampleInterval, observation.DefaultRawObservationRetention, observation.NewID)
	serverObservations := observation.NewServer(repo, observation.NewID)
	serverTimeout := cfg.Server.RequestTimeout.Duration
	if serverTimeout > observation.DefaultServerObservationTimeout {
		serverTimeout = observation.DefaultServerObservationTimeout
	}
	poll, err := poller.New(client, guardService, analyticsService, cfg.Server.PollInterval.Duration, cfg.Enforcement.AnnounceMessage, cfg.Enforcement.KickMessage, time.Now,
		poller.WithPlayerObserver(playerObservations),
		poller.WithServerObservations(client, serverObservations),
		poller.WithServerObservationTimeout(serverTimeout),
		poller.WithServerMetadataInterval(observation.DefaultServerDocumentInterval),
	)
	if err != nil {
		_ = repo.Close()
		return nil, err
	}
	app := &App{
		configPath: configPath, config: cfg, repo: repo, policies: policies, guard: guardService, analytics: analyticsService, poller: poll,
		pollerDone: make(chan struct{}), watcherDone: make(chan struct{}),
	}
	adminUser, adminPass := cfg.AdminCredentials()
	apiServer := api.New(repo, poll, guardService, repo, analyticsService, policies, guardService, repo, adminUser, adminPass, app.CurrentConfig, poll)
	app.httpServer = apiServer.HTTPServer(cfg.HTTP.Listen)
	return app, nil
}

func (a *App) CurrentConfig() config.Config {
	a.configMu.RLock()
	defer a.configMu.RUnlock()
	return a.config
}

func (a *App) Run(ctx context.Context) error {
	runCtx, cancelRun := context.WithCancel(ctx)
	defer cancelRun()
	httpErrors := make(chan error, 1)
	go func() {
		err := a.httpServer.ListenAndServe()
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			httpErrors <- fmt.Errorf("serve HTTP: %w", err)
			return
		}
		httpErrors <- nil
	}()
	go func() {
		defer close(a.pollerDone)
		a.poller.Run(runCtx)
	}()
	go func() {
		defer close(a.watcherDone)
		a.watchConfig(runCtx)
	}()

	var runErr error
	serverResultPending := true
	select {
	case <-ctx.Done():
	case err := <-httpErrors:
		runErr = err
		serverResultPending = false
	}
	cancelRun()
	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 10*time.Second)
	shutdownErr := a.httpServer.Shutdown(shutdownCtx)
	cancelShutdown()
	if serverResultPending {
		runErr = errors.Join(runErr, <-httpErrors)
	}
	<-a.pollerDone
	<-a.watcherDone
	return errors.Join(runErr, shutdownErr, a.Close())
}

func (a *App) watchConfig(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	var lastMod time.Time
	var lastSize int64 = -1
	if info, err := os.Stat(a.configPath); err == nil {
		lastMod, lastSize = info.ModTime(), info.Size()
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			info, err := os.Stat(a.configPath)
			if err != nil {
				a.poller.SetConfigReloadError(err.Error())
				continue
			}
			if info.ModTime() != lastMod || info.Size() != lastSize {
				lastMod, lastSize = info.ModTime(), info.Size()
				_ = a.reload()
			}
		}
	}
}

func (a *App) reload() error {
	data, err := os.ReadFile(a.configPath)
	if err != nil {
		return a.reloadError(fmt.Errorf("read config: %w", err))
	}
	current := a.CurrentConfig()
	data, err = withoutPolicySection(data)
	if err != nil {
		return a.reloadError(err)
	}
	next, err := config.Parse(data, os.LookupEnv)
	if err != nil {
		return a.reloadError(err)
	}
	next.Policy = current.Policy
	if !reflect.DeepEqual(current.Server, next.Server) || !reflect.DeepEqual(current.HTTP, next.HTTP) || !reflect.DeepEqual(current.Storage, next.Storage) ||
		current.Enforcement.KickRetryInitial != next.Enforcement.KickRetryInitial || current.Enforcement.KickRetryMax != next.Enforcement.KickRetryMax {
		return a.reloadError(fmt.Errorf("server, HTTP, storage, and retry settings require a restart"))
	}
	if err := a.poller.ApplyConfig(func() error { return nil }, next.Enforcement.AnnounceMessage, next.Enforcement.KickMessage); err != nil {
		return a.reloadError(err)
	}
	a.configMu.Lock()
	a.config = next
	a.configMu.Unlock()
	a.poller.SetConfigReloadError("")
	return nil
}

func withoutPolicySection(data []byte) ([]byte, error) {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	var document yaml.Node
	if err := dec.Decode(&document); err != nil {
		return nil, fmt.Errorf("decode config for reload: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("decode config for reload: multiple YAML documents are not allowed")
		}
		return nil, fmt.Errorf("decode config for reload: %w", err)
	}
	if len(document.Content) == 0 || document.Content[0].Kind != yaml.MappingNode {
		return nil, fmt.Errorf("decode config for reload: root must be a mapping")
	}
	root := document.Content[0]
	for i := 0; i < len(root.Content); i += 2 {
		if root.Content[i].Value == "policy" {
			root.Content = append(root.Content[:i], root.Content[i+2:]...)
			break
		}
	}
	result, err := yaml.Marshal(&document)
	if err != nil {
		return nil, fmt.Errorf("encode config for reload: %w", err)
	}
	return result, nil
}

func (a *App) reloadError(err error) error {
	a.poller.SetConfigReloadError(err.Error())
	return err
}

func (a *App) Close() error {
	a.closeOnce.Do(func() { a.closeErr = a.repo.Close() })
	return a.closeErr
}
