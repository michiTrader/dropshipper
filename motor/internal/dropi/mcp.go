package dropi

// A small MCP client (Model Context Protocol, "streamable HTTP" transport):
// JSON-RPC 2.0 over POST, answered either as plain JSON or as a server-sent
// event stream. Only what the Motor needs: initialize, tools/list, tools/call.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"
)

const mcpProtocol = "2025-03-26"

// MCPConfig is everything a client needs. Tokens are renewed automatically and
// reported through OnTokens so the caller can save them.
type MCPConfig struct {
	URL      string
	ClientID string
	TokenURL string
	Tokens   Tokens
	OnTokens func(Tokens)
	HTTP     *http.Client
	// Logf (optional) receives one line per tool call, for debugging.
	Logf func(format string, args ...any)
}

// Tool is one tool the MCP server offers.
type Tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Schema      map[string]any `json:"inputSchema"`
}

// ToolResult is what a tool call returned.
type ToolResult struct {
	Text       string          // all text parts joined
	Structured json.RawMessage // structuredContent, when the server sends it
	IsError    bool
}

// JSON returns the best machine-readable payload of the result.
func (r ToolResult) JSON() []byte {
	if len(r.Structured) > 0 && string(r.Structured) != "null" {
		return r.Structured
	}
	t := strings.TrimSpace(r.Text)
	if t != "" && !json.Valid([]byte(t)) {
		if b, ok := toonToJSON(t); ok {
			return b
		}
	}
	return []byte(t)
}

// MCPError is an error the MCP server reported.
type MCPError struct {
	Code    int
	Message string
}

func (e *MCPError) Error() string { return "Dropi MCP: " + e.Message }

// MCPClient talks to an MCP server. Safe for concurrent use (calls are serialized).
type MCPClient struct {
	cfg MCPConfig

	tmu sync.RWMutex // guards tok (read from other goroutines)
	tok Tokens

	mu      sync.Mutex
	session string
	inited  bool
	nextID  int
	tools   []Tool
	toolsAt time.Time

	lmu  sync.Mutex
	logs []CallLog
}

// NewMCP builds a client.
func NewMCP(cfg MCPConfig) *MCPClient {
	if cfg.HTTP == nil {
		cfg.HTTP = &http.Client{Timeout: 45 * time.Second}
	}
	if cfg.URL == "" {
		cfg.URL = MCPEndpoint
	}
	return &MCPClient{cfg: cfg, tok: cfg.Tokens}
}

// Tokens returns the session currently in use. It never blocks on a running
// call, so a settings listener may read it while a refresh is being saved.
func (c *MCPClient) Tokens() Tokens {
	c.tmu.RLock()
	defer c.tmu.RUnlock()
	return c.tok
}

func (c *MCPClient) setTokens(t Tokens) {
	c.tmu.Lock()
	c.tok = t
	c.tmu.Unlock()
}

var errUnauthorized = errors.New("no autorizado")

// refresh renews the access token (caller holds c.mu).
func (c *MCPClient) refresh(ctx context.Context) error {
	t, err := RefreshTokens(ctx, c.cfg.HTTP, c.cfg.TokenURL, c.cfg.ClientID, c.Tokens().Refresh)
	if err != nil {
		return err
	}
	c.setTokens(t)
	if c.cfg.OnTokens != nil {
		c.cfg.OnTokens(t)
	}
	return nil
}

// post sends one JSON-RPC message. wantID > 0 waits for that response.
func (c *MCPClient) post(ctx context.Context, msg map[string]any, wantID int) (json.RawMessage, error) {
	body, err := json.Marshal(msg)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.URL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Authorization", "Bearer "+c.Tokens().Access)
	if c.session != "" {
		req.Header.Set("Mcp-Session-Id", c.session)
	}
	if c.inited {
		req.Header.Set("MCP-Protocol-Version", mcpProtocol)
	}
	resp, err := c.cfg.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if s := resp.Header.Get("Mcp-Session-Id"); s != "" {
		c.session = s
	}
	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		return nil, errUnauthorized
	case resp.StatusCode == http.StatusAccepted || resp.StatusCode == http.StatusNoContent:
		return nil, nil
	case resp.StatusCode/100 != 2:
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return nil, &APIError{Status: resp.StatusCode, Body: snippet(raw)}
	}
	if wantID == 0 {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
		return nil, nil
	}
	if strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") {
		return readSSE(resp.Body, wantID)
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	return decodeRPC(raw, wantID)
}

func readSSE(r io.Reader, wantID int) (json.RawMessage, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 8<<20)
	var data []string
	flush := func() (json.RawMessage, error, bool) {
		if len(data) == 0 {
			return nil, nil, false
		}
		payload := strings.Join(data, "\n")
		data = nil
		res, err := decodeRPC([]byte(payload), wantID)
		if err == errNotMine {
			return nil, nil, false
		}
		return res, err, true
	}
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			if res, err, ok := flush(); ok {
				return res, err
			}
			continue
		}
		if strings.HasPrefix(line, "data:") {
			data = append(data, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
	}
	if res, err, ok := flush(); ok {
		return res, err
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return nil, errors.New("Dropi MCP cerró la conexión sin responder")
}

var errNotMine = errors.New("mensaje de otra petición")

func decodeRPC(raw []byte, wantID int) (json.RawMessage, error) {
	var m struct {
		ID     *json.Number    `json:"id"`
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("respuesta del MCP inválida: %w (%s)", err, snippet(raw))
	}
	if m.ID == nil || m.ID.String() != fmt.Sprint(wantID) {
		return nil, errNotMine
	}
	if m.Error != nil {
		return nil, &MCPError{Code: m.Error.Code, Message: m.Error.Message}
	}
	return m.Result, nil
}

// send makes one request with automatic recovery: a 401 renews the token once
// and a lost session starts a new one once.
func (c *MCPClient) send(ctx context.Context, method string, params any) (json.RawMessage, error) {
	retriedAuth, retriedSession := false, false
	for {
		if tk := c.Tokens(); tk.Access == "" || (!tk.Expires.IsZero() && time.Until(tk.Expires) < time.Minute) {
			if err := c.refresh(ctx); err != nil {
				// The access token may still work for a few seconds.
				if tk.Access == "" || (!tk.Expires.IsZero() && time.Now().After(tk.Expires)) {
					return nil, err
				}
			}
		}
		if !c.inited && method != "initialize" {
			if err := c.initialize(ctx); err != nil {
				return nil, err
			}
		}
		c.nextID++
		id := c.nextID
		msg := map[string]any{"jsonrpc": "2.0", "id": id, "method": method}
		if params != nil {
			msg["params"] = params
		}
		res, err := c.post(ctx, msg, id)
		switch {
		case err == nil:
			return res, nil
		case errors.Is(err, errUnauthorized) && !retriedAuth:
			retriedAuth = true
			if rerr := c.refresh(ctx); rerr != nil {
				return nil, rerr
			}
		case errors.Is(err, errUnauthorized):
			return nil, ErrNeedLogin
		case isLostSession(err) && c.session != "" && !retriedSession:
			retriedSession = true
			c.session, c.inited = "", false
		default:
			return nil, err
		}
	}
}

func isLostSession(err error) bool {
	var ae *APIError
	if errors.As(err, &ae) {
		return ae.Status == http.StatusNotFound || ae.Status == http.StatusBadRequest && strings.Contains(strings.ToLower(ae.Body), "session")
	}
	return false
}

func (c *MCPClient) initialize(ctx context.Context) error {
	c.session, c.inited = "", false
	params := map[string]any{
		"protocolVersion": mcpProtocol, "capabilities": map[string]any{},
		"clientInfo": map[string]any{"name": "dropshipper-motor", "version": "1"},
	}
	for attempt := 0; ; attempt++ {
		c.nextID++
		id := c.nextID
		_, err := c.post(ctx, map[string]any{"jsonrpc": "2.0", "id": id, "method": "initialize", "params": params}, id)
		if errors.Is(err, errUnauthorized) && attempt == 0 {
			if rerr := c.refresh(ctx); rerr != nil {
				return rerr
			}
			continue
		}
		if errors.Is(err, errUnauthorized) {
			return ErrNeedLogin
		}
		if err != nil {
			return err
		}
		break
	}
	c.inited = true
	// The spec asks the client to confirm; a server may answer 202 with no body.
	_, _ = c.post(ctx, map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"}, 0)
	return nil
}

// Tools lists the tools (cached for 10 minutes unless force).
func (c *MCPClient) Tools(ctx context.Context, force bool) ([]Tool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !force && c.tools != nil && time.Since(c.toolsAt) < 10*time.Minute {
		return c.tools, nil
	}
	var all []Tool
	cursor := ""
	for page := 0; page < 10; page++ {
		var params any
		if cursor != "" {
			params = map[string]any{"cursor": cursor}
		}
		raw, err := c.send(ctx, "tools/list", params)
		if err != nil {
			return nil, err
		}
		var out struct {
			Tools      []Tool `json:"tools"`
			NextCursor string `json:"nextCursor"`
		}
		if err := json.Unmarshal(raw, &out); err != nil {
			return nil, fmt.Errorf("lista de herramientas inválida: %w", err)
		}
		all = append(all, out.Tools...)
		if out.NextCursor == "" {
			break
		}
		cursor = out.NextCursor
	}
	c.tools, c.toolsAt = all, time.Now()
	return all, nil
}

// Call runs a tool.
func (c *MCPClient) Call(ctx context.Context, name string, args map[string]any) (ToolResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if args == nil {
		args = map[string]any{}
	}
	started := time.Now()
	raw, err := c.send(ctx, "tools/call", map[string]any{"name": name, "arguments": args})
	if err != nil {
		c.record(name, args, "", err, started)
		return ToolResult{}, err
	}
	var out struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Structured json.RawMessage `json:"structuredContent"`
		IsError    bool            `json:"isError"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		c.record(name, args, string(raw), err, started)
		return ToolResult{}, fmt.Errorf("respuesta de la herramienta %s inválida: %w", name, err)
	}
	var texts []string
	for _, p := range out.Content {
		if p.Type == "text" || p.Type == "" {
			texts = append(texts, p.Text)
		}
	}
	res := ToolResult{Text: strings.Join(texts, "\n"), Structured: out.Structured, IsError: out.IsError}
	if res.IsError {
		merr := &MCPError{Message: firstText(snippet([]byte(res.Text)), "la herramienta "+name+" devolvió un error")}
		c.record(name, args, string(res.JSON()), merr, started)
		return res, merr
	}
	c.record(name, args, string(res.JSON()), nil, started)
	return res, nil
}

// CallLog is one tool call as the Motor made it: what it sent and what came
// back. It exists so a wrong or refused call can be understood from the panel.
type CallLog struct {
	At     time.Time `json:"hora"`
	Tool   string    `json:"herramienta"`
	Args   string    `json:"argumentos"`
	Result string    `json:"respuesta"`
	Err    string    `json:"error,omitempty"`
	Millis int64     `json:"ms"`
}

const maxCallLogs = 40

var secretRe = regexp.MustCompile(`(?i)(bearer\s+|"?(?:access_token|refresh_token|authorization|client_secret)"?\s*[:=]\s*"?)[A-Za-z0-9._\-~+/=]{8,}`)

// redact hides anything that looks like a credential.
func redact(s string) string { return secretRe.ReplaceAllString(s, "${1}[oculto]") }

func clip(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

func (c *MCPClient) record(tool string, args map[string]any, result string, err error, started time.Time) {
	ab, _ := json.Marshal(args)
	e := CallLog{At: started, Tool: tool, Args: clip(redact(string(ab)), 2000), Result: clip(redact(result), 3000), Millis: time.Since(started).Milliseconds()}
	if err != nil {
		e.Err = clip(redact(err.Error()), 500)
	}
	c.lmu.Lock()
	c.logs = append(c.logs, e)
	if len(c.logs) > maxCallLogs {
		c.logs = c.logs[len(c.logs)-maxCallLogs:]
	}
	c.lmu.Unlock()
	if c.cfg.Logf != nil {
		c.cfg.Logf("dropi-mcp %s args=%s ms=%d err=%q resp=%s", tool, e.Args, e.Millis, e.Err, clip(e.Result, 300))
	}
}

// CallLog returns the most recent calls, oldest first.
func (c *MCPClient) CallLog() []CallLog {
	c.lmu.Lock()
	defer c.lmu.Unlock()
	return append([]CallLog(nil), c.logs...)
}
