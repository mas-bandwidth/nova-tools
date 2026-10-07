package tool

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Rig is the test harness for tool tests. It provides helpers for parsing
// output renderings and asserting their structure.
type Rig struct{}

// NewRig returns a new test rig.
func NewRig() *Rig { return &Rig{} }

// parsed is one rendering read back into the parts of Out, for comparing the
// lines with the JSON field for field.
type parsed struct {
	Verb, Status, Remedy string
	Exit                 int
	Why                  []string
	Facts                map[string]string
	Items                []string
	More                 []string
	Notes                []string
	Payload              string
}

// FromLines reads the text rendering into parsed.
func FromLines(t *testing.T, token, text string) parsed {
	t.Helper()
	p := parsed{Facts: map[string]string{}}
	for i, l := range strings.Split(strings.TrimSuffix(text, "\n"), "\n") {
		rest, ok := strings.CutPrefix(l, token+" ")
		if !ok {
			if i == 0 {
				p.Status = "ok"
			}
			p.Payload = l
			continue
		}
		word, rest, _ := strings.Cut(rest, " ")
		if w, ok := strings.CutSuffix(word, ":"); ok {
			word, rest = w, ": "+rest
		}
		switch word {
		case "OK", "FAILED", "REFUSED":
			p.Status = map[string]string{"OK": "ok", "FAILED": "failed", "REFUSED": "refused"}[word]
			if i := strings.LastIndex(rest, "; run: "); i >= 0 {
				p.Remedy, rest = rest[i+len("; run: "):], rest[:i]
			}
			fields, why, found := strings.Cut(rest, ": ")
			if found {
				p.Why = append(p.Why, why)
			}
			if len(p.Why) > 1 {
				continue
			}
			for _, kv := range tokens(rest) {
				k, v, _ := strings.Cut(kv, "=")
				if v == "-" {
					v = ""
				}
				p.Facts[k] = v
			}
		case "MORE":
			p.More = append(p.More, rest)
		case "NOTE":
			p.Notes = append(p.Notes, rest)
		default:
			var kv []string
			for _, f := range strings.Fields(rest) {
				k, v, _ := strings.Cut(f, "=")
				if v == "-" {
					v = ""
				}
				kv = append(kv, k+"="+v)
			}
			p.Items = append(p.Items, strings.Join(append([]string{strings.ToLower(word)}, kv...), " "))
		}
	}
	return p
}

// tokens splits s at spaces outside a quoted value.
func tokens(s string) []string {
	var out []string
	var cur strings.Builder
	quoted, escaped := false, false
	for _, r := range s {
		switch {
		case escaped:
			escaped = false
		case quoted && r == '\\':
			escaped = true
		case r == '"':
			quoted = !quoted
		case r == ' ' && !quoted:
			if cur.Len() > 0 {
				out = append(out, cur.String())
				cur.Reset()
			}
			continue
		}
		cur.WriteRune(r)
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}

// FromJSON reads the JSON rendering into parsed.
func FromJSON(t *testing.T, raw string) parsed {
	t.Helper()
	var j struct {
		Result struct {
			Verb, Status, Remedy string
			Exit                 int
			Why                  []string
		}
		Facts map[string]any
		Items []struct {
			Kind   string
			Fields json.RawMessage
		}
		More    []struct{ Kind string; Shown, Total int; Remedy string }
		Notes   []string
		Payload string
	}
	err := json.Unmarshal([]byte(raw), &j)
	require.NoError(t, err, "not one JSON object: %v: %s", err, raw)
	p := parsed{
		Verb: j.Result.Verb, Status: j.Result.Status, Remedy: j.Result.Remedy, Exit: j.Result.Exit,
		Why: j.Result.Why, Facts: map[string]string{}, Notes: j.Notes, Payload: j.Payload,
	}
	for k, v := range j.Facts {
		p.Facts[k] = strings.TrimSuffix(fmt.Sprint(v), "\n")
	}
	for _, it := range j.Items {
		dec := json.NewDecoder(strings.NewReader(string(it.Fields)))
		kv := []string{strings.ToLower(it.Kind)}
		_, err := dec.Token()
		require.NoError(t, err)
		for dec.More() {
			k, _ := dec.Token()
			var v any
			require.NoError(t, dec.Decode(&v))
			kv = append(kv, fmt.Sprint(k)+"="+fmt.Sprint(v))
		}
		p.Items = append(p.Items, strings.Join(kv, " "))
	}
	for _, m := range j.More {
		p.More = append(p.More, fmt.Sprintf("kind=%s shown=%d total=%d %s", m.Kind, m.Shown, m.Total, m.Remedy))
	}
	return p
}
