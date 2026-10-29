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

// v1Only mimics gateways (like apinex) that serve the API only under /v1 and
// answer 404 "Route ... not found" anywhere else.
func v1Only(t *testing.T, hits *[]string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*hits = append(*hits, r.Method+" "+r.URL.Path)
		switch r.URL.Path {
		case "/v1/chat/completions":
			_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"Hola"}}],"usage":{"total_tokens":3}}`))
		case "/v1/models":
			_, _ = w.Write([]byte(`{"data":[{"id":"free/deepseek-v4.1-flash"}]}`))
		default:
			w.WriteHeader(404)
			_, _ = w.Write([]byte(`{"message":"Route ` + r.Method + `:` + r.URL.Path + ` not found","error":"Not Found","statusCode":404}`))
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestCompleteFallsBackToV1AndRemembers(t *testing.T) {
	var hits []string
	srv := v1Only(t, &hits)
	c := NewClient(srv.URL+"/", "k")
	c.MaxRetries = 0
	for i := 0; i < 2; i++ {
		r, err := c.Complete(context.Background(), Request{Model: "m"})
		if err != nil || r.Message.Content != "Hola" {
			t.Fatalf("call %d: %v %+v", i, err, r)
		}
	}
	if got := strings.Join(hits, ","); got != "POST /chat/completions,POST /v1/chat/completions,POST /v1/chat/completions" {
		t.Fatalf("the working URL must be remembered, hits=%s", got)
	}
	if c.EffectiveBaseURL() != srv.URL+"/v1" {
		t.Fatalf("effective=%q", c.EffectiveBaseURL())
	}
}

func TestListModelsAndCheckFallBackToV1(t *testing.T) {
	var hits []string
	srv := v1Only(t, &hits)
	c := NewClient(srv.URL, "k")
	c.MaxRetries = 0
	got, err := c.ListModels(context.Background())
	if err != nil || len(got) != 1 || got[0] != "free/deepseek-v4.1-flash" {
		t.Fatalf("%v %v", got, err)
	}
	c2 := NewClient(srv.URL, "k")
	res, err := c2.Check(context.Background(), "m")
	if err != nil || res.BaseURL != srv.URL+"/v1" || res.Reply != "Hola" {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestNoV1FallbackWhenVersionAlreadyPresentOrNot404(t *testing.T) {
	var hits []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits = append(hits, r.URL.Path)
		w.WriteHeader(404)
	}))
	defer srv.Close()
	c := NewClient(srv.URL+"/v2", "k")
	c.MaxRetries = 0
	_, err := c.Complete(context.Background(), Request{Model: "m"})
	if err == nil || len(hits) != 1 {
		t.Fatalf("err=%v hits=%v (must not append /v1 after /v2)", err, hits)
	}
	// 401 on the bare URL is a real answer: no second attempt.
	hits = nil
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits = append(hits, r.URL.Path)
		w.WriteHeader(401)
	}))
	defer srv2.Close()
	c2 := NewClient(srv2.URL, "k")
	c2.MaxRetries = 0
	if _, err := c2.Complete(context.Background(), Request{Model: "m"}); err == nil || len(hits) != 1 {
		t.Fatalf("err=%v hits=%v", err, hits)
	}
}

func TestFriendlyRouteNotFoundMentionsV1(t *testing.T) {
	got := Friendly(errors.New(`IA HTTP 404: {"message":"Route POST:/chat/completions not found"}`))
	if !strings.Contains(got, "/v1") {
		t.Fatalf("%q", got)
	}
}

func TestFriendly402IncludesProviderMessage(t *testing.T) {
	got := Friendly(errors.New(`IA HTTP 402: {"error":{"message":"Daily check-in required to use free models.","type":"billing_error"}}`))
	if !strings.Contains(got, "saldo") || !strings.Contains(got, "Daily check-in required") {
		t.Fatalf("%q", got)
	}
}
