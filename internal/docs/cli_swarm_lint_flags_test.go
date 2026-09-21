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
// The rule is that AT LEAST ONE line of docs/CLI.md begins `nova-swarm lint `,
// and the FIRST such line -- the synopsis line -- must name every flag found.
// Not "exactly one": the page's own convention gives a verb a synopsis line AND
// may give it an example inside its subsection, and a rule that forbids the
// second would be a rule about the page's furniture rather than about the
// reference being true.
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

	flagRe := regexp.MustCompile(`f\.fs\.(Bool|String|Int|Duration)\("([^"]+)"`)
	type flag struct {
		name string
		line int
	}
	var flags []flag
	for i, line := range strings.Split(body, "\n") {
		for _, m := range flagRe.FindAllStringSubmatch(line, -1) {
			flags = append(flags, flag{name: m[2], line: start + i + 1})
		}
	}
	if len(flags) < 4 {
		t.Fatalf("cmd/nova-swarm/lint.go: found only %d flags registered directly in cmdLint; a scan that finds nothing would pass by asking nothing", len(flags))
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

	for _, f := range flags {
		if !strings.Contains(usage, "--"+f.name) {
			t.Errorf("docs/CLI.md's line for `nova-swarm lint` does not name --%s, registered at cmd/nova-swarm/lint.go:%d; docs/CLI.md is the command reference a person reads, so a flag it does not name is a flag nobody can find", f.name, f.line)
		}
	}
}
