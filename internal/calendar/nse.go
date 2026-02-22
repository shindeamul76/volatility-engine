package calendar

import "time"

// NSE holidays for 2026 (official schedule).
// Source: NSE India Circular — Trading & Clearing Holidays 2026.
//
// Note: Nov 8 (Diwali Laxmi Pujan) is a trading holiday; Muhurat Trading
// is conducted separately but regular sessions are closed.
// Holidays falling on weekends (already non-trading) are NOT listed:
//
//	Feb 15 (Sun) Mahashivratri, Mar 21 (Sat) Id-Ul-Fitr,
//	Aug 15 (Sat) Independence Day, Nov 8 (Sun) Diwali Laxmi Pujan.
var nseHolidays2026 = map[string]bool{
	// ── Trading Holidays ──
	"2026-01-15": true, // Municipal Corporation Election - Maharashtra
	"2026-01-26": true, // Republic Day
	"2026-03-03": true, // Holi (Second Day)
	"2026-03-26": true, // Ram Navami
	"2026-03-31": true, // Mahavir Jayanti
	"2026-04-03": true, // Good Friday
	"2026-04-14": true, // Dr. Babasaheb Ambedkar Jayanti
	"2026-05-01": true, // Maharashtra Din / Buddha Pournima
	"2026-05-28": true, // Bakri Id (Id-Uz-Zuha)
	"2026-06-26": true, // Muharram
	"2026-09-14": true, // Ganesh Chaturthi
	"2026-10-02": true, // Mahatma Gandhi Jayanti
	"2026-10-20": true, // Dussehra
	"2026-11-10": true, // Diwali (Bali Pratipada)
	"2026-11-24": true, // Guru Nanak Jayanti
	"2026-12-25": true, // Christmas
	// ── Additional Clearing Holidays ──
	"2026-02-19": true, // Chhatrapati Shivaji Maharaj Jayanti
	"2026-03-19": true, // Gudhi Padwa
	"2026-04-01": true, // Annual Bank Closing
	"2026-08-26": true, // Id-E-Milad
}

// IndiaLocation is the canonical IST timezone used for date operations.
var IndiaLocation = func() *time.Location {
	loc, err := time.LoadLocation("Asia/Kolkata")
	if err != nil {
		// Fallback to fixed offset if tz database is missing
		loc = time.FixedZone("IST", 5*3600+30*60)
	}
	return loc
}()

// IsNSETradingDay returns true if the given time falls on a
// weekday that is NOT an NSE-recognized holiday.
func IsNSETradingDay(t time.Time) bool {
	t = t.In(IndiaLocation)
	wd := t.Weekday()
	if wd == time.Saturday || wd == time.Sunday {
		return false
	}
	key := t.Format("2006-01-02")
	return !nseHolidays2026[key]
}

// TradingDaysBetween counts the number of NSE trading sessions
// strictly between 'from' (exclusive) and 'to' (inclusive, up to
// end-of-day). Both times are normalized to IST dates.
func TradingDaysBetween(from, to time.Time) int {
	from = truncateToDate(from.In(IndiaLocation))
	to = truncateToDate(to.In(IndiaLocation))

	if !to.After(from) {
		return 0
	}

	count := 0
	cursor := from.AddDate(0, 0, 1) // start the day after 'from'
	for !cursor.After(to) {
		if IsNSETradingDay(cursor) {
			count++
		}
		cursor = cursor.AddDate(0, 0, 1)
	}
	return count
}

// CalendarDaysBetween returns the number of calendar days between
// two timestamps, based on their IST-date boundaries.
func CalendarDaysBetween(from, to time.Time) int {
	from = truncateToDate(from.In(IndiaLocation))
	to = truncateToDate(to.In(IndiaLocation))
	days := int(to.Sub(from).Hours() / 24)
	if days < 0 {
		return 0
	}
	return days
}

// TTEYears computes precise time-to-expiry in years (ACT/365)
// using the actual hour-level difference between two timestamps.
// This is the most accurate method for pricing, as it preserves
// intraday time remaining (e.g. "3:30 PM IST today to 3:30 PM
// IST on expiry day").
func TTEYears(from, to time.Time) float64 {
	diff := to.Sub(from)
	if diff <= 0 {
		return 0
	}
	return diff.Hours() / 24.0 / 365.0
}

// truncateToDate returns midnight IST for the given time.
func truncateToDate(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, t.Location())
}
