package api

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/kevinmatt/palworld-playtime-guard/internal/sensitivejson"
	"github.com/kevinmatt/palworld-playtime-guard/internal/store"
)

const maxAdminRange = 31 * 24 * time.Hour

type adminEventDTO struct {
	ID         string         `json:"id"`
	EventType  string         `json:"event_type"`
	OccurredAt time.Time      `json:"occurred_at"`
	ObservedAt time.Time      `json:"observed_at"`
	Source     string         `json:"source"`
	Confidence string         `json:"confidence"`
	Summary    string         `json:"summary"`
	Data       map[string]any `json:"data,omitempty"`
}

func (s *Server) getAdminPlayerTimeline(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	if s.observations == nil {
		writeError(w, http.StatusInternalServerError, "query_failed", "observation query unavailable")
		return
	}
	start, end, limit, ok := parseRangeQuery(w, r.URL.Query(), 500)
	if !ok {
		return
	}
	userID := strings.TrimSpace(r.PathValue("userID"))
	if userID == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "user ID is required")
		return
	}
	timeline, err := s.observations.ReadSensitivePlayerTimeline(r.Context(), s.adminActor(), userID, start, end, limit)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "player not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query_failed", "timeline query failed")
		return
	}
	events := make([]adminEventDTO, 0, len(timeline.Events))
	for _, event := range timeline.Events {
		events = append(events, safeEvent(event))
	}
	writeJSON(w, http.StatusOK, map[string]any{"user_id": userID, "events": events, "trajectories": timeline.Trajectories, "private_samples": timeline.PrivateSamples})
}

func (s *Server) getAdminServerMetrics(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	if s.observations == nil {
		writeError(w, http.StatusInternalServerError, "query_failed", "observation query unavailable")
		return
	}
	start, end, limit, ok := parseRangeQuery(w, r.URL.Query(), 500)
	if !ok {
		return
	}
	items, err := s.observations.ReadServerMetrics(r.Context(), s.adminActor(), start, end, limit)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "server metrics not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query_failed", "metrics query failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"samples": items})
}

func (s *Server) getAdminServerDocuments(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	if s.observations == nil {
		writeError(w, http.StatusInternalServerError, "query_failed", "observation query unavailable")
		return
	}
	query := r.URL.Query()
	if !onlySingleParams(query, "kind", "limit", "cursor") {
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid query parameters")
		return
	}
	kind := query.Get("kind")
	if kind != "info" && kind != "settings" {
		writeError(w, http.StatusBadRequest, "invalid_request", "kind must be info or settings")
		return
	}
	limit, ok := parseLimit(query, 100)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid_request", "limit must be between 1 and 2000")
		return
	}
	if values, exists := query["cursor"]; exists && values[0] == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid cursor")
		return
	}
	cursor, cursorErr := decodeDocumentCursor(query.Get("cursor"), kind)
	if cursorErr != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid cursor")
		return
	}
	page, err := s.observations.ReadServerDocuments(r.Context(), s.adminActor(), kind, limit, cursor)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "server documents not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query_failed", "documents query failed")
		return
	}
	type documentDTO struct {
		Kind        string          `json:"kind"`
		ObservedAt  time.Time       `json:"observed_at"`
		ContentHash string          `json:"content_hash"`
		Canonical   json.RawMessage `json:"canonical"`
	}
	response := make([]documentDTO, 0, len(page.Documents))
	for _, item := range page.Documents {
		canonical, redactErr := sensitivejson.RedactJSON(item.Canonical)
		if redactErr != nil {
			writeError(w, http.StatusInternalServerError, "query_failed", "documents query failed")
			return
		}
		response = append(response, documentDTO{item.Kind, item.ObservedAt, item.ContentHash, json.RawMessage(canonical)})
	}
	nextCursor := ""
	if page.Next != nil {
		nextCursor, err = encodeDocumentCursor(kind, *page.Next)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "query_failed", "documents query failed")
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"documents": response, "next_cursor": nextCursor})
}

type documentCursorEnvelope struct {
	Kind        string `json:"kind"`
	ObservedAt  string `json:"observed_at"`
	ContentHash string `json:"content_hash"`
}

func encodeDocumentCursor(kind string, cursor store.ServerDocumentCursor) (string, error) {
	if cursor.ObservedAt.IsZero() || !validDocumentHash(cursor.ContentHash) {
		return "", errors.New("invalid document cursor")
	}
	payload, err := json.Marshal(documentCursorEnvelope{Kind: kind, ObservedAt: cursor.ObservedAt.UTC().Format(time.RFC3339Nano), ContentHash: cursor.ContentHash})
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(payload), nil
}

func decodeDocumentCursor(encoded, kind string) (*store.ServerDocumentCursor, error) {
	if encoded == "" {
		return nil, nil
	}
	if len(encoded) > 1024 {
		return nil, errors.New("cursor too long")
	}
	payload, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var envelope documentCursorEnvelope
	if err := decoder.Decode(&envelope); err != nil {
		return nil, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, errors.New("cursor has trailing content")
	}
	if envelope.Kind != kind || !validDocumentHash(envelope.ContentHash) {
		return nil, errors.New("cursor fields invalid")
	}
	at, err := time.Parse(time.RFC3339Nano, envelope.ObservedAt)
	if err != nil {
		return nil, err
	}
	return &store.ServerDocumentCursor{ObservedAt: at, ContentHash: envelope.ContentHash}, nil
}

func validDocumentHash(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 32 && value == strings.ToLower(value)
}

func (s *Server) adminActor() string {
	if s.auth != nil && strings.TrimSpace(s.auth.username) != "" {
		return s.auth.username
	}
	return "admin"
}

func parseRangeQuery(w http.ResponseWriter, query url.Values, defaultLimit int) (time.Time, time.Time, int, bool) {
	if !onlySingleParams(query, "start", "end", "limit") || len(query["start"]) != 1 || len(query["end"]) != 1 {
		writeError(w, http.StatusBadRequest, "invalid_request", "start and end are required once")
		return time.Time{}, time.Time{}, 0, false
	}
	start, err1 := time.Parse(time.RFC3339, query.Get("start"))
	end, err2 := time.Parse(time.RFC3339, query.Get("end"))
	if err1 != nil || err2 != nil || !start.Before(end) || end.Sub(start) > maxAdminRange {
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid RFC3339 range")
		return time.Time{}, time.Time{}, 0, false
	}
	limit, ok := parseLimit(query, defaultLimit)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid_request", "limit must be between 1 and 2000")
		return time.Time{}, time.Time{}, 0, false
	}
	return start, end, limit, true
}

func onlySingleParams(query url.Values, allowed ...string) bool {
	set := make(map[string]bool, len(allowed))
	for _, key := range allowed {
		set[key] = true
	}
	for key, values := range query {
		if !set[key] || len(values) != 1 {
			return false
		}
	}
	return true
}

func parseLimit(query url.Values, fallback int) (int, bool) {
	if _, exists := query["limit"]; !exists {
		return fallback, true
	}
	value, err := strconv.Atoi(query.Get("limit"))
	return value, err == nil && value >= 1 && value <= 2000
}

func safeEvent(event store.ActivityEvent) adminEventDTO {
	result := adminEventDTO{ID: event.ID, EventType: event.EventType, OccurredAt: event.OccurredAt, ObservedAt: event.ObservedAt, Source: event.Source, Confidence: event.Confidence, Summary: event.EventType}
	if event.SchemaVersion != 1 {
		result.Summary = "unsupported event payload"
		return result
	}
	if event.EventType != "player_joined" && event.EventType != "player_left" && event.EventType != "player_attribute_changed" {
		result.Summary = "event payload unavailable"
		return result
	}
	var payload struct {
		PlayerID      string `json:"player_id"`
		Name          string `json:"name"`
		AccountName   string `json:"account_name"`
		Level         int    `json:"level"`
		BuildingCount int    `json:"building_count"`
	}
	if json.Unmarshal([]byte(event.PayloadJSON), &payload) != nil {
		result.Summary = "invalid event payload"
		return result
	}
	result.Data = map[string]any{"player_id": payload.PlayerID, "name": payload.Name, "account_name": payload.AccountName, "level": payload.Level, "building_count": payload.BuildingCount}
	return result
}
