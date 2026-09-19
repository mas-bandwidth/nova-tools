package main

import (
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/onboarding"
)

// #1451 measured nova-cairn's refusals and found 0 of its four bare verbs named
// the door. ONBOARDING.md point 1 promises an invocation the tool cannot run
// prints `<tool>[ <verb>]: <what was wrong>; run: <tool> help`, so this asserts
// every refusal line ENDS at that literal door, in the exact spelling the
// existing helper writes.
//
// The suffix is asserted rather than `strings.Contains(stderr, "help")`: a line
// saying `--store is required; refusing to guess` followed by a dumped usage
// banner contains "help" and gives the reader nothing to type, and a near miss
// with the wrong spacing or semicolon must fail the literal test.
func TestIssue1451EveryMissingFlagRefusalNamesTheDoor(t *testing.T) {
	exit, stdout, stderr := runCLI(t, "", "help")
	if exit != 0 {
		t.Fatalf("`nova-cairn help` must be exit 0, got %d; stderr: %s", exit, stderr)
	}
	examples, err := onboarding.ExampleLines(stdout, "nova-cairn")
	if err != nil {
		t.Fatalf("cannot enumerate the bare verbs from the help banner: %v", err)
	}
	seen := map[string]bool{}
	for _, ex := range examples {
		fields := strings.Fields(ex)
		if len(fields) < 2 {
			t.Fatalf("the help example %q names no verb", ex)
		}
		verb := fields[1]
		if seen[verb] {
			continue
		}
		seen[verb] = true
		t.Run(verb, func(t *testing.T) {
			code, out, errOut := runCLI(t, "", verb)
			if code != 2 {
				t.Errorf("`nova-cairn %s` with no flags exited %d, want 2", verb, code)
			}
			if out != "" {
				t.Errorf("`nova-cairn %s` with no flags wrote to stdout: %q; a refusal belongs on stderr", verb, out)
			}
			lines := strings.Split(strings.TrimSuffix(errOut, "\n"), "\n")
			if len(lines) == 0 || lines[0] == "" {
				t.Fatalf("`nova-cairn %s` with no flags printed no refusal", verb)
			}
			for _, line := range lines {
				if !strings.HasSuffix(line, "; run: nova-cairn help") {
					t.Errorf("`nova-cairn %s` refusal does not end at the door: %q", verb, line)
				}
			}
		})
	}
	if len(seen) == 0 {
		t.Fatal("no verbs enumerated from the help banner; the test would pass by checking nothing")
	}
}
