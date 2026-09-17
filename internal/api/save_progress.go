package api

import (
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/kevinmatt/palworld-playtime-guard/internal/store"
)

func (s *Server) getPlayerProgress(w http.ResponseWriter, r *http.Request) {
	if s.progress == nil {
		writeError(w, http.StatusServiceUnavailable, "query_unavailable", "progress query unavailable")
		return
	}
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid query parameters")
		return
	}
	if limit, ok := parseLimit(query, 200); !ok || limit > 500 {
		writeError(w, http.StatusBadRequest, "invalid_request", "limit must be between 1 and 500")
		return
	}
	start, end, limit, ok := parseRangeQuery(w, query, 200)
	if !ok {
		return
	}
	if start.IsZero() {
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid RFC3339 range")
		return
	}
	userID := strings.TrimSpace(r.PathValue("userID"))
	if userID == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "user ID is required")
		return
	}
	result, err := s.progress.ReadPlayerProgress(r.Context(), userID, start, end, limit)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "player not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query_failed", "progress query failed")
		return
	}
	result.UserID = userID
	if result.Checkpoints == nil {
		result.Checkpoints = []store.ProgressCheckpoint{}
	}
	if result.Changes == nil {
		result.Changes = []store.ProgressChange{}
	}
	writeJSON(w, http.StatusOK, result)
}
