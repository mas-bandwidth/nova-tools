package main

import (
	"strings"
	"testing"
)

// Every registered flag has a help description that tells a cold reader what value it takes
// or what effect the switch has (ledger and report help are inspection modes).
func TestEveryVerbFlagHasADescription(t *testing.T) {
	t.Parallel()

	for _, verb := range []string{"fold", "report", "ledger", "sum", "check", "sources", "profiles", "session"} {
		t.Run(verb, func(t *testing.T) {
			t.Parallel()
			r := invoke(t, verb, "-h")
			wantExit(t, r, 0)
			inFlags := false
			count := 0
			for _, line := range strings.Split(r.stdout, "\n") {
				if line == "flags:" {
					inFlags = true
					continue
				}
				if inFlags && (strings.HasPrefix(line, "exit codes:") || strings.HasPrefix(line, "exit code:")) {
					break
				}
				if !inFlags || !strings.HasPrefix(line, "  --") {
					continue
				}
				count++
				if !strings.Contains(line[2:], "  ") {
					t.Errorf("%s flag has no help description: %s", verb, line)
				}
			}
			if count == 0 && verb != "version" {
				t.Errorf("%s help listed no flags; the test did not inspect anything", verb)
			}
		})
	}
}

// The two report modes have different inputs and outputs; help names each form explicitly.
func TestReportHelpNamesItsTwoModes(t *testing.T) {
	t.Parallel()

	r := invoke(t, "report", "-h")
	wantExit(t, r, 0)
	wantContains(t, r.stdout, "mode: local note body")
	wantContains(t, r.stdout, "mode: Redis month summary")
}
