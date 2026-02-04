package regime

import (
	"encoding/csv"
	"fmt"
	"os"
	"sort"
	"strconv"
	"time"
)

// IVHistoryProvider retrieves historical IV30 data.
type IVHistoryProvider interface {
	// GetIV30History returns the last `lookback` IV30 values strictly before or on `asOf` date.
	GetIV30History(symbol string, asOf time.Time, lookback int) ([]float64, error)
}

// PriceHistoryProvider retrieves historical price data (closes).
type PriceHistoryProvider interface {
	// GetDailyCloses returns the last `lookbackDays` close prices ending on or before `asOf`.
	GetDailyCloses(symbol string, asOf time.Time, lookbackDays int) ([]float64, error)
}

// CSVIVHistoryProvider implements IVHistoryProvider using a CSV file.
// CSV Format: date,iv30,confidence,method
type CSVIVHistoryProvider struct {
	BaseDir string // e.g., "data/history"
}

func (p *CSVIVHistoryProvider) GetIV30History(symbol string, asOf time.Time, lookback int) ([]float64, error) {
	filename := fmt.Sprintf("%s/%s_iv30.csv", p.BaseDir, symbol)
	file, err := os.Open(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to open history file %s: %w", filename, err)
	}
	defer file.Close()

	reader := csv.NewReader(file)
	records, err := reader.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("failed to read CSV %s: %w", filename, err)
	}

	// Format: date, iv30, ...
	// Example: 2026-01-01, 0.1523, ...
	// We assume the CSV is sorted by date ascending, but let's parse and filter.

	type entry struct {
		date time.Time
		val  float64
	}

	var history []entry
	// Skip header if present. Simple heuristic: if first col parse fails as date
	startIdx := 0
	if len(records) > 0 {
		_, err := time.Parse("2006-01-02", records[0][0])
		if err != nil {
			startIdx = 1
		}
	}

	for i := startIdx; i < len(records); i++ {
		row := records[i]
		if len(row) < 2 {
			continue
		}
		// Try YYYY-MM-DD
		d, err := time.Parse("2006-01-02", row[0])
		if err != nil {
			continue // skip bad rows
		}

		if d.After(asOf) {
			continue // future data relative to asOf
		}

		v, err := strconv.ParseFloat(row[1], 64)
		if err != nil {
			continue
		}

		history = append(history, entry{date: d, val: v})
	}

	// Sort by date just in case
	sort.Slice(history, func(i, j int) bool {
		return history[i].date.Before(history[j].date)
	})

	// Take last N
	if len(history) > lookback {
		history = history[len(history)-lookback:]
	}

	result := make([]float64, len(history))
	for i, h := range history {
		result[i] = h.val
	}

	return result, nil
}

// RecordIV30 appends or updates the IV30 entry for the given date.
func (p *CSVIVHistoryProvider) RecordIV30(symbol string, date time.Time, iv float64, conf float64, method string) error {
	filename := fmt.Sprintf("%s/%s_iv30.csv", p.BaseDir, symbol)

	// We need to read all, update/append, and write back.
	// This approach is inefficient for large files but fine for daily prototypes.
	// Or we can just append if strict daily usage is guaranteed, but idempotency (update for same day) is safer.

	// 1. Read existing
	var rows [][]string
	file, err := os.Open(filename)
	if err == nil {
		reader := csv.NewReader(file)
		rows, _ = reader.ReadAll()
		file.Close()
	}

	header := []string{"date", "iv30", "confidence", "method"}
	var newRows [][]string

	// Preserve header if exists, or create one
	if len(rows) > 0 {
		// Check if first row looks like header
		if rows[0][0] == "date" {
			newRows = append(newRows, rows[0])
			rows = rows[1:] // process data rows
		} else {
			// No header found, maybe add one? Assuming existing file has one if strictly managed.
			// If file was empty, we add header.
		}
	} else {
		newRows = append(newRows, header)
	}

	// Prepare new record
	dateStr := date.Format("2006-01-02")
	ivStr := fmt.Sprintf("%.4f", iv)
	confStr := fmt.Sprintf("%.2f", conf)

	updated := false
	for _, row := range rows {
		if len(row) > 0 && row[0] == dateStr {
			// Update existing row for today
			newRows = append(newRows, []string{dateStr, ivStr, confStr, method})
			updated = true
		} else {
			newRows = append(newRows, row)
		}
	}

	if !updated {
		newRows = append(newRows, []string{dateStr, ivStr, confStr, method})
	}

	// Sort data rows by date is good practice but not strictly required for append-only usage.
	// But let's keep it sorted to be clean.
	// Skipping sort for now to keep it simple, assuming mostly chronological appends.

	// 2. Write back
	outFile, err := os.Create(filename) // Truncates
	if err != nil {
		return fmt.Errorf("failed to open history file for writing %s: %w", filename, err)
	}
	defer outFile.Close()

	writer := csv.NewWriter(outFile)
	defer writer.Flush()

	return writer.WriteAll(newRows)
}

// CSVPriceHistoryProvider implements PriceHistoryProvider using a CSV file.
// CSV Format: date,close,...
type CSVPriceHistoryProvider struct {
	BaseDir string
}

func (p *CSVPriceHistoryProvider) GetDailyCloses(symbol string, asOf time.Time, lookbackDays int) ([]float64, error) {
	filename := fmt.Sprintf("%s/%s_closes.csv", p.BaseDir, symbol)
	file, err := os.Open(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to open price file %s: %w", filename, err)
	}
	defer file.Close()

	reader := csv.NewReader(file)
	records, err := reader.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("failed to read CSV %s: %w", filename, err)
	}

	type entry struct {
		date time.Time
		val  float64
	}

	var history []entry
	startIdx := 0
	if len(records) > 0 {
		_, err := time.Parse("2006-01-02", records[0][0])
		if err != nil {
			startIdx = 1
		}
	}

	for i := startIdx; i < len(records); i++ {
		row := records[i]
		if len(row) < 2 {
			continue
		}
		d, err := time.Parse("2006-01-02", row[0])
		if err != nil {
			continue
		}

		if d.After(asOf) {
			continue
		}

		v, err := strconv.ParseFloat(row[1], 64)
		if err != nil {
			continue
		}

		history = append(history, entry{date: d, val: v})
	}

	sort.Slice(history, func(i, j int) bool {
		return history[i].date.Before(history[j].date)
	})

	if len(history) > lookbackDays {
		history = history[len(history)-lookbackDays:]
	}

	result := make([]float64, len(history))
	for i, h := range history {
		result[i] = h.val
	}

	return result, nil
}
