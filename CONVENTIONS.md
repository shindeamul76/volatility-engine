# Pricing & Risk Conventions

## Time Units
- **Internal**: T in years (e.g., 30 days = 30/365.0)
- **Output**: 
  - `theta_per_day` = θ_year / 365
  - Daycount: ACT/365 (always 365, not 252 trading days)

## Volatility Units
- **Internal**: σ as decimal (0.20 = 20% annualized vol)
- **Vega**: 
  - Raw vega = ∂Price/∂σ (per +1.00 vol change)
  - Published: `VegaPerVolPoint` = vega / 100 (per +0.01 vol)
  - **Definition**: 1 vol point = 0.01 absolute (e.g., 0.20 → 0.21 is +1 vol point)

## Vol Shocks (Scenarios)
- **Units**: Absolute σ change (e.g., -0.10, -0.05, 0, +0.05, +0.10)
- **Application**: `finalVol = baseVol + shock`
- **Example**: If base σ = 0.20, shock = -0.05 → final σ = 0.15 (moved -5 vol points)
- **Field name**: `vol_shocks_abs` in config and code
- **Safety**: `finalVol = max(finalVol, vol_floor)` where `vol_floor = 1e-6` to avoid negative/zero vol

## Rates & Dividends
- **Spot-based BSM**: Assumes q = 0 (no dividends)
  - d1 formula: `(ln(S/K) + (r + 0.5σ²)T) / (σ√T)`
- **Forward-based BSM**: Uses F (forward) and DF (discount factor) directly
  - d1 formula: `(ln(F/K) + 0.5σ²T) / (σ√T)`
  - **Important**: If using forward-based pricing, do not also apply q/r drift in spot formula; use one approach consistently

## Greeks Sign Conventions

### For Individual Options (Long 1 Contract)
- **Delta**: 
  - Call: +1.0 (deep ITM), 0.0 (deep OTM)
  - Put: -1.0 (deep ITM), 0.0 (deep OTM)
- **Gamma**: Positive for long options (always > 0 under standard BSM)
- **Vega**: Positive for long options (always > 0)
- **Theta**: Typically negative for long options (time decay)
- **Rho**: Positive for calls, negative for puts

### For Position Greeks (BUY vs SELL)
- **Position greeks flip sign for SELL legs**
  - Long call: vega > 0, short call: vega < 0
  - Long option: gamma > 0, short option: gamma < 0
  - Long option: theta usually < 0, short option: theta usually > 0

## Strategy-Level Aggregation
- **Net Greeks**: `Σ(side_sign × qty × greek)`
  - `side_sign` = +1 for BUY, -1 for SELL
  - Example: Selling 1 call with Δ=0.6 → Position Δ = -1 × 1 × 0.6 = -0.6

## Testing Tolerances
- **Price**: abs(error) < 1e-6 or rel(error) < 1e-6
- **Delta**: abs(error) < 1e-4
- **Gamma**: abs(error) < 1e-3 (looser, sensitive to numerics)
- **Vega**: abs(error) < 1e-3
- **Theta**: abs(error) < 5e-3 (looser near expiry)

