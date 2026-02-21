package replay

import (
	"math"
	"time"
	"volatility-engine/internal/domain/market"
)

type SnapshotDescriptor struct {
	AsOf     time.Time
	Folder   string
	Manifest string
}

type SnapshotManifest struct {
	AsOf         time.Time
	Underlying   string
	Spot         float64
	RiskFreeRate float64
	Files        []struct {
		Path   string
		Expiry time.Time
	}
}

// Orders / fills
type OrderAction string

const (
	ActionOpen  OrderAction = "OPEN"
	ActionClose OrderAction = "CLOSE"
)

// ReplayLeg extends StrategyLeg with Expiry for execution
type ReplayLeg struct {
	Side       string            `json:"side"`
	Type       market.OptionType `json:"type"`
	Strike     float64           `json:"strike"`
	Qty        int               `json:"qty"`
	Expiry     string            `json:"expiry"`      // YYYY-MM-DD
	ExpiryTime time.Time         `json:"expiry_time"` // 15:30 IST
}

type OrderIntent struct {
	PositionID string
	StrategyID string
	Action     OrderAction
	Legs       []ReplayLeg
}

// LegFill details execution of a single leg
type LegFill struct {
	Leg          ReplayLeg
	FillPrice    float64
	FillBasis    string  // "BID", "ASK", "MID"
	SlippageMode string  // "ticks", "bps"
	SlippageAmt  float64 // applied slippage value (absolute amount)
	TickSize     float64
}

type Fill struct {
	AsOf       time.Time
	PositionID string
	StrategyID string
	Action     OrderAction
	LegFills   []LegFill
	NetPremium float64 // credit positive
	FeeINR     float64 // Total fees in INR
}

// RoundINR rounds a float to 2 decimal places to prevent drift
func RoundINR(val float64) float64 {
	return math.Round(val*100) / 100
}
