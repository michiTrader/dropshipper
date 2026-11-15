package dropi

import (
	"context"
	"strings"
	"testing"
)

func nullable2(t string) map[string]any { return map[string]any{"type": []any{"null", t}} }

// dropiReal mimics Dropi's real MCP: the real quote_shipping / search_cities /
// create_order schemas the owner pasted, and answers in TOON text.
func dropiReal(f *fakeDropi) {
	f.tools = []map[string]any{
		{"name": "quote_shipping", "description": "Quote shipping cost for a Dropi order.", "inputSchema": schema(map[string]any{
			"cash_on_delivery": map[string]any{"type": "boolean", "description": "cash on delivery"},
			"destination_city": schema(map[string]any{
				"dane_code": map[string]any{"type": "string"},
				"id":        map[string]any{"type": "integer"},
				"zip_code":  nullable2("string"),
			}, "id", "zip_code"),
			"direction":    nullable2("string"),
			"neighborhood": nullable2("string"),
			"zip_code":     nullable2("string"),
			"products": map[string]any{"type": "array", "items": schema(map[string]any{
				"id":           map[string]any{"type": "integer"},
				"price":        map[string]any{"type": "number"},
				"quantity":     map[string]any{"type": "integer"},
				"type":         map[string]any{"type": "string"},
				"variation_id": nullable2("integer"),
			}, "id", "price", "quantity", "type")},
		}, "cash_on_delivery", "destination_city", "products")},
		{"name": "search_cities", "description": "Search Dropi cities by name", "inputSchema": schema(map[string]any{
			"name": map[string]any{"type": "string"},
		}, "name")},
		{"name": "list_departments", "description": "List of departments (regions) of your country", "inputSchema": schema(map[string]any{})},
		{"name": "list_products", "description": "List products available in Dropi", "inputSchema": schema(map[string]any{
			"search": map[string]any{"type": "string"},
		})},
		{"name": "create_order", "description": "Create a sale order in Dropi", "inputSchema": schema(map[string]any{
			"name": strField("first name"), "surname": strField("last name"), "phone": strField("phone"),
			"dir": strField("address"), "state": strField("department"), "city": strField("city"),
			"rate_type":    strField("rate type"),
			"client_email": strField("email"),
			"notes":        strField("notes"),
			"products": map[string]any{"type": "array", "items": schema(map[string]any{
				"id":                   map[string]any{"type": "integer"},
				"price":                map[string]any{"type": "number"},
				"quantity":             map[string]any{"type": "integer"},
				"variation_id":         nullable2("integer"),
				"combo_combination_id": nullable2("integer"),
			}, "id", "price", "quantity")},
		}, "name", "surname", "dir", "state", "city", "phone", "rate_type", "products")},
	}
	f.results["list_products"] = toonProduct
	f.results["list_departments"] = "items[2]{id,name,department_code}:\n  2,ANTIOQUIA,05\n  9,META,50\ncount: 2"
	f.results["quote_shipping"] = "items[2]:\n  - carrier: ENVIA\n    service: Estandar\n    shipping_cost: 11900\n    trajectory: \"2-4 dias\"\n  - carrier: SERVIENTREGA\n    shipping_cost: null\n    error: sin cobertura\ncount: 2\ncurrency: COP"
	f.results["create_order"] = "id: 55\nstatus: PENDIENTE"
}

// "Medellín" with the accent finds nothing in Dropi (count 0): the plain
// spelling must be tried, and the answer (TOON, tabular) must be understood.
func fakeCities(f *fakeDropi) {
	f.cityAnswer = func(name string) string {
		if name != "Medellin" {
			return "items[0]{id,department_id,name,cod_dane,zip_code}:\ncount: 0"
		}
		return "items[2]{id,department_id,name,cod_dane,zip_code}:\n  1,2,MEDELLIN,05001,050001\n  88,9,MEDELLIN,50001,null\ncount: 2"
	}
}

func TestRealQuoteTOON(t *testing.T) {
	f := newFake(t)
	dropiReal(f)
	fakeCities(f)
	_, _, tok := f.login(t)
	m := f.api(t, tok, nil)
	// No origin at all: Dropi quotes from the supplier's warehouse.
	qs, err := m.Quote(context.Background(), QuoteInput{COD: true, Amount: 89900, DestDANE: "0500100", ProductID: "2022974", Price: 89900, Units: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(qs) != 1 || qs[0].Carrier != "ENVIA" || qs[0].Freight != 11900 || qs[0].Days != 4 {
		t.Fatalf("quotes: %+v", qs)
	}
	a := f.lastArgs
	if a["cash_on_delivery"] != true {
		t.Fatalf("cod: %v", a)
	}
	dc := a["destination_city"].(map[string]any)
	if int64(dc["id"].(float64)) != 1 || dc["dane_code"] != "05001" || dc["zip_code"] != "050001" {
		t.Fatalf("destination_city must use Dropi's own data: %v", dc)
	}
	p := a["products"].([]any)[0].(map[string]any)
	if int64(p["id"].(float64)) != 2022974 || p["type"] != "SIMPLE" {
		t.Fatalf("product: %v", p)
	}
	if v, has := p["variation_id"]; has && v != nil {
		t.Fatalf("a SIMPLE product must not carry a variation: %v", p)
	}
	if _, has := a["zip_code"]; has {
		t.Fatalf("optional top-level zip_code must stay out: %v", a)
	}
	if c := f.citySearches; len(c) == 0 || c[0] != "Medellin" {
		t.Fatalf("city must be searched without accent first: %v", c)
	}
}

func TestRealProductCostTOON(t *testing.T) {
	f := newFake(t)
	dropiReal(f)
	_, _, tok := f.login(t)
	m := f.api(t, tok, nil)
	p, err := m.Product(context.Background(), "2022974")
	if err != nil {
		t.Fatal(err)
	}
	if p.Cost != 27000 || p.SalePrice != 55000 || p.Stock != 144 || p.Variations != 0 {
		t.Fatalf("%+v", p)
	}
}

func TestRealVariableProductIsNotQuoted(t *testing.T) {
	f := newFake(t)
	dropiReal(f)
	f.results["list_products"] = "items[1]:\n  - id: 7\n    name: Camisa\n    sale_price: 10000\n    suggested_price: 20000\n    stock: 5\n    variations[2]{id,name}:\n      1,S\n      2,M\ncount: 1\ncurrency: COP"
	_, _, tok := f.login(t)
	m := f.api(t, tok, nil)
	_, err := m.Quote(context.Background(), QuoteInput{COD: true, Amount: 1000, DestDANE: "0500100", ProductID: "7", Price: 20000, Units: 1})
	if err == nil || !strings.Contains(err.Error(), "variaciones") {
		t.Fatalf("a VARIABLE product must not be quoted blindly: %v", err)
	}
	for _, c := range f.calls {
		if c == "quote_shipping" {
			t.Fatal("quote_shipping must not be called for a VARIABLE product")
		}
	}
}

func TestRealCreateOrderOmitsVariation(t *testing.T) {
	f := newFake(t)
	dropiReal(f)
	fakeCities(f)
	_, _, tok := f.login(t)
	m := f.api(t, tok, nil)
	res, err := m.CreateOrder(context.Background(), orderPayload())
	if err != nil {
		t.Fatal(err)
	}
	if res.ID != "55" {
		t.Fatalf("order id from TOON answer: %+v", res)
	}
	p := f.lastArgs["products"].([]any)[0].(map[string]any)
	for _, k := range []string{"variation_id", "combo_combination_id"} {
		if v, has := p[k]; has && v != nil {
			t.Fatalf("%s must be left out for a SIMPLE product: %v", k, p)
		}
	}
	if f.lastArgs["city"] != "MEDELLIN" && f.lastArgs["city"] != "Medellín" {
		t.Fatalf("city: %v", f.lastArgs["city"])
	}
}

func TestPlainCityNameVariants(t *testing.T) {
	got := cityNames("Medellín")
	if len(got) != 2 || got[0] != "Medellin" || got[1] != "Medellín" {
		t.Fatalf("%v", got)
	}
}
