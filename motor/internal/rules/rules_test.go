package rules

import (
	"path/filepath"
	"testing"
)

func loadRealZones(t *testing.T) *ZoneRules {
	t.Helper()
	z, err := LoadZones(filepath.Join("..", "..", "knowledge", "zonas.yaml"))
	if err != nil {
		t.Fatalf("LoadZones: %v", err)
	}
	return z
}

func TestNormalize(t *testing.T) {
	cases := map[string]string{
		"Medellín":         "medellin",
		"  BOGOTÁ  D.C. ":  "bogota d c",
		"Vigía del Fuerte": "vigia del fuerte",
		"San Andrés":       "san andres",
	}
	for in, want := range cases {
		if got := Normalize(in); got != want {
			t.Errorf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDecideMedellinIsCOD(t *testing.T) {
	z := loadRealZones(t)
	d := z.Decide(Destination{Department: "Antioquia", City: "Medellín", Neighborhood: "Laureles"}, 149900)
	if d.Risk != RiskNormal || d.Policy != PayCOD || d.AdvanceAmount != 0 || d.CODAmount != 149900 {
		t.Fatalf("Medellín: %+v", d)
	}
	if !d.Matched || len(d.Carriers) == 0 {
		t.Fatalf("expected matched with carriers: %+v", d)
	}
}

func TestDecideBogotaVariants(t *testing.T) {
	z := loadRealZones(t)
	for _, dept := range []string{"Bogotá D.C.", "Bogota", "Cundinamarca"} {
		if d := z.Decide(Destination{Department: dept, City: "Bogotá"}, 89900); d.Policy != PayCOD {
			t.Errorf("%s: %+v", dept, d)
		}
	}
}

func TestDecideCityOverridesDepartment(t *testing.T) {
	z := loadRealZones(t)
	d := z.Decide(Destination{Department: "ANTIOQUIA", City: "Vigía del Fuerte"}, 89900)
	if d.Risk != RiskHard || d.Policy != PayPartial {
		t.Fatalf("Vigía del Fuerte: %+v", d)
	}
	// 50 % of 89.900 = 44.950 -> rounded up to 45.000
	if d.AdvanceAmount != 45000 || d.CODAmount != 44900 {
		t.Fatalf("advance: %+v", d)
	}
	// City looser than department: Quibdó inside Chocó.
	if q := z.Decide(Destination{Department: "Chocó", City: "Quibdó"}, 89900); q.Risk != RiskFar {
		t.Fatalf("Quibdó: %+v", q)
	}
}

func TestDecideRedZoneFullAdvance(t *testing.T) {
	z := loadRealZones(t)
	d := z.Decide(Destination{Department: "Amazonas", City: "Leticia"}, 149900)
	if d.Policy != PayFullAdvance || d.AdvanceAmount != 149900 || d.CODAmount != 0 {
		t.Fatalf("Amazonas: %+v", d)
	}
}

func TestUnknownDepartmentNeverDefaultsToCOD(t *testing.T) {
	z := loadRealZones(t)
	if d := z.Decide(Destination{Department: "Narnia", City: "Cair Paravel"}, 89900); d.Policy == PayCOD || d.Matched {
		t.Fatalf("unknown destination must not be COD: %+v", d)
	}
	if d := z.Decide(Destination{}, 89900); d.Policy == PayCOD {
		t.Fatalf("empty destination must not be COD: %+v", d)
	}
}

func TestDepartmentFuzzyContains(t *testing.T) {
	z := loadRealZones(t)
	if d := z.Decide(Destination{Department: "Dpto. de Antioquia", City: "Envigado"}, 89900); d.Policy != PayCOD {
		t.Fatalf("fuzzy department: %+v", d)
	}
	// "Norte de Santander" must not be read as "Santander" (both normal, but
	// the matched rule must be the longer one).
	d := z.Decide(Destination{Department: "Departamento Norte de Santander", City: "Cúcuta"}, 89900)
	if !d.Matched || d.Reason != "regla del departamento Departamento Norte de Santander" {
		t.Fatalf("norte de santander: %+v", d)
	}
}

func TestBarrioRuleWins(t *testing.T) {
	z := loadRealZones(t)
	if err := z.SetBarrio("Medellín", "Barrio Prueba", RiskRed); err != nil {
		t.Fatal(err)
	}
	d := z.Decide(Destination{Department: "Antioquia", City: "Medellin", Neighborhood: "barrio prueba"}, 89900)
	if d.Policy != PayFullAdvance {
		t.Fatalf("barrio rule: %+v", d)
	}
}

func TestParseZonesRejectsUnknownRisk(t *testing.T) {
	bad := []byte(`
politicas:
  normal: {pago: contraentrega}
  lejano: {pago: anticipo_parcial, anticipo_porcentaje: 0.2}
  dificil_acceso: {pago: anticipo_parcial, anticipo_porcentaje: 0.5}
  zona_roja: {pago: anticipado_total}
departamentos:
  antioquia: {riesgo: peligrosisimo}
`)
	if _, err := ParseZones(bad); err == nil {
		t.Fatal("expected error for unknown risk")
	}
}

func TestAdvanceMinimum(t *testing.T) {
	p := Policy{Pago: PayPartial, AnticipoPorcentaje: 0.2, AnticipoMinimo: 20000}
	if adv, cod := Advance(p, 89900); adv != 20000 || cod != 69900 { // 17.980 -> 18.000 < min
		t.Fatalf("got %d %d", adv, cod)
	}
	if adv, cod := Advance(p, 10000); adv != 10000 || cod != 0 {
		t.Fatalf("capped: got %d %d", adv, cod)
	}
	if adv, _ := Advance(Policy{Pago: PayPartial, AnticipoPorcentaje: 0.2}, 100000); adv != 20000 {
		t.Fatalf("exact thousand must not round up: %d", adv)
	}
}

func TestProfitProposalExample(t *testing.T) {
	// The worked example in docs/PROPUESTA.md section 7.
	r := Profit(ProfitInput{Price: 89900, UnitCost: 35000, Units: 1, Freight: 15000, ReturnRate: 0.25})
	if r.ProfitIfDelivered != 39900 || r.LossIfReturned != -30000 {
		t.Fatalf("win/loss: %+v", r)
	}
	if r.BreakEvenCPA != 22425 || !r.Viable {
		t.Fatalf("break-even: %+v", r)
	}
	if perOrder, perLead := MaxCPA(r.BreakEvenCPA, 0.30, 0.85); perOrder != 15698 || perLead != 13343 {
		t.Fatalf("MaxCPA: %d %d", perOrder, perLead)
	}
}

func TestProfitAdvanceReducesReturnLoss(t *testing.T) {
	r := Profit(ProfitInput{Price: 89900, UnitCost: 35000, Freight: 15000, ReturnRate: 0.4, AdvancePaid: 30000})
	if r.LossIfReturned != 0 {
		t.Fatalf("advance should cover return freight: %+v", r)
	}
}

func TestProfitWarnsWhenPriceTooLow(t *testing.T) {
	r := Profit(ProfitInput{Price: 40000, UnitCost: 35000, Freight: 15000, ReturnRate: 0.2})
	if r.Viable || r.Warning == "" {
		t.Fatalf("expected warning: %+v", r)
	}
}

func TestReturnRateFor(t *testing.T) {
	if got := ReturnRateFor(0.25, RiskNormal, PayCOD); got != 0.25 {
		t.Fatal(got)
	}
	if got := ReturnRateFor(0.25, RiskFar, PayPartial); got != 0.163 {
		t.Fatal(got)
	}
	if got := ReturnRateFor(0.25, RiskRed, PayFullAdvance); got != 0.02 {
		t.Fatal(got)
	}
}

func TestCOPtoUSD(t *testing.T) {
	if got := COPtoUSD(89900, 4000); got != 22.48 {
		t.Fatal(got)
	}
}
