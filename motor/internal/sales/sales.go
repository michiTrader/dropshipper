// Package sales owns the order lifecycle. It is the single place where an
// order changes state, so the AI agent, the owner's panel and the payment
// webhooks all go through the same rules.
//
// The AI never decides money: it reports what the customer said, and this
// package validates the data, applies the zone rule, computes the advance and
// the expected profit, and moves the order. The owner approves.
package sales

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/michiTrader/dropshipper/motor/internal/catalog"
	"github.com/michiTrader/dropshipper/motor/internal/config"
	"github.com/michiTrader/dropshipper/motor/internal/rules"
	"github.com/michiTrader/dropshipper/motor/internal/shipping"
	"github.com/michiTrader/dropshipper/motor/internal/store"
)

// Notifier sends a message to a customer on behalf of the system (implemented
// by the conversation engine, which knows about WhatsApp and the simulator).
type Notifier interface {
	NotifyCustomer(ctx context.Context, phone, text string) error
	NotifyOwner(ctx context.Context, text string)
}

// PaymentLinker creates automatic payment links (Wompi). Optional.
type PaymentLinker interface {
	LinksAvailable() bool
	CreateLink(ctx context.Context, reference, description string, amount int64) (id, url string, err error)
}

// Service is the order lifecycle.
type Service struct {
	Store    *store.Store
	Catalog  *catalog.Catalog
	Zones    *rules.ZoneRules
	Negocio  *config.Live
	Notifier Notifier
	Payments PaymentLinker // nil or unavailable = manual payments (Bre-B / Bancolombia + comprobante)
	// Shipping picks the carrier and freight per order (Dropi quote when
	// connected, product estimate otherwise). Optional.
	Shipping *shipping.Service
	Now      func() time.Time

	mu sync.Mutex // serialises state changes (webhook vs. owner vs. agent)
}

// neg returns the current business configuration (hot-reloadable).
func (s *Service) neg() config.Negocio {
	if s.Negocio == nil {
		return config.DefaultNegocio()
	}
	return s.Negocio.Get()
}

// DraftInput is what the seller agent learned from the chat. Empty fields
// keep the value already stored.
type DraftInput struct {
	ProductSKU   string `json:"producto_sku"`
	OfferID      string `json:"oferta_id"`
	Name         string `json:"nombre_completo"`
	IDNumber     string `json:"cedula,omitempty"` // optional: no longer requested
	Phone        string `json:"celular"`
	Department   string `json:"departamento"`
	City         string `json:"ciudad"`
	Neighborhood string `json:"barrio"`
	Address      string `json:"direccion"`
	References   string `json:"referencias"`
	Email        string `json:"email"`
	Notes        string `json:"notas"`
}

// DraftResult tells the agent what is still missing and what the order looks like.
type DraftResult struct {
	OrderID        int64           `json:"pedido_id"`
	Status         string          `json:"estado"`
	Missing        []string        `json:"faltan"`
	Warnings       []string        `json:"advertencias,omitempty"`
	Offer          string          `json:"oferta,omitempty"`
	Total          int64           `json:"total,omitempty"`
	Zone           *rules.Decision `json:"envio,omitempty"`
	Summary        string          `json:"resumen_para_confirmar,omitempty"`
	ReadyToConfirm bool            `json:"listo_para_confirmar"`
	Instruction    string          `json:"instruccion"`
}

// UpsertDraft creates or updates the open order of a phone with the data the
// customer gave. It never moves an order forward on its own (except back to
// draft when shipping data changes after a confirmation).
func (s *Service) UpsertDraft(ctx context.Context, phone string, in DraftInput) (DraftResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	o, err := s.Store.OpenOrderFor(ctx, phone)
	if errors.Is(err, store.ErrNotFound) {
		p := s.productFor(in.ProductSKU)
		o = store.Order{Phone: phone, Status: store.OrderDraft, ProductSKU: p.SKU, ProductName: p.Name}
	} else if err != nil {
		return DraftResult{}, err
	}
	if o.Status != store.OrderDraft && in.changesOrder(o) {
		// Data changes after confirmation go back to draft so the customer
		// re-confirms and the owner never approves stale data. A confirmed
		// payment is kept (the money is real).
		o.Status = store.OrderDraft
		o.ConfirmedAt = nil
		if o.PaymentStatus != store.PayConfirmed {
			o.PaymentStatus = store.PayNotApplicable
		}
		_ = s.Store.ResolveOrderAlerts(ctx, o.ID)
	}

	var warnings []string
	product, ok := s.Catalog.Get(o.ProductSKU)
	if !ok {
		product = s.Catalog.Default()
	}
	// The customer may switch product before confirming: a draft takes the
	// product the agent names (its offers and price then change with it).
	if v := strings.TrimSpace(in.ProductSKU); v != "" && o.Status == store.OrderDraft && !strings.EqualFold(v, o.ProductSKU) {
		if np, ok := s.Catalog.Get(v); ok && np.Active {
			product = np
			o.ProductSKU, o.ProductName = np.SKU, np.Name
			o.OfferID, o.OfferLabel, o.Units, o.Total = "", "", 0, 0
		} else {
			warnings = append(warnings, fmt.Sprintf("producto_sku %q no existe o no está activo; usa uno de los productos del catálogo", v))
		}
	}
	if v := strings.TrimSpace(in.OfferID); v != "" {
		if offer, ok := product.Offer(v); ok {
			o.OfferID, o.OfferLabel, o.Units, o.Total = offer.ID, offer.Label, offer.Units, offer.Price
		} else {
			warnings = append(warnings, fmt.Sprintf("oferta_id %q no existe; usa una de las ofertas del catálogo", v))
		}
	}
	set := func(dst *string, v string) {
		if v = strings.TrimSpace(v); v != "" {
			*dst = v
		}
	}
	set(&o.CustomerName, titleCase(in.Name))
	if v := digits(in.IDNumber); v != "" {
		o.CustomerIDNumber = v // only if the customer volunteers it
	}
	if v := strings.TrimSpace(in.Email); v != "" && !ValidEmail(v) {
		warnings = append(warnings, "el correo no parece válido; pídeselo de nuevo con calma (ej: nombre@gmail.com)")
		in.Email = ""
	}
	if v := strings.TrimSpace(in.Phone); v != "" {
		norm, ok := NormalizeCOPhone(v)
		if !ok {
			warnings = append(warnings, "el celular no parece un móvil colombiano válido (10 dígitos que empiezan por 3); confírmalo")
		}
		o.CustomerPhone = norm
	}
	set(&o.Department, in.Department)
	set(&o.City, in.City)
	set(&o.Neighborhood, in.Neighborhood)
	set(&o.Address, in.Address)
	set(&o.AddressRefs, in.References)
	set(&o.Email, in.Email)
	set(&o.Notes, in.Notes)
	if o.CustomerPhone == "" {
		// The WhatsApp number is a valid default contact phone.
		if norm, ok := NormalizeCOPhone(phone); ok {
			o.CustomerPhone = norm
		}
	}
	if o.Address != "" && !looksLikeAddress(o.Address) {
		warnings = append(warnings, "la dirección no tiene un número claro; pide la dirección exacta (ej: Calle 10 # 20-30) para evitar una devolución")
	}

	var zone *rules.Decision
	if o.Department != "" && o.City != "" && o.Total > 0 {
		d := s.Zones.Decide(rules.Destination{Department: o.Department, City: o.City, Neighborhood: o.Neighborhood}, o.Total)
		zone = &d
		s.applyZone(ctx, &o, d, product)
		if !d.Matched {
			warnings = append(warnings, "no reconozco el departamento; verifica que esté bien escrito (se aplicó la regla por defecto, con anticipo)")
		}
	}

	o, err = s.Store.SaveOrder(ctx, o)
	if err != nil {
		return DraftResult{}, err
	}
	missing := Missing(o)
	res := DraftResult{
		OrderID: o.ID, Status: o.Status, Missing: missing, Warnings: warnings,
		Offer: o.OfferLabel, Total: o.Total, Zone: zone,
		ReadyToConfirm: len(missing) == 0 && len(warnings) == 0,
	}
	switch {
	case len(missing) > 0:
		res.Instruction = "Pide al cliente SOLO lo que falta: " + strings.Join(missing, ", ") + "."
	case len(warnings) > 0:
		res.Instruction = "Aclara con el cliente las advertencias antes de confirmar."
	default:
		res.Summary = s.Summary(o)
		res.Instruction = "Envía al cliente el resumen_para_confirmar TAL CUAL (puedes agregar una frase corta antes) y pregúntale si todo está correcto. Solo cuando responda que sí, llama confirmar_pedido."
	}
	if o.Status == store.OrderDraft {
		_ = s.Store.SetStage(ctx, phone, "datos")
	}
	return res, nil
}

// productFor returns the active product with that SKU, else the default one.
func (s *Service) productFor(sku string) catalog.Product {
	if sku = strings.TrimSpace(sku); sku != "" {
		if p, ok := s.Catalog.Get(sku); ok && p.Active {
			return p
		}
	}
	return s.Catalog.Default()
}

func (in DraftInput) changesOrder(o store.Order) bool {
	ch := func(newV, old string) bool {
		newV = strings.TrimSpace(newV)
		return newV != "" && !strings.EqualFold(newV, old)
	}
	return ch(in.OfferID, o.OfferID) || ch(in.ProductSKU, o.ProductSKU) || ch(in.Department, o.Department) || ch(in.City, o.City) ||
		ch(in.Address, o.Address) || ch(in.Neighborhood, o.Neighborhood) || ch(titleCase(in.Name), o.CustomerName) ||
		ch(in.Email, o.Email)
}

// Profit is the economics stored with each order (shown on the approval card).
type Profit struct {
	Result         rules.ProfitResult `json:"rentabilidad"`
	TargetCPA      int64              `json:"cpa_objetivo"`
	CostConfirmed  bool               `json:"costo_confirmado"`
	ExpectedUSD    float64            `json:"ganancia_esperada_usd"`
	ReturnRateUsed float64            `json:"tasa_devolucion_usada"`
	// Freight used in the numbers and where it came from ("dropi" | "estimado").
	Freight       int64  `json:"flete"`
	FreightSource string `json:"fuente_flete"`
	// Carriers compared for this destination, best first.
	Carriers       []shipping.Option `json:"transportadoras,omitempty"`
	CarrierWarning string            `json:"advertencia_transportadora,omitempty"`
}

func (s *Service) applyZone(ctx context.Context, o *store.Order, d rules.Decision, p catalog.Product) {
	o.Risk = d.Risk
	o.PaymentPolicy = d.Policy
	o.AdvanceAmount = d.AdvanceAmount
	o.CODAmount = d.CODAmount
	o.ZoneReason = d.Reason
	if len(d.Carriers) > 0 {
		o.Carrier = d.Carriers[0]
	}
	n := s.neg()
	freight, source := p.Freight, shipping.SourceEstimate
	var options []shipping.Option
	var warn string
	if s.Shipping != nil {
		cmp := s.Shipping.Compare(ctx, shipping.Scenario{
			Department: o.Department, City: o.City, Price: o.Total, UnitCost: p.UnitCost, Units: o.Units,
			EstimatedFreight: p.Freight, Risk: d.Risk, Policy: d.Policy, AdvancePaid: d.AdvanceAmount, ZoneCarriers: d.Carriers,
		})
		options, warn = cmp.Options, cmp.Warning
		if cmp.Best != nil {
			o.Carrier, freight, source = cmp.Best.Carrier, cmp.Best.Freight, cmp.Source
		}
	}
	ret := rules.ReturnRateFor(n.Finanzas.TasaDevolucion, d.Risk, d.Policy)
	if t, ok := n.Carrier(o.Carrier); ok && t.TasaDevolucion > 0 {
		ret = rules.ReturnRateFor(t.TasaDevolucion, d.Risk, d.Policy)
	}
	pr := rules.Profit(rules.ProfitInput{
		Price: o.Total, UnitCost: p.UnitCost, Units: o.Units, Freight: freight,
		ReturnRate: ret, AdvancePaid: d.AdvanceAmount,
	})
	perOrder, _ := rules.MaxCPA(pr.BreakEvenCPA, n.Finanzas.MargenSeguridad, n.Finanzas.TasaConfirmacion)
	raw, _ := json.Marshal(Profit{
		Result: pr, TargetCPA: perOrder, CostConfirmed: p.CostConfirmed,
		ExpectedUSD: rules.COPtoUSD(pr.ExpectedProfit, n.Finanzas.TRM), ReturnRateUsed: ret,
		Freight: freight, FreightSource: source, Carriers: options, CarrierWarning: warn,
	})
	o.ProfitJSON = string(raw)
}

var emailRe = regexp.MustCompile(`^[A-Za-z0-9._%+\-]+@[A-Za-z0-9\-]+(\.[A-Za-z0-9\-]+)*\.[A-Za-z]{2,}$`)

// ValidEmail is a pragmatic syntax check (the customer types it in a chat).
func ValidEmail(v string) bool { return emailRe.MatchString(strings.TrimSpace(v)) }

// Missing lists the required fields still empty.
func Missing(o store.Order) []string {
	var m []string
	add := func(cond bool, name string) {
		if cond {
			m = append(m, name)
		}
	}
	add(o.OfferID == "", "oferta (cuántos frascos)")
	add(o.CustomerName == "", "nombre completo")
	add(strings.TrimSpace(o.Email) == "", "correo electrónico")
	add(o.CustomerPhone == "", "celular")
	add(o.Department == "", "departamento")
	add(o.City == "", "ciudad o municipio")
	add(o.Neighborhood == "", "barrio")
	add(o.Address == "", "dirección")
	return m
}

// Summary is the exact text the customer must confirm.
func (s *Service) Summary(o store.Order) string {
	var b strings.Builder
	b.WriteString("📦 *Resumen de tu pedido*\n")
	fmt.Fprintf(&b, "• %s: %s\n", o.ProductName, o.OfferLabel)
	fmt.Fprintf(&b, "• Total: $%s (envío gratis)\n", catalog.FormatCOP(o.Total))
	fmt.Fprintf(&b, "• Nombre: %s\n", o.CustomerName)
	fmt.Fprintf(&b, "• Celular: %s\n", o.CustomerPhone)
	fmt.Fprintf(&b, "• Correo: %s\n", o.Email)
	addr := o.Address
	if o.AddressRefs != "" {
		addr += " (" + o.AddressRefs + ")"
	}
	fmt.Fprintf(&b, "• Dirección: %s, barrio %s, %s, %s\n", addr, o.Neighborhood, o.City, o.Department)
	switch o.PaymentPolicy {
	case rules.PayCOD:
		fmt.Fprintf(&b, "• Pago: contraentrega, pagas $%s al recibir", catalog.FormatCOP(o.CODAmount))
	case rules.PayPartial:
		fmt.Fprintf(&b, "• Pago: anticipo de $%s para despachar y $%s al recibir", catalog.FormatCOP(o.AdvanceAmount), catalog.FormatCOP(o.CODAmount))
	case rules.PayFullAdvance:
		fmt.Fprintf(&b, "• Pago: anticipado, $%s antes del despacho", catalog.FormatCOP(o.AdvanceAmount))
	}
	return b.String()
}

// ConfirmResult is what happens after the customer said "sí".
type ConfirmResult struct {
	OrderID        int64  `json:"pedido_id"`
	Status         string `json:"estado"`
	NeedsPayment   bool   `json:"requiere_pago"`
	Advance        int64  `json:"anticipo"`
	PaymentMessage string `json:"-"`
	Instruction    string `json:"instruccion"`
}

// ErrNotReady means the order cannot be confirmed yet.
var ErrNotReady = errors.New("pedido incompleto")

// Confirm records the customer's explicit confirmation. COD orders go to the
// owner's approval queue; advance orders wait for payment first.
func (s *Service) Confirm(ctx context.Context, phone string) (ConfirmResult, error) {
	s.mu.Lock()
	o, err := s.Store.OpenOrderFor(ctx, phone)
	if err != nil {
		s.mu.Unlock()
		return ConfirmResult{}, fmt.Errorf("no hay un pedido abierto para confirmar; primero registra los datos con guardar_datos_pedido")
	}
	if missing := Missing(o); len(missing) > 0 {
		s.mu.Unlock()
		return ConfirmResult{}, fmt.Errorf("%w: faltan %s", ErrNotReady, strings.Join(missing, ", "))
	}
	if o.PaymentPolicy == "" {
		s.mu.Unlock()
		return ConfirmResult{}, fmt.Errorf("%w: falta calcular el envío (departamento/ciudad)", ErrNotReady)
	}
	if o.Status != store.OrderDraft {
		s.mu.Unlock()
		return ConfirmResult{OrderID: o.ID, Status: o.Status,
			Instruction: "Este pedido ya estaba confirmado (estado " + o.Status + "). No lo confirmes de nuevo; responde lo que el cliente pregunte."}, nil
	}
	t := s.now()
	o.ConfirmedAt = &t
	res := ConfirmResult{OrderID: o.ID}

	if o.AdvanceAmount > 0 && o.PaymentStatus != store.PayConfirmed {
		o.Status = store.OrderAwaitPayment
		if o.PaymentStatus != store.PayProofReceived {
			o.PaymentStatus = store.PayPending
		}
		if o.PaymentRef == "" {
			o.PaymentRef = fmt.Sprintf("PED%d-%04d", o.ID, t.Unix()%10000)
		}
		msg := s.paymentInstructions(ctx, &o)
		res.NeedsPayment, res.Advance, res.PaymentMessage = true, o.AdvanceAmount, msg
		res.Instruction = fmt.Sprintf("Pedido confirmado. El sistema envía AUTOMÁTICAMENTE, justo después de tu respuesta, un mensaje con los datos para pagar el anticipo de $%s. NO escribas números de cuenta, llaves ni links: solo agradece y dile que ahí le llegan los datos y que apenas pague envíe el comprobante (foto o captura) por este chat.", catalog.FormatCOP(o.AdvanceAmount))
		_ = s.Store.SetStage(ctx, phone, "esperando_pago")
	} else {
		o.Status = store.OrderPendingApprove
		res.Instruction = "Pedido confirmado y enviado a despacho. Agradece, dile que le avisaremos por aquí cuando salga con la guía y el tiempo estimado de entrega, y que tenga el dinero listo si es contraentrega. No prometas una fecha exacta."
		_ = s.Store.SetStage(ctx, phone, "cerrado")
	}
	o, err = s.Store.SaveOrder(ctx, o)
	s.mu.Unlock()
	if err != nil {
		return ConfirmResult{}, err
	}
	res.Status = o.Status
	s.Store.Audit(ctx, phone, "sistema", "pedido_confirmado", fmt.Sprintf("pedido %d -> %s", o.ID, o.Status))
	if o.Status == store.OrderPendingApprove {
		s.onReadyForApproval(ctx, o)
	}
	return res, nil
}

func (s *Service) paymentInstructions(ctx context.Context, o *store.Order) string {
	amount := catalog.FormatCOP(o.AdvanceAmount)
	if s.neg().Pagos.Metodo == "wompi" && s.Payments != nil && s.Payments.LinksAvailable() {
		if o.PaymentLink == "" {
			desc := fmt.Sprintf("Anticipo pedido %d - %s", o.ID, o.ProductName)
			id, url, err := s.Payments.CreateLink(ctx, o.PaymentRef, desc, o.AdvanceAmount)
			if err != nil {
				// Fall back to manual instructions rather than losing the sale.
				s.Store.Audit(ctx, o.Phone, "sistema", "wompi_error", err.Error())
				return s.manualInstructions(*o)
			}
			o.PaymentLink = url
			o.PaymentRef = id
		}
		return fmt.Sprintf("💳 Para despachar tu pedido paga el anticipo de *$%s* aquí (Nequi, Bancolombia, PSE o tarjeta):\n%s\n\nApenas se apruebe el pago lo vemos automáticamente y despachamos ✅", amount, o.PaymentLink)
	}
	return s.manualInstructions(*o)
}

func (s *Service) manualInstructions(o store.Order) string {
	m := s.neg().Pagos.Manual
	var b strings.Builder
	fmt.Fprintf(&b, "💳 Para despachar tu pedido, el anticipo es de *$%s*.\n", catalog.FormatCOP(o.AdvanceAmount))
	if m.LlaveBreB != "" {
		fmt.Fprintf(&b, "\n🔑 *Llave Bre-B:* %s\n(La puedes pagar desde Bancolombia, Nequi, Daviplata o cualquier banco)\n", m.LlaveBreB)
	}
	if m.NumeroCuenta != "" {
		banco := m.Banco
		if banco == "" {
			banco = "Bancolombia"
		}
		fmt.Fprintf(&b, "\n🏦 *%s %s:* %s\n", banco, m.TipoCuenta, m.NumeroCuenta)
	}
	if m.Titular != "" {
		fmt.Fprintf(&b, "Titular: %s\n", m.Titular)
	}
	if m.LlaveBreB == "" && m.NumeroCuenta == "" {
		b.WriteString("\nEn un momento te compartimos los datos para el pago 🙏\n")
	}
	fmt.Fprintf(&b, "\nReferencia: %s\n", o.PaymentRef)
	if m.Instrucciones != "" {
		b.WriteString("\n" + strings.TrimSpace(m.Instrucciones) + "\n")
	}
	b.WriteString("\n📸 Cuando pagues, envíanos la foto o captura del comprobante por aquí y despachamos.")
	return b.String()
}

// RegisterProof records that the customer sent something that may be a
// payment receipt. The owner verifies it in the panel; nothing ships on a
// screenshot alone (fake receipts are common).
func (s *Service) RegisterProof(ctx context.Context, phone, mediaPath string) (store.Order, bool, error) {
	s.mu.Lock()
	o, err := s.Store.OpenOrderFor(ctx, phone)
	if err != nil || o.Status != store.OrderAwaitPayment {
		s.mu.Unlock()
		return o, false, nil
	}
	o.PaymentStatus = store.PayProofReceived
	o.PaymentProof = mediaPath
	o, err = s.Store.SaveOrder(ctx, o)
	s.mu.Unlock()
	if err != nil {
		return o, false, err
	}
	_ = s.Store.ResolvePhoneAlerts(ctx, phone, store.AlertPaymentProof)
	_, _ = s.Store.AddAlert(ctx, store.Alert{Phone: phone, OrderID: o.ID, Kind: store.AlertPaymentProof,
		Message: fmt.Sprintf("Comprobante recibido para el pedido #%d (anticipo $%s, ref %s). Verifica en tu banco y confirma el pago.", o.ID, catalog.FormatCOP(o.AdvanceAmount), o.PaymentRef)})
	s.notifyOwner(ctx, fmt.Sprintf("Comprobante recibido: pedido #%d de %s por $%s. Verifícalo en tu banco y confírmalo en el panel.", o.ID, o.CustomerName, catalog.FormatCOP(o.AdvanceAmount)))
	return o, true, nil
}

// ConfirmPayment marks the advance as paid (owner verified the receipt, or a
// signed Wompi webhook said APPROVED) and moves the order to approval.
func (s *Service) ConfirmPayment(ctx context.Context, orderID int64, by string) (store.Order, error) {
	s.mu.Lock()
	o, err := s.Store.GetOrder(ctx, orderID)
	if err != nil {
		s.mu.Unlock()
		return o, err
	}
	if o.PaymentStatus == store.PayConfirmed {
		s.mu.Unlock()
		return o, nil
	}
	if o.Status != store.OrderAwaitPayment && o.Status != store.OrderDraft {
		s.mu.Unlock()
		return o, fmt.Errorf("el pedido #%d está en estado %s, no esperando pago", o.ID, o.Status)
	}
	o.PaymentStatus = store.PayConfirmed
	if o.ConfirmedAt != nil {
		o.Status = store.OrderPendingApprove
	}
	o, err = s.Store.SaveOrder(ctx, o)
	s.mu.Unlock()
	if err != nil {
		return o, err
	}
	_ = s.Store.ResolvePhoneAlerts(ctx, o.Phone, store.AlertPaymentProof)
	s.Store.Audit(ctx, o.Phone, by, "pago_confirmado", fmt.Sprintf("pedido %d anticipo %d", o.ID, o.AdvanceAmount))
	_ = s.Store.SetStage(ctx, o.Phone, "cerrado")
	s.notifyCustomer(ctx, o.Phone, fmt.Sprintf("¡Recibimos tu pago de $%s! 🙌 Tu pedido ya pasa a despacho y te avisamos por aquí cuando salga con la guía 📦", catalog.FormatCOP(o.AdvanceAmount)))
	if o.Status == store.OrderPendingApprove {
		s.onReadyForApproval(ctx, o)
	}
	return o, nil
}

// RejectPayment marks a payment proof as invalid (owner did not find the money).
func (s *Service) RejectPayment(ctx context.Context, orderID int64, by string) (store.Order, error) {
	s.mu.Lock()
	o, err := s.Store.GetOrder(ctx, orderID)
	if err != nil {
		s.mu.Unlock()
		return o, err
	}
	o.PaymentStatus = store.PayPending
	o.PaymentProof = ""
	o, err = s.Store.SaveOrder(ctx, o)
	s.mu.Unlock()
	if err == nil {
		_ = s.Store.ResolvePhoneAlerts(ctx, o.Phone, store.AlertPaymentProof)
		s.Store.Audit(ctx, o.Phone, by, "pago_no_encontrado", fmt.Sprintf("pedido %d", o.ID))
	}
	return o, err
}

func (s *Service) onReadyForApproval(ctx context.Context, o store.Order) {
	if ok, why := s.canAutoApprove(o); ok {
		if _, err := s.Approve(ctx, o.ID, "ia-auto"); err == nil {
			return
		}
	} else if why != "" {
		s.Store.Audit(ctx, o.Phone, "sistema", "sin_auto_aprobacion", why)
	}
	_, _ = s.Store.AddAlert(ctx, store.Alert{Phone: o.Phone, OrderID: o.ID, Kind: store.AlertApproval,
		Message: fmt.Sprintf("Pedido #%d listo para aprobar: %s, %s ($%s, %s).", o.ID, o.CustomerName, o.City, catalog.FormatCOP(o.Total), o.PaymentPolicy)})
	s.notifyOwner(ctx, fmt.Sprintf("Nuevo pedido por aprobar #%d: %s · %s · $%s · %s", o.ID, o.CustomerName, o.City, catalog.FormatCOP(o.Total), o.PaymentPolicy))
}

func (s *Service) canAutoApprove(o store.Order) (bool, string) {
	a := s.neg().Autonomia
	if !a.AutoAprobarPedidos {
		return false, ""
	}
	if p, ok := s.Catalog.Get(o.ProductSKU); !ok || !p.CostConfirmed {
		return false, "costo del producto sin confirmar"
	}
	if a.AutoMaxValor > 0 && o.Total > a.AutoMaxValor {
		return false, "valor mayor al límite de auto-aprobación"
	}
	if a.AutoSoloCOD && o.PaymentPolicy != rules.PayCOD {
		return false, "solo se auto-aprueba contraentrega"
	}
	if a.AutoSoloRiesgoBajo && o.Risk != rules.RiskNormal {
		return false, "solo se auto-aprueba riesgo normal"
	}
	return true, ""
}

// Approve is the owner's "Aceptar" button.
func (s *Service) Approve(ctx context.Context, orderID int64, by string) (store.Order, error) {
	s.mu.Lock()
	o, err := s.Store.GetOrder(ctx, orderID)
	if err != nil {
		s.mu.Unlock()
		return o, err
	}
	if o.Status != store.OrderPendingApprove {
		s.mu.Unlock()
		return o, fmt.Errorf("el pedido #%d está en estado %q; solo se aprueban pedidos 'por_aprobar'", o.ID, o.Status)
	}
	if missing := Missing(o); len(missing) > 0 {
		s.mu.Unlock()
		return o, fmt.Errorf("faltan datos: %s", strings.Join(missing, ", "))
	}
	if o.AdvanceAmount > 0 && o.PaymentStatus != store.PayConfirmed {
		s.mu.Unlock()
		return o, fmt.Errorf("el anticipo de $%s no está confirmado", catalog.FormatCOP(o.AdvanceAmount))
	}
	t := s.now()
	o.Status, o.ApprovedAt = store.OrderApproved, &t
	o, err = s.Store.SaveOrder(ctx, o)
	s.mu.Unlock()
	if err != nil {
		return o, err
	}
	_ = s.Store.ResolveOrderAlerts(ctx, o.ID)
	s.Store.Audit(ctx, o.Phone, by, "pedido_aprobado", fmt.Sprintf("pedido %d", o.ID))
	name := ""
	if f := strings.Fields(o.CustomerName); len(f) > 0 {
		name = " " + f[0]
	}
	s.notifyCustomer(ctx, o.Phone, fmt.Sprintf("¡Hola%s! ✅ Tu pedido de %s quedó confirmado y ya está en preparación. Te enviamos la guía por aquí apenas salga 📦", name, o.ProductName))
	return o, nil
}

// Reject is the owner's "Rechazar" button. The customer is NOT messaged
// automatically: the owner decides what to tell them.
func (s *Service) Reject(ctx context.Context, orderID int64, reason, by string) (store.Order, error) {
	s.mu.Lock()
	o, err := s.Store.GetOrder(ctx, orderID)
	if err != nil {
		s.mu.Unlock()
		return o, err
	}
	if !o.Open() && o.Status != store.OrderApproved {
		s.mu.Unlock()
		return o, fmt.Errorf("el pedido #%d ya está en %s", o.ID, o.Status)
	}
	o.Status = store.OrderRejected
	if reason != "" {
		o.Notes = strings.TrimSpace(o.Notes + "\nRechazado: " + reason)
	}
	o, err = s.Store.SaveOrder(ctx, o)
	s.mu.Unlock()
	if err != nil {
		return o, err
	}
	_ = s.Store.ResolveOrderAlerts(ctx, o.ID)
	s.Store.Audit(ctx, o.Phone, by, "pedido_rechazado", reason)
	return o, nil
}

// Cancel cancels the open order of a customer (they changed their mind).
func (s *Service) Cancel(ctx context.Context, phone, reason, by string) (store.Order, error) {
	s.mu.Lock()
	o, err := s.Store.OpenOrderFor(ctx, phone)
	if err != nil {
		s.mu.Unlock()
		return o, fmt.Errorf("no hay pedido abierto para cancelar")
	}
	o.Status = store.OrderCancelled
	o.Notes = strings.TrimSpace(o.Notes + "\nCancelado: " + reason)
	o, err = s.Store.SaveOrder(ctx, o)
	s.mu.Unlock()
	if err == nil {
		_ = s.Store.ResolveOrderAlerts(ctx, o.ID)
		s.Store.Audit(ctx, phone, by, "pedido_cancelado", reason)
		_ = s.Store.SetStage(ctx, phone, "perdido")
	}
	return o, err
}

// Dispatch records the Dropi order / tracking number and tells the customer.
func (s *Service) Dispatch(ctx context.Context, orderID int64, dropiID, tracking, carrier, by string) (store.Order, error) {
	s.mu.Lock()
	o, err := s.Store.GetOrder(ctx, orderID)
	if err != nil {
		s.mu.Unlock()
		return o, err
	}
	if o.Status != store.OrderApproved && o.Status != store.OrderInDropi {
		s.mu.Unlock()
		return o, fmt.Errorf("primero aprueba el pedido #%d (estado actual %s)", o.ID, o.Status)
	}
	if v := strings.TrimSpace(dropiID); v != "" {
		o.DropiOrderID = v
	}
	if v := strings.TrimSpace(carrier); v != "" {
		o.Carrier = v
	}
	newTracking := strings.TrimSpace(tracking)
	notify := newTracking != "" && newTracking != o.TrackingNumber
	if newTracking != "" {
		o.TrackingNumber = newTracking
	}
	o.Status = store.OrderInDropi
	o, err = s.Store.SaveOrder(ctx, o)
	s.mu.Unlock()
	if err != nil {
		return o, err
	}
	s.Store.Audit(ctx, o.Phone, by, "pedido_en_dropi", fmt.Sprintf("dropi=%s guia=%s %s", o.DropiOrderID, o.TrackingNumber, o.Carrier))
	if notify {
		msg := fmt.Sprintf("📦 ¡Tu pedido ya salió! Va con *%s*, guía *%s*.", o.Carrier, o.TrackingNumber)
		if o.CODAmount > 0 {
			msg += fmt.Sprintf("\nRecuerda tener listos *$%s* para pagar al recibir 🙌", catalog.FormatCOP(o.CODAmount))
		}
		msg += "\nLa transportadora puede llamarte antes de llegar, mantén el celular a la mano 😊"
		s.notifyCustomer(ctx, o.Phone, msg)
	}
	return o, nil
}

// SetStatus moves an order to a post-dispatch status.
func (s *Service) SetStatus(ctx context.Context, orderID int64, status, by string) (store.Order, error) {
	allowed := map[string]bool{store.OrderInTransit: true, store.OrderDelivered: true, store.OrderIssue: true, store.OrderReturned: true, store.OrderCancelled: true}
	if !allowed[status] {
		return store.Order{}, fmt.Errorf("estado no permitido: %s", status)
	}
	s.mu.Lock()
	o, err := s.Store.GetOrder(ctx, orderID)
	if err != nil {
		s.mu.Unlock()
		return o, err
	}
	o.Status = status
	o, err = s.Store.SaveOrder(ctx, o)
	s.mu.Unlock()
	if err == nil {
		s.Store.Audit(ctx, o.Phone, by, "estado_pedido", status)
	}
	return o, err
}

// OwnerEdit applies the owner's edits to an open order. Zone and profit are
// recomputed so the approval card never shows stale numbers.
func (s *Service) OwnerEdit(ctx context.Context, orderID int64, in DraftInput, riskOverride string) (store.Order, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	o, err := s.Store.GetOrder(ctx, orderID)
	if err != nil {
		return o, err
	}
	if !o.Open() {
		return o, fmt.Errorf("el pedido #%d ya no se puede editar (estado %s)", o.ID, o.Status)
	}
	product, _ := s.Catalog.Get(o.ProductSKU)
	if in.OfferID != "" {
		if offer, ok := product.Offer(in.OfferID); ok {
			o.OfferID, o.OfferLabel, o.Units, o.Total = offer.ID, offer.Label, offer.Units, offer.Price
		}
	}
	set := func(dst *string, v string) {
		if v = strings.TrimSpace(v); v != "" {
			*dst = v
		}
	}
	set(&o.CustomerName, in.Name)
	set(&o.CustomerIDNumber, digits(in.IDNumber))
	set(&o.CustomerPhone, digits(in.Phone))
	set(&o.Department, in.Department)
	set(&o.City, in.City)
	set(&o.Neighborhood, in.Neighborhood)
	set(&o.Address, in.Address)
	set(&o.AddressRefs, in.References)
	if v := strings.TrimSpace(in.Email); v != "" {
		if !ValidEmail(v) {
			return o, fmt.Errorf("el correo %q no parece válido", v)
		}
		o.Email = v
	}
	set(&o.Notes, in.Notes)
	if o.Department != "" && o.City != "" && o.Total > 0 {
		d := s.Zones.Decide(rules.Destination{Department: o.Department, City: o.City, Neighborhood: o.Neighborhood}, o.Total)
		if riskOverride != "" && rules.ValidRisk(riskOverride) && riskOverride != d.Risk {
			p := s.Zones.PolicyFor(riskOverride)
			d.Risk, d.Policy = riskOverride, p.Pago
			d.AdvanceAmount, d.CODAmount = rules.Advance(p, o.Total)
			d.Reason = "ajustado manualmente por el dueño"
		}
		s.applyZone(ctx, &o, d, product)
		if o.Status == store.OrderPendingApprove && o.AdvanceAmount > 0 && o.PaymentStatus != store.PayConfirmed {
			o.Status = store.OrderAwaitPayment
			o.PaymentStatus = store.PayPending
			if o.PaymentRef == "" {
				o.PaymentRef = fmt.Sprintf("PED%d-%04d", o.ID, s.now().Unix()%10000)
			}
		}
	}
	o, err = s.Store.SaveOrder(ctx, o)
	if err == nil {
		s.Store.Audit(ctx, o.Phone, "dueno", "pedido_editado", fmt.Sprintf("pedido %d", o.ID))
	}
	return o, err
}

// FlagStalePayments raises an alert for orders waiting for payment too long.
func (s *Service) FlagStalePayments(ctx context.Context) {
	orders, err := s.Store.ListOrders(ctx, store.OrderAwaitPayment)
	if err != nil {
		return
	}
	limit := time.Duration(s.neg().Pagos.HorasParaPagar) * time.Hour
	for _, o := range orders {
		if o.ConfirmedAt == nil || s.now().Sub(*o.ConfirmedAt) < limit || strings.Contains(o.Notes, "[recordatorio-pago]") {
			continue
		}
		o.Notes = strings.TrimSpace(o.Notes + "\n[recordatorio-pago]")
		if _, err := s.Store.SaveOrder(ctx, o); err != nil {
			continue
		}
		_, _ = s.Store.AddAlert(ctx, store.Alert{Phone: o.Phone, OrderID: o.ID, Kind: store.AlertInfo,
			Message: fmt.Sprintf("El pedido #%d lleva más de %d h esperando el anticipo. Escríbele al cliente o cancélalo.", o.ID, s.neg().Pagos.HorasParaPagar)})
	}
}

func (s *Service) notifyCustomer(ctx context.Context, phone, text string) {
	if s.Notifier != nil {
		if err := s.Notifier.NotifyCustomer(ctx, phone, text); err != nil {
			s.Store.Audit(ctx, phone, "sistema", "error_notificacion", err.Error())
		}
	}
}

func (s *Service) notifyOwner(ctx context.Context, text string) {
	if s.Notifier != nil {
		s.Notifier.NotifyOwner(ctx, text)
	}
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

var nonDigit = regexp.MustCompile(`\D`)

func digits(s string) string { return nonDigit.ReplaceAllString(s, "") }

// NormalizeCOPhone returns a 10-digit Colombian mobile number.
func NormalizeCOPhone(s string) (string, bool) {
	d := digits(s)
	if len(d) == 12 && strings.HasPrefix(d, "57") {
		d = d[2:]
	}
	return d, len(d) == 10 && strings.HasPrefix(d, "3")
}

var hasDigit = regexp.MustCompile(`\d`)

func looksLikeAddress(a string) bool {
	l := strings.ToLower(a)
	return hasDigit.MatchString(a) || strings.Contains(l, "vereda") || strings.Contains(l, "finca") || strings.Contains(l, "corregimiento")
}

func titleCase(s string) string {
	words := strings.Fields(strings.ToLower(strings.TrimSpace(s)))
	for i, w := range words {
		r := []rune(w)
		r[0] = []rune(strings.ToUpper(string(r[0])))[0]
		words[i] = string(r)
	}
	return strings.Join(words, " ")
}
