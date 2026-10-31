package whatsweb

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"testing"
	"time"
)

func TestNilManagerIsSafe(t *testing.T) {
	var m *Manager
	m.Configure(true, nil)
	m.SetHandlers(nil, nil, nil)
	m.Close()
	if m.Enabled() || m.Available() {
		t.Fatal("nil manager must be off")
	}
	if m.Info().Estado != StateOff {
		t.Fatal("nil manager state must be off")
	}
	if _, err := m.SendText(context.Background(), "573001234567", "x"); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("got %v", err)
	}
	if err := m.MarkRead(context.Background(), "id", false); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("got %v", err)
	}
}

func newTestManager(t *testing.T) *Manager {
	t.Helper()
	m := New(t.TempDir(), t.TempDir(), log.New(io.Discard, "", 0))
	t.Cleanup(m.Close)
	return m
}

func TestOffByDefault(t *testing.T) {
	m := newTestManager(t)
	if m.Enabled() || m.Available() {
		t.Fatal("must start off")
	}
	in := m.Info()
	if in.Estado != StateOff || in.Vinculado || in.QR != "" {
		t.Fatalf("info: %+v", in)
	}
	if err := m.StartPairing(context.Background()); err == nil {
		t.Fatal("pairing must fail while the channel is off")
	}
	if _, err := m.SendText(context.Background(), "573001234567", "hola"); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("got %v", err)
	}
}

func TestSendRejectsBadNumber(t *testing.T) {
	m := newTestManager(t)
	if _, err := m.SendText(context.Background(), "12", "hola"); err == nil || errors.Is(err, ErrNotConnected) {
		t.Fatalf("want invalid number error, got %v", err)
	}
}

func TestMarkReadUnknownMessageIsNoop(t *testing.T) {
	m := newTestManager(t)
	if err := m.MarkRead(context.Background(), "desconocido", true); err != nil {
		t.Fatal(err)
	}
}

func TestRecentIsBounded(t *testing.T) {
	m := newTestManager(t)
	m.mu.Lock()
	for i := 0; i < recentKeep+50; i++ {
		m.rememberLocked(fmt.Sprintf("id%d", i), recentMsg{})
	}
	n, o := len(m.recent), len(m.order)
	_, first := m.recent["id0"]
	m.mu.Unlock()
	if n != recentKeep || o != recentKeep || first {
		t.Fatalf("recent=%d order=%d keepsOldest=%v", n, o, first)
	}
}

func TestConfigureOnOff(t *testing.T) {
	m := newTestManager(t)
	m.Configure(true, []string{"+57 300 111 2222"})
	if !m.Enabled() {
		t.Fatal("must be enabled")
	}
	deadline := time.Now().Add(5 * time.Second)
	for m.Info().Estado != StateUnlinked && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if got := m.Info().Estado; got != StateUnlinked {
		t.Fatalf("state = %s, want %s", got, StateUnlinked)
	}
	if m.Available() {
		t.Fatal("must not be available without a linked phone")
	}
	m.Configure(false, nil)
	if m.Enabled() || m.Info().Estado != StateOff {
		t.Fatalf("must be off again: %+v", m.Info())
	}
}

func TestDigits(t *testing.T) {
	if digits("+57 (300) 123-4567") != "573001234567" || digits("3001234567") != "573001234567" || digits("123@lid") != "123" {
		t.Fatal("digits normalisation failed")
	}
}
