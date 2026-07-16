package overlay

import "time"

const SchemaV1 = "overlay.snapshot/v1"

type Snapshot struct {
	Schema       string       `json:"schema"`
	GameID       string       `json:"game_id"`
	UserID       string       `json:"user_id"`
	ObservedAt   time.Time    `json:"observed_at"`
	FreshUntil   time.Time    `json:"fresh_until"`
	SourceStatus string       `json:"source_status"`
	Capabilities []string     `json:"capabilities"`
	Identity     Identity     `json:"identity"`
	Latency      *Latency     `json:"latency,omitempty"`
	Timers       []Timer      `json:"timers,omitempty"`
	Map          *MapPosition `json:"map,omitempty"`
}

type Identity struct {
	DisplayName string `json:"display_name"`
	AccountName string `json:"account_name,omitempty"`
	Level       *int   `json:"level,omitempty"`
}

type Latency struct {
	Milliseconds float64 `json:"milliseconds"`
}

type Timer struct {
	ID       string   `json:"id"`
	Label    string   `json:"label"`
	ValueMS  int64    `json:"value_ms"`
	Semantic string   `json:"semantic"`
	Tone     string   `json:"tone"`
	Progress *float64 `json:"progress,omitempty"`
}

type MapPosition struct {
	X          float64 `json:"x"`
	Y          float64 `json:"y"`
	Projection string  `json:"projection"`
	TileSet    string  `json:"tile_set"`
	TileURL    string  `json:"tile_url"`
}
