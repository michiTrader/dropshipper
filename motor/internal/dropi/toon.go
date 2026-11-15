package dropi

import (
	"encoding/json"
	"strconv"
	"strings"
)

// Dropi's MCP answers in TOON (Token-Oriented Object Notation), a compact
// indentation-based text format, not in JSON:
//
//	items[1]:
//	  - id: 2022974
//	    name: Lullabites
//	    categories[1]: Salud
//	    gallery[1]{urlS3}:
//	      "https://..."
//	    variations[0]:
//	count: 1
//
// toonToJSON converts such text into JSON so the rest of the adapter can keep
// using encoding/json. ok is false when the text does not look like TOON.
func toonToJSON(text string) ([]byte, bool) {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	var lines []toonLine
	for _, raw := range strings.Split(text, "\n") {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		ind := len(raw) - len(strings.TrimLeft(raw, " "))
		lines = append(lines, toonLine{indent: ind, text: strings.TrimSpace(raw)})
	}
	if len(lines) == 0 {
		return nil, false
	}
	p := &toonParser{lines: lines}
	var v any
	if _, _, isKV := splitKey(lines[0].text); !isKV && !strings.HasPrefix(lines[0].text, "- ") {
		// A single scalar, or something that is not TOON at all.
		return nil, false
	}
	v = p.object(lines[0].indent)
	if p.err || p.pos < len(p.lines) {
		return nil, false
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, false
	}
	return b, true
}

type toonLine struct {
	indent int
	text   string
}

type toonParser struct {
	lines []toonLine
	pos   int
	err   bool
}

// header is a parsed `name[N]{a,b}:` key.
type toonHeader struct {
	name   string
	isList bool
	n      int
	fields []string
}

// splitKey splits `key: rest` / `key:` / `key[N]{..}: rest`. The key may be quoted.
func splitKey(s string) (key string, rest string, ok bool) {
	i := 0
	if strings.HasPrefix(s, "\"") {
		j := closingQuote(s)
		if j < 0 {
			return "", "", false
		}
		i = j + 1
	}
	// find the first ':' outside brackets/braces after the key part
	depth := 0
	for ; i < len(s); i++ {
		switch s[i] {
		case '[', '{':
			depth++
		case ']', '}':
			depth--
		case ':':
			if depth == 0 {
				return strings.TrimSpace(s[:i]), strings.TrimSpace(s[i+1:]), true
			}
		}
	}
	return "", "", false
}

func closingQuote(s string) int {
	for i := 1; i < len(s); i++ {
		if s[i] == '\\' {
			i++
			continue
		}
		if s[i] == '"' {
			return i
		}
	}
	return -1
}

func parseHeader(key string) toonHeader {
	h := toonHeader{name: key}
	name := key
	if strings.HasPrefix(name, "\"") {
		if j := closingQuote(name); j > 0 {
			rest := name[j+1:]
			if u, err := strconv.Unquote(name[:j+1]); err == nil {
				h.name = u
			} else {
				h.name = name[1:j]
			}
			name = rest
			key = rest
			if rest == "" {
				return h
			}
		}
	} else if b := strings.IndexAny(name, "[{"); b >= 0 {
		h.name = name[:b]
		name = name[b:]
	} else {
		return h
	}
	if strings.HasPrefix(name, "[") {
		if e := strings.Index(name, "]"); e > 0 {
			num := strings.TrimSpace(strings.Trim(name[1:e], "#|\t"))
			if n, err := strconv.Atoi(num); err == nil {
				h.isList = true
				h.n = n
			}
			name = name[e+1:]
		}
	}
	if strings.HasPrefix(name, "{") {
		if e := strings.Index(name, "}"); e > 0 {
			for _, f := range splitCells(name[1:e]) {
				h.fields = append(h.fields, unquoteCell(f))
			}
		}
	}
	return h
}

func splitCells(s string) []string {
	var out []string
	var cur strings.Builder
	inQ := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '\\' && inQ && i+1 < len(s):
			cur.WriteByte(c)
			i++
			cur.WriteByte(s[i])
		case c == '"':
			inQ = !inQ
			cur.WriteByte(c)
		case c == ',' && !inQ:
			out = append(out, strings.TrimSpace(cur.String()))
			cur.Reset()
		default:
			cur.WriteByte(c)
		}
	}
	out = append(out, strings.TrimSpace(cur.String()))
	return out
}

func unquoteCell(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "\"") && strings.HasSuffix(s, "\"") && len(s) >= 2 {
		if u, err := strconv.Unquote(s); err == nil {
			return u
		}
		return s[1 : len(s)-1]
	}
	return s
}

func scalar(s string) any {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "\"") && strings.HasSuffix(s, "\"") && len(s) >= 2 {
		return unquoteCell(s)
	}
	switch s {
	case "null":
		return nil
	case "true":
		return true
	case "false":
		return false
	case "":
		return ""
	}
	if n, err := strconv.ParseInt(s, 10, 64); err == nil && !(len(s) > 1 && s[0] == '0') {
		return n
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil && !(len(s) > 1 && s[0] == '0' && s[1] != '.') {
		return f
	}
	return s
}

// object parses consecutive `key: value` lines at the given indent.
func (p *toonParser) object(indent int) map[string]any {
	obj := map[string]any{}
	for p.pos < len(p.lines) {
		ln := p.lines[p.pos]
		if ln.indent < indent {
			break
		}
		if ln.indent > indent || strings.HasPrefix(ln.text, "- ") || ln.text == "-" {
			p.err = true
			return obj
		}
		key, rest, ok := splitKey(ln.text)
		if !ok {
			p.err = true
			return obj
		}
		p.pos++
		p.field(obj, key, rest, indent)
		if p.err {
			return obj
		}
	}
	return obj
}

// field stores the value of `key: rest` (and its nested block) into obj.
func (p *toonParser) field(obj map[string]any, key, rest string, indent int) {
	h := parseHeader(key)
	switch {
	case h.isList && len(h.fields) > 0: // tabular
		rows := []any{}
		for len(rows) < h.n && p.pos < len(p.lines) && p.lines[p.pos].indent > indent {
			cells := splitCells(p.lines[p.pos].text)
			p.pos++
			row := map[string]any{}
			for i, f := range h.fields {
				if i < len(cells) {
					row[f] = scalar(cells[i])
				} else {
					row[f] = nil
				}
			}
			rows = append(rows, row)
		}
		obj[h.name] = rows
	case h.isList && rest != "": // inline primitives
		arr := []any{}
		for _, c := range splitCells(rest) {
			arr = append(arr, scalar(c))
		}
		obj[h.name] = arr
	case h.isList: // list of "- " items (possibly empty)
		arr := []any{}
		for p.pos < len(p.lines) && p.lines[p.pos].indent > indent {
			ln := p.lines[p.pos]
			if !strings.HasPrefix(ln.text, "- ") && ln.text != "-" {
				break
			}
			arr = append(arr, p.listItem(ln.indent))
			if p.err {
				return
			}
		}
		obj[h.name] = arr
	case rest != "":
		obj[h.name] = scalar(rest)
	default: // nested object (or empty)
		if p.pos < len(p.lines) && p.lines[p.pos].indent > indent {
			obj[h.name] = p.object(p.lines[p.pos].indent)
		} else {
			obj[h.name] = map[string]any{}
		}
	}
}

// listItem parses one `- ...` entry whose dash sits at the given indent.
func (p *toonParser) listItem(indent int) any {
	ln := p.lines[p.pos]
	body := strings.TrimSpace(strings.TrimPrefix(ln.text, "-"))
	p.pos++
	if body == "" {
		if p.pos < len(p.lines) && p.lines[p.pos].indent > indent {
			return p.object(p.lines[p.pos].indent)
		}
		return map[string]any{}
	}
	key, rest, ok := splitKey(body)
	if !ok {
		return scalar(body)
	}
	obj := map[string]any{}
	// Fields of the item are aligned with the first key (indent + 2).
	fieldIndent := indent + 2
	p.field(obj, key, rest, fieldIndent)
	if p.err {
		return obj
	}
	for p.pos < len(p.lines) && p.lines[p.pos].indent >= fieldIndent {
		l := p.lines[p.pos]
		if l.indent > fieldIndent || strings.HasPrefix(l.text, "- ") {
			p.err = true
			return obj
		}
		k, r, ok := splitKey(l.text)
		if !ok {
			p.err = true
			return obj
		}
		p.pos++
		p.field(obj, k, r, fieldIndent)
		if p.err {
			return obj
		}
	}
	return obj
}
