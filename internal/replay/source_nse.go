package replay

import (
	"fmt"
	"log"
	"time"

	"volatility-engine/internal/nsefetch"
)

// ExpirySpec holds one expiry to fetch from the NSE API.
type ExpirySpec struct {
	APIExpiry string    // "24-Feb-2026" (format NSE expects)
	ExpiryAt  time.Time // parsed expiry time with 15:30 IST
}

// SourceNSE implements SnapshotSource by fetching live data from the NSE API.
type SourceNSE struct {
	Client       *nsefetch.Client
	Symbol       string       // "NIFTY"
	Expiries     []ExpirySpec // expiries to fetch
	RiskFreeRate float64
	TempDir      string // where CSV files are written
}

// NewSourceNSE creates a new NSE API-based snapshot source.
func NewSourceNSE(symbol string, expiries []ExpirySpec, riskFreeRate float64, tempDir string) *SourceNSE {
	return &SourceNSE{
		Client:       nsefetch.NewClient(),
		Symbol:       symbol,
		Expiries:     expiries,
		RiskFreeRate: riskFreeRate,
		TempDir:      tempDir,
	}
}

// List returns a single descriptor for "now".
// In live mode, we process one snapshot at a time driven by the ticker.
func (s *SourceNSE) List() ([]SnapshotDescriptor, error) {
	now := time.Now()

	// Load IST timezone
	ist, err := time.LoadLocation("Asia/Kolkata")
	if err != nil {
		return nil, fmt.Errorf("failed to load IST timezone: %w", err)
	}
	nowIST := now.In(ist)

	return []SnapshotDescriptor{
		{
			AsOf:   nowIST,
			Folder: s.TempDir,
		},
	}, nil
}

// Load fetches option chain data from the NSE API for all configured expiries,
// writes CSV files, and returns a SnapshotManifest compatible with the runner.
func (s *SourceNSE) Load(desc SnapshotDescriptor) (SnapshotManifest, error) {
	log.Printf("[SourceNSE] Fetching live data for %s (%d expiries)...", s.Symbol, len(s.Expiries))

	var spot float64
	var files []struct {
		Path   string
		Expiry time.Time
	}

	for _, exp := range s.Expiries {
		resp, err := s.Client.FetchOptionChain(s.Symbol, exp.APIExpiry)
		if err != nil {
			return SnapshotManifest{}, fmt.Errorf("fetch failed for expiry %s: %w", exp.APIExpiry, err)
		}

		// Convert JSON → CSV
		csvPath, fetchedSpot, err := nsefetch.ConvertToCSV(resp, s.TempDir, s.Symbol, exp.APIExpiry)
		if err != nil {
			return SnapshotManifest{}, fmt.Errorf("CSV conversion failed for expiry %s: %w", exp.APIExpiry, err)
		}

		// Use the spot from the first successful fetch
		if spot == 0 && fetchedSpot > 0 {
			spot = fetchedSpot
		}

		files = append(files, struct {
			Path   string
			Expiry time.Time
		}{
			Path:   csvPath,
			Expiry: exp.ExpiryAt,
		})

		log.Printf("[SourceNSE] Saved CSV: %s (spot=%.2f, records=%d)",
			csvPath, fetchedSpot, len(resp.Records.Data))
	}

	if spot == 0 {
		return SnapshotManifest{}, fmt.Errorf("no valid spot price obtained from NSE API")
	}

	manifest := SnapshotManifest{
		AsOf:         desc.AsOf,
		Underlying:   s.Symbol,
		Spot:         spot,
		RiskFreeRate: s.RiskFreeRate,
		Files:        files,
	}

	log.Printf("[SourceNSE] Manifest ready: spot=%.2f, files=%d, asOf=%s",
		spot, len(files), desc.AsOf.Format(time.RFC3339))

	return manifest, nil
}

// ParseExpirySpecs parses API expiry strings into ExpirySpec with proper time.
// apiExpiry format: "24-Feb-2026"
// Sets expiry time to 15:30 IST on that date.
func ParseExpirySpecs(apiExpiries []string) ([]ExpirySpec, error) {
	ist, err := time.LoadLocation("Asia/Kolkata")
	if err != nil {
		return nil, fmt.Errorf("failed to load IST timezone: %w", err)
	}

	var specs []ExpirySpec
	for _, apiExp := range apiExpiries {
		// Parse "24-Feb-2026" format
		t, err := time.Parse("02-Jan-2006", apiExp)
		if err != nil {
			return nil, fmt.Errorf("invalid expiry format %q (expected DD-Mon-YYYY): %w", apiExp, err)
		}

		// Set to 15:30 IST
		expiryAt := time.Date(t.Year(), t.Month(), t.Day(), 15, 30, 0, 0, ist)

		specs = append(specs, ExpirySpec{
			APIExpiry: apiExp,
			ExpiryAt:  expiryAt,
		})
	}

	return specs, nil
}
