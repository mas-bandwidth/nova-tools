package docs

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

type swarmFlag struct {
	name string
	line int
}

func readSwarmLintFlags(t *testing.T) []swarmFlag {
	t.Helper()
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

	flagRe := regexp.MustCompile(`f\.fs\.(Bool|String|Int|Duration)\("([^"]+)"`)
	var flags []swarmFlag
	for i, line := range lines[start:end] {
		lineNo := start + i + 1
		for _, m := range flagRe.FindAllStringSubmatch(line, -1) {
			flags = append(flags, swarmFlag{name: m[2], line: lineNo})
		}
		// `--max` is registered via helper `maxFlag(f.fs)` in main.go
		if strings.Contains(line, "maxFlag(f.fs)") {
			flags = append(flags, swarmFlag{name: "max", line: lineNo})
		}
	}
	if len(flags) < 5 {
		t.Fatalf("cmd/nova-swarm/lint.go: found only %d flags registered in cmdLint, want at least 5 (card, rules, typed, trust, max); a scan that finds fewer has missed a registration", len(flags))
	}
	return flags
}

func checkFlagsNamed(usage string, flags []swarmFlag) []swarmFlag {
	if j := strings.Index(usage, "#"); j >= 0 {
		usage = usage[:j]
	}
	var missing []swarmFlag
	for _, f := range flags {
		if !strings.Contains(usage, "--"+f.name) {
			missing = append(missing, f)
		}
	}
	return missing
}

// `nova-swarm lint` is what a card is checked with before it is launched, and
// docs/CLI.md is the command reference a person is sent to to find a verb's
// flags. The reference did not name the verb at all, so this holds the
// reference's own synopsis line for the verb against the flags the verb
// registers directly or via helpers in its own function; a flag the reference
// does not name is a flag nobody can find (#1855).
//
// The rule is that AT LEAST ONE line of docs/CLI.md begins `nova-swarm lint `,
// and the FIRST such line -- the synopsis line -- must name every flag found,
// including `--max`.
func TestTheCLIReferenceNamesEverySwarmLintFlag(t *testing.T) {
	t.Parallel()

	flags := readSwarmLintFlags(t)

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

	missing := checkFlagsNamed(usage, flags)
	for _, f := range missing {
		t.Errorf("docs/CLI.md's line for `nova-swarm lint` does not name --%s, registered at cmd/nova-swarm/lint.go:%d; docs/CLI.md is the command reference a person reads, so a flag it does not name is a flag nobody can find", f.name, f.line)
	}
}

// TestTheCLIReferenceRefusesMissingMaxFlag verifies as a mutation test that if
// `--max` is dropped from the synopsis line, the verification fails.
func TestTheCLIReferenceRefusesMissingMaxFlag(t *testing.T) {
	t.Parallel()

	flags := readSwarmLintFlags(t)

	// Simulated synopsis without --max
	mutatedUsage := "nova-swarm lint     --card <file> [--typed] [--trust <file>] | --rules"
	missing := checkFlagsNamed(mutatedUsage, flags)

	hasMax := false
	for _, f := range missing {
		if f.name == "max" {
			hasMax = true
			break
		}
	}
	if !hasMax {
		t.Fatalf("checkFlagsNamed failed to report missing --max flag when omitted from synopsis: got missing=%v", missing)
	}
}
