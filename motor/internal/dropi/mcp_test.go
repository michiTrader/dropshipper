package dropi

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeDropi is Dropi's OAuth server and MCP server in one.
type fakeDropi struct {
	t   *testing.T
	srv *httptest.Server

	mu        sync.Mutex
	access    string // the access token the MCP accepts
	refresh   string // the refresh token the OAuth server accepts
	rotate    bool
	sse       bool
	n         int // counter for new tokens
	challenge string
	calls     []string       // tools/call names
	lastArgs  map[string]any // arguments of the last tools/call
	inits     int
	dropSess  bool // forget the session once (404)
	tools     []map[string]any
	results   map[string]string // tool name -> JSON text of the answer
	refreshes int
}

func schema(props map[string]any, required ...string) map[string]any {
	return map[string]any{"type": "object", "properties": props, "required": required}
}

func strField(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }

func newFake(t *testing.T) *fakeDropi {
	f := &fakeDropi{t: t, access: "acc-1", refresh: "ref-1", results: map[string]string{}}
	f.tools = []map[string]any{
		{"name": "cotizar_envio", "description": "Cotiza el envío con cada transportadora", "inputSchema": schema(map[string]any{
			"codigo_dane_origen":  strField("DANE de origen"),
			"codigo_dane_destino": strField("DANE de destino"),
			"valor":               map[string]any{"type": "number"},
			"con_recaudo":         map[string]any{"type": "boolean"},
		}, "codigo_dane_origen", "codigo_dane_destino", "valor", "con_recaudo")},
		{"name": "detalle_producto", "description": "Detalle de un producto por id", "inputSchema": schema(map[string]any{
			"producto_id": map[string]any{"type": "string"},
		}, "producto_id")},
		{"name": "crear_orden", "description": "Crea una orden nueva", "inputSchema": schema(map[string]any{
			"nombre":    strField("nombre"),
			"apellido":  strField("apellido"),
			"telefono":  strField("telefono"),
			"direccion": strField("direccion"),
			"ciudad":    strField("ciudad"),
			"productos": map[string]any{"type": "array", "items": schema(map[string]any{
				"producto_id": map[string]any{"type": "string"},
				"cantidad":    map[string]any{"type": "integer"},
				"precio":      map[string]any{"type": "number"},
			}, "producto_id", "cantidad")},
		}, "nombre", "apellido", "telefono", "direccion", "ciudad", "productos")},
	}
	f.results["cotizar_envio"] = `{"objects":[{"distribution_company":{"name":"ENVIA"},"shipping_amount":12900},{"distribution_company":{"name":"TCC"},"shipping_amount":15000}]}`
	f.results["detalle_producto"] = `{"objects":{"id":77,"name":"Gomitas","sale_price":21000,"suggested_price":89900,"stock":40}}`
	f.results["crear_orden"] = `{"objects":{"id":9001,"shipping_guide":"G-1"}}`

	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/oauth-protected-resource/mcp", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"authorization_servers":["%s/iss"]}`, f.srv.URL)
	})
	mux.HandleFunc("/iss/.well-known/oauth-authorization-server", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"authorization_endpoint":"%[1]s/authorize","token_endpoint":"%[1]s/token","registration_endpoint":"%[1]s/register","revocation_endpoint":"%[1]s/revoke"}`, f.srv.URL)
	})
	mux.HandleFunc("/register", func(w http.ResponseWriter, r *http.Request) {
		var in map[string]any
		_ = json.NewDecoder(r.Body).Decode(&in)
		if in["token_endpoint_auth_method"] != "none" {
			w.WriteHeader(400)
			return
		}
		w.WriteHeader(201)
		_, _ = w.Write([]byte(`{"client_id":"cid-1"}`))
	})
	mux.HandleFunc("/token", f.token)
	mux.HandleFunc("/revoke", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	mux.HandleFunc("/mcp", f.mcp)
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeDropi) mcpURL() string { return f.srv.URL + "/mcp" }

func (f *fakeDropi) token(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	f.mu.Lock()
	defer f.mu.Unlock()
	fail := func(code int, e string) {
		w.WriteHeader(code)
		fmt.Fprintf(w, `{"error":%q}`, e)
	}
	switch r.Form.Get("grant_type") {
	case "authorization_code":
		sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
		if r.Form.Get("code") != "the-code" || base64.RawURLEncoding.EncodeToString(sum[:]) != f.challenge {
			fail(400, "invalid_grant")
			return
		}
	case "refresh_token":
		f.refreshes++
		if r.Form.Get("refresh_token") != f.refresh {
			fail(400, "invalid_grant")
			return
		}
	default:
		fail(400, "unsupported_grant_type")
		return
	}
	f.n++
	f.access = fmt.Sprintf("acc-%d", f.n+1)
	out := map[string]any{"access_token": f.access, "expires_in": 3600}
	if r.Form.Get("grant_type") == "authorization_code" || f.rotate {
		f.refresh = fmt.Sprintf("ref-%d", f.n+1)
		out["refresh_token"] = f.refresh
	}
	_ = json.NewEncoder(w).Encode(out)
}

func (f *fakeDropi) mcp(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.Header.Get("Authorization") != "Bearer "+f.access {
		w.WriteHeader(401)
		return
	}
	var msg struct {
		ID     *int            `json:"id"`
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	_ = json.NewDecoder(r.Body).Decode(&msg)
	if msg.Method == "initialize" {
		f.inits++
		w.Header().Set("Mcp-Session-Id", fmt.Sprintf("sess-%d", f.inits))
	} else if f.dropSess {
		f.dropSess = false
		w.WriteHeader(404)
		return
	} else if r.Header.Get("Mcp-Session-Id") == "" {
		w.WriteHeader(400)
		return
	}
	if msg.ID == nil {
		w.WriteHeader(202)
		return
	}
	var result any
	switch msg.Method {
	case "initialize":
		result = map[string]any{"protocolVersion": mcpProtocol, "capabilities": map[string]any{}}
	case "tools/list":
		result = map[string]any{"tools": f.tools}
	case "tools/call":
		var p struct {
			Name string         `json:"name"`
			Args map[string]any `json:"arguments"`
		}
		_ = json.Unmarshal(msg.Params, &p)
		f.calls = append(f.calls, p.Name)
		f.lastArgs = p.Args
		txt, ok := f.results[p.Name]
		if !ok {
			result = map[string]any{"isError": true, "content": []map[string]any{{"type": "text", "text": "no existe"}}}
		} else {
			result = map[string]any{"content": []map[string]any{{"type": "text", "text": txt}}}
		}
	}
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": *msg.ID, "result": result})
	if f.sse {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "event: message\ndata: %s\n\n", body)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(body)
}

// login runs the whole OAuth dance against the fake and returns the session.
func (f *fakeDropi) login(t *testing.T) (OAuthEndpoints, string, Tokens) {
	t.Helper()
	ctx := context.Background()
	ep, err := Discover(ctx, nil, f.mcpURL())
	if err != nil {
		t.Fatal(err)
	}
	if ep.Token != f.srv.URL+"/token" || ep.Authorize != f.srv.URL+"/authorize" || ep.Revoke == "" {
		t.Fatalf("endpoints: %+v", ep)
	}
	cid, err := RegisterClient(ctx, nil, ep.Register, "http://localhost/cb", "Motor")
	if err != nil || cid != "cid-1" {
		t.Fatalf("register: %q %v", cid, err)
	}
	verifier, challenge := NewPKCE()
	f.mu.Lock()
	f.challenge = challenge
	f.mu.Unlock()
	u, _ := url.Parse(AuthorizeURL(ep, cid, "http://localhost/cb", challenge, "st", f.mcpURL()))
	q := u.Query()
	if q.Get("code_challenge_method") != "S256" || q.Get("state") != "st" || q.Get("client_id") != cid || q.Get("resource") != f.mcpURL() {
		t.Fatalf("authorize url: %s", u)
	}
	tok, err := ExchangeCode(ctx, nil, ep.Token, cid, "http://localhost/cb", "the-code", verifier, f.mcpURL())
	if err != nil {
		t.Fatal(err)
	}
	return ep, cid, tok
}

func (f *fakeDropi) api(t *testing.T, tok Tokens, saved *[]Tokens) *MCPAPI {
	t.Helper()
	m := NewMCPAPI(MCPConfig{URL: f.mcpURL(), ClientID: "cid-1", TokenURL: f.srv.URL + "/token", Tokens: tok,
		OnTokens: func(t Tokens) {
			if saved != nil {
				*saved = append(*saved, t)
			}
		}})
	return m
}

func TestOAuthPKCEAndJSONTools(t *testing.T) {
	f := newFake(t)
	_, _, tok := f.login(t)
	if tok.Access == "" || tok.Refresh == "" || tok.Expires.IsZero() {
		t.Fatalf("tokens: %+v", tok)
	}
	// a wrong verifier must be refused by the server (PKCE is really checked)
	if _, err := ExchangeCode(context.Background(), nil, f.srv.URL+"/token", "cid-1", "x", "the-code", "wrong-verifier", ""); err == nil {
		t.Fatal("a wrong PKCE verifier must fail")
	}
	m := f.api(t, tok, nil)
	n, err := m.Ping(context.Background())
	if err != nil || n != 3 {
		t.Fatalf("ping: %d %v", n, err)
	}
	if m.Kind() != "mcp" {
		t.Fatal(m.Kind())
	}
}

func TestSSEAnswers(t *testing.T) {
	f := newFake(t)
	f.sse = true
	_, _, tok := f.login(t)
	m := f.api(t, tok, nil)
	if n, err := m.Ping(context.Background()); err != nil || n != 3 {
		t.Fatalf("sse ping: %d %v", n, err)
	}
	qs, err := m.Quote(context.Background(), QuoteInput{COD: true, Amount: 89900, OriginDANE: "05001000", DestDANE: "11001000"})
	if err != nil || len(qs) != 2 {
		t.Fatalf("sse quote: %v %v", qs, err)
	}
}

func TestRefreshRotationAndPersist(t *testing.T) {
	f := newFake(t)
	f.rotate = true
	_, _, tok := f.login(t)
	tok.Expires = time.Now().Add(-time.Minute) // already expired: must renew before calling
	var saved []Tokens
	m := f.api(t, tok, &saved)
	if _, err := m.Ping(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(saved) != 1 || saved[0].Access == tok.Access || saved[0].Refresh == tok.Refresh {
		t.Fatalf("a renewed, rotated session must be reported to be saved: %+v", saved)
	}
	if m.Tokens().Access != saved[0].Access {
		t.Fatal("client must use the renewed token")
	}
}

func TestRefreshKeepsOldRefreshWhenNotRotated(t *testing.T) {
	f := newFake(t)
	_, _, tok := f.login(t)
	got, err := RefreshTokens(context.Background(), nil, f.srv.URL+"/token", "cid-1", tok.Refresh)
	if err != nil || got.Refresh != tok.Refresh || got.Access == "" {
		t.Fatalf("%+v %v", got, err)
	}
	if _, err := RefreshTokens(context.Background(), nil, f.srv.URL+"/token", "cid-1", ""); !errors.Is(err, ErrNeedLogin) {
		t.Fatalf("no refresh token must mean login again: %v", err)
	}
}

func TestExpiredLoginAsksForNewLogin(t *testing.T) {
	f := newFake(t)
	_, _, tok := f.login(t)
	f.mu.Lock()
	f.refresh = "revoked" // Dropi no longer knows our refresh token
	f.access = "other"    // and the access token is dead too
	f.mu.Unlock()
	flagged := 0
	m := f.api(t, tok, nil)
	m.NeedLogin = func() { flagged++ }
	_, err := m.Ping(context.Background())
	if !errors.Is(err, ErrNeedLogin) || flagged == 0 {
		t.Fatalf("want ErrNeedLogin and the flag raised, got %v (flag %d)", err, flagged)
	}
}

func TestLostSessionStartsANewOne(t *testing.T) {
	f := newFake(t)
	_, _, tok := f.login(t)
	m := f.api(t, tok, nil)
	if _, err := m.Ping(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.dropSess = true
	f.mu.Unlock()
	if _, err := m.Tools(context.Background()); err != nil {
		t.Fatalf("a lost session must be recovered: %v", err)
	}
	if f.inits != 2 {
		t.Fatalf("expected a second initialize, got %d", f.inits)
	}
}

func TestQuoteFillsFieldsFromTheSchema(t *testing.T) {
	f := newFake(t)
	_, _, tok := f.login(t)
	m := f.api(t, tok, nil)
	qs, err := m.Quote(context.Background(), QuoteInput{COD: true, Amount: 89900, OriginDANE: "05001000", DestDANE: "11001000"})
	if err != nil || len(qs) != 2 || qs[0].Carrier != "ENVIA" || qs[0].Freight != 12900 {
		t.Fatalf("%v %v", qs, err)
	}
	a := f.lastArgs
	if a["codigo_dane_origen"] != "05001000" || a["codigo_dane_destino"] != "11001000" || a["con_recaudo"] != true || int64(a["valor"].(float64)) != 89900 {
		t.Fatalf("args: %v", a)
	}
}

func TestQuoteRefusesWhatItCannotFill(t *testing.T) {
	f := newFake(t)
	f.tools[0]["inputSchema"] = schema(map[string]any{
		"codigo_dane_destino": strField("DANE de destino"),
		"peso_kg":             map[string]any{"type": "number"},
	}, "codigo_dane_destino", "peso_kg")
	_, _, tok := f.login(t)
	m := f.api(t, tok, nil)
	_, err := m.Quote(context.Background(), QuoteInput{Amount: 1000, DestDANE: "11001000"})
	if err == nil || !strings.Contains(err.Error(), "peso") {
		t.Fatalf("must say which field it cannot fill: %v", err)
	}
	if len(f.calls) != 0 {
		t.Fatal("nothing may be sent when a required field is unknown")
	}
}

func TestProductCostIsNeverGuessed(t *testing.T) {
	f := newFake(t)
	_, _, tok := f.login(t)
	m := f.api(t, tok, nil)
	p, err := m.Product(context.Background(), "77")
	if err != nil || p.Cost != 21000 || p.SalePrice != 89900 || p.Name != "Gomitas" || p.Stock != 40 {
		t.Fatalf("%+v %v", p, err)
	}
	// only a retail price: the cost must stay 0 (the Motor then keeps the typed cost)
	f.results["detalle_producto"] = `{"objects":{"id":77,"name":"Gomitas","suggested_price":89900,"stock":4}}`
	p, err = m.Product(context.Background(), "77")
	if err != nil || p.Cost != 0 || p.SalePrice != 89900 {
		t.Fatalf("cost must not be derived from the sale price: %+v %v", p, err)
	}
}

func orderPayload() map[string]any {
	return map[string]any{
		"type": "FINAL_ORDER", "rate_type": "CON RECAUDO", "total_order": int64(89900),
		"name": "Ana", "surname": "Pérez", "phone": "3001112233", "client_email": "a@b.co",
		"state": "Antioquia", "city": "Medellín", "dir": "Calle 1 # 2-3", "notes": "n", "distributionCompany": "ENVIA",
		"products": []map[string]any{{"id": "77", "price": int64(89900), "quantity": 1}},
	}
}

func TestCreateOrderThroughMCP(t *testing.T) {
	f := newFake(t)
	_, _, tok := f.login(t)
	m := f.api(t, tok, nil)
	res, err := m.CreateOrder(context.Background(), orderPayload())
	if err != nil || res.ID != "9001" || res.Tracking != "G-1" {
		t.Fatalf("%+v %v", res, err)
	}
	a := f.lastArgs
	if a["nombre"] != "Ana" || a["apellido"] != "Pérez" || a["telefono"] != "3001112233" || a["direccion"] != "Calle 1 # 2-3" {
		t.Fatalf("customer data: %v", a)
	}
	items, _ := a["productos"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["producto_id"] != "77" || items[0].(map[string]any)["cantidad"] != float64(1) {
		t.Fatalf("items: %v", a["productos"])
	}
}

func TestCreateOrderRefusals(t *testing.T) {
	// 1) the tool asks for something unknown: nothing is sent
	f := newFake(t)
	f.tools[2]["inputSchema"] = schema(map[string]any{
		"nombre": strField("nombre"), "telefono": strField("telefono"), "talla": strField("talla de camiseta"),
	}, "nombre", "telefono", "talla")
	_, _, tok := f.login(t)
	m := f.api(t, tok, nil)
	if _, err := m.CreateOrder(context.Background(), orderPayload()); err == nil || !strings.Contains(err.Error(), "talla") || len(f.calls) != 0 {
		t.Fatalf("unknown required field: %v (calls %v)", err, f.calls)
	}
	// 2) the tool has no place for the address: refuses (a real shipment would be wrong)
	f2 := newFake(t)
	f2.tools[2]["inputSchema"] = schema(map[string]any{"nombre": strField("nombre"), "telefono": strField("telefono")}, "nombre", "telefono")
	_, _, tok2 := f2.login(t)
	m2 := f2.api(t, tok2, nil)
	if _, err := m2.CreateOrder(context.Background(), orderPayload()); err == nil || len(f2.calls) != 0 {
		t.Fatalf("critical field without a place must refuse: %v (calls %v)", err, f2.calls)
	}
	// 3) Dropi answers but the order number cannot be read: warns to REVISAR
	f3 := newFake(t)
	f3.results["crear_orden"] = `{"ok":true}`
	_, _, tok3 := f3.login(t)
	m3 := f3.api(t, tok3, nil)
	if _, err := m3.CreateOrder(context.Background(), orderPayload()); err == nil || !strings.Contains(err.Error(), "REVISA") {
		t.Fatalf("unreadable answer must tell the owner to check Dropi: %v", err)
	}
}

func TestReportSaysWhatIsReady(t *testing.T) {
	f := newFake(t)
	_, _, tok := f.login(t)
	m := f.api(t, tok, nil)
	tools, rep, err := m.Report(context.Background())
	if err != nil || len(tools) != 3 || len(rep) != 3 {
		t.Fatalf("%d %d %v", len(tools), len(rep), err)
	}
	for _, r := range rep {
		if !r.Ready || r.Tool == "" {
			t.Errorf("job not ready: %+v", r)
		}
	}
	// no order tool: the report says so instead of failing
	f.tools = f.tools[:2]
	_, rep, err = m.Report(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	missing := 0
	for _, r := range rep {
		if !r.Ready && strings.Contains(r.Detail, "No encontré") {
			missing++
		}
	}
	if missing != 1 {
		t.Fatalf("expected exactly one missing job: %+v", rep)
	}
}
