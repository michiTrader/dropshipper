// Package payments integrates Wompi (Grupo Bancolombia) for automatic
// advance-payment links: the customer pays with Nequi, Bancolombia, PSE or a
// card, Wompi calls our webhook, and the order moves on without anyone
// checking a screenshot.
//
// Until Wompi is configured, the Motor works in "manual" mode: it sends the
// owner's Bre-B key / Bancolombia account and the owner confirms the receipt
// in the panel.
package payments

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Wompi creates payment links and validates events.
type Wompi struct {
	PrivateKey   string
	EventsSecret string
	Sandbox      bool
	BaseURL      string // overridable in tests
	HTTP         *http.Client
}

// NewWompi builds a client for sandbox or production.
func NewWompi(privateKey, eventsSecret string, sandbox bool) *Wompi {
	base := "https://production.wompi.co/v1"
	if sandbox {
		base = "https://sandbox.wompi.co/v1"
	}
	return &Wompi{PrivateKey: privateKey, EventsSecret: eventsSecret, Sandbox: sandbox, BaseURL: base,
		HTTP: &http.Client{Timeout: 30 * time.Second}}
}

// CreateLink creates a single-use payment link for an exact amount (COP).
// Returns the link id (used as the reference to match the webhook) and URL.
func (w *Wompi) CreateLink(ctx context.Context, reference, description string, amount int64) (string, string, error) {
	if w.PrivateKey == "" {
		return "", "", errors.New("Wompi sin llave privada")
	}
	payload := map[string]any{
		"name":             truncate(description, 60),
		"description":      truncate(description+" · ref "+reference, 250),
		"single_use":       true,
		"collect_shipping": false,
		"currency":         "COP",
		"amount_in_cents":  amount * 100,
		"expires_at":       time.Now().Add(72 * time.Hour).UTC().Format(time.RFC3339),
	}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(w.BaseURL, "/")+"/payment_links", bytes.NewReader(body))
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Authorization", "Bearer "+w.PrivateKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := w.HTTP.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 300 {
		return "", "", fmt.Errorf("Wompi HTTP %d: %s", resp.StatusCode, truncate(string(raw), 300))
	}
	var out struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &out); err != nil || out.Data.ID == "" {
		return "", "", fmt.Errorf("respuesta de Wompi sin id: %s", truncate(string(raw), 300))
	}
	return out.Data.ID, "https://checkout.wompi.co/l/" + out.Data.ID, nil
}

// Event is a parsed, verified Wompi event.
type Event struct {
	Type          string
	TransactionID string
	Status        string // APPROVED, DECLINED, VOIDED, ERROR, PENDING
	AmountInCents int64
	Reference     string
	PaymentLinkID string
	Method        string
}

// ErrBadChecksum means the event was not signed by Wompi.
var ErrBadChecksum = errors.New("checksum de Wompi inválido")

// ParseEvent validates the checksum (SHA256 of the values listed in
// signature.properties + timestamp + events secret) and parses the event.
// See https://docs.wompi.co/en/docs/colombia/eventos/
func (w *Wompi) ParseEvent(body []byte, headerChecksum string) (Event, error) {
	var raw struct {
		Event     string         `json:"event"`
		Data      map[string]any `json:"data"`
		Timestamp int64          `json:"timestamp"`
		Signature struct {
			Properties []string `json:"properties"`
			Checksum   string   `json:"checksum"`
		} `json:"signature"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return Event{}, err
	}
	if w.EventsSecret == "" {
		return Event{}, errors.New("falta WOMPI_EVENTS_SECRET: no se aceptan eventos sin verificar")
	}
	var b strings.Builder
	for _, prop := range raw.Signature.Properties {
		b.WriteString(lookup(raw.Data, prop))
	}
	fmt.Fprintf(&b, "%d", raw.Timestamp)
	b.WriteString(w.EventsSecret)
	sum := sha256.Sum256([]byte(b.String()))
	got := strings.ToLower(firstNonEmpty(headerChecksum, raw.Signature.Checksum))
	if len(raw.Signature.Properties) == 0 || subtle.ConstantTimeCompare([]byte(hex.EncodeToString(sum[:])), []byte(got)) != 1 {
		return Event{}, ErrBadChecksum
	}
	ev := Event{Type: raw.Event}
	if tx, ok := raw.Data["transaction"].(map[string]any); ok {
		ev.TransactionID = str(tx["id"])
		ev.Status = str(tx["status"])
		ev.Reference = str(tx["reference"])
		ev.PaymentLinkID = str(tx["payment_link_id"])
		ev.Method = str(tx["payment_method_type"])
		if f, ok := tx["amount_in_cents"].(float64); ok {
			ev.AmountInCents = int64(f)
		}
	}
	return ev, nil
}

// lookup resolves "transaction.id" inside data.
func lookup(data map[string]any, path string) string {
	var cur any = data
	for _, part := range strings.Split(path, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return ""
		}
		cur = m[part]
	}
	return str(cur)
}

func str(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case float64:
		if t == float64(int64(t)) {
			return fmt.Sprintf("%d", int64(t))
		}
		return fmt.Sprintf("%v", t)
	case bool:
		return fmt.Sprintf("%t", t)
	}
	return fmt.Sprintf("%v", v)
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s)
		}
	}
	return ""
}
