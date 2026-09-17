package pulse

import (
	"strings"
	"testing"
)

// The production liveness reader must count running card processes -- ps/pgrep naming the
// job dir, or a process whose cwd is under the slot -- the authoritative shape
// bench-hygiene.sh's live_slot uses. It must never fall back on the log-age test the
// bench-hygiene incident retired: a fresh harness-output.log can belong to a dead card and
// a long card's log is older than fifteen minutes while it is still alive.
func TestFleetStatusScriptCountsRunningProcessesNotLogAge(t *testing.T) {
	script := fleetStatusScript("/home/bench")
	for _, want := range []string{"pgrep -f", "/proc/", "/cwd", "nproc", "/proc/loadavg", "MemAvailable"} {
		if !strings.Contains(script, want) {
			t.Errorf("the liveness script does not carry %q:\n%s", want, script)
		}
	}
	for _, banned := range []string{"harness-output.log", "RESULT.md", "-mmin", "stat -c", "date +%s"} {
		if strings.Contains(script, banned) {
			t.Errorf("the liveness script still reads log age (%q):\n%s", banned, script)
		}
	}
}
