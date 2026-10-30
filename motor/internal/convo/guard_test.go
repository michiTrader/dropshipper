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
