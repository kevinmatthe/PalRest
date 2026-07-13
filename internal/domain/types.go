package domain

import "time"

type Player struct {
	UserID        string    `json:"user_id"`
	PlayerID      string    `json:"player_id"`
	Name          string    `json:"name"`
	AccountName   string    `json:"account_name"`
	LastOnline    time.Time `json:"last_online"`
	IP            string    `json:"-"`
	Ping          float64   `json:"-"`
	LocationX     float64   `json:"-"`
	LocationY     float64   `json:"-"`
	Level         int       `json:"-"`
	BuildingCount int       `json:"-"`
}

type ServerMetrics struct {
	ServerFPS        int     `json:"serverfps"`
	CurrentPlayerNum int     `json:"currentplayernum"`
	ServerFrameTime  float64 `json:"serverframetime"`
	MaxPlayerNum     int     `json:"maxplayernum"`
	UptimeSeconds    int64   `json:"uptime"`
	BaseCampNum      int     `json:"basecampnum"`
	Days             int     `json:"days"`
}

type ServerInfo struct {
	Version     string `json:"version"`
	ServerName  string `json:"servername"`
	Description string `json:"description"`
	WorldGUID   string `json:"worldguid"`
}

type ServerSettings struct {
	Values map[string]any
}

type ResolvedPolicy struct {
	Revision            string
	Enabled             bool
	Exempt              bool
	Strategy            string
	PeriodType          string
	Timezone            string
	ResetAt             string
	ResetWeekday        string
	Limit               time.Duration
	CooldownEvery       time.Duration
	CooldownRest        time.Duration
	CreditRecoverEvery  time.Duration
	CreditRecoverAmount time.Duration
	CreditMax           time.Duration
	WarningBefore       []time.Duration
}

type Period struct {
	Key   string    `json:"key"`
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}

type Usage struct {
	UserID  string
	Period  Period
	Used    time.Duration
	Updated time.Time
}

type WarningState struct {
	Threshold   time.Duration
	Status      string
	Attempts    int
	NextAttempt time.Time
}

type EnforcementState struct {
	Status      string
	Attempts    int
	NextAttempt time.Time
	Generation  int64
}

type PlayerSnapshot struct {
	Player              Player
	Policy              ResolvedPolicy
	Period              Period
	Used                time.Duration
	Remaining           time.Duration
	LastCreditRecovered time.Duration
	Warnings            []WarningState
	Enforcement         EnforcementState
	Online              bool
}

type PollStatus struct {
	StartedAt       time.Time `json:"started_at"`
	LastAttempt     time.Time `json:"last_attempt,omitempty"`
	LastSuccess     time.Time `json:"last_success,omitempty"`
	LastError       string    `json:"last_error,omitempty"`
	OnlineCount     int       `json:"online_count"`
	ConfigVersion   int       `json:"config_version"`
	ConfigReloadErr string    `json:"config_reload_error,omitempty"`
}
