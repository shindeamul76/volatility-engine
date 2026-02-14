package risk

import (
	"math"
	"testing"
)

func TestBSM_Call(t *testing.T) {
	// S=100, K=100, T=1, r=0.05, v=0.20
	// Call Price ~ 10.45
	res := BSM(true, 100, 100, 1.0, 0.05, 0.20)

	expected := 10.45
	if math.Abs(res.Price-expected) > 0.01 {
		t.Errorf("BSM Call Price mismatch. Got %.4f, Expected ~%.4f", res.Price, expected)
	}

	// Delta ~ 0.6368
	if math.Abs(res.Delta-0.6368) > 0.01 {
		t.Errorf("BSM Delta mismatch. Got %.4f", res.Delta)
	}
}

func TestBSM_Put(t *testing.T) {
	// S=100, K=100, T=1, r=0.05, v=0.20
	// Put Price = Call - S + K*exp(-rT)
	// 10.45 - 100 + 100*0.9512 = 10.45 - 100 + 95.12 = 5.57

	res := BSM(false, 100, 100, 1.0, 0.05, 0.20)

	expected := 5.57
	if math.Abs(res.Price-expected) > 0.01 {
		t.Errorf("BSM Put Price mismatch. Got %.4f, Expected ~%.4f", res.Price, expected)
	}

	// Put Delta = Call Delta - 1 ~ -0.3632
	if math.Abs(res.Delta-(-0.3632)) > 0.01 {
		t.Errorf("BSM Put Delta mismatch. Got %.4f", res.Delta)
	}
}

func TestVolFromSkew(t *testing.T) {
	// Params are now Total Variance coeffs: A + Bx + Cx^2
	// We want to test Vol = sqrt((A + Bx + Cx^2) / T)

	// Case 1: Flat Vol 20%, T=1.0
	// Var = 0.20^2 * 1 = 0.04
	params := []float64{0.04, 0.0, 0.0}

	fwd := 100.0
	// ATM: x=0 -> Var=0.04 -> Vol=sqrt(0.04/1) = 0.20
	vATM := VolFromSkew(params, 100.0, fwd, 1.0)
	if math.Abs(vATM-0.20) > 0.0001 {
		t.Errorf("ATM Vol mismatch. Got %f", vATM)
	}

	// Case 2: T=0.25. Var=0.04.
	// Vol = sqrt(0.04/0.25) = sqrt(0.16) = 0.40
	vShort := VolFromSkew(params, 100.0, fwd, 0.25)
	if math.Abs(vShort-0.40) > 0.0001 {
		t.Errorf("Short Term Vol mismatch. Got %f", vShort)
	}

	// Case 3: Slope
	// Var(x) = 0.04 - 0.02*x. T=1.
	// x = ln(110/100) ~ 0.0953
	// Var = 0.04 - 0.02*0.0953 = 0.04 - 0.001906 = 0.038094
	// Vol = sqrt(0.038094) ~ 0.19517
	paramsSlope := []float64{0.04, -0.02, 0.0}
	vOTM := VolFromSkew(paramsSlope, 110.0, fwd, 1.0)
	if math.Abs(vOTM-0.19517) > 0.001 {
		t.Errorf("OTM Vol mismatch. Got %f", vOTM)
	}
}
