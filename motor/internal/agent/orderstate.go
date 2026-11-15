package agent

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/michiTrader/dropshipper/motor/internal/catalog"
	"github.com/michiTrader/dropshipper/motor/internal/sales"
	"github.com/michiTrader/dropshipper/motor/internal/store"
)

// maxOrdersInPrompt limits how many past orders the model is shown.
const maxOrdersInPrompt = 5

func isOpen(o store.Order) bool {
	switch o.Status {
	case store.OrderDraft, store.OrderAwaitPayment, store.OrderPendingApprove:
		return true
	}
	return false
}

// orderStateText describes EVERY recent order of the customer (newest first),
// with its real status. This is the truth: it overrides anything said earlier
// in the chat, because an order can change state (cancelled, rejected, shipped)
// after the AI talked about it.
func orderStateText(orders []store.Order) string {
	if len(orders) == 0 {
		return "Sin pedidos todavía.\n"
	}
	var b strings.Builder
	b.WriteString("Pedidos de este cliente, del más nuevo al más viejo. ESTE ES EL ESTADO REAL y manda sobre cualquier cosa que se haya dicho antes en el chat (el estado cambia: un pedido que antes estaba confirmado pudo cancelarse o rechazarse después). Nunca describas un pedido con un estado distinto al que aparece aquí.\n")
	shown := orders
	if len(shown) > maxOrdersInPrompt {
		shown = shown[:maxOrdersInPrompt]
	}
	var open *store.Order
	for i := range shown {
		o := shown[i]
		if open == nil && isOpen(o) {
			open = &shown[i]
		}
		fmt.Fprintf(&b, "- Pedido #%d · producto: %s · estado: %s", o.ID, o.ProductName, o.Status)
		if o.TrackingNumber != "" {
			fmt.Fprintf(&b, " · guía %s (%s)", o.TrackingNumber, o.Carrier)
		}
		if o.Total > 0 {
			fmt.Fprintf(&b, " · total: $%s", catalog.FormatCOP(o.Total))
		}
		if isOpen(o) {
			fmt.Fprintf(&b, " · oferta: %s", firstNonEmpty(o.OfferLabel, "sin elegir"))
		}
		b.WriteString("\n")
		if note := sales.StatusNote(o); note != "" {
			b.WriteString("  " + note + "\n")
		}
		if sales.Closed(o) {
			b.WriteString("  ⚠ Ese pedido NO se va a enviar: no está confirmado, ni en preparación, ni en camino; está " + o.Status + ".\n")
		}
	}
	if open != nil {
		if m := sales.Missing(*open); len(m) > 0 {
			fmt.Fprintf(&b, "Datos que faltan del pedido abierto #%d: %s\n", open.ID, strings.Join(m, ", "))
		}
		switch open.Status {
		case store.OrderAwaitPayment:
			fmt.Fprintf(&b, "Pedido abierto #%d esperando el anticipo de $%s (estado del pago: %s). Si el cliente pregunta, recuérdale que envíe el comprobante.\n", open.ID, catalog.FormatCOP(open.AdvanceAmount), open.PaymentStatus)
		case store.OrderPendingApprove:
			fmt.Fprintf(&b, "El pedido abierto #%d ya está confirmado y en revisión para despacho. No lo vuelvas a confirmar. Si el cliente pregunta por la guía, dile que apenas el equipo la registre le llega por este chat; no prometas fecha.\n", open.ID)
		}
		return b.String()
	}
	o := shown[0]
	if sales.Closed(o) {
		fmt.Fprintf(&b, "El último pedido (#%d) NO se va a enviar. Nunca le digas al cliente que va en camino, que está confirmado, que está en preparación, que lo recibirá ni que espere una guía. Si escribe \"sí\", \"ya\" o pregunta por su pedido, se refiere a ESTE pedido: dile con claridad que está %s y explícale lo que pasó. Si pide intentarlo de nuevo o comprar otra vez, toma sus datos con guardar_datos_pedido como un pedido nuevo (no asumas que los datos del anterior siguen valiendo sin confirmarlos).\n", o.ID, o.Status)
		return b.String()
	}
	b.WriteString("Si el cliente escribe \"sí\", \"ya\" o pregunta por su pedido, se refiere al último pedido: dile en qué estado está y que debe esperar la guía. NO crees otro pedido. Solo si pide comprar otra vez, pregúntale de forma explícita si quiere un pedido adicional y, únicamente si lo confirma, usa guardar_datos_pedido con nuevo_pedido=true.\n")
	return b.String()
}

var (
	orderRefRe = regexp.MustCompile(`(?i)(?:pedido|orden|compra)\s*(?:n[uú]mero|no\.?|n[º°])?\s*#\s*(\d+)`)
	// A sentence presenting an order as alive: confirmed, being prepared, on the way...
	aliveRe = regexp.MustCompile(`(?i)(confirmad|en preparaci|preparando|alistando|en camino|va en camino|despachad|enviad|aprobad|registrad|en revisi[oó]n|ya tienes|ya tiene)`)
	// Sentences that already say the truth (or negate) are left alone.
	truthRe = regexp.MustCompile(`(?i)(cancelad|rechazad|no (se )?(va|pudo|fue|qued|est[aá]|aprob|confirm|envi|despach)|no sali[oó]|sin enviar|anulad)`)
)

func sentences(s string) []string {
	return regexp.MustCompile(`[.!?\n]+`).Split(s, -1)
}

// closedClaim returns the closed (cancelled/rejected) order the reply talks
// about as if it were alive, or nil. A reply that cites "#N" is judged against
// order N; one that talks about "el pedido" without a number is judged against
// the newest order (the one the customer is referring to) when no order is open.
func closedClaim(reply string, orders []store.Order) *store.Order {
	if len(orders) == 0 {
		return nil
	}
	byID := map[int64]*store.Order{}
	for i := range orders {
		byID[orders[i].ID] = &orders[i]
	}
	hasOpen := false
	for _, o := range orders {
		if isOpen(o) {
			hasOpen = true
			break
		}
	}
	for _, sen := range sentences(reply) {
		if !aliveRe.MatchString(sen) || truthRe.MatchString(sen) {
			continue
		}
		if m := orderRefRe.FindStringSubmatch(sen); m != nil {
			id, _ := strconv.ParseInt(m[1], 10, 64)
			if o, ok := byID[id]; ok && sales.Closed(*o) {
				return o
			}
			continue
		}
		if !hasOpen && sales.Closed(orders[0]) && regexp.MustCompile(`(?i)\b(pedido|orden|compra)\b`).MatchString(sen) {
			return &orders[0]
		}
	}
	return nil
}

func nudgeClosed(o store.Order) string {
	return fmt.Sprintf("[Aviso interno del sistema, el cliente no lo ve] Mentiste: dijiste o insinuaste que el pedido #%d está confirmado, en preparación o en camino, pero su estado REAL es «%s» y NO se va a enviar.%s Reescribe tu respuesta diciendo la verdad con tacto: que ese pedido está %s. No lo des por activo. Si el cliente quiere pedir de nuevo, ofrécele tomar sus datos como un pedido nuevo.", o.ID, o.Status, reasonSuffix(o), o.Status)
}

func reasonSuffix(o store.Order) string {
	if r := strings.TrimSpace(o.RejectReason); r != "" {
		return " Motivo anotado por el equipo: " + r + "."
	}
	return " El equipo no anotó un motivo: no lo inventes."
}

// closedFallback is the honest message sent when the model keeps lying after
// being corrected: the system speaks for it.
func closedFallback(o store.Order) string {
	what := "fue cancelado"
	if o.Status == store.OrderRejected {
		what = "no fue aprobado"
	}
	msg := fmt.Sprintf("Hola 👋 Te cuento con honestidad: tu pedido #%d %s y no se va a enviar.", o.ID, what)
	if r := strings.TrimSpace(o.RejectReason); r != "" {
		msg += " Motivo: " + r + "."
	}
	return msg + " Si quieres intentarlo de nuevo, con gusto tomo tus datos otra vez. 😊"
}
