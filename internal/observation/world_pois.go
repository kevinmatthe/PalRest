package observation

import (
	"context"
	"math"
	"strings"

	"github.com/kevinmatt/palworld-playtime-guard/internal/store"
)

// Same camp from save vs live game-data often share guild but use different IDs
// (gb-… vs pb-…). Collapse those so the map does not double-count.
const guildBaseDedupeDist = 5_000.0

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

	// Prefer save camps; only add live PalBoxes that are not the same physical camp.
	merged := make([]store.WorldPOI, 0, len(save.POIs)+len(livePOIs))
	merged = append(merged, save.POIs...)
	liveAdded := 0
	for _, live := range livePOIs {
		if guildBaseAlreadyPresent(merged, live) {
			continue
		}
		merged = append(merged, live)
		liveAdded++
	}

	source := "none"
	asOf := save.AsOf
	switch {
	case len(save.POIs) > 0 && liveAdded > 0:
		source = "save_import+game_data"
	case len(save.POIs) > 0:
		source = "save_import"
	case liveAdded > 0 || len(livePOIs) > 0:
		// live-only, or all live collapsed into empty save
		if len(save.POIs) == 0 {
			source = "game_data"
			if p.Live != nil {
				if snap := p.Live.Snapshot(); !snap.ObservedAt.IsZero() {
					asOf = snap.ObservedAt.UTC().Format(timeRFC3339)
				}
			}
		}
	}
	return store.GuildBasesCatalog{Source: source, AsOf: asOf, POIs: merged}, nil
}

func guildBaseAlreadyPresent(existing []store.WorldPOI, candidate store.WorldPOI) bool {
	for _, poi := range existing {
		if poi.ID != "" && candidate.ID != "" && poi.ID == candidate.ID {
			return true
		}
		if !sameGuildBase(poi, candidate) {
			continue
		}
		if worldDist2(poi.X, poi.Y, candidate.X, candidate.Y) <= guildBaseDedupeDist*guildBaseDedupeDist {
			return true
		}
	}
	return false
}

func sameGuildBase(a, b store.WorldPOI) bool {
	ag := strings.TrimSpace(a.GuildID)
	bg := strings.TrimSpace(b.GuildID)
	if ag != "" && bg != "" {
		return ag == bg
	}
	an := strings.TrimSpace(a.GuildName)
	bn := strings.TrimSpace(b.GuildName)
	if an != "" && bn != "" {
		return an == bn
	}
	// Unknown guild identity: still allow proximity-only collapse.
	return true
}

func worldDist2(ax, ay, bx, by float64) float64 {
	dx := ax - bx
	dy := ay - by
	if math.IsNaN(dx) || math.IsNaN(dy) || math.IsInf(dx, 0) || math.IsInf(dy, 0) {
		return math.Inf(1)
	}
	return dx*dx + dy*dy
}

// time format helper without pulling store internals.
const timeRFC3339 = "2006-01-02T15:04:05Z07:00"
