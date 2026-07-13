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
	latest, err := s.repository.LatestServerDocument(ctx, kind)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return fmt.Errorf("record observed server %s: read durable baseline: %w", kind, err)
	}

	processBaseline := *cache
	if processBaseline.valid && processBaseline.at.After(latest.At) {
		switch {
		case at.Before(processBaseline.at):
			return fmt.Errorf("record observed server %s: observation time %s is before process baseline %s", kind, at.Format(time.RFC3339Nano), processBaseline.at.Format(time.RFC3339Nano))
		case at.Equal(processBaseline.at):
			if processBaseline.hash != hash {
				return fmt.Errorf("record observed server %s: observation at %s does not match process baseline", kind, at.Format(time.RFC3339Nano))
			}
			return nil
		}
	}

	latestVersion := ""
	if err == nil && kind == "info" {
		var latestInfo domain.ServerInfo
		if decodeErr := json.Unmarshal(latest.Canonical, &latestInfo); decodeErr != nil {
			return fmt.Errorf("record observed server info: decode durable baseline: %w", decodeErr)
		}
		latestVersion = latestInfo.Version
	}
	if err == nil {
		*cache = serverDocumentBaseline{valid: true, at: latest.At, hash: latest.Hash, version: latestVersion}
		switch {
		case at.Before(latest.At):
			return fmt.Errorf("record observed server %s: observation time %s is before durable baseline %s", kind, at.Format(time.RFC3339Nano), latest.At.Format(time.RFC3339Nano))
		case at.Equal(latest.At):
			if latest.Hash != hash || !bytes.Equal(latest.Canonical, canonical) {
				return fmt.Errorf("record observed server %s: observation at %s does not match durable document", kind, at.Format(time.RFC3339Nano))
			}
			return nil
		}
	}
	write := store.ServerDocumentObservation{Kind: kind, At: at, Canonical: canonical, Hash: hash}
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
	if _, err := s.repository.RecordServerDocumentObservation(ctx, write); err != nil {
		return fmt.Errorf("record observed server %s: %w", kind, err)
	}
	*cache = serverDocumentBaseline{valid: true, at: at, hash: hash, version: version}
	return nil
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
