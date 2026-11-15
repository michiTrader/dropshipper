package convo

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/michiTrader/dropshipper/motor/internal/store"
)

// A message WhatsApp delivers hours late (the Motor was off when it was
// written) must not be answered once the conversation moved on.
func TestLateMessageIsStoredButNeverAnswered(t *testing.T) {
	r := newRig(t, manualNegocio())
	ctx := context.Background()
	phone := "573001112233"
	now := time.Now()

	// The conversation already happened: customer wrote, the AI answered.
	r.llm.push(text("Hola, claro que sí"))
	if err := r.eng.HandleInbound(ctx, Inbound{Phone: phone, Kind: "text", Text: "hola, quiero info", WAID: "A1", At: now.Add(-10 * time.Minute)}); err != nil {
		t.Fatal(err)
	}
	waitIdle(t, r, phone, 2)
	calls := len(r.llm.seen)

	// The very first message of the customer (written while the Motor was off)
	// arrives now, stamped two hours ago.
	if err := r.eng.HandleInbound(ctx, Inbound{Phone: phone, Kind: "text", Text: "precio?", WAID: "OLD", At: now.Add(-2 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond) // longer than the test debounce
	r.eng.mu.Lock()
	timers := len(r.eng.timers)
	r.eng.mu.Unlock()
	if timers != 0 || len(r.llm.seen) != calls {
		t.Fatalf("la IA respondió un mensaje tardío (timers=%d, llamadas %d→%d)", timers, calls, len(r.llm.seen))
	}
	ms, _ := r.st.Messages(ctx, phone, 0, 0)
	var late *store.Message
	for i := range ms {
		if ms[i].WAID == "OLD" {
			late = &ms[i]
		}
	}
	if late == nil || late.Status != store.MsgLate {
		t.Fatalf("el mensaje tardío debía guardarse marcado como tardío: %+v", late)
	}
	if alerts, _ := r.st.OpenAlerts(ctx); len(alerts) == 0 || alerts[0].Kind != store.AlertInfo {
		t.Fatalf("el dueño debía recibir un aviso: %+v", alerts)
	}
	if !sawAudit(t, r, phone, "mensaje_tardio") {
		t.Fatal("falta el registro de auditoría")
	}
	// The chat's last activity does not go backwards.
	c, _ := r.st.GetContact(ctx, phone)
	if c.LastInboundAt == nil || c.LastInboundAt.Before(now.Add(-time.Hour)) {
		t.Fatalf("el mensaje tardío movió la última actividad hacia atrás: %v", c.LastInboundAt)
	}

	// The next real message is answered and the AI never sees the late one.
	r.llm.push(text("Con gusto"))
	if err := r.eng.HandleInbound(ctx, Inbound{Phone: phone, Kind: "text", Text: "y cuánto cuesta el envío", WAID: "NEW", At: time.Now()}); err != nil {
		t.Fatal(err)
	}
	waitIdle(t, r, phone, 1)
	last := r.llm.seen[len(r.llm.seen)-1].Messages
	for _, m := range last {
		if strings.Contains(m.Content, "precio?") {
			t.Fatalf("la IA vio el mensaje tardío: %q", m.Content)
		}
	}
}

// A normal fresh message right after another one is not "late".
func TestFreshMessagesAreNotLate(t *testing.T) {
	r := newRig(t, manualNegocio())
	ctx := context.Background()
	phone := "573004445566"
	now := time.Now()
	r.llm.push(text("Hola"))
	_ = r.eng.HandleInbound(ctx, Inbound{Phone: phone, Kind: "text", Text: "hola", WAID: "B1", At: now.Add(-20 * time.Second)})
	waitIdle(t, r, phone, 2)
	r.llm.push(text("Claro"))
	_ = r.eng.HandleInbound(ctx, Inbound{Phone: phone, Kind: "text", Text: "info", WAID: "B2", At: now})
	waitIdle(t, r, phone, 1)
	ms, _ := r.st.Messages(ctx, phone, 0, 0)
	for _, m := range ms {
		if m.Status == store.MsgLate {
			t.Fatalf("un mensaje normal se marcó como tardío: %+v", m)
		}
	}
}

// waitIdle waits until the debounce timer fired and at least n new outbound
// messages exist (or gives up after a few seconds).
func waitIdle(t *testing.T, r *rig, phone string, n int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(30 * time.Millisecond)
		r.eng.mu.Lock()
		pending := len(r.eng.timers)
		r.eng.mu.Unlock()
		ms, _ := r.st.Messages(context.Background(), phone, 0, 0)
		out := 0
		for _, m := range ms {
			if m.Direction == store.DirOut {
				out++
			}
		}
		if pending == 0 && out >= n {
			time.Sleep(50 * time.Millisecond)
			return
		}
	}
}
