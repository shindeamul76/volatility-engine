package market

import (
	"time"
)

// TimeToExpiryYears calculates TTE under ACT/365 convention.
// Returns 0 if already expired.
func TimeToExpiryYears(asOf, expiry time.Time) float64 {
	diff := expiry.Sub(asOf)
	if diff <= 0 {
		return 0
	}
	years := diff.Hours() / 24.0 / DaysPerYear
	if years < MinTTEYears {
		return 0 // Snap to 0 for numerical stability
	}
	return years
}

// AnnualizeTheta converts Annual Theta to Daily Theta (ACT/365).
func AnnualizeTheta(thetaAnnual float64) float64 {
	return thetaAnnual / DaysPerYear
}

// YearsToTime adds years to a time (approximate).
func YearsToTime(t time.Time, years float64) time.Time {
	// This is approximate logic used for output/display, ensuring consistency.
	// Exact calendar logic is complex, but for TTE inversion:
	duration := time.Duration(years * DaysPerYear * 24 * float64(time.Hour))
	return t.Add(duration)
}
