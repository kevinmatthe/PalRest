package observation_test

import (
	"strings"
	"testing"
	"time"

	"github.com/kevinmatt/palworld-playtime-guard/internal/domain"
	"github.com/kevinmatt/palworld-playtime-guard/internal/observation"
)

func TestServerDocumentCASRetryRederivesChangeAgainstWinningWriter(t *testing.T) {
	repo, _ := openServerRepository(t)
	defer repo.Close()
	base := time.Date(2026, 7, 13, 0, 0, 0, 0, time.UTC)
	seed := observation.NewServer(repo, func() string { return "seed" })
	if err := seed.RecordSettings(t.Context(), base, domain.ServerSettings{Values: map[string]any{"value": "A"}}); err != nil {
		t.Fatal(err)
	}
	winner := observation.NewServer(repo, func() string { return "winner-settings" })
	interleaved := &interleavingRepository{delegate: repo}
	interleaved.hook = func() {
		if err := winner.RecordSettings(t.Context(), base.Add(time.Minute), domain.ServerSettings{Values: map[string]any{"value": "B"}}); err != nil {
			t.Errorf("winning writer: %v", err)
		}
	}
	loser := observation.NewServer(interleaved, func() string { return "retried-settings" })
	if err := loser.RecordSettings(t.Context(), base.Add(2*time.Minute), domain.ServerSettings{Values: map[string]any{"value": "C"}}); err != nil {
		t.Fatal(err)
	}
	if len(interleaved.documents) != 2 || interleaved.documents[0].Expected == nil || interleaved.documents[1].Expected == nil {
		t.Fatalf("writes=%#v", interleaved.documents)
	}
	winnerSnapshot, err := repo.LatestServerDocument(t.Context(), "settings")
	if err != nil {
		t.Fatal(err)
	}
	if interleaved.documents[1].Event == nil || !strings.Contains(interleaved.documents[1].Event.PayloadJSON, `"old_hash":"`+interleaved.documents[1].Expected.Hash+`"`) {
		t.Fatalf("retried event=%#v expected=%#v latest=%#v", interleaved.documents[1].Event, interleaved.documents[1].Expected, winnerSnapshot)
	}
}
