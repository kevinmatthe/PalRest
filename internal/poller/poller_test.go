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
}

func (f *fakeGuard) Observe(context.Context, time.Time, []domain.Player) (guard.Decisions, error) {
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

func TestRunOncePerformsAndRecordsDecisions(t *testing.T) {
	now := time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC)
	client := &fakeClient{players: []domain.Player{{UserID: "steam_1", Name: "Kevin"}}}
	guardService := &fakeGuard{decisions: guard.Decisions{
		Warnings: []guard.WarningDecision{{UserID: "steam_1", PlayerName: "Kevin", Remaining: 5 * time.Minute}},
		Kicks:    []guard.KickDecision{{UserID: "steam_2", PlayerName: "Matt", ResetAt: now.Add(time.Hour)}},
	}}
	p, err := New(client, guardService, time.Minute, "{{ .PlayerName }}: {{ .Remaining }}", "reset {{ .ResetAt }}", func() time.Time { return now })
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
	status := p.Status()
	if status.OnlineCount != 1 || !status.LastSuccess.Equal(now) {
		t.Fatalf("status=%+v", status)
	}
}

func TestListFailureBreaksContinuityAndHasNoSideEffects(t *testing.T) {
	client := &fakeClient{listErr: errors.New("offline")}
	guardService := &fakeGuard{decisions: guard.Decisions{Kicks: []guard.KickDecision{{UserID: "steam_1"}}}}
	p, _ := New(client, guardService, time.Minute, "warning", "kick", time.Now)
	if err := p.RunOnce(t.Context()); err == nil {
		t.Fatal("expected error")
	}
	if guardService.failed != 1 || len(client.kicks) != 0 || p.Status().LastError == "" {
		t.Fatalf("failed=%d kicks=%v status=%+v", guardService.failed, client.kicks, p.Status())
	}
}

func TestObservationFailureHasNoSideEffects(t *testing.T) {
	client := &fakeClient{}
	guardService := &fakeGuard{observeErr: errors.New("sqlite unavailable"), decisions: guard.Decisions{Kicks: []guard.KickDecision{{UserID: "steam_1"}}}}
	p, _ := New(client, guardService, time.Minute, "warning", "kick", time.Now)
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
	p, _ := New(client, guardService, 5*time.Millisecond, "warning", "kick", time.Now)
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
