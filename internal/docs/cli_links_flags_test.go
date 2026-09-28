package docs

import (
	"os"
	"strings"
	"testing"
)

// cli_links_flags_test.go pins one reference line against the flag set the verb
// actually reads. docs/CLI.md is the command reference AND the list `nova-check
// dogfood ledger` reads, so a flag on a verb and not in the reference is a flag
// nobody can find: the binary's own banner names it, the one page a reader
// consults does not. The flag set is read from the body of `func cmdLinks`
// because that is where the truth is, and it is scanned in BOTH registration
// forms — `fs.String` names its flag in the first argument, `fs.Var` in the
// second, and it is the second form that let `--exclude` stay invisible.
func TestTheCLIReferenceNamesEveryLinksFlag(t *testing.T) {
	t.Parallel()

	src, err := os.ReadFile("../../cmd/nova-check/main.go")
	if err != nil {
		t.Fatalf("cmd/nova-check/main.go: %v", err)
	}
	lines := strings.Split(string(src), "\n")

	start := -1
	for i, line := range lines {
		if strings.HasPrefix(line, "func cmdLinks(") {
			start = i
			break
		}
	}
	if start < 0 {
		t.Fatal("cmd/nova-check/main.go: func cmdLinks not found")
	}
	end := -1
	for i := start + 1; i < len(lines); i++ {
		if lines[i] == "}" {
			end = i
			break
		}
	}
	if end < 0 {
		t.Fatal("cmd/nova-check/main.go: closing brace of func cmdLinks not found")
	}

	type registration struct {
		name string
		form string
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
				regs = append(regs, registration{name: name, form: "fs." + form + "(\"" + name + "\", ...)", line: lineNo})
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
			regs = append(regs, registration{name: name, form: "fs.Var(&" + name + ", \"" + name + "\", ...)", line: lineNo})
		}
	}

	if len(regs) < 3 {
		t.Fatalf("cmd/nova-check/main.go: found %d registered flags in cmdLinks, want at least 3 (dir, exclude, file); the scan has missed a registration form", len(regs))
	}

	cli, err := os.ReadFile("../../docs/CLI.md")
	if err != nil {
		t.Fatalf("docs/CLI.md: %v", err)
	}
	ref := ""
	refLine := 0
	seen := 0
	for i, line := range strings.Split(string(cli), "\n") {
		if strings.HasPrefix(line, "nova-check links ") {
			seen++
			ref = line
			refLine = i + 1
		}
	}
	if seen == 0 {
		t.Fatal("docs/CLI.md: no line begins `nova-check links `")
	}
	if seen > 1 {
		t.Fatalf("docs/CLI.md: %d lines begin `nova-check links `, want exactly one", seen)
	}

	// The trailing `#` column explains the flags; it is not where a reader
	// finds them. Strip it, as internal/ci does, so prose that names a flag is
	// not read as the flag's declaration.
	usage := ref
	if j := strings.Index(usage, "#"); j >= 0 {
		usage = usage[:j]
	}

	for _, reg := range regs {
		if !strings.Contains(usage, "--"+reg.name) {
			t.Errorf("docs/CLI.md:%d reference line %q does not name --%s, registered at cmd/nova-check/main.go:%d as %s; the reference is where a person looks for it",
				refLine, ref, reg.name, reg.line, reg.form)
		}
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
