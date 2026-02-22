package nsefetch

import (
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
)

// ConvertToCSV writes NSE JSON data as a 2-row-header CSV file
// compatible with SmartCSVReader. Returns the path to the CSV file
// and the spot price extracted from the response.
func ConvertToCSV(resp *NSEOptionChainResponse, outDir, symbol, expiry string) (string, float64, error) {
	spot := resp.Records.UnderlyingValue

	// Create output directory if needed
	if err := os.MkdirAll(outDir, 0755); err != nil {
		return "", 0, fmt.Errorf("failed to create output dir: %w", err)
	}

	filename := fmt.Sprintf("nse-live-%s-%s.csv", symbol, expiry)
	filePath := filepath.Join(outDir, filename)

	f, err := os.Create(filePath)
	if err != nil {
		return "", 0, fmt.Errorf("failed to create CSV: %w", err)
	}
	defer f.Close()

	w := csv.NewWriter(f)
	defer w.Flush()

	// Row 0: group headers (CALLS side / PUTS side)
	row0 := []string{
		"CALLS", "", "", "", "", "", "", "", "",
		"",
		"PUTS", "", "", "", "", "", "", "", "",
	}

	// Row 1: sub-headers matching SmartCSVReader expectations
	row1 := []string{
		"OI", "Chng in OI", "Volume", "IV", "LTP", "Bid Qty", "Bid", "Ask", "Ask Qty",
		"Strike",
		"OI", "Chng in OI", "Volume", "IV", "LTP", "Bid Qty", "Bid", "Ask", "Ask Qty",
	}

	_ = w.Write(row0)
	_ = w.Write(row1)

	for _, rec := range resp.Records.Data {
		row := make([]string, 19)

		// CALLS side (columns 0-8)
		if rec.CE != nil {
			row[0] = fmt.Sprintf("%.0f", rec.CE.OpenInterest)
			row[1] = "0" // change in OI (not critical for pipeline)
			row[2] = fmt.Sprintf("%.0f", rec.CE.TotalTradedVol)
			row[3] = fmt.Sprintf("%.2f", rec.CE.ImpliedVol)
			row[4] = fmt.Sprintf("%.2f", rec.CE.LastPrice)
			row[5] = fmt.Sprintf("%.0f", rec.CE.BidQty)
			row[6] = fmt.Sprintf("%.2f", rec.CE.BidPrice)
			row[7] = fmt.Sprintf("%.2f", rec.CE.AskPrice)
			row[8] = fmt.Sprintf("%.0f", rec.CE.AskQty)
		} else {
			for i := 0; i < 9; i++ {
				row[i] = "-"
			}
		}

		// Strike (column 9)
		row[9] = fmt.Sprintf("%.0f", rec.StrikePrice)

		// PUTS side (columns 10-18)
		if rec.PE != nil {
			row[10] = fmt.Sprintf("%.0f", rec.PE.OpenInterest)
			row[11] = "0"
			row[12] = fmt.Sprintf("%.0f", rec.PE.TotalTradedVol)
			row[13] = fmt.Sprintf("%.2f", rec.PE.ImpliedVol)
			row[14] = fmt.Sprintf("%.2f", rec.PE.LastPrice)
			row[15] = fmt.Sprintf("%.0f", rec.PE.BidQty)
			row[16] = fmt.Sprintf("%.2f", rec.PE.BidPrice)
			row[17] = fmt.Sprintf("%.2f", rec.PE.AskPrice)
			row[18] = fmt.Sprintf("%.0f", rec.PE.AskQty)
		} else {
			for i := 10; i < 19; i++ {
				row[i] = "-"
			}
		}

		_ = w.Write(row)
	}

	w.Flush()
	if err := w.Error(); err != nil {
		return "", 0, fmt.Errorf("CSV write error: %w", err)
	}

	return filePath, spot, nil
}
