package dropi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func fake(t *testing.T) (*Client, *[]string) {
	t.Helper()
	var hits []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits = append(hits, r.Method+" "+r.URL.Path)
		if r.Header.Get("dropi-integration-key") != "tok-ok" {
			w.WriteHeader(401)
			_, _ = w.Write([]byte(`{"isSuccess":false,"message":"Access denied"}`))
			return
		}
		switch r.URL.Path {
		case "/department":
			_, _ = w.Write([]byte(`{"isSuccess":true,"objects":[{"id":1,"name":"Antioquia"},{"id":2,"name":"Cundinamarca"}]}`))
		case "/orders/cotizaEnvioTransportadoraV2":
			var in map[string]any
			_ = json.NewDecoder(r.Body).Decode(&in)
			if in["ciudad_destino"].(map[string]any)["cod_dane"] != "0500100" {
				_, _ = w.Write([]byte(`{"isSuccess":true,"objects":[]}`))
				return
			}
			_, _ = w.Write([]byte(`{"isSuccess":true,"objects":[
				{"distribution_company":{"id":1,"name":"COORDINADORA"},"shipping_amount":14500},
				{"distribution_company":{"id":2,"name":"ENVIA"},"shipping_amount":"12,900"},
				{"distribution_company":{"id":3,"name":"TCC"},"shipping_amount":0}]}`))
		case "/products/v2/77":
			_, _ = w.Write([]byte(`{"isSuccess":true,"objects":{"id":77,"name":"Gomitas","sale_price":"21000","suggested_price":89900,"stock":40}}`))
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(srv.Close)
	return &Client{Base: srv.URL, Token: "tok-ok", HTTP: srv.Client()}, &hits
}

func TestPingAndAuth(t *testing.T) {
	c, _ := fake(t)
	n, err := c.Ping(context.Background())
	if err != nil || n != 2 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	c.Token = "malo"
	_, err = c.Ping(context.Background())
	ae, ok := err.(*APIError)
	if !ok || !ae.IsAuth() {
		t.Fatalf("debía ser error de autenticación: %v", err)
	}
}

func TestQuoteParsesCarriers(t *testing.T) {
	c, _ := fake(t)
	qs, err := c.Quote(context.Background(), QuoteInput{COD: true, Amount: 149900, OriginDANE: "1100100", DestDANE: "0500100"})
	if err != nil {
		t.Fatal(err)
	}
	if len(qs) != 2 || qs[0].Carrier != "COORDINADORA" || qs[0].Freight != 14500 || qs[1].Freight != 12900 {
		t.Fatalf("%+v", qs)
	}
	if _, err := c.Quote(context.Background(), QuoteInput{DestDANE: "9999900"}); err == nil {
		t.Fatal("sin transportadoras debe ser error")
	}
}

func TestParseQuoteShapes(t *testing.T) {
	for name, raw := range map[string]string{
		"lista":     `[{"transportadora":"TCC","flete":9000}]`,
		"data":      `{"data":[{"carrier":"TCC","price":9000}]}`,
		"un objeto": `{"objects":{"distribution_company":{"name":"TCC"},"shipping_amount":9000}}`,
	} {
		qs := parseQuotes([]byte(raw))
		if len(qs) != 1 || qs[0].Carrier != "TCC" || qs[0].Freight != 9000 {
			t.Errorf("%s: %+v", name, qs)
		}
	}
}

func TestProduct(t *testing.T) {
	c, _ := fake(t)
	p, err := c.Product(context.Background(), "77")
	if err != nil || p.Name != "Gomitas" || p.Cost != 21000 || p.SalePrice != 89900 || p.Stock != 40 {
		t.Fatalf("%+v %v", p, err)
	}
	if _, err := c.Product(context.Background(), "1"); err == nil {
		t.Fatal("producto inexistente debe fallar")
	}
}
