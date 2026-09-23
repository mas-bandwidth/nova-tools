package docs

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// `nova-swarm lint` is what a card is checked with before it is launched, and
// docs/CLI.md is the command reference a person is sent to to find a verb's
// flags. The reference did not name the verb at all, so this holds the
// reference's own synopsis line for the verb against the flags the verb
// registers directly in its own function; a flag the reference does not name
// is a flag nobody can find (#1855).
//
// The flag list is DERIVED from cmd/nova-swarm/lint.go rather than hard-coded,
// so a new flag reddens this test instead of quietly staling the document, and
// the control runs the other way so a typo in the document cannot pass. The
// shared `--max` ceiling is registered through `maxFlag(f.fs)`, not through a
// `f.fs.<Type>` call, so it is added on its own: if someone renders the verb
// with no `--max <n>` -- or drops the flag from the code -- this test fails
// rather than letting the reference drift.
func TestTheCLIReferenceNamesEverySwarmLintFlag(t *testing.T) {
	t.Parallel()

	source, err := os.ReadFile("../../cmd/nova-swarm/lint.go")
	if err != nil {
		t.Fatalf("cmd/nova-swarm/lint.go: %v; the verb's flag registrations are read from there", err)
	}
	lines := strings.Split(string(source), "\n")
	start, end := -1, -1
	for i, line := range lines {
		if start < 0 && strings.HasPrefix(line, "func cmdLint(") {
			start = i
			continue
		}
		if start >= 0 && line == "}" {
			end = i
			break
		}
	}
	if start < 0 || end < 0 {
		t.Fatalf("cmd/nova-swarm/lint.go: func cmdLint was not found whole; this test cuts its body out of the source")
	}
	body := strings.Join(lines[start:end], "\n")

	flagRe := regexp.MustCompile(`f\.fs\.(?:String|Bool|Int|Duration)\("([^"]+)"`)
	type flag struct {
		name string
		line int
	}
	var flags []flag
	seen := map[string]bool{}
	add := func(name string, line int) {
		if seen[name] {
			return
		}
		seen[name] = true
		flags = append(flags, flag{name: name, line: line})
	}
	for i, line := range strings.Split(body, "\n") {
		for _, m := range flagRe.FindAllStringSubmatch(line, -1) {
			add(m[1], start+i+1)
		}
		// `--max` is the shared ceiling every listing carries, registered by
		// `maxFlag(f.fs)` rather than by an `f.fs.<Type>` call, so the regex
		// above cannot see it. It is a real flag this verb's synopsis names, and
		// a mutation that drops it from the document must redden this test.
		if strings.Contains(line, "maxFlag(") {
			add("max", start+i+1)
		}
	}
	if len(flags) < 5 {
		t.Fatalf("cmd/nova-swarm/lint.go: found only %d flags registered directly in cmdLint; a scan that finds almost none would pass by asking nothing", len(flags))
	}

	reference, err := os.ReadFile("../../docs/CLI.md")
	if err != nil {
		t.Fatalf("docs/CLI.md: %v; it is the command reference a person reads to find a verb's flags", err)
	}
	usage, found := "", 0
	for _, line := range strings.Split(string(reference), "\n") {
		if strings.HasPrefix(line, "nova-swarm lint ") {
			if found == 0 {
				usage = line
			}
			found++
		}
	}
	if found == 0 {
		t.Fatalf("docs/CLI.md has no line beginning %q; the command reference must document this verb", "nova-swarm lint ")
	}

	// namesFlagAtBoundary reports whether the usage line carries --<name> as a
	// whole flag token, so --max is not satisfied by a longer --max-input and
	// --card is not satisfied by some flag that merely ends in card.
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

	for _, f := range flags {
		if !namesFlagAtBoundary(usage, f.name) {
			t.Errorf("docs/CLI.md's line for `nova-swarm lint` does not name --%s, registered at cmd/nova-swarm/lint.go:%d; docs/CLI.md is the command reference a person reads, so a flag it does not name is a flag nobody can find", f.name, f.line)
		}
	}

	// The control, in the other direction: every --<word> the usage line names
	// must be a flag the source registers, so the document cannot pass by
	// inventing a token the tool does not define.
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
		t.Errorf("docs/CLI.md's `nova-swarm lint` usage line names %s, which cmd/nova-swarm/lint.go does not register; the document must not invent a flag the tool does not define", strings.Join(spurious, ", "))
	}
}
