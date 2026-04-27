package replay

import (
	"math"
	"time"
)

// Metrics represents comprehensive backtest performance metrics.
type Metrics struct {
	// Trade-level
	TotalTrades    int     `json:"total_trades"`
	ClosedTrades   int     `json:"closed_trades"`
	OpenTrades     int     `json:"open_trades"`
	Wins           int     `json:"wins"`
	Losses         int     `json:"losses"`
	WinRate        float64 `json:"win_rate"`
	AvgWin         float64 `json:"avg_win"`
	AvgLoss        float64 `json:"avg_loss"`
	Expectancy     float64 `json:"expectancy"`    // (winRate × avgWin) − (lossRate × avgLoss)
	ProfitFactor   float64 `json:"profit_factor"` // grossProfit / abs(grossLoss)
	GrossProfit    float64 `json:"gross_profit"`  // Sum of positive PnLs
	GrossLoss      float64 `json:"gross_loss"`    // Sum of negative PnLs (negative)
	AvgHoldingDays float64 `json:"avg_holding_days"`

	// Fees
	TotalFeesPaid  float64 `json:"total_fees_paid"`
	AvgFeePerTrade float64 `json:"avg_fee_per_trade"`

	// Portfolio-level
	RealizedPnL    float64 `json:"realized_pnl"`
	UnrealizedPnL  float64 `json:"unrealized_pnl"`
	FinalEquity    float64 `json:"final_equity"`
	InitialCash    float64 `json:"initial_cash"`
	ReturnPct      float64 `json:"return_pct"`
	MaxDrawdown    float64 `json:"max_drawdown"`     // INR (negative)
	MaxDrawdownPct float64 `json:"max_drawdown_pct"` // Percentage (negative)
	PeakEquity     float64 `json:"peak_equity"`

	// Daily stats
	BestDayPnL   float64 `json:"best_day_pnl"`
	WorstDayPnL  float64 `json:"worst_day_pnl"`
	TicksInMarket int    `json:"ticks_in_market"` // Ticks (minutes/days) with open positions
	TotalTicks    int    `json:"total_ticks"`     // Total replay ticks (minutes/days)
}

// EquityPoint represents a point in the equity curve.
type EquityPoint struct {
	Time          time.Time
	Equity        float64 // Liquidation equity (truth)
	MidEquity     float64 // Mid-mark equity (diagnostic)
	Realized      float64
	Unrealized    float64
	Cash          float64
	OpenPositions int
	Drawdown      float64 // Current drawdown from peak (negative)
	Peak          float64 // Running peak equity
}

// ComputeMetrics calculates comprehensive performance metrics from portfolio and equity curve.
func ComputeMetrics(pf *Portfolio, equityCurve []EquityPoint) Metrics {
	m := Metrics{
		TotalTrades: pf.TotalTrades,
		Wins:        pf.Wins,
		Losses:      pf.Losses,
		RealizedPnL: pf.RealizedPnL,
		InitialCash: pf.InitialCash,
	}

	// Count closed vs open
	for _, pos := range pf.Positions {
		if pos.ClosedAt != nil {
			m.ClosedTrades++
		} else {
			m.OpenTrades++
		}
	}

	// Win rate (from closed trades)
	if m.ClosedTrades > 0 {
		m.WinRate = float64(m.Wins) / float64(m.ClosedTrades)
	}

	// Gross profit/loss, avg win/loss, fees, holding period
	var totalHoldDays float64
	var holdCount int
	for _, pos := range pf.Positions {
		// Fees (all positions — open and closed)
		m.TotalFeesPaid += pos.EntryFee + pos.ExitFee

		if pos.ClosedAt != nil {
			// PnL breakdown
			if pos.RealizedPnL > 0 {
				m.GrossProfit += pos.RealizedPnL
			} else if pos.RealizedPnL < 0 {
				m.GrossLoss += pos.RealizedPnL
			}

			// Holding period
			days := pos.ClosedAt.Sub(pos.OpenedAt).Hours() / 24.0
			totalHoldDays += days
			holdCount++
		}
	}

	if m.Wins > 0 {
		m.AvgWin = m.GrossProfit / float64(m.Wins)
	}
	if m.Losses > 0 {
		m.AvgLoss = m.GrossLoss / float64(m.Losses) // Negative
	}
	if holdCount > 0 {
		m.AvgHoldingDays = totalHoldDays / float64(holdCount)
	}
	if m.ClosedTrades > 0 {
		m.AvgFeePerTrade = m.TotalFeesPaid / float64(m.ClosedTrades)
	}

	// Expectancy = (winRate × avgWin) − (lossRate × |avgLoss|)
	if m.ClosedTrades > 0 {
		lossRate := float64(m.Losses) / float64(m.ClosedTrades)
		m.Expectancy = (m.WinRate * m.AvgWin) - (lossRate * math.Abs(m.AvgLoss))
	}

	// Profit factor = grossProfit / |grossLoss|
	if m.GrossLoss < 0 {
		m.ProfitFactor = m.GrossProfit / math.Abs(m.GrossLoss)
	} else if m.GrossProfit > 0 {
		m.ProfitFactor = math.Inf(1) // All winners, no losers
	}

	// Equity curve metrics
	if len(equityCurve) > 0 {
		m.TotalTicks = len(equityCurve)
		m.FinalEquity = equityCurve[len(equityCurve)-1].Equity
		m.UnrealizedPnL = equityCurve[len(equityCurve)-1].Unrealized

		peak := equityCurve[0].Equity
		maxDD := 0.0
		var prevEquity float64

		for i, pt := range equityCurve {
			// Peak / drawdown
			if pt.Equity > peak {
				peak = pt.Equity
			}
			dd := pt.Equity - peak
			if dd < maxDD {
				maxDD = dd
			}

			// Time in market
			if pt.OpenPositions > 0 {
				m.TicksInMarket++
			}

			// Best / worst day PnL
			if i > 0 {
				dailyPnL := pt.Equity - prevEquity
				if dailyPnL > m.BestDayPnL {
					m.BestDayPnL = dailyPnL
				}
				if dailyPnL < m.WorstDayPnL {
					m.WorstDayPnL = dailyPnL
				}
			}
			prevEquity = pt.Equity
		}

		m.MaxDrawdown = maxDD
		m.PeakEquity = peak

		// Max drawdown percentage
		if peak > 0 {
			m.MaxDrawdownPct = (maxDD / peak) * 100.0
		}
	} else {
		m.FinalEquity = pf.Cash
	}

	// Return percentage
	if m.InitialCash > 0 {
		m.ReturnPct = ((m.FinalEquity - m.InitialCash) / m.InitialCash) * 100.0
	}

	// Round monetary values
	m.RealizedPnL = RoundINR(m.RealizedPnL)
	m.GrossProfit = RoundINR(m.GrossProfit)
	m.GrossLoss = RoundINR(m.GrossLoss)
	m.AvgWin = RoundINR(m.AvgWin)
	m.AvgLoss = RoundINR(m.AvgLoss)
	m.Expectancy = RoundINR(m.Expectancy)
	m.TotalFeesPaid = RoundINR(m.TotalFeesPaid)
	m.AvgFeePerTrade = RoundINR(m.AvgFeePerTrade)
	m.MaxDrawdown = RoundINR(m.MaxDrawdown)
	m.FinalEquity = RoundINR(m.FinalEquity)
	m.BestDayPnL = RoundINR(m.BestDayPnL)
	m.WorstDayPnL = RoundINR(m.WorstDayPnL)

	return m
}
