package docs

import (
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// docs/CLI.md is the command reference a stranger copies from AND the list
// `nova-check dogfood ledger` reads to learn which verbs exist. A flag a verb
// takes that the reference does not name is a flag nobody can find: it lives
// only in the source. The flag set is read from the source, because that is
// where the truth is, and the document is judged against it. Most nova-memory
// verbs get some of their flags from shared helpers (addRootFlags,
// addRetrievalFlags, addFailMax), so a scan that reads only each verb's own body
// finds none of those. And f.Var names its flag in its SECOND argument — the
// first is the value — which is how `--exclude` and `--exempt` stayed invisible
// to a reader of the reference. --json is every verb's, from internal/tool.

// cliMainPath and cliDocPath are the two texts, relative to this package.
const cliMainPath = "../../cmd/nova-memory/main.go"
const cliDocPath = "../../docs/CLI.md"

// directFlagCalls are the flag registrations that name their flag in the
// argument that follows the call, immediately.
var directFlagCalls = []string{"f.Bool(", "f.String(", "f.Int(", "f.Float64(", "f.Duration(", "f.Required("}

func TestTheCLIReferenceNamesEveryMemoryFlag(t *testing.T) {
	t.Parallel()

	lines := readTextLines(t, cliMainPath)

	helpers := helperFlagsRead(t, lines)
	verbs := cmdVerbsRead(t, lines, helpers)

	doc := readTextLines(t, cliDocPath)
	refLine := make(map[string]int, len(verbs))
	for _, v := range verbs {
		prefix := "nova-memory " + v.name + " "
		found := -1
		for i, line := range doc {
			if !strings.HasPrefix(line, prefix) {
				continue
			}
			require.Less(t, found, 0, "docs/CLI.md carries more than one line beginning %q", prefix)
			found = i
		}
		require.GreaterOrEqual(t, found, 0, "docs/CLI.md carries no line beginning %q, but cmd/nova-memory/main.go defines the %s verb", prefix, v.name)
		refLine[v.name] = found
	}

	for _, v := range verbs {
		line := doc[refLine[v.name]]
		names := make([]string, 0, len(v.flags))
		for name := range v.flags {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			if strings.Contains(line, "--"+name) {
				continue
			}
			t.Errorf("docs/CLI.md does not name --%s for nova-memory %s, but cmd/nova-memory/main.go:%d registers it; the reference is where a person looks",
				name, v.name, v.flags[name])
		}
	}
}

// readTextLines reads one text file and splits it into lines.
func readTextLines(t *testing.T, path string) []string {
	t.Helper()
	raw, err := os.ReadFile(path)
	require.NoError(t, err, "reading %s: %v", path, err)
	return strings.Split(string(raw), "\n")
}

// helperFlagsRead cuts the body of every `func add...(f *tool.Flags` helper and
// returns, per helper name, each flag it registers mapped to its one-based source
// line. For f.Var the flag name is the string literal after the first comma; a
// walk that stops at the first argument sees only the value.
func helperFlagsRead(t *testing.T, lines []string) map[string]map[string]int {
	t.Helper()
	helpers := map[string]map[string]int{}
	for i, line := range lines {
		name, ok := strings.CutPrefix(line, "func add")
		if !ok || !strings.Contains(line, "(f *tool.Flags") {
			continue
		}
		name = "add" + name[:strings.IndexByte(name, '(')]
		flags := map[string]int{}
		body, _ := cutBody(lines, i)
		for j, bodyLine := range body {
			if flag, ok := varFlagName(bodyLine); ok {
				flags[flag] = i + j + 2
			}
			if flag, ok := directFlagName(bodyLine); ok {
				flags[flag] = i + j + 2
			}
		}
		helpers[name] = flags
	}
	require.GreaterOrEqual(t, len(helpers["addRootFlags"]), 2, "addRootFlags registers %d flags, want at least two (root and exclude)", len(helpers["addRootFlags"]))
	return helpers
}

// cmdVerb is one `func cmdXxx` and the flags its body registers.
type cmdVerb struct {
	name  string
	flags map[string]int
}

// cmdVerbsRead cuts every body whose line begins `func cmd`, records the verb
// name (the part after cmd, lowercased), every flag registered directly in the
// body, and the flags of every helper the body names.
func cmdVerbsRead(t *testing.T, lines []string, helpers map[string]map[string]int) []*cmdVerb {
	t.Helper()
	var verbs []*cmdVerb
	for i, line := range lines {
		if !strings.HasPrefix(line, "func cmd") {
			continue
		}
		body, _ := cutBody(lines, i)
		v := &cmdVerb{name: cmdVerbName(line), flags: map[string]int{}}
		for j, bodyLine := range body {
			at := i + j + 2
			for helper, flags := range helpers {
				if strings.Contains(bodyLine, helper+"(f") || strings.Contains(bodyLine, ": "+helper+",") {
					for flag, at := range flags {
						v.flags[flag] = at
					}
				}
			}
			if flag, ok := varFlagName(bodyLine); ok {
				v.flags[flag] = at
			}
			if flag, ok := directFlagName(bodyLine); ok {
				v.flags[flag] = at
			}
		}
		verbs = append(verbs, v)
	}
	require.NotEmpty(t, verbs, "%s carries no func cmd<Verb>", cliMainPath)
	return verbs
}

// cutBody returns the lines of a function body: everything after the `func ...`
// line up to the next line that is exactly `}` at column one.
func cutBody(lines []string, start int) ([]string, int) {
	for i := start + 1; i < len(lines); i++ {
		if lines[i] == "}" {
			return lines[start+1 : i], i + 1
		}
	}
	return lines[start+1:], len(lines)
}

// cmdVerbName turns `func cmdQuickstart(` into `quickstart`.
func cmdVerbName(line string) string {
	s := strings.TrimPrefix(line, "func cmd")
	if i := strings.IndexByte(s, '('); i >= 0 {
		s = s[:i]
	}
	return strings.ToLower(s)
}

// varFlagName reads f.Var(value, "name", ...) and returns name.
func varFlagName(line string) (string, bool) {
	i := strings.Index(line, "f.Var(")
	if i < 0 {
		return "", false
	}
	rest := line[i+len("f.Var("):]
	comma := strings.IndexByte(rest, ',')
	if comma < 0 {
		return "", false
	}
	return quotedLiteral(rest[comma+1:])
}

// directFlagName reads f.String("name", ...) and friends and returns name.
func directFlagName(line string) (string, bool) {
	for _, call := range directFlagCalls {
		if i := strings.Index(line, call); i >= 0 {
			return quotedLiteral(line[i+len(call):])
		}
	}
	return "", false
}

// quotedLiteral returns the first double-quoted string in s.
func quotedLiteral(s string) (string, bool) {
	i := strings.IndexByte(s, '"')
	if i < 0 {
		return "", false
	}
	rest := s[i+1:]
	j := strings.IndexByte(rest, '"')
	if j < 0 {
		return "", false
	}
	return rest[:j], true
}
