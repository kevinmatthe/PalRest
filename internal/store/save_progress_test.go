package store

import (
	"fmt"
	"testing"
	"time"

	"github.com/kevinmatt/palworld-playtime-guard/internal/domain"
)

var progressStart = time.Date(2026, 7, 13, 8, 0, 0, 0, time.UTC)

func progressSnapshot(n int, owned ...string) SaveSnapshot {
	s := testSaveSnapshot()
	s.Parser.Version = 2
	s.Source.Fingerprint = fmt.Sprintf("%064x", n)
	s.Source.WorldID = "world-a"
	s.Source.WorldIDKind = "explicit"
	s.Source.SourceTime = progressStart.Add(time.Duration(n) * time.Minute).Format(time.RFC3339)
	s.Source.SourceTimeKind = "save_timestamp"
	s.Source.Consistent = true
	captures := int64(100 + n)
	count := int64(len(owned))
	s.Players[0].Progress = &SaveProgress{SchemaVersion: 1, Metrics: map[string]ProgressMetric{
		"owned_pals":    {State: "known", Value: &count, IDs: owned},
		"capture_total": {State: "known", Value: &captures},
		"paldeck":       {State: "unknown", Reason: "not_collected"},
		"fast_travel":   {State: "unsupported", Reason: "field_unverified"},
	}}
	return s
}

func progressPlayer(t *testing.T, r *Repository, id string) {
	t.Helper()
	if err := r.WithTx(t.Context(), func(tx *Tx) error {
		return tx.UpsertPlayer(domain.Player{UserID: id, PlayerID: testSaveSnapshot().Players[0].SavePlayerHex}, progressStart)
	}); err != nil {
		t.Fatal(err)
	}
}

func importProgress(t *testing.T, r *Repository, s SaveSnapshot) SaveImportResult {
	t.Helper()
	out, err := r.ImportSaveSnapshot(t.Context(), s, progressStart.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func queryProgress(t *testing.T, r *Repository) PlayerProgress {
	t.Helper()
	out, err := r.ReadPlayerProgress(t.Context(), "user", progressStart, progressStart.Add(time.Hour), 500)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestProgressBaselineDiffAndReplay(t *testing.T) {
	r, _ := openTemp(t)
	progressPlayer(t, r, "user")
	first := progressSnapshot(1, "a", "b")
	importProgress(t, r, first)
	if out := queryProgress(t, r); len(out.Changes) != 0 || len(out.Checkpoints) != 1 || out.Checkpoints[0].Boundary != "baseline" {
		t.Fatalf("baseline=%+v", out)
	}
	next := progressSnapshot(2, "b", "c", "d")
	inserted := importProgress(t, r, next)
	out := queryProgress(t, r)
	if len(out.Changes) != 2 {
		t.Fatalf("changes=%+v", out)
	}
	var owned ProgressChange
	for _, c := range out.Changes {
		if c.Metric == "owned_pals" {
			owned = c
		}
	}
	if owned.Before != 2 || owned.After != 3 || owned.Delta != 1 || fmt.Sprint(owned.Added) != "[c d]" || fmt.Sprint(owned.Removed) != "[a]" || owned.IntervalStart != out.Checkpoints[0].ObservedAt || owned.IntervalEnd != out.Checkpoints[1].ObservedAt {
		t.Fatalf("owned=%+v", owned)
	}
	repeated := importProgress(t, r, next)
	if repeated.Inserted || repeated.ImportID != inserted.ImportID || len(queryProgress(t, r).Changes) != 2 {
		t.Fatal("replay duplicated progress")
	}
}

func TestProgressBoundariesNeverInventChanges(t *testing.T) {
	cases := map[string]func(*SaveSnapshot){
		"world_changed":  func(s *SaveSnapshot) { s.Source.WorldID = "world-b" },
		"schema_changed": func(s *SaveSnapshot) { s.Players[0].Progress.SchemaVersion = 2 },
		"inconsistent":   func(s *SaveSnapshot) { s.Source.Consistent = false },
		"counter_reset": func(s *SaveSnapshot) {
			v := int64(1)
			m := s.Players[0].Progress.Metrics["capture_total"]
			m.Value = &v
			s.Players[0].Progress.Metrics["capture_total"] = m
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			r, _ := openTemp(t)
			progressPlayer(t, r, "user")
			importProgress(t, r, progressSnapshot(1, "a"))
			s := progressSnapshot(2, "a", "b")
			mutate(&s)
			importProgress(t, r, s)
			out := queryProgress(t, r)
			if len(out.Changes) != 0 || out.Checkpoints[1].Boundary != name {
				t.Fatalf("out=%+v", out)
			}
		})
	}
}

func TestProgressUnknownBreaksMetricBaselineAndDoesNotBecomeZero(t *testing.T) {
	r, _ := openTemp(t)
	progressPlayer(t, r, "user")
	importProgress(t, r, progressSnapshot(1, "a"))
	s := progressSnapshot(2)
	s.Players[0].Progress.Metrics["owned_pals"] = ProgressMetric{State: "unknown", Reason: "missing_file"}
	importProgress(t, r, s)
	importProgress(t, r, progressSnapshot(3, "a", "b"))
	out := queryProgress(t, r)
	for _, c := range out.Changes {
		if c.Metric == "owned_pals" {
			t.Fatalf("invented diff=%+v", c)
		}
	}
	if out.Checkpoints[1].Metrics["owned_pals"].Value != nil {
		t.Fatal("unknown became zero")
	}
}

func TestProgressOutOfOrderDoesNotRegressBaseline(t *testing.T) {
	r, _ := openTemp(t)
	progressPlayer(t, r, "user")
	importProgress(t, r, progressSnapshot(2, "a"))
	importProgress(t, r, progressSnapshot(1, "x", "y"))
	importProgress(t, r, progressSnapshot(3, "a", "b"))
	out := queryProgress(t, r)
	if len(out.Checkpoints) != 2 {
		t.Fatalf("out of order became visible=%+v", out)
	}
	if len(out.Changes) != 0 || out.Checkpoints[1].Boundary != "after_out_of_order" {
		t.Fatalf("bridged unknown order: %+v", out)
	}
	for _, c := range out.Changes {
		if c.Metric == "owned_pals" && (c.Before != 1 || c.After != 2) {
			t.Fatalf("regressed=%+v", c)
		}
	}
	var count int64
	r.gorm.Table("save_progress_checkpoints").Count(&count)
	if count != 3 {
		t.Fatal("out of order snapshot was not retained")
	}
}

func TestProgressInconsistentBreaksNextComparison(t *testing.T) {
	r, _ := openTemp(t)
	progressPlayer(t, r, "user")
	importProgress(t, r, progressSnapshot(1, "a"))
	s := progressSnapshot(2, "a", "b")
	s.Source.Consistent = false
	importProgress(t, r, s)
	importProgress(t, r, progressSnapshot(3, "c"))
	if out := queryProgress(t, r); len(out.Changes) != 0 || out.Checkpoints[2].Boundary != "after_inconsistent" {
		t.Fatalf("out=%+v", out)
	}
}

func TestProgressQueryBaselineAndExactIdentity(t *testing.T) {
	r, _ := openTemp(t)
	importProgress(t, r, progressSnapshot(1, "a"))
	progressPlayer(t, r, "user")
	importProgress(t, r, progressSnapshot(2, "a", "b"))
	out, err := r.ReadPlayerProgress(t.Context(), "user", progressStart.Add(90*time.Second), progressStart.Add(3*time.Minute), 1)
	if err != nil || out.Baseline == nil || len(out.Checkpoints) != 1 || out.ChangeTotal != 2 || len(out.Changes) != 1 {
		t.Fatalf("out=%+v err=%v", out, err)
	}
	progressPlayer(t, r, "ambiguous")
	if out := queryProgress(t, r); out.Status != "identity_unknown" || out.Baseline != nil || len(out.Checkpoints) != 0 {
		t.Fatalf("ambiguous leaked=%+v", out)
	}
}

func TestProgressValidationAndTransactionRollback(t *testing.T) {
	r, _ := openTemp(t)
	s := progressSnapshot(1, "a")
	m := s.Players[0].Progress.Metrics["owned_pals"]
	m.IDs = []string{"a", "a"}
	s.Players[0].Progress.Metrics["owned_pals"] = m
	if _, err := r.ImportSaveSnapshot(t.Context(), s, progressStart); err == nil {
		t.Fatal("accepted duplicate IDs")
	}
	var n int64
	r.gorm.Table("save_imports").Count(&n)
	if n != 0 {
		t.Fatal("invalid checkpoint partially imported")
	}
	if err := r.gorm.Exec("CREATE TRIGGER fail_progress BEFORE INSERT ON save_progress_changes BEGIN SELECT RAISE(ABORT, 'test failure'); END;").Error; err != nil {
		t.Fatal(err)
	}
	progressPlayer(t, r, "user")
	importProgress(t, r, progressSnapshot(1, "a"))
	if _, err := r.ImportSaveSnapshot(t.Context(), progressSnapshot(2, "b"), progressStart); err == nil {
		t.Fatal("expected change insertion failure")
	}
	r.gorm.Table("save_imports").Count(&n)
	if n != 1 {
		t.Fatalf("partial transaction committed: %d", n)
	}
}

func TestProgressUnlockRegressionAndEqualCountReplacement(t *testing.T) {
	for _, name := range []string{"paldeck", "fast_travel"} {
		t.Run(name, func(t *testing.T) {
			r, _ := openTemp(t)
			progressPlayer(t, r, "user")
			one := int64(1)
			a, b := progressSnapshot(1, "a"), progressSnapshot(2, "a", "b")
			a.Players[0].Progress.Metrics[name] = ProgressMetric{State: "known", Value: &one, IDs: []string{"old"}}
			b.Players[0].Progress.Metrics[name] = ProgressMetric{State: "known", Value: &one, IDs: []string{"new"}}
			importProgress(t, r, a)
			importProgress(t, r, b)
			out := queryProgress(t, r)
			if len(out.Changes) != 0 || out.Checkpoints[1].Boundary != "counter_reset" {
				t.Fatalf("out=%+v", out)
			}
		})
	}
}

func TestProgressUnknownWorldAndLegacyImportBreakComparison(t *testing.T) {
	r, _ := openTemp(t)
	progressPlayer(t, r, "user")
	a := progressSnapshot(1, "a")
	a.Source.WorldID = ""
	a.Source.WorldIDKind = "unknown"
	importProgress(t, r, a)
	b := progressSnapshot(2, "b")
	b.Source.WorldID = ""
	b.Source.WorldIDKind = "unknown"
	importProgress(t, r, b)
	if out := queryProgress(t, r); len(out.Changes) != 0 {
		t.Fatal("compared unknown world")
	}
	legacy := progressSnapshot(3)
	legacy.Players[0].Progress = nil
	importProgress(t, r, legacy)
	importProgress(t, r, progressSnapshot(4, "a", "b"))
	if out := queryProgress(t, r); len(out.Changes) != 0 || out.Checkpoints[2].Boundary != "baseline" {
		t.Fatalf("legacy bridged=%+v", out)
	}
}

func TestProgressWindowDoesNotIncludeFutureAndKeepsLatestWhenLimited(t *testing.T) {
	r, _ := openTemp(t)
	progressPlayer(t, r, "user")
	for n := 1; n <= 3; n++ {
		importProgress(t, r, progressSnapshot(n, fmt.Sprint(n)))
	}
	out, err := r.ReadPlayerProgress(t.Context(), "user", progressStart, progressStart.Add(3*time.Minute), 1)
	if err != nil || out.CheckpointTotal != 2 || len(out.Checkpoints) != 1 || out.Checkpoints[0].Metrics["owned_pals"].IDs[0] != "2" {
		t.Fatalf("out=%+v err=%v", out, err)
	}
	for _, c := range out.Changes {
		if c.IntervalEnd >= formatObservationTime(progressStart.Add(3*time.Minute)) {
			t.Fatal("future change leaked")
		}
	}
}

func TestProgressAmbiguousIdentityDoesNotPersistMapping(t *testing.T) {
	r, _ := openTemp(t)
	progressPlayer(t, r, "user")
	importProgress(t, r, progressSnapshot(1, "a"))
	progressPlayer(t, r, "second")
	importProgress(t, r, progressSnapshot(2, "b"))
	var n int64
	if err := r.gorm.Table("save_identity_mappings").Count(&n).Error; err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("ambiguous identity persisted: %d", n)
	}
}

func TestProgressSharedOwnershipNoticeIsStoredWithoutPersonalDiff(t *testing.T) {
	r, _ := openTemp(t)
	progressPlayer(t, r, "user")
	a, b := progressSnapshot(1, "a"), progressSnapshot(2, "a")
	first, next := int64(12), int64(15)
	a.Players[0].Progress.UnattributedPals = &ProgressMetric{State: "known", Value: &first}
	b.Players[0].Progress.UnattributedPals = &ProgressMetric{State: "known", Value: &next}
	importProgress(t, r, a)
	importProgress(t, r, b)
	out := queryProgress(t, r)
	if out.Checkpoints[1].UnattributedPals == nil || *out.Checkpoints[1].UnattributedPals.Value != 15 {
		t.Fatalf("missing shared ownership notice: %+v", out)
	}
	for _, c := range out.Changes {
		if c.Metric != "capture_total" {
			t.Fatalf("shared count became personal diff: %+v", c)
		}
	}
}

func TestProgressReplayedOldSnapshotBreaksContinuity(t *testing.T) {
	r, _ := openTemp(t)
	progressPlayer(t, r, "user")
	a, b, c := progressSnapshot(1, "a"), progressSnapshot(2, "a", "b"), progressSnapshot(3, "c")
	// Ownership can roll back even when capture/unlock counters are unchanged.
	n := int64(10)
	for _, s := range []*SaveSnapshot{&a, &b, &c} {
		m := s.Players[0].Progress.Metrics["capture_total"]
		m.Value = &n
		s.Players[0].Progress.Metrics["capture_total"] = m
	}
	importProgress(t, r, a)
	importProgress(t, r, b)
	replay := importProgress(t, r, a)
	if replay.Inserted {
		t.Fatal("replay must stay idempotent")
	}
	importProgress(t, r, c)
	out := queryProgress(t, r)
	if len(out.Changes) != 1 || out.Checkpoints[2].Boundary != "replayed_snapshot" {
		t.Fatalf("bridged replay: %+v", out)
	}
}

func TestProgressCrossWorldOutOfOrderCannotRegressHead(t *testing.T) {
	r, _ := openTemp(t)
	progressPlayer(t, r, "user")
	importProgress(t, r, progressSnapshot(10, "a"))
	old := progressSnapshot(1, "b")
	old.Source.WorldID = "world-b"
	importProgress(t, r, old)
	importProgress(t, r, progressSnapshot(11, "a", "c"))
	out := queryProgress(t, r)
	if len(out.Checkpoints) != 2 || len(out.Changes) != 0 || out.Checkpoints[1].Boundary != "after_out_of_order" {
		t.Fatalf("world order=%+v", out)
	}
}

func TestProgressReplayingCurrentDoesNotBreakNextDiff(t *testing.T) {
	r, _ := openTemp(t)
	progressPlayer(t, r, "user")
	a := progressSnapshot(1, "a")
	importProgress(t, r, a)
	importProgress(t, r, a)
	importProgress(t, r, progressSnapshot(2, "b"))
	if out := queryProgress(t, r); len(out.Changes) != 2 || out.Checkpoints[1].Boundary != "" {
		t.Fatalf("current replay=%+v", out)
	}
}

func TestProgressClassificationDefinitionChangesBoundary(t *testing.T) {
	r, _ := openTemp(t)
	progressPlayer(t, r, "user")
	a, b := progressSnapshot(1, "a"), progressSnapshot(2, "a", "npc-now-pal")
	a.Source.ProgressDefinition = fmt.Sprintf("%064x", 1)
	b.Source.ProgressDefinition = fmt.Sprintf("%064x", 2)
	importProgress(t, r, a)
	importProgress(t, r, b)
	out := queryProgress(t, r)
	if len(out.Changes) != 0 || out.Checkpoints[1].Boundary != "schema_changed" {
		t.Fatalf("catalogue change became activity: %+v", out)
	}
}
