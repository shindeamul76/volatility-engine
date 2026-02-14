package market

import (
	"time"
)

// ExpiryContext represents all knowledge about a single expiry throughout the pipeline.
// It serves as the container for data across the 3-pass architecture.
type ExpiryContext struct {
	// Identity
	ID     string // Unique ID for this context
	Expiry ExpiryMetadata
	AsOf   time.Time

	// Artifacts from Pass 1 (Parallel Construction)
	ChainSnapshot *ChainSnapshot
	ForwardState  *ForwardState
	IVPoints      []IVPoint       // All converged points
	SkewSnapshot  *IVSkewSnapshot // Nil if skew generation failed

	// Artifacts from Pass 3 (Intel & Strategy)
	IntelSnapshot      *ChainIntelSnapshot
	StrategyCandidates []StrategyCandidate

	// Diagnostics & Flow Control
	Stage      string   // e.g., "Constructed", "Skewed", "Intel", "Complete"
	SkipReason string   // If set, this context is invalid/skipped
	Warnings   []string // Non-critical issues
}
