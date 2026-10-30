package api

import (
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
