package poller

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kevinmatt/palworld-playtime-guard/internal/domain"
	"github.com/kevinmatt/palworld-playtime-guard/internal/guard"
)

type fakeClient struct {
	players   []domain.Player
	listErr   error
	announces []string
	kicks     []string
}

func (f *fakeClient) ListPlayers(context.Context) ([]domain.Player, error) {
	return f.players, f.listErr
}
func (f *fakeClient) Announce(_ context.Context, message string) error {
	f.announces = append(f.announces, message)
	return nil
}
func (f *fakeClient) Kick(_ context.Context, userID, message string) error {
	f.kicks = append(f.kicks, userID+":"+message)
	return nil
}

type fakeGuard struct {
	decisions  guard.Decisions
	observeErr error
	failed     int
	warnings   int
	kicks      int
	observed   int
	order      *[]string
}

func (f *fakeGuard) Observe(context.Context, time.Time, []domain.Player) (guard.Decisions, error) {
	f.observed++
	if f.order != nil {
		*f.order = append(*f.order, "guard")
	}
	return f.decisions, f.observeErr
}
func (f *fakeGuard) PollFailed() { f.failed++ }
func (f *fakeGuard) RecordWarningResult(context.Context, guard.WarningDecision, error, time.Time) error {
	f.warnings++
	return nil
}
func (f *fakeGuard) RecordKickResult(context.Context, guard.KickDecision, error, time.Time) error {
	f.kicks++
	return nil
}

type fakeAnalytics struct {
	err      error
	observed int
	order    *[]string
}

func (f *fakeAnalytics) Observe(context.Context, time.Time, []domain.Player) error {
	f.observed++
	if f.order != nil {
		*f.order = append(*f.order, "analytics")
	}
	return f.err
}

func TestRunOncePerformsAndRecordsDecisions(t *testing.T) {
	now := time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC)
	client := &fakeClient{players: []domain.Player{{UserID: "steam_1", Name: "Kevin"}}}
	order := []string{}
	analytics := &fakeAnalytics{order: &order}
	guardService := &fakeGuard{decisions: guard.Decisions{
		Warnings: []guard.WarningDecision{{UserID: "steam_1", PlayerName: "Kevin", Remaining: 5 * time.Minute}},
		Kicks:    []guard.KickDecision{{UserID: "steam_2", PlayerName: "Matt", ResetAt: now.Add(time.Hour)}},
	}, order: &order}
	p, err := New(client, guardService, analytics, time.Minute, "{{ .PlayerName }}: {{ .Remaining }}", "reset {{ .ResetAt }}", func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	if err := p.RunOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(client.announces) != 1 || !strings.Contains(client.announces[0], "Kevin") || len(client.kicks) != 1 {
		t.Fatalf("announces=%v kicks=%v", client.announces, client.kicks)
	}
	if guardService.warnings != 1 || guardService.kicks != 1 {
		t.Fatalf("recorded warnings=%d kicks=%d", guardService.warnings, guardService.kicks)
	}
	if strings.Join(order, ",") != "analytics,guard" {
		t.Fatalf("observation order=%v", order)
	}
	status := p.Status()
	if status.OnlineCount != 1 || !status.LastSuccess.Equal(now) {
		t.Fatalf("status=%+v", status)
	}
}

func TestListFailureBreaksContinuityAndHasNoSideEffects(t *testing.T) {
	client := &fakeClient{listErr: errors.New("offline")}
	analytics := &fakeAnalytics{}
	guardService := &fakeGuard{decisions: guard.Decisions{Kicks: []guard.KickDecision{{UserID: "steam_1"}}}}
	p, _ := New(client, guardService, analytics, time.Minute, "warning", "kick", time.Now)
	if err := p.RunOnce(t.Context()); err == nil {
		t.Fatal("expected error")
	}
	if guardService.failed != 1 || len(client.kicks) != 0 || p.Status().LastError == "" {
		t.Fatalf("failed=%d kicks=%v status=%+v", guardService.failed, client.kicks, p.Status())
	}
	if analytics.observed != 0 || guardService.observed != 0 {
		t.Fatalf("analytics observed=%d guard observed=%d", analytics.observed, guardService.observed)
	}
}

func TestAnalyticsFailureBreaksContinuityAndSkipsGuard(t *testing.T) {
	now := time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC)
	client := &fakeClient{players: []domain.Player{{UserID: "steam_1", Name: "Kevin"}}}
	analytics := &fakeAnalytics{err: errors.New("analytics unavailable")}
	guardService := &fakeGuard{decisions: guard.Decisions{Kicks: []guard.KickDecision{{UserID: "steam_1"}}}}
	p, _ := New(client, guardService, analytics, time.Minute, "warning", "kick", func() time.Time { return now })

	if err := p.RunOnce(t.Context()); !errors.Is(err, analytics.err) {
		t.Fatalf("error=%v", err)
	}
	status := p.Status()
	if analytics.observed != 1 || guardService.observed != 0 || guardService.failed != 1 || len(client.kicks) != 0 {
		t.Fatalf("analytics=%d guard=%d failed=%d kicks=%v", analytics.observed, guardService.observed, guardService.failed, client.kicks)
	}
	if status.LastError == "" || !status.LastSuccess.IsZero() || status.OnlineCount != 0 {
		t.Fatalf("status=%+v", status)
	}
}

func TestObservationFailureHasNoSideEffects(t *testing.T) {
	client := &fakeClient{}
	guardService := &fakeGuard{observeErr: errors.New("sqlite unavailable"), decisions: guard.Decisions{Kicks: []guard.KickDecision{{UserID: "steam_1"}}}}
	p, _ := New(client, guardService, &fakeAnalytics{}, time.Minute, "warning", "kick", time.Now)
	if err := p.RunOnce(t.Context()); err == nil {
		t.Fatal("expected error")
	}
	if len(client.kicks) != 0 {
		t.Fatalf("kicks=%v", client.kicks)
	}
}

func TestRunNeverOverlapsSlowCycles(t *testing.T) {
	client := &blockingClient{entered: make(chan struct{}, 2), release: make(chan struct{}, 2)}
	guardService := &fakeGuard{}
	p, _ := New(client, guardService, &fakeAnalytics{}, 5*time.Millisecond, "warning", "kick", time.Now)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); p.Run(ctx) }()
	<-client.entered
	time.Sleep(20 * time.Millisecond)
	client.mu.Lock()
	max := client.maxConcurrent
	client.mu.Unlock()
	if max != 1 {
		t.Fatalf("max concurrent=%d", max)
	}
	client.release <- struct{}{}
	cancel()
	<-done
}

func TestRunAllowsActiveCycleToFinishAfterStop(t *testing.T) {
	client := &blockingClient{entered: make(chan struct{}, 1), release: make(chan struct{}, 1)}
	p, _ := New(client, &fakeGuard{}, &fakeAnalytics{}, time.Minute, "warning", "kick", time.Now)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); p.Run(ctx) }()
	<-client.entered
	cancel()
	select {
	case <-done:
		t.Fatal("active cycle was canceled instead of finishing")
	case <-time.After(20 * time.Millisecond):
	}
	client.release <- struct{}{}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("poller did not stop after active cycle finished")
	}
}

func TestApplyConfigWaitsForActiveCycle(t *testing.T) {
	client := &blockingClient{entered: make(chan struct{}, 1), release: make(chan struct{}, 1)}
	p, _ := New(client, &fakeGuard{}, &fakeAnalytics{}, time.Minute, "warning", "kick", time.Now)
	cycleDone := make(chan error, 1)
	go func() { cycleDone <- p.RunOnce(context.Background()) }()
	<-client.entered
	applied := make(chan error, 1)
	go func() {
		applied <- p.ApplyConfig(func() error { return nil }, "new warning", "new kick")
	}()
	select {
	case <-applied:
		t.Fatal("configuration changed during active cycle")
	case <-time.After(20 * time.Millisecond):
	}
	client.release <- struct{}{}
	if err := <-cycleDone; err != nil {
		t.Fatal(err)
	}
	if err := <-applied; err != nil {
		t.Fatal(err)
	}
}

type blockingClient struct {
	mu            sync.Mutex
	concurrent    int
	maxConcurrent int
	entered       chan struct{}
	release       chan struct{}
}

func (b *blockingClient) ListPlayers(ctx context.Context) ([]domain.Player, error) {
	b.mu.Lock()
	b.concurrent++
	if b.concurrent > b.maxConcurrent {
		b.maxConcurrent = b.concurrent
	}
	b.mu.Unlock()
	b.entered <- struct{}{}
	select {
	case <-b.release:
	case <-ctx.Done():
	}
	b.mu.Lock()
	b.concurrent--
	b.mu.Unlock()
	return nil, ctx.Err()
}
func (*blockingClient) Announce(context.Context, string) error     { return nil }
func (*blockingClient) Kick(context.Context, string, string) error { return nil }
