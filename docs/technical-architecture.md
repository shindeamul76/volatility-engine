# Technical Architecture & Build Spec

> **Status:** Draft v1 · Engineering spec for the Strategy Risk & Compliance platform
> **Companion to:** [vision-and-roadmap.md](vision-and-roadmap.md)
> **Scope:** V0 (Strategy Risk & Compliance Report generator) in full detail, plus the evolution path to the risk-infrastructure API.

---

## 1. Overview

We are turning the existing `volatility-engine` (today a CLI monolith) into a **service** that ingests a customer's strategy + trades, runs them through our quant engine, and produces a compliance-grade risk report + tamper-evident audit trail.

**The 5 product pillars map to components:**

| Pillar | Component | Reuses existing code? |
|---|---|---|
| 1. Data quality score | Normalization & Validation | New |
| 2. Execution realism | Execution Analysis (on `execsim`) | ~Yes |
| 3. Risk analysis | Risk & Analytics Engine (`quant/*`) | ✅ Yes |
| 4. Backtest-to-live reconciliation | Reconciliation Engine | Partial |
| 5. Audit trail | Audit Ledger (`audit_jsonl` → hash-chained) | ~Yes |

**Design principle:** the quant math is done. V0 is mostly **ingest + normalize + package + deliver** around the existing engine.

---

## 2. System Architecture

### V0 (monolith-as-a-service)

```
                         ┌─────────────────────────────────────────┐
   Upload (files/API)    │            API Gateway (REST)            │
   ─────────────────────▶│   auth · rate-limit · job submission    │
                         └───────────────────┬─────────────────────┘
                                             │ enqueue job
                                             ▼
                         ┌─────────────────────────────────────────┐
                         │              Worker (Go)                 │
                         │                                          │
   ┌─────────────┐       │  1. Ingest & Connectors ──┐             │
   │ Object store │◀──────│  2. Normalize + DQ score  │             │
   │  (raw files) │       │  3. Risk & Analytics ─────┼─▶ quant/*   │
   └─────────────┘       │  4. Reconciliation        │             │
                         │  5. Report + Audit Ledger │             │
                         └───────────────┬────────────┘             │
                                         │ persist                  │
                         ┌───────────────▼─────────────────────────┐
                         │  Postgres (jobs, reports, audit chain)   │
                         └───────────────┬─────────────────────────┘
                                         ▼
                         Report (PDF/JSON) + Audit trail  ──▶ customer
```

### Evolution to infrastructure (Phase 2+)
- The same **Risk & Analytics core** is exposed as a **low-latency risk API** (gRPC/REST) that brokers call in-line.
- Batch (report jobs) and real-time (risk checks) share one engine; only the transport differs.
- Multi-tenancy, per-tenant data isolation, and usage metering are added.

---

## 3. Technology Stack

| Layer | Choice | Rationale |
|---|---|---|
| Core engine & services | **Go** | Reuse the existing engine; great for concurrent, low-latency risk compute |
| API | REST (V0) → **gRPC** (Phase 2 real-time) | REST is simple for uploads; gRPC for broker-embedded latency |
| Persistence | **PostgreSQL** | Jobs, reports, audit chain, tenants; JSONB for flexible report payloads |
| Object storage | S3-compatible (AWS S3 / Cloudflare R2) | Raw uploaded files, generated PDFs |
| Job queue | Postgres-backed queue (V0) → NATS/Redis (scale) | Keep V0 dependency-light |
| AI layer | **Claude API** (Sonnet 5 for ingest-mapping/narration; Haiku 4.5 for cheap classification) | LLM for messy-CSV mapping + report narration |
| PDF generation | HTML template → headless render (or a Go PDF lib) | Compliance report as branded PDF |
| Frontend | Minimal React (upload + dashboard) | Thin; the value is server-side |
| Deploy | Containers (Docker) on a single VM → managed k8s later | Start simple |

---

## 4. Canonical Data Model (the heart)

Everything ingested maps to **one internal representation**. This is the single most important abstraction — connectors normalize *into* it; the engine and reports read *from* it. It extends your existing `internal/domain` models.

```go
// A customer's strategy definition (rules or parameters).
type Strategy struct {
    ID          string
    TenantID    string
    Name        string
    Underlying  string          // e.g. "NIFTY"
    Kind        StrategyKind    // IronCondor, Straddle, Custom, ...
    Legs        []LegTemplate   // structural definition
    Rules       EntryExitRules  // entry/exit/adjust logic (may be opaque = black-box)
    IsBlackBox  bool            // drives SEBI RA report requirements
}

// One executed/simulated trade = a set of legs opened & closed.
type Trade struct {
    ID          string
    StrategyID  string
    OpenedAt    time.Time
    ClosedAt    *time.Time
    Legs        []Leg
    Source      DataSource      // Backtest | Live | Paper
}

type Leg struct {
    Contract    OptionContract  // reuse domain/market
    Side        Side            // Buy/Sell
    Qty         int
    Fills       []Fill          // actual/assumed fills
}

type Fill struct {
    At          time.Time
    Price       float64
    PriceBasis  PriceBasis      // Mid | Bid | Ask | LTP | Modeled  ← key for DQ + execution realism
    Qty         int
    Source      DataSource
}

// Market context needed to price/scenario the trade.
type MarketSnapshot struct {
    AsOf        time.Time
    Spot        float64
    Forward     float64
    RiskFreeRate float64
    Chain       []OptionQuote    // strike/expiry → bid/ask/IV/greeks (reuse existing)
}

// The graded output of ingestion.
type DataQualityReport struct {
    Score       float64          // 0–100
    Findings    []DQFinding      // each with severity + evidence
}

// The final risk/compliance report payload (persisted as JSONB, rendered to PDF).
type ComplianceReport struct { /* see §8 */ }

// One immutable audit event (hash-chained).
type AuditEvent struct {
    Seq         uint64
    At          time.Time
    Kind        string           // ingest | normalize | price | scenario | gate | reconcile | render
    PayloadHash string           // sha256 of the event payload
    PrevHash    string           // chain link
    Hash        string           // sha256(Seq|At|Kind|PayloadHash|PrevHash)
}
```

---

## 5. Component Specs

### 5.1 Ingest & Connectors

**Problem:** every source (AlgoTest export, Stockmock, TradingView, Python backtest, Zerodha/Angel/Dhan tradebook, orderbook, historical option-chain) has a *different schema*. This is the real engineering.

**Pattern:** a `Connector` interface (mirrors your existing `SnapshotSource` abstraction in `replay`):

```go
type Connector interface {
    Detect(file RawFile) bool                       // sniff format
    Parse(file RawFile) (Ingested, error)           // → intermediate rows
    MapToCanonical(Ingested) (CanonicalBundle, []MappingIssue, error)
}
```

**Connector registry:** V0 ships **2–3 connectors** (pick the highest-volume first — likely an AlgoTest/Python backtest export + one broker tradebook CSV). Each new format = one connector.

**AI-assisted mapping (the accelerant):** for unknown/variant CSVs, an LLM (Claude Sonnet 5) infers the column mapping ("which column is fill price? timestamp? strike? side?") and proposes a `CanonicalBundle`, which a deterministic validator then checks. This collapses weeks of parser work and lets us onboard new formats fast.

**Existing reuse:** `internal/ingest/csv_reader.go`, `internal/chain/processor.go`.

---

### 5.2 Normalization & Data Quality Scoring (Pillar 1)

After parsing, we grade the data. **This is a differentiator — nobody scores backtest data quality.**

**Checks (each → severity + evidence):**

| Check | What it catches | Signal |
|---|---|---|
| **Fill-price basis** | Backtest filled at **Mid/LTP** instead of **Bid/Ask** | Inflated returns; the single biggest DQ lie |
| **Look-ahead bias** | Trade uses data timestamped *after* decision time | Impossible fills |
| **Stale quotes** | Quote timestamp far from trade time | Unrealistic pricing |
| **Bid-ask spread sanity** | Fills inside/at implausible spread | Optimistic execution |
| **Chain completeness** | Missing strikes/expiries in option-chain | Can't price/scenario reliably |
| **Timestamp alignment** | Trade vs. market-snapshot clock drift | Reconciliation errors downstream |
| **Survivorship** | Only winning periods present | Cherry-picked backtest |
| **Liquidity vs. size** | Trade size >> available depth | Unfillable in reality |

**Output:** `DataQualityReport{ Score, Findings[] }`. Score feeds the final report; severe findings can gate report generation ("data too poor to certify").

---

### 5.3 Risk & Analytics Engine (Pillars 2 & 3) — mostly existing

Runs the normalized trades/strategy through the **existing quant engine**:

- **IV & pricing:** `internal/quant/iv/solver.go`, `internal/quant/pricing/pricing.go`
- **Greeks:** `internal/quant/risk/greeks.go` — per-leg and net (Δ, Γ, Θ, V)
- **Scenario / stress:** `internal/quant/risk/scenario.go` — spot ±%, IV ±pts, time decay matrices
- **Payoff:** `internal/quant/risk/payoff.go`, `internal/engines/payoff/builder.go`
- **Risk gates:** `internal/replay/riskgate.go` — for each gate, record **pass/fail + the computed number vs. the limit + the driver** (this is the "why" audit)
- **Execution realism (Pillar 2):** re-run fills through `internal/replay/execsim.go` (slippage/fees) to compute *realistic* P&L vs. the backtest's *assumed* P&L.

**Output:** a `RiskAnalysis` struct — greeks, scenario matrix, gate ledger, realistic-vs-assumed P&L.

---

### 5.4 Reconciliation Engine (Pillar 4) — the hard new algorithm

**Goal:** given a **backtest** (expected) and a **live tradebook** (realized), explain the P&L gap.

**Step 1 — Match trades (entity resolution).**
Align backtest trades ↔ live trades by (underlying, legs' strike/expiry/right, side, approximate timestamp). Fuzzy match with a scoring function; unmatched items are surfaced, not hidden.

**Step 2 — Attribute the gap into buckets:**

```
ΔP&L (backtest − live) =
     Slippage         (entry/exit fill price vs. backtest assumption)
   + Timing           (gate/exit fired at a different time live vs. backtest)
   + Fees & taxes     (real brokerage/STT/charges vs. modeled)
   + IV/Greek moves   (realized IV/spot path vs. backtest path)
   + Gate interference (a risk-gate blocked/changed a live trade)
   + Unexplained residual
```

Each bucket is computed by **holding the others constant** (a waterfall). The residual is reported honestly — a large residual means the backtest and live diverge structurally (different strategy/params), which is itself a finding.

**Constraint (documented in vision doc):** clean reconciliation requires the *same* strategy/params in both inputs. When they differ, we still deliver per-file DQ + risk analysis, and flag the mismatch.

---

### 5.5 Report Generator (see §8 for structure)

- Assembles `ComplianceReport` from DQ + RiskAnalysis + Reconciliation.
- Renders to **JSON** (machine) and **PDF** (branded, SEBI-shaped, shareable).
- Existing reuse: `internal/engines/report/` (json.go, markdown.go, renderer.go), `internal/service/report/`.

---

### 5.6 Audit Ledger (Pillar 5) — tamper-evident

Upgrade `internal/replay/audit_jsonl.go` from an append log to a **hash-chained ledger**:

- Each `AuditEvent` stores `Hash = sha256(Seq | At | Kind | PayloadHash | PrevHash)`.
- Chain is verifiable end-to-end (any tampering breaks the chain).
- The full chain is exportable as evidence and its root hash printed on the report.
- (Later) optionally anchor the root hash externally for stronger non-repudiation.

---

### 5.7 API & Delivery

**V0 REST endpoints:**
```
POST /v1/jobs                 # upload files, create a report job → job_id
GET  /v1/jobs/{id}            # status
GET  /v1/jobs/{id}/report     # JSON report
GET  /v1/jobs/{id}/report.pdf # rendered PDF
GET  /v1/jobs/{id}/audit      # audit chain
```
Auth: API key per tenant (V0) → OAuth (Phase 2). Jobs run async via the worker.

---

### 5.8 AI Layer — where it genuinely helps

| Use | Model tier | Notes |
|---|---|---|
| Messy-CSV column mapping | Claude Sonnet 5 | Structured output → validated deterministically |
| Report narration ("plain-English why") | Claude Sonnet 5 | Turns the gate ledger + attribution into readable prose |
| Cheap classification (format sniffing, tagging) | Claude Haiku 4.5 | High-volume, low-cost |
| (Phase 3) regime/vol-surface forecasting | ML/diffusion (not LLM) | Separate model track |

LLM outputs are always validated against the deterministic engine — **the engine is the source of truth, the LLM is the interface.**

---

## 6. How the Existing Engine Maps In

| Existing package | V0 role |
|---|---|
| `internal/quant/iv`, `pricing` | IV solve + Black-Scholes pricing |
| `internal/quant/risk` (greeks, scenario, payoff) | Pillar 3 risk analysis |
| `internal/quant/surface`, `regime` | Context for pricing + (later) regime features |
| `internal/replay/execsim.go` | Pillar 2 execution realism |
| `internal/replay/riskgate.go` | Gate pass/fail ledger (the "why") |
| `internal/replay/*` (runner, portfolio) | Backtest re-run for reconciliation |
| `internal/replay/audit_jsonl.go` | Pillar 5 audit ledger (→ hash-chained) |
| `internal/engines/report`, `service/report` | Report rendering |
| `internal/ingest`, `chain` | Basis for connectors |
| `internal/config` | Per-tenant/per-report config |

**~70% reuse. New work: connectors, DQ scoring, reconciliation matching, hash-chain, API, PDF, thin UI.**

---

## 7. Data Quality Scoring — Algorithm Detail

```
score = 100
for each finding:
    score -= weight(finding.severity)      # critical: -25, high: -12, medium: -5, low: -2
score = clamp(score, 0, 100)

band:  ≥85 Certifiable · 60–84 Usable-with-caveats · <60 Not-certifiable
```
Weights are configurable per report type. Every deduction carries **evidence** (row refs, timestamps, values) so the score is defensible, not a black box.

---

## 8. Compliance Report Structure (SEBI-shaped)

```
1. Header            — strategy name, underlying, period, report hash, DQ band
2. Methodology       — strategy description, assumptions, data sources
3. Performance       — returns, win rate, avg P&L, distribution
4. Risk Metrics      — max drawdown, VaR-style scenario losses, greeks profile
5. Scenario Matrix   — spot ±% × IV ±pts × time  → P&L grid
6. Risk-Gate Ledger  — each gate: pass/fail, computed value vs. limit, driver
7. Execution Realism — assumed vs. realistic P&L (slippage/fees impact)
8. Reconciliation    — (if live provided) backtest vs. live + gap attribution
9. Data Quality      — score + findings with evidence
10. Audit Trail      — hash-chain root + event summary
```
Sections 2–4 satisfy the SEBI "performance, risk metrics, methodology" strategy-report expectation; 6–9 are our differentiators.

---

## 9. Evolution: V0 → Infrastructure

| Aspect | V0 (Phase 1) | Risk API (Phase 2) | Infra OS (Phase 3) |
|---|---|---|---|
| Transport | REST, async jobs | gRPC, low-latency sync | High-throughput, metered |
| Compute | Batch worker | In-line risk checks | Order-flow embedded |
| Tenancy | API key | OAuth, per-broker isolation | Full multi-tenant + usage billing |
| Data | Uploaded files | Broker read APIs | Streaming positions |
| New modules | — | Real-time greeks/margin | Margin optimization, surveillance |

The **core engine is constant**; each phase changes transport, scale, and modules — not the math.

---

## 10. Non-Functionals

- **Security & data handling:** customer strategies/trades are sensitive IP → encryption at rest (object store + Postgres), per-tenant isolation, least-privilege, audited access. Critical for trust (an auditor that leaks is dead).
- **Determinism:** given the same inputs + config, the engine must produce identical output (report reproducibility is a compliance property). Pin versions; record engine version in the report.
- **Observability:** structured logs, per-job tracing, metrics on ingest success rate per connector (the connector library is the maintenance surface).
- **Scalability:** stateless workers scale horizontally; Postgres is the initial bottleneck — partition by tenant later.

---

## 11. Build Plan (V0, ~6 weeks)

| Week | Deliverable |
|---|---|
| 1 | Canonical data model + Connector interface; 1 backtest-format connector |
| 2 | 1 broker-tradebook connector + LLM-assisted mapping fallback |
| 3 | Normalization + Data Quality scoring (Pillar 1) |
| 4 | Wire existing engine → RiskAnalysis (Pillars 2–3); report assembly |
| 5 | Hash-chained audit ledger (Pillar 5) + PDF rendering + REST API |
| 6 | Reconciliation v1 (Pillar 4) + thin upload UI + polish → demoable V0 |

**Premium/Phase-1.5:** deepen reconciliation, add more connectors, report narration.

---

## 12. Open Technical Decisions

1. **First connectors** — which 2–3 formats have the most customer coverage? (Needs Phase-0 input.)
2. **Historical option-chain data** — do we require the customer to upload it, or source a licensed EOD dataset for pricing/scenario? (Affects DQ + reconciliation fidelity.)
3. **PDF stack** — HTML→headless render vs. native Go lib (branding vs. simplicity).
4. **Reconciliation matching threshold** — tune fuzzy-match scoring to minimize false pairs.
5. **Multi-tenancy timing** — build tenant isolation in V0, or retrofit? (Recommend: minimal tenant scoping from day 1.)

---

*This spec is build-ready for V0. The math is largely done in the existing engine; the new surface is ingestion, data-quality scoring, reconciliation matching, the hash-chained ledger, and the service/API shell. Start with the canonical model and one connector — everything else hangs off that.*
