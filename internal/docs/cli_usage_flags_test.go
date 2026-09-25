package docs

import (
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// TestCLIDocSynopsisNamesTheFlagsTheBinaryHas is the class test nova-tools #1760 asked for,
// restored and adapted for live tools in nova-tools #3836.
//
// A 2026-09-19 dogfood ran `nova-pulse launch` exactly as docs/CLI.md spells it and could
// not place a card at all: the document's synopsis promised `--benches` and `--bench`, the
// shipped binary defined neither, and the binary's own usage line carried four flags
// (`--routes`, `--floor`, `--key-env`, `--base-url`) the document did not mention. Two
// sources of truth for one flag set is one source of truth too many, and drift between them
// is only ever found by a person following the document and failing.
//
// So: for every verb docs/CLI.md gives a synopsis for, the set of flags in that synopsis is
// the set of flags in the binary's own usage text, in BOTH directions.
//
// SCOPE (#3836). Originally, nova-pulse was the only covered subject; deleting nova-pulse
// (#3801) left it no subject. Measured on dev 3192d8a4, pointed at each of the other 22
// tools' sections, all 22 drifted (missing synopsis, missing verbs, or drifted flags).
// This test covers `nova-sprint` today (whose usage comes dynamically from its verb registry,
// not a static const), and every covered tool is green. Each tool left out is tracked in
// TestLeftOutToolsDriftCount with its open drift count. Once a tool's drift is closed, it
// moves from leftOut into covered.
type coveredTool struct {
	section string // the "## <name>" heading in docs/CLI.md
	tool    string // the word every synopsis line begins with
	usage   func(t *testing.T) string
}

func sprintUsage(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("go", "run", "../../cmd/nova-sprint", "help").Output()
	if err != nil {
		t.Fatalf("nova-sprint help (from verb registry): %v", err)
	}
	return string(out)
}

func TestCLIDocSynopsisNamesTheFlagsTheBinaryHas(t *testing.T) {
	t.Parallel()

	covered := []coveredTool{
		{
			section: "nova-sprint",
			tool:    "nova-sprint",
			usage:   sprintUsage,
		},
	}
	if _, err := os.Stat("../../cmd/nova-pulse/main.go"); err == nil {
		covered = append(covered, coveredTool{
			section: "nova-pulse",
			tool:    "nova-pulse",
			usage: func(t *testing.T) string {
				raw, err := os.ReadFile("../../cmd/nova-pulse/main.go")
				if err != nil {
					t.Fatalf("cmd/nova-pulse/main.go: %v", err)
				}
				return string(raw)
			},
		})
	}

	doc, err := os.ReadFile("../../docs/CLI.md")
	if err != nil {
		t.Fatalf("docs/CLI.md: %v", err)
	}

	for _, c := range covered {
		t.Run(c.section, func(t *testing.T) {
			usageStr := c.usage(t)
			binary := synopsisFlags(usageLines(usageStr, c.tool), c.tool)
			documented := synopsisFlags(docSynopsisLines(string(doc), c.section, c.tool), c.tool)

			if len(documented) == 0 {
				t.Fatalf("docs/CLI.md's %q section gives no synopsis this test can read; the parser or the document changed shape", c.section)
			}
			if len(binary) == 0 {
				t.Fatalf("binary usage holds no usage lines beginning %q", c.tool)
			}

			for _, verb := range sortedKeys(documented) {
				has, ok := binary[verb]
				if !ok {
					t.Errorf("docs/CLI.md documents `%s %s`, which %s's usage does not name at all (nova-tools #1760)", c.tool, verb, c.tool)
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

// leftOutTool records one tool not yet covered by TestCLIDocSynopsisNamesTheFlagsTheBinaryHas
// and its open drift count (nova-tools #3836).
type leftOutTool struct {
	section string
	tool    string
	main    string
	drift   int
}

var leftOutTools = []leftOutTool{
	{section: "nova-board", tool: "nova-board", main: "../../cmd/nova-board/main.go", drift: 2},
	{section: "nova-bus", tool: "nova-bus", main: "../../cmd/nova-bus/main.go", drift: 29},
	{section: "nova-cairn", tool: "nova-cairn", main: "../../cmd/nova-cairn/main.go", drift: 5},
	{section: "nova-check", tool: "nova-check", main: "../../cmd/nova-check/main.go", drift: 9},
	{section: "nova-ci", tool: "nova-ci", main: "../../cmd/nova-ci/main.go", drift: 6},
	{section: "nova-decide", tool: "nova-decide", main: "../../cmd/nova-decide/main.go", drift: 2},
	{section: "nova-fuse", tool: "nova-fuse", main: "../../cmd/nova-fuse/main.go", drift: 2},
	{section: "nova-memory", tool: "nova-memory", main: "../../cmd/nova-memory/main.go", drift: 1},
	{section: "nova-merge", tool: "nova-merge", main: "../../cmd/nova-merge/main.go", drift: 8},
	{section: "nova-play", tool: "nova-play", main: "../../cmd/nova-play/main.go", drift: 7},
	{section: "nova-post", tool: "nova-post", main: "../../cmd/nova-post/main.go", drift: 6},
	{section: "nova-review", tool: "nova-review", main: "../../cmd/nova-review/main.go", drift: 11},
	{section: "nova-sandbox", tool: "nova-sandbox", main: "../../cmd/nova-sandbox/main.go", drift: 12},
	{section: "nova-secrets", tool: "nova-secrets", main: "../../cmd/nova-secrets/main.go", drift: 11},
	{section: "nova-self-talk", tool: "nova-self-talk", main: "../../cmd/nova-self-talk/main.go", drift: 3},
	{section: "nova-swarm", tool: "nova-swarm", main: "../../cmd/nova-swarm/main.go", drift: 50},
	{section: "nova-tokens", tool: "nova-tokens", main: "../../cmd/nova-tokens/main.go", drift: 10},
	{section: "nova-update", tool: "nova-update", main: "../../cmd/nova-update/main.go", drift: 0},
	{section: "nova-version", tool: "nova-version", main: "../../cmd/nova-version/main.go", drift: 0},
	{section: "nova-wake", tool: "nova-wake", main: "../../cmd/nova-wake/main.go", drift: 9},
	{section: "nova-work", tool: "nova-work", main: "../../cmd/nova-work/main.go", drift: 13},
}

// TestLeftOutToolsDriftCount pins the open drift count for every tool not yet in covered
// (nova-tools #3836). A repair that closes drift lowers the count, and the test fails
// until the tool moves to covered or updates its count. A regression that increases drift
// also fails.
func TestLeftOutToolsDriftCount(t *testing.T) {
	t.Parallel()

	doc, err := os.ReadFile("../../docs/CLI.md")
	if err != nil {
		t.Fatalf("docs/CLI.md: %v", err)
	}

	for _, c := range leftOutTools {
		t.Run(c.tool, func(t *testing.T) {
			raw, err := os.ReadFile(c.main)
			if err != nil {
				t.Fatalf("%s: %v", c.main, err)
			}
			binary := synopsisFlags(usageLines(string(raw), c.tool), c.tool)
			documented := synopsisFlags(docSynopsisLines(string(doc), c.section, c.tool), c.tool)

			got := countDrift(documented, binary)
			if got != c.drift {
				t.Errorf("%s: drift count changed from %d to %d (repair or regression; move to covered or update count)", c.tool, c.drift, got)
			}
		})
	}
}

// countDrift returns the total number of missing verbs (in either direction)
// plus missing/extra flags for common verbs.
func countDrift(documented, binary map[string]map[string]bool) int {
	drift := 0
	allVerbs := map[string]bool{}
	for v := range documented {
		allVerbs[v] = true
	}
	for v := range binary {
		allVerbs[v] = true
	}
	for v := range allVerbs {
		docFlags, docOk := documented[v]
		binFlags, binOk := binary[v]
		if !binOk || !docOk {
			drift++
			continue
		}
		for f := range docFlags {
			if !binFlags[f] {
				drift++
			}
		}
		for f := range binFlags {
			if !docFlags[f] {
				drift++
			}
		}
	}
	return drift
}

var flagToken = regexp.MustCompile(`--[a-z0-9][a-z0-9-]*`)

// usageLines pulls the synopsis lines out of a Go source file's usage string: every line
// beginning with the tool's own name (allowing leading whitespace).
func usageLines(src, tool string) []string {
	return joinWraps(strings.Split(src, "\n"), func(line string) bool {
		trimmed := strings.TrimSpace(line)
		return strings.HasPrefix(trimmed, tool+" ")
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
		trimmed := strings.TrimSpace(line)
		return strings.HasPrefix(trimmed, tool+" ") &&
			(strings.ContainsRune(line, '<') || strings.ContainsRune(line, '['))
	})
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
		trimmed := strings.TrimSpace(line)
		fields := strings.Fields(strings.TrimPrefix(trimmed, tool))
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
