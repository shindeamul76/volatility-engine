package main

import (
	"encoding/csv"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"volatility-engine/internal/config"

	"gopkg.in/yaml.v3"
)

type ManifestFile struct {
	Path   string `yaml:"path"`
	Expiry string `yaml:"expiry"`
}

type Manifest struct {
	AsOf         string         `yaml:"as_of"`
	Underlying   string         `yaml:"underlying"`
	Spot         float64        `yaml:"spot"`
	RiskFreeRate float64        `yaml:"risk_free_rate"`
	Files        []ManifestFile `yaml:"files"`
}

func main() {
	// 1. Load Config to get RiskFreeRate
	cfg, err := config.Load("config.yaml")
	if err != nil {
		log.Fatalf("Failed to load config.yaml: %v", err)
	}
	r := cfg.Pricing.RiskFreeRate
	log.Printf("Using RiskFreeRate from config: %.2f%%", r*100)

	// 2. Load Spot Prices from History
	spotMap, err := loadSpotHistory("data/history/NIFTY_closes.csv")
	if err != nil {
		log.Fatalf("Failed to load spot history: %v", err)
	}
	log.Printf("Loaded %d spot prices", len(spotMap))

	baseDir := "testdata/snapshots"
	if len(os.Args) > 1 {
		baseDir = os.Args[1]
	}

	entries, err := os.ReadDir(baseDir)
	if err != nil {
		log.Fatalf("Failed to read dir %s: %v", baseDir, err)
	}

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}

		folderPath := filepath.Join(baseDir, e.Name())
		fmt.Printf("Processing %s...\n", folderPath)

		// 1. Parse Date from folder name (DD-MM-YYYY)
		dateParts := strings.Split(e.Name(), "-")
		if len(dateParts) != 3 {
			log.Printf("Skipping %s: invalid date format", e.Name())
			continue
		}

		// Parse into time.Time (default UTC 00:00)
		parsedAsOf, err := time.Parse("02-01-2006", e.Name())
		if err != nil {
			log.Printf("Skipping %s: %v", e.Name(), err)
			continue
		}

		// Convert to IST 15:30:00
		fixedZone := time.FixedZone("IST", 5*3600+1800) // +05:30
		asOf := time.Date(parsedAsOf.Year(), parsedAsOf.Month(), parsedAsOf.Day(), 15, 30, 0, 0, fixedZone)

		// 2. Find CSVs
		files, err := os.ReadDir(folderPath)
		if err != nil {
			log.Printf("Failed to read folder %s: %v", folderPath, err)
			continue
		}

		var manFiles []ManifestFile
		for _, f := range files {
			if strings.HasSuffix(f.Name(), ".csv") {
				expiry := parseExpiryFromFilename(f.Name())
				if !expiry.IsZero() {
					manFiles = append(manFiles, ManifestFile{
						Path:   f.Name(),
						Expiry: expiry.Format(time.RFC3339),
					})
				} else {
					log.Printf("Warning: Could not extract expiry from %s", f.Name())
				}
			}
		}

		if len(manFiles) == 0 {
			log.Printf("No valid CSVs found in %s", folderPath)
			continue
		}

		// 3. Lookup Spot Price
		dateKey := asOf.Format("2006-01-02")
		spot, ok := spotMap[dateKey]
		if !ok {
			log.Printf("Warning: No spot price found for %s, defaulting to config spot %.2f", dateKey, cfg.Market.Spot)
			spot = cfg.Market.Spot
		} else {
			// Validate approximate match (sanity check)
			if spot < 10000 || spot > 40000 {
				log.Printf("Warning: Spot price %.2f for %s seems suspicious", spot, dateKey)
			}
		}

		// 4. Write Manifest
		m := Manifest{
			AsOf:         asOf.Format(time.RFC3339),
			Underlying:   cfg.Market.Underlying,
			Spot:         spot,
			RiskFreeRate: r,
			Files:        manFiles,
		}

		data, err := yaml.Marshal(m)
		if err != nil {
			log.Printf("Failed to marshal manifest: %v", err)
			continue
		}

		manifestPath := filepath.Join(folderPath, "manifest.yaml")
		if err := os.WriteFile(manifestPath, data, 0666); err != nil {
			log.Printf("Failed to write %s: %v", manifestPath, err)
		} else {
			fmt.Printf("Generated %s (Spot: %.2f, R: %.2f)\n", manifestPath, spot, r)
		}
	}
}

func loadSpotHistory(path string) (map[string]float64, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	r := csv.NewReader(f)
	records, err := r.ReadAll()
	if err != nil {
		return nil, err
	}

	out := make(map[string]float64)
	for i, row := range records {
		if i == 0 {
			continue // Header
		}
		if len(row) < 2 {
			continue
		}
		dateStr := row[0]
		price, err := strconv.ParseFloat(row[1], 64)
		if err == nil {
			out[dateStr] = price
		}
	}
	return out, nil
}

func parseExpiryFromFilename(fname string) time.Time {
	// Remove extension and prefix
	clean := strings.TrimSuffix(fname, ".csv")
	clean = strings.TrimPrefix(clean, "option-chain-ED-NIFTY-")

	parts := strings.Split(clean, "-")

	var parsed time.Time
	var err error

	if len(parts) >= 3 {
		candidate := strings.Join(parts[len(parts)-3:], "-")
		parsed, err = time.Parse("02-Jan-2006", candidate)
		if err != nil {
			candidate = strings.Join(parts[:3], "-")
			parsed, err = time.Parse("02-Jan-2006", candidate)
		}
	}

	if !parsed.IsZero() && err == nil {
		fixedZone := time.FixedZone("IST", 5*3600+1800) // +05:30
		return time.Date(parsed.Year(), parsed.Month(), parsed.Day(), 15, 30, 0, 0, fixedZone)
	}

	return time.Time{}
}
