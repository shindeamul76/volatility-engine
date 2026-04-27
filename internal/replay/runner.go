package replay

import (
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"volatility-engine/internal/app"
	"volatility-engine/internal/config"
	"volatility-engine/internal/domain/market"
)

type Runner struct {
	Cfg       *config.Config
	Engine    *app.Engine
	Source    SnapshotSource
	Decider   *Decider
	RiskGate  *RiskGate
	Exec      *ExecSim
	Audit     *Auditor
	Alerts    *AlertService
	Pf        *Portfolio
	OutputDir string

	// Track for metrics
	PeakEquity  float64
	EquityCurve []EquityPoint
}

func (r *Runner) Run(baseDir string) error {
	descs, err := r.Source.List()
	if err != nil {
		return err
	}
	if len(descs) == 0 {
		return fmt.Errorf("no snapshot folders found in %s", baseDir)
	}

	// Setup CSV Reporters
	eqPath := filepath.Join(r.OutputDir, "equity.csv")
	
	needsHeader := false
	if _, err := os.Stat(eqPath); os.IsNotExist(err) {
		needsHeader = true
	}
	
	eqFile, err := os.OpenFile(eqPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0666)
	if err != nil {
		return err
	}
	defer eqFile.Close()
	eqWriter := csv.NewWriter(eqFile)
	defer eqWriter.Flush()

	if needsHeader {
		// Header: t, cash, equity, equity_mid, unrealized_pnl, realized_pnl, open_positions, peak, drawdown
		_ = eqWriter.Write([]string{"t", "cash", "equity", "equity_mid", "unrealized_pnl", "realized_pnl", "open_positions", "peak", "drawdown"})
	}

	var lastSnap *market.Snapshot
	for _, desc := range descs {
		man, err := r.Source.Load(desc)
		if err != nil {
			return err
		}

		// audit: snapshot loaded
		_ = r.Audit.Append(map[string]any{
			"t":      man.AsOf.UTC().Format(time.RFC3339),
			"type":   "SNAPSHOT_LOADED",
			"folder": desc.Folder,
		})

		// build app.SnapshotInput
		var files []app.FileSpec
		for _, f := range man.Files {
			files = append(files, app.FileSpec{Path: f.Path, Expiry: f.Expiry})
		}

		snapID := fmt.Sprintf("snap_%s_%s", man.AsOf.Format("20060102_150405"), man.Underlying)

		in := app.SnapshotInput{
			AsOf:         man.AsOf,
			Underlying:   man.Underlying,
			Spot:         man.Spot,
			RiskFreeRate: man.RiskFreeRate,
			Files:        files,
			SnapshotID:   snapID,
		}

		out, err := r.Engine.RunSnapshot(in)
		if err != nil {
			_ = r.Audit.Append(map[string]any{
				"t":    man.AsOf.UTC().Format(time.RFC3339),
				"type": "ENGINE_ERROR",
				"err":  err.Error(),
			})
			continue
		}

		// Restore detailed logs per snapshot in Replay
		lastSnap = out.Snapshot
		app.LogSnapshotSummary(out.Snapshot)
		app.LogCandidates(out.Candidates, &out.Surface, man.RiskFreeRate)

		_ = r.Audit.Append(map[string]any{
			"t":      man.AsOf.UTC().Format(time.RFC3339),
			"type":   "REGIME",
			"regime": out.Regime.Decision.Regime,
			"iv30":   out.Regime.IVReference.IV,
			"rank":   out.Regime.HistoricalContext.IVRank,
		})

		_ = r.Audit.Append(map[string]any{
			"t":     man.AsOf.UTC().Format(time.RFC3339),
			"type":  "CANDIDATES",
			"count": len(out.Candidates),
			"top": func() string {
				if len(out.Candidates) > 0 {
					return out.Candidates[0].ID
				}
				return ""
			}(),
		})

		// 1. MTM Logging
		mtmResults, totalEquity, totalMidEquity := r.Pf.MarkToMarket(out.Snapshot)

		// Record start-of-day equity for daily PnL circuit breaker
		if r.Pf.DayStartEquity == 0 {
			r.Pf.StartOfDay(totalEquity)
		}

		totalUnrealized := 0.0
		for _, m := range mtmResults {
			totalUnrealized += m.UnrealizedPnL
			_ = r.Audit.Append(map[string]any{
				"t":                 man.AsOf.UTC().Format(time.RFC3339),
				"type":              "MTM",
				"pos":               m.PosID,
				"liquidation_value": m.LiquidationValue, // Strict Bid/Ask
				"market_val_inr":    m.LiquidationValue, // Alias for clarity
				"mid_value":         m.MidValue,         // Analytics
				"unreal_pnl":        m.UnrealizedPnL,
				"equity":            totalEquity,    // Liquidation Equity (legacy field name)
				"equity_inr":        totalEquity,    // Explicit INR equity
				"equity_mid":        totalMidEquity, // Analytics Equity
				"price_mode":        "LIQUIDATION",  // Explicit mode
				"pnl_units":         "INR",
			})
		}

		// 2. Exits
		exitOrders, exitLogs := r.Decider.DecideExits(out, r.Pf)
		for _, log := range exitLogs {
			if log.Type == "DECISION_EXIT" && log.Status == "CLOSE" {
				r.Alerts.Dispatch(man.AsOf, AlertLevelInfo, "POSITION_EXIT", log.Reason, map[string]any{"pos": log.PosID})
			}
			_ = r.Audit.Append(map[string]any{
				"t":      man.AsOf.UTC().Format(time.RFC3339),
				"type":   log.Type,
				"status": log.Status,
				"reason": log.Reason,
				"pos":    log.PosID,
			})
		}

		for _, ord := range exitOrders {
			fill, err := r.Exec.FillOrder(out.Snapshot.AsOf, out.Snapshot, ord, r.Pf.ContractMultiplier)
			if err != nil {
				// audit fill failure?
				continue
			}
			r.Pf.ApplyFill(fill)
			legs := buildLegsLog(fill.LegFills)
			_ = r.Audit.Append(map[string]any{
				"t":          man.AsOf.UTC().Format(time.RFC3339),
				"type":       "FILL",
				"action":     string(fill.Action),
				"pos":        fill.PositionID,
				"net_points": fill.NetPremium,
				"net_inr":    fill.NetPremium * r.Pf.ContractMultiplier,
				"fee_inr":    fill.FeeINR,
				"legs":       legs, // Added
				"pnl_units":  "INR",
			})

			// POSITION_CLOSED event
			pos := r.Pf.Positions[fill.PositionID]
			if pos != nil && pos.ClosedAt != nil {
				holdDays := pos.ClosedAt.Sub(pos.OpenedAt).Hours() / 24.0
				_ = r.Audit.Append(map[string]any{
					"t":                man.AsOf.UTC().Format(time.RFC3339),
					"type":             "POSITION_CLOSED",
					"pos_id":           fill.PositionID,
					"realized_pnl_inr": pos.RealizedPnL, // Explicit name
					"realized_pnl":     pos.RealizedPnL, // Legacy
					"entry_fee":        pos.EntryFee,
					"exit_fee":         pos.ExitFee,
					"exit_inr":         pos.ExitPremium, // Already converted to INR
					"reason":           "EXIT_RULE",
					"hold_days":        holdDays,
				})
			}
		}

		// 3. Entries — with RiskGate evaluation
		entryOrders, entryLogs := r.Decider.DecideEntries(out, r.Pf)
		for _, log := range entryLogs {
			if log.Type == "DECISION_CANDIDATE_CHANGE" {
				r.Alerts.Dispatch(man.AsOf, AlertLevelInfo, "TOP_CANDIDATE_CHANGE", log.Reason, nil)
			}
			_ = r.Audit.Append(map[string]any{
				"t":      man.AsOf.UTC().Format(time.RFC3339),
				"type":   log.Type,
				"status": log.Status,
				"reason": log.Reason,
				"pos":    log.PosID,
			})
		}

		for _, ord := range entryOrders {
			if ord.Action != ActionOpen {
				// Non-entry orders (e.g. roll-close) skip RiskGate
				fill, err := r.Exec.FillOrder(out.Snapshot.AsOf, out.Snapshot, ord, r.Pf.ContractMultiplier)
				if err != nil {
					continue
				}
				r.Pf.ApplyFill(fill)
				legs := buildLegsLog(fill.LegFills)
				_ = r.Audit.Append(map[string]any{
					"t":          man.AsOf.UTC().Format(time.RFC3339),
					"type":       "FILL",
					"action":     string(fill.Action),
					"pos":        fill.PositionID,
					"net_points": fill.NetPremium,
					"net_inr":    fill.NetPremium * r.Pf.ContractMultiplier,
					"fee_inr":    fill.FeeINR,
					"legs":       legs,
					"pnl_units":  "INR",
				})
				continue
			}

			// ──── RiskGate evaluation for OPEN orders ────
			// Find the matching candidate for this order
			var candidate market.StrategyCandidate
			for _, c := range out.Candidates {
				if c.ID == ord.StrategyID {
					candidate = c
					break
				}
			}

			// Recalculate equity after any exit fills this snapshot
			_, currentEquity, _ := r.Pf.MarkToMarket(out.Snapshot)

			verdict := r.RiskGate.Evaluate(candidate, r.Pf, currentEquity, r.PeakEquity, r.Pf.ContractMultiplier)

			// Log the RiskGate verdict
			_ = r.Audit.Append(map[string]any{
				"t":            man.AsOf.UTC().Format(time.RFC3339),
				"type":         "RISK_GATE",
				"pos":          ord.PositionID,
				"action":       verdict.Action,
				"qty":          verdict.Qty,
				"original_qty": verdict.OriginalQty,
				"budget_inr":   verdict.BudgetINR,
				"risk_per_lot": verdict.RiskPerLot,
				"entry_cost":   verdict.EntryCostINR,
				"max_loss":     verdict.MaxLossINR,
				"reason":       fmt.Sprintf("%v", verdict.Reasons),
			})

			if verdict.Action == "REJECTED" {
				continue // Skip this order entirely
			}

			// Apply resized qty to legs if needed
			if verdict.Qty != verdict.OriginalQty {
				ord.Legs = ApplyQtyToLegs(ord.Legs, verdict.Qty)
			}

			// Proceed to fill
			fill, err := r.Exec.FillOrder(out.Snapshot.AsOf, out.Snapshot, ord, r.Pf.ContractMultiplier)
			if err != nil {
				continue
			}
			r.Pf.ApplyFill(fill)

			// Store max loss estimate on position for total risk tracking
			if pos := r.Pf.Positions[fill.PositionID]; pos != nil {
				pos.MaxLossEstimate = verdict.MaxLossINR
			}

			legs := buildLegsLog(fill.LegFills)

			_ = r.Audit.Append(map[string]any{
				"t":          man.AsOf.UTC().Format(time.RFC3339),
				"type":       "FILL",
				"action":     string(fill.Action),
				"pos":        fill.PositionID,
				"net_points": fill.NetPremium,
				"net_inr":    fill.NetPremium * r.Pf.ContractMultiplier,
				"fee_inr":    fill.FeeINR,
				"strategy":   fill.StrategyID,
				"legs":       legs,
				"pnl_units":  "INR",
			})

			// POSITION_OPENED event
			pos := r.Pf.Positions[fill.PositionID]
			_ = r.Audit.Append(map[string]any{
				"t":         man.AsOf.UTC().Format(time.RFC3339),
				"type":      "POSITION_OPENED",
				"pos_id":    fill.PositionID,
				"strategy":  fill.StrategyID,
				"entry_inr": pos.EntryPremium,
				"entry_fee": pos.EntryFee,
				"legs":      legs,
			})
		}

		// 4. Portfolio Snapshot
		// Update equity after fills
		// FIX: Recalculate Unrealized PnL based on new positions/fills
		freshMtmResults, finalEquity, totalMidEquity := r.Pf.MarkToMarket(out.Snapshot)
		freshUnrealized := 0.0
		for _, m := range freshMtmResults {
			freshUnrealized += m.UnrealizedPnL
		}

		// Track peak equity and compute drawdown
		if finalEquity > r.PeakEquity {
			r.PeakEquity = finalEquity
		}
		drawdown := finalEquity - r.PeakEquity // 0 or negative
		
		if r.PeakEquity > 0 {
			drawdownPct := drawdown / r.PeakEquity
			if drawdownPct <= -r.RiskGate.Cfg.MaxDrawdownPct {
				r.Alerts.Dispatch(man.AsOf, AlertLevelWarning, "DRAWDOWN_ALERT",
					fmt.Sprintf("Drawdown %.2f%% breached limit %.2f%%", drawdownPct*100, r.RiskGate.Cfg.MaxDrawdownPct*100),
					map[string]any{"drawdown": drawdown, "equity": finalEquity, "peak": r.PeakEquity})
			}
		}

		_ = r.Audit.Append(map[string]any{
			"t":          man.AsOf.UTC().Format(time.RFC3339),
			"type":       "PORTFOLIO",
			"cash":       r.Pf.Cash,
			"equity":     finalEquity,
			"equity_mid": totalMidEquity, // Added log
			"open_pos":   len(r.Pf.OpenPositions()),
			"unreal_pnl": freshUnrealized,
			"pnl_units":  "INR",
		})

		// Write to Equity CSV
		// Use Portfolio's RealizedPnL (already in INR)
		realizedSoFar := r.Pf.RealizedPnL

		_ = eqWriter.Write([]string{
			man.AsOf.UTC().Format(time.RFC3339),
			fmt.Sprintf("%.2f", r.Pf.Cash),
			fmt.Sprintf("%.2f", finalEquity),
			fmt.Sprintf("%.2f", totalMidEquity),
			fmt.Sprintf("%.2f", freshUnrealized),
			fmt.Sprintf("%.2f", realizedSoFar),
			strconv.Itoa(len(r.Pf.OpenPositions())),
			fmt.Sprintf("%.2f", r.PeakEquity),
			fmt.Sprintf("%.2f", drawdown),
		})
		eqWriter.Flush()

		// PORTFOLIO_DAILY event (track peak for drawdown)
		_ = r.Audit.Append(map[string]any{
			"t":          man.AsOf.UTC().Format(time.RFC3339),
			"type":       "PORTFOLIO_DAILY",
			"equity":     finalEquity,
			"realized":   realizedSoFar,
			"unrealized": freshUnrealized,
			"drawdown":   drawdown,
			"peak":       r.PeakEquity,
		})

		// Track equity curve for metrics
		r.EquityCurve = append(r.EquityCurve, EquityPoint{
			Time:          man.AsOf,
			Equity:        finalEquity,
			MidEquity:     totalMidEquity,
			Realized:      realizedSoFar,
			Unrealized:    freshUnrealized,
			Cash:          r.Pf.Cash,
			OpenPositions: len(r.Pf.OpenPositions()),
			Drawdown:      drawdown,
			Peak:          r.PeakEquity,
		})
	}

	// Force Close at end of replay?
	shouldClose := true
	if r.Cfg.Replay.CloseAllAtEnd != nil {
		shouldClose = *r.Cfg.Replay.CloseAllAtEnd
	}

	forceClosedOK := 0
	forceClosedIntrinsic := 0
	forceCloseFail := 0

	if shouldClose && lastSnap != nil {
		for _, pos := range r.Pf.OpenPositions() {
			// Construct closing order
			var closeLegs []ReplayLeg
			for _, fill := range pos.LegFills {
				oppSide := "SELL"
				if fill.Leg.Side == "SELL" {
					oppSide = "BUY"
				}
				closeLegs = append(closeLegs, ReplayLeg{
					Side:   oppSide,
					Type:   fill.Leg.Type,
					Strike: fill.Leg.Strike,
					Qty:    fill.Leg.Qty,
					Expiry: fill.Leg.Expiry,
				})
			}

			ord := OrderIntent{
				PositionID: pos.ID,
				StrategyID: pos.StrategyID,
				Action:     ActionClose,
				Legs:       closeLegs,
			}

			fill, err := r.Exec.FillOrder(lastSnap.AsOf, lastSnap, ord, r.Pf.ContractMultiplier)
			if err != nil {
				// Fallback: fill at intrinsic value
				_ = r.Audit.Append(map[string]any{
					"t":    lastSnap.AsOf.UTC().Format(time.RFC3339),
					"type": "FORCE_CLOSE_QUOTE_FAIL",
					"err":  err.Error(),
					"pos":  pos.ID,
					"note": "falling back to intrinsic-value fill",
				})

				fill = r.Exec.FillOrderAtIntrinsic(lastSnap.AsOf, lastSnap.Underlying.Spot, ord, r.Pf.ContractMultiplier)
				r.Pf.ApplyFill(fill)
				legs := buildLegsLog(fill.LegFills)
				_ = r.Audit.Append(map[string]any{
					"t":          lastSnap.AsOf.UTC().Format(time.RFC3339),
					"type":       "FILL",
					"action":     "CLOSE",
					"reason":     "FORCE_CLOSE_INTRINSIC",
					"pos":        fill.PositionID,
					"net_points": fill.NetPremium,
					"net_inr":    fill.NetPremium * r.Pf.ContractMultiplier,
					"fee_inr":    fill.FeeINR,
					"legs":       legs,
					"pnl_units":  "INR",
				})
				forceClosedIntrinsic++
			} else {
				r.Pf.ApplyFill(fill)
				legs := buildLegsLog(fill.LegFills)
				_ = r.Audit.Append(map[string]any{
					"t":          lastSnap.AsOf.UTC().Format(time.RFC3339),
					"type":       "FILL",
					"action":     "CLOSE",
					"reason":     "END_OF_REPLAY",
					"pos":        fill.PositionID,
					"net_points": fill.NetPremium,
					"net_inr":    fill.NetPremium * r.Pf.ContractMultiplier,
					"fee_inr":    fill.FeeINR,
					"legs":       legs,
					"pnl_units":  "INR",
				})
				forceClosedOK++
			}

			// POSITION_CLOSED event
			closedPos := r.Pf.Positions[fill.PositionID]
			if closedPos != nil && closedPos.ClosedAt != nil {
				holdDays := closedPos.ClosedAt.Sub(closedPos.OpenedAt).Hours() / 24.0
				closeReason := "END_OF_REPLAY"
				if err != nil {
					closeReason = "FORCE_CLOSE_INTRINSIC"
				}
				_ = r.Audit.Append(map[string]any{
					"t":                lastSnap.AsOf.UTC().Format(time.RFC3339),
					"type":             "POSITION_CLOSED",
					"pos_id":           fill.PositionID,
					"realized_pnl_inr": closedPos.RealizedPnL,
					"realized_pnl":     closedPos.RealizedPnL,
					"entry_fee":        closedPos.EntryFee,
					"exit_fee":         closedPos.ExitFee,
					"exit_inr":         closedPos.ExitPremium,
					"reason":           closeReason,
					"hold_days":        holdDays,
				})
			}
		}
	}

	// End of Replay Summary
	r.logSummary(lastSnap, forceClosedOK, forceClosedIntrinsic, forceCloseFail)

	// Generate Metrics + Stats
	metrics := ComputeMetrics(r.Pf, r.EquityCurve)

	// Write stats.json (comprehensive — replaces metrics.json)
	if err := WriteStatsJSON(r.OutputDir, metrics); err != nil {
		_ = r.Audit.Append(map[string]any{
			"type":  "STATS_ERROR",
			"error": err.Error(),
		})
	}

	// Write legacy metrics.json (backward compat)
	if err := WriteStatsJSON(r.OutputDir, metrics); err != nil {
		_ = r.Audit.Append(map[string]any{
			"type":  "METRICS_ERROR",
			"error": err.Error(),
		})
	}

	// Write stats.md (human readable)
	if err := WriteStatsMarkdown(r.OutputDir, metrics, r.Alerts); err != nil {
		_ = r.Audit.Append(map[string]any{
			"type":  "STATS_ERROR",
			"error": err.Error(),
		})
	}

	// Write daily.csv
	if err := WriteDailyCSV(r.OutputDir, r.EquityCurve); err != nil {
		_ = r.Audit.Append(map[string]any{
			"type":  "STATS_ERROR",
			"error": err.Error(),
		})
	}

	// Generate Trades CSV
	trPath := filepath.Join(r.OutputDir, "trades.csv")
	trFile, err := os.Create(trPath)
	if err != nil {
		return err
	}
	defer trFile.Close()
	trWriter := csv.NewWriter(trFile)
	defer trWriter.Flush()

	_ = trWriter.Write([]string{
		"pos_id", "strategy_id", "entry_time", "exit_time",
		"entry_cost", "exit_credit", "pnl_inr", "status",
		"hold_days", "entry_fee", "exit_fee", "total_fees",
	})

	for _, pos := range r.Pf.Positions {
		entryCost := pos.EntryPremium // already INR
		exitCredit := 0.0
		pnl := 0.0
		status := "OPEN"
		exitTime := ""
		holdDays := 0.0

		if pos.ClosedAt != nil {
			status = "CLOSED"
			exitTime = pos.ClosedAt.UTC().Format(time.RFC3339)
			exitCredit = pos.ExitPremium // already INR
			pnl = pos.RealizedPnL        // already INR
			holdDays = pos.ClosedAt.Sub(pos.OpenedAt).Hours() / 24.0
		}

		totalFees := pos.EntryFee + pos.ExitFee

		_ = trWriter.Write([]string{
			pos.ID,
			pos.StrategyID,
			pos.OpenedAt.UTC().Format(time.RFC3339),
			exitTime,
			fmt.Sprintf("%.2f", entryCost),
			fmt.Sprintf("%.2f", exitCredit),
			fmt.Sprintf("%.2f", pnl),
			status,
			fmt.Sprintf("%.1f", holdDays),
			fmt.Sprintf("%.2f", pos.EntryFee),
			fmt.Sprintf("%.2f", pos.ExitFee),
			fmt.Sprintf("%.2f", totalFees),
		})
	}

	return nil
}

func (r *Runner) logSummary(lastSnap *market.Snapshot, forceClosedOK, forceClosedIntrinsic, forceCloseFail int) {
	// Calculate Final Equity (Cash + Unrealized of Open)
	finalEquity := r.Pf.Cash
	unrealized := 0.0
	openCount := 0

	if lastSnap != nil {
		results, eq, _ := r.Pf.MarkToMarket(lastSnap)
		finalEquity = eq
		for _, res := range results {
			unrealized += res.UnrealizedPnL
		}
		openCount = len(results)
	}

	_ = r.Audit.Append(map[string]any{
		"type":                   "PORTFOLIO_SUMMARY",
		"total_trades":           r.Pf.TotalTrades,
		"open_positions":         openCount,
		"wins":                   r.Pf.Wins,
		"losses":                 r.Pf.Losses,
		"realized_pnl":           r.Pf.RealizedPnL,
		"unrealized_pnl":         unrealized,
		"final_cash":             r.Pf.Cash,
		"final_equity":           finalEquity,
		"pnl_units":              "INR",
		"force_closed_ok":        forceClosedOK,
		"force_closed_intrinsic": forceClosedIntrinsic,
		"force_close_fail":       forceCloseFail,
	})
}

func buildLegsLog(fills []LegFill) []map[string]any {
	var legs []map[string]any
	for _, lf := range fills {
		legs = append(legs, map[string]any{
			"side":          lf.Leg.Side,
			"type":          lf.Leg.Type,
			"strike":        lf.Leg.Strike,
			"qty":           lf.Leg.Qty,
			"fill_px":       lf.FillPrice,
			"fill_basis":    lf.FillBasis,
			"slippage_mode": lf.SlippageMode,
			"slippage_amt":  lf.SlippageAmt,
			"tick_size":     lf.TickSize,
		})
	}
	return legs
}
