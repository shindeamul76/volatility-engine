package main

import (
	"fmt"
	"math"
	"os"

	"volatility-engine/internal/domain/market"
	"volatility-engine/internal/quant/pricing"
)

func main() {
	f, _ := os.Create("debug_result.txt")
	defer f.Close()

	fmt.Println("Running Debug Pricing... writing to debug_result.txt")

	ctx := pricing.PricingContext{
		Type:           market.Call,
		S:              100,
		K:              100,
		T:              1.0,
		R:              0.05,
		Q:              0.0,
		Sigma:          0.2,
		IsForwardModel: false,
	}

	res := pricing.Calculate(ctx)

	fmt.Fprintf(f, "Price: %.4f (Expected: 10.4506)\n", res.Price)
	fmt.Fprintf(f, "Delta: %.4f (Expected: 0.6368)\n", res.Greeks.Delta)
	fmt.Fprintf(f, "Vega:  %.4f (Expected: 0.3752)\n", res.Greeks.Vega)
	fmt.Fprintf(f, "Theta: %.4f (Expected: -0.0175)\n", res.Greeks.Theta)

	d1Test := (math.Log(100/100) + (0.05+0.5*0.2*0.2)*1.0) / (0.2 * 1.0)
	fmt.Fprintf(f, "Manual d1: %.4f\n", d1Test)
}
