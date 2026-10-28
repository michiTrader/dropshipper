// Package catalog holds the products and the sales guide (playbook) that make
// up the seller agent's knowledge. Products live in the database, so they can
// be edited from the panel or the terminal; the YAML files in knowledge/ only
// seed the first run (and serve as import/export format).
package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"

	"github.com/michiTrader/dropshipper/motor/internal/store"
)

// Offer is one price option (1x, 2x, 3x...).
type Offer struct {
	ID        string `yaml:"id" json:"id"`
	Label     string `yaml:"etiqueta" json:"etiqueta"`
	Units     int    `yaml:"unidades" json:"unidades"`
	Price     int64  `yaml:"precio" json:"precio"`
	Highlight bool   `yaml:"destacada" json:"destacada"`
}

// Objection is a known objection with its suggested handling.
type Objection struct {
	Objection string `yaml:"objecion" json:"objecion"`
	Answer    string `yaml:"respuesta" json:"respuesta"`
}

// Shipping holds delivery promises shown to customers.
type Shipping struct {
	MainCities string `yaml:"tiempo_ciudades_principales" json:"tiempo_ciudades_principales"`
	Others     string `yaml:"tiempo_otras_zonas" json:"tiempo_otras_zonas"`
	Cost       string `yaml:"costo_para_cliente" json:"costo_para_cliente"`
}

// Product is one product sheet (knowledge/productos/*.yaml).
type Product struct {
	SKU            string      `yaml:"sku" json:"sku"`
	Name           string      `yaml:"nombre" json:"nombre"`
	Category       string      `yaml:"categoria" json:"categoria"`
	Short          string      `yaml:"descripcion_corta" json:"descripcion_corta"`
	Presentation   string      `yaml:"presentacion" json:"presentacion"`
	Ingredients    []string    `yaml:"ingredientes" json:"ingredientes"`
	NoMelatonin    bool        `yaml:"sin_melatonina" json:"sin_melatonina"`
	Usage          string      `yaml:"modo_uso" json:"modo_uso"`
	Feeling        string      `yaml:"sensacion" json:"sensacion"`
	Precautions    []string    `yaml:"precauciones" json:"precauciones"`
	Offers         []Offer     `yaml:"ofertas" json:"ofertas"`
	UnitCost       int64       `yaml:"costo_proveedor" json:"costo_proveedor"`
	Freight        int64       `yaml:"flete_estimado" json:"flete_estimado"`
	CostConfirmed  bool        `yaml:"costo_confirmado" json:"costo_confirmado"`
	DropiProductID string      `yaml:"dropi_product_id" json:"dropi_product_id"`
	Shipping       Shipping    `yaml:"envio" json:"envio"`
	Guarantee      string      `yaml:"garantia" json:"garantia"`
	Pains          []string    `yaml:"dolores" json:"dolores"`
	Benefits       []string    `yaml:"beneficios" json:"beneficios"`
	Objections     []Objection `yaml:"objeciones" json:"objeciones"`
	// Active products are offered by the seller; inactive ones are kept
	// (history, pending orders) but never sold. Stored beside the data.
	Active bool `yaml:"-" json:"activo"`
}

// Offer returns the offer with the given id ("2x", "2", "2 frascos" all match 2x).
func (p Product) Offer(id string) (Offer, bool) {
	id = strings.ToLower(strings.TrimSpace(id))
	for _, o := range p.Offers {
		if strings.ToLower(o.ID) == id {
			return o, true
		}
	}
	for _, o := range p.Offers {
		if id == fmt.Sprint(o.Units) || strings.HasPrefix(id, fmt.Sprint(o.Units)+" ") {
			return o, true
		}
	}
	return Offer{}, false
}

const (
	playbookKey = "playbook"
	seededKey   = "_catalog_seeded"
)

// Catalog is every product plus the playbook. Safe for concurrent use.
type Catalog struct {
	st  *store.Store // nil = memory only (tests, tools)
	dir string

	mu       sync.RWMutex
	products []Product
	bySKU    map[string]Product
	playbook string
}

func newCatalog() *Catalog { return &Catalog{bySKU: map[string]Product{}} }

// NormalizeSKU upper-cases and trims a SKU.
func NormalizeSKU(s string) string { return strings.ToUpper(strings.TrimSpace(s)) }

// ReadYAML parses and validates one product sheet.
func ReadYAML(raw []byte) (Product, error) {
	var p Product
	if err := yaml.Unmarshal(raw, &p); err != nil {
		return p, err
	}
	p.Active = true
	if err := p.Validate(); err != nil {
		return p, err
	}
	return p, nil
}

func loadYAMLDir(dir string) ([]Product, string, error) {
	files, err := filepath.Glob(filepath.Join(dir, "productos", "*.yaml"))
	if err != nil {
		return nil, "", err
	}
	sort.Strings(files)
	var out []Product
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			return nil, "", err
		}
		p, err := ReadYAML(raw)
		if err != nil {
			return nil, "", fmt.Errorf("%s: %w", filepath.Base(f), err)
		}
		out = append(out, p)
	}
	pb, _ := os.ReadFile(filepath.Join(dir, "playbook_ventas.md"))
	return out, string(pb), nil
}

// Load reads knowledge/productos/*.yaml and playbook_ventas.md into a
// memory-only catalog (no database). Used by tools and tests.
func Load(dir string) (*Catalog, error) {
	c := newCatalog()
	ps, pb, err := loadYAMLDir(dir)
	if err != nil {
		return nil, err
	}
	if len(ps) == 0 {
		return nil, fmt.Errorf("no hay productos en %s/productos", dir)
	}
	for _, p := range ps {
		c.putLocked(p)
	}
	c.playbook = pb
	return c, nil
}

// Open returns the database-backed catalog. The first time (empty database)
// it is seeded from the YAML files in dir; afterwards the database rules.
func Open(ctx context.Context, st *store.Store, dir string) (*Catalog, error) {
	c := newCatalog()
	c.st, c.dir = st, dir
	if _, err := st.GetSetting(ctx, seededKey); errors.Is(err, store.ErrNotFound) {
		ps, pb, lerr := loadYAMLDir(dir)
		if lerr != nil {
			return nil, lerr
		}
		for _, p := range ps {
			if err := c.persist(ctx, p); err != nil {
				return nil, err
			}
		}
		if pb != "" {
			if err := st.SetSetting(ctx, playbookKey, pb); err != nil {
				return nil, err
			}
		}
		if err := st.SetSetting(ctx, seededKey, "1"); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}
	if err := c.Reload(ctx); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *Catalog) putLocked(p Product) {
	key := NormalizeSKU(p.SKU)
	if _, ok := c.bySKU[key]; !ok {
		c.products = append(c.products, p)
	} else {
		for i := range c.products {
			if NormalizeSKU(c.products[i].SKU) == key {
				c.products[i] = p
			}
		}
	}
	c.bySKU[key] = p
}

// Reload re-reads products and playbook from the database.
func (c *Catalog) Reload(ctx context.Context) error {
	if c.st == nil {
		return nil
	}
	rows, err := c.st.ListProductRows(ctx)
	if err != nil {
		return err
	}
	var list []Product
	by := map[string]Product{}
	for _, r := range rows {
		var p Product
		if err := json.Unmarshal([]byte(r.Data), &p); err != nil {
			return fmt.Errorf("producto %s dañado: %w", r.SKU, err)
		}
		p.Active = r.Active
		list = append(list, p)
		by[NormalizeSKU(p.SKU)] = p
	}
	pb, err := c.st.GetSetting(ctx, playbookKey)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return err
	}
	c.mu.Lock()
	c.products, c.bySKU, c.playbook = list, by, pb
	c.mu.Unlock()
	return nil
}

// Validate checks a product sheet; the message is shown to the owner.
func (p Product) Validate() error {
	if strings.TrimSpace(p.SKU) == "" {
		return errors.New("el producto necesita un código (SKU), por ejemplo LULLA-60")
	}
	if strings.ContainsAny(p.SKU, " /\\?#%") {
		return errors.New("el código (SKU) no puede tener espacios ni símbolos; usa letras, números y guiones")
	}
	if strings.TrimSpace(p.Name) == "" {
		return fmt.Errorf("el producto %s necesita un nombre", p.SKU)
	}
	if len(p.Offers) == 0 {
		return fmt.Errorf("el producto %s necesita al menos una oferta (por ejemplo 1 unidad a un precio)", p.SKU)
	}
	if p.UnitCost < 0 || p.Freight < 0 {
		return errors.New("el costo y el flete no pueden ser negativos")
	}
	seen := map[string]bool{}
	for _, o := range p.Offers {
		if strings.TrimSpace(o.ID) == "" || o.Price <= 0 || o.Units <= 0 {
			return fmt.Errorf("producto %s: cada oferta necesita identificador, unidades y precio mayores que cero", p.SKU)
		}
		if seen[strings.ToLower(o.ID)] {
			return fmt.Errorf("producto %s: la oferta %s está repetida", p.SKU, o.ID)
		}
		seen[strings.ToLower(o.ID)] = true
	}
	return nil
}

func (c *Catalog) persist(ctx context.Context, p Product) error {
	raw, err := json.Marshal(p)
	if err != nil {
		return err
	}
	return c.st.SaveProductRow(ctx, store.ProductRow{SKU: NormalizeSKU(p.SKU), Data: string(raw), Active: p.Active})
}

// Save creates or updates a product (SKU is normalized to upper case).
func (c *Catalog) Save(ctx context.Context, p Product) (Product, error) {
	p.SKU = NormalizeSKU(p.SKU)
	if err := p.Validate(); err != nil {
		return p, err
	}
	if c.st != nil {
		c.mu.RLock()
		old, exists := c.bySKU[p.SKU]
		c.mu.RUnlock()
		_ = old
		if !exists && len(c.products) == 0 {
			p.Active = true
		}
		if err := c.persist(ctx, p); err != nil {
			return p, err
		}
		return p, c.Reload(ctx)
	}
	c.mu.Lock()
	c.putLocked(p)
	c.mu.Unlock()
	return p, nil
}

// SetActive turns a product on or off. At least one product must stay active,
// otherwise the seller would have nothing to sell.
func (c *Catalog) SetActive(ctx context.Context, sku string, active bool) error {
	sku = NormalizeSKU(sku)
	p, ok := c.Get(sku)
	if !ok {
		return store.ErrNotFound
	}
	if !active && p.Active {
		others := 0
		for _, o := range c.List() {
			if o.Active && NormalizeSKU(o.SKU) != sku {
				others++
			}
		}
		if others == 0 {
			return errors.New("debe quedar al menos un producto activo: activa otro antes de desactivar este")
		}
	}
	p.Active = active
	if c.st == nil {
		c.mu.Lock()
		c.putLocked(p)
		c.mu.Unlock()
		return nil
	}
	if err := c.persist(ctx, p); err != nil {
		return err
	}
	return c.Reload(ctx)
}

// Delete removes a product. Products with orders cannot be deleted (their
// history matters); deactivate them instead.
func (c *Catalog) Delete(ctx context.Context, sku string) error {
	sku = NormalizeSKU(sku)
	p, ok := c.Get(sku)
	if !ok {
		return store.ErrNotFound
	}
	if c.st != nil {
		n, err := c.st.CountOrdersForSKU(ctx, sku)
		if err != nil {
			return err
		}
		if n > 0 {
			return fmt.Errorf("este producto tiene %d pedido(s): no se puede borrar para conservar el historial; desactívalo si ya no lo vendes", n)
		}
	}
	if p.Active {
		others := 0
		for _, o := range c.List() {
			if o.Active && NormalizeSKU(o.SKU) != sku {
				others++
			}
		}
		if others == 0 {
			return errors.New("es el único producto activo: activa otro antes de borrarlo")
		}
	}
	if c.st == nil {
		c.mu.Lock()
		delete(c.bySKU, sku)
		out := c.products[:0:0]
		for _, o := range c.products {
			if NormalizeSKU(o.SKU) != sku {
				out = append(out, o)
			}
		}
		c.products = out
		c.mu.Unlock()
		return nil
	}
	if err := c.st.DeleteProductRow(ctx, sku); err != nil {
		return err
	}
	return c.Reload(ctx)
}

// SetPlaybook replaces the sales guide text.
func (c *Catalog) SetPlaybook(ctx context.Context, text string) error {
	text = strings.TrimSpace(text)
	if len(text) > 60000 {
		return errors.New("la guía de ventas es demasiado larga (máximo 60.000 caracteres)")
	}
	if c.st == nil {
		c.mu.Lock()
		c.playbook = text
		c.mu.Unlock()
		return nil
	}
	if err := c.st.SetSetting(ctx, playbookKey, text); err != nil {
		return err
	}
	return c.Reload(ctx)
}

// Playbook returns the sales guide text.
func (c *Catalog) Playbook() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.playbook
}

// List returns every product (active or not) in display order.
func (c *Catalog) List() []Product {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return append([]Product(nil), c.products...)
}

// Active returns only the products the seller may offer.
func (c *Catalog) Active() []Product {
	c.mu.RLock()
	defer c.mu.RUnlock()
	var out []Product
	for _, p := range c.products {
		if p.Active {
			out = append(out, p)
		}
	}
	return out
}

// Get returns a product by SKU (case-insensitive), active or not.
func (c *Catalog) Get(sku string) (Product, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	p, ok := c.bySKU[NormalizeSKU(sku)]
	return p, ok
}

// Default returns the first active product (the one advertised today); when
// none is active, the first product; when empty, a zero Product.
func (c *Catalog) Default() Product {
	c.mu.RLock()
	defer c.mu.RUnlock()
	for _, p := range c.products {
		if p.Active {
			return p
		}
	}
	if len(c.products) > 0 {
		return c.products[0]
	}
	return Product{}
}

// Sheet renders a product as compact text for the system prompt.
func (p Product) Sheet() string {
	var b strings.Builder
	fmt.Fprintf(&b, "PRODUCTO: %s (sku %s)\n%s\n", p.Name, p.SKU, strings.TrimSpace(p.Short))
	fmt.Fprintf(&b, "Presentación: %s\n", p.Presentation)
	if p.NoMelatonin {
		b.WriteString("NO contiene melatonina.\n")
	}
	b.WriteString("Ingredientes:\n")
	for _, i := range p.Ingredients {
		fmt.Fprintf(&b, "- %s\n", i)
	}
	fmt.Fprintf(&b, "Modo de uso: %s\n", strings.TrimSpace(p.Usage))
	fmt.Fprintf(&b, "Qué se siente: %s\n", strings.TrimSpace(p.Feeling))
	b.WriteString("OFERTAS (precios exactos en COP, no inventes otros):\n")
	for _, o := range p.Offers {
		star := ""
		if o.Highlight {
			star = " ⭐ recomendada"
		}
		fmt.Fprintf(&b, "- oferta_id=%s · %s · $%s%s\n", o.ID, o.Label, FormatCOP(o.Price), star)
	}
	fmt.Fprintf(&b, "Envío: %s. Ciudades principales: %s. Otras zonas: %s.\n", p.Shipping.Cost, p.Shipping.MainCities, p.Shipping.Others)
	fmt.Fprintf(&b, "Garantía: %s\n", strings.TrimSpace(p.Guarantee))
	b.WriteString("Dolores del cliente ideal:\n")
	for _, x := range p.Pains {
		fmt.Fprintf(&b, "- %s\n", x)
	}
	b.WriteString("Beneficios:\n")
	for _, x := range p.Benefits {
		fmt.Fprintf(&b, "- %s\n", x)
	}
	b.WriteString("PRECAUCIONES (obligatorias):\n")
	for _, x := range p.Precautions {
		fmt.Fprintf(&b, "- %s\n", x)
	}
	b.WriteString("Objeciones frecuentes y cómo manejarlas:\n")
	for _, o := range p.Objections {
		fmt.Fprintf(&b, "- \"%s\": %s\n", o.Objection, strings.TrimSpace(o.Answer))
	}
	return b.String()
}

// FormatCOP formats 149900 as "149.900".
func FormatCOP(v int64) string {
	neg := v < 0
	if neg {
		v = -v
	}
	s := fmt.Sprintf("%d", v)
	var out []byte
	for i := 0; i < len(s); i++ {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, '.')
		}
		out = append(out, s[i])
	}
	if neg {
		return "-" + string(out)
	}
	return string(out)
}
