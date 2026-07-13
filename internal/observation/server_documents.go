package observation

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/kevinmatt/palworld-playtime-guard/internal/domain"
	"github.com/kevinmatt/palworld-playtime-guard/internal/store"
)

func (s *ServerService) RecordInfo(ctx context.Context, at time.Time, info domain.ServerInfo) error {
	canonical, hash, err := canonicalDocument(info)
	if err != nil {
		return fmt.Errorf("record observed server info: %w", err)
	}
	s.infoMu.Lock()
	defer s.infoMu.Unlock()
	return s.recordDocument(ctx, "info", at, canonical, hash, info.Version, &s.info)
}

func (s *ServerService) RecordSettings(ctx context.Context, at time.Time, settings domain.ServerSettings) error {
	canonical, hash, err := canonicalSettings(settings.Values)
	if err != nil {
		return fmt.Errorf("record observed server settings: %w", err)
	}
	s.settingsMu.Lock()
	defer s.settingsMu.Unlock()
	return s.recordDocument(ctx, "settings", at, canonical, hash, "", &s.settings)
}

func (s *ServerService) recordDocument(ctx context.Context, kind string, at time.Time, canonical []byte, hash, version string, cache *serverDocumentBaseline) error {
	if at.IsZero() {
		return fmt.Errorf("record observed server %s: observation time is zero", kind)
	}
	at = at.UTC()
	for attempt := 0; attempt < 3; attempt++ {
		latest, err := s.repository.LatestServerDocument(ctx, kind)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return fmt.Errorf("record observed server %s: read durable baseline: %w", kind, err)
		}
		expected := &store.ServerDocumentToken{}
		latestVersion := ""
		if err == nil {
			expected.Exists = true
			expected.At = latest.At
			expected.Hash = latest.Hash
			if kind == "info" {
				var latestInfo domain.ServerInfo
				if decodeErr := json.Unmarshal(latest.Canonical, &latestInfo); decodeErr != nil {
					return fmt.Errorf("record observed server info: decode durable baseline: %w", decodeErr)
				}
				latestVersion = latestInfo.Version
			}
			*cache = serverDocumentBaseline{valid: true, at: latest.At, hash: latest.Hash, version: latestVersion}
			switch {
			case at.Before(latest.At):
				return fmt.Errorf("record observed server %s: observation time %s is before durable baseline %s", kind, at.Format(time.RFC3339Nano), latest.At.Format(time.RFC3339Nano))
			case at.Equal(latest.At):
				if latest.Hash != hash || !bytes.Equal(latest.Canonical, canonical) {
					return fmt.Errorf("record observed server %s: observation at %s does not match durable document", kind, at.Format(time.RFC3339Nano))
				}
				replay := store.ServerDocumentObservation{
					Kind: kind, At: at, Canonical: canonical, Hash: hash, Event: latest.Event, Expected: expected,
				}
				if _, replayErr := s.repository.RecordServerDocumentObservation(ctx, replay); replayErr != nil {
					return fmt.Errorf("record observed server %s: prove durable replay: %w", kind, replayErr)
				}
				return nil
			}
		}
		write := store.ServerDocumentObservation{Kind: kind, At: at, Canonical: canonical, Hash: hash, Expected: expected}
		if err == nil && latest.Hash != hash {
			eventType := ""
			switch {
			case kind == "settings":
				eventType = "server_settings_changed"
			case version != latestVersion:
				eventType = "server_version_changed"
			}
			if eventType != "" {
				event, eventErr := s.newDocumentEvent(eventType, at, latest.Hash, hash, latestVersion, version)
				if eventErr != nil {
					return eventErr
				}
				write.Event = &event
			}
		}
		if _, writeErr := s.repository.RecordServerDocumentObservation(ctx, write); writeErr != nil {
			if errors.Is(writeErr, store.ErrObservationConflict) {
				continue
			}
			return fmt.Errorf("record observed server %s: %w", kind, writeErr)
		}
		*cache = serverDocumentBaseline{valid: true, at: at, hash: hash, version: version}
		return nil
	}
	return fmt.Errorf("record observed server %s: %w after 3 attempts", kind, store.ErrObservationConflict)
}

func (s *ServerService) newDocumentEvent(eventType string, at time.Time, oldHash, newHash, oldVersion, newVersion string) (store.ActivityEvent, error) {
	var payload []byte
	var err error
	if eventType == "server_settings_changed" {
		payload, err = json.Marshal(struct {
			OldHash string `json:"old_hash"`
			NewHash string `json:"new_hash"`
			Summary string `json:"summary"`
		}{OldHash: oldHash, NewHash: newHash, Summary: "server settings changed"})
	} else {
		payload, err = json.Marshal(struct {
			OldHash    string `json:"old_hash"`
			NewHash    string `json:"new_hash"`
			OldVersion string `json:"old_version"`
			NewVersion string `json:"new_version"`
		}{OldHash: oldHash, NewHash: newHash, OldVersion: oldVersion, NewVersion: newVersion})
	}
	if err != nil {
		return store.ActivityEvent{}, fmt.Errorf("record observed server document event: encode payload: %w", err)
	}
	return s.newEvent(eventType, at, payload)
}
