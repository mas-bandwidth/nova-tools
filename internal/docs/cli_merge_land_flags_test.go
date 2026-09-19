package docs

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// `nova-merge land` is the one entrance to the merge queue, and docs/CLI.md is
// the command reference a person is sent to to find a verb's flags. The
// reference had no `land` section at all, so this holds the reference's own
// usage line for the verb against the flags the verb registers directly in its
// own function; a flag the reference does not name is a flag nobody can find
// (#1835).
func TestTheCLIReferenceNamesEveryMergeLandFlag(t *testing.T) {
	t.Parallel()

	source, err := os.ReadFile("../../cmd/nova-merge/land.go")
	if err != nil {
		t.Fatalf("cmd/nova-merge/land.go: %v; the verb's flag registrations are read from there", err)
	}
	lines := strings.Split(string(source), "\n")
	start, end := -1, -1
	for i, line := range lines {
		if start < 0 && strings.HasPrefix(line, "func cmdLand(") {
			start = i
			continue
		}
		if start >= 0 && line == "}" {
			end = i
			break
		}
	}
	if start < 0 || end < 0 {
		t.Fatalf("cmd/nova-merge/land.go: func cmdLand was not found whole; this test cuts its body out of the source")
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
	if len(flags) < 11 {
		t.Fatalf("cmd/nova-merge/land.go: found only %d flags registered directly in cmdLand; a scan that finds nothing would pass by asking nothing", len(flags))
	}

	reference, err := os.ReadFile("../../docs/CLI.md")
	if err != nil {
		t.Fatalf("docs/CLI.md: %v; it is the command reference a person reads to find a verb's flags", err)
	}
	usage, found := "", 0
	for _, line := range strings.Split(string(reference), "\n") {
		if strings.HasPrefix(line, "nova-merge land ") {
			usage = line
			found++
		}
	}
	if found == 0 {
		t.Fatalf("docs/CLI.md has no line beginning %q; the command reference must document this verb", "nova-merge land ")
	}
	if found > 1 {
		t.Fatalf("docs/CLI.md has %d lines beginning %q; the reference must name the verb exactly once", found, "nova-merge land ")
	}

	for _, f := range flags {
		if !strings.Contains(usage, "--"+f.name) {
			t.Errorf("docs/CLI.md's line for `nova-merge land` does not name --%s, registered at cmd/nova-merge/land.go:%d; docs/CLI.md is the command reference a person reads, so a flag it does not name is a flag nobody can find", f.name, f.line)
		}
	}
}
