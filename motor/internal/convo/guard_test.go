package convo

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/michiTrader/dropshipper/motor/internal/store"
)

func fullDraft(email string) map[string]string {
	return map[string]string{
		"oferta_id": "2x", "nombre_completo": "Ana Restrepo", "email": email, "celular": "3001234567",
		"departamento": "Antioquia", "ciudad": "Medellín", "barrio": "Laureles", "direccion": "Cra 76 # 33-20",
	}
}

func sawAudit(t *testing.T, r *rig, phone, action string) bool {
	t.Helper()
	logs, _ := r.st.AuditFor(context.Background(), phone, 200)
	for _, l := range logs {
		if l.Action == action {
			return true
		}
	}
	return false
}

// The model says "déjame verificar" and stops: the customer must not wait.
func TestStallIsNudgedAndOrderConfirmed(t *testing.T) {
	r := newRig(t, manualNegocio())
	ctx := context.Background()
	phone := "sim-stall"
	r.llm.push(call("c1", "guardar_datos_pedido", fullDraft("ana@gmail.com")), text("Resumen listo"))
	r.say(t, phone, "mis datos")
	r.llm.push(
		text("¡Perfecto! Déjame verificar un momento y te confirmo."),
		call("c2", "confirmar_pedido", map[string]string{}),
		text("¡Listo Ana, tu pedido quedó confirmado!"),
	)
	r.say(t, phone, "Sí")
	o, _ := r.st.OpenOrderFor(ctx, phone)
	if o.Status != store.OrderPendingApprove {
		t.Fatalf("estado %s, debía llegar a por_aprobar", o.Status)
	}
	if !sawAudit(t, r, phone, "correccion_ia") {
		t.Fatal("faltó la corrección de la IA")
	}
}

// The model claims confirmation but never calls the tool: the system confirms.
func TestFalseConfirmationIsAutoConfirmed(t *testing.T) {
	r := newRig(t, manualNegocio())
	ctx := context.Background()
	phone := "sim-claim"
	r.llm.push(call("c1", "guardar_datos_pedido", fullDraft("ana@gmail.com")), text("Resumen listo"))
	r.say(t, phone, "mis datos")
	// Make the last outgoing message the real summary.
	_, _ = r.st.AddMessage(ctx, store.Message{Phone: phone, Direction: store.DirOut, Sender: store.SenderSystem, Kind: "text", Body: "Resumen de tu pedido ..."})
	r.llm.push(text("Tu pedido quedó confirmado"), text("Tu pedido quedó confirmado"), text("Tu pedido quedó confirmado"), text("¡Listo, quedó confirmado!"))
	r.say(t, phone, "sisas")
	o, _ := r.st.OpenOrderFor(ctx, phone)
	if o.Status != store.OrderPendingApprove {
		t.Fatalf("estado %s, debía confirmarse sola", o.Status)
	}
}

func TestEmailRequiredNotCedula(t *testing.T) {
	r := newRig(t, manualNegocio())
	in := fullDraft("")
	delete(in, "email")
	r.llm.push(call("c1", "guardar_datos_pedido", in), text("¿Me das tu correo?"))
	r.say(t, "sim-mail", "mis datos")
	last := r.llm.seen[len(r.llm.seen)-1].Messages
	res := last[len(last)-1].Content
	if !strings.Contains(res, "correo electrónico") || strings.Contains(res, "cédula") {
		t.Fatalf("resultado: %s", res)
	}
}

func TestPromptHumanPersonaAndNoCedula(t *testing.T) {
	r := newRig(t, manualNegocio())
	r.llm.push(text("Hola"))
	r.say(t, "sim-persona", "hola")
	sys := r.llm.seen[0].Messages[0].Content
	for _, want := range []string{"persona real", "NO pidas la cédula", "déjame verificar"} {
		if !strings.Contains(sys, want) {
			t.Errorf("el prompt no incluye %q", want)
		}
	}
	for _, tool := range r.llm.seen[0].Tools {
		if strings.Contains(string(mustJSONb(tool)), "cedula") {
			t.Error("la herramienta aún expone cedula")
		}
	}
}

func TestThinkingNeverReachesCustomer(t *testing.T) {
	r := newRig(t, manualNegocio())
	r.llm.push(text("Hola Ana"))
	r.say(t, "sim-think", "hola")
	for _, m := range lastOut(t, r.st, "sim-think") {
		if strings.Contains(m.Body, "think>") {
			t.Fatalf("filtración: %q", m.Body)
		}
	}
}

func mustJSONb(v any) []byte { b, _ := json.Marshal(v); return b }

// A typo'd email is never stored: the seller gets a suggestion to confirm.
func TestBadEmailIsNotStoredAndSuggestionGiven(t *testing.T) {
	r := newRig(t, manualNegocio())
	ctx := context.Background()
	phone := "sim-badmail"
	r.llm.push(call("c1", "guardar_datos_pedido", fullDraft("anaarrobagmail.com")), text("¿Quisiste decir ana@gmail.com?"))
	r.say(t, phone, "mis datos, mi correo es anaarrobagmail.com")
	o, _ := r.st.OpenOrderFor(ctx, phone)
	if o.Email != "" {
		t.Fatalf("el correo malo se guardó: %q", o.Email)
	}
	last := r.llm.seen[len(r.llm.seen)-1].Messages
	res := last[len(last)-1].Content
	if !strings.Contains(res, "ana@gmail.com") || !strings.Contains(res, "NO lo guardé") {
		t.Fatalf("faltó la sugerencia al modelo: %s", res)
	}
}

// After the order was approved, a new "sí" must not open a second order.
func TestNoSecondOrderWhileFirstIsInFlight(t *testing.T) {
	r := newRig(t, manualNegocio())
	ctx := context.Background()
	phone := "sim-twice"
	r.llm.push(call("c1", "guardar_datos_pedido", fullDraft("ana@gmail.com")), text("Resumen"))
	r.say(t, phone, "mis datos")
	r.llm.push(call("c2", "confirmar_pedido", map[string]string{}), text("Confirmado"))
	r.say(t, phone, "sí")
	o, _ := r.st.OpenOrderFor(ctx, phone)
	if _, err := r.sv.Approve(ctx, o.ID, "dueno"); err != nil {
		t.Fatal(err)
	}
	// The model (wrongly) tries to save a new draft after the customer says "sí".
	r.llm.push(call("c3", "guardar_datos_pedido", fullDraft("ana@gmail.com")), text("Tu pedido ya está en preparación, espera la guía."))
	r.say(t, phone, "si")
	orders, _ := r.st.OrdersFor(ctx, phone)
	if len(orders) != 1 {
		t.Fatalf("hay %d pedidos, debía seguir en 1", len(orders))
	}
	last := r.llm.seen[len(r.llm.seen)-1].Messages
	if res := last[len(last)-1].Content; !strings.Contains(res, "pedido_existente") || !strings.Contains(res, "esperar la guía") {
		t.Fatalf("la IA no recibió el aviso: %s", res)
	}
	// With the customer's explicit request, a second order is allowed.
	in := fullDraft("ana@gmail.com")
	r.llm.push(call("c4", "guardar_datos_pedido", map[string]any{"oferta_id": "1x", "nuevo_pedido": true}), text("Listo, abro un segundo pedido."))
	r.say(t, phone, "sí quiero otro pedido adicional")
	_ = in
	orders, _ = r.st.OrdersFor(ctx, phone)
	if len(orders) != 2 {
		t.Fatalf("con nuevo_pedido debía haber 2 pedidos, hay %d", len(orders))
	}
}

func TestPromptKnowsInFlightOrder(t *testing.T) {
	r := newRig(t, manualNegocio())
	ctx := context.Background()
	phone := "sim-state"
	r.llm.push(call("c1", "guardar_datos_pedido", fullDraft("ana@gmail.com")), text("Resumen"))
	r.say(t, phone, "mis datos")
	r.llm.push(call("c2", "confirmar_pedido", map[string]string{}), text("Confirmado"))
	r.say(t, phone, "sí")
	o, _ := r.st.OpenOrderFor(ctx, phone)
	_, _ = r.sv.Approve(ctx, o.ID, "dueno")
	r.llm.push(text("Tu pedido está en preparación."))
	r.say(t, phone, "ya llegó la guía?")
	sys := r.llm.seen[len(r.llm.seen)-1].Messages[0].Content
	if !strings.Contains(sys, "NO crees otro pedido") || !strings.Contains(sys, "no existe") {
		t.Fatalf("el prompt no explica el estado del pedido:\n%s", sys[len(sys)-700:])
	}
}
