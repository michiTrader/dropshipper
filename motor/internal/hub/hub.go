// Package hub owns the live integrations (AI, WhatsApp, Wompi) and rebuilds
// them whenever the settings change, from the panel or from the terminal.
package hub

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/michiTrader/dropshipper/motor/internal/llm"
	"github.com/michiTrader/dropshipper/motor/internal/payments"
	"github.com/michiTrader/dropshipper/motor/internal/settings"
	"github.com/michiTrader/dropshipper/motor/internal/whatsapp"
)

// TestTimeout bounds every "probar conexión" call.
const TestTimeout = 25 * time.Second

// Hub holds the integrations built from the current settings.
type Hub struct {
	AI *llm.Switch

	mu    sync.RWMutex
	cur   settings.Settings
	wa    *whatsapp.Client
	wompi *payments.Wompi
}

// New creates an empty hub.
func New() *Hub { return &Hub{AI: &llm.Switch{}} }

// Apply rebuilds the integrations from settings.
func (h *Hub) Apply(s settings.Settings) {
	h.mu.Lock()
	h.cur = s
	h.wa, h.wompi = nil, nil
	if s.WhatsApp.Enabled() {
		h.wa = whatsapp.New(s.WhatsApp.Token, s.WhatsApp.PhoneNumberID, s.WhatsApp.GraphVersion)
	}
	if s.Wompi.Enabled() {
		h.wompi = payments.NewWompi(s.Wompi.PrivateKey, s.Wompi.EventsSecret, s.Wompi.Sandbox)
	}
	h.mu.Unlock()

	if p, ok := s.Active(); ok && p.Ready() {
		c := llm.NewClient(p.BaseURL, p.APIKey)
		c.AllowNoKey = p.NoKey
		h.AI.Set(c, p.Model, p.Temperature)
	} else {
		h.AI.Set(nil, "", nil)
	}
}

// Settings returns the settings last applied.
func (h *Hub) Settings() settings.Settings {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.cur
}

// WhatsApp returns the live client (nil when not configured).
func (h *Hub) WhatsApp() *whatsapp.Client {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.wa
}

// Wompi returns the live Wompi client (nil when not configured).
func (h *Hub) Wompi() *payments.Wompi {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.wompi
}

// Available reports whether WhatsApp sending is configured. It lets the
// conversation engine decide at call time instead of at start-up.
func (h *Hub) Available() bool { return h.WhatsApp() != nil }

// SendText sends through the current WhatsApp client.
func (h *Hub) SendText(ctx context.Context, to, text string) (string, error) {
	c := h.WhatsApp()
	if c == nil {
		return "", whatsapp.ErrNotConfigured
	}
	return c.SendText(ctx, to, text)
}

// MarkRead marks a message as read through the current client.
func (h *Hub) MarkRead(ctx context.Context, waID string, typing bool) error {
	c := h.WhatsApp()
	if c == nil {
		return whatsapp.ErrNotConfigured
	}
	return c.MarkRead(ctx, waID, typing)
}

// LinksAvailable reports whether automatic payment links can be created.
func (h *Hub) LinksAvailable() bool { return h.Wompi() != nil }

// CreateLink creates a Wompi payment link with the current client.
func (h *Hub) CreateLink(ctx context.Context, reference, description string, amount int64) (string, string, error) {
	w := h.Wompi()
	if w == nil {
		return "", "", errors.New("Wompi no está configurado")
	}
	return w.CreateLink(ctx, reference, description, amount)
}
