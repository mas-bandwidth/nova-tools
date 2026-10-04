package docs

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// docs/CLI.md is the command reference AND the list `nova-check dogfood ledger`
// reads, so a flag added to a verb and not to the reference is a flag nobody
// can find. This holds the reference line for `nova-check dogfood gate` against
// the flags the verb registers directly in its own function; the flags that come
// from the shared helpers are not covered here, because those helpers serve
// several verbs. #1466 is the flag that went missing.
func TestTheCLIReferenceNamesEveryDogfoodGateFlag(t *testing.T) {
	t.Parallel()

	source, err := os.ReadFile("../../cmd/nova-check/dogfood.go")
	require.NoError(t, err, "cmd/nova-check/dogfood.go: %v; the verb's flag registrations are read from there", err)
	lines := strings.Split(string(source), "\n")
	start, end := -1, -1
	for i, line := range lines {
		if start < 0 && strings.HasPrefix(line, "func cmdDogfoodGate(") {
			start = i
			continue
		}
		if start >= 0 && line == "}" {
			end = i
			break
		}
	}
	require.False(t, start < 0 || end < 0, "cmd/nova-check/dogfood.go: func cmdDogfoodGate was not found whole; this test cuts its body out of the source")
	body := strings.Join(lines[start:end], "\n")

	flagRe := regexp.MustCompile(`fs\.(Bool|String|Int|Duration)\("([^"]+)"`)
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
	require.GreaterOrEqual(t, len(flags), 2, "cmd/nova-check/dogfood.go: found only %d flags registered directly in cmdDogfoodGate; a scan that finds nothing would pass by asking nothing", len(flags))

	reference, err := os.ReadFile("../../docs/CLI.md")
	require.NoError(t, err, "docs/CLI.md: %v; it is the command reference a person reads to find a verb's flags", err)
	usage, found := "", 0
	for _, line := range strings.Split(string(reference), "\n") {
		if strings.HasPrefix(line, "nova-check dogfood gate ") {
			usage = line
			found++
		}
	}
	require.NotZero(t, found, "docs/CLI.md has no line beginning %q; the command reference must document this verb", "nova-check dogfood gate ")
	require.LessOrEqual(t, found, 1, "docs/CLI.md has %d lines beginning %q; the reference must name the verb exactly once", found, "nova-check dogfood gate ")

	for _, f := range flags {
		assert.Contains(t, usage, "--"+f.name, "docs/CLI.md's line for `nova-check dogfood gate` does not name --%s, registered at cmd/nova-check/dogfood.go:%d; docs/CLI.md is the command reference and the list `nova-check dogfood ledger` reads, so a flag it does not name is a flag a person cannot find", f.name, f.line)
	}
}
