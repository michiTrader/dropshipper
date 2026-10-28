package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func serve(t *testing.T, status int, body string) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" || r.Header.Get("Authorization") != "Bearer k" {
			t.Errorf("unexpected request %s %s", r.URL.Path, r.Header.Get("Authorization"))
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	c := NewClient(srv.URL, "k")
	c.MaxRetries = 0
	return c
}

func TestCompleteParsesToolCalls(t *testing.T) {
	c := serve(t, 200, `{"choices":[{"message":{"role":"assistant","content":"","tool_calls":[{"id":"1","type":"function","function":{"name":"confirmar_pedido","arguments":"{}"}}]},"finish_reason":"tool_calls"}],"usage":{"total_tokens":12}}`)
	r, err := c.Complete(context.Background(), Request{Model: "deepseek-chat"})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Message.ToolCalls) != 1 || r.Message.ToolCalls[0].Function.Name != "confirmar_pedido" || r.Usage.TotalTokens != 12 {
		t.Fatalf("%+v", r)
	}
}

// A gateway notice delivered as HTTP 200 + assistant text must be an error,
// never a message a customer could receive. (Found in a real run.)
func TestGatewayNoticeIsAnError(t *testing.T) {
	c := serve(t, 200, `{"choices":[{"message":{"role":"assistant","content":"Free-plan credits can't be used..."},"finish_reason":"stop"}],"x_genspark":{"code":"free_plan_block"}}`)
	if _, err := c.Complete(context.Background(), Request{}); err == nil || !strings.Contains(err.Error(), "free_plan_block") {
		t.Fatalf("expected gateway error, got %v", err)
	}
}

func TestErrorObjectWith200IsAnError(t *testing.T) {
	c := serve(t, 200, `{"error":{"message":"Insufficient Balance"}}`)
	if _, err := c.Complete(context.Background(), Request{}); err == nil || !strings.Contains(err.Error(), "Insufficient Balance") {
		t.Fatalf("expected error, got %v", err)
	}
}

func TestHTTPErrorNotRetriedOn4xx(t *testing.T) {
	c := serve(t, 401, `{"error":{"message":"Authentication Fails"}}`)
	if _, err := c.Complete(context.Background(), Request{}); err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("expected 401, got %v", err)
	}
}

func TestNoAPIKey(t *testing.T) {
	if _, err := NewClient("http://x", "").Complete(context.Background(), Request{}); err != ErrNoAPIKey {
		t.Fatalf("got %v", err)
	}
}

func TestTemperatureOmittedWhenNil(t *testing.T) {
	b, _ := json.Marshal(Request{Model: "m"})
	if strings.Contains(string(b), "temperature") {
		t.Fatalf("temperature must be omitted: %s", b)
	}
}
