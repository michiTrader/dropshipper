package dropi

// MCPAPI makes Dropi's official MCP server look like the REST client: the same
// Quote / Product / CreateOrder the Motor already calls. The names and fields
// of the MCP tools are discovered at run time (tools/list gives each tool's
// input schema), so the adapter finds "the quoting tool" by what it is called
// and fills its arguments by what each field means. When it cannot fill a
// required field it says exactly which one instead of guessing.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/michiTrader/dropshipper/motor/internal/geo"
)

// MCPAPI is the MCP implementation of API.
type MCPAPI struct {
	C *MCPClient
	// NeedLogin is called (if set) when the saved session can no longer be renewed.
	NeedLogin func()
}

var _ API = (*MCPAPI)(nil)

// NewMCPAPI builds the adapter.
func NewMCPAPI(cfg MCPConfig) *MCPAPI { return &MCPAPI{C: NewMCP(cfg)} }

// Tokens returns the session in use (it may have been renewed).
func (m *MCPAPI) Tokens() Tokens { return m.C.Tokens() }

// Identity says which login this client belongs to.
func (m *MCPAPI) Identity() string {
	return m.C.cfg.ClientID + "|" + m.C.cfg.TokenURL + "|" + m.C.cfg.URL
}

func (m *MCPAPI) note(err error) error {
	if errors.Is(err, ErrNeedLogin) && m.NeedLogin != nil {
		m.NeedLogin()
	}
	return err
}

// Ping proves the login works: it lists the tools. Returns how many there are.
func (m *MCPAPI) Ping(ctx context.Context) (int, error) {
	tools, err := m.C.Tools(ctx, true)
	if err != nil {
		return 0, m.note(err)
	}
	if len(tools) == 0 {
		return 0, errors.New("el MCP de Dropi respondió, pero no ofrece ninguna herramienta")
	}
	return len(tools), nil
}

// Tools lists the MCP tools (for diagnostics).
func (m *MCPAPI) Tools(ctx context.Context) ([]Tool, error) {
	t, err := m.C.Tools(ctx, true)
	return t, m.note(err)
}

// Call runs any tool (for diagnostics from the terminal).
func (m *MCPAPI) Call(ctx context.Context, name string, args map[string]any) (ToolResult, error) {
	r, err := m.C.Call(ctx, name, args)
	return r, m.note(err)
}

// ---------- tolerant reading of words ----------

var accents = strings.NewReplacer("á", "a", "é", "e", "í", "i", "ó", "o", "ú", "u", "ü", "u", "ñ", "n")

// words splits "ciudadDestino", "cod_dane" or "Envío con cobro" into lowercase
// words without accents.
func words(s string) []string {
	var out []string
	var cur []rune
	flush := func() {
		if len(cur) > 0 {
			out = append(out, accents.Replace(strings.ToLower(string(cur))))
			cur = nil
		}
	}
	rs := []rune(s)
	for i, r := range rs {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			flush()
			continue
		}
		if len(cur) > 0 && unicode.IsUpper(r) {
			prev := rs[i-1]
			if unicode.IsLower(prev) || unicode.IsDigit(prev) || (unicode.IsUpper(prev) && i+1 < len(rs) && unicode.IsLower(rs[i+1])) {
				flush()
			}
		}
		cur = append(cur, r)
	}
	flush()
	return out
}

// has reports whether any wanted word is among toks ("x*" matches by prefix).
func has(toks []string, want ...string) bool {
	for _, w := range want {
		prefix := strings.HasSuffix(w, "*")
		w = strings.TrimSuffix(w, "*")
		for _, t := range toks {
			if t == w || prefix && strings.HasPrefix(t, w) {
				return true
			}
		}
	}
	return false
}

// toolWant describes a tool by words: every group in all must match (any word
// of the group), prefer adds points, avoid in the name rules the tool out.
type toolWant struct {
	all    [][]string
	prefer []string
	avoid  []string
}

func pickTool(tools []Tool, w toolWant) (Tool, bool) {
	best, found := -1, false
	var bt Tool
	for _, t := range tools {
		nt, dt := words(t.Name), words(t.Description)
		if has(nt, w.avoid...) {
			continue
		}
		score, ok := 0, true
		for _, g := range w.all {
			switch {
			case has(nt, g...):
				score += 3
			case has(dt, g...):
				score++
			default:
				ok = false
			}
			if !ok {
				break
			}
		}
		if !ok {
			continue
		}
		if has(nt, w.prefer...) {
			score += 2
		}
		if has(dt, w.avoid...) {
			score--
		}
		if score > best {
			best, bt, found = score, t, true
		}
	}
	return bt, found
}

// ---------- schema-driven arguments ----------

type fillResult struct {
	used    map[string]bool // which facts found a place
	missing []string        // required fields nobody knows how to fill
}

func typeOf(s map[string]any) string {
	switch t := s["type"].(type) {
	case string:
		return t
	case []any:
		for _, x := range t {
			if str, ok := x.(string); ok && str != "null" {
				return str
			}
		}
	}
	if _, ok := s["properties"]; ok {
		return "object"
	}
	return "string"
}

func requiredSet(s map[string]any) map[string]bool {
	out := map[string]bool{}
	switch l := s["required"].(type) {
	case []any:
		for _, x := range l {
			if n, ok := x.(string); ok {
				out[n] = true
			}
		}
	case []string:
		for _, n := range l {
			out[n] = true
		}
	}
	return out
}

// classify says what a field means, from its full path (parents included).
func classify(toks []string, typ string, inProduct bool) string {
	side := "dest"
	if has(toks, "origen", "origin", "remitente", "sender", "from", "desde", "pickup", "bodega", "warehouse") {
		side = "origin"
	}
	switch {
	case has(toks, "email", "correo", "mail"):
		return "email"
	case has(toks, "phone", "telefono", "celular", "movil", "whatsapp", "cel", "tel"):
		return "phone"
	case has(toks, "surname", "apellido", "apellidos", "lastname"):
		return "surname"
	case has(toks, "quantity", "cantidad", "qty", "units", "unidades"):
		return "quantity"
	case inProduct && has(toks, "name", "nombre", "title", "titulo"):
		return "product_name"
	case has(toks, "product", "products", "producto", "productos", "sku", "item", "items", "articulo") && !has(toks, "price", "precio", "name", "nombre"):
		return "product_id"
	case inProduct && has(toks, "price", "precio", "valor", "unitprice"):
		return "price"
	case has(toks, "carrier", "transportadora", "courier", "distribution", "distributioncompany"):
		return "carrier"
	case has(toks, "address", "direccion", "dir"):
		return "address"
	case has(toks, "notes", "note", "nota", "notas", "observaciones", "observacion", "comentarios", "comments"):
		return "notes"
	case has(toks, "country", "pais"):
		return "country"
	case has(toks, "dane") || (has(toks, "code", "codigo") && has(toks, "city", "ciudad", "municipio", "origen", "origin", "destino", "destination", "dest")):
		return side + ".code"
	case has(toks, "state", "departamento", "department", "dept", "estado") && typ != "boolean":
		return side + ".dept"
	case has(toks, "city", "ciudad", "municipio"):
		return side + ".city"
	case typ == "boolean" && has(toks, "cod", "cobro", "recaudo", "contraentrega", "collect"):
		return "cod"
	case typ == "string" && has(toks, "rate", "tarifa", "modalidad", "payment", "pago", "metodo", "tipo", "type") && has(toks, "rate", "tarifa", "recaudo", "cobro", "cod", "contraentrega", "modalidad", "pago", "payment"):
		return "rate"
	case has(toks, "total", "amount", "monto", "valor", "value", "recaudo", "declared", "declarado", "price", "precio"):
		return "amount"
	case has(toks, "name", "nombre", "fullname", "nombres"):
		if has(toks, "full", "completo", "fullname") {
			return "fullname"
		}
		return "name"
	}
	return ""
}

func enumOf(s map[string]any) []string {
	var out []string
	if l, ok := s["enum"].([]any); ok {
		for _, x := range l {
			out = append(out, fmt.Sprint(x))
		}
	}
	return out
}

func toInt(v any) (int64, bool) {
	switch t := v.(type) {
	case int:
		return int64(t), true
	case int64:
		return t, true
	case float64:
		return int64(t), true
	case string:
		n, err := strconv.ParseInt(strings.TrimSpace(t), 10, 64)
		return n, err == nil
	}
	return 0, false
}

// valueFor converts a known fact to what the field's type wants.
func valueFor(key string, sch map[string]any, typ string, vals map[string]any) (any, bool) {
	raw, ok := vals[key]
	if !ok || raw == nil || raw == "" {
		return nil, false
	}
	if strings.HasSuffix(key, ".code") {
		digits := fmt.Sprint(raw)
		desc := strings.ToLower(fmt.Sprint(sch["description"]))
		if strings.Contains(desc, "5 d") || strings.Contains(desc, "5-d") || strings.Contains(desc, "five") || strings.Contains(desc, "cinco") {
			if len(digits) >= 5 {
				digits = digits[:5]
			}
		}
		raw = digits
	}
	enum := enumOf(sch)
	if key == "rate" {
		cod, _ := vals["cod"].(bool)
		for _, e := range enum {
			l := strings.ToLower(accents.Replace(e))
			withCollect := strings.Contains(l, "con") && (strings.Contains(l, "recaudo") || strings.Contains(l, "cobro")) || strings.Contains(l, "contraentrega") || l == "cod"
			noCollect := strings.Contains(l, "sin") || strings.Contains(l, "prepago") || strings.Contains(l, "anticipado")
			if cod && withCollect && !noCollect || !cod && noCollect {
				return e, true
			}
		}
		return nil, false
	}
	switch typ {
	case "integer", "number":
		n, ok := toInt(raw)
		if !ok {
			return nil, false
		}
		return n, true
	case "boolean":
		b, ok := raw.(bool)
		return b, ok
	}
	s := fmt.Sprint(raw)
	if len(enum) > 0 {
		for _, e := range enum {
			if strings.EqualFold(e, s) {
				return e, true
			}
		}
		return nil, false
	}
	return s, true
}

func fillObject(schema map[string]any, prefix []string, vals map[string]any, r *fillResult, inProduct bool) map[string]any {
	props, _ := schema["properties"].(map[string]any)
	req := requiredSet(schema)
	names := make([]string, 0, len(props))
	for n := range props {
		names = append(names, n)
	}
	sort.Strings(names)
	out := map[string]any{}
	for _, name := range names {
		sub, _ := props[name].(map[string]any)
		if sub == nil {
			continue
		}
		toks := append(append([]string{}, prefix...), words(name)...)
		_, hasDefault := sub["default"]
		switch typ := typeOf(sub); typ {
		case "object":
			if _, ok := sub["properties"].(map[string]any); ok {
				if v := fillObject(sub, toks, vals, r, inProduct); len(v) > 0 {
					out[name] = v
					continue
				}
			}
			if req[name] {
				r.missing = append(r.missing, strings.Join(toks, "."))
			}
		case "array":
			items, _ := sub["items"].(map[string]any)
			if items != nil && typeOf(items) == "object" {
				if _, ok := items["properties"].(map[string]any); ok {
					if elem := fillObject(items, toks, vals, r, true); len(elem) > 0 {
						out[name] = []any{elem}
						continue
					}
				}
			}
			if req[name] {
				r.missing = append(r.missing, strings.Join(toks, "."))
			}
		default:
			key := classify(toks, typ, inProduct)
			if key != "" {
				if v, ok := valueFor(key, sub, typ, vals); ok {
					out[name] = v
					r.used[key] = true
					continue
				}
			}
			if req[name] && !hasDefault {
				r.missing = append(r.missing, strings.Join(toks, "."))
			}
		}
	}
	return out
}

// buildArgs fills a tool's input schema from known facts.
func buildArgs(t Tool, vals map[string]any) (map[string]any, fillResult) {
	r := fillResult{used: map[string]bool{}}
	out := fillObject(t.Schema, nil, vals, &r, false)
	// A tool with one single "name" field and no surname field must get the
	// full name, or the customer's surname would be lost.
	if full, _ := vals["fullname"].(string); r.used["name"] && !r.used["surname"] && full != "" && full != vals["name"] {
		v2 := make(map[string]any, len(vals))
		for k, v := range vals {
			v2[k] = v
		}
		v2["name"] = full
		r2 := fillResult{used: map[string]bool{}}
		return fillObject(t.Schema, nil, v2, &r2, false), r2
	}
	return out, r
}

// ---------- the API ----------

func quoteTool(tools []Tool) (Tool, bool) {
	return pickTool(tools, toolWant{
		all:    [][]string{{"cotiz*", "quote", "quotes", "flete*", "freight", "tarifa*", "rate", "rates"}},
		prefer: []string{"envio", "shipping", "transportadora*", "cotizar"},
		avoid:  []string{"orden*", "order*", "crear*", "create*", "listar*", "list"},
	})
}

func quoteVals(in QuoteInput) map[string]any {
	v := map[string]any{"amount": in.Amount, "cod": in.COD}
	if in.OriginDANE != "" {
		v["origin.code"] = in.OriginDANE
		if m, ok := geo.ByCode(in.OriginDANE); ok {
			v["origin.city"], v["origin.dept"] = m.Name, m.Department
		}
	}
	if in.DestDANE != "" {
		v["dest.code"] = in.DestDANE
		if m, ok := geo.ByCode(in.DestDANE); ok {
			v["dest.city"], v["dest.dept"] = m.Name, m.Department
		}
	}
	return v
}

// Quote asks Dropi's quoting tool for every carrier's freight on a route.
func (m *MCPAPI) Quote(ctx context.Context, in QuoteInput) ([]Quote, error) {
	tools, err := m.C.Tools(ctx, false)
	if err != nil {
		return nil, m.note(err)
	}
	t, ok := quoteTool(tools)
	if !ok {
		return nil, errors.New("el MCP de Dropi no tiene una herramienta para cotizar el envío (ver Conexiones > Dropi > Ver herramientas)")
	}
	args, r := buildArgs(t, quoteVals(in))
	if len(r.missing) > 0 {
		return nil, fmt.Errorf("la herramienta %q del MCP pide datos que el Motor no sabe llenar: %s", t.Name, strings.Join(r.missing, ", "))
	}
	res, err := m.C.Call(ctx, t.Name, args)
	if err != nil {
		return nil, m.note(err)
	}
	qs := parseQuotes(res.JSON())
	if len(qs) == 0 {
		return nil, fmt.Errorf("Dropi no devolvió transportadoras para esa ruta: %s", snippet([]byte(res.Text)))
	}
	return qs, nil
}

var (
	productDetailWant = toolWant{
		all:    [][]string{{"producto*", "product*"}, {"detalle*", "detail*", "obtener", "get", "ver", "consultar*", "info", "informacion"}},
		prefer: []string{"detalle", "detail", "por", "id"},
		avoid:  []string{"buscar*", "search", "listar*", "list", "categoria*", "categories"},
	}
	productSearchWant = toolWant{
		all:    [][]string{{"producto*", "product*"}, {"buscar*", "search", "listar*", "list", "catalogo*"}},
		prefer: []string{"buscar", "search"},
		avoid:  []string{"categoria*", "categories"},
	}
)

// Product reads a product by its Dropi id. The MCP may not show the supplier
// cost at all: then Cost stays 0 and the caller keeps the owner's own number
// (a cost is never guessed from a field that might be a sale price).
func (m *MCPAPI) Product(ctx context.Context, id string) (Product, error) {
	id = strings.TrimSpace(id)
	tools, err := m.C.Tools(ctx, false)
	if err != nil {
		return Product{}, m.note(err)
	}
	if detail, ok := pickTool(tools, productDetailWant); ok {
		if args, r := buildArgs(detail, map[string]any{"product_id": id}); r.used["product_id"] && len(r.missing) == 0 {
			res, err := m.C.Call(ctx, detail.Name, args)
			if err != nil {
				return Product{}, m.note(err)
			}
			return parseProductMCP(res.JSON(), id)
		}
	}
	search, ok := pickTool(tools, productSearchWant)
	if !ok {
		return Product{}, errors.New("el MCP de Dropi no tiene una herramienta para consultar productos")
	}
	args, miss := buildArgsSearch(search, id)
	if len(miss) > 0 {
		return Product{}, fmt.Errorf("la herramienta %q del MCP pide datos que el Motor no sabe llenar: %s", search.Name, strings.Join(miss, ", "))
	}
	res, err := m.C.Call(ctx, search.Name, args)
	if err != nil {
		return Product{}, m.note(err)
	}
	return parseProductMCP(res.JSON(), id)
}

// buildArgsSearch fills a search/list tool so it looks for one product id.
func buildArgsSearch(t Tool, id string) (map[string]any, []string) {
	props, _ := t.Schema["properties"].(map[string]any)
	req := requiredSet(t.Schema)
	names := make([]string, 0, len(props))
	for n := range props {
		names = append(names, n)
	}
	sort.Strings(names)
	args := map[string]any{}
	var missing []string
	set := false
	for _, name := range names {
		sub, _ := props[name].(map[string]any)
		if sub == nil {
			continue
		}
		toks := words(name)
		_, hasDefault := sub["default"]
		switch {
		case !set && (has(toks, "id", "sku") || has(toks, "product", "producto") && has(toks, "id")):
			if typeOf(sub) == "integer" || typeOf(sub) == "number" {
				if n, ok := toInt(id); ok {
					args[name], set = n, true
				}
			} else {
				args[name], set = id, true
			}
		case !set && has(toks, "query", "q", "search", "term", "texto", "keyword", "buscar", "busqueda", "nombre", "name", "filter", "filtro") && typeOf(sub) == "string":
			args[name], set = id, true
		case req[name] && !hasDefault:
			missing = append(missing, strings.Join(toks, "."))
		}
	}
	if !set {
		missing = append(missing, "(campo para buscar el producto)")
	}
	return args, missing
}

// parseProductMCP reads a product from an MCP answer with STRICT cost fields
// (a generic "price" may be the selling price, so it is never taken as cost).
func parseProductMCP(raw []byte, id string) (Product, error) {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return Product{}, fmt.Errorf("respuesta de Dropi inválida: %w (%s)", err, snippet(raw))
	}
	obj := findObjectByID(v, id)
	if obj == nil {
		return Product{}, fmt.Errorf("Dropi no devolvió el producto %s", id)
	}
	p := Product{ID: id}
	p.Name = str(obj, "name", "nombre", "title", "titulo")
	p.Cost = num(obj, "sale_price", "supplier_price", "precio_proveedor", "costo", "cost", "cost_price", "precio_costo", "wholesale_price", "precio_mayorista")
	p.SalePrice = num(obj, "suggested_price", "precio_sugerido", "sugested_price", "precio_sugerido_venta")
	p.Stock = num(obj, "stock", "total_stock", "quantity", "existencias")
	if p.Name == "" && p.Cost == 0 && p.Stock == 0 {
		return Product{}, errors.New("el producto de Dropi llegó sin datos reconocibles")
	}
	return p, nil
}

// findObjectByID walks a result and returns the object whose id is id (or the
// only object when the answer is a single product).
func findObjectByID(v any, id string) map[string]any {
	var objs []map[string]any
	var walk func(x any, depth int)
	walk = func(x any, depth int) {
		if depth > 6 {
			return
		}
		switch t := x.(type) {
		case map[string]any:
			objs = append(objs, t)
			for _, c := range t {
				walk(c, depth+1)
			}
		case []any:
			for _, c := range t {
				walk(c, depth+1)
			}
		}
	}
	walk(v, 0)
	for _, o := range objs {
		if idStr(o, "id", "product_id", "producto_id") == id {
			return o
		}
	}
	// A single-object answer without an explicit id.
	if m, ok := v.(map[string]any); ok {
		if o := firstObject(m); o != nil && asList(m) == nil {
			return o
		}
	}
	return nil
}

func orderTool(tools []Tool) (Tool, bool) {
	return pickTool(tools, toolWant{
		all:    [][]string{{"crear*", "create*", "nueva*", "nuevo*", "new", "registrar*", "generar*"}, {"orden*", "order*", "pedido*"}},
		prefer: []string{"crear", "create"},
		avoid:  []string{"listar*", "list", "detalle*", "estado*", "status", "cancel*"},
	})
}

// orderVals translates the REST-style payload the Motor builds into facts.
func orderVals(payload map[string]any) map[string]any {
	s := func(k string) string { v, _ := payload[k].(string); return strings.TrimSpace(v) }
	name, surname := s("name"), s("surname")
	if surname == "." {
		surname = ""
	}
	v := map[string]any{
		"name": name, "surname": surname, "fullname": strings.TrimSpace(name + " " + surname),
		"phone": s("phone"), "email": s("client_email"), "dest.dept": s("state"), "dest.city": s("city"),
		"address": s("dir"), "notes": s("notes"), "carrier": s("distributionCompany"), "country": "Colombia",
		"origin.city": s("origen"),
	}
	if mu, ok := geo.Find(s("state"), s("city")); ok {
		v["dest.code"] = mu.DANE7()
		v["dest.city"], v["dest.dept"] = mu.Name, mu.Department
	}
	total, _ := toInt(payload["total_order"])
	cod := strings.EqualFold(s("rate_type"), "CON RECAUDO") && total > 0
	v["cod"], v["amount"] = cod, total
	if items, ok := payload["products"].([]map[string]any); ok && len(items) > 0 {
		it := items[0]
		v["product_id"], v["price"], v["quantity"] = it["id"], it["price"], it["quantity"]
	} else if l, ok := payload["products"].([]any); ok && len(l) > 0 {
		if it, ok := l[0].(map[string]any); ok {
			v["product_id"], v["price"], v["quantity"] = it["id"], it["price"], it["quantity"]
		}
	}
	return v
}

// criticalOrderFields must all be mapped before a REAL order is sent: a wrong
// mapping would put wrong data in a real shipment.
var criticalOrderFields = []struct{ any []string }{
	{[]string{"phone"}}, {[]string{"address"}}, {[]string{"product_id"}}, {[]string{"quantity"}},
	{[]string{"dest.city", "dest.code"}}, {[]string{"name", "fullname"}},
}

// CreateOrder creates a real order through the MCP. It refuses (without
// sending anything) when the tool wants data the Motor cannot supply, or when
// the customer's essentials would not be sent.
func (m *MCPAPI) CreateOrder(ctx context.Context, payload map[string]any) (OrderResult, error) {
	tools, err := m.C.Tools(ctx, false)
	if err != nil {
		return OrderResult{}, m.note(err)
	}
	t, ok := orderTool(tools)
	if !ok {
		return OrderResult{}, errors.New("el MCP de Dropi no tiene una herramienta para crear órdenes: crea la orden a mano en Dropi")
	}
	args, r := buildArgs(t, orderVals(payload))
	if len(r.missing) > 0 {
		return OrderResult{}, fmt.Errorf("la herramienta %q del MCP pide datos que el Motor no sabe llenar (%s): crea la orden a mano en Dropi", t.Name, strings.Join(r.missing, ", "))
	}
	for _, f := range criticalOrderFields {
		ok := false
		for _, k := range f.any {
			ok = ok || r.used[k]
		}
		if !ok {
			return OrderResult{}, fmt.Errorf("la herramienta %q del MCP no tiene dónde poner «%s»: por seguridad no se envió nada, crea la orden a mano en Dropi", t.Name, strings.Join(f.any, " / "))
		}
	}
	res, err := m.C.Call(ctx, t.Name, args)
	if err != nil {
		return OrderResult{}, m.note(err)
	}
	out, perr := parseOrderResult(res.JSON())
	if perr != nil || out.ID == "" {
		return OrderResult{}, fmt.Errorf("Dropi respondió a la creación de la orden pero no pude leer su número: REVISA en Dropi si se creó antes de reintentar. Respuesta: %s", snippet([]byte(res.Text)))
	}
	return out, nil
}

// ToolReport says, for each job the Motor needs, which tool does it and
// whether the Motor can fill it in.
type ToolReport struct {
	Job    string `json:"tarea"`
	Tool   string `json:"herramienta"`
	Ready  bool   `json:"lista"`
	Detail string `json:"detalle"`
}

// Report checks the three jobs against the real tool list.
func (m *MCPAPI) Report(ctx context.Context) ([]Tool, []ToolReport, error) {
	tools, err := m.C.Tools(ctx, true)
	if err != nil {
		return nil, nil, m.note(err)
	}
	var rep []ToolReport
	add := func(job string, t Tool, found bool, args map[string]any, r fillResult) {
		switch {
		case !found:
			rep = append(rep, ToolReport{Job: job, Detail: "No encontré una herramienta para esto."})
		case len(r.missing) > 0:
			rep = append(rep, ToolReport{Job: job, Tool: t.Name, Detail: "Pide datos que no sé llenar: " + strings.Join(r.missing, ", ")})
		default:
			b, _ := json.Marshal(args)
			rep = append(rep, ToolReport{Job: job, Tool: t.Name, Ready: true, Detail: "Enviaría: " + string(b)})
		}
	}
	qt, ok := quoteTool(tools)
	var qa map[string]any
	var qr fillResult
	if ok {
		qa, qr = buildArgs(qt, quoteVals(QuoteInput{COD: true, Amount: 100000, OriginDANE: "0500100", DestDANE: "1100100"}))
	}
	add("Cotizar el envío", qt, ok, qa, qr)

	var pt Tool
	var okP bool
	var pa map[string]any
	var pr fillResult
	if pt, okP = pickTool(tools, productDetailWant); okP {
		pa, pr = buildArgs(pt, map[string]any{"product_id": "12345"})
		if !pr.used["product_id"] && len(pr.missing) == 0 {
			pr.missing = append(pr.missing, "(campo del producto)")
		}
	}
	if !okP || len(pr.missing) > 0 {
		if st, okS := pickTool(tools, productSearchWant); okS {
			sa, miss := buildArgsSearch(st, "12345")
			pt, okP, pa, pr = st, true, sa, fillResult{used: map[string]bool{}, missing: miss}
		}
	}
	add("Leer un producto (costo)", pt, okP, pa, pr)

	ot, okO := orderTool(tools)
	var oa map[string]any
	var or fillResult
	if okO {
		oa, or = buildArgs(ot, orderVals(sampleOrderPayload()))
		if len(or.missing) == 0 {
			for _, f := range criticalOrderFields {
				got := false
				for _, k := range f.any {
					got = got || or.used[k]
				}
				if !got {
					or.missing = append(or.missing, "(sin lugar para "+strings.Join(f.any, "/")+")")
				}
			}
		}
	}
	add("Crear la orden", ot, okO, oa, or)
	return tools, rep, nil
}

func sampleOrderPayload() map[string]any {
	return map[string]any{
		"name": "Ana", "surname": "Prueba", "phone": "3001234567", "client_email": "ana@example.com",
		"state": "Antioquia", "city": "Medellín", "dir": "Calle 1 # 2-3", "notes": "prueba",
		"distributionCompany": "SERVIENTREGA", "rate_type": "CON RECAUDO", "total_order": int64(100000),
		"products": []map[string]any{{"id": "12345", "price": int64(100000), "quantity": 1}},
		"origen":   "Bogotá",
	}
}
