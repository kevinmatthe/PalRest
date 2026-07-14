package palworld

import (
	"fmt"
	"math"
	"strings"
	"time"
)

// Live game-data actor tables (mod/REST extension). Field names match the wire format.

type gameDataPayload struct {
	Time        string             `json:"Time"`
	FPS         float64            `json:"FPS"`
	AverageFPS  float64            `json:"AverageFPS"`
	InGameTime  string             `json:"InGameTime"`
	InGameDays  int                `json:"InGameDays"`
	ActorData   []gameDataActor    `json:"ActorData"`
}

type gameDataActor struct {
	Type               string  `json:"Type"`
	UnitType           string  `json:"UnitType"`
	NickName           string  `json:"NickName"`
	UserID             string  `json:"userid"`
	GuildID            string  `json:"GuildID"`
	GuildName          string  `json:"GuildName"`
	Name               string  `json:"Name"`
	Class              string  `json:"Class"`
	LocationX          float64 `json:"LocationX"`
	LocationY          float64 `json:"LocationY"`
	LocationZ          float64 `json:"LocationZ"`
	InstanceID         string  `json:"InstanceID"`
}

// GameDataSnapshot is a normalized view used for live guild-base POIs and membership.
type GameDataSnapshot struct {
	ObservedAt   time.Time
	InGameTime   string
	InGameDays   int
	// UserID -> GuildID for online Player actors.
	PlayerGuild  map[string]string
	GuildNames   map[string]string
	// PalBox hubs as candidate guild bases (deduped by guild+rounded coords).
	PalBoxes     []LivePalBox
}

type LivePalBox struct {
	ID        string
	GuildID   string
	GuildName string
	X, Y      float64
}

func normalizeGameData(payload gameDataPayload, observedAt time.Time) GameDataSnapshot {
	snap := GameDataSnapshot{
		ObservedAt:  observedAt,
		InGameTime:  strings.TrimSpace(payload.InGameTime),
		InGameDays:  payload.InGameDays,
		PlayerGuild: make(map[string]string),
		GuildNames:  make(map[string]string),
		PalBoxes:    make([]LivePalBox, 0),
	}
	seenBox := make(map[string]struct{})
	for _, actor := range payload.ActorData {
		guildID := strings.TrimSpace(actor.GuildID)
		guildName := strings.TrimSpace(actor.GuildName)
		if guildID != "" && guildName != "" {
			snap.GuildNames[guildID] = guildName
		}
		switch {
		case strings.EqualFold(actor.Type, "Character") && strings.EqualFold(actor.UnitType, "Player"):
			userID := strings.TrimSpace(actor.UserID)
			if userID != "" && guildID != "" {
				snap.PlayerGuild[userID] = guildID
			}
		case strings.EqualFold(actor.Type, "PalBox"):
			if guildID == "" || !finiteCoord(actor.LocationX, actor.LocationY) {
				continue
			}
			id := palBoxID(guildID, actor.LocationX, actor.LocationY)
			if _, ok := seenBox[id]; ok {
				continue
			}
			seenBox[id] = struct{}{}
			name := guildName
			if name == "" {
				name = snap.GuildNames[guildID]
			}
			if name == "" {
				name = "公会"
			}
			snap.PalBoxes = append(snap.PalBoxes, LivePalBox{
				ID:        id,
				GuildID:   guildID,
				GuildName: name,
				X:         actor.LocationX,
				Y:         actor.LocationY,
			})
		}
	}
	return snap
}

func finiteCoord(x, y float64) bool {
	return !math.IsNaN(x) && !math.IsNaN(y) && !math.IsInf(x, 0) && !math.IsInf(y, 0)
}

func palBoxID(guildID string, x, y float64) string {
	// Round to 10 world units to collapse duplicate PalBox spam at one camp.
	rx := int64(math.Round(x / 10))
	ry := int64(math.Round(y / 10))
	return fmt.Sprintf("pb-%s-%d-%d", guildID, rx, ry)
}

// POIsForUser returns live guild_base POIs for the player's current guild (from Player actors).
func (s GameDataSnapshot) POIsForUser(userID string) []LivePalBox {
	userID = strings.TrimSpace(userID)
	if userID == "" || s.PlayerGuild == nil {
		return nil
	}
	guildID := s.PlayerGuild[userID]
	if guildID == "" {
		return nil
	}
	out := make([]LivePalBox, 0)
	for _, box := range s.PalBoxes {
		if box.GuildID == guildID {
			out = append(out, box)
		}
	}
	return out
}
