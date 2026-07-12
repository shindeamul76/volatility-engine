# Vision & Roadmap — Options Risk & Vol Infrastructure for India

> **Status:** Draft v1 · Strategy & phased plan
> **One-liner:** We are building **AI-native options risk & vol infrastructure for India's derivatives market** — starting with the compliance wedge that SEBI's April-2026 algo framework forces on the ecosystem.

---

## 1. The Vision

Become the **risk & compliance engine embedded across India's options/derivatives ecosystem** — the neutral infrastructure that brokers, algo providers, and prop desks rely on to measure risk, stay compliant, and prove their strategies are real.

Think of it as: **the risk brain that sits inside other people's trading products**, not another analytics app that fights retail traders for subscriptions.

**Why this framing (and not a retail app):** the research is unambiguous — retail options tools cap small (QuantConnect: ~$1M revenue after 14 years; Marketfeed: ~$115K). The scaled winners are *infrastructure distributed through others' channels* (Vest: $50B+ in products via Cboe/First-Trust partnerships; Alpaca: embedded brokerage API). We build infrastructure, not a tool.

---

## 2. Why Now (the tailwinds)

- **India is the world's largest options market** — >84% of global equity-options contracts traded (Q1 2024).
- **Regulatory forcing function:** SEBI's Feb-2025 algo framework is **mandatory from April 1, 2026**. Every broker + thousands of algo providers must comply: order-level Algo-IDs, audit trails, risk checks, and — for black-box strategies — RA registration + a **maintained strategy report** (performance, risk metrics, methodology) per algo.
- **A market that just professionalized:** SEBI's Oct-2024 curbs cut retail F&O volumes ~35% and pushed out small punters. 91% of retail F&O traders lose money (₹1.05–1.06 lakh crore lost in FY25) — regulators and brokers are now *incentivized* to promote risk tooling.
- **AI wave + investor appetite:** YC is the most-active fintech investor (2025) and explicitly funds AI-native quant infrastructure (Sharpe, Bayesline). India options risk-infrastructure is **unclaimed**.

---

## 3. What We're Building

### The wedge (first product)
**Strategy Risk & Compliance Report generator.** An algo provider or strategy seller uploads their strategy + trades; our engine produces:
1. The **SEBI-shaped strategy report** (performance, Greeks, scenario stress, risk metrics, methodology).
2. A **tamper-evident, timestamped audit trail**.
3. *(premium)* **Backtest-vs-live reconciliation** with gap attribution (why live P&L diverged from the backtest).

File-upload first — no deep broker integration required to start.

### The infrastructure (what it grows into)
Real-time risk API embedded in brokers → margin optimization → surveillance → the **derivatives risk & compliance OS** for India.

### Our unfair advantage
We already have the hard part — a working quant engine: IV solving, vol-surface construction, Greeks, scenario/stress risk, risk-gates, backtest/replay with slippage & fees, and an audit trail. **~70% of the wedge MVP already exists in code.** Competitors would need a year to reach where we start.

**Moat = quant risk engine + India options focus + neutral/independent infrastructure + distribution.** AI is an accelerant layer, not the core magic.

---

## 4. Who We Sell To

| Segment | Why they buy | When (phase) |
|---|---|---|
| **Algo providers / strategy sellers** | Forced to maintain per-strategy compliance reports; want credibility vs. fake-backtest competitors | Phase 1 (wedge) |
| **Mid-tier brokers** (Dhan, 5paisa, Fyers, trading members) | Need risk/compliance infra, can't build it in-house | Phase 2 |
| **Prop desks / small funds** | Strategy validation, risk attribution | Phase 2–3 |
| **Large brokers / exchanges** | Embedded risk/margin at scale | Phase 3 |

**Note:** big brokers (Zerodha/Angel) build in-house — our buyers are the **mid-tier + long tail** who can't.

---

## 5. Business Model

- **Phase 1:** self-serve SaaS to algo providers (subscription per provider/strategy).
- **Phase 2:** B2B licensing to brokers (broker-funded, à la Sensibull — but risk is stickier than analytics).
- **Phase 3:** usage/order-based pricing embedded in order flow + multi-product (risk + margin + surveillance). **This is the shift that unlocks venture-scale.**

**Pricing lesson:** flat licenses cap small. Venture-scale requires *volume-based* pricing tied to the (huge) order flow + multiple modules expanding ACV.

---

## 6. Phased Roadmap

### Phase 0 — Validate (now, ~2–4 weeks) — *do this before building much*
- Talk to **3–5 algo providers**: "How do you produce the SEBI strategy report today? Would you pay to auto-generate it?"
- Talk to **3–5 mid-tier brokers**: "For April-2026, are you building compliance/risk in-house, buying, or waiting for the exchange?" — **this answer decides venture-scale vs. lifestyle business.**
- Build **V0** in parallel (~6 weeks) as the validation demo.
- **Gate:** ≥ several credible "yes, I'd use/pay" before committing hard.

### Phase 1 — Compliance Wedge (months 1–6)
- Ship the Strategy Risk & Compliance Report generator (file-upload).
- Land the first paying algo providers / strategy sellers.
- **AI role:** LLM to parse messy uploads (connectors); report narration.
- **Milestone:** 10–20 paying providers; validated willingness-to-pay.
- **Compliance:** obtain SEBI RA registration if we generate anything recommendation-shaped.

### Phase 2 — Risk API for Brokers (months 6–15)
- Turn the engine into a real-time risk API; embed in 1–3 mid-tier brokers.
- Add backtest-vs-live reconciliation as the credibility product.
- **AI role:** risk copilot (plain-English risk explanations), anomaly detection.
- **Milestone:** first broker deal(s); B2B2C distribution reaching real end-users.
- **Funding:** raise seed/Series A on wedge traction + broker LOIs.

### Phase 3 — Infrastructure OS (months 15–36)
- Expand modules: **margin optimization** (brokers care most), surveillance, compliance automation.
- Move to **usage/order-based pricing** across the ecosystem.
- **AI role:** ML/diffusion vol-surface & regime forecasting; agentic monitoring.
- **Milestone:** embedded across multiple brokers; path to ₹100Cr+ revenue.

### Phase 4 — The Vision (year 3+)
- Fully AI-native risk & vol infrastructure.
- Expand beyond India to other under-served emerging-market options venues.

---

## 7. Where AI Genuinely Fits (not buzzwords)

| Phase | Real AI use |
|---|---|
| 1 | LLM maps arbitrary broker/backtest CSV formats → canonical model; narrates reports |
| 2 | Risk copilot; anomaly/outlier detection on strategies |
| 3 | ML/generative (diffusion) vol-surface & regime forecasting; agentic risk monitoring |
| 4 | AI-native throughout |

Build AI where it earns its place — not as a label on everything.

---

## 8. Market Size & the Venture-Scale Path

- **Flat broker licenses alone:** ~₹15–30Cr TAM → *good business, not venture-scale.*
- **+ algo providers + prop desks:** ~₹30–50Cr.
- **Usage-based embedded infra + multi-product:** **₹100–300Cr revenue reachable** → credible venture outcome (₹1,000Cr+ valuation).

**Venture-scale is achievable but NOT the default.** It requires becoming *embedded* across brokers with *volume-based* pricing before an exchange or incumbent closes the door.

---

## 9. Competitive Landscape (from research)

- **No direct competitor** does independent, multi-source, options-aware risk audit + backtest-to-live reconciliation (globally or in India).
- Indian incumbents (AlgoTest, Stockmock, Quantsapp, Opstra, Tradetron, Sensibull) are **walled-garden backtesters** — they mark their own homework. They are our **input sources**, not rivals.
- Closest global analogs are walled-garden (QuantConnect reconciliation) or wrong asset class (FX track-record verifiers, institutional TCA).
- **Independence can't be insourced** — a backtester auditing its own backtests is a conflict of interest. That's our structural edge.

---

## 10. Key Risks & Assumptions to Validate

| Risk / assumption | Why it matters | How we test |
|---|---|---|
| **Channel:** will mid-tier brokers *buy* vs. build/wait-for-exchange? | Decides venture-scale vs. ₹15Cr business | Phase 0 broker interviews |
| **Willingness to pay** (algo providers) | Feature vs. business | Phase 0 provider interviews |
| **Exchange (NSE/BSE) builds it themselves** | Could shrink the wedge | Monitor; move fast; stay neutral/multi-broker |
| **Compliance report is self-maintained, not independently audited** | Weakens "mandate" → efficiency/credibility play, not forced-audit | Lean on efficiency + credibility value, not a mandate that doesn't exist |
| **Shrinking F&O base** (−19% participants YoY) | Volume-based pricing rides a declining base | Multi-product ACV; prop/institutional expansion |
| **India-consumer monetization is weak** | Affects fundability | Build infra/B2B, not retail; design to go global |

---

## 11. Immediate Next Steps

1. **Phase 0 interviews** — 3–5 algo providers + 3–5 mid-tier brokers (the channel answer gates everything).
2. **Build V0** — Strategy Risk & Compliance Report generator (~6 weeks, reuses existing engine).
3. **Decide** on validation results: green-light Phase 1, or reshape.

---

*This doc is the synthesis of market, regulatory, competitor, and funding research. It is deliberately honest about what is validated vs. assumed — the biggest open question is the broker channel, which Phase 0 exists to answer before we over-invest.*
