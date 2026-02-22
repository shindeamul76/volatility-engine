package calendar

import (
	"testing"
	"time"
)

func TestIsNSETradingDay(t *testing.T) {
	ist := IndiaLocation

	tests := []struct {
		date    string
		trading bool
		reason  string
	}{
		{"2026-02-20", true, "normal Friday"},
		{"2026-02-21", false, "Saturday"},
		{"2026-02-22", false, "Sunday"},
		{"2026-01-15", false, "Municipal Corporation Election"},
		{"2026-01-26", false, "Republic Day"},
		{"2026-03-03", false, "Holi"},
		{"2026-10-02", false, "Gandhi Jayanti"},
		{"2026-12-25", false, "Christmas"},
		{"2026-02-19", false, "Chhatrapati Shivaji Maharaj Jayanti"},
		{"2026-09-14", false, "Ganesh Chaturthi"},
		{"2026-02-23", true, "normal Monday"},
	}

	for _, tc := range tests {
		dt, _ := time.ParseInLocation("2006-01-02", tc.date, ist)
		got := IsNSETradingDay(dt)
		if got != tc.trading {
			t.Errorf("%s (%s): expected trading=%v, got %v", tc.date, tc.reason, tc.trading, got)
		}
	}
}

func TestTradingDaysBetween(t *testing.T) {
	ist := IndiaLocation

	// Feb 20 (Fri) → Feb 24 (Tue): Mon 23 + Tue 24 = 2 trading days
	from := time.Date(2026, 2, 20, 15, 30, 0, 0, ist)
	to := time.Date(2026, 2, 24, 15, 30, 0, 0, ist)

	got := TradingDaysBetween(from, to)
	if got != 2 {
		t.Errorf("Feb 20→24: expected 2 trading days, got %d", got)
	}

	// Same day → 0
	got = TradingDaysBetween(from, from)
	if got != 0 {
		t.Errorf("same day: expected 0, got %d", got)
	}

	// Full week Mon-Fri (Feb 23 → Feb 27) = 4 trading days (24,25,26,27)
	from2 := time.Date(2026, 2, 23, 9, 0, 0, 0, ist)
	to2 := time.Date(2026, 2, 27, 15, 30, 0, 0, ist)
	got = TradingDaysBetween(from2, to2)
	if got != 4 {
		t.Errorf("Feb 23→27: expected 4 trading days, got %d", got)
	}
}

func TestCalendarDaysBetween(t *testing.T) {
	ist := IndiaLocation

	from := time.Date(2026, 2, 20, 15, 30, 0, 0, ist)
	to := time.Date(2026, 3, 2, 15, 30, 0, 0, ist)

	got := CalendarDaysBetween(from, to)
	if got != 10 {
		t.Errorf("Feb 20→Mar 2: expected 10 calendar days, got %d", got)
	}
}

func TestTTEYears(t *testing.T) {
	from := time.Date(2026, 2, 20, 15, 30, 0, 0, time.UTC)
	to := time.Date(2026, 3, 2, 15, 30, 0, 0, time.UTC)

	got := TTEYears(from, to)
	expected := 10.0 / 365.0 // exactly 10 days
	tolerance := 1e-9

	if got-expected > tolerance || expected-got > tolerance {
		t.Errorf("TTEYears: expected %.10f, got %.10f", expected, got)
	}
}
