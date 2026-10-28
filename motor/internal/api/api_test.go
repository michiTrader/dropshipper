package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/michiTrader/dropshipper/motor/internal/agent"
	"github.com/michiTrader/dropshipper/motor/internal/catalog"
	"github.com/michiTrader/dropshipper/motor/internal/config"
	"github.com/michiTrader/dropshipper/motor/internal/convo"
	"github.com/michiTrader/dropshipper/motor/internal/hub"
	"github.com/michiTrader/dropshipper/motor/internal/llm"
	"github.com/michiTrader/dropshipper/motor/internal/rules"
	"github.com/michiTrader/dropshipper/motor/internal/sales"
	"github.com/michiTrader/dropshipper/motor/internal/settings"
	"github.com/michiTrader/dropshipper/motor/internal/store"
)

type env struct {
	srv *Server
	h   http.Handler
	set *settings.Manager
	cat *catalog.Catalog
}

func newEnv(t *testing.T, token string) *env {
	t.Helper()
	kdir := filepath.Join("..", "..", "knowledge")
	st, err := store.OpenPath(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	n := config.Negocio{}
	n.ApplyDefaults()
	cfg := &config.Config{Negocio: n, AdminToken: token, WhatsApp: config.WhatsAppConfig{GraphVersion: "v21.0"}}
	set, err := settings.Open(context.Background(), st, func() settings.Settings { return settings.Seed(cfg) })
	if err != nil {
		t.Fatal(err)
	}
	cat, err := catalog.Open(context.Background(), st, kdir)
	if err != nil {
		t.Fatal(err)
	}
	zones, err := rules.LoadZones(filepath.Join(kdir, "zonas.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	h := hub.New()
	live := config.NewLive(set.Get().Negocio)
	set.OnChange(func(s settings.Settings) { h.Apply(s); live.Set(s.Negocio) })
	sv := &sales.Service{Store: st, Catalog: cat, Zones: zones, Negocio: live, Payments: h}
	seller := &agent.Seller{LLM: h.AI, Sales: sv, Store: st, Catalog: cat, Zones: zones, Negocio: live}
	eng := convo.NewEngine(st, seller, sv, h, live, log.New(io.Discard, "", 0))
	sv.Notifier = eng
	t.Cleanup(eng.Close)
	srv := &Server{Settings: set, Hub: h, Store: st, Engine: eng, Sales: sv, Catalog: cat, Zones: zones,
		MediaDir: t.TempDir(), Log: log.New(io.Discard, "", 0)}
	return &env{srv: srv, h: srv.Handler(), set: set, cat: cat}
}

type resp struct {
	Code int
	Body string
}

func (r resp) json(t *testing.T) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(r.Body), &m); err != nil {
		t.Fatalf("not json (%d): %s", r.Code, r.Body)
	}
	return m
}

// do sends a request as if it came straight from loopback.
func (e *env) do(method, path string, body any, hdr ...string) resp {
	var rd io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		rd = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, path, rd)
	req.RemoteAddr = "127.0.0.1:5555"
	req.Header.Set("Content-Type", "application/json")
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return resp{rec.Code, rec.Body.String()}
}

// fakeAI is an OpenAI-compatible server (what "Arxi" or DeepSeek look like to us).
func fakeAI(t *testing.T, wantKey string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+wantKey {
			w.WriteHeader(401)
			_, _ = w.Write([]byte(`{"error":{"message":"Authentication Fails"}}`))
			return
		}
		switch r.URL.Path {
		case "/models":
			_, _ = w.Write([]byte(`{"data":[{"id":"arxi-fast"},{"id":"arxi-pro"}]}`))
		case "/chat/completions":
			_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"listo"}}],"usage":{"total_tokens":7}}`))
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// ---------- access control ----------

func TestNoTokenOnlyLocal(t *testing.T) {
	e := newEnv(t, "")
	if r := e.do("GET", "/api/estado", nil); r.Code != 200 {
		t.Fatalf("loopback must work without a token: %d %s", r.Code, r.Body)
	}
	// a non-loopback client is refused with a clear reason
	req := httptest.NewRequest("GET", "/api/estado", nil)
	req.RemoteAddr = "203.0.113.9:4000"
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	if rec.Code != 401 || !strings.Contains(rec.Body.String(), "sin_token") {
		t.Fatalf("remote without token: %d %s", rec.Code, rec.Body.String())
	}
}

func TestNoTokenRejectsProxiedLoopback(t *testing.T) {
	e := newEnv(t, "")
	for _, h := range []string{"X-Forwarded-For", "Cf-Connecting-Ip", "X-Real-Ip", "Forwarded", "X-Forwarded-Host"} {
		if r := e.do("GET", "/api/ajustes", nil, h, "198.51.100.7"); r.Code != 401 {
			t.Errorf("%s must make a loopback request count as remote, got %d", h, r.Code)
		}
	}
}

func TestTokenRequired(t *testing.T) {
	e := newEnv(t, "secret-token-1")
	if r := e.do("GET", "/api/estado", nil); r.Code != 401 {
		t.Fatalf("no token: %d", r.Code)
	}
	if r := e.do("GET", "/api/estado", nil, "Authorization", "Bearer wrong"); r.Code != 401 {
		t.Fatalf("wrong token: %d", r.Code)
	}
	if r := e.do("GET", "/api/estado", nil, "Authorization", "Bearer secret-token-1"); r.Code != 200 {
		t.Fatalf("right token: %d", r.Code)
	}
	if r := e.do("GET", "/api/estado?token=secret-token-1", nil); r.Code != 200 {
		t.Fatalf("query token: %d", r.Code)
	}
	if r := e.do("GET", "/salud", nil); r.Code != 200 {
		t.Fatalf("health must stay public: %d", r.Code)
	}
}

// ---------- settings ----------

func TestSettingsViewMasksSecrets(t *testing.T) {
	e := newEnv(t, "")
	ai := fakeAI(t, "sk-real-key-0001")
	r := e.do("POST", "/api/ia/proveedores", map[string]any{"nombre": "Arxi", "preset": "custom", "base_url": ai.URL, "modelo": "arxi-fast", "api_key": "sk-real-key-0001", "activar": true})
	if r.Code != 200 || r.json(t)["ok"] != true {
		t.Fatalf("add: %d %s", r.Code, r.Body)
	}
	e.do("PUT", "/api/ajustes/whatsapp", map[string]any{"token": "EAAB-whatsapp-secret", "phone_number_id": "999", "app_secret": "wa-app-secret-x"})
	if r := e.do("PUT", "/api/ajustes/wompi", map[string]any{"public_key": "pub_test_1", "private_key": "prv_test_secret-y"}); r.Code < 400 || !strings.Contains(r.Body, "secreto de eventos") {
		t.Fatalf("a private key without events secret must be refused: %d %s", r.Code, r.Body)
	}
	if r := e.do("PUT", "/api/ajustes/wompi", map[string]any{"public_key": "pub_test_1", "private_key": "prv_test_secret-y", "events_secret": "evt-secret-zzzz"}); r.Code != 200 {
		t.Fatalf("%d %s", r.Code, r.Body)
	}
	got := e.do("GET", "/api/ajustes", nil)
	for _, leak := range []string{"sk-real-key-0001", "EAAB-whatsapp-secret", "wa-app-secret-x", "prv_test_secret-y", "evt-secret-zzzz"} {
		if strings.Contains(got.Body, leak) {
			t.Errorf("GET /api/ajustes leaked %q", leak)
		}
	}
	if !strings.Contains(got.Body, "••••") || !strings.Contains(got.Body, "pub_test_1") {
		t.Errorf("expected masks and the public key: %s", got.Body)
	}
}

func TestEmptySecretKeepsOldOne(t *testing.T) {
	e := newEnv(t, "")
	e.do("PUT", "/api/ajustes/whatsapp", map[string]any{"token": "tok-original-1234", "phone_number_id": "111"})
	e.do("PUT", "/api/ajustes/whatsapp", map[string]any{"token": "", "phone_number_id": "222"})
	w := e.set.Get().WhatsApp
	if w.Token != "tok-original-1234" || w.PhoneNumberID != "222" {
		t.Fatalf("%+v", w)
	}
}

func TestBusinessSettingsValidation(t *testing.T) {
	e := newEnv(t, "")
	r := e.do("PUT", "/api/ajustes/negocio", map[string]any{"finanzas": map[string]any{"tasa_devolucion": 1.7, "trm_cop_usd": 4000, "margen_seguridad": 0.3, "tasa_confirmacion": 0.85}})
	if r.Code < 400 || !strings.Contains(r.Body, "devolución") {
		t.Fatalf("expected friendly validation error: %d %s", r.Code, r.Body)
	}
	r = e.do("PUT", "/api/ajustes/negocio", map[string]any{"persona": map[string]any{"nombre": "Valeria"}})
	if r.Code != 200 {
		t.Fatalf("%d %s", r.Code, r.Body)
	}
	n := e.set.Get().Negocio
	if n.Persona.Nombre != "Valeria" || n.Finanzas.TRM == 0 || n.NombreTienda == "" {
		t.Fatalf("partial update must keep the other values: %+v", n)
	}
}

func TestGeneralTokenRules(t *testing.T) {
	e := newEnv(t, "")
	if r := e.do("PUT", "/api/ajustes/general", map[string]any{"token_admin": "corto"}); r.Code < 400 {
		t.Fatalf("short token must be rejected: %d", r.Code)
	}
	if r := e.do("PUT", "/api/ajustes/general", map[string]any{"url_publica": "tienda.com"}); r.Code < 400 {
		t.Fatalf("url without scheme must be rejected: %d", r.Code)
	}
	if r := e.do("PUT", "/api/ajustes/general", map[string]any{"url_publica": "https://tienda.example/", "token_admin": "un-token-largo"}); r.Code != 200 {
		t.Fatalf("%d %s", r.Code, r.Body)
	}
	if g := e.set.Get().General; g.PublicURL != "https://tienda.example" || g.AdminToken != "un-token-largo" {
		t.Fatalf("%+v", g)
	}
	// from now on the token is required
	if r := e.do("GET", "/api/estado", nil); r.Code != 401 {
		t.Fatalf("token must now be enforced: %d", r.Code)
	}
}

// ---------- AI providers ----------

func TestProviderTestAndModels(t *testing.T) {
	e := newEnv(t, "")
	ai := fakeAI(t, "good-key-12345")
	body := map[string]any{"preset": "custom", "base_url": ai.URL + "/chat/completions", "modelo": "arxi-fast", "api_key": "good-key-12345"}

	r := e.do("POST", "/api/ia/probar", body).json(t)
	if r["ok"] != true || !strings.Contains(r["mensaje"].(string), "Conexión correcta") {
		t.Fatalf("%v", r)
	}
	m := e.do("POST", "/api/ia/modelos", body).json(t)
	if m["ok"] != true || len(m["modelos"].([]any)) != 2 {
		t.Fatalf("%v", m)
	}
	body["api_key"] = "bad-key"
	bad := e.do("POST", "/api/ia/probar", body)
	if bad.Code != 200 { // test endpoints always answer 200 with ok=false
		t.Fatalf("code %d", bad.Code)
	}
	if j := bad.json(t); j["ok"] != false || !strings.Contains(j["error"].(string), "rechazó la clave") {
		t.Fatalf("%v", j)
	}
	body["api_key"], body["modelo"] = "", ""
	if j := e.do("POST", "/api/ia/probar", body).json(t); j["ok"] != false {
		t.Fatalf("missing key/model must fail: %v", j)
	}
}

func TestProviderLifecycleAndHotSwap(t *testing.T) {
	e := newEnv(t, "")
	a := fakeAI(t, "key-a-123456")
	b := fakeAI(t, "key-b-123456")
	add := func(name, url, key string, activate bool) string {
		r := e.do("POST", "/api/ia/proveedores", map[string]any{"nombre": name, "preset": "custom", "base_url": url, "modelo": "m", "api_key": key, "activar": activate}).json(t)
		if r["ok"] != true {
			t.Fatalf("%v", r)
		}
		return r["id"].(string)
	}
	ida := add("Alfa", a.URL, "key-a-123456", true)
	idb := add("Beta", b.URL, "key-b-123456", false)
	if e.set.Get().IA.ActiveID != ida || !e.srv.Hub.AI.Ready() {
		t.Fatal("first provider must be active and live")
	}
	// the live switch answers through the active provider
	if _, err := e.srv.Hub.AI.Complete(context.Background(), llmReq()); err != nil {
		t.Fatal(err)
	}
	if r := e.do("POST", "/api/ia/proveedores/"+idb+"/activar", nil); r.Code != 200 {
		t.Fatalf("%d %s", r.Code, r.Body)
	}
	if e.set.Get().IA.ActiveID != idb {
		t.Fatal("activation did not stick")
	}
	if _, err := e.srv.Hub.AI.Complete(context.Background(), llmReq()); err != nil {
		t.Fatalf("after hot swap: %v", err)
	}
	if r := e.do("DELETE", "/api/ia/proveedores/"+idb, nil); r.Code != 200 {
		t.Fatalf("%d %s", r.Code, r.Body)
	}
	if e.set.Get().IA.ActiveID != ida {
		t.Fatalf("removing the active provider must fall back to another, got %q", e.set.Get().IA.ActiveID)
	}
	if r := e.do("DELETE", "/api/ia/proveedores/nope", nil); r.Code != 404 {
		t.Fatalf("unknown provider: %d", r.Code)
	}
}

func TestSavedKeyNeverGoesToAnotherHost(t *testing.T) {
	e := newEnv(t, "")
	a := fakeAI(t, "key-a-123456")
	r := e.do("POST", "/api/ia/proveedores", map[string]any{"nombre": "Alfa", "preset": "custom", "base_url": a.URL, "modelo": "m", "api_key": "key-a-123456"}).json(t)
	id := r["id"].(string)
	// pointing the same provider at a different host WITHOUT a new key must not reuse the saved key
	evil := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Header.Get("Authorization") != "" {
			t.Errorf("the saved key was sent to another server: %q", req.Header.Get("Authorization"))
		}
		w.WriteHeader(401)
	}))
	defer evil.Close()
	evilURL := strings.Replace(evil.URL, "127.0.0.1", "localhost", 1)
	res := e.do("POST", "/api/ia/probar", map[string]any{"id": id, "base_url": evilURL, "modelo": "m"}).json(t)
	if res["ok"] != false {
		t.Fatalf("%v", res)
	}
	if r := e.do("PUT", "/api/ia/proveedores/"+id, map[string]any{"base_url": evilURL}); r.Code < 400 {
		t.Fatalf("saving a new host without a key must fail, got %d %s", r.Code, r.Body)
	}
	if p, _ := e.set.Get().IA.Provider(id); p.APIKey != "key-a-123456" || strings.Contains(p.BaseURL, "localhost") {
		t.Fatalf("a rejected save must not change the provider: %+v", p)
	}
}

func TestStatusReflectsConfiguration(t *testing.T) {
	e := newEnv(t, "")
	s := e.do("GET", "/api/estado", nil).json(t)
	ia := s["ia"].(map[string]any)
	if ia["configurada"] != false || ia["hay_proveedor"] != false {
		t.Fatalf("fresh install has no AI: %v", ia)
	}
	if s["productos"].(map[string]any)["total"].(float64) != 1 {
		t.Fatalf("%v", s["productos"])
	}
	ai := fakeAI(t, "k-123456789")
	e.do("POST", "/api/ia/proveedores", map[string]any{"nombre": "Arxi", "preset": "custom", "base_url": ai.URL, "modelo": "m", "api_key": "k-123456789"})
	s = e.do("GET", "/api/estado", nil).json(t)
	if ia = s["ia"].(map[string]any); ia["configurada"] != true || ia["proveedor"] != "Arxi" {
		t.Fatalf("%v", ia)
	}
}

// ---------- products ----------

func newProduct(sku string) map[string]any {
	return map[string]any{"sku": sku, "nombre": "Producto " + sku, "costo_proveedor": 20000, "flete_estimado": 12000,
		"ofertas": []map[string]any{{"id": "1x", "etiqueta": "1x", "unidades": 1, "precio": 79900}, {"id": "2x", "etiqueta": "2x", "unidades": 2, "precio": 129900}}}
}

func TestProductsMultiProductFlow(t *testing.T) {
	e := newEnv(t, "")
	if r := e.do("POST", "/api/productos", newProduct("dos-1")); r.Code != 200 {
		t.Fatalf("%d %s", r.Code, r.Body)
	}
	if r := e.do("POST", "/api/productos", newProduct("DOS-1")); r.Code < 400 || !strings.Contains(r.Body, "ya existe") {
		t.Fatalf("duplicate sku: %d %s", r.Code, r.Body)
	}
	var list []map[string]any
	_ = json.Unmarshal([]byte(e.do("GET", "/api/productos", nil).Body), &list)
	if len(list) != 2 {
		t.Fatalf("%d products", len(list))
	}
	d := e.do("GET", "/api/productos/dos-1", nil).json(t)
	eco := d["economia"].([]any)
	if len(eco) != 2 || d["finanzas"] == nil {
		t.Fatalf("%v", d)
	}
	if first := eco[0].(map[string]any); first["contraentrega"] == nil || first["cpa_objetivo"] == nil {
		t.Fatalf("economics incomplete: %v", first)
	}
	// edit keeps the code and the active flag
	up := newProduct("OTRO")
	up["nombre"] = "Renombrado"
	if r := e.do("PUT", "/api/productos/DOS-1", up); r.Code != 200 {
		t.Fatalf("%d %s", r.Code, r.Body)
	}
	if p, ok := e.cat.Get("DOS-1"); !ok || p.Name != "Renombrado" || p.SKU != "DOS-1" {
		t.Fatalf("%+v", p)
	}
	if _, ok := e.cat.Get("OTRO"); ok {
		t.Fatal("the code must not change on edit")
	}
	// activation rules
	if r := e.do("POST", "/api/productos/LULLA-60/activar", map[string]any{"activo": false}); r.Code < 400 {
		t.Fatalf("last active must stay on: %d", r.Code)
	}
	if r := e.do("POST", "/api/productos/DOS-1/activar", map[string]any{"activo": true}); r.Code != 200 {
		t.Fatalf("%d %s", r.Code, r.Body)
	}
	if r := e.do("POST", "/api/productos/LULLA-60/activar", map[string]any{"activo": false}); r.Code != 200 {
		t.Fatalf("%d %s", r.Code, r.Body)
	}
	if r := e.do("DELETE", "/api/productos/NOPE", nil); r.Code != 404 {
		t.Fatalf("unknown: %d", r.Code)
	}
	if r := e.do("DELETE", "/api/productos/LULLA-60", nil); r.Code != 200 {
		t.Fatalf("an inactive product without orders can be deleted: %d %s", r.Code, r.Body)
	}
}

func TestProductValidationMessages(t *testing.T) {
	e := newEnv(t, "")
	p := newProduct("X-1")
	p["ofertas"] = []map[string]any{}
	if r := e.do("POST", "/api/productos", p); r.Code < 400 || !strings.Contains(r.Body, "oferta") {
		t.Fatalf("%d %s", r.Code, r.Body)
	}
	p = newProduct("bad sku")
	if r := e.do("POST", "/api/productos", p); r.Code < 400 || !strings.Contains(r.Body, "SKU") {
		t.Fatalf("%d %s", r.Code, r.Body)
	}
}

func TestCalcPreviewDoesNotSave(t *testing.T) {
	e := newEnv(t, "")
	r := e.do("POST", "/api/productos/calcular", newProduct("PREVIEW")).json(t)
	if len(r["economia"].([]any)) != 2 || r["supuestos"] == nil {
		t.Fatalf("%v", r)
	}
	if _, ok := e.cat.Get("PREVIEW"); ok {
		t.Fatal("preview must not persist")
	}
}

func TestPlaybookAPI(t *testing.T) {
	e := newEnv(t, "")
	if r := e.do("PUT", "/api/guia-ventas", map[string]any{"texto": "Saluda con calidez."}); r.Code != 200 {
		t.Fatalf("%d", r.Code)
	}
	if got := e.do("GET", "/api/guia-ventas", nil).json(t); got["texto"] != "Saluda con calidez." {
		t.Fatalf("%v", got)
	}
}

func TestPanelServesWithoutEmojiShell(t *testing.T) {
	e := newEnv(t, "")
	r := e.do("GET", "/", nil)
	if r.Code != 200 || !strings.Contains(r.Body, "<html") {
		t.Fatalf("%d", r.Code)
	}
}

func llmReq() llm.Request {
	return llm.Request{Messages: []llm.Message{{Role: "user", Content: "hola"}}}
}
