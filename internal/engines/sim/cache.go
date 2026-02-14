package sim

import (
	"math"
	"sync"
	"volatility-engine/internal/domain/market"
	"volatility-engine/internal/quant/pricing"
)

// PricerCacheKey keys the pricing result.
// Quantized to reduce cardinality.
type PricerCacheKey struct {
	Type  market.OptionType
	K     float64
	T     int64 // Quantized time (e.g. 1e-5 precision)
	S     int64 // Quantized spot (e.g. 0.01 precision)
	Sigma int64 // Quantized vol (e.g. 0.0005 precision)
}

// ResultCache stores pricing results.
type ResultCache struct {
	mu    sync.RWMutex
	store map[PricerCacheKey]pricing.Result
}

func NewResultCache() *ResultCache {
	return &ResultCache{
		store: make(map[PricerCacheKey]pricing.Result),
	}
}

func (c *ResultCache) Get(ctx pricing.PricingContext) (pricing.Result, bool) {
	key := makeKey(ctx)
	c.mu.RLock()
	res, ok := c.store[key]
	c.mu.RUnlock()
	return res, ok
}

func (c *ResultCache) Put(ctx pricing.PricingContext, res pricing.Result) {
	key := makeKey(ctx)
	c.mu.Lock()
	c.store[key] = res
	c.mu.Unlock()
}

func makeKey(ctx pricing.PricingContext) PricerCacheKey {
	// Quantization Constants
	const (
		precT     = 100000.0 // 1e-5
		precS     = 20.0     // 0.05
		precSigma = 2000.0   // 0.0005
	)

	return PricerCacheKey{
		Type:  ctx.Type,
		K:     ctx.K,
		T:     int64(math.Round(ctx.T * precT)),
		S:     int64(math.Round(ctx.S * precS)),
		Sigma: int64(math.Round(ctx.Sigma * precSigma)),
	}
}

// CachedPricer wraps the standard calculate and adds caching.
type CachedPricer struct {
	Cache *ResultCache
}

func (p CachedPricer) Price(ctx pricing.PricingContext) pricing.Result {
	if res, ok := p.Cache.Get(ctx); ok {
		return res
	}
	res := pricing.Calculate(ctx)
	p.Cache.Put(ctx, res)
	return res
}
