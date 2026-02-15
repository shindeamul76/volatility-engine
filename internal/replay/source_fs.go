package replay

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"gopkg.in/yaml.v3"
)

type SnapshotSource interface {
	List() ([]SnapshotDescriptor, error)
	Load(desc SnapshotDescriptor) (SnapshotManifest, error)
}

type SourceFS struct {
	BaseDir      string
	ManifestName string // "manifest.yaml"
}

func NewSourceFS(baseDir string) *SourceFS {
	return &SourceFS{
		BaseDir:      baseDir,
		ManifestName: "manifest.yaml",
	}
}

func (s *SourceFS) List() ([]SnapshotDescriptor, error) {
	entries, err := os.ReadDir(s.BaseDir)
	if err != nil {
		return nil, err
	}

	var descs []SnapshotDescriptor
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		folder := filepath.Join(s.BaseDir, e.Name())
		manifest := filepath.Join(folder, s.ManifestName)
		if _, err := os.Stat(manifest); err != nil {
			continue
		}

		m, err := s.Load(SnapshotDescriptor{Folder: folder, Manifest: manifest})
		if err != nil {
			return nil, fmt.Errorf("manifest load failed for %s: %w", manifest, err)
		}

		descs = append(descs, SnapshotDescriptor{
			AsOf:     m.AsOf,
			Folder:   folder,
			Manifest: manifest,
		})
	}

	sort.Slice(descs, func(i, j int) bool {
		return descs[i].AsOf.Before(descs[j].AsOf)
	})
	return descs, nil
}

func (s *SourceFS) Load(desc SnapshotDescriptor) (SnapshotManifest, error) {
	raw, err := os.ReadFile(desc.Manifest)
	if err != nil {
		return SnapshotManifest{}, err
	}

	// decode into temp struct with strings if needed
	var tmp struct {
		AsOf         string  `yaml:"as_of"`
		Underlying   string  `yaml:"underlying"`
		Spot         float64 `yaml:"spot"`
		RiskFreeRate float64 `yaml:"risk_free_rate"`
		Files        []struct {
			Path   string `yaml:"path"`
			Expiry string `yaml:"expiry"`
		} `yaml:"files"`
	}

	if err := yaml.Unmarshal(raw, &tmp); err != nil {
		return SnapshotManifest{}, err
	}

	asOf, err := parseRFC3339(tmp.AsOf)
	if err != nil {
		return SnapshotManifest{}, fmt.Errorf("bad as_of: %w", err)
	}

	m := SnapshotManifest{
		AsOf:         asOf,
		Underlying:   tmp.Underlying,
		Spot:         tmp.Spot,
		RiskFreeRate: tmp.RiskFreeRate,
	}

	for _, f := range tmp.Files {
		exp, err := parseRFC3339(f.Expiry)
		if err != nil {
			return SnapshotManifest{}, fmt.Errorf("bad expiry %s: %w", f.Path, err)
		}
		m.Files = append(m.Files, struct {
			Path   string
			Expiry time.Time
		}{
			Path:   filepath.Join(desc.Folder, f.Path),
			Expiry: exp,
		})
	}

	return m, nil
}

func parseRFC3339(s string) (time.Time, error) {
	return time.Parse(time.RFC3339, s)
}
