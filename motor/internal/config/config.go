// Package config loads the Motor configuration.
//
// Secrets (API keys, tokens) come ONLY from environment variables (or a .env
// file next to the binary). Business settings (persona, payment methods, auto
// approval rules) live in negocio.yaml so they can be edited without touching
// code and without leaking secrets into git.
package config

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is the full runtime configuration.
type Config struct {
	Port         int
	DataDir      string
	KnowledgeDir string
	AdminToken   string
	PublicURL    string

	LLM      LLMConfig
	WhatsApp WhatsAppConfig
	Wompi    WompiConfig
	Negocio  Negocio
}

// LLMConfig points to any OpenAI-compatible chat completions endpoint.
// DeepSeek is the default: https://api.deepseek.com with model deepseek-chat.
type LLMConfig struct {
	BaseURL string
	APIKey  string
	Model   string
	// Temperature nil = do not send it (some models only accept the default).
	// Set LLM_TEMPERATURE=omit to leave it out.
	Temperature *float64
}

// WhatsAppConfig holds the WhatsApp Business Cloud API credentials.
type WhatsAppConfig struct {
	Token         string `json:"token"`
	PhoneNumberID string `json:"phone_number_id"`
	VerifyToken   string `json:"verify_token"`
	AppSecret     string `json:"app_secret"`
	GraphVersion  string `json:"graph_version"`
}

// Enabled reports whether real WhatsApp sending is configured.
func (w WhatsAppConfig) Enabled() bool { return w.Token != "" && w.PhoneNumberID != "" }

// WompiConfig holds Wompi (Bancolombia) credentials for automatic payment links.
type WompiConfig struct {
	PublicKey    string `json:"public_key"`
	PrivateKey   string `json:"private_key"`
	EventsSecret string `json:"events_secret"`
	Sandbox      bool   `json:"sandbox"`
}

// Enabled reports whether automatic Wompi payment links can be created.
func (w WompiConfig) Enabled() bool { return w.PrivateKey != "" }

// Negocio is the editable business configuration (negocio.yaml).
type Negocio struct {
	NombreTienda string        `yaml:"nombre_tienda" json:"nombre_tienda"`
	Ciudad       string        `yaml:"ciudad" json:"ciudad"`
	Persona      Persona       `yaml:"persona" json:"persona"`
	Pagos        Pagos         `yaml:"pagos" json:"pagos"`
	Autonomia    Autonomia     `yaml:"autonomia" json:"autonomia"`
	Conversacion Conversacion  `yaml:"conversacion" json:"conversacion"`
	Finanzas     FinanzasConf  `yaml:"finanzas" json:"finanzas"`
	Dropi        DropiConf     `yaml:"dropi" json:"dropi"`
	Notificar    NotificarConf `yaml:"notificar" json:"notificar"`
}

// Persona describes how the seller agent presents itself.
type Persona struct {
	Nombre string `yaml:"nombre" json:"nombre"`
	Rol    string `yaml:"rol" json:"rol"`
	Tono   string `yaml:"tono" json:"tono"`
}

// Pagos configures how advance payments are collected.
type Pagos struct {
	// Metodo: "manual" (llave Bre-B / cuenta Bancolombia + comprobante) or
	// "wompi" (link de pago con confirmación automática).
	Metodo string     `yaml:"metodo" json:"metodo"`
	Manual PagoManual `yaml:"manual" json:"manual"`
	// HorasParaPagar: after this, an unpaid order is flagged for follow-up.
	HorasParaPagar int `yaml:"horas_para_pagar" json:"horas_para_pagar"`
}

// PagoManual are the payment instructions shown to the customer.
type PagoManual struct {
	LlaveBreB     string `yaml:"llave_bre_b" json:"llave_bre_b"`
	Banco         string `yaml:"banco" json:"banco"`
	TipoCuenta    string `yaml:"tipo_cuenta" json:"tipo_cuenta"`
	NumeroCuenta  string `yaml:"numero_cuenta" json:"numero_cuenta"`
	Titular       string `yaml:"titular" json:"titular"`
	Instrucciones string `yaml:"instrucciones" json:"instrucciones"`
}

// Autonomia controls what the AI may do without the owner pressing "Aceptar".
type Autonomia struct {
	// AutoAprobarPedidos: level 2. Off by default: every order waits for the owner.
	AutoAprobarPedidos bool  `yaml:"auto_aprobar_pedidos" json:"auto_aprobar_pedidos"`
	AutoMaxValor       int64 `yaml:"auto_max_valor" json:"auto_max_valor"`
	AutoSoloCOD        bool  `yaml:"auto_solo_contraentrega" json:"auto_solo_contraentrega"`
	AutoSoloRiesgoBajo bool  `yaml:"auto_solo_riesgo_normal" json:"auto_solo_riesgo_normal"`
}

// Conversacion tunes the chat behaviour.
type Conversacion struct {
	// EsperaAgruparSeg: customers often send several short messages in a row;
	// the agent waits this long after the last one before answering.
	EsperaAgruparSeg int `yaml:"espera_agrupar_seg" json:"espera_agrupar_seg"`
	// PausaEntreMensajesMs: delay between the bubbles of one answer (feels human).
	PausaEntreMensajesMs int `yaml:"pausa_entre_mensajes_ms" json:"pausa_entre_mensajes_ms"`
	HistorialMaximo      int `yaml:"historial_maximo" json:"historial_maximo"`
}

// FinanzasConf holds the default economic assumptions.
type FinanzasConf struct {
	TasaDevolucion   float64 `yaml:"tasa_devolucion" json:"tasa_devolucion"`
	TRM              float64 `yaml:"trm_cop_usd" json:"trm_cop_usd"`
	MargenSeguridad  float64 `yaml:"margen_seguridad" json:"margen_seguridad"`
	TasaConfirmacion float64 `yaml:"tasa_confirmacion" json:"tasa_confirmacion"`
}

// DropiConf: until API access is granted, orders are uploaded by hand.
type DropiConf struct {
	Modo string `yaml:"modo" json:"modo"` // "manual" | "api"
}

// NotificarConf: the owner's own WhatsApp number to receive alerts.
type NotificarConf struct {
	WhatsAppDueno string `yaml:"whatsapp_dueno" json:"whatsapp_dueno"`
}

// Load reads .env (optional), environment variables and negocio.yaml.
func Load(baseDir string) (*Config, error) {
	loadDotEnv(filepath.Join(baseDir, ".env"))

	cfg := &Config{
		Port:         envInt("PORT", 8080),
		DataDir:      env("DATA_DIR", filepath.Join(baseDir, "data")),
		KnowledgeDir: env("KNOWLEDGE_DIR", filepath.Join(baseDir, "knowledge")),
		AdminToken:   env("ADMIN_TOKEN", ""),
		PublicURL:    strings.TrimRight(env("PUBLIC_URL", ""), "/"),
		LLM: LLMConfig{
			BaseURL:     strings.TrimRight(env("LLM_BASE_URL", "https://api.deepseek.com"), "/"),
			APIKey:      firstNonEmpty(os.Getenv("LLM_API_KEY"), os.Getenv("DEEPSEEK_API_KEY")),
			Model:       env("LLM_MODEL", "deepseek-chat"),
			Temperature: envTemperature("LLM_TEMPERATURE", 0.7),
		},
		WhatsApp: WhatsAppConfig{
			Token:         env("WHATSAPP_TOKEN", ""),
			PhoneNumberID: env("WHATSAPP_PHONE_NUMBER_ID", ""),
			VerifyToken:   env("WHATSAPP_VERIFY_TOKEN", ""),
			AppSecret:     env("WHATSAPP_APP_SECRET", ""),
			GraphVersion:  env("WHATSAPP_GRAPH_VERSION", "v21.0"),
		},
		Wompi: WompiConfig{
			PublicKey:    env("WOMPI_PUBLIC_KEY", ""),
			PrivateKey:   env("WOMPI_PRIVATE_KEY", ""),
			EventsSecret: env("WOMPI_EVENTS_SECRET", ""),
			Sandbox:      env("WOMPI_ENV", "sandbox") != "production",
		},
	}

	negPath := env("NEGOCIO_FILE", filepath.Join(baseDir, "negocio.yaml"))
	neg, err := LoadNegocio(negPath)
	if err != nil {
		return nil, err
	}
	cfg.Negocio = neg
	return cfg, nil
}

// LoadNegocio reads negocio.yaml, applying defaults for anything missing.
func LoadNegocio(path string) (Negocio, error) {
	n := DefaultNegocio()
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return n, nil
		}
		return n, fmt.Errorf("leer %s: %w", path, err)
	}
	if err := yaml.Unmarshal(raw, &n); err != nil {
		return n, fmt.Errorf("negocio.yaml inválido: %w", err)
	}
	n.ApplyDefaults()
	return n, nil
}

// DefaultNegocio returns safe defaults (human approval for everything).
func DefaultNegocio() Negocio {
	n := Negocio{}
	n.ApplyDefaults()
	return n
}

// ApplyDefaults fills missing values with safe defaults.
func (n *Negocio) ApplyDefaults() {
	if n.NombreTienda == "" {
		n.NombreTienda = "Mi tienda"
	}
	if n.Ciudad == "" {
		n.Ciudad = "Medellín"
	}
	if n.Persona.Nombre == "" {
		n.Persona.Nombre = "Sofi"
	}
	if n.Persona.Rol == "" {
		n.Persona.Rol = "asesora de ventas"
	}
	if n.Pagos.Metodo == "" {
		n.Pagos.Metodo = "manual"
	}
	if n.Pagos.HorasParaPagar == 0 {
		n.Pagos.HorasParaPagar = 24
	}
	if n.Conversacion.EsperaAgruparSeg == 0 {
		n.Conversacion.EsperaAgruparSeg = 4
	}
	if n.Conversacion.PausaEntreMensajesMs == 0 {
		n.Conversacion.PausaEntreMensajesMs = 1200
	}
	if n.Conversacion.HistorialMaximo == 0 {
		n.Conversacion.HistorialMaximo = 40
	}
	if n.Finanzas.TasaDevolucion == 0 {
		n.Finanzas.TasaDevolucion = 0.25
	}
	if n.Finanzas.TRM == 0 {
		n.Finanzas.TRM = 4000
	}
	if n.Finanzas.MargenSeguridad == 0 {
		n.Finanzas.MargenSeguridad = 0.30
	}
	if n.Finanzas.TasaConfirmacion == 0 {
		n.Finanzas.TasaConfirmacion = 0.85
	}
	if n.Dropi.Modo == "" {
		n.Dropi.Modo = "manual"
	}
}

// AgruparDuration is the debounce window as a duration.
func (c Conversacion) AgruparDuration() time.Duration {
	return time.Duration(c.EsperaAgruparSeg) * time.Second
}

func env(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	if v, err := strconv.Atoi(os.Getenv(key)); err == nil {
		return v
	}
	return def
}

func envFloat(key string, def float64) float64 {
	if v, err := strconv.ParseFloat(os.Getenv(key), 64); err == nil {
		return v
	}
	return def
}

func envTemperature(key string, def float64) *float64 {
	raw := strings.TrimSpace(os.Getenv(key))
	if strings.EqualFold(raw, "omit") || strings.EqualFold(raw, "none") {
		return nil
	}
	v := envFloat(key, def)
	return &v
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// loadDotEnv sets variables from a KEY=VALUE file without overriding the real
// environment. Missing file is fine.
func loadDotEnv(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.Trim(strings.TrimSpace(val), `"'`)
		if _, exists := os.LookupEnv(key); !exists {
			os.Setenv(key, val)
		}
	}
}
