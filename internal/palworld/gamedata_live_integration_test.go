//go:build live

package palworld

import (
	"context"
	"encoding/base64"
	"os"
	"strings"
	"testing"
	"time"
)

// Run with:
//
//	PAL_BASIC='...' PAL_BASE_URL='https://host/v1/api' go test -tags=live ./internal/palworld/ -run TestLiveGameDataFetch -v
func TestLiveGameDataFetch(t *testing.T) {
	auth := os.Getenv("PAL_BASIC")
	base := os.Getenv("PAL_BASE_URL")
	if auth == "" || base == "" {
		t.Skip("set PAL_BASIC (base64 user:pass) and PAL_BASE_URL to run live game-data check")
	}
	raw, err := base64.StdEncoding.DecodeString(auth)
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.SplitN(string(raw), ":", 2)
	if len(parts) != 2 {
		t.Fatal("bad basic auth payload")
	}
	c := New(base, parts[1], 60*time.Second)
	snap, err := c.GameData(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.PlayerGuild) == 0 {
		t.Fatalf("expected online players with guild, got 0")
	}
	if len(snap.PalBoxes) == 0 {
		t.Fatalf("expected pal boxes")
	}
	t.Logf("players=%d boxes=%d days=%d", len(snap.PlayerGuild), len(snap.PalBoxes), snap.InGameDays)
	for u, g := range snap.PlayerGuild {
		t.Logf("user=%s guild=%s bases=%d", u, g, len(snap.POIsForUser(u)))
	}
}
