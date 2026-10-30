package shipping

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/michiTrader/dropshipper/motor/internal/config"
	"github.com/michiTrader/dropshipper/motor/internal/dropi"
	"github.com/michiTrader/dropshipper/motor/internal/rules"
)

type fakeQ struct {
	quotes []dropi.Quote
	err    error
	calls  int
}

func (f *fakeQ) Quote(_ context.Context, _ dropi.QuoteInput) ([]dropi.Quote, error) {
	f.calls++
	return f.quotes, f.err
}

func svc(q *fakeQ, mut func(*config.Negocio)) *Service {
	n := config.DefaultNegocio()
	if mut != nil {
		mut(&n)
	}
	s := &Service{
		Negocio: config.NewLive(n),
		Origin:  func() (string, string) { return "Cundinamarca", "Bogotá" },
	}
	if q != nil {
		s.Client = func() Quoter { return q }
	}
	return s
}

var scn = Scenario{Department: "Antioquia", City: "Medellín", Price: 149900, UnitCost: 40000, Units: 2,
	EstimatedFreight: 18000, Risk: rules.RiskNormal, Policy: rules.PayCOD, ZoneCarriers: []string{"Coordinadora", "Envía"}}

func TestChoosesMostProfitableCarrier(t *testing.T) {
	q := &fakeQ{quotes: []dropi.Quote{{Carrier: "COORDINADORA", Freight: 16000}, {Carrier: "ENVIA", Freight: 12000}, {Carrier: "TCC", Freight: 13000}}}
	c := svc(q, nil).Compare(context.Background(), scn)
	if c.Source != SourceDropi || c.Best == nil || c.Best.Carrier != "Envía" || c.Best.Freight != 12000 {
		t.Fatalf("%+v", c)
	}
	if len(c.Options) != 3 || !c.Options[0].Best || c.Options[1].Best {
		t.Fatalf("opciones: %+v", c.Options)
	}
	if c.Options[0].ProfitIfDelivered != 149900-80000-12000 || c.Options[0].LossIfReturned >= 0 {
		t.Fatalf("economía: %+v", c.Options[0])
	}
}

func TestCarrierOwnReturnRateChangesTheWinner(t *testing.T) {
	q := &fakeQ{quotes: []dropi.Quote{{Carrier: "ENVIA", Freight: 12000}, {Carrier: "COORDINADORA", Freight: 13000}}}
	c := svc(q, func(n *config.Negocio) {
		for i := range n.Transportadoras {
			if n.Transportadoras[i].ID == "envia" {
				n.Transportadoras[i].TasaDevolucion = 0.6 // very bad delivery rate
			}
			if n.Transportadoras[i].ID == "coordinadora" {
				n.Transportadoras[i].TasaDevolucion = 0.05
			}
		}
	}).Compare(context.Background(), scn)
	if c.Best.Carrier != "Coordinadora" {
		t.Fatalf("debía ganar Coordinadora por devolver menos: %+v", c.Options)
	}
}

func TestInactiveCarrierIsNeverChosen(t *testing.T) {
	q := &fakeQ{quotes: []dropi.Quote{{Carrier: "TCC", Freight: 5000}, {Carrier: "COORDINADORA", Freight: 15000}}}
	c := svc(q, func(n *config.Negocio) {
		for i := range n.Transportadoras {
			if n.Transportadoras[i].ID == "tcc" {
				n.Transportadoras[i].Activa = false
			}
		}
	}).Compare(context.Background(), scn)
	if c.Best.Carrier != "Coordinadora" || len(c.Options) != 1 {
		t.Fatalf("%+v", c)
	}
}

func TestFallsBackToEstimateWhenDropiFails(t *testing.T) {
	q := &fakeQ{err: errors.New("timeout")}
	c := svc(q, nil).Compare(context.Background(), scn)
	if c.Source != SourceEstimate || c.Warning == "" || c.Best == nil || c.Best.Freight != 18000 || c.Best.Carrier != "Coordinadora" {
		t.Fatalf("%+v", c)
	}
	// No Dropi at all: same fallback, without a warning.
	c = svc(nil, nil).Compare(context.Background(), scn)
	if c.Source != SourceEstimate || c.Warning != "" || c.Best == nil {
		t.Fatalf("%+v", c)
	}
}

func TestUnknownCityOrOriginFallsBack(t *testing.T) {
	q := &fakeQ{quotes: []dropi.Quote{{Carrier: "TCC", Freight: 9000}}}
	s := svc(q, nil)
	bad := scn
	bad.City = "Narnia"
	if c := s.Compare(context.Background(), bad); c.Source != SourceEstimate || !strings.Contains(c.Warning, "Narnia") || q.calls != 0 {
		t.Fatalf("%+v calls=%d", c, q.calls)
	}
	s.Origin = func() (string, string) { return "", "" }
	if c := s.Compare(context.Background(), scn); c.Source != SourceEstimate || !strings.Contains(c.Warning, "origen") {
		t.Fatalf("%+v", c)
	}
}

func TestQuoteIsCached(t *testing.T) {
	q := &fakeQ{quotes: []dropi.Quote{{Carrier: "TCC", Freight: 9000}}}
	s := svc(q, nil)
	for i := 0; i < 3; i++ {
		s.Compare(context.Background(), scn)
	}
	if q.calls != 1 {
		t.Fatalf("la cotización debía cachearse: %d llamadas", q.calls)
	}
	s.Clear()
	s.Compare(context.Background(), scn)
	if q.calls != 2 {
		t.Fatalf("tras Clear debía volver a cotizar: %d", q.calls)
	}
}

func TestPromptSection(t *testing.T) {
	n := config.DefaultNegocio()
	for i := range n.Transportadoras {
		if n.Transportadoras[i].ID == "tcc" {
			n.Transportadoras[i].Notas = "Llega en 2 días a ciudades principales"
		}
	}
	p := PromptSection(n)
	if !strings.Contains(p, "# Transportadoras") || !strings.Contains(p, "TCC: Llega en 2 días") || strings.Contains(p, "Servientrega") {
		t.Fatalf("%s", p)
	}
	for _, id := range []string{"Veloces", "Envía", "Interrapidísimo", "Domina", "Coordinadora", "99minutos", "TCC", "Wiilog", "Jamv-drive", "Derocha-express"} {
		if !strings.Contains(p, id+":") {
			t.Errorf("falta %s en el prompt", id)
		}
	}
}
