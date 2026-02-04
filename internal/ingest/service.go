package ingest

import (
	"fmt"
	"strings"
	"time"

	"volatility-engine/internal/domain/market"
)

// IngestService handles loading and normalizing snapshots.
type IngestService struct {
	Reader *SmartCSVReader
	Config ValidationConfig
}

func NewIngestService() *IngestService {
	return &IngestService{
		Reader: NewSmartCSVReader(),
		Config: DefaultValidationConfig(),
	}
}

// IngestSnapshot reads a file and returns a canonical Snapshot.
func (s *IngestService) IngestSnapshot(filePath string, underlying string, spot float64, expiry time.Time, s3Key string) (*market.Snapshot, error) {
	rawRows, err := s.Reader.ReadFile(filePath)
	// println("RawRows-->", rawRows)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}

	timestamp := time.Now()
	id := fmt.Sprintf("snap_%s_%s_%s", timestamp.Format("20060102"), timestamp.Format("150405"), strings.ToLower(underlying))

	snapshot := &market.Snapshot{
		ID:   id,
		AsOf: timestamp, // Ideally parsing this from filename or metadata
		Underlying: market.Underlying{
			Symbol: underlying,
			Spot:   spot,
		},
		Expiries: []market.ExpiryMetadata{
			{
				Expiry:       expiry,
				DaysToExpiry: int(expiry.Sub(timestamp).Hours() / 24), // Approx
				// TODO: Precise trading calendar logic
			},
		},
		Quotes:         []market.Quote{},
		QualitySummary: market.QualitySummary{RejectionReasons: make(map[string]int)},
		RawDump: market.RawDumpMetadata{
			Stored: true,
			S3Key:  s3Key,
		},
	}

	for _, row := range rawRows {
		// Identify columns (simple mapping based on string matching)
		// This creates pairs of calls and puts for the same strike

		strikeVal := ParseFloat(getValue(row, "strike", "strikeprice"))

		if strikeVal == 0 {
			continue
		}

		// Process CALL
		callQuote := s.buildQuote(row, strikeVal, market.Call, underlying, expiry, spot)
		snapshot.Quotes = append(snapshot.Quotes, callQuote)
		updateQualitySummary(&snapshot.QualitySummary, callQuote)

		// Process PUT
		putQuote := s.buildQuote(row, strikeVal, market.Put, underlying, expiry, spot)
		snapshot.Quotes = append(snapshot.Quotes, putQuote)
		updateQualitySummary(&snapshot.QualitySummary, putQuote)
	}

	snapshot.QualitySummary.TotalQuotes = len(snapshot.Quotes)

	return snapshot, nil
}

func (s *IngestService) buildQuote(row RawRow, strike float64, optType market.OptionType, underlying string, expiry time.Time, spot float64) market.Quote {
	prefix := "CALLS"
	if optType == market.Put {
		prefix = "PUTS"
	}

	// Helper to find key with prefix
	find := func(keywords ...string) string {
		// First pass: look for exact matches or matches without "qty"/ "chng" if looking for price
		for k, v := range row {
			kLower := strings.ToLower(k)
			// Check if it belongs to our side (CALLS/PUTS)
			if !strings.Contains(strings.ToUpper(k), prefix) {
				continue
			}

			for _, kw := range keywords {
				if strings.Contains(kLower, kw) {
					// Logic to prefer "bid" over "bid qty"
					if (kw == "bid" || kw == "ask") && strings.Contains(kLower, "qty") {
						continue
					}
					// Logic to prefer "oi" over "chng in oi"
					if kw == "oi" && strings.Contains(kLower, "chng") {
						continue
					}

					return v
				}
			}
		}
		return ""
	}

	// Try to find specific columns.
	// This matching logic mimics the Node.js script's regex approach roughly.

	// OI
	oiStr := find("oi", "open interest", "openinterest")
	// If strict "Call OI" / "Put OI" didn't match, try generic matching if headers were simple

	oi := ParseInt(oiStr)

	// LTP
	ltp := ParseFloat(find("ltp", "last price", "close"))

	volume := ParseInt(find("volume"))

	// Bid/Ask (Often missing in simple dumps, might default to LTP or infer)
	// For this prototype, if Bid/Ask are missing, we might mock them or look for them.
	// The node script uses LTP mostly. Let's look for "Bid" and "Ask".
	bid := ParseFloat(find("bid"))
	ask := ParseFloat(find("ask"))

	// --- IMPROVEMENT #3: Missing Market Handling ---
	markSource := "MID"
	usedLTPFallback := false

	// Check if bid/ask are valid
	bidValid := bid > 0
	askValid := ask > 0

	if !bidValid || !askValid {
		// Missing or invalid bid/ask - use LTP fallback
		if ltp > 0 {
			bid = ltp
			ask = ltp
			markSource = "LTP_FALLBACK"
			usedLTPFallback = true
		}
	}

	mid := (bid + ask) / 2
	if bid == 0 && ask == 0 {
		mid = ltp
	}

	spread := ask - bid
	spreadPct := 0.0
	if mid > 0 {
		spreadPct = spread / mid
	}

	q := market.Quote{
		Contract: market.Contract{
			Symbol: underlying,
			Expiry: expiry,
			Strike: strike,
			Type:   optType,
		},
		Market: market.MarketData{
			Bid:          bid,
			Ask:          ask,
			Mid:          mid,
			Last:         ltp,
			OpenInterest: oi,
			Volume:       volume,
		},
		Derived: market.DerivedMetrics{
			Spread:     spread,
			SpreadPct:  spreadPct,
			MarkSource: markSource,
		},
		Quality: market.QualityFlags{
			IsTradable: true,
			Flags:      []string{},
		},
	}

	// --- REFINED VALIDATION LOGIC ---
	cfg := s.Config

	// 1. Bid/Ask Sanity
	if q.Market.Bid <= 0 || q.Market.Ask <= 0 {
		q.Quality.Flags = append(q.Quality.Flags, "BAD_PRICE")
	}
	if q.Market.Ask < q.Market.Bid {
		q.Quality.Flags = append(q.Quality.Flags, "CROSSED_MARKET")
	}
	if q.Market.Mid <= 0 {
		q.Quality.Flags = append(q.Quality.Flags, "BAD_MID_PRICE")
	}

	// 2. Spread Logic (Dynamic with BOTH percentage AND absolute checks)
	isLowPremium := q.Market.Mid < cfg.LowPremiumThreshold

	// Determine thresholds based on premium level
	maxSpreadPct := cfg.SpreadPctNormal
	maxSpreadAbs := cfg.SpreadAbsNormal
	if isLowPremium {
		maxSpreadPct = cfg.SpreadPctLowPremium
		maxSpreadAbs = cfg.SpreadAbsLowPremium
		q.Quality.Flags = append(q.Quality.Flags, "LOW_PREMIUM") // Info flag
	}

	// Check percentage spread
	if q.Derived.SpreadPct > maxSpreadPct {
		q.Quality.Flags = append(q.Quality.Flags, "WIDE_SPREAD")
	}

	// --- IMPROVEMENT #1: Absolute Spread Check ---
	if q.Derived.Spread > maxSpreadAbs {
		q.Quality.Flags = append(q.Quality.Flags, "WIDE_SPREAD_ABS")
	}

	// 3. Liquidity Logic (Multi-factor, config-driven)
	if q.Market.OpenInterest < cfg.OI_Strategy_Min && q.Market.Volume < cfg.Vol_Strategy_Min {
		q.Quality.Flags = append(q.Quality.Flags, "ILLIQUID_STRATEGY")
	}
	if q.Market.OpenInterest < cfg.OI_Severe_Min && q.Market.Volume < cfg.Vol_Severe_Min {
		q.Quality.Flags = append(q.Quality.Flags, "ILLIQUID")
	}

	// If using LTP fallback, mark it
	if usedLTPFallback {
		q.Quality.Flags = append(q.Quality.Flags, "LTP_FALLBACK")
	}

	// 4. Deterministic Usability
	hasFlag := func(f string) bool {
		for _, v := range q.Quality.Flags {
			if v == f {
				return true
			}
		}
		return false
	}

	// Analytics: Lenient (Just need valid price structure)
	q.Quality.IsUsableForAnalytics = !hasFlag("BAD_PRICE") && !hasFlag("CROSSED_MARKET") && !hasFlag("BAD_MID_PRICE")

	// IV Surface: Valid mid from bid/ask, decent percentage spread, not severely illiquid
	// Relaxed for far expiries: Ignore absolute spread check for IV solving
	q.Quality.IsUsableForIV = q.Quality.IsUsableForAnalytics &&
		!hasFlag("WIDE_SPREAD") &&
		!hasFlag("ILLIQUID") &&
		!hasFlag("LTP_FALLBACK")

	// Strategy: All IV requirements + not ILLIQUID_STRATEGY
	// --- IMPROVEMENT #2: Remove LOW_PREMIUM gate (role-specific in selector) ---
	q.Quality.IsUsableForStrategy = q.Quality.IsUsableForIV && !hasFlag("ILLIQUID_STRATEGY")

	// Backwards compatibility for IsTradable (Strict)
	q.Quality.IsTradable = q.Quality.IsUsableForStrategy

	return q
}

// Helpers

func getValue(row RawRow, keys ...string) string {
	for k, v := range row {
		kLower := strings.ToLower(k)
		for _, key := range keys {
			if strings.Contains(kLower, key) {
				return v
			}
		}
	}
	return ""
}

func updateQualitySummary(summary *market.QualitySummary, q market.Quote) {
	if q.Quality.IsTradable {
		summary.TradableQuotes++
	} else {
		summary.RejectedQuotes++
		for _, flag := range q.Quality.Flags {
			summary.RejectionReasons[flag]++
		}
	}
}
