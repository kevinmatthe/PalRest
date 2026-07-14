package observation

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/kevinmatt/palworld-playtime-guard/internal/palworld"
	"github.com/kevinmatt/palworld-playtime-guard/internal/store"
)

// LiveGameData holds the latest successful /game-data snapshot for live guild-base POIs.
type LiveGameData struct {
	mu   sync.RWMutex
	snap palworld.GameDataSnapshot
}

func NewLiveGameData() *LiveGameData {
	return &LiveGameData{}
}

func (l *LiveGameData) Update(snap palworld.GameDataSnapshot) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.snap = snap
}

func (l *LiveGameData) Snapshot() palworld.GameDataSnapshot {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.snap
}

// POIsForUser returns live guild_base WorldPOIs for a REST user id.
func (l *LiveGameData) POIsForUser(userID string) []store.WorldPOI {
	snap := l.Snapshot()
	boxes := snap.POIsForUser(userID)
	if len(boxes) == 0 {
		return nil
	}
	out := make([]store.WorldPOI, 0, len(boxes))
	for _, b := range boxes {
		out = append(out, liveBoxToPOI(b))
	}
	return out
}

// AllGuildPOIs returns every PalBox hub from the latest game-data sample.
func (l *LiveGameData) AllGuildPOIs() []store.WorldPOI {
	if l == nil {
		return nil
	}
	snap := l.Snapshot()
	if len(snap.PalBoxes) == 0 {
		return nil
	}
	out := make([]store.WorldPOI, 0, len(snap.PalBoxes))
	for _, b := range snap.PalBoxes {
		out = append(out, liveBoxToPOI(b))
	}
	return out
}

func liveBoxToPOI(b palworld.LivePalBox) store.WorldPOI {
	name := b.GuildName
	if name == "" {
		name = b.GuildID
	}
	return store.WorldPOI{
		ID:        b.ID,
		NameZh:    "公会「" + name + "」据点",
		Kind:      "guild_base",
		X:         b.X,
		Y:         b.Y,
		GuildName: name,
		GuildID:   b.GuildID,
		Source:    "game_data",
	}
}

// GameDataReader is satisfied by palworld.Client.
type GameDataReader interface {
	GameData(context.Context) (palworld.GameDataSnapshot, error)
}

// SampleGameData fetches game-data once and stores it. Failures are logged and non-fatal.
func SampleGameData(ctx context.Context, reader GameDataReader, live *LiveGameData, log *slog.Logger) {
	if reader == nil || live == nil {
		return
	}
	snap, err := reader.GameData(ctx)
	if err != nil {
		if log != nil {
			log.Warn("game-data sample failed", "err", err)
		}
		return
	}
	if snap.ObservedAt.IsZero() {
		snap.ObservedAt = time.Now().UTC()
	}
	live.Update(snap)
	if log != nil {
		log.Debug("game-data sample ok",
			"players_with_guild", len(snap.PlayerGuild),
			"pal_boxes", len(snap.PalBoxes),
			"in_game_days", snap.InGameDays,
		)
	}
}
