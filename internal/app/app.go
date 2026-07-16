package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/kevinmatt/palworld-playtime-guard/internal/analytics"
	"github.com/kevinmatt/palworld-playtime-guard/internal/api"
	"github.com/kevinmatt/palworld-playtime-guard/internal/config"
	"github.com/kevinmatt/palworld-playtime-guard/internal/guard"
	"github.com/kevinmatt/palworld-playtime-guard/internal/observation"
	"github.com/kevinmatt/palworld-playtime-guard/internal/overlay"
	"github.com/kevinmatt/palworld-playtime-guard/internal/palworld"
	"github.com/kevinmatt/palworld-playtime-guard/internal/policy"
	"github.com/kevinmatt/palworld-playtime-guard/internal/poller"
	"github.com/kevinmatt/palworld-playtime-guard/internal/saveworker"
	"github.com/kevinmatt/palworld-playtime-guard/internal/store"
	"go.yaml.in/yaml/v3"
)

type App struct {
	configPath   string
	configMu     sync.RWMutex
	config       config.Config
	repo         *store.Repository
	policies     *policy.Service
	guard        *guard.Service
	analytics    *analytics.Service
	poller       *poller.Poller
	saveWorker   *saveworker.Runner
	saveImporter *saveworker.Importer
	liveGameData *observation.LiveGameData
	client       *palworld.Client
	httpServer   *http.Server
	pollerDone   chan struct{}
	watcherDone  chan struct{}
	auxDone      chan struct{}
	closeOnce    sync.Once
	closeErr     error
}

type overlayPolicyCoordinator struct {
	mu       sync.RWMutex
	updater  api.PolicyUpdater
	provider *overlay.PalworldProvider
}

func newOverlayPolicyCoordinator(updater api.PolicyUpdater, provider *overlay.PalworldProvider) *overlayPolicyCoordinator {
	if updater == nil {
		panic("app: nil policy updater")
	}
	if provider == nil {
		panic("app: nil overlay provider")
	}
	return &overlayPolicyCoordinator{updater: updater, provider: provider}
}

func (c *overlayPolicyCoordinator) Snapshot(ctx context.Context, gameID, userID string) (overlay.Snapshot, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.provider.Snapshot(ctx, gameID, userID)
}

func (c *overlayPolicyCoordinator) ApplyPolicyTimezone(update func() error, location *time.Location) error {
	if location == nil {
		return errors.New("overlay policy location is nil")
	}
	// Lock order is coordinator, then updater/provider. Neither dependency calls back into the coordinator.
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.updater.ApplyPolicyTimezone(update, location); err != nil {
		return err
	}
	return c.provider.SetLocation(location)
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
	playerObservations := observation.New(repo, cfg.Server.MaxObservationGap.Duration, cfg.Observation.TrajectoryMinDistance,
		cfg.Observation.TrajectoryPingChangeThreshold, cfg.Observation.TrajectoryMaxInterval.Duration, cfg.Observation.RawRetention.Duration, observation.NewID)
	serverObservations := observation.NewServer(repo, observation.NewID)
	if err := serverObservations.Restore(context.Background()); err != nil {
		_ = repo.Close()
		return nil, err
	}
	var saveRunner *saveworker.Runner
	var saveImporter *saveworker.Importer
	if cfg.Save.Enabled {
		saveRunner, err = saveworker.New(cfg.Save.WorkerCommand, cfg.Save.WorkerTimeout.Duration)
		if err != nil {
			_ = repo.Close()
			return nil, err
		}
		saveImporter = saveworker.NewImporter(saveRunner, repo, time.Now)
	}
	liveGameData := observation.NewLiveGameData()
	worldPOIs := &observation.WorldPOIProvider{Save: repo, Live: liveGameData}
	pollOptions := []poller.Option{
		poller.WithPlayerObserver(playerObservations),
		poller.WithServerObservations(client, serverObservations),
		poller.WithServerMetadataInterval(cfg.Observation.ServerDocumentInterval.Duration),
	}
	if cfg.Server.RequestTimeout.Duration < poller.DefaultServerObservationTimeout {
		pollOptions = append(pollOptions, poller.WithServerObservationTimeout(cfg.Server.RequestTimeout.Duration))
	}
	poll, err := poller.New(client, guardService, analyticsService, cfg.Server.PollInterval.Duration,
		cfg.Enforcement.AnnounceMessage, cfg.Enforcement.KickMessage, cfg.Enforcement.LoginMessage, time.Now, pollOptions...)
	if err != nil {
		_ = repo.Close()
		return nil, err
	}
	app := &App{
		configPath: configPath, config: cfg, repo: repo, policies: policies, guard: guardService, analytics: analyticsService, poller: poll, saveWorker: saveRunner, saveImporter: saveImporter,
		liveGameData: liveGameData, client: client,
		pollerDone: make(chan struct{}), watcherDone: make(chan struct{}), auxDone: make(chan struct{}),
	}
	adminUser, adminPass := cfg.AdminCredentials()
	overlayProvider := overlay.NewPalworldProvider(guardService, repo, poll, location, cfg.Server.MaxObservationGap.Duration)
	overlayCoordinator := newOverlayPolicyCoordinator(poll, overlayProvider)
	apiOptions := []any{overlayCoordinator, worldPOIs, api.WithOverlayProvider(overlayCoordinator)}
	if saveImporter != nil {
		apiOptions = append(apiOptions, saveImporter)
	}
	apiServer := api.New(repo, poll, guardService, repo, analyticsService, policies, guardService, repo, adminUser, adminPass, app.CurrentConfig, apiOptions...)
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
	go func() {
		defer close(a.auxDone)
		a.runAuxLoops(runCtx)
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
	if a.auxDone != nil {
		<-a.auxDone
	}
	return errors.Join(runErr, shutdownErr, a.Close())
}

// runAuxLoops samples live /game-data and optionally auto-imports saves.
// Failures never stop Guard polling.
func (a *App) runAuxLoops(ctx context.Context) {
	cfg := a.CurrentConfig()
	gameInterval := cfg.Observation.ServerDocumentInterval.Duration
	if gameInterval <= 0 {
		gameInterval = 5 * time.Minute
	}
	// First game-data sample soon after start (10s) so POIs appear without long wait.
	gameTicker := time.NewTicker(10 * time.Second)
	defer gameTicker.Stop()
	var saveTicker *time.Ticker
	var saveC <-chan time.Time
	if a.saveImporter != nil && cfg.Save.ImportInterval.Duration > 0 {
		saveTicker = time.NewTicker(cfg.Save.ImportInterval.Duration)
		defer saveTicker.Stop()
		saveC = saveTicker.C
		// Kick an import shortly after boot.
		go a.runSaveImportOnce(ctx)
	}
	firstGame := true
	for {
		select {
		case <-ctx.Done():
			return
		case <-gameTicker.C:
			if firstGame {
				firstGame = false
				gameTicker.Reset(gameInterval)
			}
			a.sampleGameDataOnce(ctx)
		case <-saveC:
			a.runSaveImportOnce(ctx)
		}
	}
}

func (a *App) sampleGameDataOnce(ctx context.Context) {
	if a.client == nil || a.liveGameData == nil {
		return
	}
	timeout := a.CurrentConfig().Server.RequestTimeout.Duration
	if timeout < 15*time.Second {
		timeout = 15 * time.Second
	}
	sampleCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	observation.SampleGameData(sampleCtx, a.client, a.liveGameData, slog.Default())
}

func (a *App) runSaveImportOnce(ctx context.Context) {
	if a.saveImporter == nil {
		return
	}
	cfg := a.CurrentConfig()
	if !cfg.Save.Enabled || strings.TrimSpace(cfg.Save.Path) == "" {
		return
	}
	timeout := cfg.Save.WorkerTimeout.Duration
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	// Allow worker wall time slightly beyond configured timeout.
	importCtx, cancel := context.WithTimeout(ctx, timeout+15*time.Second)
	defer cancel()
	result, err := a.saveImporter.Import(importCtx, cfg.Save.Path)
	if err != nil {
		slog.Warn("auto save import failed", "err", err, "path", cfg.Save.Path)
		return
	}
	slog.Info("auto save import ok", "import_id", result.ImportID, "inserted", result.Inserted, "fingerprint", result.Fingerprint)
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
	if !reflect.DeepEqual(current.Server, next.Server) || !reflect.DeepEqual(current.HTTP, next.HTTP) || !reflect.DeepEqual(current.Storage, next.Storage) || !reflect.DeepEqual(current.Observation, next.Observation) ||
		current.Enforcement.KickRetryInitial != next.Enforcement.KickRetryInitial || current.Enforcement.KickRetryMax != next.Enforcement.KickRetryMax {
		return a.reloadError(fmt.Errorf("server, HTTP, storage, observation, and retry settings require a restart"))
	}
	if err := a.poller.ApplyConfig(func() error { return nil }, next.Enforcement.AnnounceMessage, next.Enforcement.KickMessage, next.Enforcement.LoginMessage); err != nil {
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
