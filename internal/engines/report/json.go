package report

import (
	"encoding/json"
)

type JSONRenderer struct{}

func (r *JSONRenderer) Render(report interface{}) ([]byte, error) {
	return json.MarshalIndent(report, "", "  ")
}
