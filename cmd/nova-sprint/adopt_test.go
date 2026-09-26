//go:build functional

package main

import (
	"bytes"
	"strings"
	"testing"
)

func runAdoptVerb(t *testing.T, seat string, args ...string) (int, string, string) {
	t.Helper()
	t.Setenv(seatEnv, seat)
	var out, errOut bytes.Buffer
	code := run(append([]string{"adopt"}, args...), &out, &errOut)
	return code, out.String(), errOut.String()
}

// TestAdoptVerbMatrix is the verb over the real functions on a fresh
// throwaway Redis with no library loaded: receipt (seat from NOVA_FRIEND),
// the no-gap refusal (exit 1), a retry (UNCHANGED), usage (exit 2), then
// matrix, matrix --md and status read back from the store.
func TestAdoptVerbMatrix(t *testing.T) {
	addr := startThrowawayRedis(t)

	code, out, errOut := runAdoptVerb(t, "rowan", "receipt", "--redis", addr, "--verb", "nova-sprint land stream",
		"--pov", "coordinator", "--state", "in-flight", "--gap", "nova-tools#3975", "--hand", "stream branch built by hand")
	if code != 0 || !strings.HasPrefix(out, `ADOPT RECEIPT verb="nova-sprint land stream" who=rowan pov=coordinator state=in-flight gap=nova-tools#3975 at=`) || !strings.HasSuffix(out, " receipts=1\n") {
		t.Fatalf("receipt: %d %q %q", code, out, errOut)
	}
	code, out, errOut = runAdoptVerb(t, "stella", "receipt", "--redis", addr, "--verb", "nova-sprint pitstop", "--pov", "friend", "--state", "adopted")
	if code != 0 || !strings.Contains(out, "who=stella") {
		t.Fatalf("receipt 2: %d %q %q", code, out, errOut)
	}
	code, out, _ = runAdoptVerb(t, "stella", "receipt", "--redis", addr, "--verb", "nova-sprint pitstop", "--pov", "friend", "--state", "adopted")
	if code != 0 || !strings.HasPrefix(out, `ADOPT UNCHANGED verb="nova-sprint pitstop" who=stella pov=friend at=`) {
		t.Fatalf("retry: %d %q", code, out)
	}
	code, _, errOut = runAdoptVerb(t, "emma", "receipt", "--redis", addr, "--verb", "nova-sprint pitstop", "--pov", "reader", "--state", "adopted-gaps")
	if code != 1 || !strings.HasPrefix(errOut, `ADOPT REFUSED reason=no-gap verb="nova-sprint pitstop"; remedy:`) {
		t.Fatalf("no-gap: %d %q", code, errOut)
	}
	if code, _, errOut = runAdoptVerb(t, "", "receipt", "--redis", addr, "--verb", "x", "--pov", "bench", "--state", "adopted"); code != 2 || !strings.Contains(errOut, "--as") {
		t.Fatalf("no seat: %d %q", code, errOut)
	}
	if code, _, _ = runAdoptVerb(t, "rowan", "status", "--redis", addr, "--md"); code != 2 {
		t.Fatalf("--md on status: %d", code)
	}

	code, out, _ = runAdoptVerb(t, "rowan", "matrix", "--redis", addr)
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	if code != 0 || len(lines) != 3 || !strings.HasPrefix(lines[0], `ADOPT ROW verb="nova-sprint land stream" state=in-flight who=rowan at=`) ||
		!strings.HasPrefix(lines[1], `ADOPT ROW verb="nova-sprint pitstop" state=adopted who=stella`) || lines[2] != "ADOPT MATRIX verbs=2 adopted 1/2 50%" {
		t.Fatalf("matrix: %d %q", code, out)
	}
	code, out, _ = runAdoptVerb(t, "rowan", "matrix", "--redis", addr, "--md")
	if code != 0 || !strings.HasPrefix(out, "| hand step today | verb | state | who | when | pov | gap |\n") || !strings.Contains(out, "| stream branch built by hand | `nova-sprint land stream` | in-flight | rowan | ") {
		t.Fatalf("matrix --md: %d %q", code, out)
	}
	code, out, _ = runAdoptVerb(t, "rowan", "status", "--redis", addr)
	if code != 0 || out != "ADOPT STATUS adopted 1/2 50%\n" {
		t.Fatalf("status: %d %q", code, out)
	}
}
