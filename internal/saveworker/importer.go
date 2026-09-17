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
	now    func() time.Time
	active chan struct{}
}

func NewImporter(runner *Runner, repo interface {
	ImportSaveSnapshot(context.Context, store.SaveSnapshot, time.Time) (store.SaveImportResult, error)
}, now func() time.Time) *Importer {
	if now == nil {
		now = time.Now
	}
	return &Importer{runner: runner, repo: repo, now: now, active: make(chan struct{}, 1)}
}

func (i *Importer) Import(ctx context.Context, levelPath string) (store.SaveImportResult, error) {
	// Hold the slot through persistence so another request cannot start parsing
	// while the previous snapshot is still being imported.
	select {
	case i.active <- struct{}{}:
	case <-ctx.Done():
		return store.SaveImportResult{}, ctx.Err()
	}
	defer func() { <-i.active }()
	if err := ctx.Err(); err != nil {
		return store.SaveImportResult{}, err
	}

	snapshot, err := i.runner.Extract(ctx, levelPath)
	if err != nil {
		return store.SaveImportResult{}, err
	}
	return i.repo.ImportSaveSnapshot(ctx, snapshot, i.now())
}
