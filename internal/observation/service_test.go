package observation_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kevinmatt/palworld-playtime-guard/internal/domain"
	"github.com/kevinmatt/palworld-playtime-guard/internal/observation"
	"github.com/kevinmatt/palworld-playtime-guard/internal/store"
)

type recorderFake struct {
	mu           sync.Mutex
	writes       []store.PlayerObservationWrite
	attempts     []store.PlayerObservationWrite
	recordErr    error
	cleanupCalls []cleanupCall
	cleanupErr   error
}

type cleanupCall struct {
	cutoff time.Time
	limit  int
}

func (r *recorderFake) RecordPlayerObservation(_ context.Context, write store.PlayerObservationWrite) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.attempts = append(r.attempts, write)
	if r.recordErr != nil {
		return r.recordErr
	}
	r.writes = append(r.writes, write)
	return nil
}

func (r *recorderFake) CleanupRawObservations(_ context.Context, cutoff time.Time, limit int) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cleanupCalls = append(r.cleanupCalls, cleanupCall{cutoff: cutoff, limit: limit})
	return 0, r.cleanupErr
}

func newService(recorder *recorderFake) *observation.Service {
	sequence := 0
	return observation.New(recorder, 75*time.Second, 100, 5*time.Minute, 90*24*time.Hour, func() string {
		sequence++
		return fmt.Sprintf("id-%d", sequence)
	})
}

func player(id string, x, y float64) domain.Player {
	return domain.Player{
		UserID: id, PlayerID: "pal-" + id, Name: "name-" + id,
		AccountName: "account-" + id, IP: "192.0.2.10", Ping: 28.5,
		LocationX: x, LocationY: y, Level: 41, BuildingCount: 12,
	}
}

func TestFirstObservationCreatesJoinedEventAndTrajectory(t *testing.T) {
	recorder := &recorderFake{}
	svc := newService(recorder)
	at := time.Date(2026, 7, 13, 10, 0, 0, 0, time.FixedZone("CST", 8*60*60))

	if err := svc.Observe(t.Context(), at, []domain.Player{player("b", 10, 20), player("a", 30, 40)}, "poll-1"); err != nil {
		t.Fatal(err)
	}
	if len(recorder.writes) != 1 {
		t.Fatalf("writes=%d", len(recorder.writes))
	}
	write := recorder.writes[0]
	if len(write.Events) != 2 || len(write.Trajectories) != 2 {
		t.Fatalf("write=%+v", write)
	}
	for i, id := range []string{"a", "b"} {
		event := write.Events[i]
		if event.EventType != "player_joined" || event.SubjectType != "player" || event.SubjectID != id || event.OccurredAt != at.UTC() || event.ObservedAt != at.UTC() {
			t.Fatalf("event[%d]=%+v", i, event)
		}
		if event.Source != "palworld_rest" || event.SourceRef != "poll-1" || event.CorrelationID != "poll-1" || event.Confidence != "observed" || event.SchemaVersion != 1 {
			t.Fatalf("event metadata=%+v", event)
		}
		var payload map[string]any
		if err := json.Unmarshal([]byte(event.PayloadJSON), &payload); err != nil || payload["name"] != "name-"+id || strings.Contains(event.PayloadJSON, "192.0.2.10") {
			t.Fatalf("payload=%q err=%v", event.PayloadJSON, err)
		}
		point := write.Trajectories[i]
		if point.UserID != id || point.SegmentID == "" || point.ObservedAt != at.UTC() || point.SourceRef != "poll-1" {
			t.Fatalf("point[%d]=%+v", i, point)
		}
	}
	if write.Events[0].ID == write.Events[1].ID || write.Trajectories[0].SegmentID == write.Trajectories[1].SegmentID {
		t.Fatal("generated IDs must be unique")
	}
}

func TestPrivateSamplesTrackSensitiveChangesWithoutPingChurn(t *testing.T) {
	recorder := &recorderFake{}
	svc := newService(recorder)
	base := time.Date(2026, 7, 13, 0, 0, 0, 0, time.UTC)
	p := player("u", 10, 10)
	observations := []domain.Player{p, p, p, p}
	observations[1].Ping = 99
	observations[2].Ping = 100
	observations[2].IP = "[2001:db8::1]:8211"
	observations[3].Ping = 101
	observations[3].IP = observations[2].IP
	for i, value := range observations {
		if err := svc.Observe(t.Context(), base.Add(time.Duration(i)*time.Minute), []domain.Player{value}, fmt.Sprintf("poll-%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	if got := len(recorder.writes[0].PrivateSamples); got != 1 {
		t.Fatalf("first private samples=%d", got)
	}
	if got := len(recorder.writes[1].PrivateSamples); got != 0 {
		t.Fatalf("ping-only private samples=%d", got)
	}
	if got := recorder.writes[2].PrivateSamples; len(got) != 1 || got[0].IP != "[2001:db8::1]:8211" || got[0].Ping != 100 {
		t.Fatalf("changed private sample=%+v", got)
	}
	if got := len(recorder.writes[3].PrivateSamples); got != 0 {
		t.Fatalf("second ping-only private samples=%d", got)
	}
}

func TestConsecutiveObservationsDeriveKnownPlayerAttributeChanges(t *testing.T) {
	recorder := &recorderFake{}
	svc := newService(recorder)
	base := time.Date(2026, 7, 13, 0, 0, 0, 0, time.UTC)
	before := player("u", 10, 10)
	after := before
	after.PlayerID = "pal-u-2"
	after.Name = "renamed"
	after.Level = 42
	after.BuildingCount = 13
	after.IP = "198.51.100.4"
	after.Ping = 999

	if err := svc.Observe(t.Context(), base, []domain.Player{before}, "poll-1"); err != nil {
		t.Fatal(err)
	}
	if err := svc.Observe(t.Context(), base.Add(time.Minute), []domain.Player{after}, "poll-2"); err != nil {
		t.Fatal(err)
	}

	events := recorder.writes[1].Events
	if len(events) != 1 || events[0].EventType != "player_attribute_changed" || events[0].SubjectID != "u" {
		t.Fatalf("events=%+v", events)
	}
	var payload struct {
		Changes map[string]struct {
			Old any `json:"old"`
			New any `json:"new"`
		} `json:"changes"`
	}
	if err := json.Unmarshal([]byte(events[0].PayloadJSON), &payload); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"player_id", "name", "level", "building_count"} {
		if _, ok := payload.Changes[field]; !ok {
			t.Errorf("missing change %q in %s", field, events[0].PayloadJSON)
		}
	}
	if strings.Contains(events[0].PayloadJSON, before.IP) || strings.Contains(events[0].PayloadJSON, after.IP) || strings.Contains(events[0].PayloadJSON, "999") || len(payload.Changes) != 4 {
		t.Fatalf("unsafe or unexpected payload=%s", events[0].PayloadJSON)
	}
}

func TestAttributeChangesRequireContinuousKnownPriorObservation(t *testing.T) {
	recorder := &recorderFake{}
	svc := newService(recorder)
	base := time.Date(2026, 7, 13, 0, 0, 0, 0, time.UTC)
	initial := player("u", 10, 10)
	initial.Name = ""
	if err := svc.Observe(t.Context(), base, []domain.Player{initial}, "poll-1"); err != nil {
		t.Fatal(err)
	}
	known := initial
	known.Name = "known"
	if err := svc.Observe(t.Context(), base.Add(time.Minute), []domain.Player{known}, "poll-2"); err != nil {
		t.Fatal(err)
	}
	if got := recorder.writes[1].Events; len(got) != 0 {
		t.Fatalf("unknown-to-known change=%+v", got)
	}
	changed := known
	changed.Level++
	if err := svc.Observe(t.Context(), base.Add(3*time.Minute), []domain.Player{changed}, "poll-3"); err != nil {
		t.Fatal(err)
	}
	if got := recorder.writes[2].Events; len(got) != 0 {
		t.Fatalf("post-gap events=%+v", got)
	}
}

func TestExplicitPollFailureBreaksAttributeAndTrajectoryContinuityWithinMaxGap(t *testing.T) {
	recorder := &recorderFake{}
	svc := newService(recorder)
	base := time.Date(2026, 7, 13, 0, 0, 0, 0, time.UTC)
	before := player("u", 10, 10)
	if err := svc.Observe(t.Context(), base, []domain.Player{before}, "poll-1"); err != nil {
		t.Fatal(err)
	}
	svc.PollFailed()
	after := before
	after.Level++
	after.LocationX = 11
	if err := svc.Observe(t.Context(), base.Add(time.Minute), []domain.Player{after}, "poll-3"); err != nil {
		t.Fatal(err)
	}
	if got := recorder.writes[1].Events; len(got) != 0 {
		t.Fatalf("events across failed poll=%+v", got)
	}
	if got := recorder.writes[1].Trajectories; len(got) != 1 || got[0].SegmentID == recorder.writes[0].Trajectories[0].SegmentID {
		t.Fatalf("trajectory continuity was not broken: %+v", got)
	}
}

func TestAttributeChangeEventIDIsStableAcrossReplay(t *testing.T) {
	base := time.Date(2026, 7, 13, 0, 0, 0, 0, time.UTC)
	derive := func(prefix string) store.ActivityEvent {
		recorder := &recorderFake{}
		sequence := 0
		svc := observation.New(recorder, 75*time.Second, 100, 5*time.Minute, 90*24*time.Hour, func() string {
			sequence++
			return fmt.Sprintf("%s-%d", prefix, sequence)
		})
		before := player("u", 0, 0)
		before.IP = ""
		after := before
		after.Level++
		if err := svc.Observe(t.Context(), base, []domain.Player{before}, "poll-1"); err != nil {
			t.Fatal(err)
		}
		if err := svc.Observe(t.Context(), base.Add(time.Minute), []domain.Player{after}, "poll-2"); err != nil {
			t.Fatal(err)
		}
		return recorder.writes[1].Events[0]
	}
	first, replay := derive("first"), derive("replay")
	if first.ID != replay.ID || first.PayloadJSON != replay.PayloadJSON {
		t.Fatalf("first=%+v replay=%+v", first, replay)
	}
}

func TestMissingIPSkipsPrivateSampleWithoutBlockingObservationOrBaseline(t *testing.T) {
	recorder := &recorderFake{}
	svc := newService(recorder)
	base := time.Date(2026, 7, 13, 0, 0, 0, 0, time.UTC)
	missing := player("u", 10, 10)
	missing.IP = ""
	if err := svc.Observe(t.Context(), base, []domain.Player{missing}, "poll-1"); err != nil {
		t.Fatal(err)
	}
	if len(recorder.writes) != 1 || len(recorder.writes[0].Events) != 1 || len(recorder.writes[0].Trajectories) != 1 || len(recorder.writes[0].PrivateSamples) != 0 {
		t.Fatalf("write=%+v", recorder.writes)
	}
	available := missing
	available.IP = "192.0.2.99:8211"
	if err := svc.Observe(t.Context(), base.Add(time.Minute), []domain.Player{available}, "poll-2"); err != nil {
		t.Fatal(err)
	}
	if got := recorder.writes[1].PrivateSamples; len(got) != 1 || got[0].IP != available.IP {
		t.Fatalf("private=%+v", got)
	}
	disappeared := available
	disappeared.IP = ""
	if err := svc.Observe(t.Context(), base.Add(2*time.Minute), []domain.Player{disappeared}, "poll-3"); err != nil {
		t.Fatal(err)
	}
	if got := recorder.writes[2].PrivateSamples; len(got) != 0 {
		t.Fatalf("missing IP erased history: %+v", got)
	}
}

func TestMovementThresholdBoundary(t *testing.T) {
	recorder := &recorderFake{}
	svc := newService(recorder)
	base := time.Date(2026, 7, 13, 0, 0, 0, 0, time.UTC)
	observations := []struct {
		at   time.Time
		x, y float64
		want int
	}{
		{base, 10, 10, 1},
		{base.Add(time.Minute), 109.999, 10, 0},
		{base.Add(2 * time.Minute), 110, 10, 0},
		{base.Add(3 * time.Minute), 110.001, 10, 1},
	}
	for i, tc := range observations {
		if err := svc.Observe(t.Context(), tc.at, []domain.Player{player("u", tc.x, tc.y)}, fmt.Sprintf("poll-%d", i)); err != nil {
			t.Fatal(err)
		}
		if got := len(recorder.writes[i].Trajectories); got != tc.want {
			t.Fatalf("observation %d trajectories=%d, want %d", i, got, tc.want)
		}
	}
	firstSegment := recorder.writes[0].Trajectories[0].SegmentID
	if recorder.writes[3].Trajectories[0].SegmentID != firstSegment {
		t.Fatal("ordinary sampling should preserve segment")
	}
}

func TestHeartbeatSamplesAtMaximumInterval(t *testing.T) {
	recorder := &recorderFake{}
	svc := newService(recorder)
	base := time.Date(2026, 7, 13, 0, 0, 0, 0, time.UTC)
	for minute := 0; minute <= 5; minute++ {
		if err := svc.Observe(t.Context(), base.Add(time.Duration(minute)*time.Minute), []domain.Player{player("u", 10, 10)}, fmt.Sprintf("poll-%d", minute)); err != nil {
			t.Fatal(err)
		}
		want := 0
		if minute == 0 || minute == 5 {
			want = 1
		}
		if got := len(recorder.writes[minute].Trajectories); got != want {
			t.Fatalf("minute %d trajectories=%d, want %d", minute, got, want)
		}
	}
}

func TestExcessiveGapStartsNewSegmentWithAnchorPoint(t *testing.T) {
	recorder := &recorderFake{}
	svc := newService(recorder)
	base := time.Date(2026, 7, 13, 0, 0, 0, 0, time.UTC)
	if err := svc.Observe(t.Context(), base, []domain.Player{player("u", 10, 10)}, "poll-1"); err != nil {
		t.Fatal(err)
	}
	if err := svc.Observe(t.Context(), base.Add(76*time.Second), []domain.Player{player("u", 11, 10)}, "poll-2"); err != nil {
		t.Fatal(err)
	}
	first := recorder.writes[0].Trajectories[0]
	second := recorder.writes[1].Trajectories[0]
	if first.SegmentID == second.SegmentID || second.X != 11 {
		t.Fatalf("first=%+v second=%+v", first, second)
	}
}

func TestMaxGapEqualityPreservesSegment(t *testing.T) {
	recorder := &recorderFake{}
	svc := newService(recorder)
	base := time.Date(2026, 7, 13, 0, 0, 0, 0, time.UTC)
	if err := svc.Observe(t.Context(), base, []domain.Player{player("u", 10, 10)}, "poll-1"); err != nil {
		t.Fatal(err)
	}
	if err := svc.Observe(t.Context(), base.Add(75*time.Second), []domain.Player{player("u", 111, 10)}, "poll-2"); err != nil {
		t.Fatal(err)
	}
	first := recorder.writes[0].Trajectories[0]
	second := recorder.writes[1].Trajectories[0]
	if first.SegmentID != second.SegmentID {
		t.Fatalf("segment changed at max-gap equality: first=%+v second=%+v", first, second)
	}
}

func TestJoinLeaveAndRejoinProducesEventsAndNewSegment(t *testing.T) {
	recorder := &recorderFake{}
	svc := newService(recorder)
	base := time.Date(2026, 7, 13, 0, 0, 0, 0, time.UTC)
	if err := svc.Observe(t.Context(), base, []domain.Player{player("u", 10, 10)}, "one"); err != nil {
		t.Fatal(err)
	}
	if err := svc.Observe(t.Context(), base.Add(time.Minute), nil, "two"); err != nil {
		t.Fatal(err)
	}
	if err := svc.Observe(t.Context(), base.Add(2*time.Minute), []domain.Player{player("u", 10, 10)}, "three"); err != nil {
		t.Fatal(err)
	}
	if got := recorder.writes[1].Events; len(got) != 1 || got[0].EventType != "player_left" || got[0].SubjectID != "u" {
		t.Fatalf("left=%+v", got)
	}
	if got := recorder.writes[2].Events; len(got) != 1 || got[0].EventType != "player_joined" {
		t.Fatalf("rejoin=%+v", got)
	}
	if recorder.writes[0].Trajectories[0].SegmentID == recorder.writes[2].Trajectories[0].SegmentID {
		t.Fatal("rejoin reused trajectory segment")
	}
}

func TestInvalidCoordinatesBreakPositionalContinuity(t *testing.T) {
	invalid := [][2]float64{{0, 0}, {math.NaN(), 1}, {1, math.Inf(1)}}
	for i, coords := range invalid {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			recorder := &recorderFake{}
			svc := newService(recorder)
			base := time.Date(2026, 7, 13, 0, 0, 0, 0, time.UTC)
			if err := svc.Observe(t.Context(), base, []domain.Player{player("u", 10, 10)}, "one"); err != nil {
				t.Fatal(err)
			}
			if err := svc.Observe(t.Context(), base.Add(time.Minute), []domain.Player{player("u", coords[0], coords[1])}, "two"); err != nil {
				t.Fatal(err)
			}
			if err := svc.Observe(t.Context(), base.Add(2*time.Minute), []domain.Player{player("u", 11, 10)}, "three"); err != nil {
				t.Fatal(err)
			}
			if len(recorder.writes[1].Trajectories) != 0 || len(recorder.writes[2].Trajectories) != 1 {
				t.Fatalf("writes=%+v", recorder.writes)
			}
			if recorder.writes[0].Trajectories[0].SegmentID == recorder.writes[2].Trajectories[0].SegmentID {
				t.Fatal("valid point after invalid coordinates continued old segment")
			}
		})
	}
}

func TestCoordinateValidity(t *testing.T) {
	tests := map[string]struct {
		x, y float64
		want bool
	}{
		"nan x":               {math.NaN(), 1, false},
		"nan y":               {1, math.NaN(), false},
		"positive infinity x": {math.Inf(1), 1, false},
		"negative infinity x": {math.Inf(-1), 1, false},
		"positive infinity y": {1, math.Inf(1), false},
		"negative infinity y": {1, math.Inf(-1), false},
		"zero x":              {0, 1, true},
		"zero y":              {1, 0, true},
		"origin":              {0, 0, false},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			recorder := &recorderFake{}
			svc := newService(recorder)
			at := time.Date(2026, 7, 13, 0, 0, 0, 0, time.UTC)
			if err := svc.Observe(t.Context(), at, []domain.Player{player("u", tc.x, tc.y)}, "poll"); err != nil {
				t.Fatal(err)
			}
			if got := len(recorder.writes[0].Trajectories) == 1; got != tc.want {
				t.Fatalf("trajectory present=%v, want %v", got, tc.want)
			}
		})
	}
}

func TestObserveRejectsInvalidInputWithoutRecording(t *testing.T) {
	tests := map[string]struct {
		at          time.Time
		players     []domain.Player
		correlation string
	}{
		"zero time":         {players: []domain.Player{player("u", 1, 1)}, correlation: "poll"},
		"empty correlation": {at: time.Now(), players: []domain.Player{player("u", 1, 1)}},
		"empty user":        {at: time.Now(), players: []domain.Player{player(" ", 1, 1)}, correlation: "poll"},
		"duplicate user":    {at: time.Now(), players: []domain.Player{player("u", 1, 1), player(" u ", 2, 2)}, correlation: "poll"},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			recorder := &recorderFake{}
			if err := newService(recorder).Observe(t.Context(), tc.at, tc.players, tc.correlation); err == nil {
				t.Fatal("expected error")
			}
			if len(recorder.writes) != 0 {
				t.Fatal("invalid observation was recorded")
			}
		})
	}
}

func TestRecorderFailureDoesNotAdvanceBaseline(t *testing.T) {
	recorder := &recorderFake{recordErr: errors.New("disk full")}
	svc := newService(recorder)
	at := time.Date(2026, 7, 13, 0, 0, 0, 0, time.UTC)
	if err := svc.Observe(t.Context(), at, []domain.Player{player("u", 10, 10)}, "poll"); !errors.Is(err, recorder.recordErr) {
		t.Fatalf("err=%v", err)
	}
	recorder.recordErr = nil
	if err := svc.Observe(t.Context(), at, []domain.Player{player("u", 10, 10)}, "poll"); err != nil {
		t.Fatal(err)
	}
	if len(recorder.writes) != 1 || len(recorder.writes[0].Events) != 1 || recorder.writes[0].Events[0].EventType != "player_joined" || len(recorder.writes[0].Trajectories) != 1 {
		t.Fatalf("retry write=%+v", recorder.writes)
	}
	if len(recorder.attempts) != 2 {
		t.Fatalf("attempts=%d", len(recorder.attempts))
	}
	failed, retry := recorder.attempts[0], recorder.attempts[1]
	if len(failed.Events) != 1 || len(retry.Events) != 1 ||
		failed.Events[0].EventType != retry.Events[0].EventType ||
		failed.Events[0].SubjectID != retry.Events[0].SubjectID ||
		len(failed.Trajectories) != 1 || len(retry.Trajectories) != 1 ||
		failed.Trajectories[0].UserID != retry.Trajectories[0].UserID ||
		failed.Trajectories[0].X != retry.Trajectories[0].X ||
		failed.Trajectories[0].Y != retry.Trajectories[0].Y {
		t.Fatalf("failed=%+v retry=%+v", failed, retry)
	}
	if failed.Events[0].ID == retry.Events[0].ID || failed.Trajectories[0].SegmentID == retry.Trajectories[0].SegmentID {
		t.Fatal("retry should use fresh attempt IDs while preserving semantics")
	}
}

func TestCleanupCadenceAndFailureAreNonfatal(t *testing.T) {
	recorder := &recorderFake{cleanupErr: errors.New("busy")}
	svc := newService(recorder)
	base := time.Date(2026, 7, 13, 0, 0, 0, 0, time.UTC)
	for i, offset := range []time.Duration{0, 23 * time.Hour, 24 * time.Hour} {
		if err := svc.Observe(t.Context(), base.Add(offset), nil, fmt.Sprintf("poll-%d", i)); err != nil {
			t.Fatalf("observation %d: %v", i, err)
		}
	}
	want := []cleanupCall{{cutoff: base.Add(-90 * 24 * time.Hour), limit: 500}, {cutoff: base.Add(24 * time.Hour).Add(-90 * 24 * time.Hour), limit: 500}}
	if !reflect.DeepEqual(recorder.cleanupCalls, want) {
		t.Fatalf("cleanup=%+v want %+v", recorder.cleanupCalls, want)
	}
}

func TestNewRejectsInvalidDependenciesAndThresholds(t *testing.T) {
	recorder := &recorderFake{}
	id := func() string { return "id" }
	tests := map[string]func(){
		"nil recorder":        func() { observation.New(nil, time.Second, 1, time.Second, time.Hour, id) },
		"bad max gap":         func() { observation.New(recorder, 0, 1, time.Second, time.Hour, id) },
		"bad movement":        func() { observation.New(recorder, time.Second, -1, time.Second, time.Hour, id) },
		"bad sample interval": func() { observation.New(recorder, time.Second, 1, 0, time.Hour, id) },
		"bad retention":       func() { observation.New(recorder, time.Second, 1, time.Second, 0, id) },
		"nil ID generator":    func() { observation.New(recorder, time.Second, 1, time.Second, time.Hour, nil) },
	}
	for name, fn := range tests {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatal("expected panic")
				}
			}()
			fn()
		})
	}
}

func TestZeroMovementThresholdSamplesEveryChangedPosition(t *testing.T) {
	recorder := &recorderFake{}
	svc := observation.New(recorder, time.Minute, 0, time.Hour, time.Hour, func() string { return "id" })
	at := time.Date(2026, 7, 13, 0, 0, 0, 0, time.UTC)
	if err := svc.Observe(t.Context(), at, []domain.Player{player("u", 1, 1)}, "poll-1"); err != nil {
		t.Fatal(err)
	}
	if err := svc.Observe(t.Context(), at.Add(time.Second), []domain.Player{player("u", 1.1, 1)}, "poll-2"); err != nil {
		t.Fatal(err)
	}
	if len(recorder.writes) != 2 || len(recorder.writes[1].Trajectories) != 1 {
		t.Fatalf("writes=%+v", recorder.writes)
	}
}
