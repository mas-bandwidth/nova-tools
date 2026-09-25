package main

import (
	"sort"
	"strings"
	"testing"
)

// TestHelpListsExactlyTheThreeKeptVerbs is the control for deleting the frozen
// nova-pulse (superseded by nova-sprint): the help lists status, cut and harvest and
// nothing else, and every retired verb is an unknown subcommand, exit 2.
func TestHelpListsExactlyTheThreeKeptVerbs(t *testing.T) {
	exit, stdout, stderr := invokePulse(t, "help")
	if exit != 0 {
		t.Fatalf("help exit = %d; stderr=%s", exit, stderr)
	}
	seen := map[string]bool{}
	for _, line := range strings.Split(stdout, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == "nova-pulse" && !strings.HasPrefix(line, " ") {
			seen[fields[1]] = true
		}
	}
	var verbs []string
	for v := range seen {
		verbs = append(verbs, v)
	}
	sort.Strings(verbs)
	if got := strings.Join(verbs, " "); got != "cut harvest status" {
		t.Fatalf("nova-pulse help lists %q, want exactly \"cut harvest status\"", got)
	}
	for _, gone := range []string{"pool", "launch", "fill", "ci", "index", "beat", "watch", "wait", "manager", "progress", "capacity", "gate", "run", "triage", "sweep", "reap", "event", "fold", "hygiene", "fleet", "sprint", "wake", "sleep", "width"} {
		exit, _, stderr := invokePulse(t, gone)
		if exit != 2 || !strings.Contains(stderr, "unknown subcommand") {
			t.Errorf("nova-pulse %s: exit %d stderr %q; a retired verb is an unknown subcommand, exit 2", gone, exit, stderr)
		}
	}
}
