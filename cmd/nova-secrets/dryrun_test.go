package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// `--dry-run` through the built binary: place's plan is what the real run then does,
// the dry run ran no ssh and wrote no receipt (not even the receipts directory), and
// the help of the three verbs that take the flag says what it does and shows a line
// that uses it. Fixtures and the fake ssh and sops the place tests already use; no real
// secret, key or store.

func fieldOf(t *testing.T, text, key string) string {
	t.Helper()
	for _, tok := range strings.Fields(text) {
		if v, ok := strings.CutPrefix(tok, key+"="); ok {
			return v
		}
	}
	t.Fatalf("no %s= in:\n%s", key, text)
	return ""
}

func TestPlaceDryRunPrintsThePlanAndWritesNothing(t *testing.T) {
	t.Parallel()
	f := newPlaceFixture(t)

	args := append(f.placeArgs("mini", "DEEPSEEK_API_KEY", f.remotePath), "--dry-run")
	stdout, stderr, code := runNovaSecrets(f.bin, args...)
	if code != 0 {
		t.Fatalf("place --dry-run exit=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if stderr != "" {
		t.Errorf("a dry run wrote to stderr: %q", stderr)
	}
	if strings.Contains(stdout, f.value) {
		t.Fatalf("the value reached the plan:\n%s", stdout)
	}
	for _, p := range []string{f.sshArgsFile, f.sshStdinFile, f.receipts} {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("the dry run wrote %s", p)
		}
	}

	lines := strings.Split(strings.TrimRight(stdout, "\n"), "\n")
	if len(lines) != 4 {
		t.Fatalf("want 4 lines (path, ssh, receipt, DRY-RUN OK), got %d:\n%s", len(lines), stdout)
	}
	for i, prefix := range []string{
		"SECRETS PLACE PLAN machine=mini secret=DEEPSEEK_API_KEY path=" + f.remotePath + " mode=0600 file=rowan.yaml head=- blob=",
		"SECRETS PLACE PLAN ssh=" + f.ssh + " target=mini.example writes=" + f.remotePath,
		"SECRETS PLACE PLAN receipt=" + filepath.Join(f.receipts, "mini.receipt") + " action=add",
		"SECRETS PLACE DRY-RUN OK machine=mini secret=DEEPSEEK_API_KEY nothing written, no ssh run",
	} {
		if !strings.HasPrefix(lines[i], prefix) {
			t.Errorf("line %d:\n got %s\nwant prefix %s", i, lines[i], prefix)
		}
	}
	plannedBlob := fieldOf(t, lines[0], "blob")

	// The real run does what was planned: the same path, the same sealed file.
	stdout, stderr, code = runNovaSecrets(f.bin, f.placeArgs("mini", "DEEPSEEK_API_KEY", f.remotePath)...)
	if code != 0 {
		t.Fatalf("place exit=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if got := fieldOf(t, stdout, "blob"); got != plannedBlob {
		t.Errorf("the real run wrote blob=%s, the plan said %s", got, plannedBlob)
	}
	if got := fieldOf(t, stdout, "path"); got != f.remotePath {
		t.Errorf("the real run wrote path=%s, the plan said %s", got, f.remotePath)
	}
	sshArgs, err := os.ReadFile(f.sshArgsFile)
	if err != nil || !strings.Contains(string(sshArgs), f.remotePath) || !strings.Contains(string(sshArgs), "mini.example") {
		t.Errorf("the real run's ssh call is not the planned target and path: %q (%v)", sshArgs, err)
	}

	// With the receipt now on disk the same dry run says the placement would change nothing.
	again, stderr, code := runNovaSecrets(f.bin, args...)
	if code != 0 {
		t.Fatalf("second place --dry-run exit=%d stderr=%q", code, stderr)
	}
	if !strings.Contains(again, "action=unchanged") {
		t.Errorf("a secret already placed from this sealed file is not reported as unchanged:\n%s", again)
	}
	receipt, err := os.ReadFile(filepath.Join(f.receipts, "mini.receipt"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(receipt), "\n") != 1 {
		t.Errorf("the dry run changed the receipt file: %q", receipt)
	}
}

// TestPlaceDryRunRefusesWhereTheRealRunRefuses: an unregistered machine and an absent
// secret are exit 2 from the dry run, before any plan line, and nothing is written.
func TestPlaceDryRunRefusesWhereTheRealRunRefuses(t *testing.T) {
	t.Parallel()
	f := newPlaceFixture(t)

	stdout, stderr, code := runNovaSecrets(f.bin, append(f.placeArgs("nowhere", "DEEPSEEK_API_KEY", f.remotePath), "--dry-run")...)
	if code != 2 || stdout != "" || !strings.Contains(stderr, "nowhere") {
		t.Errorf("unknown machine: exit=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	stdout, stderr, code = runNovaSecrets(f.bin, append(f.placeArgs("mini", "NOT_THERE", f.remotePath), "--dry-run")...)
	if code != 2 || stdout != "" || !strings.Contains(stderr, "NOT_THERE") {
		t.Errorf("absent secret: exit=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if _, err := os.Stat(f.receipts); err == nil {
		t.Errorf("a refused dry run created the receipts directory")
	}
}

var spaces = regexp.MustCompile(`\s+`)

// TestDryRunIsInTheHelpOfEveryVerbThatTakesIt: `<verb> -h` says "--dry-run prints the plan
// and writes nothing" and carries an example line that uses the flag; so does `help`.
func TestDryRunIsInTheHelpOfEveryVerbThatTakesIt(t *testing.T) {
	t.Parallel()
	bin := buildNovaSecrets(t)
	runNovaSecrets(bin, "version")

	for _, verb := range []string{"place", "seal", "seat inject"} {
		verb := verb
		t.Run(verb, func(t *testing.T) {
			t.Parallel()
			out, stderr, code := runNovaSecrets(bin, append(strings.Fields(verb), "-h")...)
			if code != 0 || stderr != "" {
				t.Fatalf("%s -h exit=%d stderr=%q", verb, code, stderr)
			}
			flat := spaces.ReplaceAllString(out, " ")
			if !strings.Contains(flat, "--dry-run prints the plan and writes nothing") {
				t.Errorf("%s -h does not say what --dry-run does:\n%s", verb, out)
			}
			example := false
			for _, l := range strings.Split(out, "\n") {
				l = strings.TrimSpace(l)
				if strings.HasPrefix(spaces.ReplaceAllString(l, " "), "nova-secrets "+verb+" ") && strings.HasSuffix(l, "--dry-run") {
					example = true
				}
			}
			if !example {
				t.Errorf("%s -h shows no example line ending in --dry-run:\n%s", verb, out)
			}
		})
	}

	out, _, code := runNovaSecrets(bin, "help")
	if code != 0 || !strings.Contains(spaces.ReplaceAllString(out, " "), "--dry-run prints the plan and writes nothing") {
		t.Errorf("help does not say what --dry-run does (exit %d):\n%s", code, out)
	}
}
