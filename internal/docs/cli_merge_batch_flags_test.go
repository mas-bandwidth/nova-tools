package docs

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// docs/CLI.md is the command reference a stranger copies from, so a usage line
// there is a counted claim about the tool's own flag set -- and one that rots
// silently as flags are added. nova-tools #1748 made five of nova-merge batch's
// flags load-bearing and did not amend the reference, so every invocation the
// document showed was refused by the shipped tool. The flag list below is
// DERIVED FROM cmd/nova-merge/batch.go rather than hard-coded, so a seventeenth
// flag reddens this test instead of quietly staling the document; the control
// runs the other way so a typo in the document cannot pass.
func TestTheCLIReferenceNamesEveryNovaMergeBatchFlag(t *testing.T) {
	t.Parallel()

	source, err := os.ReadFile("../../cmd/nova-merge/batch.go")
	if err != nil {
		t.Fatalf("cmd/nova-merge/batch.go: %v; the batch verb's flag registrations are read from there", err)
	}
	flagRe := regexp.MustCompile(`f\.fs\.(?:String|Bool|Int|Duration)\("([^"]+)"`)
	type flag struct {
		name string
		line int
	}
	var flags []flag
	seen := map[string]bool{}
	for i, line := range strings.Split(string(source), "\n") {
		for _, m := range flagRe.FindAllStringSubmatch(line, -1) {
			if seen[m[1]] {
				continue
			}
			seen[m[1]] = true
			flags = append(flags, flag{name: m[1], line: i + 1})
		}
	}
	if len(flags) < 10 {
		t.Fatalf("cmd/nova-merge/batch.go: found only %d flags registered with f.fs.<Type>; a scan that finds almost none would pass by asking nothing", len(flags))
	}

	reference, err := os.ReadFile("../../docs/CLI.md")
	if err != nil {
		t.Fatalf("docs/CLI.md: %v; it is the command reference a person reads to find a verb's flags", err)
	}
	usage, found := "", 0
	for _, line := range strings.Split(string(reference), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "nova-merge batch ") {
			usage = trimmed
			found++
		}
	}
	if found != 1 {
		t.Fatalf("docs/CLI.md has %d lines beginning %q; the reference must name this verb exactly once with its flag list", found, "nova-merge batch ")
	}

	// namesFlagAtBoundary reports whether the usage line carries --<name> as a
	// whole flag token, so --reason is not satisfied by a longer --reason-x and
	// --lane is not satisfied by some flag that merely ends in lane.
	namesFlagAtBoundary := func(line, name string) bool {
		needle := "--" + name
		for i := 0; i+len(needle) <= len(line); {
			j := strings.Index(line[i:], needle)
			if j < 0 {
				return false
			}
			at := i + j + len(needle)
			if at == len(line) {
				return true
			}
			switch line[at] {
			case ' ', '>', ')', ']':
				return true
			}
			i = at
		}
		return false
	}

	var missing []string
	for _, f := range flags {
		if !namesFlagAtBoundary(usage, f.name) {
			missing = append(missing, "--"+f.name)
		}
	}
	if len(missing) > 0 {
		t.Errorf("docs/CLI.md's `nova-merge batch` usage line purports to be the flag list, but it does not name %s; it is counted from cmd/nova-merge/batch.go, and a reader who copies the line gets an invocation the shipped tool refuses", strings.Join(missing, ", "))
	}

	// The control, in the other direction: every --<word> the usage line names
	// must be a flag the source registers, so the document cannot pass by naming
	// --reviewer for --reviewers or otherwise inventing a token.
	defined := map[string]bool{}
	for _, f := range flags {
		defined[f.name] = true
	}
	tokenRe := regexp.MustCompile(`--([A-Za-z0-9][A-Za-z0-9-]*)`)
	var spurious []string
	for _, m := range tokenRe.FindAllStringSubmatch(usage, -1) {
		if !defined[m[1]] {
			spurious = append(spurious, "--"+m[1])
		}
	}
	if len(spurious) > 0 {
		t.Errorf("docs/CLI.md's `nova-merge batch` usage line names %s, which cmd/nova-merge/batch.go does not register; the document must not invent a flag the tool does not define", strings.Join(spurious, ", "))
	}
}
