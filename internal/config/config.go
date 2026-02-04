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

// Config is the root configuration structure
type Config struct {
	Market    MarketConfig    `yaml:"market"`
	Pricing   PricingConfig   `yaml:"pricing"`
	Selection SelectionConfig `yaml:"selection"`
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
