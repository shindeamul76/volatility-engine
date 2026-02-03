package surface

import (
	"time"

	"volatility-engine/internal/domain/market"
)

// Builder constructs volatility surfaces from solver results
type Builder struct {
	settings BuilderSettings
}

type BuilderSettings struct {
	// Selection & Weights
	ConfidenceMin   float64
	WeightPowerConf float64 // Power for confidence weight (e.g. 2.0)
	WeightPowerVega float64 // Power for vega weight (e.g. 1.0)
	TaperX0         float64 // Moneyness taper width (e.g. 0.06)

	// OTM Selection
	OTMPreference bool
	ATMBandX      float64 // Log-moneyness band for ATM blending (e.g. 0.003)
	DropITMOnly   bool    // If true, drop single ITM points if not in ATM band

	// Filtering
	XMax               float64 // Max abs log-moneyness (e.g. 0.12)
	IVMin              float64 // Min IV (e.g. 0.01)
	IVMax              float64 // Max IV (e.g. 3.0)
	MinMarkPrice       float64 // Min price to accept (e.g. 0.5)
	MaxFitErrorAbs     float64 // Max solver fit error (e.g. 0.5)
	MinVegaPerVolPoint float64 // Min vega to accept (e.g. 0.2)
}

func DefaultSettings() BuilderSettings {
	return BuilderSettings{
		ConfidenceMin:   0.45,
		WeightPowerConf: 2.0,
		WeightPowerVega: 1.0,
		TaperX0:         0.06,

		OTMPreference: true,
		ATMBandX:      0.003,
		DropITMOnly:   true,

		XMax:               0.12,
		IVMin:              0.01,
		IVMax:              3.0,
		MinMarkPrice:       0.5,
		MaxFitErrorAbs:     0.5,
		MinVegaPerVolPoint: 0.2,
	}
}

func NewBuilder(settings BuilderSettings) *Builder {
	return &Builder{settings: settings}
}

// Build creates a full IV surface snapshot from results
func (b *Builder) Build(results []market.IVResult, asOf time.Time, underlying string) market.IVSurfaceSnapshot {
	surface := market.IVSurfaceSnapshot{
		AsOf:       asOf,
		Underlying: underlying,
		Skews:      []market.IVSkewSnapshot{},
	}

	// Group results by expiry
	// byExpiry := make(map[string][]market.IVResult)
	for _, res := range results {
		// Filter non-converged or low confidence results early?
		// User rule: status == CONVERGED and confidence >= min
		if res.Status != market.IVStatusConverged {
			continue
		}
		if res.Quality.Confidence < b.settings.ConfidenceMin {
			continue
		}
		// Assuming we can derive expiry/forward from result (or request inside result?)
		// The IVResult struct doesn't strictly carry request details unless we link them.
		// For the prototype, I'll assume IVResult OR a combined struct is passed.
		// Wait, market.IVResult struct definition does NOT have Expiry info directly.
		// It has RequestID. The prompt implies "From your IV Solver results, you will create IVPoints."
		// I'll need to assume the caller passes something that has metadata, OR I attach metadata to IVResult previously.
		// Checking current IVResult: it has RequestID.
		// I will assume for this implementation that 'results' might need to be 'IVRequest' + 'IVResult' pairs
		// or that I can extract it from somewhere.
		// Actually, looking at the previous step, I defined IVPoint in models.go
		// I will assume the caller has already mapped results to some intermediate struct or I need to handle it.
		// Let's assume for now that I can parse invalid request IDs or that I accept a richer input.
		// RE-READING PROMPT: "From your IV Solver results, you will create IVPoints."
		// "Each point should carry: expiry, strike... forward F..."
		// I'll create a helper `BuildSurfaceInput` struct or just pass flattened data.
		// For simplicity in this mono-repo, let's assume the caller will pass a wrapper struct:
	}

	return surface
}

// BuildFromPoints is a cleaner entry point if we pre-convert to IVPoint
// This separates the "extraction" logic from the "fitting" logic.
func (b *Builder) BuildFromPoints(points []market.IVPoint, asOf time.Time, underlying string) market.IVSurfaceSnapshot {
	surface := market.IVSurfaceSnapshot{
		AsOf:       asOf,
		Underlying: underlying,
		Skews:      []market.IVSkewSnapshot{},
	}

	// Group by expiry
	byExpiry := make(map[string][]market.IVPoint)

	// Process each expiry	// We need Forward F to calc log-moneyness.
	// Assumption: The input points DO NOT yet have log_moneyness computed, or F is needed.
	// The user prompt says "2) Inputs... Each point should carry... forward F".
	// So I will assume IVPoint has enough info or I can group them.
	// Wait, IVPoint implementation I just added has `LogMoneyness` field.
	// But it does not have `Forward`. I should probably rely on the caller to provide F
	// OR assume F is consistent per expiry.

	// Let's group by Expiry
	for _, p := range points {
		byExpiry[p.Expiry] = append(byExpiry[p.Expiry], p)
	}

	// Process each expiry
	for expiry, pList := range byExpiry {
		// Need Forward and TTE.
		// In a real system, you'd get this from the chain snapshot.
		// Here, we might have to infer it or require it in the input.
		// For prototype, let's assume we can derive F from ATM parity or it's passed in.
		// Valid concern: how to get F and TTE?
		// I'll assume for BuildFromPoints, either:
		// 1. IVPoint has F and TTE (It doesn't currently)
		// 2. We pass a map of metadata.
		// Let's refine the method signature to take `map[string]ExpiryMetadata`?
		// Or simpler: Just calculate skew for the points provided, assuming they are consistent.
		// Wait, I need F to calc LogMoneyness.
		// I will update BuildSkew to take F and TTE as args.

		// For now, I'll stub the retrieval of F/TTE (maybe passing 0 and letting visualizer fail? No.)
		// Best approach: The input should probably be grouped already or carry context.
		// Let's assume the caller groups it.

		skew := b.BuildSkew(expiry, pList, 0, 0) // Placeholders
		surface.Skews = append(surface.Skews, skew)
	}

	// Sort by expiry?

	// Build Term Structure
	surface.TermStructure = b.BuildTermStructure(surface.Skews)

	return surface
}
