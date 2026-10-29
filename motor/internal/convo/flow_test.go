package convo

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/michiTrader/dropshipper/motor/internal/agent"
	"github.com/michiTrader/dropshipper/motor/internal/catalog"
	"github.com/michiTrader/dropshipper/motor/internal/config"
	"github.com/michiTrader/dropshipper/motor/internal/llm"
	"github.com/michiTrader/dropshipper/motor/internal/rules"
	"github.com/michiTrader/dropshipper/motor/internal/sales"
	"github.com/michiTrader/dropshipper/motor/internal/store"
)

// scriptedLLM answers with a fixed sequence of responses (tool calls or text).
type scriptedLLM struct {
	mu    sync.Mutex
	steps []llm.Message
	seen  []llm.Request
}

func (f *scriptedLLM) Complete(_ context.Context, req llm.Request) (llm.Response, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seen = append(f.seen, req)
	if len(f.steps) == 0 {
		return llm.Response{Message: llm.Message{Role: "assistant", Content: "ok"}}, nil
	}
	m := f.steps[0]
	f.steps = f.steps[1:]
	return llm.Response{Message: m}, nil
}

func (f *scriptedLLM) push(ms ...llm.Message) {
	f.mu.Lock()
	f.steps = append(f.steps, ms...)
	f.mu.Unlock()
}

func call(id, name string, args any) llm.Message {
	raw, _ := json.Marshal(args)
	return llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: id, Type: "function",
		Function: llm.FunctionCall{Name: name, Arguments: string(raw)}}}}
}

func text(s string) llm.Message { return llm.Message{Role: "assistant", Content: s} }

type rig struct {
	st  *store.Store
	sv  *sales.Service
	eng *Engine
	llm *scriptedLLM
}

func newRig(t *testing.T, neg config.Negocio) *rig {
	t.Helper()
	kdir := filepath.Join("..", "..", "knowledge")
	cat, err := catalog.Load(kdir)
	if err != nil {
		t.Fatal(err)
	}
	zones, err := rules.LoadZones(filepath.Join(kdir, "zonas.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.OpenPath(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	fake := &scriptedLLM{}
	live := config.NewLive(neg)
	sv := &sales.Service{Store: st, Catalog: cat, Zones: zones, Negocio: live}
	seller := &agent.Seller{LLM: fake, Model: "test", Sales: sv, Store: st, Catalog: cat, Zones: zones, Negocio: live}
	eng := NewEngine(st, seller, sv, nil, live, log.New(io.Discard, "", 0))
	eng.Debounce, eng.BubblePause = 30*time.Millisecond, -1
	sv.Notifier = eng
	t.Cleanup(eng.Close)
	return &rig{st: st, sv: sv, eng: eng, llm: fake}
}

func (r *rig) say(t *testing.T, phone, msg string) {
	t.Helper()
	r.sayKind(t, phone, "text", msg)
}

func (r *rig) sayKind(t *testing.T, phone, kind, msg string) {
	t.Helper()
	ctx := context.Background()
	before, _ := r.st.Messages(ctx, phone, 0, 0)
	if err := r.eng.HandleInbound(ctx, Inbound{Phone: phone, Kind: kind, Text: msg, Simulated: true}); err != nil {
		t.Fatal(err)
	}
	// Wait for the debounced answer (or for nothing, if the AI is paused).
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
		r.eng.mu.Lock()
		pending := len(r.eng.timers)
		r.eng.mu.Unlock()
		ms, _ := r.st.Messages(ctx, phone, 0, 0)
		if pending == 0 && len(ms) > len(before)+1 {
			time.Sleep(30 * time.Millisecond)
			return
		}
		c, _ := r.st.GetContact(ctx, phone)
		if pending == 0 && c.AIPaused {
			return
		}
	}
}

func lastOut(t *testing.T, st *store.Store, phone string) []store.Message {
	t.Helper()
	ms, _ := st.Messages(context.Background(), phone, 0, 0)
	var out []store.Message
	for i := len(ms) - 1; i >= 0 && ms[i].Direction == store.DirOut; i-- {
		out = append([]store.Message{ms[i]}, out...)
	}
	return out
}

func manualNegocio() config.Negocio {
	n := config.DefaultNegocio()
	n.Pagos.Metodo = "manual"
	n.Pagos.Manual.LlaveBreB = "@lullabites"
	n.Pagos.Manual.NumeroCuenta = "123-456789-01"
	n.Pagos.Manual.Titular = "Tienda Prueba"
	return n
}

// Medellín: COD, straight to the owner's approval queue.
func TestFlowMedellinCOD(t *testing.T) {
	r := newRig(t, manualNegocio())
	ctx := context.Background()
	phone := "sim-med"

	r.llm.push(text("¡Hola! Soy Sofi 😊\n\n¿Cómo has estado durmiendo?"))
	r.say(t, phone, "Hola, info de las gomitas")
	if out := lastOut(t, r.st, phone); len(out) != 2 || out[0].Sender != store.SenderAI {
		t.Fatalf("expected 2 AI bubbles, got %+v", out)
	}

	r.llm.push(
		call("c1", "guardar_datos_pedido", map[string]string{
			"oferta_id": "2x", "nombre_completo": "ana maría restrepo", "cedula": "1.036.123.456", "celular": "300 123 4567",
			"departamento": "Antioquia", "ciudad": "Medellín", "barrio": "Laureles", "direccion": "Cra 76 # 33-20 apto 301",
		}),
		text("Perfecto Ana, este es tu resumen..."),
	)
	r.say(t, phone, "Ana María Restrepo, cc 1036123456, ...")
	o, err := r.st.OpenOrderFor(ctx, phone)
	if err != nil {
		t.Fatal(err)
	}
	if o.Status != store.OrderDraft || o.PaymentPolicy != rules.PayCOD || o.Total != 149900 || o.CustomerName != "Ana María Restrepo" || o.CustomerIDNumber != "1036123456" || o.CustomerPhone != "3001234567" {
		t.Fatalf("draft: %+v", o)
	}
	// The tool result must have handed the agent the exact summary to confirm.
	last := r.llm.seen[len(r.llm.seen)-1].Messages
	toolResult := last[len(last)-1].Content
	if !strings.Contains(toolResult, "listo_para_confirmar\":true") || !strings.Contains(toolResult, "Resumen de tu pedido") {
		t.Fatalf("tool result: %s", toolResult)
	}

	r.llm.push(call("c2", "confirmar_pedido", map[string]string{}), text("¡Listo! Tu pedido quedó confirmado ✅"))
	r.say(t, phone, "Sí, todo correcto")
	o, _ = r.st.GetOrder(ctx, o.ID)
	if o.Status != store.OrderPendingApprove || o.ConfirmedAt == nil {
		t.Fatalf("after confirm: %+v", o)
	}
	alerts, _ := r.st.OpenAlerts(ctx)
	if len(alerts) != 1 || alerts[0].Kind != store.AlertApproval {
		t.Fatalf("approval alert: %+v", alerts)
	}

	// Owner presses "Aceptar": the customer gets a system message.
	if _, err := r.sv.Approve(ctx, o.ID, "dueno"); err != nil {
		t.Fatal(err)
	}
	out := lastOut(t, r.st, phone)
	if len(out) == 0 || out[len(out)-1].Sender != store.SenderSystem || !strings.Contains(out[len(out)-1].Body, "confirmado") {
		t.Fatalf("approval message: %+v", out)
	}
	// Dispatch with tracking number: customer gets the guide and the COD amount.
	if _, err := r.sv.Dispatch(ctx, o.ID, "DROPI-1", "240012345", "Coordinadora", "dueno"); err != nil {
		t.Fatal(err)
	}
	out = lastOut(t, r.st, phone)
	if msg := out[len(out)-1].Body; !strings.Contains(msg, "240012345") || !strings.Contains(msg, "149.900") {
		t.Fatalf("dispatch message: %s", msg)
	}
	if alerts, _ := r.st.OpenAlerts(ctx); len(alerts) != 0 {
		t.Fatalf("alerts should be resolved after approval: %+v", alerts)
	}
}

// Far zone: partial advance, manual Bre-B instructions sent by the SYSTEM,
// screenshot registered as proof, owner confirms, then approves.
func TestFlowFarZoneAdvance(t *testing.T) {
	r := newRig(t, manualNegocio())
	ctx := context.Background()
	phone := "sim-far"

	r.llm.push(
		call("c1", "guardar_datos_pedido", map[string]string{
			"oferta_id": "1x", "nombre_completo": "Juan Pérez", "cedula": "71234567", "celular": "3109876543",
			"departamento": "Antioquia", "ciudad": "Vigía del Fuerte", "barrio": "Centro", "direccion": "Calle 3 # 4-10",
		}),
		text("Resumen..."),
	)
	r.say(t, phone, "mis datos")
	r.llm.push(call("c2", "confirmar_pedido", map[string]string{}), text("¡Gracias Juan! Ahora te llegan los datos para el anticipo 🙏"))
	r.say(t, phone, "sí confirmo")

	o, _ := r.st.OpenOrderFor(ctx, phone)
	if o.Status != store.OrderAwaitPayment || o.PaymentPolicy != rules.PayPartial || o.AdvanceAmount != 45000 || o.CODAmount != 44900 {
		t.Fatalf("await payment: %+v", o)
	}
	out := lastOut(t, r.st, phone)
	sys := out[len(out)-1]
	if sys.Sender != store.SenderSystem || !strings.Contains(sys.Body, "@lullabites") || !strings.Contains(sys.Body, "45.000") || !strings.Contains(sys.Body, o.PaymentRef) {
		t.Fatalf("payment instructions must come from the system with exact data: %+v", sys)
	}
	if _, err := r.sv.Approve(ctx, o.ID, "dueno"); err == nil {
		t.Fatal("approving before the advance is paid must fail")
	}

	r.llm.push(text("¡Gracias! Ya lo estamos verificando 🙌"))
	r.sayKind(t, phone, "image", "")
	o, _ = r.st.GetOrder(ctx, o.ID)
	if o.PaymentStatus != store.PayProofReceived || o.Status != store.OrderAwaitPayment {
		t.Fatalf("proof: %+v", o)
	}

	if _, err := r.sv.ConfirmPayment(ctx, o.ID, "dueno"); err != nil {
		t.Fatal(err)
	}
	o, _ = r.st.GetOrder(ctx, o.ID)
	if o.Status != store.OrderPendingApprove || o.PaymentStatus != store.PayConfirmed {
		t.Fatalf("after payment: %+v", o)
	}
	if _, err := r.sv.Approve(ctx, o.ID, "dueno"); err != nil {
		t.Fatal(err)
	}
}

// Red zone: 100 % advance, never COD, whatever the customer says.
func TestFlowRedZoneFullAdvance(t *testing.T) {
	r := newRig(t, manualNegocio())
	phone := "sim-red"
	r.llm.push(
		call("c1", "guardar_datos_pedido", map[string]string{
			"oferta_id": "3x", "nombre_completo": "Luz Gómez", "cedula": "41000111", "departamento": "Amazonas",
			"ciudad": "Leticia", "barrio": "Centro", "direccion": "Calle 8 # 10-20",
		}),
		text("Resumen"),
	)
	r.say(t, phone, "datos")
	o, _ := r.st.OpenOrderFor(context.Background(), phone)
	if o.PaymentPolicy != rules.PayFullAdvance || o.AdvanceAmount != 199900 || o.CODAmount != 0 {
		t.Fatalf("red zone: %+v", o)
	}
}

// "Yo tomo": while paused the AI never answers; the owner's message is
// stored as dueno; escalation pauses the AI by itself.
func TestTakeoverAndEscalation(t *testing.T) {
	r := newRig(t, manualNegocio())
	ctx := context.Background()
	phone := "sim-human"

	r.llm.push(call("c1", "escalar_a_humano", map[string]string{"motivo": "quiere que lo llamen"}), text("Claro, en breve te contacta una persona del equipo 🙌"))
	r.say(t, phone, "Quiero que me llamen")
	c, _ := r.st.GetContact(ctx, phone)
	if !c.AIPaused {
		t.Fatal("escalation must pause the AI")
	}
	calls := len(r.llm.seen)
	r.say(t, phone, "¿hola?")
	if len(r.llm.seen) != calls {
		t.Fatal("the AI answered while paused")
	}
	if err := r.eng.OwnerSend(ctx, phone, "Hola, soy el dueño, te llamo ya"); err != nil {
		t.Fatal(err)
	}
	out := lastOut(t, r.st, phone)
	if out[len(out)-1].Sender != store.SenderOwner {
		t.Fatalf("owner message: %+v", out)
	}
	// The owner's message reaches the model as [Mensaje del equipo] once resumed.
	if err := r.eng.SetPaused(ctx, phone, false); err != nil {
		t.Fatal(err)
	}
	r.llm.push(text("¿Te puedo ayudar con algo más?"))
	r.say(t, phone, "gracias")
	req := r.llm.seen[len(r.llm.seen)-1]
	joined := ""
	for _, m := range req.Messages {
		joined += m.Role + ":" + m.Content + "\n"
	}
	if !strings.Contains(joined, "[Mensaje del equipo] Hola, soy el dueño") {
		t.Fatalf("owner message not in history:\n%s", joined)
	}
}

// Several quick messages are answered once (debounce), and a duplicated
// WhatsApp id is ignored.
func TestDebounceAndDuplicates(t *testing.T) {
	r := newRig(t, manualNegocio())
	r.eng.Debounce = 150 * time.Millisecond
	ctx := context.Background()
	phone := "573001112233"
	r.llm.push(text("¡Hola! ¿En qué te ayudo?"))
	for i, m := range []string{"hola", "buenas", "info"} {
		if err := r.eng.HandleInbound(ctx, Inbound{Phone: phone, Text: m, WAID: "wamid." + string(rune('a'+i)), Simulated: true}); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.eng.HandleInbound(ctx, Inbound{Phone: phone, Text: "info", WAID: "wamid.c", Simulated: true}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(500 * time.Millisecond)
	if len(r.llm.seen) != 1 {
		t.Fatalf("expected one answer for a burst, got %d model calls", len(r.llm.seen))
	}
	ms, _ := r.st.Messages(ctx, phone, 0, 0)
	in := 0
	for _, m := range ms {
		if m.Direction == store.DirIn {
			in++
		}
	}
	if in != 3 {
		t.Fatalf("duplicate webhook stored: %d inbound", in)
	}
}

// Changing the address after confirming sends the order back to draft so the
// owner never approves stale data.
func TestChangeAfterConfirmReturnsToDraft(t *testing.T) {
	r := newRig(t, manualNegocio())
	ctx := context.Background()
	phone := "sim-change"
	in := sales.DraftInput{OfferID: "1x", Name: "Pedro Ruiz", IDNumber: "98765432", Phone: "3001112233",
		Department: "Antioquia", City: "Envigado", Neighborhood: "Zúñiga", Address: "Cl 40 Sur # 30-10"}
	if _, err := r.sv.UpsertDraft(ctx, phone, in); err != nil {
		t.Fatal(err)
	}
	if _, err := r.st.EnsureContact(ctx, phone, "", true); err != nil {
		t.Fatal(err)
	}
	if _, err := r.sv.Confirm(ctx, phone); err != nil {
		t.Fatal(err)
	}
	res, err := r.sv.UpsertDraft(ctx, phone, sales.DraftInput{Address: "Cl 41 Sur # 31-20"})
	if err != nil {
		t.Fatal(err)
	}
	o, _ := r.st.GetOrder(ctx, res.OrderID)
	if o.Status != store.OrderDraft || o.ConfirmedAt != nil {
		t.Fatalf("expected back to draft: %+v", o)
	}
}

type failingLLM struct{ err error }

func (f failingLLM) Complete(context.Context, llm.Request) (llm.Response, error) {
	return llm.Response{}, f.err
}

// A failing AI must raise an alert AND tell the panel to stop showing
// "escribiendo..." (regression: the panel stayed typing forever).
func TestAIErrorPublishesListoAndAlert(t *testing.T) {
	r := newRig(t, manualNegocio())
	r.eng.Agent.(*agent.Seller).LLM = failingLLM{err: errors.New(`IA HTTP 404: {"message":"Route POST:/chat/completions not found"}`)}
	ch, stop := r.eng.Events.Subscribe()
	defer stop()

	phone := "573001112233"
	if err := r.eng.HandleInbound(context.Background(), Inbound{Phone: phone, Kind: "text", Text: "hola", Simulated: true}); err != nil {
		t.Fatal(err)
	}
	var seen []string
	deadline := time.After(3 * time.Second)
	for done := false; !done; {
		select {
		case ev := <-ch:
			if ev.Phone == phone {
				seen = append(seen, ev.Type)
			}
			if ev.Type == "listo" {
				done = true
			}
		case <-deadline:
			t.Fatalf("never got 'listo'; events=%v", seen)
		}
	}
	alerts, _ := r.st.OpenAlerts(context.Background())
	if len(alerts) == 0 || !strings.Contains(alerts[0].Message, "/v1") {
		t.Fatalf("expected a friendly alert mentioning /v1, got %+v", alerts)
	}
}
