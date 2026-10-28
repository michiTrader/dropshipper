package settings

// Preset is a ready-made provider template shown in the panel and the CLI.
type Preset struct {
	ID      string `json:"id"`
	Name    string `json:"nombre"`
	BaseURL string `json:"base_url"`
	Model   string `json:"modelo"`
	NoKey   bool   `json:"sin_clave"`
	KeyHelp string `json:"ayuda_clave"`
}

// Presets lists the providers the Motor knows how to talk to. Any other
// OpenAI-compatible service works with the "custom" preset (URL + model + key).
var Presets = []Preset{
	{"deepseek", "DeepSeek", "https://api.deepseek.com", "deepseek-chat", false,
		"Crea la clave en platform.deepseek.com > API keys. Empieza por sk-."},
	{"openai", "OpenAI", "https://api.openai.com/v1", "gpt-4o-mini", false,
		"Crea la clave en platform.openai.com > API keys."},
	{"openrouter", "OpenRouter", "https://openrouter.ai/api/v1", "openai/gpt-4o-mini", false,
		"Crea la clave en openrouter.ai > Keys. Un solo proveedor con muchos modelos."},
	{"groq", "Groq", "https://api.groq.com/openai/v1", "llama-3.3-70b-versatile", false,
		"Crea la clave en console.groq.com > API Keys."},
	{"gemini", "Google Gemini", "https://generativelanguage.googleapis.com/v1beta/openai", "gemini-2.0-flash", false,
		"Crea la clave en aistudio.google.com > Get API key."},
	{"ollama", "Ollama (local)", "http://localhost:11434/v1", "llama3.1", true,
		"No necesita clave: corre en tu propio computador."},
	{"custom", "Otro proveedor (compatible con OpenAI)", "", "", false,
		"Pega la dirección base (URL), el nombre del modelo y la clave que te dé tu proveedor."},
}

// PresetByID returns a preset.
func PresetByID(id string) (Preset, bool) {
	for _, p := range Presets {
		if p.ID == id {
			return p, true
		}
	}
	return Preset{}, false
}
