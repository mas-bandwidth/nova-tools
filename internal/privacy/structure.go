package privacy

import (
	"regexp"
	"strings"
)

// StructureHit is one configured shape found in a payload: its class, the
// matched text, and whether it refuses or only warns.
type StructureHit struct {
	Class    string
	Specimen string
	Refuse   bool
}

// redactedMark replaces a refused span before the warning patterns run, so
// one specimen is never reported twice.
const redactedMark = "\x00"

// Structure scans a payload for the configured shapes. It needs no corpus, so
// a corpus problem cannot silence it. Refusing patterns run first and their
// matches are redacted; then the warning patterns run. Hits come back in
// pattern order and document order, exact duplicates collapsed, and a
// specimen on the allow list is dropped.
func (r Rules) Structure(payload string) []StructureHit {
	var hits []StructureHit
	add := func(class string, refuse bool, specimens []string) {
		seen := map[string]bool{}
		for _, s := range specimens {
			if seen[s] || r.allowSet[strings.ToLower(s)] {
				continue
			}
			seen[s] = true
			hits = append(hits, StructureHit{Class: class, Specimen: s, Refuse: refuse})
		}
	}
	text := payload
	for _, p := range r.Refuse {
		if m := p.RE.FindAllString(text, -1); len(m) > 0 {
			add(p.Class, true, m)
			text = p.RE.ReplaceAllLiteralString(text, redactedMark)
		}
	}
	for _, p := range r.Warn {
		add(p.Class, false, p.RE.FindAllString(text, -1))
	}
	return hits
}

// StructureRefusals is the refusing subset of a result's structure hits.
func (res Result) StructureRefusals() []StructureHit {
	var out []StructureHit
	for _, h := range res.Structure {
		if h.Refuse {
			out = append(out, h)
		}
	}
	return out
}

// CompilePattern builds a Pattern, naming the class in any error.
func CompilePattern(class, expr string) (Pattern, error) {
	re, err := regexp.Compile(expr)
	if err != nil {
		return Pattern{}, err
	}
	return Pattern{Class: class, RE: re}, nil
}
