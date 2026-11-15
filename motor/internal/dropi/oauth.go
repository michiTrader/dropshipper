package dropi

// OAuth 2.1 helpers for Dropi's official MCP server: discovery, dynamic client
// registration (public client), authorization code + PKCE, refresh and revoke.

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// MCPEndpoint is Dropi's official MCP server. A variable so tests can point it
// to a fake server.
var MCPEndpoint = "https://mcp.dropi.co/mcp"

// ErrNeedLogin means the saved session is gone or expired and the owner must
// press "Conectar con Dropi" again.
var ErrNeedLogin = errors.New("la sesión con Dropi venció o fue revocada: vuelve a pulsar «Conectar con Dropi» (Conexiones > Dropi)")

// OAuthEndpoints are the URLs of Dropi's authorization server.
type OAuthEndpoints struct {
	Issuer    string
	Authorize string
	Token     string
	Register  string
	Revoke    string
}

func httpClient(hc *http.Client) *http.Client {
	if hc != nil {
		return hc
	}
	return &http.Client{Timeout: 20 * time.Second}
}

func getJSON(ctx context.Context, hc *http.Client, u string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := httpClient(hc).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s respondió %d", u, resp.StatusCode)
	}
	return json.Unmarshal(raw, out)
}

// Discover finds the authorization server of an MCP server following the MCP
// authorization spec: protected-resource metadata first, then the server's own
// metadata.
func Discover(ctx context.Context, hc *http.Client, mcpURL string) (OAuthEndpoints, error) {
	u, err := url.Parse(mcpURL)
	if err != nil || u.Host == "" {
		return OAuthEndpoints{}, fmt.Errorf("dirección del MCP inválida: %q", mcpURL)
	}
	base := u.Scheme + "://" + u.Host
	var res struct {
		AuthServers []string `json:"authorization_servers"`
	}
	var lastErr error
	for _, p := range []string{"/.well-known/oauth-protected-resource" + u.Path, "/.well-known/oauth-protected-resource"} {
		if err := getJSON(ctx, hc, base+p, &res); err == nil && len(res.AuthServers) > 0 {
			lastErr = nil
			break
		} else if err != nil {
			lastErr = err
		}
	}
	if len(res.AuthServers) == 0 {
		if lastErr == nil {
			lastErr = errors.New("no indica servidor de autorización")
		}
		return OAuthEndpoints{}, fmt.Errorf("no pude descubrir cómo iniciar sesión en el MCP de Dropi: %w", lastErr)
	}
	issuer := strings.TrimRight(res.AuthServers[0], "/")
	var meta struct {
		Authorize string `json:"authorization_endpoint"`
		Token     string `json:"token_endpoint"`
		Register  string `json:"registration_endpoint"`
		Revoke    string `json:"revocation_endpoint"`
	}
	var err2 error
	for _, p := range []string{"/.well-known/oauth-authorization-server", "/.well-known/openid-configuration"} {
		if err2 = getJSON(ctx, hc, issuer+p, &meta); err2 == nil && meta.Authorize != "" && meta.Token != "" {
			break
		}
	}
	if meta.Authorize == "" || meta.Token == "" {
		return OAuthEndpoints{}, fmt.Errorf("el servidor de autorización de Dropi no publicó sus direcciones: %v", err2)
	}
	return OAuthEndpoints{Issuer: issuer, Authorize: meta.Authorize, Token: meta.Token, Register: meta.Register, Revoke: meta.Revoke}, nil
}

// RegisterClient registers the Motor as an OAuth public client (RFC 7591) for
// one redirect address and returns its client_id.
func RegisterClient(ctx context.Context, hc *http.Client, registerURL, redirectURI, name string) (string, error) {
	if registerURL == "" {
		return "", errors.New("Dropi no permite registrar aplicaciones nuevas")
	}
	body, _ := json.Marshal(map[string]any{
		"client_name": name, "redirect_uris": []string{redirectURI},
		"grant_types": []string{"authorization_code", "refresh_token"}, "response_types": []string{"code"},
		"token_endpoint_auth_method": "none",
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, registerURL, strings.NewReader(string(body)))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := httpClient(hc).Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var out struct {
		ClientID string `json:"client_id"`
		Error    string `json:"error"`
		Desc     string `json:"error_description"`
	}
	_ = json.Unmarshal(raw, &out)
	if resp.StatusCode/100 != 2 || out.ClientID == "" {
		return "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, firstText(out.Desc, out.Error, snippet(raw)))
	}
	return out.ClientID, nil
}

func firstText(v ...string) string {
	for _, s := range v {
		if strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s)
		}
	}
	return ""
}

// NewPKCE returns a code verifier and its S256 challenge.
func NewPKCE() (verifier, challenge string) {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	verifier = base64.RawURLEncoding.EncodeToString(b)
	sum := sha256.Sum256([]byte(verifier))
	return verifier, base64.RawURLEncoding.EncodeToString(sum[:])
}

// NewState returns an unguessable one-time value.
func NewState() string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// AuthorizeURL is the page the owner opens to log in at Dropi.
func AuthorizeURL(ep OAuthEndpoints, clientID, redirectURI, challenge, state, resource string) string {
	q := url.Values{
		"response_type": {"code"}, "client_id": {clientID}, "redirect_uri": {redirectURI},
		"code_challenge": {challenge}, "code_challenge_method": {"S256"}, "state": {state},
		"scope": {"mcp"}, "resource": {resource},
	}
	sep := "?"
	if strings.Contains(ep.Authorize, "?") {
		sep = "&"
	}
	return ep.Authorize + sep + q.Encode()
}

// Tokens is a saved Dropi session.
type Tokens struct {
	Access  string
	Refresh string
	Expires time.Time // zero = unknown
}

func tokenRequest(ctx context.Context, hc *http.Client, tokenURL string, form url.Values) (Tokens, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return Tokens{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := httpClient(hc).Do(req)
	if err != nil {
		return Tokens{}, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var out struct {
		Access  string `json:"access_token"`
		Refresh string `json:"refresh_token"`
		Expires int64  `json:"expires_in"`
		Error   string `json:"error"`
		Desc    string `json:"error_description"`
	}
	_ = json.Unmarshal(raw, &out)
	if resp.StatusCode/100 != 2 || out.Access == "" {
		if form.Get("grant_type") == "refresh_token" && (resp.StatusCode == 400 || resp.StatusCode == 401) {
			return Tokens{}, ErrNeedLogin
		}
		return Tokens{}, fmt.Errorf("Dropi no entregó la sesión (HTTP %d): %s", resp.StatusCode, firstText(out.Desc, out.Error, snippet(raw)))
	}
	t := Tokens{Access: out.Access, Refresh: out.Refresh}
	if out.Expires > 0 {
		t.Expires = time.Now().Add(time.Duration(out.Expires) * time.Second)
	}
	return t, nil
}

// ExchangeCode turns the code Dropi sent to the callback into a session.
func ExchangeCode(ctx context.Context, hc *http.Client, tokenURL, clientID, redirectURI, code, verifier, resource string) (Tokens, error) {
	return tokenRequest(ctx, hc, tokenURL, url.Values{
		"grant_type": {"authorization_code"}, "code": {code}, "client_id": {clientID},
		"redirect_uri": {redirectURI}, "code_verifier": {verifier}, "resource": {resource},
	})
}

// RefreshTokens renews a session. Dropi may rotate the refresh token: the new
// one (when present) replaces the old.
func RefreshTokens(ctx context.Context, hc *http.Client, tokenURL, clientID, refresh string) (Tokens, error) {
	if strings.TrimSpace(refresh) == "" {
		return Tokens{}, ErrNeedLogin
	}
	t, err := tokenRequest(ctx, hc, tokenURL, url.Values{
		"grant_type": {"refresh_token"}, "refresh_token": {refresh}, "client_id": {clientID},
	})
	if err != nil {
		return t, err
	}
	if t.Refresh == "" {
		t.Refresh = refresh
	}
	return t, nil
}

// Revoke asks Dropi to invalidate a session (best effort: the Motor forgets it
// either way).
func Revoke(ctx context.Context, hc *http.Client, revokeURL, clientID, token string) error {
	if revokeURL == "" || strings.TrimSpace(token) == "" {
		return nil
	}
	form := url.Values{"token": {token}, "client_id": {clientID}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, revokeURL, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := httpClient(hc).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	return nil
}
