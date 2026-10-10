package docs

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// cli_spelling_flags_test.go pins the whole reference entry in docs/CLI.md against the flag set
// spellingFlags actually registers in cmd/nova-check/spelling.go.
func TestTheCLIReferenceNamesEverySpellingFlag(t *testing.T) {
	t.Parallel()

	src, err := os.ReadFile("../../cmd/nova-check/spelling.go")
	require.NoError(t, err, "cmd/nova-check/spelling.go: %v", err)
	lines := strings.Split(string(src), "\n")

	start := -1
	for i, line := range lines {
		if strings.HasPrefix(line, "func spellingFlags(") {
			start = i
			break
		}
	}
	require.GreaterOrEqual(t, start, 0, "cmd/nova-check/spelling.go: func spellingFlags not found")
	end := -1
	for i := start + 1; i < len(lines); i++ {
		if lines[i] == "}" {
			end = i
			break
		}
	}
	require.GreaterOrEqual(t, end, 0, "cmd/nova-check/spelling.go: closing brace of func spellingFlags not found")

	type registration struct {
		name string
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
				if name != "json" {
					regs = append(regs, registration{name: name, line: lineNo})
				}
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
			regs = append(regs, registration{name: name, line: lineNo})
		}
	}

	require.GreaterOrEqual(t, len(regs), 6, "cmd/nova-check/spelling.go: found %d registered flags in spellingFlags, want at least 6; the scan has missed a registration form", len(regs))

	cli, err := os.ReadFile("../../docs/CLI.md")
	require.NoError(t, err, "docs/CLI.md: %v", err)
	usage := generatedVerbEntry(t, string(cli), "nova-check", "spelling")

	for _, reg := range regs {
		assert.Contains(t, usage, "--"+reg.name, "docs/CLI.md generated spelling entry does not name --%s, registered at cmd/nova-check/spelling.go:%d",
			reg.name, reg.line)
	}
}
