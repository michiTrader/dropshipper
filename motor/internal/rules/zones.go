// Package rules holds the deterministic business rules: which payment policy a
// destination gets, and whether an order is profitable once returns are priced
// in. These are plain functions with tests, deliberately NOT left to the LLM:
// the model asks, the rules decide, so a persuasive customer cannot talk the AI
// into cash-on-delivery for a red zone.
package rules

import (
	"fmt"
	"math"
	"os"
	"sort"
	"strings"
	"unicode"

	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
	"gopkg.in/yaml.v3"
)

// Risk levels, from lowest to highest.
const (
	RiskNormal = "normal"
	RiskFar    = "lejano"
	RiskHard   = "dificil_acceso"
	RiskRed    = "zona_roja"
)

// Payment policies.
const (
	PayCOD         = "contraentrega"
	PayPartial     = "anticipo_parcial"
	PayFullAdvance = "anticipado_total"
)

var riskRank = map[string]int{RiskNormal: 0, RiskFar: 1, RiskHard: 2, RiskRed: 3}

// Policy is what a risk level implies for payment.
type Policy struct {
	Pago               string  `yaml:"pago" json:"pago"`
	AnticipoPorcentaje float64 `yaml:"anticipo_porcentaje" json:"anticipo_porcentaje"`
	AnticipoMinimo     int64   `yaml:"anticipo_minimo" json:"anticipo_minimo"`
	ExplicacionCliente string  `yaml:"explicacion_cliente" json:"explicacion_cliente"`
}

type deptRule struct {
	Riesgo   string            `yaml:"riesgo"`
	Ciudades map[string]string `yaml:"ciudades"`
}

// ZoneRules is the parsed zonas.yaml.
type ZoneRules struct {
	Politicas       map[string]Policy   `yaml:"politicas"`
	Transportadoras map[string][]string `yaml:"transportadoras"`
	PorDefecto      string              `yaml:"por_defecto"`
	Departamentos   map[string]deptRule `yaml:"departamentos"`
	Barrios         map[string]string   `yaml:"barrios"`

	depts   map[string]deptRule
	barrios map[string]string
}

// LoadZones reads and validates zonas.yaml.
func LoadZones(path string) (*ZoneRules, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("leer reglas de zona: %w", err)
	}
	return ParseZones(raw)
}

// ParseZones parses zone rules from YAML.
func ParseZones(raw []byte) (*ZoneRules, error) {
	var z ZoneRules
	if err := yaml.Unmarshal(raw, &z); err != nil {
		return nil, fmt.Errorf("zonas.yaml inválido: %w", err)
	}
	if z.PorDefecto == "" {
		z.PorDefecto = RiskFar
	}
	for _, lvl := range []string{RiskNormal, RiskFar, RiskHard, RiskRed} {
		if _, ok := z.Politicas[lvl]; !ok {
			return nil, fmt.Errorf("zonas.yaml: falta la política %q", lvl)
		}
	}
	check := func(where, lvl string) error {
		if _, ok := riskRank[lvl]; !ok {
			return fmt.Errorf("zonas.yaml: nivel de riesgo desconocido %q en %s", lvl, where)
		}
		return nil
	}
	if err := check("por_defecto", z.PorDefecto); err != nil {
		return nil, err
	}
	z.depts = map[string]deptRule{}
	for name, d := range z.Departamentos {
		if d.Riesgo == "" {
			d.Riesgo = z.PorDefecto
		}
		if err := check("departamento "+name, d.Riesgo); err != nil {
			return nil, err
		}
		cities := map[string]string{}
		for c, lvl := range d.Ciudades {
			if err := check("ciudad "+c, lvl); err != nil {
				return nil, err
			}
			cities[Normalize(c)] = lvl
		}
		d.Ciudades = cities
		z.depts[Normalize(name)] = d
	}
	z.barrios = map[string]string{}
	for key, lvl := range z.Barrios {
		if err := check("barrio "+key, lvl); err != nil {
			return nil, err
		}
		city, hood, ok := strings.Cut(key, "|")
		if !ok {
			return nil, fmt.Errorf("zonas.yaml: barrio %q debe tener formato \"ciudad|barrio\"", key)
		}
		z.barrios[Normalize(city)+"|"+Normalize(hood)] = lvl
	}
	return &z, nil
}

// SetBarrio adds or replaces a neighbourhood rule at runtime.
func (z *ZoneRules) SetBarrio(city, hood, risk string) error {
	if !ValidRisk(risk) {
		return fmt.Errorf("nivel de riesgo desconocido %q", risk)
	}
	z.barrios[Normalize(city)+"|"+Normalize(hood)] = risk
	return nil
}

// Destination is where an order goes.
type Destination struct {
	Department   string
	City         string
	Neighborhood string
}

// Decision is the payment decision for one order.
type Decision struct {
	Risk                string   `json:"riesgo"`
	Policy              string   `json:"politica_pago"`
	AdvanceAmount       int64    `json:"anticipo"`
	CODAmount           int64    `json:"contraentrega"`
	Carriers            []string `json:"transportadoras_sugeridas"`
	Reason              string   `json:"motivo"`
	CustomerExplanation string   `json:"explicacion_cliente"`
	Matched             bool     `json:"zona_reconocida"`
}

// Decide applies the most specific matching rule (barrio > ciudad >
// departamento > default) and computes the advance for the order total.
//
// An unknown department falls to the default (never to COD): the safe failure
// for a cash-on-delivery business is asking for an advance, not shipping blind.
func (z *ZoneRules) Decide(dst Destination, total int64) Decision {
	city, hood := Normalize(dst.City), Normalize(dst.Neighborhood)
	d := Decision{Risk: z.PorDefecto, Reason: "departamento no reconocido: se aplica la regla por defecto"}

	if rule, ok := z.lookupDept(Normalize(dst.Department)); ok {
		d.Risk, d.Matched = rule.Riesgo, true
		d.Reason = "regla del departamento " + dst.Department
		if lvl, ok := rule.Ciudades[city]; ok {
			d.Risk = lvl
			d.Reason = "regla del municipio " + dst.City
		}
	}
	if lvl, ok := z.barrios[city+"|"+hood]; ok && hood != "" {
		d.Risk = lvl
		d.Matched = true
		d.Reason = "regla del barrio " + dst.Neighborhood + " (" + dst.City + ")"
	}

	p := z.Politicas[d.Risk]
	d.Policy = p.Pago
	d.CustomerExplanation = strings.TrimSpace(p.ExplicacionCliente)
	d.Carriers = append([]string(nil), z.Transportadoras[d.Risk]...)
	d.AdvanceAmount, d.CODAmount = Advance(p, total)
	return d
}

func (z *ZoneRules) lookupDept(dept string) (deptRule, bool) {
	if dept == "" {
		return deptRule{}, false
	}
	if r, ok := z.depts[dept]; ok {
		return r, true
	}
	// Tolerate "Antioquia - Colombia", "Dpto. de Antioquia", etc. Longest key
	// first so "norte de santander" wins over "santander".
	keys := make([]string, 0, len(z.depts))
	for k := range z.depts {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return len(keys[i]) > len(keys[j]) })
	for _, k := range keys {
		if strings.Contains(" "+dept+" ", " "+k+" ") {
			return z.depts[k], true
		}
	}
	return deptRule{}, false
}

// Advance computes the advance and the remaining cash-on-delivery amount.
// Advances are rounded UP to the next $1.000 (easy to transfer, never short).
func Advance(p Policy, total int64) (advance, cod int64) {
	switch p.Pago {
	case PayCOD:
		return 0, total
	case PayFullAdvance:
		return total, 0
	}
	adv := int64(math.Ceil(float64(total)*p.AnticipoPorcentaje/1000.0-1e-9)) * 1000
	if adv < p.AnticipoMinimo {
		adv = p.AnticipoMinimo
	}
	if adv > total {
		adv = total
	}
	return adv, total - adv
}

// ValidRisk reports whether s is a known risk level.
func ValidRisk(s string) bool { _, ok := riskRank[s]; return ok }

// PolicyFor exposes the policy of a risk level.
func (z *ZoneRules) PolicyFor(risk string) Policy { return z.Politicas[risk] }

// Normalize lowercases, strips accents and collapses spaces so "Medellín",
// "MEDELLIN" and " medellin " compare equal.
func Normalize(s string) string {
	t := transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC)
	out, _, err := transform.String(t, s)
	if err != nil {
		out = s
	}
	out = strings.ToLower(out)
	out = strings.NewReplacer(".", " ", ",", " ", "-", " ", "_", " ").Replace(out)
	return strings.Join(strings.Fields(out), " ")
}
