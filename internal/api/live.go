package api

import (
	"math"
	"net/http"
	"sort"
)

type livePositionDTO struct {
	UserID      string  `json:"user_id"`
	PlayerID    string  `json:"player_id,omitempty"`
	Name        string  `json:"name"`
	AccountName string  `json:"account_name,omitempty"`
	X           float64 `json:"x"`
	Y           float64 `json:"y"`
	Ping        float64 `json:"ping,omitempty"`
	Level       int     `json:"level,omitempty"`
}

func finiteWorldCoord(x, y float64) bool {
	return !math.IsNaN(x) && !math.IsInf(x, 0) && !math.IsNaN(y) && !math.IsInf(y, 0)
}

// getLivePositions returns online players with last polled world coordinates.
// Public read — same trust boundary as GET /players (no IP).
func (s *Server) getLivePositions(w http.ResponseWriter, r *http.Request) {
	snapshots, err := s.snapshots.AllSnapshots(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query_failed", "could not query player state")
		return
	}
	status := s.status.Status()
	players := make([]livePositionDTO, 0, len(snapshots))
	for _, snap := range snapshots {
		if !snap.Online {
			continue
		}
		x, y := snap.Player.LocationX, snap.Player.LocationY
		if !finiteWorldCoord(x, y) {
			continue
		}
		// Zero,zero is a valid rare edge case; still emit — map can show it.
		players = append(players, livePositionDTO{
			UserID:      snap.Player.UserID,
			PlayerID:    snap.Player.PlayerID,
			Name:        snap.Player.Name,
			AccountName: snap.Player.AccountName,
			X:           x,
			Y:           y,
			Ping:        snap.Player.Ping,
			Level:       snap.Player.Level,
		})
	}
	sort.Slice(players, func(i, j int) bool {
		if players[i].Name != players[j].Name {
			return players[i].Name < players[j].Name
		}
		return players[i].UserID < players[j].UserID
	})
	asOf := ""
	if !status.LastSuccess.IsZero() {
		asOf = status.LastSuccess.UTC().Format(timeRFC3339Live)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"as_of":        asOf,
		"online_count": status.OnlineCount,
		"positioned":   len(players),
		"players":      players,
	})
}

const timeRFC3339Live = "2006-01-02T15:04:05Z07:00"
