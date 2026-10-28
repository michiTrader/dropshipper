package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/michiTrader/dropshipper/motor/internal/catalog"
	"github.com/michiTrader/dropshipper/motor/internal/convo"
	"github.com/michiTrader/dropshipper/motor/internal/rules"
	"github.com/michiTrader/dropshipper/motor/internal/store"
)

func (s *Server) productRoutes(api *http.ServeMux) {
	api.HandleFunc("GET /api/productos", s.listProducts)
	api.HandleFunc("POST /api/productos", s.createProduct)
	api.HandleFunc("POST /api/productos/calcular", s.calcProduct)
	api.HandleFunc("GET /api/productos/{sku}", s.getProduct)
	api.HandleFunc("PUT /api/productos/{sku}", s.updateProduct)
	api.HandleFunc("DELETE /api/productos/{sku}", s.deleteProduct)
	api.HandleFunc("POST /api/productos/{sku}/activar", s.activateProduct)
	api.HandleFunc("GET /api/guia-ventas", s.getPlaybook)
	api.HandleFunc("PUT /api/guia-ventas", s.putPlaybook)
}

// offerEco is the economics of one offer under the current assumptions.
type offerEco struct {
	catalog.Offer
	COD        rules.ProfitResult `json:"contraentrega"`
	Partial    rules.ProfitResult `json:"anticipo_parcial"`
	Advance    int64              `json:"anticipo"`
	TargetCPA  int64              `json:"cpa_objetivo"`
	TargetLead int64              `json:"costo_por_chat_objetivo"`
	PriceUSD   float64            `json:"precio_usd"`
	PerUnit    int64              `json:"precio_por_unidad"`
}

func (s *Server) economics(p catalog.Product) []offerEco {
	f := s.Settings.Get().Negocio.Finanzas
	out := []offerEco{}
	for _, o := range p.Offers {
		units := o.Units
		if units <= 0 {
			units = 1
		}
		cod := rules.Profit(rules.ProfitInput{Price: o.Price, UnitCost: p.UnitCost, Units: units, Freight: p.Freight, ReturnRate: f.TasaDevolucion})
		adv, _ := rules.Advance(s.Zones.PolicyFor(rules.RiskFar), o.Price)
		partial := rules.Profit(rules.ProfitInput{Price: o.Price, UnitCost: p.UnitCost, Units: units, Freight: p.Freight,
			ReturnRate: rules.ReturnRateFor(f.TasaDevolucion, rules.RiskFar, rules.PayPartial), AdvancePaid: adv})
		perOrder, perLead := rules.MaxCPA(cod.BreakEvenCPA, f.MargenSeguridad, f.TasaConfirmacion)
		out = append(out, offerEco{Offer: o, COD: cod, Partial: partial, Advance: adv, TargetCPA: perOrder, TargetLead: perLead,
			PriceUSD: rules.COPtoUSD(o.Price, f.TRM), PerUnit: o.Price / int64(units)})
	}
	return out
}

type productSummary struct {
	SKU           string `json:"sku"`
	Name          string `json:"nombre"`
	Category      string `json:"categoria"`
	Active        bool   `json:"activo"`
	CostConfirmed bool   `json:"costo_confirmado"`
	Offers        int    `json:"ofertas"`
	PriceFrom     int64  `json:"precio_desde"`
	BestProfit    int64  `json:"ganancia_esperada_mejor"`
	Viable        bool   `json:"viable"`
	Orders        int    `json:"pedidos"`
}

func (s *Server) listProducts(w http.ResponseWriter, r *http.Request) {
	counts := map[string]int{}
	if os, err := s.Store.ListOrders(r.Context()); err == nil {
		for _, o := range os {
			counts[catalog.NormalizeSKU(o.ProductSKU)]++
		}
	}
	out := []productSummary{}
	for _, p := range s.Catalog.List() {
		sm := productSummary{SKU: p.SKU, Name: p.Name, Category: p.Category, Active: p.Active, CostConfirmed: p.CostConfirmed,
			Offers: len(p.Offers), Orders: counts[catalog.NormalizeSKU(p.SKU)]}
		for i, e := range s.economics(p) {
			if i == 0 || e.Price < sm.PriceFrom {
				sm.PriceFrom = e.Price
			}
			if i == 0 || e.COD.ExpectedProfit > sm.BestProfit {
				sm.BestProfit = e.COD.ExpectedProfit
			}
			if e.COD.Viable {
				sm.Viable = true
			}
		}
		out = append(out, sm)
	}
	writeJSON(w, 200, out)
}

// realFinance sums what actually happened with this product's orders.
type realFinance struct {
	Orders      int   `json:"pedidos"`
	Open        int   `json:"abiertos"`
	Approved    int   `json:"aprobados"`
	Delivered   int   `json:"entregados"`
	Returned    int   `json:"devueltos"`
	Cancelled   int   `json:"cancelados"`
	SalesTotal  int64 `json:"ventas"`
	AdvanceHeld int64 `json:"anticipos_cobrados"`
}

func (s *Server) getProduct(w http.ResponseWriter, r *http.Request) {
	p, ok := s.Catalog.Get(r.PathValue("sku"))
	if !ok {
		writeErr(w, store.ErrNotFound)
		return
	}
	var fin realFinance
	orders := []store.Order{}
	if os, err := s.Store.ListOrders(r.Context()); err == nil {
		for _, o := range os {
			if catalog.NormalizeSKU(o.ProductSKU) != catalog.NormalizeSKU(p.SKU) {
				continue
			}
			if len(orders) < 15 {
				orders = append(orders, o)
			}
			fin.Orders++
			switch o.Status {
			case store.OrderDraft, store.OrderAwaitPayment, store.OrderPendingApprove:
				fin.Open++
			case store.OrderRejected, store.OrderCancelled:
				fin.Cancelled++
			case store.OrderReturned:
				fin.Returned++
			case store.OrderDelivered:
				fin.Delivered++
				fin.SalesTotal += o.Total
				fin.Approved++
			default:
				fin.Approved++
				fin.SalesTotal += o.Total
			}
			if o.PaymentStatus == store.PayConfirmed {
				fin.AdvanceHeld += o.AdvanceAmount
			}
		}
	}
	writeJSON(w, 200, map[string]any{
		"producto": p, "economia": s.economics(p),
		"finanzas": map[string]any{"supuestos": s.Settings.Get().Negocio.Finanzas, "real": fin},
		"pedidos":  orders,
	})
}

// cleanProduct trims text and drops empty list entries and empty offers.
func cleanProduct(p *catalog.Product) {
	p.SKU = catalog.NormalizeSKU(p.SKU)
	p.Name = strings.TrimSpace(p.Name)
	clean := func(in []string) []string {
		out := []string{}
		for _, x := range in {
			if x = strings.TrimSpace(x); x != "" {
				out = append(out, x)
			}
		}
		return out
	}
	p.Ingredients, p.Precautions, p.Pains, p.Benefits = clean(p.Ingredients), clean(p.Precautions), clean(p.Pains), clean(p.Benefits)
	offers := []catalog.Offer{}
	for _, o := range p.Offers {
		o.ID, o.Label = strings.TrimSpace(o.ID), strings.TrimSpace(o.Label)
		if o.ID == "" && o.Label == "" && o.Price == 0 {
			continue
		}
		if o.Label == "" {
			o.Label = o.ID
		}
		offers = append(offers, o)
	}
	p.Offers = offers
	objs := []catalog.Objection{}
	for _, o := range p.Objections {
		if strings.TrimSpace(o.Objection) != "" && strings.TrimSpace(o.Answer) != "" {
			objs = append(objs, o)
		}
	}
	p.Objections = objs
}

func (s *Server) createProduct(w http.ResponseWriter, r *http.Request) {
	var p catalog.Product
	if err := readJSON(r, &p); err != nil {
		writeErr(w, errors.New("los datos del producto no son válidos"))
		return
	}
	cleanProduct(&p)
	if _, exists := s.Catalog.Get(p.SKU); exists {
		writeErr(w, errors.New("ya existe un producto con ese código (SKU); elige otro"))
		return
	}
	saved, err := s.Catalog.Save(r.Context(), p)
	if err != nil {
		writeErr(w, err)
		return
	}
	s.Engine.Events.Publish(convo.Event{Type: "producto"})
	writeJSON(w, 200, map[string]any{"ok": true, "sku": saved.SKU})
}

func (s *Server) updateProduct(w http.ResponseWriter, r *http.Request) {
	sku := r.PathValue("sku")
	old, ok := s.Catalog.Get(sku)
	if !ok {
		writeErr(w, store.ErrNotFound)
		return
	}
	var p catalog.Product
	if err := readJSON(r, &p); err != nil {
		writeErr(w, errors.New("los datos del producto no son válidos"))
		return
	}
	cleanProduct(&p)
	p.SKU = old.SKU // the code identifies the product; it cannot change
	p.Active = old.Active
	if _, err := s.Catalog.Save(r.Context(), p); err != nil {
		writeErr(w, err)
		return
	}
	s.Engine.Events.Publish(convo.Event{Type: "producto"})
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) deleteProduct(w http.ResponseWriter, r *http.Request) {
	if err := s.Catalog.Delete(r.Context(), r.PathValue("sku")); err != nil {
		writeErr(w, err)
		return
	}
	s.Engine.Events.Publish(convo.Event{Type: "producto"})
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) activateProduct(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Active bool `json:"activo"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, err)
		return
	}
	if err := s.Catalog.SetActive(r.Context(), r.PathValue("sku"), in.Active); err != nil {
		writeErr(w, err)
		return
	}
	s.Engine.Events.Publish(convo.Event{Type: "producto"})
	writeJSON(w, 200, map[string]any{"ok": true, "activo": in.Active})
}

// calcProduct previews the economics of an unsaved product while typing.
func (s *Server) calcProduct(w http.ResponseWriter, r *http.Request) {
	var p catalog.Product
	if err := readJSON(r, &p); err != nil {
		writeErr(w, errors.New("los datos del producto no son válidos"))
		return
	}
	offers := []catalog.Offer{}
	for _, o := range p.Offers {
		if o.Price > 0 && o.Units > 0 {
			offers = append(offers, o)
		}
	}
	p.Offers = offers
	writeJSON(w, 200, map[string]any{"economia": s.economics(p), "supuestos": s.Settings.Get().Negocio.Finanzas})
}

func (s *Server) getPlaybook(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"texto": s.Catalog.Playbook()})
}

func (s *Server) putPlaybook(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Text string `json:"texto"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, err)
		return
	}
	if err := s.Catalog.SetPlaybook(r.Context(), in.Text); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}
