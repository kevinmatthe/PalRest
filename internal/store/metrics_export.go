package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/kevinmatt/palworld-playtime-guard/internal/domain"
	"gorm.io/gorm"
)

// MetricsExport is a scrape-time snapshot of durable game/server facts for Prometheus.
type MetricsExport struct {
	ServerAt       time.Time
	Server         *domain.ServerMetrics
	Runtime        ServerRuntimeState
	RestartEvents  int64
	InfoAt         time.Time
	Info           *domain.ServerInfo
	SettingsAt     time.Time
	SettingsScalar map[string]float64
	Save           *SaveMetricsExport
}

// SaveMetricsExport is the latest save-import game-data snapshot.
type SaveMetricsExport struct {
	ImportID     int64
	ImportedAt   time.Time
	LevelSAVSize int64
	PlayerCount  int
	GuildCount   int
	BaseCampCount int
	BaseCampAreaSum float64
	Players      []SavePlayerMetric
	Guilds       []SaveGuildMetric
}

// SavePlayerMetric is one character from the latest save import.
type SavePlayerMetric struct {
	UserID      string // may be empty when not mapped to REST identity
	SaveUID     string
	Name        string
	Level       int
	Exp         int64
	HP          int64
	ShieldHP    int64
	FullStomach float64
	LastOnline  time.Time // zero if unknown
}

// SaveGuildMetric is one guild from the latest save import.
type SaveGuildMetric struct {
	GuildID       string
	Name          string
	BaseCampLevel int
	MemberCount   int
	BaseCampCount int
	BaseCampArea  float64
}

// LoadMetricsExport gathers durable metrics for /metrics. Missing optional
// streams yield zero values / nil pointers rather than hard errors.
func (r *Repository) LoadMetricsExport(ctx context.Context) (MetricsExport, error) {
	var out MetricsExport

	at, metrics, err := r.LatestServerMetrics(ctx)
	if err == nil {
		m := metrics
		out.ServerAt = at
		out.Server = &m
	} else if !errors.Is(err, ErrNotFound) {
		return MetricsExport{}, fmt.Errorf("metrics export: server metrics: %w", err)
	}

	runtime, err := r.CurrentServerRuntime(ctx)
	if err != nil {
		return MetricsExport{}, fmt.Errorf("metrics export: runtime: %w", err)
	}
	out.Runtime = runtime

	if err := r.db.QueryRowContext(ctx, `
SELECT COUNT(*) FROM activity_events WHERE event_type='server_restarted'`).Scan(&out.RestartEvents); err != nil {
		return MetricsExport{}, fmt.Errorf("metrics export: restart count: %w", err)
	}

	if snap, err := r.LatestServerDocument(ctx, "info"); err == nil {
		out.InfoAt = snap.At
		if info, parseErr := decodeServerInfoJSON(snap.Canonical); parseErr == nil {
			out.Info = &info
		}
	} else if !errors.Is(err, ErrNotFound) {
		return MetricsExport{}, fmt.Errorf("metrics export: info: %w", err)
	}

	if snap, err := r.LatestServerDocument(ctx, "settings"); err == nil {
		out.SettingsAt = snap.At
		out.SettingsScalar = extractSettingsScalars(snap.Canonical)
	} else if !errors.Is(err, ErrNotFound) {
		return MetricsExport{}, fmt.Errorf("metrics export: settings: %w", err)
	}

	save, err := r.loadLatestSaveMetrics(ctx)
	if err != nil {
		return MetricsExport{}, err
	}
	out.Save = save
	return out, nil
}

func decodeServerInfoJSON(canonical []byte) (domain.ServerInfo, error) {
	var raw struct {
		Version     string `json:"version"`
		ServerName  string `json:"servername"`
		Description string `json:"description"`
		WorldGUID   string `json:"worldguid"`
	}
	if err := json.Unmarshal(canonical, &raw); err != nil {
		return domain.ServerInfo{}, err
	}
	return domain.ServerInfo{
		Version: raw.Version, ServerName: raw.ServerName,
		Description: raw.Description, WorldGUID: raw.WorldGUID,
	}, nil
}

// Whitelist of Palworld WorldSettings-style numeric/bool keys safe to expose.
var settingsScalarKeys = []string{
	"DayTimeSpeedRate",
	"NightTimeSpeedRate",
	"ExpRate",
	"PalCaptureRate",
	"PalSpawnNumRate",
	"PalDamageRateAttack",
	"PalDamageRateDefense",
	"PlayerDamageRateAttack",
	"PlayerDamageRateDefense",
	"PlayerStomachDecreaceRate",
	"PlayerStaminaDecreaceRate",
	"PlayerAutoHPRegeneRate",
	"PlayerAutoHpRegeneRateInSleep",
	"PalStomachDecreaceRate",
	"PalStaminaDecreaceRate",
	"PalAutoHPRegeneRate",
	"PalAutoHpRegeneRateInSleep",
	"BuildObjectDamageRate",
	"BuildObjectDeteriorationDamageRate",
	"CollectionDropRate",
	"CollectionObjectHpRate",
	"CollectionObjectRespawnSpeedRate",
	"EnemyDropItemRate",
	"DropItemMaxNum",
	"DropItemMaxNum_UNKO",
	"BaseCampMaxNum",
	"BaseCampWorkerMaxNum",
	"DropItemAliveMaxHours",
	"AutoResetGuildTimeNoOnlinePlayers",
	"GuildPlayerMaxNum",
	"PalEggDefaultHatchingTime",
	"WorkSpeedRate",
	"CoopPlayerMaxNum",
	"ServerPlayerMaxNum",
	"bEnableInvaderEnemy",
	"bEnableFastTravel",
	"bIsStartLocationSelectByMap",
	"bExistPlayerAfterLogout",
	"bEnableDefenseOtherGuildPlayer",
	"bInvisibleOtherGuildBaseCampAreaFX",
	"bBuildAreaLimit",
	"MaxBuildingLimitNum",
	"ServerReplicatePawnCullDistance",
}

func extractSettingsScalars(canonical []byte) map[string]float64 {
	var root map[string]any
	if err := json.Unmarshal(canonical, &root); err != nil {
		return nil
	}
	// Settings may be nested under Difficulty or flat.
	candidates := []map[string]any{root}
	if nested, ok := root["Difficulty"].(map[string]any); ok {
		candidates = append(candidates, nested)
	}
	// Some exports wrap under "OptionSettings" / "WorldSettings".
	for _, key := range []string{"OptionSettings", "WorldSettings", "ServerSettings"} {
		if nested, ok := root[key].(map[string]any); ok {
			candidates = append(candidates, nested)
		}
	}
	out := make(map[string]float64)
	for _, key := range settingsScalarKeys {
		for _, m := range candidates {
			if v, ok := m[key]; ok {
				if f, ok := coerceFloat(v); ok {
					out[key] = f
					break
				}
			}
		}
	}
	return out
}

func coerceFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return 0, false
		}
		return x, true
	case float32:
		return float64(x), true
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	case json.Number:
		f, err := x.Float64()
		return f, err == nil
	case bool:
		if x {
			return 1, true
		}
		return 0, true
	case string:
		// ignore enums / free text
		return 0, false
	default:
		return 0, false
	}
}

func (r *Repository) loadLatestSaveMetrics(ctx context.Context) (*SaveMetricsExport, error) {
	db := r.gorm.WithContext(ctx)
	var latest saveImportModel
	err := db.Order("imported_at DESC").Limit(1).Take(&latest).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("metrics export: latest save import: %w", err)
	}
	importedAt, err := time.Parse(time.RFC3339, latest.ImportedAt)
	if err != nil {
		// tolerate non-RFC3339 storage if any
		importedAt, err = parseTime(latest.ImportedAt)
		if err != nil {
			importedAt = time.Time{}
		}
	}

	var players []savePlayerModel
	if err := db.Where("import_id = ?", latest.ID).Find(&players).Error; err != nil {
		return nil, fmt.Errorf("metrics export: save players: %w", err)
	}
	var guilds []saveGuildModel
	if err := db.Where("import_id = ?", latest.ID).Find(&guilds).Error; err != nil {
		return nil, fmt.Errorf("metrics export: save guilds: %w", err)
	}
	var members []saveGuildMemberModel
	if err := db.Where("import_id = ?", latest.ID).Find(&members).Error; err != nil {
		return nil, fmt.Errorf("metrics export: save members: %w", err)
	}
	var camps []saveBaseCampModel
	if err := db.Where("import_id = ?", latest.ID).Find(&camps).Error; err != nil {
		return nil, fmt.Errorf("metrics export: save camps: %w", err)
	}
	var mappings []saveIdentityMappingModel
	_ = db.Find(&mappings).Error
	hexToUser := make(map[string]string, len(mappings))
	for _, m := range mappings {
		hexToUser[m.SavePlayerHex] = m.UserID
	}

	memberCount := make(map[string]int)
	for _, m := range members {
		memberCount[m.SaveGuildID]++
	}
	campCount := make(map[string]int)
	campArea := make(map[string]float64)
	var areaSum float64
	for _, c := range camps {
		campCount[c.SaveGuildID]++
		campArea[c.SaveGuildID] += c.Area
		areaSum += c.Area
	}

	out := &SaveMetricsExport{
		ImportID:        int64(latest.ID),
		ImportedAt:      importedAt.UTC(),
		LevelSAVSize:    latest.LevelSAVSize,
		PlayerCount:     len(players),
		GuildCount:      len(guilds),
		BaseCampCount:   len(camps),
		BaseCampAreaSum: areaSum,
		Players:         make([]SavePlayerMetric, 0, len(players)),
		Guilds:          make([]SaveGuildMetric, 0, len(guilds)),
	}
	for _, p := range players {
		name := p.Nickname
		if name == "" {
			name = p.SavePlayerUID
		}
		row := SavePlayerMetric{
			UserID:      hexToUser[p.SavePlayerHex],
			SaveUID:     p.SavePlayerUID,
			Name:        name,
			Level:       p.Level,
			Exp:         p.Exp,
			HP:          p.HP,
			ShieldHP:    p.ShieldHP,
			FullStomach: p.FullStomach,
		}
		if p.SaveLastOnline != nil && *p.SaveLastOnline != "" {
			if t, err := time.Parse(time.RFC3339, *p.SaveLastOnline); err == nil {
				row.LastOnline = t.UTC()
			}
		}
		out.Players = append(out.Players, row)
	}
	for _, g := range guilds {
		name := g.Name
		if name == "" {
			name = g.SaveGuildID
		}
		out.Guilds = append(out.Guilds, SaveGuildMetric{
			GuildID:       g.SaveGuildID,
			Name:          name,
			BaseCampLevel: g.BaseCampLevel,
			MemberCount:   memberCount[g.SaveGuildID],
			BaseCampCount: campCount[g.SaveGuildID],
			BaseCampArea:  campArea[g.SaveGuildID],
		})
	}
	return out, nil
}
