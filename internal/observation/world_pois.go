package observation

import (
	"context"
	"strings"

	"github.com/kevinmatt/palworld-playtime-guard/internal/store"
)

// SaveWorldPOISource is implemented by *store.Repository.
type SaveWorldPOISource interface {
	ListPlayerWorldPOIs(ctx context.Context, userID string) (store.PlayerWorldPOIs, error)
	ListAllGuildBases(ctx context.Context) (store.GuildBasesCatalog, error)
}

// WorldPOIProvider merges save-import bases with live game-data PalBox hubs.
type WorldPOIProvider struct {
	Save SaveWorldPOISource // optional
	Live *LiveGameData      // optional
}

func (p *WorldPOIProvider) ListPlayerWorldPOIs(ctx context.Context, userID string) (store.PlayerWorldPOIs, error) {
	userID = strings.TrimSpace(userID)
	empty := store.PlayerWorldPOIs{UserID: userID, Source: "none", POIs: []store.WorldPOI{}}
	if userID == "" {
		return empty, nil
	}

	var save store.PlayerWorldPOIs
	if p.Save != nil {
		var err error
		save, err = p.Save.ListPlayerWorldPOIs(ctx, userID)
		if err != nil {
			return store.PlayerWorldPOIs{}, err
		}
	} else {
		save = empty
	}

	livePOIs := []store.WorldPOI(nil)
	if p.Live != nil {
		livePOIs = p.Live.POIsForUser(userID)
	}

	if len(save.POIs) == 0 && len(livePOIs) == 0 {
		return empty, nil
	}

	// Prefer save POIs when ids collide; always include unique live PalBoxes.
	byID := make(map[string]store.WorldPOI, len(save.POIs)+len(livePOIs))
	for _, poi := range save.POIs {
		byID[poi.ID] = poi
	}
	for _, poi := range livePOIs {
		if _, exists := byID[poi.ID]; !exists {
			byID[poi.ID] = poi
		}
		// Also dedupe near-identical camps: if a save camp is within 5k of a live box same guild, keep both ids but that's ok.
	}
	merged := make([]store.WorldPOI, 0, len(byID))
	for _, poi := range byID {
		merged = append(merged, poi)
	}

	source := "none"
	asOf := save.AsOf
	switch {
	case len(save.POIs) > 0 && len(livePOIs) > 0:
		source = "save_import+game_data"
	case len(save.POIs) > 0:
		source = "save_import"
	case len(livePOIs) > 0:
		source = "game_data"
		if snap := p.Live.Snapshot(); !snap.ObservedAt.IsZero() {
			asOf = snap.ObservedAt.UTC().Format(timeRFC3339)
		}
	}

	return store.PlayerWorldPOIs{
		UserID: userID,
		Source: source,
		AsOf:   asOf,
		POIs:   merged,
	}, nil
}

// ListAllGuildBases merges save-import camps with live PalBox hubs for map landmarks.
func (p *WorldPOIProvider) ListAllGuildBases(ctx context.Context) (store.GuildBasesCatalog, error) {
	empty := store.GuildBasesCatalog{Source: "none", POIs: []store.WorldPOI{}}
	var save store.GuildBasesCatalog
	if p.Save != nil {
		var err error
		save, err = p.Save.ListAllGuildBases(ctx)
		if err != nil {
			return store.GuildBasesCatalog{}, err
		}
	} else {
		save = empty
	}
	livePOIs := []store.WorldPOI(nil)
	if p.Live != nil {
		livePOIs = p.Live.AllGuildPOIs()
	}
	if len(save.POIs) == 0 && len(livePOIs) == 0 {
		return empty, nil
	}
	byID := make(map[string]store.WorldPOI, len(save.POIs)+len(livePOIs))
	for _, poi := range save.POIs {
		byID[poi.ID] = poi
	}
	for _, poi := range livePOIs {
		if _, exists := byID[poi.ID]; !exists {
			byID[poi.ID] = poi
		}
	}
	merged := make([]store.WorldPOI, 0, len(byID))
	for _, poi := range byID {
		merged = append(merged, poi)
	}
	source := "none"
	asOf := save.AsOf
	switch {
	case len(save.POIs) > 0 && len(livePOIs) > 0:
		source = "save_import+game_data"
	case len(save.POIs) > 0:
		source = "save_import"
	case len(livePOIs) > 0:
		source = "game_data"
		if p.Live != nil {
			if snap := p.Live.Snapshot(); !snap.ObservedAt.IsZero() {
				asOf = snap.ObservedAt.UTC().Format(timeRFC3339)
			}
		}
	}
	return store.GuildBasesCatalog{Source: source, AsOf: asOf, POIs: merged}, nil
}

// time format helper without pulling store internals.
const timeRFC3339 = "2006-01-02T15:04:05Z07:00"
