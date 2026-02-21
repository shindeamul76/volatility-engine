package replay

import (
	"fmt"
	"time"

	"volatility-engine/internal/app"
	"volatility-engine/internal/config"
)

type DeciderConfig struct {
	MinScore         float64
	MaxOpen          int
	TakeProfit       float64 // 0.50 means 50%
	StopLoss         float64 // 1.0 means 100% of debit
	MinDTEToClose    int     // Close if DTE <= this
	RollMinScoreDiff float64 // If new score > old + diff, roll
}

type Decider struct {
	Cfg                DeciderConfig
	PrevTopCandidateID string
	PrevTopScore       float64
}

func NewDecider(cfg config.ReplayConfig) *Decider {
	return &Decider{
		Cfg: DeciderConfig{
			MinScore:         cfg.MinScore,
			MaxOpen:          cfg.MaxOpenPositions,
			TakeProfit:       cfg.TakeProfit,
			StopLoss:         cfg.StopLoss,
			MinDTEToClose:    1, // Keep hardcoded or add to config if requested (not requested yet)
			RollMinScoreDiff: cfg.RollMinScoreDiff,
		},
	}
}

// DecisionLog captures the "Why"
type DecisionLog struct {
	Type   string
	Status string
	Reason string
	PosID  string
}

func (d *Decider) DecideExits(out *app.EngineOutput, pf *Portfolio) ([]OrderIntent, []DecisionLog) {
	var orders []OrderIntent
	var logs []DecisionLog

	mtmResults, _, _ := pf.MarkToMarket(out.Snapshot)
	mtmMap := make(map[string]MTMResult)
	for _, m := range mtmResults {
		mtmMap[m.PosID] = m
	}

	for _, pos := range pf.OpenPositions() {
		// 1. Check DTE
		// derive expiry from first leg (assuming all same expiry for now)
		if len(pos.LegFills) == 0 {
			continue
		}

		var expTime time.Time
		if !pos.LegFills[0].Leg.ExpiryTime.IsZero() {
			expTime = pos.LegFills[0].Leg.ExpiryTime
		} else {
			// Fallback (for old state or if not set)
			t, err := parseExpiry(pos.LegFills[0].Leg.Expiry)
			if err != nil {
				continue
			}
			expTime = t
		}

		// DTE = (Expiry - Now) in days
		dte := expTime.Sub(out.Snapshot.AsOf).Hours() / 24.0

		shouldClose := false
		reason := ""

		// Rule: Time Stop
		// If DTE is effectively 0 or less (expiry day), CLOSE.
		// Note from user: "DTE <= 1 -> close"
		if dte <= float64(d.Cfg.MinDTEToClose) { // e.g. 1.0
			shouldClose = true
			reason = fmt.Sprintf("DTE %.1f <= %d", dte, d.Cfg.MinDTEToClose)
		}

		// Rule: PnL based (TP / SL)
		// Use liquidation PnL (what we'd actually realize if we close now)
		// instead of raw unrealized MTM to avoid phantom TP/SL triggers.
		if !shouldClose {
			if res, ok := mtmMap[pos.ID]; ok {
				// Liquidation PnL = LiquidationValue + EntryPremium - estimated close fees
				// This is what we'd actually realize by closing at bid/ask.
				estimatedCloseFees := pos.EntryFee // Approximate: same fee structure as entry
				liqPnL := res.LiquidationValue + pos.EntryPremium - estimatedCloseFees

				entryDebit := pos.EntryPremium
				if entryDebit < 0 {
					entryDebit = -entryDebit
				}

				// Take Profit Target (e.g. +30% of debit)
				tpTarget := entryDebit * d.Cfg.TakeProfit

				// Stop Loss Target (e.g. -50% of debit)
				slTarget := -entryDebit * d.Cfg.StopLoss

				// Take Profit
				if liqPnL >= tpTarget {
					shouldClose = true
					reason = fmt.Sprintf("TP Hit: liq_pnl %.1f >= %.1f (%.0f%% of %.1f)",
						liqPnL, tpTarget, d.Cfg.TakeProfit*100, entryDebit)
				}

				// Stop Loss
				if !shouldClose && liqPnL <= slTarget {
					shouldClose = true
					reason = fmt.Sprintf("SL Hit: liq_pnl %.1f <= %.1f (%.0f%% of %.1f)",
						liqPnL, slTarget, d.Cfg.StopLoss*100, entryDebit)
				}
			}
		}

		if shouldClose {
			// Generate Close Order
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

			orders = append(orders, OrderIntent{
				PositionID: pos.ID,
				StrategyID: pos.StrategyID,
				Action:     ActionClose,
				Legs:       closeLegs,
			})

			logs = append(logs, DecisionLog{
				Type:   "DECISION_EXIT",
				Status: "CLOSE",
				Reason: reason,
				PosID:  pos.ID,
			})
		} else {
			detail := "PnL within limits"
			if res, ok := mtmMap[pos.ID]; ok {
				entryDebit := pos.EntryPremium
				if entryDebit < 0 {
					entryDebit = -entryDebit
				}
				estimatedCloseFees := pos.EntryFee
				liqPnL := res.LiquidationValue + pos.EntryPremium - estimatedCloseFees
				tpTarget := entryDebit * d.Cfg.TakeProfit
				slTarget := -entryDebit * d.Cfg.StopLoss
				detail = fmt.Sprintf("liq_pnl %.1f in range (SL: %.1f, TP: %.1f)", liqPnL, slTarget, tpTarget)
			}

			logs = append(logs, DecisionLog{
				Type:   "DECISION_EXIT",
				Status: "HOLD",
				Reason: fmt.Sprintf("DTE %.1f > %d, %s", dte, d.Cfg.MinDTEToClose, detail),
				PosID:  pos.ID,
			})
		}
	}

	return orders, logs
}

func (d *Decider) DecideEntries(out *app.EngineOutput, pf *Portfolio) ([]OrderIntent, []DecisionLog) {
	var logs []DecisionLog

	// 1. Check len(Candidates) == 0 first.
	if len(out.Candidates) == 0 {
		logs = append(logs, DecisionLog{
			Type:   "DECISION_ENTRY",
			Status: "SKIP",
			Reason: "No candidates generated",
		})
		return nil, logs
	}

	// 2. Define best.
	best := out.Candidates[0]

	// 3. Check/Update PrevTop.
	// Store current PrevTopScore before updating for potential roll comparison.
	previousTopScore := d.PrevTopScore
	previousTopCandidateID := d.PrevTopCandidateID

	if d.PrevTopCandidateID != "" && best.ID != d.PrevTopCandidateID {
		logs = append(logs, DecisionLog{
			Type:   "DECISION_CANDIDATE_CHANGE",
			Status: "INFO",
			Reason: fmt.Sprintf("New Top: %s (%.2f) replaced %s (%.2f)", best.ID, best.Quality.Score, d.PrevTopCandidateID, d.PrevTopScore),
		})
	}
	// Update state for the next iteration.
	d.PrevTopCandidateID = best.ID
	d.PrevTopScore = best.Quality.Score

	// 4. Check MaxOpen. If reached:
	if len(pf.OpenPositions()) >= d.Cfg.MaxOpen {
		// Check for Roll Opportunity
		// If new score > old + diff, roll.
		// 'old' score refers to the PrevTopScore from the *previous* decision cycle.
		if previousTopScore > 0 && best.Quality.Score >= previousTopScore+d.Cfg.RollMinScoreDiff {
			// ROLL: Close existing, Open new.
			// Assumption: MaxOpen=1, so we close the single open position.
			// If MaxOpen > 1, we might need to pick which one to close (e.g. lowest score, or matching strategy).
			// For this implementation, we close ALL logic or just the first one?
			// Let's close all to make room for the new "better" one, or just the first.

			var rollOrders []OrderIntent

			// Close existing
			for _, pos := range pf.OpenPositions() {
				// Construct closing legs
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
				rollOrders = append(rollOrders, OrderIntent{
					PositionID: pos.ID,
					StrategyID: pos.StrategyID,
					Action:     ActionClose,
					Legs:       closeLegs,
				})

				logs = append(logs, DecisionLog{
					Type:   "DECISION_EXIT",
					Status: "ROLL_CLOSE",
					Reason: fmt.Sprintf("Rolling from %s (score %.2f) to better candidate %s (Score %.2f > %.2f + %.2f)", previousTopCandidateID, previousTopScore, best.ID, best.Quality.Score, previousTopScore, d.Cfg.RollMinScoreDiff),
					PosID:  pos.ID,
				})
			}

			// Open New
			posID := pf.NewPositionID()
			var replayLegs []ReplayLeg
			// Parse Expiry Time once
			expTime, _ := parseExpiry(best.Expiry)

			for _, leg := range best.Legs {
				replayLegs = append(replayLegs, ReplayLeg{
					Side:       leg.Side,
					Type:       leg.Type,
					Strike:     leg.Strike,
					Qty:        leg.Qty,
					Expiry:     best.Expiry,
					ExpiryTime: expTime,
				})
			}
			rollOrders = append(rollOrders, OrderIntent{
				PositionID: posID,
				StrategyID: best.ID,
				Action:     ActionOpen,
				Legs:       replayLegs,
			})

			logs = append(logs, DecisionLog{
				Type:   "DECISION_ENTRY",
				Status: "ROLL_OPEN",
				Reason: fmt.Sprintf("Rolling into best candidate %s (Score %.2f)", best.ID, best.Quality.Score),
				PosID:  posID,
			})

			return rollOrders, logs
		}

		// Else (MaxOpen reached, no roll opportunity)
		logs = append(logs, DecisionLog{
			Type:   "DECISION_ENTRY",
			Status: "SKIP",
			Reason: fmt.Sprintf("Max positions reached (%d)", d.Cfg.MaxOpen),
		})
		return nil, logs
	}

	// 5. Check MinScore.
	if best.Quality.Score < d.Cfg.MinScore {
		logs = append(logs, DecisionLog{
			Type:   "DECISION_ENTRY",
			Status: "SKIP",
			Reason: fmt.Sprintf("Best candidate score %.2f < %.2f", best.Quality.Score, d.Cfg.MinScore),
		})
		return nil, logs
	}

	// Check for duplicates? (e.g. don't open same strategy if already open - simplistic check)
	// For now, allow trigger if we are under MaxOpen.

	// 6. Return Open order.
	posID := pf.NewPositionID()

	// Convert candidate legs to OrderIntent (OPEN uses same BUY/SELL as candidate)
	// Parse expiry once outside the loop to avoid redundant work
	expTime, _ := parseExpiry(best.Expiry)
	var replayLegs []ReplayLeg
	for _, leg := range best.Legs {
		replayLegs = append(replayLegs, ReplayLeg{
			Side:       leg.Side,
			Type:       leg.Type,
			Strike:     leg.Strike,
			Qty:        leg.Qty,
			Expiry:     best.Expiry, // Candidate-level expiry
			ExpiryTime: expTime,     // 15:30 IST
		})
	}

	logs = append(logs, DecisionLog{
		Type:   "DECISION_ENTRY",
		Status: "OPEN",
		Reason: fmt.Sprintf("Opening best candidate %s (Score %.2f)", best.ID, best.Quality.Score),
		PosID:  posID,
	})

	return []OrderIntent{{
		PositionID: posID,
		StrategyID: best.ID,
		Action:     ActionOpen,
		Legs:       replayLegs,
	}}, logs
}

func parseExpiry(exp string) (time.Time, error) {
	// RFC3339 first (manifest-style)
	if t, err := time.Parse(time.RFC3339, exp); err == nil {
		return t, nil
	}

	// Date-only (candidate-style)
	d, err := time.Parse("2006-01-02", exp)
	if err != nil {
		return time.Time{}, err
	}

	// Set to 15:30 IST
	loc, err := time.LoadLocation("Asia/Kolkata")
	if err != nil {
		// Fallback to FixedZone if system doesn't have timezone db
		loc = time.FixedZone("IST", 5*3600+1800)
	}
	return time.Date(d.Year(), d.Month(), d.Day(), 15, 30, 0, 0, loc), nil
}
