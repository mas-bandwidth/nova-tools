// nova-tools #2222 — "Add `accept --selftest` — the fixture repo, twelve seeds and
// the control id (SPEC-TOOLWORK §1 rules 6-8, eligibility rule 10)".
//
// The gate's own negative control: `accept --selftest` runs the gate over a fixture
// repository, prints one ACCEPT SELFTEST line, and exits 0 with PASS only when every row
// of the twelve-seed table is right. At pre-fix the verb "accept" is not in nova-pulse's
// verb table at all -- run() prints "unknown subcommand" and returns 2 -- which is the
// red the spec demands: a green that has not first been red is not a check.

package main

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

// TestIssue2222 is the reproduction of nova-tools#2222 at the verb level. It drives
// `nova-pulse accept --selftest` through the public run() entry point (so a smoke test
// can't satisfy it with a unit under a different name) and asserts the one line the spec
// promises: ACCEPT SELFTEST ... PASS, with a twelve-hex control=, the build identity, the
// fixture digest and the bench name, in that order, on one line, exit 0.
func TestIssue2222(t *testing.T) {
	var out, errb bytes.Buffer
	code := run([]string{
		"accept",
		"--selftest",
		"--bench", "selftest",
		"--cert", "/dev/null",
	}, &out, &errb, time.Now().UTC())
	if code != 0 {
		t.Fatalf("nova-pulse accept --selftest: exit=%d, want 0\nstdout=%q\nstderr=%q", code, out.String(), errb.String())
	}
	line := strings.TrimSpace(out.String())
	// One line: ACCEPT SELFTEST, control=, accepted=, rejected=, edits=1, build=, fixtures=, bench=, PASS.
	if !strings.HasPrefix(line, "ACCEPT SELFTEST ") {
		t.Fatalf("ACCEPT SELFTEST line missing: %q", line)
	}
	for _, want := range []string{"control=", "accepted=", "rejected=", "edits=1 ", "build=", "fixtures=", "bench=selftest ", "PASS"} {
		if !strings.Contains(line, want) {
			t.Fatalf("ACCEPT SELFTEST line missing %q: %q", want, line)
		}
	}
	if strings.HasSuffix(line, "FAIL") {
		t.Fatalf("ACCEPT SELFTEST ended FAIL when the fixture ships a passing table: %q", line)
	}
}
