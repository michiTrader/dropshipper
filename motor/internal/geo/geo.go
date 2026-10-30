// Package geo resolves Colombian department/city names to DANE (DIVIPOLA)
// codes. Dropi's shipping quote needs the municipality code (5 digits,
// sent padded to the 7-digit form its API documents).
package geo

import (
	_ "embed"
	"strings"
	"sync"
	"unicode"

	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

//go:embed divipola.tsv
var raw string

// Municipality is one row of DIVIPOLA.
type Municipality struct {
	Code       string // 5 digits, e.g. 05001
	Department string
	Name       string
}

// DANE7 is the 7-digit code Dropi expects (5-digit code + "00").
func (m Municipality) DANE7() string { return m.Code + "00" }

var (
	once    sync.Once
	byCity  map[string][]Municipality // normalized city -> candidates
	byCode  map[string]Municipality
	deptKey map[string]string // normalized department -> 2-digit code
)

// Normalize lowercases, strips accents and punctuation.
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

func load() {
	byCity = map[string][]Municipality{}
	byCode = map[string]Municipality{}
	deptKey = map[string]string{}
	for _, line := range strings.Split(raw, "\n") {
		f := strings.Split(strings.TrimSpace(line), "\t")
		if len(f) != 3 {
			continue
		}
		m := Municipality{Code: f[0], Department: f[1], Name: f[2]}
		byCode[m.Code] = m
		deptKey[Normalize(m.Department)] = m.Code[:2]
		k := Normalize(m.Name)
		byCity[k] = append(byCity[k], m)
	}
	// Common ways people write big cities.
	alias := map[string]string{
		"bogota": "11001", "bogota dc": "11001", "bogota d c": "11001",
		"cartagena": "13001", "cartagena de indias": "13001",
		"santa marta": "47001", "buga": "76111", "tulua": "76834",
	}
	for k, code := range alias {
		if m, ok := byCode[code]; ok && len(byCity[k]) == 0 {
			byCity[k] = []Municipality{m}
		}
	}
	deptKey["bogota"] = "11"
	deptKey["bogota dc"] = "11"
	deptKey["san andres"] = "88"
	deptKey["san andres y providencia"] = "88"
	deptKey["san andres providencia y santa catalina"] = "88"
}

// Count is how many municipalities are known (for tests / status).
func Count() int { once.Do(load); return len(byCode) }

// Find resolves a city (and optional department) to a municipality. When the
// name is ambiguous (e.g. "La Unión") the department decides; without a
// department the first candidate wins.
func Find(department, city string) (Municipality, bool) {
	once.Do(load)
	c := Normalize(city)
	// Bogotá is a capital district, but customers (and Dropi) often file it
	// under Cundinamarca: it resolves whatever department was typed.
	if c == "bogota" || c == "bogota dc" || c == "bogota d c" {
		return byCode["11001"], true
	}
	cands := byCity[c]
	if len(cands) == 0 {
		return Municipality{}, false
	}
	if d := Normalize(department); d != "" {
		prefix := deptKey[d]
		if prefix == "" {
			for k, v := range deptKey {
				if strings.Contains(d, k) || strings.Contains(k, d) {
					prefix = v
					break
				}
			}
		}
		if prefix != "" {
			for _, m := range cands {
				if m.Code[:2] == prefix {
					return m, true
				}
			}
			return Municipality{}, false
		}
	}
	return cands[0], true
}

// ByCode returns a municipality from its 5- or 7-digit code.
func ByCode(code string) (Municipality, bool) {
	once.Do(load)
	if len(code) >= 5 {
		code = code[:5]
	}
	m, ok := byCode[code]
	return m, ok
}
