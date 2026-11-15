package convo

import (
	"context"
	"strings"
	"testing"

	"github.com/michiTrader/dropshipper/motor/internal/store"
)

// Rejecting needs a reason, the reason is stored and the AI is told the order
// will NOT ship, so it never says "va en camino".
func TestRejectedOrderGivesTheAIItsReason(t *testing.T) {
	r := newRig(t, manualNegocio())
	ctx := context.Background()
	phone := "sim-reject"
	r.llm.push(call("c1", "guardar_datos_pedido", fullDraft("ana@gmail.com")), text("Resumen"))
	r.say(t, phone, "mis datos")
	r.llm.push(call("c2", "confirmar_pedido", map[string]string{}), text("Confirmado"))
	r.say(t, phone, "sí")
	o, err := r.st.OpenOrderFor(ctx, phone)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := r.sv.Reject(ctx, o.ID, "   ", "dueno"); err == nil {
		t.Fatal("rechazar sin motivo debía fallar")
	}
	if got, _ := r.st.GetOrder(ctx, o.ID); got.Status == store.OrderRejected {
		t.Fatal("el pedido se rechazó sin motivo")
	}

	rej, err := r.sv.Reject(ctx, o.ID, "La dirección está fuera de cobertura de la transportadora", "dueno")
	if err != nil {
		t.Fatal(err)
	}
	if rej.RejectReason != "La dirección está fuera de cobertura de la transportadora" {
		t.Fatalf("motivo sin guardar: %q", rej.RejectReason)
	}
	if again, _ := r.st.GetOrder(ctx, o.ID); again.RejectReason == "" {
		t.Fatal("el motivo no se guardó en la base")
	}

	r.llm.push(text("Tu pedido no pudo salir"))
	r.say(t, phone, "ya salió mi pedido?")
	sys := r.llm.seen[len(r.llm.seen)-1].Messages[0].Content
	for _, want := range []string{"NO aprobó", "fuera de cobertura", "Ese pedido NO se va a enviar", "Nunca le digas al cliente que va en camino"} {
		if !strings.Contains(sys, want) {
			t.Errorf("el prompt no incluye %q", want)
		}
	}
	if strings.Contains(sys, "Si el cliente escribe \"sí\", \"ya\" o pregunta por su pedido, se refiere a ESTE pedido: dile en qué estado está y que debe esperar la guía") {
		t.Error("el prompt aún le pide esperar la guía de un pedido rechazado")
	}
}

func TestCancelledOrderKeepsItsReason(t *testing.T) {
	r := newRig(t, manualNegocio())
	ctx := context.Background()
	phone := "sim-cancel"
	r.llm.push(call("c1", "guardar_datos_pedido", fullDraft("ana@gmail.com")), text("Resumen"))
	r.say(t, phone, "mis datos")
	o, _ := r.st.OpenOrderFor(ctx, phone)
	if _, err := r.sv.Cancel(ctx, phone, "El cliente ya no lo quiere", "dueno"); err != nil {
		t.Fatal(err)
	}
	got, _ := r.st.GetOrder(ctx, o.ID)
	if got.RejectReason != "El cliente ya no lo quiere" {
		t.Fatalf("motivo de cancelación: %q", got.RejectReason)
	}
}
