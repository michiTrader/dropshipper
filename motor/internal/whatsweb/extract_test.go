package whatsweb

import (
	"strings"
	"testing"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"
)

func TestExtractText(t *testing.T) {
	c, ok := extract(&waE2E.Message{Conversation: proto.String("hola")})
	if !ok || c.Kind != "text" || c.Text != "hola" {
		t.Fatalf("got %+v ok=%v", c, ok)
	}
	c, ok = extract(&waE2E.Message{ExtendedTextMessage: &waE2E.ExtendedTextMessage{Text: proto.String("quiero el producto")}})
	if !ok || c.Text != "quiero el producto" {
		t.Fatalf("got %+v", c)
	}
}

func TestExtractAd(t *testing.T) {
	c, ok := extract(&waE2E.Message{ExtendedTextMessage: &waE2E.ExtendedTextMessage{
		Text: proto.String("info"),
		ContextInfo: &waE2E.ContextInfo{ExternalAdReply: &waE2E.ContextInfo_ExternalAdReplyInfo{
			Title: proto.String("Masajeador"), Body: proto.String("Envío gratis"),
		}},
	}})
	if !ok || !strings.Contains(c.Text, "Llegó desde el anuncio: Masajeador Envío gratis") {
		t.Fatalf("got %q", c.Text)
	}
}

func TestExtractMedia(t *testing.T) {
	c, ok := extract(&waE2E.Message{ImageMessage: &waE2E.ImageMessage{Caption: proto.String("comprobante"), Mimetype: proto.String("image/jpeg"), FileLength: proto.Uint64(10)}})
	if !ok || c.Kind != "image" || c.Text != "comprobante" || c.Media == nil || c.Mime != "image/jpeg" {
		t.Fatalf("image: %+v", c)
	}
	c, ok = extract(&waE2E.Message{AudioMessage: &waE2E.AudioMessage{Mimetype: proto.String("audio/ogg")}})
	if !ok || c.Kind != "audio" || c.Media == nil {
		t.Fatalf("audio: %+v", c)
	}
	c, ok = extract(&waE2E.Message{DocumentMessage: &waE2E.DocumentMessage{FileName: proto.String("pago.pdf")}})
	if !ok || c.Kind != "document" || c.Text != "pago.pdf" {
		t.Fatalf("doc: %+v", c)
	}
	c, ok = extract(&waE2E.Message{LocationMessage: &waE2E.LocationMessage{Name: proto.String("Casa"), DegreesLatitude: proto.Float64(4.5), DegreesLongitude: proto.Float64(-74.1)}})
	if !ok || c.Kind != "location" || !strings.Contains(c.Text, "Casa") {
		t.Fatalf("loc: %+v", c)
	}
}

func TestExtractIgnored(t *testing.T) {
	if _, ok := extract(nil); ok {
		t.Fatal("nil must be ignored")
	}
	if _, ok := extract(&waE2E.Message{ReactionMessage: &waE2E.ReactionMessage{}}); ok {
		t.Fatal("reaction must be ignored")
	}
	if _, ok := extract(&waE2E.Message{}); ok {
		t.Fatal("empty must be ignored")
	}
}

func TestParseTo(t *testing.T) {
	cases := map[string]string{
		"573001234567":                 "573001234567@s.whatsapp.net",
		"3001234567":                   "573001234567@s.whatsapp.net",
		"+57 300 123 4567":             "573001234567@s.whatsapp.net",
		"12345@lid":                    "12345@lid",
		"5215512345678@s.whatsapp.net": "5215512345678@s.whatsapp.net",
	}
	for in, want := range cases {
		got, err := ParseTo(in)
		if err != nil || got.String() != want {
			t.Errorf("ParseTo(%q) = %v, %v; want %s", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "abc", "123"} {
		if _, err := ParseTo(bad); err == nil {
			t.Errorf("ParseTo(%q) should fail", bad)
		}
	}
}

func TestIsCustomerChat(t *testing.T) {
	if !isCustomerChat(types.NewJID("573001234567", types.DefaultUserServer)) || !isCustomerChat(types.NewJID("99", types.HiddenUserServer)) {
		t.Fatal("one-to-one chats must be accepted")
	}
	if isCustomerChat(types.NewJID("123-456", types.GroupServer)) || isCustomerChat(types.StatusBroadcastJID) {
		t.Fatal("groups and status must be ignored")
	}
}

func TestPhoneKey(t *testing.T) {
	pn := types.NewJID("573001234567", types.DefaultUserServer)
	lid := types.NewJID("9988", types.HiddenUserServer)
	if k := phoneKey(pn, types.EmptyJID, types.EmptyJID); k != "573001234567" {
		t.Errorf("pn: %q", k)
	}
	if k := phoneKey(lid, types.EmptyJID, pn); k != "573001234567" {
		t.Errorf("resolved: %q", k)
	}
	if k := phoneKey(lid, pn, types.EmptyJID); k != "573001234567" {
		t.Errorf("alt: %q", k)
	}
	if k := phoneKey(lid, types.EmptyJID, types.EmptyJID); k != "9988@lid" {
		t.Errorf("lid fallback: %q", k)
	}
}
