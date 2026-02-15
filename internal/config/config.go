package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// FileConfig represents a CSV file to ingest with its expiry
type FileConfig struct {
	Path   string `yaml:"path"`
	Expiry string `yaml:"expiry"` // e.g., "2026-02-10T15:30:00"
}

// MarketConfig holds market data configuration
type MarketConfig struct {
	Underlying string  `yaml:"underlying"`
	Spot       float64 `yaml:"spot"`
	Timezone   string  `yaml:"timezone"` // e.g., "Asia/Kolkata"
}

// PricingConfig holds pricing parameters
type PricingConfig struct {
	RiskFreeRate  float64 `yaml:"risk_free_rate"`
	DividendYield float64 `yaml:"dividend_yield"`
}

// SelectionConfig holds heuristics for strike and expiry filtering
type SelectionConfig struct {
	LogMoneynessWindow    float64 `yaml:"log_moneyness_window"`     // e.g., 0.10
	MinPointsPerExpiry    int     `yaml:"min_points_per_expiry"`    // e.g., 20
	TargetPointsPerExpiry int     `yaml:"target_points_per_expiry"` // e.g., 50
	MaxPointsPerExpiry    int     `yaml:"max_points_per_expiry"`    // e.g., 60
}

type ScenarioConfig struct {
	SpotShocks     []float64 `yaml:"spot_shocks"`
	VolShocksAbs   []float64 `yaml:"vol_shocks_abs"`
	TimeShiftsDays []int     `yaml:"time_shifts_days"`
}

type PayoffConfig struct {
	GridMinMult float64 `yaml:"grid_min_mult"`
	GridMaxMult float64 `yaml:"grid_max_mult"`
	GridSteps   int     `yaml:"grid_steps"`
}

type RiskConfig struct {
	ContractMultiplier float64        `yaml:"contract_multiplier"`
	Scenario           ScenarioConfig `yaml:"scenario"`
	Payoff             PayoffConfig   `yaml:"payoff"`
}

type ReportConfig struct {
	TopN    int      `yaml:"top_n"`
	Formats []string `yaml:"formats"`
	OutDir  string   `yaml:"out_dir"`
	Workers int      `yaml:"workers"`
}

type DeciderConfig struct {
	MaxOpenPositions int     `yaml:"max_open_positions"`
	MinScore         float64 `yaml:"min_score"`
	TakeProfit       float64 `yaml:"take_profit"`
	StopLoss         float64 `yaml:"stop_loss"`
	RollMinScoreDiff float64 `yaml:"roll_min_score_diff"`
}

type ExecutionConfig struct {
	TickSize float64        `yaml:"tick_size"`
	Slippage SlippageConfig `yaml:"slippage"`
	Fees     FeeConfig      `yaml:"fees"`
}

type FeeConfig struct {
	PerLeg   float64 `yaml:"per_leg"`
	PerOrder float64 `yaml:"per_order"`
	Bps      float64 `yaml:"bps"`
}

type SlippageConfig struct {
	Mode  string  `yaml:"mode"` // "none" | "bps" | "ticks"
	Bps   float64 `yaml:"bps"`
	Ticks float64 `yaml:"ticks"`
}

type ReplayConfig struct {
	CloseAllAtEnd    *bool   `yaml:"close_all_at_end"`
	MaxOpenPositions int     `yaml:"max_open_positions"`
	MinScore         float64 `yaml:"min_score"`
	TakeProfit       float64 `yaml:"take_profit"`
	StopLoss         float64 `yaml:"stop_loss"`
	RollMinScoreDiff float64 `yaml:"roll_min_score_diff"`
}

// Config is the root configuration structure
type Config struct {
	Market    MarketConfig    `yaml:"market"`
	Pricing   PricingConfig   `yaml:"pricing"`
	Selection SelectionConfig `yaml:"selection"`
	Risk      RiskConfig      `yaml:"risk"`
	Report    ReportConfig    `yaml:"report"`
	Replay    ReplayConfig    `yaml:"replay"`
	Decider   DeciderConfig   `yaml:"decider"`
	Execution ExecutionConfig `yaml:"execution"`
	Files     []FileConfig    `yaml:"files"`
}

// Load reads and parses a YAML configuration file
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	// Validate
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	return &cfg, nil
}

// Validate checks if the configuration is valid
func (c *Config) Validate() error {
	if c.Market.Underlying == "" {
		return fmt.Errorf("market.underlying is required")
	}
	if c.Market.Spot <= 0 {
		return fmt.Errorf("market.spot must be positive")
	}
	if c.Market.Timezone == "" {
		return fmt.Errorf("market.timezone is required")
	}
	if c.Pricing.RiskFreeRate < 0 {
		return fmt.Errorf("pricing.risk_free_rate cannot be negative")
	}
	if len(c.Files) == 0 {
		return fmt.Errorf("at least one file must be configured")
	}

	// Selection defaults and validation
	if c.Selection.LogMoneynessWindow <= 0 {
		c.Selection.LogMoneynessWindow = 0.10
	}
	if c.Selection.MinPointsPerExpiry <= 0 {
		c.Selection.MinPointsPerExpiry = 20
	}
	if c.Selection.TargetPointsPerExpiry <= 0 {
		c.Selection.TargetPointsPerExpiry = 50
	}
	if c.Selection.MaxPointsPerExpiry <= 0 {
		c.Selection.MaxPointsPerExpiry = 60
	}

	if c.Selection.MinPointsPerExpiry > c.Selection.MaxPointsPerExpiry {
		return fmt.Errorf("selection.min_points_per_expiry cannot be greater than max_points_per_expiry")
	}

	// Risk Defaults
	if c.Risk.ContractMultiplier <= 0 {
		c.Risk.ContractMultiplier = 1.0 // Default to 1 if not set
	}
	if c.Risk.Payoff.GridSteps <= 0 {
		c.Risk.Payoff.GridSteps = 100
	}
	if c.Report.TopN <= 0 {
		c.Report.TopN = 3
	}
	if c.Report.OutDir == "" {
		c.Report.OutDir = "reports"
	}
	if c.Report.Workers <= 0 {
		c.Report.Workers = 1
	}

	// Replay Defaults
	if c.Replay.CloseAllAtEnd == nil {
		def := true
		c.Replay.CloseAllAtEnd = &def
	}
	if c.Replay.MaxOpenPositions <= 0 {
		c.Replay.MaxOpenPositions = 1
	}
	if c.Replay.MinScore <= 0 {
		c.Replay.MinScore = 0.50
	}
	if c.Replay.TakeProfit <= 0 {
		c.Replay.TakeProfit = 0.30
	}
	if c.Replay.StopLoss <= 0 {
		c.Replay.StopLoss = 0.50
	}
	if c.Replay.RollMinScoreDiff <= 0 {
		c.Replay.RollMinScoreDiff = 0.10
	}

	// Validate each file config
	for i, f := range c.Files {
		if f.Path == "" {
			return fmt.Errorf("files[%d].path is required", i)
		}
		if f.Expiry == "" {
			return fmt.Errorf("files[%d].expiry is required", i)
		}
		// Try parsing the expiry using the helper to ensure it's valid
		if _, err := c.ParseFileExpiry(f.Expiry); err != nil {
			return fmt.Errorf("files[%d].expiry is invalid: %w", i, err)
		}
	}

	return nil
}

// GetLocation parses the timezone and returns a *time.Location
func (c *Config) GetLocation() (*time.Location, error) {
	return time.LoadLocation(c.Market.Timezone)
}

// ParseFileExpiry parses a file's expiry string into a time.Time in the configured timezone
func (c *Config) ParseFileExpiry(fileExpiry string) (time.Time, error) {
	loc, err := c.GetLocation()
	if err != nil {
		return time.Time{}, err
	}

	// Parse as RFC3339 and convert to configured timezone
	t, err := time.Parse(time.RFC3339, fileExpiry)
	if err != nil {
		// Try parsing without timezone info, assume configured timezone
		t, err = time.ParseInLocation("2006-01-02T15:04:05", fileExpiry, loc)
		if err != nil {
			return time.Time{}, fmt.Errorf("failed to parse expiry: %w", err)
		}
	}

	return t.In(loc), nil
}
