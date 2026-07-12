# Volatility Engine — Explained Simply

> **For**: A developer with Node.js experience who is learning Go.
> **Goal**: Understand what this project does, why it exists, and how it works — without needing a finance degree.

---

## The Big Idea in One Sentence

This is a **"trader's brain" program** written in Go. It reads stock option prices, figures out if the market is overpricing or underpricing the risk of those options, and then recommends (or simulates) trades that profit from that mispricing — while making sure you don't lose too much money if things go wrong.

---

## What Problem Does It Solve?

Imagine you want to bet that a stock won't move much in the next 30 days. You could sell options (collecting a premium), but what if the stock crashes? You could lose a lot.

Professional traders don't just guess. They:
1. **Measure** what the market thinks volatility will be (implied volatility).
2. **Compare** it to what volatility has actually been in the past.
3. **Build strategies** like "Iron Condors" or "Straddles" that make money if they're right and limit losses if they're wrong.
4. **Simulate** everything on historical data before risking real money.

This program automates steps 1-4.

---

## The 30,000-Foot View: How Data Flows Through the System

```
CSV Option Chain Data
        ↓
   [Data Ingestion]
        ↓
   [IV Solver]  ← "What volatility does the market imply from these prices?"
        ↓
   [Surface Builder]  ← "What does the volatility landscape look like?"
        ↓
   [Regime Detector]  ← "Is implied vol high, low, or normal vs history?"
        ↓
   [Strategy Selector]  ← "Given the regime, what trades make sense?"
        ↓
   [Risk Engine]  ← "If the market moves against us, how bad is it?"
        ↓
   [Portfolio Simulator]  ← "Let's paper-trade this on historical data"
        ↓
   Reports, Alerts, Equity Curve
```

---

## Key Concepts (Explained Like You're Five... ish)

### 1. Options
An **option** is a contract that gives someone the right (but not obligation) to buy or sell a stock at a set price by a set date.
- **Call** = right to buy
- **Put** = right to sell
- **Strike** = the agreed price
- **Expiry** = the deadline

Example: A "NIFTY 25000 Call expiring March 30" gives you the right to buy NIFTY at 25000 before March 30.

### 2. Implied Volatility (IV)
If a stock is very jumpy, options become expensive (higher chance of big moves). If it's calm, options are cheap.

**Implied Volatility** is the "fear factor" baked into option prices. It answers: *"How much does the market think this stock will move?"*

This project uses the **Black-Scholes model** (a famous math formula) to reverse-engineer IV from market prices.

### 3. Volatility Surface
IV isn't the same for every strike and expiry. Out-of-the-money puts might have higher IV than at-the-money calls. The **volatility surface** is a 3D view of IV across different strikes and expiries.

### 4. Volatility Regime
Is IV currently high or low compared to the past 30-90 days?
- **High vol regime** → options are expensive → good time to *sell* options
- **Low vol regime** → options are cheap → good time to *buy* options
- **Neutral** → stay cautious

### 5. Option Strategies
Instead of buying a single option, professionals combine multiple options:
- **Iron Condor**: Sell a call spread + sell a put spread. You profit if the stock stays in a range.
- **Long Straddle**: Buy a call + buy a put at the same strike. You profit if the stock moves a lot (either direction).
- **Bull Put Spread**: Buy a put + sell a lower-strike put. You profit if the stock goes up or stays flat.

### 6. Greeks
These measure how sensitive an option's price is to different factors:
- **Delta**: Sensitivity to stock price moves
- **Gamma**: How fast Delta changes
- **Theta**: Time decay (options lose value as expiry approaches)
- **Vega**: Sensitivity to volatility changes

This project calculates Greeks for every position and uses them for risk management.

---

## Architecture: How the Code is Organized

If you're coming from Node.js, think of this as a **modular monolith** — one deployable binary, but cleanly separated into packages.

### Folder Structure (Simplified)

```
volatility-engine/
├── cmd/volengine/          ← Entry point (like index.js)
│   └── main.go             ← Parses CLI args, runs snapshot/replay/live
├── internal/
│   ├── app/                ← "Glue" layer — orchestrates the pipeline
│   ├── config/             ← Reads config.yaml
│   ├── domain/market/      ← Pure data models (like TypeScript interfaces)
│   ├── domain/risk/        ← Risk scenario models
│   ├── engines/            ← High-level engines (payoff, risk, sim)
│   ├── quant/              ← "Quant" = math-heavy stuff
│   │   ├── iv/             ← Implied volatility solver
│   │   ├── pricing/        ← Black-Scholes pricing + Greeks
│   │   ├── surface/        ← Volatility surface construction
│   │   ├── regime/         ← Regime detection
│   │   ├── strategy/       ← Strategy selection
│   │   └── intel/          ← "Intelligence" layer for scoring
│   ├── replay/             ← Backtesting engine
│   │   ├── runner.go       ← Main backtest loop
│   │   ├── decider_rules.go← Trade entry/exit logic
│   │   ├── portfolio.go    ← Tracks positions & P&L
│   │   ├── execsim.go      ← Simulates slippage & fees
│   │   └── riskgate.go     ← Circuit breakers & risk limits
│   ├── nsefetch/           ← Fetches live NSE (Indian stock exchange) data
│   ├── ingest/             ← CSV parsing
│   ├── chain/              ← Option chain processing
│   └── service/report/     ← Report generation (JSON, Markdown)
├── docs/                   ← Documentation
├── testdata/               ← Sample data for testing
└── config.yaml             ← Tunable parameters (strikes, risk limits, etc.)
```

### Design Patterns You'll See

| Pattern | Where | What It Does |
|---------|-------|--------------|
| **Pipeline** | `app/engine.go` | Data flows through ingest → IV → surface → regime → strategy in stages |
| **Strategy Pattern** | `quant/strategy/selector.go` | Different selection logic for different regimes |
| **Builder** | `quant/surface/builder.go` | Step-by-step construction of a volatility surface |
| **Simulation / Backtesting** | `replay/runner.go` | Runs the engine on historical snapshots, simulating trades |
| **Circuit Breaker** | `replay/riskgate.go` | Stops trading if losses exceed thresholds |

### Go-Specific Things to Notice

1. **Structs instead of classes**: Go doesn't have classes. "Objects" are `struct` types with methods.
   ```go
   type Engine struct {
       cfg *config.Config
   }

   func NewEngine(cfg *config.Config) *Engine {
       return &Engine{cfg: cfg}
   }
   ```

2. **Interfaces for abstraction**: The `replay` package uses interfaces like `SnapshotSource` so it can work with both filesystem data (`SourceFS`) and live API data (`SourceNSE`).

3. **No exceptions**: Go uses `error` returns. You'll see `if err != nil` everywhere.

4. **Pointers**: `*config.Config` means "a pointer to a Config struct." This avoids copying large structs.

---

## The Three Operating Modes

### 1. `snapshot-run`
Analyzes **one moment in time**. Give it a CSV of option prices, and it tells you:
- What's the current volatility regime?
- What strategies look attractive?
- What are the risk scenarios?

```bash
go run cmd/volengine/main.go snapshot-run
```

### 2. `replay-run`
Backtests on **historical data**. It walks through past snapshots day by day, makes pretend trades, and tracks how much money you would've made or lost.

```bash
go run cmd/volengine/main.go replay-run
```

### 3. `live-run`
Fetches **real-time data** from NSE (National Stock Exchange of India) every N minutes and runs the analysis pipeline. Includes fallback to historical data if live fetch fails.

```bash
go run cmd/volengine/main.go live-run
```

---

## A Concrete Example Walkthrough

Let's say you run `snapshot-run` with NIFTY option data for February 2026.

**Step 1: Ingest**
The program reads CSVs like `option-chain-ED-NIFTY-17-Feb-2026.csv`. Each row is one option contract with bid, ask, strike, expiry, etc.

**Step 2: IV Solving**
For each option, the program asks: *"Given the market price, what volatility makes the Black-Scholes formula match that price?"*

It uses a **hybrid Newton-Raphson + Bisection solver** — fancy words for "smart guessing until the math lines up."

**Step 3: Surface Building**
The program collects all the IVs and fits curves to create a volatility surface. It discards low-quality data (wide bid-ask spreads, low liquidity).

**Step 4: Regime Detection**
It compares the current 30-day implied volatility (IV30) to historical data:
- IV30 = 18%, historical average = 16% → **slightly high vol**
- Score = rank vs percentile vs richness → decides between HIGH_VOL, LOW_VOL, NEUTRAL

**Step 5: Strategy Selection**
In a high-vol regime, the program looks for **short volatility defined-risk** strategies — trades that profit if volatility falls and the market stays calm.

It might recommend an **Iron Condor**:
- Sell a call spread (bet stock won't go up much)
- Sell a put spread (bet stock won't go down much)
- Collect a net credit
- Max loss is the width of the spreads minus the credit

**Step 6: Risk Scenarios**
The program runs stress tests:
- What if spot moves +10% overnight?
- What if volatility jumps +5 points?
- What if 7 days pass?

It calculates the P&L for each scenario and ensures the worst-case loss is within your configured limits.

**Step 7: Reporting**
Outputs a JSON/Markdown report with:
- Top 3 candidates
- Greeks for each
- Scenario matrix
- Risk metrics

---

## Configuration (config.yaml)

The behavior is fully tunable without touching code:

```yaml
market:
  underlying: NIFTY
  spot: 25455.35

pricing:
  risk_free_rate: 0.065  # 6.5% annualized

risk:
  contract_multiplier: 65
  take_profit: 0.3      # Close at 30% profit
  stop_loss: 0.5        # Close at 50% loss

selection:
  log_moneyness_window: 0.10  # Only use strikes near the money
  min_points_per_expiry: 20   # Need enough data to fit curves

risk_gate:
  max_debit_per_trade_inr: 25000
  max_loss_per_trade_inr: 50000
  max_drawdown_pct: 0.10      # Stop if down 10%
```

---

## How to Think About This as a Developer

If this were a Node.js app, it might be structured like:
- **Express routes** → CLI commands in `main.go`
- **Service layer** → `internal/app/` and `internal/service/`
- **Domain models** → TypeScript interfaces in `internal/domain/`
- **Math libraries** → The `internal/quant/` packages
- **Database/ORM** → In-memory structs + CSV parsing
- **Background jobs** → The `replay` and `live` loops

The difference is Go's emphasis on:
- **Explicit error handling** (no `try/catch`)
- **Value semantics** (structs copied by default, pointers used intentionally)
- **Composition over inheritance** (no classes, just structs embedding other structs)
- **Concurrency primitives** (goroutines, channels — though this project is mostly sequential)

---

## Why This Matters

This isn't just a calculator. It's a **decision support system** for options traders. Instead of manually scanning option chains, doing mental math, and risking emotional decisions, the program:

1. **Systematizes** the analysis
2. **Quantifies** risk before entering a trade
3. **Backtests** ideas on historical data
4. **Enforces discipline** through automated risk gates

For someone learning Go, it's also a great example of a **real-world domain model** with complex business logic cleanly separated from I/O and orchestration.

---

## Quick Start

```bash
# 1. Run a single snapshot analysis
go run cmd/volengine/main.go snapshot-run

# 2. Run a backtest on historical data
go run cmd/volengine/main.go replay-run

# 3. Run tests
go test ./...

# 4. Check the logs
tail -f volengine.log
```

---

## Summary

| Question | Answer |
|----------|--------|
| **What is it?** | A Go program that analyzes options prices, finds mispriced volatility, and recommends/simulates trades. |
| **Who uses it?** | Options traders (especially in Indian markets via NSE). |
| **What does it do well?** | IV calculation, volatility regime detection, strategy selection, risk simulation, backtesting. |
| **What tech should I learn to understand it?** | Go basics (structs, pointers, interfaces, error handling) + basic options concepts. |
| **Is it production-ready?** | It's a prototype/sophisticated tool. Live trading would need more safeguards. |

---

*Have questions about a specific package or algorithm? Ask away!*
