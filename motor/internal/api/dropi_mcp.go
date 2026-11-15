package api

// "Conectar con Dropi": the owner logs in on Dropi's own page (OAuth 2.1 with
// PKCE) and the Motor keeps the session for Dropi's official MCP server. No
// token has to be copied by hand.

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/michiTrader/dropshipper/motor/internal/config"
	"github.com/michiTrader/dropshipper/motor/internal/dropi"
	"github.com/michiTrader/dropshipper/motor/internal/hub"
	"github.com/michiTrader/dropshipper/motor/internal/settings"
)

// loginTTL is how long the owner has to finish the login on Dropi's page.
const loginTTL = 15 * time.Minute

// pendingLogin is a login started from the panel and not yet finished.
type pendingLogin struct {
	verifier    string
	clientID    string
	redirectURI string
	ep          dropi.OAuthEndpoints
	created     time.Time
}

// mcpLogins keeps the logins in progress (in memory: they last minutes).
type mcpLogins struct {
	mu sync.Mutex
	m  map[string]pendingLogin
}

func (l *mcpLogins) add(state string, p pendingLogin) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.m == nil {
		l.m = map[string]pendingLogin{}
	}
	for k, v := range l.m { // forget the abandoned ones
		if time.Since(v.created) > loginTTL {
			delete(l.m, k)
		}
	}
	l.m[state] = p
}

// take returns the login for a state and forgets it: a state works once.
func (l *mcpLogins) take(state string) (pendingLogin, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	p, ok := l.m[state]
	delete(l.m, state)
	if !ok || time.Since(p.created) > loginTTL {
		return pendingLogin{}, false
	}
	return p, true
}

func (s *Server) dropiMCPRoutes(api *http.ServeMux) {
	api.HandleFunc("POST /api/dropi/mcp/conectar", s.mcpConnect)
	api.HandleFunc("POST /api/dropi/mcp/desconectar", s.mcpDisconnect)
	api.HandleFunc("GET /api/dropi/mcp/herramientas", s.mcpTools)
}

// mcpConnect starts the login: it finds Dropi's authorization server, registers
// the Motor there and returns the address of Dropi's login page.
func (s *Server) mcpConnect(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), hub.TestTimeout)
	defer cancel()
	redirect := origin(r) + "/dropi/callback"

	ep, err := dropi.Discover(ctx, nil, dropi.MCPEndpoint)
	if err != nil {
		writeJSON(w, 200, map[string]any{"ok": false, "error": "No pude hablar con Dropi: " + dropiAdvice(err)})
		return
	}
	clientID, err := dropi.RegisterClient(ctx, nil, ep.Register, redirect, "Dropshipper Motor")
	if err != nil {
		writeJSON(w, 200, map[string]any{"ok": false, "error": "Dropi no aceptó registrar el Motor: " + err.Error()})
		return
	}
	verifier, challenge := dropi.NewPKCE()
	state := dropi.NewState()
	s.logins.add(state, pendingLogin{verifier: verifier, clientID: clientID, redirectURI: redirect, ep: ep, created: time.Now()})
	writeJSON(w, 200, map[string]any{
		"ok":  true,
		"url": dropi.AuthorizeURL(ep, clientID, redirect, challenge, state, dropi.MCPEndpoint),
	})
}

// dropiCallback is where Dropi sends the browser back after the login. It is
// public on purpose (the browser arrives from another site); the one-use state
// created in mcpConnect is what proves the owner started it.
func (s *Server) dropiCallback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	p, ok := s.logins.take(q.Get("state"))
	if !ok {
		callbackPage(w, false, "Este enlace de inicio de sesión ya se usó o venció. Vuelve al panel y pulsa «Conectar con Dropi» otra vez.")
		return
	}
	if e := q.Get("error"); e != "" {
		callbackPage(w, false, "Dropi no completó el inicio de sesión: "+firstNonEmpty(q.Get("error_description"), e))
		return
	}
	code := q.Get("code")
	if code == "" {
		callbackPage(w, false, "Dropi no devolvió el código de acceso. Inténtalo de nuevo.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), hub.TestTimeout)
	defer cancel()
	tok, err := dropi.ExchangeCode(ctx, nil, p.ep.Token, p.clientID, p.redirectURI, code, p.verifier, dropi.MCPEndpoint)
	if err != nil {
		callbackPage(w, false, "No pude completar la conexión: "+dropiAdvice(err))
		return
	}
	err = s.Settings.Update(ctx, func(st *settings.Settings) error {
		st.Dropi.MCPClientID, st.Dropi.MCPTokenURL, st.Dropi.MCPRevokeURL = p.clientID, p.ep.Token, p.ep.Revoke
		st.Dropi.MCPAccess, st.Dropi.MCPRefresh, st.Dropi.MCPExpires = tok.Access, tok.Refresh, tok.Expires
		st.Dropi.MCPSince, st.Dropi.MCPStale = time.Now().UTC(), false
		st.Negocio.Dropi.Modo = "api"
		return nil
	})
	if err != nil {
		callbackPage(w, false, "Dropi aceptó el inicio de sesión pero no pude guardarlo: "+err.Error())
		return
	}
	callbackPage(w, true, "Dropi quedó conectado. Ya puedes volver al panel.")
}

func callbackPage(w http.ResponseWriter, ok bool, msg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	title, color := "Dropi conectado", "#15803d"
	if !ok {
		title, color = "No se pudo conectar Dropi", "#b91c1c"
		w.WriteHeader(http.StatusBadRequest)
	}
	refresh := ""
	if ok {
		refresh = `<meta http-equiv="refresh" content="3;url=/">`
	}
	fmt.Fprintf(w, `<!doctype html><html lang="es"><head><meta charset="utf-8">%s<meta name="viewport" content="width=device-width,initial-scale=1"><title>%s</title></head>`+
		`<body style="font-family:system-ui,sans-serif;max-width:32rem;margin:4rem auto;padding:0 1rem"><h1 style="color:%s">%s</h1><p>%s</p><p><a href="/">Volver al panel</a></p></body></html>`,
		refresh, html.EscapeString(title), color, html.EscapeString(title), html.EscapeString(msg))
}

// mcpDisconnect forgets the Dropi login (the REST token, if any, stays).
func (s *Server) mcpDisconnect(w http.ResponseWriter, r *http.Request) {
	s.revokeSession(r.Context(), s.Settings.Get().Dropi)
	if s.update(w, r, func(st *settings.Settings) error {
		st.Dropi = st.Dropi.ClearMCP()
		if !st.Dropi.Enabled() {
			st.Negocio.Dropi.Modo = "manual"
		}
		return nil
	}) {
		writeJSON(w, 200, map[string]any{"ok": true})
	}
}

// revokeSession tells Dropi to cancel the saved session (best effort: the
// owner is disconnecting either way).
func (s *Server) revokeSession(ctx context.Context, d config.DropiConfig) {
	if !d.UsesMCP() || strings.TrimSpace(d.MCPRevokeURL) == "" {
		return
	}
	tok := firstNonEmpty(d.MCPRefresh, d.MCPAccess)
	if c, ok := s.Hub.Dropi().(*dropi.MCPAPI); ok { // the live session may be newer
		if t := c.Tokens(); t.Refresh != "" {
			tok = t.Refresh
		}
	}
	rctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	_ = dropi.Revoke(rctx, nil, d.MCPRevokeURL, d.MCPClientID, tok)
}

// mcpTools lists what Dropi's MCP offers and whether the Motor knows how to
// use it for quoting, reading a product and creating an order.
func (s *Server) mcpTools(w http.ResponseWriter, r *http.Request) {
	c, ok := s.Hub.Dropi().(*dropi.MCPAPI)
	if !ok {
		writeJSON(w, 200, map[string]any{"ok": false, "error": "Conecta Dropi con tu inicio de sesión primero (Conectar con Dropi)."})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*hub.TestTimeout)
	defer cancel()
	tools, rep, err := c.Report(ctx)
	if err != nil {
		writeJSON(w, 200, map[string]any{"ok": false, "error": dropiAdvice(err)})
		return
	}
	type toolOut struct {
		Name        string `json:"nombre"`
		Description string `json:"descripcion"`
	}
	out := make([]toolOut, 0, len(tools))
	for _, t := range tools {
		out = append(out, toolOut{t.Name, t.Description})
	}
	writeJSON(w, 200, map[string]any{"ok": true, "herramientas": out, "informe": rep})
}
