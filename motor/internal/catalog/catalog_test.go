package catalog

import (
	"context"
	"strings"
	"testing"

	"github.com/michiTrader/dropshipper/motor/internal/store"
)

func open(t *testing.T) (*Catalog, *store.Store) {
	t.Helper()
	st, err := store.OpenPath(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	c, err := Open(context.Background(), st, "../../knowledge")
	if err != nil {
		t.Fatal(err)
	}
	return c, st
}

func prod(sku string) Product {
	return Product{SKU: sku, Name: "Producto " + sku, UnitCost: 20000, Freight: 12000,
		Offers: []Offer{{ID: "1x", Label: "1x", Units: 1, Price: 79900}, {ID: "2x", Label: "2x", Units: 2, Price: 129900}}}
}

func TestFirstRunSeedsFromYAML(t *testing.T) {
	c, _ := open(t)
	if len(c.List()) != 1 || c.List()[0].SKU != "LULLA-60" || !c.List()[0].Active {
		t.Fatalf("%+v", c.List())
	}
	if !strings.Contains(c.Playbook(), "Playbook") {
		t.Fatal("playbook not seeded")
	}
}

func TestDatabaseIsSourceOfTruthAfterSeed(t *testing.T) {
	c, st := open(t)
	ctx := context.Background()
	if _, err := c.Save(ctx, prod("B-1")); err != nil {
		t.Fatal(err)
	}
	if err := c.SetActive(ctx, "B-1", true); err != nil {
		t.Fatal(err)
	}
	if err := c.SetActive(ctx, "LULLA-60", false); err != nil {
		t.Fatal(err)
	}
	c2, err := Open(ctx, st, "../../knowledge") // reopening must not re-seed or revert
	if err != nil {
		t.Fatal(err)
	}
	if len(c2.List()) != 2 {
		t.Fatalf("got %d products", len(c2.List()))
	}
	if p, _ := c2.Get("lulla-60"); p.Active {
		t.Fatal("deactivation was lost")
	}
}

func TestSaveNormalizesSKUAndValidates(t *testing.T) {
	c, _ := open(t)
	ctx := context.Background()
	saved, err := c.Save(ctx, prod("  nuevo-1 "))
	if err != nil || saved.SKU != "NUEVO-1" {
		t.Fatalf("%v %+v", err, saved)
	}
	if _, ok := c.Get("nuevo-1"); !ok {
		t.Fatal("lookup must be case-insensitive")
	}
	bad := map[string]func(*Product){
		"código":    func(p *Product) { p.SKU = "" },
		"símbolos":  func(p *Product) { p.SKU = "A B" },
		"nombre":    func(p *Product) { p.Name = " " },
		"oferta":    func(p *Product) { p.Offers = nil },
		"negativos": func(p *Product) { p.UnitCost = -1 },
		"repetida":  func(p *Product) { p.Offers = append(p.Offers, Offer{ID: "1X", Units: 1, Price: 1}) },
		"mayores":   func(p *Product) { p.Offers[0].Price = 0 },
	}
	for want, fn := range bad {
		p := prod("X-1")
		fn(&p)
		if _, err := c.Save(ctx, p); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: got %v", want, err)
		}
	}
}

func TestActiveFiltersAndLastActiveGuard(t *testing.T) {
	c, _ := open(t)
	ctx := context.Background()
	p := prod("B-1")
	p.Active = true
	if _, err := c.Save(ctx, p); err != nil {
		t.Fatal(err)
	}
	if len(c.Active()) != 2 {
		t.Fatalf("active=%d", len(c.Active()))
	}
	if err := c.SetActive(ctx, "B-1", false); err != nil {
		t.Fatal(err)
	}
	if len(c.Active()) != 1 || c.Active()[0].SKU != "LULLA-60" {
		t.Fatalf("%+v", c.Active())
	}
	if err := c.SetActive(ctx, "LULLA-60", false); err == nil || !strings.Contains(err.Error(), "al menos un producto activo") {
		t.Fatalf("last active product must stay on: %v", err)
	}
	if err := c.SetActive(ctx, "NOPE", true); err == nil {
		t.Fatal("unknown sku must fail")
	}
}

func TestDeleteRules(t *testing.T) {
	c, st := open(t)
	ctx := context.Background()
	p := prod("B-1")
	p.Active = true
	if _, err := c.Save(ctx, p); err != nil {
		t.Fatal(err)
	}
	// a product with orders cannot be deleted
	if _, err := st.SaveOrder(ctx, store.Order{Phone: "573001112233", ProductSKU: "B-1", Status: "pending_approval"}); err != nil {
		t.Fatal(err)
	}
	if err := c.Delete(ctx, "B-1"); err == nil || !strings.Contains(err.Error(), "pedido") {
		t.Fatalf("expected history guard, got %v", err)
	}
	// a product without orders can
	if _, err := c.Save(ctx, prod("C-1")); err != nil {
		t.Fatal(err)
	}
	if err := c.Delete(ctx, "C-1"); err != nil {
		t.Fatal(err)
	}
	if _, ok := c.Get("C-1"); ok {
		t.Fatal("not deleted")
	}
	// the last active product cannot be deleted
	_ = c.SetActive(ctx, "B-1", false)
	if err := c.Delete(ctx, "LULLA-60"); err == nil {
		t.Fatal("deleting the only active product must fail")
	}
}

func TestFirstProductIsActiveInEmptyCatalog(t *testing.T) {
	c := newCatalog()
	if _, err := c.Save(context.Background(), prod("A-1")); err != nil {
		t.Fatal(err)
	}
	if len(c.List()) != 1 {
		t.Fatal("memory catalog must store the product")
	}
}

func TestPlaybookRoundTrip(t *testing.T) {
	c, st := open(t)
	ctx := context.Background()
	if err := c.SetPlaybook(ctx, "  Siempre saluda.  "); err != nil {
		t.Fatal(err)
	}
	if c.Playbook() != "Siempre saluda." {
		t.Fatalf("%q", c.Playbook())
	}
	if err := c.SetPlaybook(ctx, strings.Repeat("x", 60001)); err == nil {
		t.Fatal("too long must fail")
	}
	// another process (the CLI) changes it; Reload picks it up
	if err := st.SetSetting(ctx, playbookKey, "Nueva guía"); err != nil {
		t.Fatal(err)
	}
	if err := c.Reload(ctx); err != nil || c.Playbook() != "Nueva guía" {
		t.Fatalf("%v %q", err, c.Playbook())
	}
}

func TestOfferLookupAndSheet(t *testing.T) {
	p := prod("A-1")
	for _, id := range []string{"2x", "2X", "2", "2 frascos"} {
		if o, ok := p.Offer(id); !ok || o.ID != "2x" {
			t.Errorf("Offer(%q) = %+v %v", id, o, ok)
		}
	}
	if _, ok := p.Offer("9x"); ok {
		t.Error("9x must not match")
	}
	if !strings.Contains(p.Sheet(), "A-1") {
		t.Error("sheet must mention the sku")
	}
	if FormatCOP(149900) != "149.900" && FormatCOP(149900) != "$149.900" {
		t.Errorf("FormatCOP=%q", FormatCOP(149900))
	}
}
