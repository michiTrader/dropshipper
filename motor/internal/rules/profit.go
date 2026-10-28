package rules

import "math"

// ProfitInput describes one order (or one product offer) economically.
// All money is COP.
type ProfitInput struct {
	Price          int64   // what the customer pays (total of the offer)
	UnitCost       int64   // supplier cost per unit (Dropi)
	Units          int     // units in the offer
	Freight        int64   // outbound freight for the package
	ReturnFreight  int64   // freight charged when it comes back (0 = same as Freight)
	ReturnRate     float64 // expected return rate for this order (0..1)
	AdSpendPerSale int64   // ad cost attributed to this sale (0 if unknown)
	// AdvancePaid: money already collected up front. It is NOT lost on a
	// return, so it reduces the downside of a return.
	AdvancePaid int64
}

// ProfitResult is the expected economics of an order.
type ProfitResult struct {
	ProfitIfDelivered int64   `json:"ganancia_si_entrega"`
	LossIfReturned    int64   `json:"perdida_si_devuelve"`
	ExpectedProfit    int64   `json:"ganancia_esperada"`
	BreakEvenCPA      int64   `json:"cpa_equilibrio"`
	ReturnRate        float64 `json:"tasa_devolucion"`
	MarginPct         float64 `json:"margen_pct"`
	Viable            bool    `json:"viable"`
	Warning           string  `json:"advertencia,omitempty"`
}

// Profit computes expected profit with returns priced in:
//
//	E = (1-d)·(P − C·u − F) − d·(F + Fr − A) − CPA
//
// where A (advance already paid) offsets what a return costs. BreakEvenCPA is
// E before ads: the most you can pay Meta per dispatched order and not lose.
func Profit(in ProfitInput) ProfitResult {
	units := in.Units
	if units <= 0 {
		units = 1
	}
	ret := in.ReturnFreight
	if ret == 0 {
		ret = in.Freight
	}
	d := clamp01(in.ReturnRate)
	win := in.Price - in.UnitCost*int64(units) - in.Freight
	loss := in.Freight + ret - in.AdvancePaid
	if loss < 0 {
		loss = 0
	}
	beforeAds := roundMoney((1-d)*float64(win) - d*float64(loss))
	r := ProfitResult{
		ProfitIfDelivered: win,
		LossIfReturned:    -loss,
		BreakEvenCPA:      beforeAds,
		ExpectedProfit:    beforeAds - in.AdSpendPerSale,
		ReturnRate:        d,
	}
	if in.Price > 0 {
		r.MarginPct = math.Round(float64(win)/float64(in.Price)*1000) / 10
	}
	r.Viable = r.ExpectedProfit > 0
	switch {
	case win <= 0:
		r.Warning = "el precio no cubre costo + flete: se pierde dinero incluso si se entrega"
	case !r.Viable:
		r.Warning = "con esta tasa de devolución y este gasto en anuncios la venta no es rentable"
	}
	return r
}

// ReturnRateFor adjusts the base return rate by destination risk. An advance
// strongly reduces returns (the customer has skin in the game).
func ReturnRateFor(base float64, risk, policy string) float64 {
	r := base
	switch risk {
	case RiskFar:
		r *= 1.3
	case RiskHard:
		r *= 1.6
	case RiskRed:
		r *= 2
	}
	switch policy {
	case PayPartial:
		r *= 0.5
	case PayFullAdvance:
		r = 0.02 // only damaged/lost parcels
	}
	return clamp01(math.Round(r*1000) / 1000)
}

// MaxCPA is the target cost per dispatched order once a safety margin is kept,
// and the cost per lead conversation given the confirmation rate.
func MaxCPA(breakEven int64, safety, confirmRate float64) (perOrder, perLead int64) {
	perOrder = roundMoney(float64(breakEven) * (1 - clamp01(safety)))
	perLead = roundMoney(float64(perOrder) * clamp01(confirmRate))
	return
}

// COPtoUSD converts with the configured TRM.
func COPtoUSD(cop int64, trm float64) float64 {
	if trm <= 0 {
		return 0
	}
	return math.Round(float64(cop)/trm*100) / 100
}

// roundMoney rounds half-up, absorbing float error (22425*0.7 is
// 15697.499999… in binary floating point but is 15697.5 in pesos).
func roundMoney(f float64) int64 {
	if f < 0 {
		return -int64(math.Round(-f + 1e-6))
	}
	return int64(math.Round(f + 1e-6))
}

func clamp01(f float64) float64 {
	switch {
	case f < 0:
		return 0
	case f > 1:
		return 1
	}
	return f
}
