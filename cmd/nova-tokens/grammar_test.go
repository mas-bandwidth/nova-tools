package main

// The printed OUTPUT GRAMMAR of docs/SPEC-TOKENS.md is the contract with a scanner, and a
// line the tool prints that the grammar does not admit is a line no scanner can parse.
// Two were found by a cold read: `TOKENS UNPARSED label=bus:<name>` while four readers
// print `claude:`, `opencode:`, `swarm:` and `provider:` labels, and `day_basis=<utc|zone>`
// while a bus lane carrying two bases prints `mixed` (the prose beside the block already
// said so; the block did not).

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// outputGrammar is the fenced grammar block of docs/SPEC-TOKENS.md -- the one that carries
// `TOKENS FOLD at=` -- keyed by the first two words of each line.
func outputGrammar(t *testing.T) map[string]string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(root, "docs", "SPEC-TOKENS.md"))
	if err != nil {
		t.Fatal(err)
	}
	var block, cur []string
	in := false
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			if in {
				for _, l := range cur {
					if strings.HasPrefix(l, "TOKENS FOLD at=") {
						block = cur
					}
				}
				cur = nil
			}
			in = !in
			continue
		}
		if in {
			cur = append(cur, line)
		}
	}
	if block == nil {
		t.Fatal("docs/SPEC-TOKENS.md has no OUTPUT GRAMMAR block carrying `TOKENS FOLD at=`; this test was reading the wrong thing and would have passed by checking nothing")
	}
	out := map[string]string{}
	for _, l := range block {
		f := strings.Fields(l)
		if len(f) < 2 {
			continue
		}
		out[f[0]+" "+strings.TrimSuffix(f[1], ":")] = l
	}
	return out
}

// grammarPairs splits the head of a line (everything before the free-text tail) into its
// key=value pairs. A token with no `=` continues the value before it, because a rendered
// value can carry a space inside quotes.
func grammarPairs(s string) map[string]string {
	out := map[string]string{}
	key := ""
	for _, tok := range strings.Fields(s) {
		if i := strings.Index(tok, "="); i > 0 && !strings.ContainsAny(tok[:i], "<>:\"") {
			key = tok[:i]
			out[key] = tok[i+1:]
			continue
		}
		if key != "" {
			out[key] += " " + tok
		}
	}
	return out
}

// grammarEnum is the set of members a `<a|b|c>` value admits. A member in angle brackets --
// `<zone>` in `<utc|mixed|<zone>>` -- is a PLACEHOLDER for a class of values, not a literal,
// and comes back with its brackets so the matcher can check the class instead of the word:
// the grammar used to spell that member `zone`, a word the tool has never printed, and the
// zone name it does print was admitted by nothing. A one-letter member is a placeholder too
// (`<all|d>`, `<n|->`), and this test knows no class for it, so such a template is not a set.
func grammarEnum(spec string) ([]string, bool) {
	if !strings.HasPrefix(spec, "<") || !strings.HasSuffix(spec, ">") {
		return nil, false
	}
	alts := strings.Split(strings.TrimSuffix(strings.TrimPrefix(spec, "<"), ">"), "|")
	if len(alts) < 2 {
		return nil, false
	}
	for _, a := range alts {
		if strings.HasPrefix(a, "<") && strings.HasSuffix(a, ">") && len(a) > 2 {
			continue // a named placeholder, checked by grammarPlaceholder
		}
		if len(a) < 2 || strings.ContainsAny(a, "<>") {
			return nil, false
		}
	}
	return alts, true
}

// grammarAdmits reports whether an enumerated value is admitted: a literal member matches
// exactly, a bracketed member by its class.
func grammarAdmits(alts []string, v string) bool {
	for _, a := range alts {
		if strings.HasPrefix(a, "<") && strings.HasSuffix(a, ">") && len(a) > 2 {
			if grammarPlaceholder(a[1:len(a)-1], v, alts) {
				return true
			}
			continue
		}
		if v == a {
			return true
		}
	}
	return false
}

// grammarPlaceholder is the class each named placeholder stands for. `<zone>` is the zone
// an export declares, as rule 17 and rule 13 accept it: non-empty, no whitespace, and not
// one of the literal members standing beside it (`utc` spelled out is refused by rule 6).
// A placeholder this test knows no class for admits anything, which is the old behaviour.
func grammarPlaceholder(name, v string, alts []string) bool {
	switch name {
	case "zone":
		if v == "" || strings.ContainsAny(v, " \t") {
			return false
		}
		for _, a := range alts {
			if v == a {
				return false
			}
		}
		return true
	}
	return true
}

func head(s string) string {
	if i := strings.Index(s, ": "); i >= 0 {
		return s[:i]
	}
	return s
}

// checkAgainstGrammar fails if the grammar has no line of this kind, does not name a key
// the line prints, or admits a narrower value than the line carries.
func checkAgainstGrammar(t *testing.T, grammar map[string]string, line string) {
	t.Helper()
	f := strings.Fields(head(line))
	if len(f) < 2 {
		return
	}
	kind := f[0] + " " + strings.TrimSuffix(f[1], ":")
	tmpl, ok := grammar[kind]
	if !ok {
		t.Errorf("the tool prints %q; the output grammar has no line for %s, so a consumer scanning the grammar cannot parse it", line, kind)
		return
	}
	want := grammarPairs(head(tmpl))
	for k, v := range grammarPairs(head(line)) {
		spec, ok := want[k]
		if !ok {
			t.Errorf("%s prints %s=%s; the output grammar's %s line has no %s= field", kind, k, v, kind, k)
			continue
		}
		if i := strings.Index(spec, "<"); i > 0 {
			if !strings.HasPrefix(v, spec[:i]) {
				t.Errorf("%s prints %s=%s; the output grammar admits only %s=%s", kind, k, v, k, spec)
			}
			continue
		}
		if alts, closed := grammarEnum(spec); closed {
			if !grammarAdmits(alts, v) {
				t.Errorf("%s prints %s=%s; the output grammar enumerates %s=%s", kind, k, v, k, spec)
			}
		}
	}
}

// printedLines is every line of a run that starts with one of the tool's tokens.
func printedLines(r result) []string {
	var out []string
	for _, s := range []string{r.stdout, r.stderr} {
		for _, line := range strings.Split(s, "\n") {
			switch strings.Fields(line + " x")[0] {
			case "TOKENS", "SOURCES", "SUM", "CHECK", "REPORT":
				out = append(out, line)
			}
		}
	}
	return out
}

func TestTheOutputGrammarAdmitsTheLinesTheToolPrints(t *testing.T) {
	grammar := outputGrammar(t)

	// A transcript with a stamp this tool cannot read: the UNPARSED line carries a
	// `claude:` label, not a `bus:` one.
	dir := t.TempDir()
	out := mkdir(t, filepath.Join(dir, "out"))
	tr := write(t, filepath.Join(dir, "a.jsonl"), strings.Join([]string{
		`{"type":"assistant","timestamp":"2026-09-11T10:00:00Z","message":{"id":"m1","model":"f","usage":{"input_tokens":7}},"cwd":"/x/schema"}`,
		`{"type":"assistant","timestamp":"the eleventh","message":{"id":"m3","model":"f","usage":{"input_tokens":9}}}`,
	}, "\n")+"\n")
	runs := []result{invoke(t, "fold", "--out", out, "--all", "--repos", reposFile(t, dir), "--claude", "g="+tr)}

	// A bus lane carrying a six-field and a seven-field line: day_basis=mixed.
	dir2 := t.TempDir()
	out2 := mkdir(t, filepath.Join(dir2, "out"))
	bus := busDir(t, mkdir(t, filepath.Join(dir2, "bus")), "emma")
	busNote(t, bus, "emma", "a.md", "emma-000000000001", "tokens 2026-09-11", busDate, strings.Join([]string{
		"2026-09-11\temma\tutcmodel\tschema\tinput\t100",
		"2026-09-11\temma\tzonemodel\tschema\tinput\t5\tday_basis=America/Los_Angeles",
		"",
	}, "\n"))
	runs = append(runs,
		invoke(t, "fold", "--out", out2, "--all", "--repos", reposFile(t, dir2), "--bus", bus),
		invoke(t, "sources", "--all", "--repos", reposFile(t, dir2), "--bus", bus),
		invoke(t, "sum", "--out", out2, "--month", "2026-09"),
		invoke(t, "check", "--out", out2))

	// A provider export of per-day totals in its own zone (rule 17): the SOURCE line's
	// `day_basis=` is the zone NAME, `America/Los_Angeles`, never the word `zone`. The
	// grammar said `<utc|zone|mixed>` and the tool has never printed `zone`.
	dir3 := t.TempDir()
	out3 := mkdir(t, filepath.Join(dir3, "out"))
	xai := write(t, filepath.Join(dir3, "xai.csv"), strings.Join([]string{
		"# timezone: America/Los_Angeles",
		"date,model,input,output,reasoning",
		"2026-09-11,grok-4,9912340,301122,55",
		"",
	}, "\n"))
	runs = append(runs,
		invoke(t, "fold", "--out", out3, "--all", "--repos", reposFile(t, dir3), "--provider", "xai:johnny="+xai),
		invoke(t, "sources", "--all", "--repos", reposFile(t, dir3), "--provider", "xai:johnny="+xai),
		invoke(t, "sum", "--out", out3, "--month", "2026-09"),
		invoke(t, "check", "--out", out3))

	n := 0
	for _, r := range runs {
		for _, line := range printedLines(r) {
			n++
			checkAgainstGrammar(t, grammar, line)
		}
	}
	if n < 10 {
		t.Fatalf("%d printed lines checked against the grammar; the fixtures printed nothing and this test would have passed by checking nothing", n)
	}
}
