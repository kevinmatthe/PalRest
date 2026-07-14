package store

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const saveSnapshotSchema = "palrest.save_snapshot.v1"

var saveHexPattern = regexp.MustCompile(`^[0-9A-F]{32}$`)

type SaveSnapshot struct {
	Schema string `json:"schema"`
	Parser struct {
		Name    string `json:"name"`
		Version int    `json:"version"`
	} `json:"parser"`
	Source struct {
		LevelSAV        string `json:"level_sav"`
		Fingerprint     string `json:"fingerprint"`
		LevelSAVSize    int64  `json:"level_sav_size"`
		LevelSAVMTime   string `json:"level_sav_mtime"`
		CapturedAt      string `json:"captured_at"`
		PlayerFileCount int    `json:"player_file_count"`
	} `json:"source"`
	Players []SavePlayer `json:"players"`
	Guilds  []SaveGuild  `json:"guilds"`
}

type SavePlayer struct {
	SavePlayerUID  string  `json:"save_player_uid"`
	SavePlayerHex  string  `json:"save_player_hex"`
	Nickname       string  `json:"nickname"`
	Level          int     `json:"level"`
	Exp            int64   `json:"exp"`
	HP             int64   `json:"hp"`
	ShieldHP       int64   `json:"shield_hp"`
	FullStomach    float64 `json:"full_stomach"`
	SaveLastOnline string  `json:"save_last_online"`
}

type SaveGuild struct {
	SaveGuildID        string            `json:"save_guild_id"`
	Name               string            `json:"name"`
	BaseCampLevel      int               `json:"base_camp_level"`
	AdminSavePlayerUID string            `json:"admin_save_player_uid"`
	AdminSavePlayerHex string            `json:"admin_save_player_hex"`
	Members            []SaveGuildMember `json:"members"`
	BaseCamps          []SaveBaseCamp    `json:"base_camps"`
}

type SaveGuildMember struct {
	SavePlayerUID string `json:"save_player_uid"`
	SavePlayerHex string `json:"save_player_hex"`
	Nickname      string `json:"nickname"`
	LastOnline    string `json:"last_online"`
}

type SaveBaseCamp struct {
	SaveBaseUID  string  `json:"save_base_uid"`
	SaveBaseHex  string  `json:"save_base_hex"`
	SaveGroupUID string  `json:"save_group_uid"`
	SaveGroupHex string  `json:"save_group_hex"`
	Area         float64 `json:"area"`
	LocationX    float64 `json:"location_x"`
	LocationY    float64 `json:"location_y"`
	LocationZ    float64 `json:"location_z"`
}

type SaveImportResult struct {
	ImportID    int64
	Fingerprint string
	Inserted    bool
}

type saveImportModel struct {
	ID            uint   `gorm:"primaryKey"`
	Fingerprint   string `gorm:"uniqueIndex;not null"`
	ParserName    string `gorm:"not null"`
	ParserVersion int    `gorm:"not null"`
	SourcePath    string `gorm:"not null"`
	LevelSAVSize  int64  `gorm:"not null"`
	LevelSAVMTime string `gorm:"not null"`
	CapturedAt    string `gorm:"not null"`
	ImportedAt    string `gorm:"not null;index"`
	PlayerCount   int    `gorm:"not null"`
	GuildCount    int    `gorm:"not null"`
}

func (saveImportModel) TableName() string { return "save_imports" }

type savePlayerModel struct {
	ImportID       uint    `gorm:"primaryKey;autoIncrement:false;index:save_players_latest_lookup,priority:2"`
	SavePlayerUID  string  `gorm:"primaryKey;autoIncrement:false"`
	SavePlayerHex  string  `gorm:"not null;index:save_players_latest_lookup,priority:1"`
	Nickname       string  `gorm:"not null"`
	Level          int     `gorm:"not null"`
	Exp            int64   `gorm:"not null"`
	HP             int64   `gorm:"not null"`
	ShieldHP       int64   `gorm:"not null"`
	FullStomach    float64 `gorm:"not null"`
	SaveLastOnline *string
}

func (savePlayerModel) TableName() string { return "save_players" }

type saveGuildModel struct {
	ImportID           uint   `gorm:"primaryKey;autoIncrement:false"`
	SaveGuildID        string `gorm:"primaryKey;autoIncrement:false"`
	Name               string `gorm:"not null"`
	BaseCampLevel      int    `gorm:"not null"`
	AdminSavePlayerUID string `gorm:"not null"`
	AdminSavePlayerHex string `gorm:"not null"`
}

func (saveGuildModel) TableName() string { return "save_guilds" }

type saveGuildMemberModel struct {
	ImportID      uint   `gorm:"primaryKey;autoIncrement:false;index:save_guild_members_player_lookup,priority:2"`
	SaveGuildID   string `gorm:"primaryKey;autoIncrement:false"`
	SavePlayerUID string `gorm:"primaryKey;autoIncrement:false"`
	SavePlayerHex string `gorm:"not null;index:save_guild_members_player_lookup,priority:1"`
	Nickname      string `gorm:"not null"`
	LastOnline    *string
}

func (saveGuildMemberModel) TableName() string { return "save_guild_members" }

type saveBaseCampModel struct {
	ImportID     uint    `gorm:"primaryKey;autoIncrement:false"`
	SaveBaseUID  string  `gorm:"primaryKey;autoIncrement:false"`
	SaveGuildID  string  `gorm:"not null;index"`
	SaveBaseHex  string  `gorm:"not null"`
	SaveGroupUID string  `gorm:"not null"`
	SaveGroupHex string  `gorm:"not null"`
	Area         float64 `gorm:"not null"`
	LocationX    float64 `gorm:"not null"`
	LocationY    float64 `gorm:"not null"`
	LocationZ    float64 `gorm:"not null"`
}

func (saveBaseCampModel) TableName() string { return "save_base_camps" }

type saveIdentityMappingModel struct {
	SavePlayerHex string `gorm:"primaryKey"`
	UserID        string `gorm:"not null;index"`
	PlayerID      string `gorm:"not null"`
	Method        string `gorm:"not null"`
	Confidence    string `gorm:"not null"`
	FirstImportID uint   `gorm:"not null"`
	UpdatedAt     string `gorm:"not null"`
}

func (saveIdentityMappingModel) TableName() string { return "save_identity_mappings" }

func (r *Repository) migrateSaveModels(ctx context.Context) error {
	db := r.gorm.WithContext(ctx)
	if err := db.AutoMigrate(
		&saveImportModel{},
		&savePlayerModel{},
		&saveGuildModel{},
		&saveGuildMemberModel{},
		&saveBaseCampModel{},
		&saveIdentityMappingModel{},
	); err != nil {
		return fmt.Errorf("migrate save models: %w", err)
	}
	return nil
}

func (r *Repository) ImportSaveSnapshot(ctx context.Context, snapshot SaveSnapshot, importedAt time.Time) (SaveImportResult, error) {
	if importedAt.IsZero() {
		return SaveImportResult{}, fmt.Errorf("import save snapshot: imported time is zero")
	}
	if err := validateSaveSnapshot(snapshot); err != nil {
		return SaveImportResult{}, fmt.Errorf("import save snapshot: %w", err)
	}
	result := SaveImportResult{}
	err := r.gorm.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var existing saveImportModel
		err := tx.Where("fingerprint = ?", snapshot.Source.Fingerprint).First(&existing).Error
		if err == nil {
			result = SaveImportResult{ImportID: int64(existing.ID), Fingerprint: snapshot.Source.Fingerprint, Inserted: false}
			return nil
		}
		if err != nil && err != gorm.ErrRecordNotFound {
			return fmt.Errorf("read existing save import: %w", err)
		}
		model := saveImportModel{
			Fingerprint: snapshot.Source.Fingerprint, ParserName: snapshot.Parser.Name,
			ParserVersion: snapshot.Parser.Version, SourcePath: snapshot.Source.LevelSAV,
			LevelSAVSize: snapshot.Source.LevelSAVSize, LevelSAVMTime: snapshot.Source.LevelSAVMTime,
			CapturedAt: snapshot.Source.CapturedAt, ImportedAt: formatObservationTime(importedAt),
			PlayerCount: len(snapshot.Players), GuildCount: len(snapshot.Guilds),
		}
		if err := tx.Create(&model).Error; err != nil {
			return fmt.Errorf("insert save import: %w", err)
		}
		importID := model.ID
		for _, player := range snapshot.Players {
			value := savePlayerModel{
				ImportID: importID, SavePlayerUID: player.SavePlayerUID, SavePlayerHex: player.SavePlayerHex,
				Nickname: player.Nickname, Level: player.Level, Exp: player.Exp, HP: player.HP,
				ShieldHP: player.ShieldHP, FullStomach: player.FullStomach,
				SaveLastOnline: optionalString(player.SaveLastOnline),
			}
			if err := tx.Create(&value).Error; err != nil {
				return fmt.Errorf("insert save player %q: %w", player.SavePlayerUID, err)
			}
			if err := upsertIdentityMapping(tx, importID, player.SavePlayerHex, importedAt); err != nil {
				return err
			}
		}
		for _, guild := range snapshot.Guilds {
			value := saveGuildModel{
				ImportID: importID, SaveGuildID: guild.SaveGuildID, Name: guild.Name,
				BaseCampLevel: guild.BaseCampLevel, AdminSavePlayerUID: guild.AdminSavePlayerUID,
				AdminSavePlayerHex: guild.AdminSavePlayerHex,
			}
			if err := tx.Create(&value).Error; err != nil {
				return fmt.Errorf("insert save guild %q: %w", guild.SaveGuildID, err)
			}
			for _, member := range guild.Members {
				value := saveGuildMemberModel{
					ImportID: importID, SaveGuildID: guild.SaveGuildID, SavePlayerUID: member.SavePlayerUID,
					SavePlayerHex: member.SavePlayerHex, Nickname: member.Nickname,
					LastOnline: optionalString(member.LastOnline),
				}
				if err := tx.Create(&value).Error; err != nil {
					return fmt.Errorf("insert save guild member %q/%q: %w", guild.SaveGuildID, member.SavePlayerUID, err)
				}
			}
			for _, camp := range guild.BaseCamps {
				value := saveBaseCampModel{
					ImportID: importID, SaveGuildID: guild.SaveGuildID, SaveBaseUID: camp.SaveBaseUID, SaveBaseHex: camp.SaveBaseHex,
					SaveGroupUID: camp.SaveGroupUID, SaveGroupHex: camp.SaveGroupHex,
					Area: camp.Area, LocationX: camp.LocationX, LocationY: camp.LocationY, LocationZ: camp.LocationZ,
				}
				if err := tx.Create(&value).Error; err != nil {
					return fmt.Errorf("insert save base camp %q: %w", camp.SaveBaseUID, err)
				}
			}
		}
		result = SaveImportResult{ImportID: int64(importID), Fingerprint: snapshot.Source.Fingerprint, Inserted: true}
		return nil
	})
	return result, err
}

func upsertIdentityMapping(tx *gorm.DB, importID uint, savePlayerHex string, importedAt time.Time) error {
	var player struct {
		UserID   string
		PlayerID string
	}
	err := tx.Table("players").Select("user_id, player_id").Where("player_id = ?", savePlayerHex).Take(&player).Error
	if err == gorm.ErrRecordNotFound {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read player identity for save %q: %w", savePlayerHex, err)
	}
	mapping := saveIdentityMappingModel{
		SavePlayerHex: savePlayerHex,
		UserID:        player.UserID,
		PlayerID:      player.PlayerID,
		Method:        "rest_player_id_exact",
		Confidence:    "deterministic",
		FirstImportID: importID,
		UpdatedAt:     formatObservationTime(importedAt),
	}
	err = tx.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "save_player_hex"}},
		DoUpdates: clause.AssignmentColumns([]string{"user_id", "player_id", "method", "confidence", "updated_at"}),
	}).Create(&mapping).Error
	if err != nil {
		return fmt.Errorf("upsert save identity mapping %q: %w", savePlayerHex, err)
	}
	return nil
}

func validateSaveSnapshot(snapshot SaveSnapshot) error {
	if snapshot.Schema != saveSnapshotSchema {
		return fmt.Errorf("unsupported schema %q", snapshot.Schema)
	}
	if strings.TrimSpace(snapshot.Parser.Name) == "" || snapshot.Parser.Version <= 0 {
		return fmt.Errorf("parser name and positive version are required")
	}
	if strings.TrimSpace(snapshot.Source.Fingerprint) == "" || strings.TrimSpace(snapshot.Source.LevelSAV) == "" {
		return fmt.Errorf("source fingerprint and level path are required")
	}
	if len(snapshot.Source.Fingerprint) != 64 {
		return fmt.Errorf("source fingerprint must be sha256 hex")
	}
	if snapshot.Source.LevelSAVSize < 0 || snapshot.Source.PlayerFileCount < 0 {
		return fmt.Errorf("source sizes must be nonnegative")
	}
	if !validOptionalTimeString(snapshot.Source.LevelSAVMTime) || !validOptionalTimeString(snapshot.Source.CapturedAt) {
		return fmt.Errorf("source times must be RFC3339")
	}
	seenPlayers := make(map[string]struct{}, len(snapshot.Players))
	for _, player := range snapshot.Players {
		if err := validateSavePlayer(player); err != nil {
			return err
		}
		if _, exists := seenPlayers[player.SavePlayerUID]; exists {
			return fmt.Errorf("duplicate save player %q", player.SavePlayerUID)
		}
		seenPlayers[player.SavePlayerUID] = struct{}{}
	}
	seenGuilds := make(map[string]struct{}, len(snapshot.Guilds))
	for _, guild := range snapshot.Guilds {
		if err := validateSaveGuild(guild); err != nil {
			return err
		}
		if _, exists := seenGuilds[guild.SaveGuildID]; exists {
			return fmt.Errorf("duplicate save guild %q", guild.SaveGuildID)
		}
		seenGuilds[guild.SaveGuildID] = struct{}{}
	}
	return nil
}

func validateSavePlayer(player SavePlayer) error {
	if strings.TrimSpace(player.SavePlayerUID) == "" || !saveHexPattern.MatchString(player.SavePlayerHex) {
		return fmt.Errorf("save player has invalid identity")
	}
	if player.Level < 0 || player.Exp < 0 || player.HP < 0 || player.ShieldHP < 0 || !finite(player.FullStomach) || player.FullStomach < 0 {
		return fmt.Errorf("save player %q has invalid numeric values", player.SavePlayerUID)
	}
	if !validOptionalTimeString(player.SaveLastOnline) {
		return fmt.Errorf("save player %q has invalid last online time", player.SavePlayerUID)
	}
	return nil
}

func validateSaveGuild(guild SaveGuild) error {
	if strings.TrimSpace(guild.SaveGuildID) == "" || guild.BaseCampLevel < 0 {
		return fmt.Errorf("save guild has invalid identity or level")
	}
	if guild.AdminSavePlayerHex != "" && !saveHexPattern.MatchString(guild.AdminSavePlayerHex) {
		return fmt.Errorf("save guild %q has invalid admin player", guild.SaveGuildID)
	}
	seenMembers := make(map[string]struct{}, len(guild.Members))
	for _, member := range guild.Members {
		if strings.TrimSpace(member.SavePlayerUID) == "" || !saveHexPattern.MatchString(member.SavePlayerHex) {
			return fmt.Errorf("save guild %q has invalid member", guild.SaveGuildID)
		}
		if !validOptionalTimeString(member.LastOnline) {
			return fmt.Errorf("save guild %q member %q has invalid last online time", guild.SaveGuildID, member.SavePlayerUID)
		}
		if _, exists := seenMembers[member.SavePlayerUID]; exists {
			return fmt.Errorf("save guild %q has duplicate member %q", guild.SaveGuildID, member.SavePlayerUID)
		}
		seenMembers[member.SavePlayerUID] = struct{}{}
	}
	seenBases := make(map[string]struct{}, len(guild.BaseCamps))
	for _, camp := range guild.BaseCamps {
		if strings.TrimSpace(camp.SaveBaseUID) == "" || !saveHexPattern.MatchString(camp.SaveBaseHex) {
			return fmt.Errorf("save guild %q has invalid base camp", guild.SaveGuildID)
		}
		if !finite(camp.Area) || camp.Area < 0 || !finite(camp.LocationX) || !finite(camp.LocationY) || !finite(camp.LocationZ) {
			return fmt.Errorf("save guild %q base %q has invalid coordinates", guild.SaveGuildID, camp.SaveBaseUID)
		}
		if _, exists := seenBases[camp.SaveBaseUID]; exists {
			return fmt.Errorf("save guild %q has duplicate base %q", guild.SaveGuildID, camp.SaveBaseUID)
		}
		seenBases[camp.SaveBaseUID] = struct{}{}
	}
	return nil
}

type WorldPOI struct {
	ID        string  `json:"id"`
	NameZh    string  `json:"name_zh"`
	Kind      string  `json:"kind"` // "guild_base"
	X         float64 `json:"x"`
	Y         float64 `json:"y"`
	GuildName string  `json:"guild_name,omitempty"`
}

type PlayerWorldPOIs struct {
	UserID string     `json:"user_id"`
	Source string     `json:"source"` // "save_import" | "none"
	AsOf   string     `json:"as_of,omitempty"`
	POIs   []WorldPOI `json:"pois"`
}

func (r *Repository) ListPlayerWorldPOIs(ctx context.Context, userID string) (PlayerWorldPOIs, error) {
	userID = strings.TrimSpace(userID)
	empty := PlayerWorldPOIs{UserID: userID, Source: "none", POIs: []WorldPOI{}}
	if userID == "" {
		return empty, nil
	}
	db := r.gorm.WithContext(ctx)

	var latest saveImportModel
	err := db.Order("imported_at DESC").Limit(1).Take(&latest).Error
	if err == gorm.ErrRecordNotFound {
		return empty, nil
	}
	if err != nil {
		return PlayerWorldPOIs{}, fmt.Errorf("list player world pois: latest import: %w", err)
	}

	var mapping saveIdentityMappingModel
	err = db.Where("user_id = ?", userID).Take(&mapping).Error
	if err == gorm.ErrRecordNotFound {
		return empty, nil
	}
	if err != nil {
		return PlayerWorldPOIs{}, fmt.Errorf("list player world pois: identity mapping: %w", err)
	}

	var members []saveGuildMemberModel
	if err := db.Where("import_id = ? AND save_player_hex = ?", latest.ID, mapping.SavePlayerHex).Find(&members).Error; err != nil {
		return PlayerWorldPOIs{}, fmt.Errorf("list player world pois: guild members: %w", err)
	}
	if len(members) == 0 {
		return empty, nil
	}

	pois := make([]WorldPOI, 0)
	seenGuild := make(map[string]struct{}, len(members))
	for _, member := range members {
		if _, seen := seenGuild[member.SaveGuildID]; seen {
			continue
		}
		seenGuild[member.SaveGuildID] = struct{}{}

		var guild saveGuildModel
		err := db.Where("import_id = ? AND save_guild_id = ?", latest.ID, member.SaveGuildID).Take(&guild).Error
		if err == gorm.ErrRecordNotFound {
			continue
		}
		if err != nil {
			return PlayerWorldPOIs{}, fmt.Errorf("list player world pois: guild %q: %w", member.SaveGuildID, err)
		}

		var camps []saveBaseCampModel
		if err := db.Where("import_id = ? AND save_guild_id = ?", latest.ID, member.SaveGuildID).Find(&camps).Error; err != nil {
			return PlayerWorldPOIs{}, fmt.Errorf("list player world pois: base camps for guild %q: %w", member.SaveGuildID, err)
		}
		for _, camp := range camps {
			pois = append(pois, WorldPOI{
				ID:        "gb-" + guild.SaveGuildID + "-" + camp.SaveBaseUID,
				NameZh:    "公会「" + guild.Name + "」据点",
				Kind:      "guild_base",
				X:         camp.LocationX,
				Y:         camp.LocationY,
				GuildName: guild.Name,
			})
		}
	}
	if len(pois) == 0 {
		return empty, nil
	}
	return PlayerWorldPOIs{
		UserID: userID,
		Source: "save_import",
		AsOf:   latest.ImportedAt,
		POIs:   pois,
	}, nil
}

func optionalString(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func validOptionalTimeString(value string) bool {
	if value == "" {
		return true
	}
	_, err := time.Parse(time.RFC3339, value)
	return err == nil
}
