package llm

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNormalizeBaseURL(t *testing.T) {
	cases := map[string]string{
		"https://api.deepseek.com/":                                "https://api.deepseek.com",
		"https://api.x.com/v1/chat/completions":                    "https://api.x.com/v1",
		"  https://api.x.com/v1/chat/completions/  ":               "https://api.x.com/v1",
		"https://api.x.com/v1/models":                              "https://api.x.com/v1",
		"https://generativelanguage.googleapis.com/v1beta/openai/": "https://generativelanguage.googleapis.com/v1beta/openai",
	}
	for in, want := range cases {
		if got := NormalizeBaseURL(in); got != want {
			t.Errorf("NormalizeBaseURL(%q)=%q want %q", in, got, want)
		}
	}
}

func TestValidateBaseURL(t *testing.T) {
	for _, bad := range []string{"", "api.x.com", "ftp://x.com", "https://"} {
		if ValidateBaseURL(bad) == nil {
			t.Errorf("%q should be invalid", bad)
		}
	}
	if err := ValidateBaseURL("https://api.arxi.example/v1"); err != nil {
		t.Fatal(err)
	}
	if err := ValidateBaseURL("http://localhost:11434/v1"); err != nil {
		t.Fatal(err)
	}
}

func TestListModelsOpenAIAndGeminiShapes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" || r.Header.Get("Authorization") != "Bearer k" {
			t.Errorf("bad request %s %q", r.URL.Path, r.Header.Get("Authorization"))
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"b-model"},{"id":"a-model"}],"models":[{"name":"models/gem-1"}]}`))
	}))
	defer srv.Close()
	got, err := NewClient(srv.URL, "k").ListModels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, ",") != "a-model,b-model,gem-1" {
		t.Fatalf("%v", got)
	}
}

func TestListModelsErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		_, _ = w.Write([]byte(`{"error":"bad key"}`))
	}))
	defer srv.Close()
	_, err := NewClient(srv.URL, "k").ListModels(context.Background())
	if err == nil || !strings.Contains(Friendly(err), "rechazó la clave") {
		t.Fatalf("got %v / %q", err, Friendly(err))
	}
	if _, err := NewClient(srv.URL, "").ListModels(context.Background()); !errors.Is(err, ErrNoAPIKey) {
		t.Fatalf("expected ErrNoAPIKey, got %v", err)
	}
}

func TestCheckReportsModelAndLatency(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":" listo "},"finish_reason":"stop"}],"usage":{"total_tokens":9}}`))
	}))
	defer srv.Close()
	res, err := NewClient(srv.URL, "k").Check(context.Background(), "m1")
	if err != nil {
		t.Fatal(err)
	}
	if res.Model != "m1" || res.Reply != "listo" || res.Tokens != 9 {
		t.Fatalf("%+v", res)
	}
}

func TestNoKeyProviderSendsNoAuthHeader(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Errorf("unexpected auth header %q", r.Header.Get("Authorization"))
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`))
	}))
	defer srv.Close()
	c := NewClient(srv.URL, "")
	c.AllowNoKey = true
	if _, err := c.Check(context.Background(), "llama"); err != nil {
		t.Fatal(err)
	}
}

func TestSwitchSwapsProvider(t *testing.T) {
	mk := func(reply string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"` + reply + `"}}]}`))
		}))
	}
	a, b := mk("A"), mk("B")
	defer a.Close()
	defer b.Close()
	sw := &Switch{}
	if sw.Ready() {
		t.Fatal("empty switch must not be ready")
	}
	if _, err := sw.Complete(context.Background(), Request{}); !errors.Is(err, ErrNoAPIKey) {
		t.Fatalf("got %v", err)
	}
	sw.Set(NewClient(a.URL, "k"), "m", nil)
	r, _ := sw.Complete(context.Background(), Request{})
	if r.Message.Content != "A" || !sw.Ready() || sw.Model() != "m" {
		t.Fatalf("%+v", r)
	}
	sw.Set(NewClient(b.URL, "k"), "m2", nil)
	r, _ = sw.Complete(context.Background(), Request{})
	if r.Message.Content != "B" || sw.Model() != "m2" {
		t.Fatalf("%+v", r)
	}
	sw.Set(nil, "", nil)
	if sw.Ready() {
		t.Fatal("cleared switch must not be ready")
	}
}

func TestFriendlyMessages(t *testing.T) {
	cases := map[string]string{
		"IA HTTP 401: x":                "clave",
		"IA HTTP 402: Insufficient":     "saldo",
		"IA HTTP 404: model not found":  "modelo",
		"IA HTTP 404: page":             "compatible con OpenAI",
		"IA HTTP 429: slow":             "limitando",
		"IA HTTP 503: down":             "temporal",
		"dial tcp: no such host":        "conectar",
		"respuesta de IA inválida: eof": "formato",
	}
	for in, want := range cases {
		if got := Friendly(errors.New(in)); !strings.Contains(got, want) {
			t.Errorf("Friendly(%q)=%q, want it to mention %q", in, got, want)
		}
	}
	if got := Friendly(context.DeadlineExceeded); !strings.Contains(got, "tardó") {
		t.Errorf("timeout: %q", got)
	}
	if Friendly(nil) != "" {
		t.Error("nil must give empty")
	}
}
