package report

import (
	"volatility-engine/internal/domain/risk"
)

// ReportBundle holds a collection of risk reports, e.g. "Top 3 Candidates".
type ReportBundle struct {
	ID          string                    `json:"bundle_id"`
	Reports     []risk.StrategyRiskReport `json:"reports"`
	GeneratedAt string                    `json:"generated_at"`
}

// BundleBuilder helps construct the bundle.
type BundleBuilder struct {
	Reports []risk.StrategyRiskReport
}

func NewBundleBuilder() *BundleBuilder {
	return &BundleBuilder{
		Reports: make([]risk.StrategyRiskReport, 0),
	}
}

func (b *BundleBuilder) Add(report risk.StrategyRiskReport) {
	b.Reports = append(b.Reports, report)
}

func (b *BundleBuilder) Build(id string) ReportBundle {
	return ReportBundle{
		ID:      id,
		Reports: b.Reports,
		// GeneratedAt: time.Now().Format...
	}
}
