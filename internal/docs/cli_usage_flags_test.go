package docs

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// TestCLIDocSynopsisNamesTheFlagsTheBinaryHas is the class test nova-tools #1760 asked for.
//
// A 2026-09-19 dogfood ran `nova-pulse launch` exactly as docs/CLI.md spells it and could
// not place a card at all: the document's synopsis promised `--benches` and `--bench`, the
// shipped binary defined neither, and the binary's own usage line carried four flags
// (`--routes`, `--floor`, `--key-env`, `--base-url`) the document did not mention. Two
// sources of truth for one flag set is one source of truth too many, and drift between them
// is only ever found by a person following the document and failing.
//
// So: for every verb docs/CLI.md gives a synopsis for, the set of flags in that synopsis is
// the set of flags in the binary's own usage text, in BOTH directions. The binary's usage
// string is read as source text rather than by running it, because a doc test that has to
// build a binary first is a doc test nobody runs.
//
// SCOPE, said out loud. This covers `## nova-pulse` today: the section the dogfood measured
// and the one this change makes true. The other tools' sections are not covered because
// their drift has not been read yet, and a class test that lands red is not a class test.
// Each one is one line in `covered` once its own drift is closed.
func TestCLIDocSynopsisNamesTheFlagsTheBinaryHas(t *testing.T) {
	t.Parallel()

	covered := []struct {
		section string // the "## <name>" heading in docs/CLI.md
		main    string // the file holding that binary's usage string
		tool    string // the word every synopsis line begins with
	}{
		{section: "nova-pulse", main: "../../cmd/nova-pulse/main.go", tool: "nova-pulse"},
	}

	doc, err := os.ReadFile("../../docs/CLI.md")
	if err != nil {
		t.Fatalf("docs/CLI.md: %v", err)
	}

	for _, c := range covered {
		t.Run(c.section, func(t *testing.T) {
			raw, err := os.ReadFile(c.main)
			if err != nil {
				t.Fatalf("%s: %v", c.main, err)
			}
			binary := synopsisFlags(usageLines(string(raw), c.tool), c.tool)
			documented := synopsisFlags(docSynopsisLines(string(doc), c.section, c.tool), c.tool)

			if len(documented) == 0 {
				t.Fatalf("docs/CLI.md's %q section gives no synopsis this test can read; the parser or the document changed shape", c.section)
			}
			if len(binary) == 0 {
				t.Fatalf("%s holds no usage lines beginning %q", c.main, c.tool)
			}

			for _, verb := range sortedKeys(documented) {
				has, ok := binary[verb]
				if !ok {
					t.Errorf("docs/CLI.md documents `%s %s`, which %s's usage does not name at all (nova-tools #1760)", c.tool, verb, c.main)
					continue
				}
				for _, f := range sortedKeys(documented[verb]) {
					if !has[f] {
						t.Errorf("docs/CLI.md's `%s %s` promises %s, which the binary does not define (nova-tools #1760)", c.tool, verb, f)
					}
				}
				for _, f := range sortedKeys(has) {
					if !documented[verb][f] {
						t.Errorf("`%s %s` defines %s, which docs/CLI.md's synopsis does not mention (nova-tools #1760)", c.tool, verb, f)
					}
				}
			}
		})
	}
}

var flagToken = regexp.MustCompile(`--[a-z0-9][a-z0-9-]*`)

// usageLines pulls the synopsis lines out of a Go source file's usage string: every line
// beginning with the tool's own name. The string is a raw literal in every binary here, so
// the file's lines are the usage's lines.
func usageLines(src, tool string) []string {
	return joinWraps(strings.Split(src, "\n"), func(line string) bool {
		return strings.HasPrefix(line, tool+" ")
	})
}

// docSynopsisLines pulls the synopsis lines out of docs/CLI.md's section for one tool: the
// lines inside a fenced block that begin with the tool's name AND hold a placeholder or an
// optional -- a "<" or a "[". That is what separates a synopsis from the worked examples in
// the same section, which spell real paths and real values and carry neither.
func docSynopsisLines(doc, section, tool string) []string {
	var block []string
	in, fenced := false, false
	for _, line := range strings.Split(doc, "\n") {
		switch {
		case strings.HasPrefix(line, "## "):
			in = strings.TrimSpace(strings.TrimPrefix(line, "## ")) == section
			fenced = false
			continue
		case !in:
			continue
		case strings.HasPrefix(strings.TrimSpace(line), "```"):
			fenced = !fenced
			continue
		}
		if fenced {
			block = append(block, line)
		}
	}
	return joinWraps(block, func(line string) bool {
		return strings.HasPrefix(line, tool+" ") && (strings.ContainsRune(line, '<') ||
			strings.ContainsRune(line, '[') || allFlagsAfterTheVerb(line))
	})
}

// allFlagsAfterTheVerb is the second shape a synopsis line can take: a verb form that
// takes NO argument at all, so it holds neither a placeholder nor an optional and the
// rule above cannot see it (`nova-pulse accept --kinds`). It is still not an example,
// because every token after the verb is a `--flag`: a worked example spells real values
// (`--bench hulk`, `--root ~/rowan-swarm-root`) and this cannot.
//
// The binary's side of this test has always read such a line -- usageLines takes every
// line beginning with the tool's name -- so without this the two sides parse the same
// document shape by different rules, and a no-argument form is reported as undocumented
// however it is written. Widening the doc side makes the test STRICTER in both
// directions: more synopsis lines are read, so more flags must match.
func allFlagsAfterTheVerb(line string) bool {
	fields := strings.Fields(line)
	if len(fields) < 3 {
		return false
	}
	for _, f := range fields[2:] {
		if !strings.HasPrefix(f, "--") {
			return false
		}
	}
	return true
}

// joinWraps folds a synopsis's continuation lines into the line they continue. Both sources
// wrap `status --html` over two lines and they wrap it in different places, so a parser that
// read only the line beginning with the tool's name would be comparing two different halves
// and would call the difference drift.
func joinWraps(lines []string, keep func(string) bool) []string {
	var out []string
	open := false // the previous line was one we kept, so an indented line continues IT
	for _, line := range lines {
		trimmed := strings.TrimLeft(line, " \t")
		wrap := line != trimmed && (strings.HasPrefix(trimmed, "-") || strings.HasPrefix(trimmed, "["))
		switch {
		case wrap && open:
			out[len(out)-1] += " " + trimmed
		case keep(line):
			out = append(out, line)
			open = true
		default:
			// Anything else closes the wrap: an indented flag under a line this
			// parser did not keep belongs to that line, not to the last kept one
			// several lines above it.
			open = false
		}
	}
	return out
}

// synopsisFlags groups synopsis lines by the verb they describe -- the words after the
// tool's name, up to the first token that is a flag or a placeholder -- and unions the flags
// of every line for that verb. A verb with several forms (`harvest --id`, `harvest --bench`,
// `harvest --working`) is one entry holding the flags of all of them, because each side
// spells it as several lines and no one line is the whole truth.
func synopsisFlags(lines []string, tool string) map[string]map[string]bool {
	out := map[string]map[string]bool{}
	for _, line := range lines {
		fields := strings.Fields(strings.TrimPrefix(line, tool))
		var verb []string
		for _, f := range fields {
			if strings.HasPrefix(f, "-") || strings.HasPrefix(f, "[") || strings.HasPrefix(f, "<") || strings.HasPrefix(f, "(") {
				break
			}
			verb = append(verb, f)
		}
		if len(verb) == 0 {
			continue
		}
		name := strings.Join(verb, " ")
		if out[name] == nil {
			out[name] = map[string]bool{}
		}
		for _, f := range flagToken.FindAllString(line, -1) {
			out[name][f] = true
		}
	}
	return out
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
