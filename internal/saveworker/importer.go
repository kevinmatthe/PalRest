package saveworker

import (
	"context"
	"time"

	"github.com/kevinmatt/palworld-playtime-guard/internal/store"
)

type Importer struct {
	runner *Runner
	repo   interface {
		ImportSaveSnapshot(context.Context, store.SaveSnapshot, time.Time) (store.SaveImportResult, error)
	}
	now func() time.Time
}

func NewImporter(runner *Runner, repo interface {
	ImportSaveSnapshot(context.Context, store.SaveSnapshot, time.Time) (store.SaveImportResult, error)
}, now func() time.Time) *Importer {
	if now == nil {
		now = time.Now
	}
	return &Importer{runner: runner, repo: repo, now: now}
}

func (i *Importer) Import(ctx context.Context, levelPath string) (store.SaveImportResult, error) {
	snapshot, err := i.runner.Extract(ctx, levelPath)
	if err != nil {
		return store.SaveImportResult{}, err
	}
	return i.repo.ImportSaveSnapshot(ctx, snapshot, i.now())
}
