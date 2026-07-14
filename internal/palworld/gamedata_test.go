package palworld

import (
	"testing"
	"time"
)

func TestNormalizeGameDataExtractsPlayersAndPalBoxes(t *testing.T) {
	payload := gameDataPayload{
		InGameTime: "11:05",
		InGameDays: 222,
		ActorData: []gameDataActor{
			{
				Type: "Character", UnitType: "Player", UserID: "steam_1",
				GuildID: "g1", GuildName: "Alpha", LocationX: 1, LocationY: 2,
			},
			{
				Type: "PalBox", GuildID: "g1", GuildName: "Alpha",
				LocationX: -835118, LocationY: 26611.9,
			},
			{
				Type: "PalBox", GuildID: "g1", GuildName: "Alpha",
				LocationX: -835119, LocationY: 26612, // same camp after round
			},
			{
				Type: "PalBox", GuildID: "g2", GuildName: "Beta",
				LocationX: 100, LocationY: 200,
			},
			{
				Type: "Character", UnitType: "BaseCampPal", GuildID: "g1",
				LocationX: 0, LocationY: 0,
			},
		},
	}
	snap := normalizeGameData(payload, time.Unix(1, 0).UTC())
	if snap.PlayerGuild["steam_1"] != "g1" {
		t.Fatalf("player guild=%v", snap.PlayerGuild)
	}
	if len(snap.PalBoxes) != 2 {
		t.Fatalf("palboxes=%d want 2 (deduped)", len(snap.PalBoxes))
	}
	pois := snap.POIsForUser("steam_1")
	if len(pois) != 1 {
		t.Fatalf("pois for steam_1=%d", len(pois))
	}
	if pois[0].GuildName != "Alpha" {
		t.Fatalf("name=%s", pois[0].GuildName)
	}
	if len(snap.POIsForUser("missing")) != 0 {
		t.Fatal("expected no pois for missing user")
	}
}
