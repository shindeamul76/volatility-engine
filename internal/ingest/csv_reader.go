package ingest

import (
	"encoding/csv"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// RawRow represents a generic row from the CSV.
type RawRow map[string]string

// SmartCSVReader handles reading CSVs with potential multi-row headers.
type SmartCSVReader struct{}

func NewSmartCSVReader() *SmartCSVReader {
	return &SmartCSVReader{}
}

// ReadFile reads a CSV file and returns a list of maps (headers -> value).
func (r *SmartCSVReader) ReadFile(path string) ([]RawRow, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	reader := csv.NewReader(f)
	// Allow variable number of fields if necessary, though strict is better if possible.
	reader.FieldsPerRecord = -1

	rows, err := reader.ReadAll()
	if err != nil {
		return nil, err
	}

	if len(rows) < 2 {
		return nil, fmt.Errorf("csv must have at least 2 header rows")
	}

	// Header processing logic similar to the Node.js script
	headers := parseHeaders(rows[0], rows[1])

	var records []RawRow
	// Data starts from index 2
	for i := 2; i < len(rows); i++ {
		row := rows[i]
		// Skip empty rows
		if len(row) == 0 {
			continue
		}

		record := make(RawRow)
		for j, val := range row {
			if j < len(headers) {
				record[headers[j]] = strings.TrimSpace(val)
			}
		}
		records = append(records, record)
	}

	return records, nil
}

// parseHeaders attempts to detect headers from the first two rows.
func parseHeaders(row0, row1 []string) []string {
	// 1. Find the "Strike" column index in row1 (the sub-headers)
	strikeIdx := -1
	for i, h := range row1 {
		cleanH := normalizeString(h)
		if strings.Contains(cleanH, "strike") {
			strikeIdx = i
			break
		}
	}

	// If no strike found in row1, fallback to row0 (simple headers)
	if strikeIdx == -1 {
		return deduplicateHeaders(row0)
	}

	// 2. Build headers based on CALLS (left of strike) and PUTS (right of strike)
	headers := make([]string, len(row1))
	for i, sub := range row1 {
		subClean := strings.TrimSpace(sub)

		if i == strikeIdx {
			headers[i] = "STRIKE"
			continue
		}

		side := "CALLS"
		if i > strikeIdx {
			side = "PUTS"
		}

		if subClean == "" {
			headers[i] = fmt.Sprintf("%s_COL_%d", side, i)
		} else {
			headers[i] = fmt.Sprintf("%s %s", side, subClean)
		}
	}

	return deduplicateHeaders(headers)
}

func normalizeString(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

func deduplicateHeaders(headers []string) []string {
	counts := make(map[string]int)
	result := make([]string, len(headers))
	for i, h := range headers {
		base := strings.TrimSpace(h)
		if base == "" {
			base = fmt.Sprintf("COL_%d", i)
		}
		counts[base]++
		if counts[base] > 1 {
			result[i] = fmt.Sprintf("%s_%d", base, counts[base])
		} else {
			result[i] = base
		}
	}
	return result
}

// Helper to parse floats safely
func ParseFloat(s string) float64 {
	clean := strings.ReplaceAll(s, ",", "")
	clean = strings.TrimSpace(clean)
	if clean == "-" || clean == "" {
		return 0
	}
	v, err := strconv.ParseFloat(clean, 64)
	if err != nil {
		return 0
	}
	return v
}

func ParseInt(s string) int64 {
	clean := strings.ReplaceAll(s, ",", "")
	clean = strings.TrimSpace(clean)
	if clean == "-" || clean == "" {
		return 0
	}
	// Handle float strings that might be ints appropriately if needed,
	// but usually ParseInt is strict. Using ParseFloat then cast for safety
	// against formats like "12,000.00"
	v, err := strconv.ParseFloat(clean, 64)
	if err != nil {
		return 0
	}
	return int64(v)
}
