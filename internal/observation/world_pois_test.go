package observation

import (
	"context"
	"testing"
	"time"

	"github.com/kevinmatt/palworld-playtime-guard/internal/palworld"
	"github.com/kevinmatt/palworld-playtime-guard/internal/store"
)

type savePOIFake struct {
	result   store.PlayerWorldPOIs
	allBases store.GuildBasesCatalog
	err      error
}

func (f savePOIFake) ListPlayerWorldPOIs(_ context.Context, userID string) (store.PlayerWorldPOIs, error) {
	if f.err != nil {
		return store.PlayerWorldPOIs{}, f.err
	}
	out := f.result
	out.UserID = userID
	return out, nil
}

func (f savePOIFake) ListAllGuildBases(context.Context) (store.GuildBasesCatalog, error) {
	if f.err != nil {
		return store.GuildBasesCatalog{}, f.err
	}
	return f.allBases, nil
}

func TestWorldPOIProviderMergesLiveAndSave(t *testing.T) {
	live := NewLiveGameData()
	live.Update(palworld.GameDataSnapshot{
		ObservedAt:  time.Unix(100, 0).UTC(),
		PlayerGuild: map[string]string{"u1": "g1"},
		PalBoxes: []palworld.LivePalBox{
			{ID: "pb-g1-1", GuildID: "g1", GuildName: "Alpha", X: 10, Y: 20},
		},
	})
	provider := &WorldPOIProvider{
		Save: savePOIFake{result: store.PlayerWorldPOIs{
			Source: "save_import",
			AsOf:   "2026-07-14T00:00:00Z",
			POIs: []store.WorldPOI{{
				ID: "gb-g1-base", NameZh: "公会「Alpha」据点", Kind: "guild_base",
				X: 11, Y: 21, GuildName: "Alpha", Source: "save_import",
			}},
		}},
		Live: live,
	}
	got, err := provider.ListPlayerWorldPOIs(context.Background(), "u1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Source != "save_import+game_data" {
		t.Fatalf("source=%s", got.Source)
	}
	if len(got.POIs) != 2 {
		t.Fatalf("pois=%d %+v", len(got.POIs), got.POIs)
	}
}

func TestWorldPOIProviderLiveOnly(t *testing.T) {
	live := NewLiveGameData()
	live.Update(palworld.GameDataSnapshot{
		ObservedAt:  time.Unix(100, 0).UTC(),
		PlayerGuild: map[string]string{"u1": "g1"},
		PalBoxes: []palworld.LivePalBox{
			{ID: "pb-g1-1", GuildID: "g1", GuildName: "Alpha", X: 10, Y: 20},
		},
	})
	provider := &WorldPOIProvider{Live: live}
	got, err := provider.ListPlayerWorldPOIs(context.Background(), "u1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Source != "game_data" || len(got.POIs) != 1 {
		t.Fatalf("got=%+v", got)
	}
}

func TestWorldPOIProviderListAllGuildBasesMerges(t *testing.T) {
	live := NewLiveGameData()
	live.Update(palworld.GameDataSnapshot{
		ObservedAt: time.Unix(100, 0).UTC(),
		PalBoxes: []palworld.LivePalBox{
			{ID: "pb-live", GuildID: "g2", GuildName: "Beta", X: 50, Y: 60},
		},
	})
	provider := &WorldPOIProvider{
		Save: savePOIFake{allBases: store.GuildBasesCatalog{
			Source: "save_import",
			AsOf:   "2026-07-14T00:00:00Z",
			POIs: []store.WorldPOI{{
				ID: "gb-save", NameZh: "公会「Alpha」据点", Kind: "guild_base",
				X: 1, Y: 2, GuildName: "Alpha", GuildID: "g1", Source: "save_import",
			}},
		}},
		Live: live,
	}
	got, err := provider.ListAllGuildBases(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.Source != "save_import+game_data" || len(got.POIs) != 2 {
		t.Fatalf("got=%+v", got)
	}
}
