package whatsapp

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

const sample = `{"object":"whatsapp_business_account","entry":[{"id":"1","changes":[{"field":"messages","value":{
 "messaging_product":"whatsapp","metadata":{"display_phone_number":"573000000000","phone_number_id":"123"},
 "contacts":[{"profile":{"name":"Laura"},"wa_id":"573001112233"}],
 "messages":[
  {"from":"573001112233","id":"wamid.A","timestamp":"1761600000","type":"text","text":{"body":"Hola, info de las gomitas"},
   "referral":{"headline":"Duerme mejor","body":"LullaBites"}},
  {"from":"573001112233","id":"wamid.B","timestamp":"1761600010","type":"image","image":{"id":"MEDIA1","mime_type":"image/jpeg","caption":"pago"}}
 ],
 "statuses":[{"id":"wamid.OUT","status":"read"}]}}]}]}`

func TestParseWebhook(t *testing.T) {
	msgs, stats, err := ParseWebhook([]byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 2 || len(stats) != 1 {
		t.Fatalf("msgs=%d stats=%d", len(msgs), len(stats))
	}
	if msgs[0].Name != "Laura" || msgs[0].From != "573001112233" || msgs[0].Type != "text" {
		t.Fatalf("%+v", msgs[0])
	}
	if want := "Hola, info de las gomitas\n[Llegó desde el anuncio: Duerme mejor LullaBites]"; msgs[0].Text != want {
		t.Fatalf("text = %q", msgs[0].Text)
	}
	if msgs[1].MediaID != "MEDIA1" || msgs[1].Text != "pago" {
		t.Fatalf("%+v", msgs[1])
	}
	if stats[0].Status != "read" {
		t.Fatalf("%+v", stats[0])
	}
}

func TestVerifySignature(t *testing.T) {
	body := []byte(sample)
	mac := hmac.New(sha256.New, []byte("secreto"))
	mac.Write(body)
	header := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	if !VerifySignature("secreto", body, header) {
		t.Fatal("valid signature rejected")
	}
	if VerifySignature("otro", body, header) || VerifySignature("secreto", body, "sha256=00") || VerifySignature("secreto", body, "") {
		t.Fatal("invalid signature accepted")
	}
	if !VerifySignature("", body, "") {
		t.Fatal("empty secret disables verification")
	}
}
