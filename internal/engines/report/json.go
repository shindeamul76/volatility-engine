package report

import (
	"encoding/json"
	"volatility-engine/internal/domain/risk"
)

type JSONRenderer struct{}

func (r *JSONRenderer) Render(report risk.StrategyRiskReport) ([]byte, error) {
	return json.MarshalIndent(report, "", "  ")
}
