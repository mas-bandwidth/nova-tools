package main

// The printed OUTPUT GRAMMAR of docs/SPEC-TOKENS.md is the contract with a scanner, and a
// line the tool prints that the grammar does not admit is a line no scanner can parse.
// Two were found by a cold read: `TOKENS UNPARSED label=bus:<name>` while four readers
// print `claude:`, `opencode:`, `swarm:` and `provider:` labels, and `day_basis=<utc|zone>`
// while a bus lane carrying two bases prints `mixed` (the prose beside the block already
// said so; the block did not).

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/testkit"
)

// grammarPairs splits the head of a line (everything before the free-text tail, the first
// ": ") into its key=value pairs. A token with no `=` continues the value before it,
// because a rendered value can carry a space inside quotes.
func grammarPairs(s string) map[string]string {
	s, _, _ = strings.Cut(s, ": ")
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

// grammarAdmits reports whether spec is an enumeration, `<a|b|c>`, and whether it admits v.
// A literal member matches exactly. A member in angle brackets -- `<zone>` in
// `<utc|mixed|<zone>>` -- is a PLACEHOLDER for a class of values, and is checked by its
// class, never as the literal word `zone`, which the tool never prints. `<zone>` is the
// zone an export
// declares, as rule 17 and rule 13 accept it: non-empty, no whitespace, and not one of the
// literal members beside it (`utc` spelled out is refused by rule 6); a placeholder this
// test knows no class for admits anything. A one-letter member is a placeholder too
// (`<all|d>`, `<n|->`), and this test knows no class for it, so such a template is not a set.
func grammarAdmits(spec, v string) (enum, ok bool) {
	if !strings.HasPrefix(spec, "<") || !strings.HasSuffix(spec, ">") {
		return false, false
	}
	alts := strings.Split(strings.TrimSuffix(strings.TrimPrefix(spec, "<"), ">"), "|")
	if len(alts) < 2 {
		return false, false
	}
	for _, a := range alts {
		placeholder := strings.HasPrefix(a, "<") && strings.HasSuffix(a, ">") && len(a) > 2
		if !placeholder && (len(a) < 2 || strings.ContainsAny(a, "<>")) {
			return false, false
		}
	}
	for _, a := range alts {
		if strings.HasPrefix(a, "<") {
			ok = ok || a != "<zone>" || v != "" && !strings.ContainsAny(v, " \t") && !slices.Contains(alts, v)
		} else {
			ok = ok || v == a
		}
	}
	return true, ok
}

func TestTheOutputGrammarAdmitsTheLinesTheToolPrints(t *testing.T) {
	t.Parallel()

	// The fenced grammar block of docs/SPEC-TOKENS.md -- the one that carries
	// `TOKENS FOLD at=` -- keyed by the first two words of each line.
	var block, cur []string
	in := false
	for _, line := range strings.Split(testkit.ReadFile(t, filepath.Join("..", "..", "docs", "SPEC-TOKENS.md")), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			if in && strings.Contains("\n"+strings.Join(cur, "\n"), "\nTOKENS FOLD at=") {
				block = cur
			}
			cur, in = nil, !in
			continue
		}
		if in {
			cur = append(cur, line)
		}
	}
	require.NotNil(t, block, "docs/SPEC-TOKENS.md has no OUTPUT GRAMMAR block carrying `TOKENS FOLD at=`; this test was reading the wrong thing and would have passed by checking nothing")
	grammar := map[string]string{}
	for _, l := range block {
		if f := strings.Fields(l); len(f) >= 2 {
			grammar[f[0]+" "+strings.TrimSuffix(f[1], ":")] = l
		}
	}

	b := newBench(t)
	outs := func(name string) string { return testkit.Mkdir(t, filepath.Join(b.dir, name)) }
	// A transcript with a stamp this tool cannot read: the UNPARSED line carries a
	// `claude:` label, not a `bus:` one.
	tr := b.transcript("a.jsonl",
		`{"type":"assistant","timestamp":"2026-09-11T10:00:00Z","message":{"id":"m1","model":"f","usage":{"input_tokens":7}},"cwd":"/x/schema"}`,
		`{"type":"assistant","timestamp":"the eleventh","message":{"id":"m3","model":"f","usage":{"input_tokens":9}}}`)
	runs := []testkit.Ran{novaTokens.Do(t, "fold", "--out", b.out, "--all", "--repos", b.repos, "--claude", "g="+tr)}

	// A bus lane carrying a six-field and a seven-field line: day_basis=mixed.
	out2 := outs("out2")
	bus := busDir(t, outs("bus"), "emma")
	busNote(t, bus, "emma", "a.md", "emma-000000000001", "tokens 2026-09-11", busDate,
		"2026-09-11\temma\tutcmodel\tschema\tinput\t100\n2026-09-11\temma\tzonemodel\tschema\tinput\t5\tday_basis=America/Los_Angeles\n")
	runs = append(runs,
		novaTokens.Do(t, "fold", "--out", out2, "--all", "--repos", b.repos, "--bus", bus),
		novaTokens.Do(t, "sources", "--all", "--repos", b.repos, "--bus", bus),
		novaTokens.Do(t, "sum", "--out", out2, "--month", "2026-09"),
		novaTokens.Do(t, "check", "--out", out2))

	// A provider export of per-day totals in its own zone (rule 17): the SOURCE line's
	// `day_basis=` is the zone NAME, `America/Los_Angeles`, never the word `zone`. The
	// grammar said `<utc|zone|mixed>` and the tool has never printed `zone`.
	out3 := outs("out3")
	xai := testkit.WriteFile(t, filepath.Join(b.dir, "xai.csv"), "# timezone: America/Los_Angeles\ndate,model,input,output,reasoning\n2026-09-11,grok-4,9912340,301122,55\n")
	runs = append(runs,
		novaTokens.Do(t, "fold", "--out", out3, "--all", "--repos", b.repos, "--provider", "xai:johnny="+xai),
		novaTokens.Do(t, "sources", "--all", "--repos", b.repos, "--provider", "xai:johnny="+xai),
		novaTokens.Do(t, "sum", "--out", out3, "--month", "2026-09"),
		novaTokens.Do(t, "check", "--out", out3))

	// A partial-source fold: produces a TOKENS PARTIAL line when a row was blended across
	// declared and undeclared sources.
	out4, poolA, poolB := outs("out4"), outs("poolA"), outs("poolB")
	swarmUsage(t, poolA, "j1", swarmRow("j1", "1", "-", "claude-x", "serialize", "2026-09-14T01:00:00Z", "410", "100", "0", "0", "-"))
	swarmUsage(t, poolB, "j2", swarmRow("j2", "1", "-", "claude-x", "serialize", "2026-09-14T02:00:00Z", "2000", "420", "0", "0", "-"))
	novaTokens.Do(t, "fold", "--out", out4, "--day", "2026-09-14", "--repos", b.repos, "--swarm", "glenn="+poolA, "--swarm", "freddy="+poolB)
	runs = append(runs, novaTokens.Do(t, "fold", "--out", out4, "--day", "2026-09-14", "--repos", b.repos, "--swarm", "freddy="+poolB))

	// Each printed line: the grammar has a line of its kind, names every key it prints, and
	// admits no narrower value than it carries.
	sawPartial, n := false, 0
	for _, r := range runs {
		for _, line := range strings.Split(r.Stdout+"\n"+r.Stderr, "\n") {
			f := strings.Fields(strings.SplitN(line, ": ", 2)[0])
			if len(f) < 2 || !strings.Contains(" TOKENS SOURCES SUM CHECK REPORT ", " "+f[0]+" ") {
				continue
			}
			n++
			sawPartial = sawPartial || strings.HasPrefix(line, "TOKENS PARTIAL ")
			kind := f[0] + " " + strings.TrimSuffix(f[1], ":")
			tmpl, ok := grammar[kind]
			if !assert.True(t, ok, "the tool prints %q; the output grammar has no line for %s, so a consumer scanning the grammar cannot parse it", line, kind) {
				continue
			}
			want := grammarPairs(tmpl)
			for k, v := range grammarPairs(line) {
				spec, ok := want[k]
				if !assert.True(t, ok, "%s prints %s=%s; the output grammar's %s line has no %s= field", kind, k, v, kind, k) {
					continue
				}
				if i := strings.Index(spec, "<"); i > 0 {
					assert.True(t, strings.HasPrefix(v, spec[:i]), "%s prints %s=%s; the output grammar admits only %s=%s", kind, k, v, k, spec)
				} else if enum, admitted := grammarAdmits(spec, v); enum {
					assert.True(t, admitted, "%s prints %s=%s; the output grammar enumerates %s=%s", kind, k, v, k, spec)
				}
			}
		}
	}
	require.True(t, sawPartial, "no TOKENS PARTIAL line was checked against the grammar")
	require.GreaterOrEqual(t, n, 10, "printed lines checked against the grammar; the fixtures printed nothing and this test would have passed by checking nothing")
}
