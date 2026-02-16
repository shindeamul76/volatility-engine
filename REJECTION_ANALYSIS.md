# Option Chain Rejection Analysis

## Current Rejection Statistics
- **Total Quotes**: 1,132
- **Tradable**: 168 (15%)
- **Rejected**: 964  (85%)

## Why Options Are Being Rejected

### Rejection Criteria (from `ingest/service.go`)

Your options are being filtered through a **multi-stage quality pipeline**:

#### **Stage 1: Price Sanity Checks**
```go
Flag: "BAD_PRICE" → Bid or Ask <= 0
Flag: "CROSSED_MARKET" → Ask < Bid
Flag: "BAD_MID_PRICE" → Mid <= 0
```

#### **Stage 2: Spread Checks (Adaptive Thresholds)**
Your system uses **dynamic thresholds** based on premium level:

**Normal Premium (Mid >= Low Premium Threshold)**:
- `SpreadPctNormal`: ~12% (0.12)
- `SpreadAbsNormal`: (absolute rupee spread limit)

**Low Premium (Mid < Threshold)**:
- `SpreadPctLowPremium`: Stricter percentage
- `SpreadAbsLowPremium`: Stricter absolute

```go
Flag: "WIDE_SPREAD" → (Ask - Bid) / Mid > MaxSpreadPct
Flag: "WIDE_SPREAD_ABS" → (Ask - Bid) > MaxSpreadAbs
Flag: "LOW_PREMIUM" → Info flag for low premium options
```

#### **Stage 3: Liquidity Checks**
```go
Flag: "ILLIQUID_STRATEGY" → OI < OI_Strategy_Min AND Volume < Vol_Strategy_Min
Flag: "ILLIQUID" → OI < OI_Severe_Min AND Volume < Vol_Severe_Min
```

Current defaults from processor:
- `MinVolume`: 100
- `MinOpenInterest`: 1,000

#### **Stage 4: Data Quality**
```go
Flag: "LTP_FALLBACK" → Missing bid/ask, using Last Traded Price
```

### Usability Tiers

Your system has **3 levels** of usability:

1. **IsUsableForAnalytics** (Lenient):
   - Just needs valid price structure
   - Used for: General analysis

2. **IsUsableForIV** (Moderate):
   - Valid mid price from bid/ask
   - No WIDE_SPREAD
   - No ILLIQUID
   - No LTP_FALLBACK
   - Used for: IV solving, skew fitting

3. **IsTradable** (Strict):
   - All IV requirements
   - Also no ILLIQUID_STRATEGY
   - Used for: Strategy construction

## Sample Rejection from Your Log

```
Line 17: NIFTY 23100.00 CALL: Mid=2384.75
Flags=[WIDE_SPREAD WIDE_SPREAD_ABS ILLIQUID_STRATEGY ILLIQUID]
```

This option failed on **ALL** fronts:
- Spread too wide (both % and absolute)
- Not enough volume
- Not enough open interest

## Skipped Expiries Analysis

### 2026-04-28 & 2026-03-30: 0 Points (Skew Skipped)

**Why they were skipped**:
1. **After ingestion**: Had quotes but likely far OTM
2. **After quality filtering**: All options flagged as unusable
3. **After log-moneyness window**: Filtered to 0 points

**The cascade**:
```
Raw Quotes (218 for Mar-30, 186 for Apr-28)
    ↓ (Quality Filter)
IsUsableForIV filter applied
    ↓ (Moneyness Filter)
log_moneyness_window: 0.10 (config line 14)
Only keeps strikes where |ln(K/F)| <= 0.10
    ↓ (Point Count Filter)
min_points_per_expiry: 20 (config line 15)
    ↓
0 points remaining → Skew skipped
```

## Is 85% Rejection Rate Normal?

### ✅ **YES**, if:
- You're trading NIFTY weekly options with strict quality requirements
- Most strikes are deep OTM with LOW_PREMIUM
- Longer-dated expiries have WIDE_SPREAD_ABS (absolute spread kills them)
- You require both volume AND open interest minimums

### ❌ **NO**, if:
- You're rejecting ATM/near-ATM liquid strikes
- Rejection reasons show "WIDE_SPREAD" on strikes like 25400-25600 (near spot 25471)

## Recommended Diagnostics

### 1. **Get Rejection Histogram**
The log already shows sample flags. To get full breakdown, look at:
- How many rejected due to WIDE_SPREAD only?
- How many rejected due to ILLIQUID only?
- How many rejected due to multiple reasons?

### 2. **Check Near-ATM Specifically**
For spot = 25471, check strikes 25400-25550:
- Are these showing as tradable?
- If not, which flag is killing them?

### 3. **Review Configuration**
Check `ValidationConfig` defaults in `service.go`:
```go
// Current thresholds (likely values)
MaxSpreadPct: 0.12 (12%)
MinVolume: 100
MinOpenInterest: 1000
LowPremiumThreshold: ~20
```

### 4. **For Skipped Expiries**
Check if Apr-28 and Mar-30 CSV files have:
- Bid/Ask data or just LTP?
- Reasonable OI/Volume?
- Strikes near the forward?

## Tuning Recommendations

### If rejection rate is too high:

**For wider bid-ask tolerance**:
```go
// In ValidationConfig
SpreadPctNormal: 0.15 // Was 0.12, now 15%
SpreadAbsLowPremium: 5.0 // Allow ₹5 spread for low premium
```

**For liquidity**:
```go
OI_Strategy_Min: 500 // Down from 1000
Vol_Strategy_Min: 50 // Down from 100
```

**For longer expiries**:
- Increase `log_moneyness_window` from 0.10 to 0.15
- Decrease `min_points_per_expiry` from 20 to 10

### If you want to see what's being rejected:
Add logging in `updateQualitySummary`:
```go
if !q.Quality.IsTradable && q.Contract.Strike >= 25400 && q.Contract.Strike <= 25550 {
    log.Printf("Rejected ATM strike %.0f %s: Flags=%v, Spread=%.2f%%, OI=%d",
        q.Contract.Strike, q.Contract.Type, q.Quality.Flags, 
        q.Derived.SpreadPct*100, q.Market.OpenInterest)
}
```

## Bottom Line

Your **85% rejection is by design** - the system is being very strict to ensure:
1. Only tradable strikes with tight spreads
2. Adequate liquidity for actual execution
3. Clean IV data for skew fitting

This is **appropriate for production** where you want to avoid:
- Slippage from wide spreads
- Execution risk from low liquidity
- IV noise from unreliable quotes

But it means:
- Fewer strategy candidates
- Some expiries might be skipped entirely (Mar-30, Apr-28)
- Your "tradable universe" is narrow but high-quality
