// Package catalog loads the product sheets and the sales playbook that make up
// the seller agent's knowledge.
package catalog

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
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

// Catalog is every product plus the playbook.
type Catalog struct {
	Products []Product
	Playbook string
	bySKU    map[string]Product
}

// Load reads knowledge/productos/*.yaml and knowledge/playbook_ventas.md.
func Load(dir string) (*Catalog, error) {
	c := &Catalog{bySKU: map[string]Product{}}
	files, err := filepath.Glob(filepath.Join(dir, "productos", "*.yaml"))
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		var p Product
		if err := yaml.Unmarshal(raw, &p); err != nil {
			return nil, fmt.Errorf("%s: %w", filepath.Base(f), err)
		}
		if err := p.validate(); err != nil {
			return nil, fmt.Errorf("%s: %w", filepath.Base(f), err)
		}
		c.Products = append(c.Products, p)
		c.bySKU[strings.ToUpper(p.SKU)] = p
	}
	if len(c.Products) == 0 {
		return nil, fmt.Errorf("no hay productos en %s/productos", dir)
	}
	pb, err := os.ReadFile(filepath.Join(dir, "playbook_ventas.md"))
	if err != nil {
		return nil, fmt.Errorf("leer playbook: %w", err)
	}
	c.Playbook = string(pb)
	return c, nil
}

func (p Product) validate() error {
	if p.SKU == "" || p.Name == "" {
		return fmt.Errorf("producto sin sku o nombre")
	}
	if len(p.Offers) == 0 {
		return fmt.Errorf("producto %s sin ofertas", p.SKU)
	}
	seen := map[string]bool{}
	for _, o := range p.Offers {
		if o.ID == "" || o.Price <= 0 || o.Units <= 0 {
			return fmt.Errorf("producto %s: oferta inválida %+v", p.SKU, o)
		}
		if seen[strings.ToLower(o.ID)] {
			return fmt.Errorf("producto %s: oferta repetida %s", p.SKU, o.ID)
		}
		seen[strings.ToLower(o.ID)] = true
	}
	return nil
}

// Get returns a product by SKU (case-insensitive).
func (c *Catalog) Get(sku string) (Product, bool) {
	p, ok := c.bySKU[strings.ToUpper(strings.TrimSpace(sku))]
	return p, ok
}

// Default returns the first product (the one being advertised today).
func (c *Catalog) Default() Product { return c.Products[0] }

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
