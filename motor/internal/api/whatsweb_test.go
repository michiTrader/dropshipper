package api

import "testing"

func waBlock(t *testing.T, e *env) map[string]any {
	t.Helper()
	r := e.do("GET", "/api/estado", nil)
	if r.Code != 200 {
		t.Fatalf("estado: %d %s", r.Code, r.Body)
	}
	return r.json(t)["whatsapp"].(map[string]any)
}

func TestWhatsAppChannelDefaultsToCloud(t *testing.T) {
	e := newEnv(t, "")
	wa := waBlock(t, e)
	if wa["canal"] != "cloud" {
		t.Fatalf("canal = %v", wa["canal"])
	}
	if wa["web"] == nil {
		t.Fatal("estado must include the web block")
	}
}

func TestWhatsAppChannelSwitchKeepsCloudCredentials(t *testing.T) {
	e := newEnv(t, "")
	if r := e.do("PUT", "/api/ajustes/whatsapp", map[string]any{"token": "EAAGtesttoken", "phone_number_id": "1234567890"}); r.Code != 200 {
		t.Fatalf("save cloud: %d %s", r.Code, r.Body)
	}
	if wa := waBlock(t, e); wa["configurado"] != true || wa["canal"] != "cloud" {
		t.Fatalf("cloud: %v", wa)
	}
	if !e.srv.Hub.Available() {
		t.Fatal("cloud client must be available")
	}

	if r := e.do("PUT", "/api/ajustes/whatsapp/canal", map[string]any{"canal": "web"}); r.Code != 200 {
		t.Fatalf("to web: %d %s", r.Code, r.Body)
	}
	wa := waBlock(t, e)
	if wa["canal"] != "web" || wa["nube_configurada"] != true {
		t.Fatalf("web: %v", wa)
	}
	if e.srv.Hub.Available() {
		t.Fatal("with the web channel and no phone linked nothing can be sent")
	}
	if wa["configurado"] != false {
		t.Fatalf("configurado must follow the active channel: %v", wa)
	}

	if r := e.do("PUT", "/api/ajustes/whatsapp/canal", map[string]any{"canal": "cloud"}); r.Code != 200 {
		t.Fatalf("back to cloud: %d %s", r.Code, r.Body)
	}
	if wa := waBlock(t, e); wa["canal"] != "cloud" || wa["configurado"] != true {
		t.Fatalf("cloud again: %v", wa)
	}
	if !e.srv.Hub.Available() {
		t.Fatal("credentials must survive the switch")
	}
}

func TestWhatsAppChannelValidation(t *testing.T) {
	e := newEnv(t, "")
	for _, c := range []string{"", "telegram"} {
		if r := e.do("PUT", "/api/ajustes/whatsapp/canal", map[string]any{"canal": c}); r.Code != 400 {
			t.Errorf("canal %q: got %d", c, r.Code)
		}
	}
}

func TestWhatsAppWebEndpoints(t *testing.T) {
	e := newEnv(t, "")
	r := e.do("GET", "/api/whatsapp/web/estado", nil)
	if r.Code != 200 || r.json(t)["estado"] != "apagado" {
		t.Fatalf("estado: %d %s", r.Code, r.Body)
	}
	if r := e.do("POST", "/api/whatsapp/web/codigo", map[string]any{}); r.Code != 400 {
		t.Fatalf("codigo sin numero: %d", r.Code)
	}
	if r := e.do("POST", "/api/whatsapp/web/vincular", map[string]any{}); r.Code == 200 {
		t.Fatalf("vincular must fail while the web channel is not selected: %s", r.Body)
	}
	if r := e.do("POST", "/api/whatsapp/web/desvincular", map[string]any{}); r.Code != 200 {
		t.Fatalf("desvincular: %d %s", r.Code, r.Body)
	}
}
