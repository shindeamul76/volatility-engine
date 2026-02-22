package ingest_test

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"volatility-engine/internal/ingest"
)

func TestIngestSnapshot(t *testing.T) {
	svc := ingest.NewIngestService()

	// Use the sample file we just created
	path := "../../testdata/snapshots/sample_nifty.csv"
	expiry := time.Date(2026, 2, 5, 15, 30, 0, 0, time.UTC)
	asOf := time.Date(2026, 1, 28, 15, 30, 0, 0, time.UTC)

	snap, err := svc.IngestSnapshot(path, "NIFTY", 20000.0, expiry, asOf, "s3://test-bucket/nifty_sample.csv")
	if err != nil {
		t.Fatalf("IngestSnapshot failed: %v", err)
	}

	fmt.Printf("Snapshot ID: %s\n", snap.ID)
	bytes, _ := json.MarshalIndent(snap, "", "  ")
	fmt.Printf("JSON Dump:\n%s\n", string(bytes))

	fmt.Printf("Total Quotes: %d\n", len(snap.Quotes))
	fmt.Printf("Tradable: %d, Rejected: %d\n", snap.QualitySummary.TradableQuotes, snap.QualitySummary.RejectedQuotes)

	if len(snap.Quotes) == 0 {
		t.Fatal("Expected quotes, got 0")
	}

	// Verify specific quote (20000 CE)
	var found bool
	for _, q := range snap.Quotes {
		if q.Contract.Strike == 20000 && q.Contract.Type == "CALL" {
			found = true
			if q.Market.OpenInterest != 350000 {
				t.Errorf("Expected OI 350,000, got %d", q.Market.OpenInterest)
			}
			if q.Market.Last != 122.0 {
				t.Errorf("Expected Last 122.0, got %f", q.Market.Last)
			}
			// In our logic, since bid/ask are missing, mid = last = 122.0
			if q.Market.Mid != 122.0 {
				t.Errorf("Expected Mid 122.0, got %f", q.Market.Mid)
			}
		}
	}
	if !found {
		t.Error("Did not find 20000 CALL")
	}
}
