package replay

import (
	"log"
	"time"
)

// AlertLevel represents the severity of the alert
type AlertLevel string

const (
	AlertLevelInfo     AlertLevel = "INFO"
	AlertLevelWarning  AlertLevel = "WARNING"
	AlertLevelCritical AlertLevel = "CRITICAL"
)

// Alert records a significant event during the replay
type Alert struct {
	Time    time.Time
	Level   AlertLevel
	Type    string // e.g., "DRAWDOWN_BREACH", "STOP_LOSS", "TAKE_PROFIT", "TOP_CANDIDATE"
	Message string
	Context map[string]any
}

// AlertService manages the generation and storage of alerts
type AlertService struct {
	Alerts  []Alert
	Enabled bool
}

// NewAlertService creates a new AlertService
func NewAlertService(enabled bool) *AlertService {
	return &AlertService{
		Alerts:  make([]Alert, 0),
		Enabled: enabled,
	}
}

// Dispatch creates an alert and logs it
func (a *AlertService) Dispatch(t time.Time, level AlertLevel, alertType, message string, ctx map[string]any) {
	if !a.Enabled {
		return
	}

	alert := Alert{
		Time:    t.UTC(),
		Level:   level,
		Type:    alertType,
		Message: message,
		Context: ctx,
	}

	a.Alerts = append(a.Alerts, alert)

	// Informational console logging for significant actions
	log.Printf("[ALERT] [%s] %s: %s", level, alertType, message)
}
