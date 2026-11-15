package dropi

import (
	"context"
	"strings"
	"testing"
)

// realTools mimics the shapes the owner saw in the panel for Dropi's MCP:
// quote_shipping asks cash.on.delivery, destination.city.id and
// products[id, price, quantity, type]; create_order asks rate.type.
func realTools(f *fakeDropi) {
	f.tools = []map[string]any{
		{"name": "quote_shipping", "description": "Quote shipping for an order", "inputSchema": schema(map[string]any{
			"cashOnDelivery": map[string]any{"type": "boolean"},
			"destination": schema(map[string]any{
				"city": schema(map[string]any{"id": map[string]any{"type": "integer"}}, "id"),
			}, "city"),
			"products": map[string]any{"type": "array", "items": schema(map[string]any{
				"id":       map[string]any{"type": "integer"},
				"price":    map[string]any{"type": "number"},
				"quantity": map[string]any{"type": "integer"},
				"type":     map[string]any{"type": "string", "enum": []any{"SIMPLE", "VARIABLE"}},
			}, "id", "price", "quantity", "type")},
		}, "cashOnDelivery", "destination", "products")},
		{"name": "list_cities", "description": "List cities of Colombia", "inputSchema": schema(map[string]any{
			"search": map[string]any{"type": "string"},
		})},
		{"name": "list_products", "description": "Search products", "inputSchema": schema(map[string]any{
			"search": map[string]any{"type": "string"},
		})},
		{"name": "create_order", "description": "Create a new order", "inputSchema": schema(map[string]any{
			"name":    strField("name"),
			"surname": strField("surname"),
			"phone":   strField("phone"),
			"dir":     strField("address"),
			"city":    strField("city"),
			"rate": schema(map[string]any{
				"type": map[string]any{"type": "string", "enum": []any{"CON RECAUDO", "SIN RECAUDO"}},
			}, "type"),
			"products": map[string]any{"type": "array", "items": schema(map[string]any{
				"id":       map[string]any{"type": "integer"},
				"quantity": map[string]any{"type": "integer"},
				"price":    map[string]any{"type": "number"},
			}, "id", "quantity")},
		}, "name", "phone", "dir", "city", "rate", "products")},
	}
	f.results["list_cities"] = `{"objects":[{"id":1,"name":"MEDELLIN","department":{"id":2,"name":"ANTIOQUIA"}},{"id":88,"name":"MEDELLIN","department":{"id":9,"name":"META"}},{"id":1100,"name":"BOGOTA D.C.","department":{"id":11,"name":"BOGOTA"}}]}`
	f.results["quote_shipping"] = `{"objects":[{"distribution_company":{"name":"ENVIA"},"shipping_amount":11900}]}`
	f.results["create_order"] = `{"objects":{"id":55}}`
}

func TestQuoteFillsRealFieldPaths(t *testing.T) {
	f := newFake(t)
	realTools(f)
	_, _, tok := f.login(t)
	m := f.api(t, tok, nil)
	in := QuoteInput{COD: true, Amount: 89900, OriginDANE: "0500100", DestDANE: "0500100", ProductID: "777", Price: 89900, Units: 1}
	qs, err := m.Quote(context.Background(), in)
	if err != nil || len(qs) != 1 || qs[0].Freight != 11900 {
		t.Fatalf("%v %v", qs, err)
	}
	a := f.lastArgs
	if a["cashOnDelivery"] != true {
		t.Fatalf("cash on delivery: %v", a)
	}
	city := a["destination"].(map[string]any)["city"].(map[string]any)
	if int64(city["id"].(float64)) != 1 { // Medellín of Antioquia, not the one of Meta
		t.Fatalf("city id must be the Antioquia one: %v", a)
	}
	p := a["products"].([]any)[0].(map[string]any)
	if int64(p["id"].(float64)) != 777 || int64(p["price"].(float64)) != 89900 || int64(p["quantity"].(float64)) != 1 || p["type"] != "SIMPLE" {
		t.Fatalf("product: %v", p)
	}
	// the city list is asked once and remembered
	m.Quote(context.Background(), in)
	n := 0
	for _, c := range f.calls {
		if c == "list_cities" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("list_cities called %d times, want 1 (cached)", n)
	}
}

func TestQuoteAsksForTheProductWhenMissing(t *testing.T) {
	f := newFake(t)
	realTools(f)
	_, _, tok := f.login(t)
	m := f.api(t, tok, nil)
	_, err := m.Quote(context.Background(), QuoteInput{COD: true, Amount: 1000, OriginDANE: "0500100", DestDANE: "0500100"})
	if err == nil || !strings.Contains(err.Error(), "ID de Dropi") {
		t.Fatalf("must tell the owner to set the Dropi product id: %v", err)
	}
}

func TestCreateOrderFillsRateType(t *testing.T) {
	f := newFake(t)
	realTools(f)
	_, _, tok := f.login(t)
	m := f.api(t, tok, nil)
	res, err := m.CreateOrder(context.Background(), orderPayload())
	if err != nil || res.ID != "55" {
		t.Fatalf("%v %v", res, err)
	}
	if got := f.lastArgs["rate"].(map[string]any)["type"]; got != "CON RECAUDO" {
		t.Fatalf("rate.type = %v", got)
	}
}

func TestReportReadyWithRealFieldPaths(t *testing.T) {
	f := newFake(t)
	realTools(f)
	_, _, tok := f.login(t)
	m := f.api(t, tok, nil)
	_, rep, err := m.Report(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rep {
		if !r.Ready {
			t.Errorf("not ready: %+v", r)
		}
	}
}

func TestMissingFieldsShowWhatTheyWant(t *testing.T) {
	f := newFake(t)
	f.tools = []map[string]any{{"name": "quote_shipping", "description": "Quote shipping", "inputSchema": schema(map[string]any{
		"carrier_mode": map[string]any{"type": "string", "enum": []any{"A", "B"}, "description": "modo"},
	}, "carrier_mode")}}
	_, _, tok := f.login(t)
	m := f.api(t, tok, nil)
	_, err := m.Quote(context.Background(), QuoteInput{Amount: 1})
	if err == nil || !strings.Contains(err.Error(), "valores: A/B") {
		t.Fatalf("the error must say what the field wants: %v", err)
	}
}

func TestCallLogRecordsAndHidesSecrets(t *testing.T) {
	f := newFake(t)
	realTools(f)
	f.results["list_products"] = `{"objects":[],"access_token":"abcdefghijklmnop123"}`
	_, _, tok := f.login(t)
	m := f.api(t, tok, nil)
	if _, err := m.Call(context.Background(), "list_products", map[string]any{"search": "x"}); err != nil {
		t.Fatal(err)
	}
	l := m.CallLog()
	if len(l) != 1 || l[0].Tool != "list_products" || !strings.Contains(l[0].Args, "search") {
		t.Fatalf("%+v", l)
	}
	if strings.Contains(l[0].Result, "abcdefghijklmnop123") {
		t.Fatalf("secret leaked: %s", l[0].Result)
	}
}
