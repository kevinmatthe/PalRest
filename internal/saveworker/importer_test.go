package saveworker

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kevinmatt/palworld-playtime-guard/internal/store"
)

type importRepositoryFunc func(context.Context, store.SaveSnapshot, time.Time) (store.SaveImportResult, error)

func (f importRepositoryFunc) ImportSaveSnapshot(ctx context.Context, snapshot store.SaveSnapshot, now time.Time) (store.SaveImportResult, error) {
	return f(ctx, snapshot, now)
}

func awaitImport(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(3 * time.Second):
		t.Fatal("import did not finish")
		return nil
	}
}

func awaitWorkerStart(t *testing.T, path string) {
	t.Helper()
	deadline := time.After(3 * time.Second)
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		if _, err := os.Stat(path + ".started"); err == nil {
			return
		}
		select {
		case <-deadline:
			t.Fatal("worker did not start")
		case <-ticker.C:
		}
	}
}

func TestImporterWaitingCanCancelDuringExtractionAndPersistence(t *testing.T) {
	for _, phase := range []string{"extraction", "persistence"} {
		t.Run(phase, func(t *testing.T) {
			worker := writeWorker(t, `#!/bin/sh
touch "$2.started"
while [ -f "$2.hold" ]; do sleep 0.01; done
printf '{"source":{"level_sav":"%s"}}' "$2"
`)
			runner, err := New(worker, 3*time.Second)
			if err != nil {
				t.Fatal(err)
			}
			firstPath := filepath.Join(t.TempDir(), "first")
			if phase == "extraction" {
				if err := os.WriteFile(firstPath+".hold", nil, 0o600); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = os.Remove(firstPath + ".hold") })
			}
			persisting := make(chan struct{})
			releasePersistence := make(chan struct{})
			defer close(releasePersistence)
			repo := importRepositoryFunc(func(ctx context.Context, snapshot store.SaveSnapshot, _ time.Time) (store.SaveImportResult, error) {
				if phase == "persistence" && snapshot.Source.LevelSAV == firstPath {
					close(persisting)
					select {
					case <-releasePersistence:
					case <-ctx.Done():
						return store.SaveImportResult{}, ctx.Err()
					}
				}
				return store.SaveImportResult{Inserted: true}, nil
			})
			importer := NewImporter(runner, repo, nil)
			firstCtx, cancelFirst := context.WithCancel(t.Context())
			defer cancelFirst()
			firstDone := make(chan error, 1)
			go func() {
				_, err := importer.Import(firstCtx, firstPath)
				firstDone <- err
			}()
			awaitWorkerStart(t, firstPath)
			if phase == "persistence" {
				select {
				case <-persisting:
				case <-time.After(3 * time.Second):
					t.Fatal("first import did not reach persistence")
				}
			}

			waitingPath := filepath.Join(t.TempDir(), "waiting")
			waitingCtx, cancelWaiting := context.WithTimeout(t.Context(), 100*time.Millisecond)
			defer cancelWaiting()
			_, err = importer.Import(waitingCtx, waitingPath)
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Errorf("waiting import error = %v, want deadline exceeded", err)
			}
			if _, err := os.Stat(waitingPath + ".started"); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("waiting import started another worker during %s; stat error = %v", phase, err)
			}

			cancelFirst()
			if err := awaitImport(t, firstDone); err == nil {
				t.Error("canceled active import succeeded")
			}
			result, err := importer.Import(t.Context(), filepath.Join(t.TempDir(), "next"))
			if err != nil || !result.Inserted {
				t.Fatalf("next import after cancellation: result=%+v error=%v", result, err)
			}
		})
	}
}

func TestImporterReleasesAfterFailure(t *testing.T) {
	for _, phase := range []string{"extraction", "persistence"} {
		t.Run(phase, func(t *testing.T) {
			worker := writeWorker(t, `#!/bin/sh
case "$2" in */invalid) echo broken; exit 0;; esac
echo '{}'
`)
			runner, err := New(worker, time.Second)
			if err != nil {
				t.Fatal(err)
			}
			repoFailure := errors.New("persistence failed")
			calls := 0
			repo := importRepositoryFunc(func(context.Context, store.SaveSnapshot, time.Time) (store.SaveImportResult, error) {
				calls++
				if phase == "persistence" && calls == 1 {
					return store.SaveImportResult{}, repoFailure
				}
				return store.SaveImportResult{Inserted: true}, nil
			})
			importer := NewImporter(runner, repo, nil)
			firstPath := "/save/valid"
			if phase == "extraction" {
				firstPath = "/save/invalid"
			}
			if _, err := importer.Import(t.Context(), firstPath); err == nil {
				t.Fatal("first import should fail")
			} else if phase == "extraction" && !strings.Contains(err.Error(), "decode") {
				t.Fatalf("expected decoding failure, got %v", err)
			} else if phase == "persistence" && !errors.Is(err, repoFailure) {
				t.Fatalf("expected persistence failure, got %v", err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			result, err := importer.Import(ctx, "/save/valid")
			if err != nil || !result.Inserted {
				t.Fatalf("import after failure: result=%+v error=%v", result, err)
			}
		})
	}
}
