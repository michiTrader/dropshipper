package agent

import (
	"regexp"
	"strings"

	"github.com/michiTrader/dropshipper/motor/internal/store"
)

// Models sometimes stall ("déjame verificar…") or claim an order is confirmed
// without calling confirmar_pedido. The customer then waits forever and the
// order stays in "borrador". These checks catch both cases.

var (
	stallRe = regexp.MustCompile(`(?i)\b(d[eé]jame|perm[ií]teme|dame un momento|un momento|un segundito|voy a)\b[^.!?\n]{0,40}\b(verific|revis|consult|confirm|checar|chequear|validar|buscar)`)
	claimRe = regexp.MustCompile(`(?i)(pedido|compra|orden)[^.!?\n]{0,30}(qued[oó]|est[aá]|ha quedado|fue|ya est[aá])[^.!?\n]{0,20}(confirmad|registrad|listo|aprobad)|(confirm[eé]|registr[eé]) (tu|su) (pedido|compra)|ya (confirm|registr)[eé]`)
	yesRe   = regexp.MustCompile(`^(s[ií]+s*|sisas?|sip|claro|correcto|confirmo|confirmado|de una|dale|listo|ok|okay|perfecto|todo bien|todo correcto|as[ií] es|exacto|vale|obvio|por supuesto)( .*)?$`)
)

const nudgeStall = "[Aviso interno del sistema, el cliente no lo ve] Dijiste que ibas a verificar algo pero no hiciste nada. No hay nada que esperar: usa ahora la herramienta que corresponda (por ejemplo confirmar_pedido si el cliente ya dijo que sí al resumen) o responde directamente al cliente con la información que ya tienes. No anuncies acciones: hazlas."
const nudgeClaim = "[Aviso interno del sistema, el cliente no lo ve] Afirmaste que el pedido quedó confirmado o registrado, pero no llamaste a confirmar_pedido, así que NO lo está. Si el cliente ya dijo que sí al resumen, llama confirmar_pedido ahora y luego responde según lo que devuelva. Si no, no afirmes nada que no hayas hecho."

// needsNudge returns the internal correction to send back to the model when its
// answer is a stall or a false claim, or "" when the answer is fine.
func needsNudge(content string, called map[string]bool) string {
	c := strings.TrimSpace(content)
	if c == "" {
		return ""
	}
	if !called["confirmar_pedido"] && claimRe.MatchString(c) {
		return nudgeClaim
	}
	if len(c) < 280 && stallRe.MatchString(c) && !strings.Contains(c, "?") {
		return nudgeStall
	}
	return ""
}

// customerSaidYesToSummary is true when the customer's latest message is a
// short, clear "yes" and the message right before it (from us) was the order
// summary waiting for confirmation.
func customerSaidYesToSummary(history []store.Message) bool {
	n := len(history)
	if n == 0 || history[n-1].Sender != store.SenderCustomer || history[n-1].Kind != "text" && history[n-1].Kind != "" {
		return false
	}
	last := strings.ToLower(strings.TrimSpace(strings.Trim(history[n-1].Body, " .,!¡¿?")))
	if len(last) > 40 || !yesRe.MatchString(last) {
		return false
	}
	for i := n - 2; i >= 0; i-- {
		if history[i].Sender == store.SenderCustomer {
			return false
		}
		if strings.Contains(history[i].Body, "Resumen de tu pedido") {
			return true
		}
	}
	return false
}
