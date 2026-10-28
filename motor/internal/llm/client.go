// Package llm is a minimal client for OpenAI-compatible chat completions with
// native tool calling. DeepSeek (https://api.deepseek.com, model deepseek-chat)
// speaks this protocol, so does OpenAI and most gateways; the Motor only
// depends on this contract.
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Message is one chat message in OpenAI wire format.
type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

// ToolCall is a function call requested by the model.
type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function FunctionCall `json:"function"`
}

// FunctionCall carries the function name and JSON arguments.
type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// Tool declares a function the model may call.
type Tool struct {
	Type     string      `json:"type"`
	Function FunctionDef `json:"function"`
}

// FunctionDef is a tool schema.
type FunctionDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

// Request is a chat completion request.
type Request struct {
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	Tools       []Tool    `json:"tools,omitempty"`
	Temperature *float64  `json:"temperature,omitempty"`
	MaxTokens   int       `json:"max_tokens,omitempty"`
}

// Usage reports token consumption.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// Response is the first choice of a completion.
type Response struct {
	Message      Message
	FinishReason string
	Usage        Usage
}

// Completer is what agents depend on (real client or a test fake).
type Completer interface {
	Complete(ctx context.Context, req Request) (Response, error)
}

// Client talks to an OpenAI-compatible endpoint.
type Client struct {
	BaseURL    string
	APIKey     string
	HTTP       *http.Client
	MaxRetries int
	// AllowNoKey lets keyless endpoints (a local Ollama) work.
	AllowNoKey bool
}

// NewClient builds a client with sane timeouts.
func NewClient(baseURL, apiKey string) *Client {
	return &Client{
		BaseURL:    strings.TrimRight(baseURL, "/"),
		APIKey:     apiKey,
		HTTP:       &http.Client{Timeout: 120 * time.Second},
		MaxRetries: 3,
	}
}

// ErrNoAPIKey means the LLM is not configured.
var ErrNoAPIKey = errors.New("la IA no está configurada: agrega un proveedor con su clave en Conexiones (o con el comando 'motor ia agregar')")

// authorize sets the auth header when a key exists.
func (c *Client) authorize(req *http.Request) {
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
}

// NormalizeBaseURL trims whitespace, trailing slashes and a pasted
// "/chat/completions" (or "/models") suffix, so users can paste whatever
// their provider's docs show.
func NormalizeBaseURL(u string) string {
	u = strings.TrimSpace(u)
	u = strings.TrimRight(u, "/")
	for _, suf := range []string{"/chat/completions", "/completions", "/models"} {
		if strings.HasSuffix(strings.ToLower(u), suf) {
			u = strings.TrimRight(u[:len(u)-len(suf)], "/")
		}
	}
	return u
}

// ValidateBaseURL checks the URL is an absolute http(s) address.
func ValidateBaseURL(u string) error {
	u = strings.TrimSpace(u)
	if u == "" {
		return errors.New("falta la dirección (URL) del proveedor")
	}
	pu, err := url.Parse(u)
	if err != nil || (pu.Scheme != "http" && pu.Scheme != "https") || pu.Host == "" {
		return errors.New("la dirección debe empezar por https:// (por ejemplo https://api.deepseek.com)")
	}
	return nil
}

// Complete sends one chat completion request, retrying 429/5xx with backoff.
func (c *Client) Complete(ctx context.Context, req Request) (Response, error) {
	if c.APIKey == "" && !c.AllowNoKey {
		return Response{}, ErrNoAPIKey
	}
	body, err := json.Marshal(req)
	if err != nil {
		return Response{}, err
	}
	endpoint := c.BaseURL + "/chat/completions"
	var lastErr error
	for attempt := 0; attempt <= c.MaxRetries; attempt++ {
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
		if err != nil {
			return Response{}, err
		}
		httpReq.Header.Set("Content-Type", "application/json")
		c.authorize(httpReq)
		resp, err := c.HTTP.Do(httpReq)
		if err != nil {
			lastErr = err
			if !sleepCtx(ctx, backoff(attempt, "")) {
				return Response{}, ctx.Err()
			}
			continue
		}
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
		resp.Body.Close()
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			lastErr = fmt.Errorf("IA HTTP %d: %s", resp.StatusCode, snippet(raw))
			if !sleepCtx(ctx, backoff(attempt, resp.Header.Get("Retry-After"))) {
				return Response{}, ctx.Err()
			}
			continue
		}
		if resp.StatusCode != http.StatusOK {
			return Response{}, fmt.Errorf("IA HTTP %d: %s", resp.StatusCode, snippet(raw))
		}
		var parsed struct {
			Choices []struct {
				Message      Message `json:"message"`
				FinishReason string  `json:"finish_reason"`
			} `json:"choices"`
			Usage Usage `json:"usage"`
			// Some gateways answer HTTP 200 with an error object, or with a
			// billing/quota notice dressed as an assistant message plus a
			// vendor marker. Either way it is NOT the model talking and must
			// never be forwarded to a customer.
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
			Gateway *struct {
				Code string `json:"code"`
			} `json:"x_genspark"`
		}
		if err := json.Unmarshal(raw, &parsed); err != nil {
			return Response{}, fmt.Errorf("respuesta de IA inválida: %w: %s", err, snippet(raw))
		}
		if parsed.Error != nil && parsed.Error.Message != "" {
			return Response{}, fmt.Errorf("la IA devolvió un error: %s", parsed.Error.Message)
		}
		if parsed.Gateway != nil && parsed.Gateway.Code != "" {
			msg := ""
			if len(parsed.Choices) > 0 {
				msg = parsed.Choices[0].Message.Content
			}
			return Response{}, fmt.Errorf("el proveedor de IA rechazó la solicitud (%s): %s", parsed.Gateway.Code, snippet([]byte(msg)))
		}
		if len(parsed.Choices) == 0 {
			return Response{}, fmt.Errorf("respuesta de IA vacía: %s", snippet(raw))
		}
		ch := parsed.Choices[0]
		return Response{Message: ch.Message, FinishReason: ch.FinishReason, Usage: parsed.Usage}, nil
	}
	return Response{}, lastErr
}

func backoff(attempt int, retryAfter string) time.Duration {
	if s, err := strconv.Atoi(strings.TrimSpace(retryAfter)); err == nil && s > 0 && s <= 60 {
		return time.Duration(s) * time.Second
	}
	d := time.Duration(1<<attempt) * time.Second
	if d > 15*time.Second {
		d = 15 * time.Second
	}
	return d
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

func snippet(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 400 {
		s = s[:400] + "…"
	}
	return s
}

// Func declares a tool.
func Func(name, description string, params map[string]any) Tool {
	return Tool{Type: "function", Function: FunctionDef{Name: name, Description: description, Parameters: params}}
}

// Obj builds a JSON schema object.
func Obj(props map[string]any, required ...string) map[string]any {
	if required == nil {
		required = []string{}
	}
	return map[string]any{"type": "object", "properties": props, "required": required}
}

// Str is a string property.
func Str(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }

// Enum is a string property restricted to values.
func Enum(desc string, values ...string) map[string]any {
	return map[string]any{"type": "string", "description": desc, "enum": values}
}

// ListModels asks the provider for its model ids (GET /models).
func (c *Client) ListModels(ctx context.Context) ([]string, error) {
	if c.APIKey == "" && !c.AllowNoKey {
		return nil, ErrNoAPIKey
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/models", nil)
	if err != nil {
		return nil, err
	}
	c.authorize(req)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("IA HTTP %d: %s", resp.StatusCode, snippet(raw))
	}
	var parsed struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
		Models []struct {
			Name string `json:"name"`
			ID   string `json:"id"`
		} `json:"models"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("respuesta de IA inválida: %w", err)
	}
	var out []string
	for _, d := range parsed.Data {
		if d.ID != "" {
			out = append(out, d.ID)
		}
	}
	for _, m := range parsed.Models {
		n := m.ID
		if n == "" {
			n = strings.TrimPrefix(m.Name, "models/")
		}
		if n != "" {
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out, nil
}

// CheckResult is the outcome of a real, tiny connection test.
type CheckResult struct {
	Model     string `json:"modelo"`
	Reply     string `json:"respuesta"`
	LatencyMs int64  `json:"latencia_ms"`
	Tokens    int    `json:"tokens"`
}

// Check sends a tiny real request (no retries) to prove URL, key and model work.
func (c *Client) Check(ctx context.Context, model string) (CheckResult, error) {
	cc := *c
	cc.MaxRetries = 0
	start := time.Now()
	resp, err := cc.Complete(ctx, Request{
		Model:     model,
		Messages:  []Message{{Role: "user", Content: "Responde solo con la palabra: listo"}},
		MaxTokens: 16,
	})
	if err != nil {
		return CheckResult{}, err
	}
	return CheckResult{
		Model:     model,
		Reply:     strings.TrimSpace(resp.Message.Content),
		LatencyMs: time.Since(start).Milliseconds(),
		Tokens:    resp.Usage.TotalTokens,
	}, nil
}
