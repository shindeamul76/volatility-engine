package ingest

// ValidationConfig holds configurable thresholds for quote validation
type ValidationConfig struct {
	// Spread percentage thresholds
	SpreadPctLowPremium float64 // Max spread % for options with mid < LowPremiumThreshold
	SpreadPctNormal     float64 // Max spread % for normal premium options

	// Absolute spread thresholds
	SpreadAbsLowPremium float64 // Max absolute spread for low premium options
	SpreadAbsNormal     float64 // Max absolute spread for normal premium options

	// Premium thresholds
	LowPremiumThreshold float64 // Mid price below this is "low premium"

	// Liquidity thresholds
	OI_Strategy_Min  int64 // Min OI for strategy legs
	Vol_Strategy_Min int64 // Min volume for strategy legs
	OI_Severe_Min    int64 // Min OI for any use (severe illiquid)
	Vol_Severe_Min   int64 // Min volume for any use (severe illiquid)
}

// DefaultValidationConfig returns sensible defaults for NIFTY
func DefaultValidationConfig() ValidationConfig {
	return ValidationConfig{
		SpreadPctLowPremium: 0.25, // 25% for cheap options
		SpreadPctNormal:     0.15, // 15% for normal options

		SpreadAbsLowPremium: 0.30, // ₹0.30 absolute for cheap options
		SpreadAbsNormal:     1.50, // ₹1.50 absolute for normal options

		LowPremiumThreshold: 5.0, // Options < ₹5 are "low premium"

		OI_Strategy_Min:  500,
		Vol_Strategy_Min: 100,
		OI_Severe_Min:    100,
		Vol_Severe_Min:   20,
	}
}
