package app

import (
	"time"
	"volatility-engine/internal/domain/market"
)

type FileSpec struct {
	Path   string
	Expiry time.Time
}

type SnapshotInput struct {
	AsOf         time.Time
	Underlying   string
	Spot         float64
	RiskFreeRate float64
	Files        []FileSpec

	// Optional: stable id parts (useful in replay)
	SnapshotID string
}

type EngineOutput struct {
	Snapshot       *market.Snapshot
	ExpiryContexts []*market.ExpiryContext

	Surface market.IVSurfaceSnapshot
	Regime  market.RegimeState

	Candidates []market.StrategyCandidate
}
