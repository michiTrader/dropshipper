package convo

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/michiTrader/dropshipper/motor/internal/store"
)

// confirmThenCancel leaves the phone with a cancelled order whose chat history
// still says it was confirmed (the exact case of the bug report).
func confirmThenCancel(t *testing.T, r *rig, phone, reason string) int64 {
	t.Helper()
	ctx := context.Background()
	r.llm.push(call("c1", "guardar_datos_pedido", fullDraft("john@gmail.com")), text("Resumen listo"))
	r.say(t, phone, "mis datos")
	r.llm.push(call("c2", "confirmar_pedido", map[string]string{}), text("¡Listo John, tu pedido quedó confirmado y en preparación!"))
	r.say(t, phone, "sí")
	o, err := r.st.OpenOrderFor(ctx, phone)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.sv.Cancel(ctx, phone, reason, "dueño"); err != nil {
		t.Fatal(err)
	}
	return o.ID
}

func TestPromptListsRealStatusOfCancelledOrder(t *testing.T) {
	r := newRig(t, manualNegocio())
	phone := "sim-cancel-prompt"
	id := confirmThenCancel(t, r, phone, "cliente no contesta")
	r.llm.push(text("Tu pedido fue cancelado por: cliente no contesta."))
	r.say(t, phone, "quiero otro pedido")
	sys := r.llm.seen[len(r.llm.seen)-1].Messages[0].Content
	for _, want := range []string{"ESTE ES EL ESTADO REAL", "estado: cancelado", "cliente no contesta", "Ese pedido NO se va a enviar"} {
		if !strings.Contains(sys, want) {
			t.Errorf("el prompt no incluye %q\n%s", want, sys)
		}
	}
	_ = id
}

func TestAILyingAboutCancelledOrderIsCorrected(t *testing.T) {
	r := newRig(t, manualNegocio())
	phone := "sim-cancel-lie"
	id := confirmThenCancel(t, r, phone, "cliente no contesta")
	r.llm.push(
		text("Hola John, como ya tienes el pedido #"+strconv.FormatInt(id, 10)+" confirmado y en preparación, si quieres otro lo hacemos."),
		text("Hola John, te cuento que tu pedido #"+strconv.FormatInt(id, 10)+" fue cancelado (cliente no contesta). ¿Quieres intentarlo de nuevo?"),
	)
	r.say(t, phone, "quiero otro pedido")
	out := lastOut(t, r.st, phone)
	all := ""
	for _, m := range out {
		all += m.Body + " "
	}
	if strings.Contains(all, "en preparación") || !strings.Contains(all, "cancelado") {
		t.Fatalf("la IA no corrigió su mentira: %q", all)
	}
	if !sawAudit(t, r, phone, "correccion_ia") {
		t.Fatal("falta la auditoría de la corrección")
	}
}

func TestPersistentLieIsReplacedByTruth(t *testing.T) {
	r := newRig(t, manualNegocio())
	phone := "sim-cancel-lie2"
	confirmThenCancel(t, r, phone, "cliente no contesta")
	lie := text("Claro, tu pedido ya está confirmado y en preparación.")
	r.llm.push(lie, lie, lie, lie)
	r.say(t, phone, "cómo va mi pedido?")
	all := ""
	for _, m := range lastOut(t, r.st, phone) {
		all += m.Body + " "
	}
	if strings.Contains(all, "en preparación") || !strings.Contains(all, "cancelado") || !strings.Contains(all, "cliente no contesta") {
		t.Fatalf("debía responder el sistema con la verdad: %q", all)
	}
}

func TestHonestReplyAboutCancelledOrderIsNotTouched(t *testing.T) {
	r := newRig(t, manualNegocio())
	phone := "sim-cancel-ok"
	confirmThenCancel(t, r, phone, "cliente no contesta")
	r.llm.push(text("Tu pedido no está confirmado: fue cancelado y no va en camino."))
	r.say(t, phone, "cómo va mi pedido?")
	logs, _ := r.st.AuditFor(context.Background(), phone, 200)
	for _, l := range logs {
		if l.Action == "correccion_ia" {
			t.Fatalf("no debía corregir una respuesta honesta: %+v", l)
		}
	}
	_ = store.OrderCancelled
}
