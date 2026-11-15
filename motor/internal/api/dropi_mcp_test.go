package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/michiTrader/dropshipper/motor/internal/dropi"
)

// mcpFake is Dropi's OAuth + MCP server, just enough for the panel flow.
type mcpFake struct {
	srv *httptest.Server

	mu      sync.Mutex
	access  string
	refresh string
	n       int
	revoked []string
	inits   int
}

func newMCPFake(t *testing.T) *mcpFake {
	f := &mcpFake{access: "acc-1", refresh: "ref-1"}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/oauth-protected-resource/mcp", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"authorization_servers":["%s/iss"]}`, f.srv.URL)
	})
	mux.HandleFunc("/iss/.well-known/oauth-authorization-server", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"authorization_endpoint":"%[1]s/authorize","token_endpoint":"%[1]s/token","registration_endpoint":"%[1]s/register","revocation_endpoint":"%[1]s/revoke"}`, f.srv.URL)
	})
	mux.HandleFunc("/register", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(201)
		_, _ = w.Write([]byte(`{"client_id":"cid-1"}`))
	})
	mux.HandleFunc("/revoke", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		f.mu.Lock()
		f.revoked = append(f.revoked, r.Form.Get("token"))
		f.mu.Unlock()
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		f.mu.Lock()
		defer f.mu.Unlock()
		switch r.Form.Get("grant_type") {
		case "authorization_code":
			if r.Form.Get("code") != "the-code" {
				w.WriteHeader(400)
				_, _ = w.Write([]byte(`{"error":"invalid_grant","error_description":"código inválido"}`))
				return
			}
		case "refresh_token":
			if r.Form.Get("refresh_token") != f.refresh {
				w.WriteHeader(400)
				_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
				return
			}
		}
		f.n++
		f.access = fmt.Sprintf("acc-%d", f.n+1)
		f.refresh = fmt.Sprintf("ref-%d", f.n+1)
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": f.access, "refresh_token": f.refresh, "expires_in": 3600})
	})
	mux.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if r.Header.Get("Authorization") != "Bearer "+f.access {
			w.WriteHeader(401)
			return
		}
		var msg struct {
			ID     *int   `json:"id"`
			Method string `json:"method"`
		}
		_ = json.NewDecoder(r.Body).Decode(&msg)
		if msg.Method == "initialize" {
			f.inits++
			w.Header().Set("Mcp-Session-Id", "s")
		}
		if msg.ID == nil {
			w.WriteHeader(202)
			return
		}
		var result any
		switch msg.Method {
		case "initialize":
			result = map[string]any{"protocolVersion": "2025-03-26", "capabilities": map[string]any{}}
		case "tools/list":
			result = map[string]any{"tools": []map[string]any{
				{"name": "cotizar_envio", "description": "Cotiza el envío", "inputSchema": map[string]any{"type": "object",
					"properties": map[string]any{"codigo_dane_destino": map[string]any{"type": "string"}, "valor": map[string]any{"type": "number"}},
					"required":   []string{"codigo_dane_destino", "valor"}}},
				{"name": "detalle_producto", "description": "Detalle de producto", "inputSchema": map[string]any{"type": "object",
					"properties": map[string]any{"producto_id": map[string]any{"type": "string"}}, "required": []string{"producto_id"}}},
			}}
		}
		body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": *msg.ID, "result": result})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	old := dropi.MCPEndpoint
	dropi.MCPEndpoint = f.srv.URL + "/mcp"
	t.Cleanup(func() { dropi.MCPEndpoint = old })
	return f
}

// connect runs the panel flow (button + Dropi login) and returns the state used.
func (e *env) connect(t *testing.T, hdr ...string) string {
	t.Helper()
	r := e.do("POST", "/api/dropi/mcp/conectar", nil, hdr...)
	j := r.json(t)
	if r.Code != 200 || j["ok"] != true {
		t.Fatalf("conectar: %d %s", r.Code, r.Body)
	}
	u, err := url.Parse(j["url"].(string))
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if q.Get("code_challenge_method") != "S256" || q.Get("client_id") != "cid-1" || !strings.HasSuffix(q.Get("redirect_uri"), "/dropi/callback") {
		t.Fatalf("authorize url: %s", u)
	}
	return q.Get("state")
}

// callback is the browser coming back from Dropi (a stranger's address: public).
func (e *env) callback(query string) resp {
	req := httptest.NewRequest("GET", "/dropi/callback?"+query, nil)
	req.RemoteAddr = "203.0.113.9:4000"
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return resp{rec.Code, rec.Body.String()}
}

func TestMCPConnectAndCallback(t *testing.T) {
	f := newMCPFake(t)
	e := newEnv(t, "panel-secret") // the panel needs a token; Dropi's callback must not
	auth := []string{"Authorization", "Bearer panel-secret"}

	if r := e.do("POST", "/api/dropi/mcp/conectar", nil); r.Code != 401 {
		t.Fatalf("starting the login needs the panel token: %d", r.Code)
	}
	state := e.connect(t, auth...)

	r := e.callback("code=the-code&state=" + url.QueryEscape(state))
	if r.Code != 200 || !strings.Contains(r.Body, "Dropi conectado") {
		t.Fatalf("callback: %d %s", r.Code, r.Body)
	}
	d := e.set.Get().Dropi
	if !d.UsesMCP() || d.MCPClientID != "cid-1" || d.MCPAccess == "" || d.MCPRefresh == "" || d.MCPSince.IsZero() || e.set.Get().Negocio.Dropi.Modo != "api" {
		t.Fatalf("login not saved: %+v", d)
	}
	if e.srv.Hub.Dropi() == nil || e.srv.Hub.Dropi().Kind() != "mcp" {
		t.Fatal("the hub must now use the MCP")
	}
	// the state works once
	if r := e.callback("code=the-code&state=" + url.QueryEscape(state)); r.Code != 400 {
		t.Fatalf("a reused state must be refused: %d", r.Code)
	}
	// unknown state
	if r := e.callback("code=the-code&state=nope"); r.Code != 400 {
		t.Fatalf("unknown state: %d", r.Code)
	}
	// the panel shows it, and probar uses the login
	j := e.do("GET", "/api/ajustes", nil, auth...).json(t)
	dv := j["dropi"].(map[string]any)
	if dv["mcp_conectado"] != true || dv["mcp_necesita_login"] != false || dv["configurado"] != true {
		t.Fatalf("view: %v", dv)
	}
	pr := e.do("POST", "/api/dropi/probar", map[string]any{}, auth...).json(t)
	if pr["ok"] != true || !strings.Contains(pr["mensaje"].(string), "inicio de sesión") {
		t.Fatalf("probar: %v", pr)
	}
	_ = f
}

func TestMCPCallbackDropiError(t *testing.T) {
	newMCPFake(t)
	e := newEnv(t, "")
	state := e.connect(t)
	r := e.callback("error=access_denied&error_description=" + url.QueryEscape("el usuario canceló") + "&state=" + url.QueryEscape(state))
	if r.Code != 400 || !strings.Contains(r.Body, "canceló") || e.set.Get().Dropi.UsesMCP() {
		t.Fatalf("%d %s", r.Code, r.Body)
	}
	// a bad code is explained and nothing is saved
	state = e.connect(t)
	r = e.callback("code=bad&state=" + url.QueryEscape(state))
	if r.Code != 400 || e.set.Get().Dropi.UsesMCP() {
		t.Fatalf("%d %s", r.Code, r.Body)
	}
}

func TestMCPFormSaveKeepsLogin(t *testing.T) {
	newMCPFake(t)
	e := newEnv(t, "")
	e.callback("code=the-code&state=" + url.QueryEscape(e.connect(t)))
	r := e.do("PUT", "/api/ajustes/dropi", map[string]any{"token": "", "sandbox": false, "origen_departamento": "Antioquia", "origen_ciudad": "Medellín"})
	if r.Code != 200 {
		t.Fatalf("%d %s", r.Code, r.Body)
	}
	d := e.set.Get().Dropi
	if !d.UsesMCP() || d.OriginCity != "Medellín" {
		t.Fatalf("saving the form must not lose the login: %+v", d)
	}
}

func TestMCPRenewedSessionIsSaved(t *testing.T) {
	f := newMCPFake(t)
	e := newEnv(t, "")
	e.callback("code=the-code&state=" + url.QueryEscape(e.connect(t)))
	before := e.set.Get().Dropi

	// Dropi stops accepting the access token (it expired): the next call gets a
	// 401, renews with the refresh token and the new session is saved
	f.mu.Lock()
	f.access = "expired-on-dropi"
	f.mu.Unlock()
	if pr := e.do("POST", "/api/dropi/probar", map[string]any{}).json(t); pr["ok"] != true {
		t.Fatalf("probar: %v", pr)
	}
	after := e.set.Get().Dropi
	if after.MCPAccess == before.MCPAccess || after.MCPRefresh == before.MCPRefresh || !after.MCPExpires.After(time.Now()) || after.MCPStale {
		t.Fatalf("the renewed session must be persisted:\nbefore %+v\nafter  %+v", before, after)
	}
}

func TestMCPFailedRenewalAsksForLoginAndReconnectClears(t *testing.T) {
	f := newMCPFake(t)
	e := newEnv(t, "")
	e.callback("code=the-code&state=" + url.QueryEscape(e.connect(t)))

	f.mu.Lock()
	f.refresh, f.access = "revoked", "dead" // Dropi forgot us
	f.mu.Unlock()
	pr := e.do("POST", "/api/dropi/probar", map[string]any{}).json(t)
	if pr["ok"] != false || !strings.Contains(pr["error"].(string), "Conectar con Dropi") {
		t.Fatalf("probar: %v", pr)
	}
	if !e.set.Get().Dropi.MCPStale {
		t.Fatal("the saved login must be flagged as needing a new login")
	}
	dv := e.do("GET", "/api/ajustes", nil).json(t)["dropi"].(map[string]any)
	if dv["mcp_necesita_login"] != true || dv["mcp_conectado"] != false {
		t.Fatalf("view: %v", dv)
	}
	st := e.do("GET", "/api/estado", nil).json(t)["dropi"].(map[string]any)
	if st["mcp_necesita_login"] != true {
		t.Fatalf("status: %v", st)
	}
	// logging in again clears the flag
	e.callback("code=the-code&state=" + url.QueryEscape(e.connect(t)))
	if d := e.set.Get().Dropi; d.MCPStale || !d.UsesMCP() {
		t.Fatalf("reconnecting must clear the flag: %+v", d)
	}
}

func TestMCPToolsReport(t *testing.T) {
	newMCPFake(t)
	e := newEnv(t, "")
	if r := e.do("GET", "/api/dropi/mcp/herramientas", nil).json(t); r["ok"] != false {
		t.Fatalf("without a login: %v", r)
	}
	e.callback("code=the-code&state=" + url.QueryEscape(e.connect(t)))
	r := e.do("GET", "/api/dropi/mcp/herramientas", nil).json(t)
	if r["ok"] != true || len(r["herramientas"].([]any)) != 2 {
		t.Fatalf("%v", r)
	}
	rep := r["informe"].([]any)
	if len(rep) != 3 {
		t.Fatalf("the report must cover quoting, product and order: %v", rep)
	}
	ready, missing := 0, 0
	for _, x := range rep {
		m := x.(map[string]any)
		if m["lista"] == true {
			ready++
		} else if strings.Contains(m["detalle"].(string), "No encontré") {
			missing++
		}
	}
	if ready != 2 || missing != 1 { // this fake has no order tool
		t.Fatalf("report: %v", rep)
	}
}

func TestMCPDisconnectRevokesAndClears(t *testing.T) {
	f := newMCPFake(t)
	e := newEnv(t, "")
	e.callback("code=the-code&state=" + url.QueryEscape(e.connect(t)))
	if r := e.do("POST", "/api/dropi/mcp/desconectar", nil); r.Code != 200 {
		t.Fatalf("%d %s", r.Code, r.Body)
	}
	if d := e.set.Get().Dropi; d.UsesMCP() || d.MCPRefresh != "" || d.MCPAccess != "" {
		t.Fatalf("the login must be forgotten: %+v", d)
	}
	f.mu.Lock()
	n := len(f.revoked)
	f.mu.Unlock()
	if n != 1 {
		t.Fatalf("Dropi must be told to revoke the session: %d", n)
	}
	if e.srv.Hub.Dropi() != nil || e.set.Get().Negocio.Dropi.Modo != "manual" {
		t.Fatal("disconnected: no client, manual mode")
	}
}

func TestMCPWinsOverTokenAndTokenSurvivesDisconnect(t *testing.T) {
	newMCPFake(t)
	e := newEnv(t, "")
	if r := e.do("PUT", "/api/ajustes/dropi", map[string]any{"token": "tok-abc", "sandbox": false}); r.Code != 200 {
		t.Fatalf("%d %s", r.Code, r.Body)
	}
	if k := e.srv.Hub.Dropi().Kind(); k != "token" {
		t.Fatalf("only a token: %s", k)
	}
	e.callback("code=the-code&state=" + url.QueryEscape(e.connect(t)))
	if k := e.srv.Hub.Dropi().Kind(); k != "mcp" {
		t.Fatalf("the login must win over the token: %s", k)
	}
	e.do("POST", "/api/dropi/mcp/desconectar", nil)
	if c := e.srv.Hub.Dropi(); c == nil || c.Kind() != "token" {
		t.Fatal("after disconnecting the login the token must work again")
	}
	if e.set.Get().Negocio.Dropi.Modo != "api" {
		t.Fatal("still connected through the token: mode stays api")
	}
}
