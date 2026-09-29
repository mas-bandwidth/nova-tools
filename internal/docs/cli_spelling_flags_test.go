package docs

import (
	"os"
	"strings"
	"testing"
)

// cli_spelling_flags_test.go pins the reference line in docs/CLI.md against the flag set
// cmdSpelling actually registers in cmd/nova-check/spelling.go.
func TestTheCLIReferenceNamesEverySpellingFlag(t *testing.T) {
	t.Parallel()

	src, err := os.ReadFile("../../cmd/nova-check/spelling.go")
	if err != nil {
		t.Fatalf("cmd/nova-check/spelling.go: %v", err)
	}
	lines := strings.Split(string(src), "\n")

	start := -1
	for i, line := range lines {
		if strings.HasPrefix(line, "func cmdSpelling(") {
			start = i
			break
		}
	}
	if start < 0 {
		t.Fatal("cmd/nova-check/spelling.go: func cmdSpelling not found")
	}
	end := -1
	for i := start + 1; i < len(lines); i++ {
		if lines[i] == "}" {
			end = i
			break
		}
	}
	if end < 0 {
		t.Fatal("cmd/nova-check/spelling.go: closing brace of func cmdSpelling not found")
	}

	type registration struct {
		name string
		line int
	}
	var regs []registration
	for i, line := range lines[start:end] {
		lineNo := start + i + 1
		for _, form := range []string{"Bool", "String", "Int", "Duration"} {
			needle := "fs." + form + "("
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
				regs = append(regs, registration{name: name, line: lineNo})
			}
		}
		rest := line
		for {
			idx := strings.Index(rest, "fs.Var(")
			if idx < 0 {
				break
			}
			rest = rest[idx+len("fs.Var("):]
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

	if len(regs) < 6 {
		t.Fatalf("cmd/nova-check/spelling.go: found %d registered flags in cmdSpelling, want at least 6; the scan has missed a registration form", len(regs))
	}

	cli, err := os.ReadFile("../../docs/CLI.md")
	if err != nil {
		t.Fatalf("docs/CLI.md: %v", err)
	}
	ref := ""
	seen := 0
	for _, line := range strings.Split(string(cli), "\n") {
		if strings.HasPrefix(line, "nova-check spelling ") {
			seen++
			ref = line
		}
	}
	if seen == 0 {
		t.Fatal("docs/CLI.md: no line begins `nova-check spelling `")
	}
	if seen > 1 {
		t.Fatalf("docs/CLI.md: %d lines begin `nova-check spelling `, want exactly one", seen)
	}

	usage := ref
	if j := strings.Index(usage, "#"); j >= 0 {
		usage = usage[:j]
	}

	for _, reg := range regs {
		if !strings.Contains(usage, "--"+reg.name) {
			t.Errorf("docs/CLI.md reference line does not name --%s, registered at cmd/nova-check/spelling.go:%d",
				reg.name, reg.line)
		}
	}
}
