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

func TestProgressGrowthCountersValidate(t *testing.T) {
	for _, name := range []string{"level", "experience"} {
		for _, tc := range []struct {
			label string
			value int64
			ids   []string
			valid bool
		}{
			{"positive", 8, nil, true}, {"zero", 0, nil, name == "experience"},
			{"negative", -1, nil, false}, {"ids", 1, []string{"bad"}, false},
			{"unsafe_integer", 9007199254740992, nil, false},
		} {
			t.Run(name+"/"+tc.label, func(t *testing.T) {
				s := progressSnapshot(1)
				s.Players[0].Progress.Metrics[name] = ProgressMetric{State: "known", Value: &tc.value, IDs: tc.ids}
				err := validateSaveProgress(s, s.Players[0].Progress)
				if (err == nil) != tc.valid {
					t.Fatalf("valid=%v err=%v", tc.valid, err)
				}
			})
		}
	}
}

func growthSnapshot(n int, level, experience int64) SaveSnapshot {
	s := progressSnapshot(n, "a")
	s.Parser.Version = 3
	s.Players[0].Progress.Metrics["capture_total"] = ProgressMetric{State: "unknown"}
	s.Players[0].Progress.Metrics["level"] = ProgressMetric{State: "known", Value: &level}
	s.Players[0].Progress.Metrics["experience"] = ProgressMetric{State: "known", Value: &experience}
	return s
}

func TestProgressGrowthBaselineDiffAndBoundaries(t *testing.T) {
	for _, boundary := range []string{"", "level_reset", "experience_reset", "schema_changed", "unknown"} {
		name := boundary
		if name == "" {
			name = "growth_increase"
		}
		t.Run(name, func(t *testing.T) {
			r, _ := openTemp(t)
			progressPlayer(t, r, "user")
			a, b := growthSnapshot(1, 1, 0), growthSnapshot(2, 2, 50)
			wantBoundary := boundary
			switch boundary {
			case "level_reset":
				a = growthSnapshot(1, 3, 0)
				wantBoundary = "counter_reset"
			case "experience_reset":
				a = growthSnapshot(1, 1, 100)
				wantBoundary = "counter_reset"
			case "schema_changed":
				a.Parser.Version = 2
			case "unknown":
				delete(a.Players[0].Progress.Metrics, "level")
				delete(a.Players[0].Progress.Metrics, "experience")
				wantBoundary = ""
			}
			importProgress(t, r, a)
			if got := queryProgress(t, r); len(got.Changes) != 0 || got.Checkpoints[0].Boundary != "baseline" {
				t.Fatalf("baseline=%+v", got)
			}
			importProgress(t, r, b)
			got := queryProgress(t, r)
			if got.Checkpoints[1].Boundary != wantBoundary {
				t.Fatalf("boundary=%q", got.Checkpoints[1].Boundary)
			}
			if boundary != "" {
				if len(got.Changes) != 0 {
					t.Fatalf("unproven changes=%+v", got.Changes)
				}
				return
			}
			if len(got.Changes) != 2 {
				t.Fatalf("changes=%+v", got.Changes)
			}
			for _, c := range got.Changes {
				if c.Delta <= 0 || len(c.Added) != 0 || len(c.Removed) != 0 || c.CheckpointID != got.Checkpoints[1].ID || c.PreviousCheckpointID != got.Checkpoints[0].ID {
					t.Fatalf("change=%+v", c)
				}
				if c.Metric == "experience" && (c.Before != 0 || c.After != 50 || c.Delta != 50) {
					t.Fatalf("zero experience=%+v", c)
				}
			}
		})
	}
}

func TestProgressGrowthLegacyStoredJSONIsUnknown(t *testing.T) {
	r, _ := openTemp(t)
	progressPlayer(t, r, "user")
	importProgress(t, r, progressSnapshot(1, "a"))
	// Simulate checkpoint JSON persisted by parser v2, before normalization knew growth.
	if err := r.gorm.Model(&progressCheckpointModel{}).Where("id > 0").Update("metrics_json", `{"owned_pals":{"state":"known","value":45},"capture_total":{"state":"known","value":101},"paldeck":{"state":"known","value":12}}`).Error; err != nil {
		t.Fatal(err)
	}
	got := queryProgress(t, r)
	if got.Status != "available" || len(got.Checkpoints) != 1 || len(got.Changes) != 0 {
		t.Fatalf("legacy progress=%+v", got)
	}
	for name, value := range map[string]int64{"owned_pals": 45, "capture_total": 101, "paldeck": 12} {
		metric := got.Checkpoints[0].Metrics[name]
		if metric.State != "known" || metric.Value == nil || *metric.Value != value || len(metric.IDs) != 0 {
			t.Fatalf("legacy %s=%+v", name, metric)
		}
	}
	for _, name := range []string{"level", "experience"} {
		m := got.Checkpoints[0].Metrics[name]
		if m.State != "unknown" || m.Value != nil {
			t.Fatalf("%s=%+v", name, m)
		}
	}
}

func TestProgressGrowthChangeFailureRollsBackImport(t *testing.T) {
	r, _ := openTemp(t)
	progressPlayer(t, r, "user")
	importProgress(t, r, growthSnapshot(1, 1, 0))
	if err := r.gorm.Exec("CREATE TRIGGER fail_growth BEFORE INSERT ON save_progress_changes WHEN NEW.metric = 'experience' BEGIN SELECT RAISE(ABORT, 'test failure'); END;").Error; err != nil {
		t.Fatal(err)
	}
	if _, err := r.ImportSaveSnapshot(t.Context(), growthSnapshot(2, 2, 50), progressStart); err == nil {
		t.Fatal("expected growth insertion failure")
	}
	got := queryProgress(t, r)
	if len(got.Checkpoints) != 1 || len(got.Changes) != 0 {
		t.Fatalf("partial progress=%+v", got)
	}
	var count int64
	r.gorm.Table("save_imports").Count(&count)
	if count != 1 {
		t.Fatalf("partial import count=%d", count)
	}
}

func TestProgressImportAfterLegacyCountOnlyCheckpoint(t *testing.T) {
	for _, metric := range []string{"owned_pals", "paldeck", "fast_travel"} {
		for _, tc := range []struct {
			name        string
			stored      string
			wantChanges int
		}{
			{"same_count", `{"state":"known","value":2}`, 0},
			{"count_increase", `{"state":"known","value":1}`, 1},
			{"incomplete_ids", `{"state":"known","value":2,"ids":["missing-old-id"]}`, 0},
			{"missing_value", `{"state":"known"}`, 0},
		} {
			t.Run(metric+"/"+tc.name, func(t *testing.T) {
				r, _ := openTemp(t)
				progressPlayer(t, r, "user")
				importProgress(t, r, progressSnapshot(1, "a"))
				if err := r.gorm.Model(&progressCheckpointModel{}).Where("id > 0").Update("metrics_json", fmt.Sprintf(`{"%s":%s}`, metric, tc.stored)).Error; err != nil {
					t.Fatal(err)
				}
				next := progressSnapshot(2, "a", "b")
				count := int64(2)
				next.Players[0].Progress.Metrics[metric] = ProgressMetric{State: "known", Value: &count, IDs: []string{"a", "b"}}
				importProgress(t, r, next)
				got := queryProgress(t, r)
				if got.Checkpoints[1].Boundary != "" || len(got.Changes) != tc.wantChanges {
					t.Fatalf("boundary=%q changes=%+v", got.Checkpoints[1].Boundary, got.Changes)
				}
				for _, change := range got.Changes {
					if change.Metric != metric || change.Before != 1 || change.After != 2 || change.Delta != 1 || change.Added != nil || change.Removed != nil {
						t.Fatalf("legacy numeric change must retain unknown set details: %+v", change)
					}
				}
			})
		}
	}
}
