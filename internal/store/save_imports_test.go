package store

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/kevinmatt/palworld-playtime-guard/internal/domain"
)

func testSaveSnapshot() SaveSnapshot {
	var snapshot SaveSnapshot
	snapshot.Schema = saveSnapshotSchema
	snapshot.Parser.Name = "palrest-palsav-worker"
	snapshot.Parser.Version = 1
	snapshot.Source.LevelSAV = "/saves/Level.sav"
	snapshot.Source.Fingerprint = strings.Repeat("a", 64)
	snapshot.Source.LevelSAVSize = 1024
	snapshot.Source.LevelSAVMTime = "2026-07-13T08:00:00Z"
	snapshot.Source.CapturedAt = "2026-07-13T08:01:00Z"
	snapshot.Source.PlayerFileCount = 1
	snapshot.Players = []SavePlayer{{
		SavePlayerUID: "4041639035", SavePlayerHex: "F0E6847B000000000000000000000000",
		Nickname: "Truthzzz", Level: 63, Exp: 100, HP: 90, ShieldHP: 20,
		FullStomach: 74.2, SaveLastOnline: "2026-07-13T07:59:00Z",
	}}
	snapshot.Guilds = []SaveGuild{{
		SaveGuildID: "guild-1", Name: "Base", BaseCampLevel: 10,
		AdminSavePlayerUID: "4041639035", AdminSavePlayerHex: "F0E6847B000000000000000000000000",
		Members: []SaveGuildMember{{
			SavePlayerUID: "4041639035", SavePlayerHex: "F0E6847B000000000000000000000000",
			Nickname: "Truthzzz", LastOnline: "2026-07-13T07:59:00Z",
		}},
		BaseCamps: []SaveBaseCamp{{
			SaveBaseUID: "1", SaveBaseHex: "00000001000000000000000000000000",
			SaveGroupUID: "2", SaveGroupHex: "00000002000000000000000000000000",
			Area: 3500, LocationX: 12.5, LocationY: -3.25, LocationZ: 99,
		}},
	}}
	return snapshot
}

func TestImportSaveSnapshotPersistsWorldAndIdentityMapping(t *testing.T) {
	repo, _ := openTemp(t)
	at := time.Date(2026, 7, 13, 8, 2, 0, 0, time.UTC)
	if err := repo.WithTx(t.Context(), func(tx *Tx) error {
		return tx.UpsertPlayer(domain.Player{
			UserID: "steam_76561198323313815", PlayerID: "F0E6847B000000000000000000000000",
			Name: "Truthzzz", AccountName: "account",
		}, at)
	}); err != nil {
		t.Fatal(err)
	}
	result, err := repo.ImportSaveSnapshot(t.Context(), testSaveSnapshot(), at)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Inserted || result.ImportID == 0 || result.Fingerprint != strings.Repeat("a", 64) {
		t.Fatalf("result=%+v", result)
	}
	counts := map[string]int{
		"save_imports":           1,
		"save_players":           1,
		"save_guilds":            1,
		"save_guild_members":     1,
		"save_base_camps":        1,
		"save_identity_mappings": 1,
	}
	for table, want := range counts {
		var got int
		if err := repo.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM `+table).Scan(&got); err != nil || got != want {
			t.Fatalf("%s=%d want=%d err=%v", table, got, want, err)
		}
	}
	var userID, method, confidence string
	if err := repo.db.QueryRowContext(t.Context(), `SELECT user_id,method,confidence FROM save_identity_mappings WHERE save_player_hex=?`, "F0E6847B000000000000000000000000").Scan(&userID, &method, &confidence); err != nil {
		t.Fatal(err)
	}
	if userID != "steam_76561198323313815" || method != "rest_player_id_exact" || confidence != "deterministic" {
		t.Fatalf("mapping user=%q method=%q confidence=%q", userID, method, confidence)
	}

	replay, err := repo.ImportSaveSnapshot(t.Context(), testSaveSnapshot(), at.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if replay.Inserted || replay.ImportID != result.ImportID {
		t.Fatalf("replay=%+v original=%+v", replay, result)
	}
}

func TestListPlayerWorldPOIsReturnsGuildBaseFromLatestImport(t *testing.T) {
	repo, _ := openTemp(t)
	at := time.Date(2026, 7, 13, 8, 2, 0, 0, time.UTC)
	userID := "steam_76561198323313815"
	if err := repo.WithTx(t.Context(), func(tx *Tx) error {
		return tx.UpsertPlayer(domain.Player{
			UserID: userID, PlayerID: "F0E6847B000000000000000000000000",
			Name: "Truthzzz", AccountName: "account",
		}, at)
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.ImportSaveSnapshot(t.Context(), testSaveSnapshot(), at); err != nil {
		t.Fatal(err)
	}

	result, err := repo.ListPlayerWorldPOIs(t.Context(), userID)
	if err != nil {
		t.Fatal(err)
	}
	if result.UserID != userID || result.Source != "save_import" || result.AsOf == "" {
		t.Fatalf("result=%+v", result)
	}
	if len(result.POIs) != 1 {
		t.Fatalf("pois=%+v", result.POIs)
	}
	poi := result.POIs[0]
	if poi.ID != "gb-guild-1-1" || poi.Kind != "guild_base" || poi.NameZh != "公会「Base」据点" || poi.GuildName != "Base" {
		t.Fatalf("poi=%+v", poi)
	}
	if poi.X != 12.5 || poi.Y != -3.25 {
		t.Fatalf("coords x=%v y=%v", poi.X, poi.Y)
	}

	none, err := repo.ListPlayerWorldPOIs(t.Context(), "steam_unknown")
	if err != nil {
		t.Fatal(err)
	}
	if none.Source != "none" || len(none.POIs) != 0 {
		t.Fatalf("unknown user=%+v", none)
	}
}

func TestImportSaveSnapshotValidatesContract(t *testing.T) {
	repo, _ := openTemp(t)
	at := time.Date(2026, 7, 13, 8, 2, 0, 0, time.UTC)
	tests := map[string]func(*SaveSnapshot){
		"bad schema":       func(s *SaveSnapshot) { s.Schema = "other" },
		"bad fingerprint":  func(s *SaveSnapshot) { s.Source.Fingerprint = "bad" },
		"bad player hex":   func(s *SaveSnapshot) { s.Players[0].SavePlayerHex = "F0" },
		"negative level":   func(s *SaveSnapshot) { s.Players[0].Level = -1 },
		"bad member time":  func(s *SaveSnapshot) { s.Guilds[0].Members[0].LastOnline = "yesterday" },
		"bad base coords":  func(s *SaveSnapshot) { s.Guilds[0].BaseCamps[0].LocationX = math.Inf(1) },
		"duplicate player": func(s *SaveSnapshot) { s.Players = append(s.Players, s.Players[0]) },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			snapshot := testSaveSnapshot()
			mutate(&snapshot)
			if _, err := repo.ImportSaveSnapshot(t.Context(), snapshot, at); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}
