package market

// ChainIntelSnapshot represents high-level positioning intelligence for a single expiry.
type ChainIntelSnapshot struct {
	Expiry  string // YYYY-MM-DD
	Signals []Signal

	PCR           PCRMetrics
	MaxPain       MaxPainMetrics
	OIWalls       OIWalls
	PinRisk       PinRiskMetrics
	SkewAnomalies SkewAnomalyMetrics

	Explanation []string // Human readable summary lines
}

// PCRMetrics holds Put-Call Ratio data.
type PCRMetrics struct {
	OIPCRTotal   float64 // Full chain
	OIPCRNearATM float64 // Near ATM window
	VolumePCR    float64
	IsValid      bool
	Note         string
}

// MaxPainMetrics holds Max Pain calculation results.
type MaxPainMetrics struct {
	Strike     float64
	Confidence float64 // 0.0 to 1.0 (based on data density)
}

// OIWalls identifies strikes with heavy positioning.
type OIWalls struct {
	CallWallStrike float64
	PutWallStrike  float64
	CallWallOI     int64
	PutWallOI      int64

	// Top strikes by OI
	TopCalls []StrikeOI
	TopPuts  []StrikeOI
}

type StrikeOI struct {
	Strike float64
	OI     int64
}

// PinRiskMetrics assesses likelihood of expiration pinning.
type PinRiskMetrics struct {
	PinStrike float64
	Score     float64 // 0-1 score
	IsPinRisk bool    // True if Score > Threshold
}

// SkewAnomalyMetrics flags unusual skew shapes.
type SkewAnomalyMetrics struct {
	IsAnomaly bool
	ZScore    float64  // If history available
	Flags     []string // e.g., "PUT_SKEW_SPIKE"
}

// Signal is a discrete trading signal derived from the data.
type Signal struct {
	Name        string  // e.g. "GAMMA_CONCENTRATION", "PIN_RISK"
	Score       float64 // 0.0 to 1.0 strength
	Explanation string
}
