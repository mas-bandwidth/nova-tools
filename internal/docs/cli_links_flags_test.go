package docs

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// cli_links_flags_test.go pins one reference line against the flag set the verb
// actually reads. docs/CLI.md is the command reference AND the list `nova-check
// dogfood ledger` reads, so a flag on a verb and not in the reference is a flag
// nobody can find: the binary's own banner names it, the one page a reader
// consults does not. The flag set is read from the body of `func linksFlags`
// because that is where the truth is, and it is scanned in BOTH registration
// forms — `f.Required` / `f.String` names its flag in the first argument, `f.Var` in the
// second, and it is the second form that let `--exclude` stay invisible.
func TestTheCLIReferenceNamesEveryLinksFlag(t *testing.T) {
	t.Parallel()

	src, err := os.ReadFile("../../cmd/nova-check/main.go")
	require.NoError(t, err, "cmd/nova-check/main.go: %v", err)
	lines := strings.Split(string(src), "\n")

	start := -1
	for i, line := range lines {
		if strings.HasPrefix(line, "func linksFlags(") {
			start = i
			break
		}
	}
	require.GreaterOrEqual(t, start, 0, "cmd/nova-check/main.go: func linksFlags not found")
	end := -1
	for i := start + 1; i < len(lines); i++ {
		if lines[i] == "}" {
			end = i
			break
		}
	}
	require.GreaterOrEqual(t, end, 0, "cmd/nova-check/main.go: closing brace of func linksFlags not found")

	type registration struct {
		name string
		form string
		line int
	}
	var regs []registration
	for i, line := range lines[start:end] {
		lineNo := start + i + 1
		for _, form := range []string{"Bool", "String", "Int", "Duration", "Required"} {
			needle := "f." + form + "("
			rest := line
			for {
				idx := strings.Index(rest, needle)
				if idx < 0 {
					break
				}
				rest = rest[idx+len(needle):]
				name, ok := nameAfterQuote(rest)
				if !ok {
					break
				}
				regs = append(regs, registration{name: name, form: "f." + form + "(\"" + name + "\", ...)", line: lineNo})
			}
		}
		rest := line
		for {
			idx := strings.Index(rest, "f.Var(")
			if idx < 0 {
				break
			}
			rest = rest[idx+len("f.Var("):]
			comma := strings.Index(rest, ",")
			if comma < 0 {
				break
			}
			name, ok := nameAfterQuote(rest[comma+1:])
			if !ok {
				break
			}
			regs = append(regs, registration{name: name, form: "f.Var(&" + name + ", \"" + name + "\", ...)", line: lineNo})
		}
	}

	require.GreaterOrEqual(t, len(regs), 3, "cmd/nova-check/main.go: found %d registered flags in linksFlags, want at least 3 (dir, exclude, file); the scan has missed a registration form", len(regs))

	cli, err := os.ReadFile("../../docs/CLI.md")
	require.NoError(t, err, "docs/CLI.md: %v", err)
	usage := generatedVerbEntry(t, string(cli), "nova-check", "links")

	for _, reg := range regs {
		assert.Contains(t, usage, "--"+reg.name, "docs/CLI.md generated links entry does not name --%s, registered at cmd/nova-check/main.go:%d as %s",
			reg.name, reg.line, reg.form)
	}
}

func nameAfterQuote(s string) (string, bool) {
	i := strings.Index(s, "\"")
	if i < 0 {
		return "", false
	}
	rest := s[i+1:]
	j := strings.Index(rest, "\"")
	if j < 0 {
		return "", false
	}
	return rest[:j], true
}
