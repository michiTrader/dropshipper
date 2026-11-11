package api

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/michiTrader/dropshipper/motor/internal/dropi"
)

// fakeDropi imitates the Dropi Integrations API for one valid token.
func fakeDropi(t *testing.T, token string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("dropi-integration-key") != token {
			w.WriteHeader(401)
			_, _ = w.Write([]byte(`{"isSuccess":false,"message":"Access denied"}`))
			return
		}
		switch r.URL.Path {
		case "/department":
			_, _ = w.Write([]byte(`{"isSuccess":true,"objects":[{"id":1,"name":"Antioquia"}]}`))
		case "/orders/cotizaEnvioTransportadoraV2":
			_, _ = w.Write([]byte(`{"isSuccess":true,"objects":[
				{"distribution_company":{"name":"COORDINADORA"},"shipping_amount":19000},
				{"distribution_company":{"name":"ENVIA"},"shipping_amount":14000}]}`))
		case "/products/v2/555":
			_, _ = w.Write([]byte(`{"isSuccess":true,"objects":{"id":555,"name":"Gomitas","sale_price":27000,"stock":10}}`))
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(srv.Close)
	old := dropi.BaseURLOverride
	dropi.BaseURLOverride = srv.URL
	t.Cleanup(func() { dropi.BaseURLOverride = old })
	return srv
}

func connectDropi(t *testing.T, e *env, token string) {
	t.Helper()
	r := e.do("PUT", "/api/ajustes/dropi", map[string]any{"token": token, "origen_departamento": "Cundinamarca", "origen_ciudad": "Bogotá"})
	if r.Code != 200 {
		t.Fatalf("guardar dropi: %d %s", r.Code, r.Body)
	}
}

func TestDropiSaveTestAndMasking(t *testing.T) {
	fakeDropi(t, "tok-dropi-123456")
	e := newEnv(t, "")

	if r := e.do("POST", "/api/dropi/probar", map[string]any{"token": "malo"}); r.json(t)["ok"] != false || !strings.Contains(r.Body, "rechazó el token") {
		t.Fatalf("token malo: %s", r.Body)
	}
	if r := e.do("POST", "/api/dropi/probar", map[string]any{"token": "tok-dropi-123456"}); r.json(t)["ok"] != true || !strings.Contains(r.Body, "Conectado a Dropi") {
		t.Fatalf("token bueno: %s", r.Body)
	}
	if r := e.do("PUT", "/api/ajustes/dropi", map[string]any{"token": "tok-dropi-123456", "origen_ciudad": "Narnia"}); r.Code != 400 {
		t.Fatalf("ciudad inválida debe rechazarse: %d %s", r.Code, r.Body)
	}
	connectDropi(t, e, "tok-dropi-123456")

	view := e.do("GET", "/api/ajustes", nil)
	if strings.Contains(view.Body, "tok-dropi-123456") {
		t.Fatalf("el token jamás debe salir completo: %s", view.Body)
	}
	d := view.json(t)["dropi"].(map[string]any)
	if d["configurado"] != true || d["origen_ciudad"] != "Bogotá" {
		t.Fatalf("dropi view: %+v", d)
	}
	// Saving again without a token keeps the stored one.
	e.do("PUT", "/api/ajustes/dropi", map[string]any{"origen_departamento": "Antioquia", "origen_ciudad": "Medellín"})
	if e.set.Get().Dropi.Token != "tok-dropi-123456" {
		t.Fatal("el token vacío debe conservar el guardado")
	}
	if e.set.Get().Negocio.Dropi.Modo != "api" {
		t.Fatal("con token el modo de Dropi debe pasar a api")
	}
	if r := e.do("DELETE", "/api/ajustes/dropi", nil); r.Code != 200 || e.set.Get().Dropi.Token != "" || e.set.Get().Negocio.Dropi.Modo != "manual" {
		t.Fatalf("quitar dropi: %d %s", r.Code, r.Body)
	}
}

func TestQuoteShippingComparesCarriers(t *testing.T) {
	fakeDropi(t, "tok-dropi-123456")
	e := newEnv(t, "")

	// Without Dropi: estimated freight, flagged as such.
	r := e.do("POST", "/api/envios/cotizar", map[string]any{"departamento": "Antioquia", "ciudad": "Medellín"}).json(t)
	cmp := r["comparacion"].(map[string]any)
	if cmp["fuente_flete"] != "estimado" || r["costo_confirmado"] != false {
		t.Fatalf("sin Dropi: %+v", r)
	}

	connectDropi(t, e, "tok-dropi-123456")
	r = e.do("POST", "/api/envios/cotizar", map[string]any{"departamento": "Antioquia", "ciudad": "Medellín", "oferta_id": "2x"}).json(t)
	cmp = r["comparacion"].(map[string]any)
	best := cmp["mejor"].(map[string]any)
	if cmp["fuente_flete"] != "dropi" || best["transportadora"] != "Envía" || best["flete"].(float64) != 14000 {
		t.Fatalf("con Dropi: %+v", cmp)
	}
	if len(cmp["opciones"].([]any)) != 2 {
		t.Fatalf("opciones: %+v", cmp["opciones"])
	}
	if r := e.do("POST", "/api/envios/cotizar", map[string]any{"departamento": "Antioquia"}); r.Code != 400 {
		t.Fatalf("sin ciudad: %d", r.Code)
	}
}

func TestSimulateDropiOrder(t *testing.T) {
	fakeDropi(t, "tok-dropi-123456")
	e := newEnv(t, "")

	// Not connected: an error check, but still a harmless 200.
	r := e.do("POST", "/api/dropi/simular", map[string]any{"departamento": "Antioquia", "ciudad": "Medellín"}).json(t)
	if r["ok"] != false || r["simulacion"] != true {
		t.Fatalf("sin conexión: %+v", r)
	}

	connectDropi(t, e, "tok-dropi-123456")
	r = e.do("POST", "/api/dropi/simular", map[string]any{"departamento": "Antioquia", "ciudad": "Medellín", "oferta_id": "2x"}).json(t)
	if r["ok"] != true || !strings.Contains(r["mensaje"].(string), "no se creó ningún pedido") {
		t.Fatalf("simulación: %+v", r)
	}
	states := map[string]string{}
	for _, c := range r["verificaciones"].([]any) {
		m := c.(map[string]any)
		states[m["nombre"].(string)] = m["estado"].(string)
	}
	if states["Conexión con Dropi"] != "ok" || states["Flete y transportadora"] == "" || states["Costo confirmado"] != "aviso" {
		t.Fatalf("verificaciones: %+v", states)
	}
	payload := r["pedido_dropi"].(map[string]any)
	if payload["distributionCompany"] != "Envía" || payload["rate_type"] != "CON RECAUDO" {
		t.Fatalf("payload: %+v", payload)
	}
	if orders, _ := e.srv.Store.ListOrders(t.Context()); len(orders) != 0 {
		t.Fatal("simular no debe crear pedidos")
	}
}

func TestPullDropiCosts(t *testing.T) {
	fakeDropi(t, "tok-dropi-123456")
	e := newEnv(t, "")
	connectDropi(t, e, "tok-dropi-123456")
	sku := e.cat.Default().SKU

	if r := e.do("POST", "/api/productos/"+sku+"/costos-dropi", nil); r.Code != 400 || !strings.Contains(r.Body, "ID de Dropi") {
		t.Fatalf("sin ID de Dropi: %d %s", r.Code, r.Body)
	}
	p := e.cat.Default()
	p.DropiProductID = "555"
	if _, err := e.cat.Save(t.Context(), p); err != nil {
		t.Fatal(err)
	}
	r := e.do("POST", "/api/productos/"+sku+"/costos-dropi", nil)
	if r.Code != 200 || r.json(t)["costo"].(float64) != 27000 {
		t.Fatalf("traer costos: %d %s", r.Code, r.Body)
	}
	got, _ := e.cat.Get(sku)
	if got.UnitCost != 27000 || !got.CostConfirmed {
		t.Fatalf("el costo debe quedar confirmado: %+v", got)
	}
}

// With a Dropi ID, saving the product copies the REAL cost and a real freight
// (average of the cheapest quote to the main cities) without pressing anything.
func TestSaveProductSyncsRealCostAndFreight(t *testing.T) {
	fakeDropi(t, "tok-dropi-123456")
	e := newEnv(t, "")
	connectDropi(t, e, "tok-dropi-123456")
	sku := e.cat.Default().SKU

	p := e.cat.Default()
	if p.FreightConfirmed {
		t.Fatal("el flete de la ficha inicial es un estimado")
	}
	p.DropiProductID = "555"
	r := e.do("PUT", "/api/productos/"+sku, p)
	if r.Code != 200 {
		t.Fatalf("guardar: %d %s", r.Code, r.Body)
	}
	got, _ := e.cat.Get(sku)
	// fakeDropi quotes COORDINADORA 19000 / ENVIA 14000 for every route: cheapest = 14000.
	if got.UnitCost != 27000 || !got.CostConfirmed || got.Freight != 14000 || !got.FreightConfirmed || got.DropiSyncedAt == "" {
		t.Fatalf("debía traer costo y flete reales: %+v", got)
	}

	// Typing another freight by hand makes it an estimate again.
	got.Freight = 20000
	if r := e.do("PUT", "/api/productos/"+sku, got); r.Code != 200 {
		t.Fatalf("guardar 2: %d %s", r.Code, r.Body)
	}
	// ...and the next save with a Dropi ID refreshes it from Dropi.
	again, _ := e.cat.Get(sku)
	if again.Freight != 14000 || !again.FreightConfirmed {
		t.Fatalf("el flete a mano debe volver a leerse de Dropi: %+v", again)
	}
}

func TestSyncKeepsFreightEstimatedWhenDropiCannotQuote(t *testing.T) {
	// Sandbox has no carriers: the cost is real, the freight must NOT be marked real.
	srv := fakeDropi(t, "tok-dropi-123456")
	_ = srv
	e := newEnv(t, "")
	e.do("PUT", "/api/ajustes/dropi", map[string]any{"token": "tok-dropi-123456"}) // no origin city
	sku := e.cat.Default().SKU
	p := e.cat.Default()
	p.DropiProductID = "555"
	if _, err := e.cat.Save(t.Context(), p); err != nil {
		t.Fatal(err)
	}
	res, err := e.srv.SyncProduct(t.Context(), sku)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := e.cat.Get(sku)
	if !got.CostConfirmed || got.FreightConfirmed || res.FreightWarn == "" {
		t.Fatalf("costo real pero flete aún estimado y con motivo: %+v %+v", got, res)
	}
}

func TestSyncAllSkipsProductsWithoutDropiID(t *testing.T) {
	fakeDropi(t, "tok-dropi-123456")
	e := newEnv(t, "")
	connectDropi(t, e, "tok-dropi-123456")
	if n := e.srv.SyncAll(t.Context()); n != 0 {
		t.Fatalf("sin ID de Dropi no hay nada que sincronizar: %d", n)
	}
	p := e.cat.Default()
	p.DropiProductID = "555"
	if _, err := e.cat.Save(t.Context(), p); err != nil {
		t.Fatal(err)
	}
	if n := e.srv.SyncAll(t.Context()); n != 1 {
		t.Fatalf("debía actualizar 1 producto: %d", n)
	}
}

func TestDropiAdviceExplainsIPRestrictedToken(t *testing.T) {
	payload := `{"integration_type":"APIFY","ip_url":[{"url":"tambora.co","ip":null},{"url":null,"ip":"67.23.253.250"}]}`
	tok := "x." + base64.RawURLEncoding.EncodeToString([]byte(payload)) + ".y"
	err := &dropi.APIError{Status: 401, Body: "Access denied", IP: "181.134.155.66"}
	msg := dropiAdviceTok(err, tok)
	for _, want := range []string{"181.134.155.66", "APIFY", "67.23.253.250", "tambora.co", "Tu IP no está en esa lista"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("falta %q en: %s", want, msg)
		}
	}
	if strings.Contains(msg, tok) {
		t.Fatal("el mensaje nunca debe repetir el token")
	}
	// Unrestricted/unknown token: generic advice, still shows the IP.
	if m := dropiAdviceTok(err, "abc"); !strings.Contains(m, "181.134.155.66") || strings.Contains(m, "Tu IP no está") {
		t.Fatalf("consejo genérico: %s", m)
	}
}
