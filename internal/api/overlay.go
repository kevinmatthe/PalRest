package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/kevinmatt/palworld-playtime-guard/internal/overlay"
	"github.com/kevinmatt/palworld-playtime-guard/internal/store"
)

type OverlayProvider interface {
	Snapshot(context.Context, string, string) (overlay.Snapshot, error)
}

type overlayOption struct {
	provider OverlayProvider
}

func WithOverlayProvider(provider OverlayProvider) any {
	return overlayOption{provider: provider}
}

func (s *Server) getOverlaySnapshot(w http.ResponseWriter, r *http.Request) {
	gameID, ok := overlayQueryValue(r, "game_id", 64)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid_request", "game_id must be provided exactly once and be valid")
		return
	}
	userID, ok := overlayQueryValue(r, "user_id", 256)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid_request", "user_id must be provided exactly once and be valid")
		return
	}
	if s.overlayProvider == nil {
		writeError(w, http.StatusServiceUnavailable, "snapshot_unavailable", "overlay snapshot is unavailable")
		return
	}

	snapshot, err := s.overlayProvider.Snapshot(r.Context(), gameID, userID)
	if err != nil {
		switch {
		case errors.Is(err, overlay.ErrInvalidRequest):
			writeError(w, http.StatusBadRequest, "invalid_request", "invalid overlay snapshot request")
		case errors.Is(err, overlay.ErrGameNotSupported):
			writeError(w, http.StatusNotFound, "game_not_supported", "game is not supported")
		case errors.Is(err, store.ErrNotFound):
			writeError(w, http.StatusNotFound, "player_not_found", "player was not found")
		default:
			writeError(w, http.StatusServiceUnavailable, "snapshot_unavailable", "overlay snapshot is unavailable")
		}
		return
	}

	payload, err := json.Marshal(snapshot)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "snapshot_unavailable", "overlay snapshot is unavailable")
		return
	}
	digest := sha256.Sum256(payload)
	etag := `"` + hex.EncodeToString(digest[:]) + `"`
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "no-cache")
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(append(payload, '\n'))
}

func overlayQueryValue(r *http.Request, name string, maxBytes int) (string, bool) {
	values, found := r.URL.Query()[name]
	if !found || len(values) != 1 {
		return "", false
	}
	value := strings.TrimSpace(values[0])
	if len(value) == 0 || len(value) > maxBytes || !utf8.ValidString(value) {
		return "", false
	}
	for _, r := range value {
		if unicode.IsControl(r) || unicode.In(r, unicode.Cf) {
			return "", false
		}
	}
	return value, true
}
