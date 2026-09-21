package secrets

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// docs/SPEC-SECRETS.md:1031, among the ten additions from the 2026-09-16/17
// dogfooding:
//
//	The bench standard checks **exactly one seat key per owner prefix** and that
//	check passes, because two keys for one owner is either a lost key still
//	trusted or a grant nobody declared.
//
// "The bench standard" is tools/bench-standard.sh, whose check (5) reads the
// `*.key` files under ~/.config/nova-secrets. The spec's unit is the OWNER
// PREFIX: the part of a seat key's name before its first `-`, so `rowan-claude`
// and `rowan-codex` are both owner `rowan`. The check must pass when every owner
// prefix carries exactly one key, and drift when one prefix carries two. This
// test runs the real script against a throwaway HOME and never reads a real key.
func TestBenchStandardChecksOneSeatKeyPerOwnerPrefix(t *testing.T) {
	skipPOSIXFakesOnWindows(t)
	script := filepath.Join("..", "..", "tools", "bench-standard.sh")
	if _, err := os.Stat(script); err != nil {
		t.Fatalf("the bench standard is missing: %v", err)
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bench-standard.sh is a bash script and bash is not on PATH here")
	}

	for _, tc := range []struct {
		name      string
		keys      []string
		wantDrift bool
	}{
		// One key under each owner prefix: the check passes.
		{"one key per owner prefix", []string{"rowan.key", "alex.key"}, false},
		// Two keys for one owner: a lost key still trusted, or a grant nobody declared.
		{"two keys for one owner prefix", []string{"rowan-claude.key", "rowan-codex.key"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			seatDir := filepath.Join(home, ".config", "nova-secrets")
			mustMkdir(t, seatDir, 0700)
			for _, name := range tc.keys {
				mustWrite(t, filepath.Join(seatDir, name), "AGE-SECRET-KEY-1FAKE\n", 0600)
			}
			mustMkdir(t, filepath.Join(home, "nova-bench"), 0755)

			out := runBenchStandardSeatCheck(t, bash, script, home)
			drift := seatKeyDriftLines(out)
			if tc.wantDrift && len(drift) == 0 {
				t.Errorf("two keys for one owner prefix did not drift; the standard must catch a lost key still trusted:\n%s", out)
			}
			if !tc.wantDrift && len(drift) != 0 {
				t.Errorf("docs/SPEC-SECRETS.md:1031 demands exactly one seat key per owner prefix, so %v is one key under each prefix and the check must pass; the standard drifted:\n%s",
					tc.keys, strings.Join(drift, "\n"))
			}
		})
	}
}

// runBenchStandardSeatCheck runs tools/bench-standard.sh with HOME on the
// throwaway directory and a minimal PATH. The seat-key check is the only output
// this test reads, so every other check is left to say whatever the temp HOME
// makes it say. No network, no ambient HOME, no real credential.
func runBenchStandardSeatCheck(t *testing.T, bash, script, home string) string {
	t.Helper()
	cmd := exec.Command(bash, script)
	cmd.Env = []string{
		"PATH=" + filepath.Join(home, "bin") + ":/usr/bin:/bin:/usr/sbin:/sbin",
		"HOME=" + home,
		"NOVA_GO=go1.26.5",
		"NOVA_PROBE_URL=https://probe.invalid/api.json",
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		if _, ok := err.(*exec.ExitError); !ok {
			t.Fatalf("running %s: %v\n%s", script, err, out)
		}
	}
	return string(out)
}

// seatKeyDriftLines are the standard's own lines about the seat-key count.
func seatKeyDriftLines(out string) []string {
	var lines []string
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, "DRIFT seat keys=") {
			lines = append(lines, l)
		}
	}
	return lines
}
