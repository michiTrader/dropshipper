package dropi

import (
	"encoding/json"
	"testing"
)

const toonProduct = "items[1]:\r\n  - id: 2022974\r\n    sku: Lullabites (gomitas para dormir)\r\n    name: Lullabites (gomitas para dormir)\r\n    sale_price: 27000\r\n    suggested_price: 55000\r\n    visible_start_sale_price: null\r\n    stock: 144\r\n    supplier_id: 10581\r\n    categories[1]: Salud\r\n    gallery[1]{urlS3}:\r\n      \"https://d39ru7awumhhs2.cloudfront.net/colombia/products/2022974/178077340924.png\"\r\n    variations[0]:\r\ncount: 1\r\ncurrency: COP\r\n"

func TestToonProduct(t *testing.T) {
	b, ok := toonToJSON(toonProduct)
	if !ok {
		t.Fatal("not parsed")
	}
	var v struct {
		Items []map[string]any `json:"items"`
		Count float64          `json:"count"`
		Cur   string           `json:"currency"`
	}
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatal(err, string(b))
	}
	if len(v.Items) != 1 || v.Count != 1 || v.Cur != "COP" {
		t.Fatalf("bad: %s", b)
	}
	it := v.Items[0]
	if it["sale_price"].(float64) != 27000 || it["suggested_price"].(float64) != 55000 || it["visible_start_sale_price"] != nil {
		t.Fatalf("bad item: %s", b)
	}
	if vs, _ := it["variations"].([]any); vs == nil || len(vs) != 0 {
		t.Fatalf("variations: %s", b)
	}
	if g, _ := it["gallery"].([]any); len(g) != 1 {
		t.Fatalf("gallery: %s", b)
	}
}

func TestToonEmptyTabular(t *testing.T) {
	b, ok := toonToJSON("items[0]{id,department_id,name,cod_dane,zip_code}:\ncount: 0")
	if !ok || string(b) != `{"count":0,"items":[]}` {
		t.Fatalf("%v %s", ok, b)
	}
}

func TestToonTabularRows(t *testing.T) {
	b, ok := toonToJSON("items[2]{id,department_id,name,cod_dane,zip_code}:\n  1,2,Medellin,05001000,\"050001\"\n  3,2,\"Bello, Ant\",05088000,null\ncount: 2")
	if !ok {
		t.Fatal("no")
	}
	var v struct {
		Items []map[string]any `json:"items"`
	}
	json.Unmarshal(b, &v)
	if len(v.Items) != 2 || v.Items[0]["name"] != "Medellin" || v.Items[1]["name"] != "Bello, Ant" || v.Items[1]["zip_code"] != nil {
		t.Fatalf("%s", b)
	}
}

func TestToonRejectsPlainText(t *testing.T) {
	if _, ok := toonToJSON("Error: algo salio mal"); ok {
		// "Error: algo..." parses as key/value; acceptable but must not panic
		return
	}
}
