package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	require.Equal(t, 0, code, "place --dry-run exit=%d stdout=%q stderr=%q", code, stdout, stderr)
	assert.Empty(t, stderr, "a dry run wrote to stderr: %q", stderr)
	require.NotContains(t, stdout, f.value, "the value reached the plan:\n%s", stdout)
	for _, p := range []string{f.sshArgsFile, f.sshStdinFile, f.receipts} {
		_, err := os.Stat(p)
		assert.Error(t, err, "the dry run wrote %s", p)
	}

	lines := strings.Split(strings.TrimRight(stdout, "\n"), "\n")
	require.Len(t, lines, 4, "want 4 lines (path, ssh, receipt, DRY-RUN OK), got %d:\n%s", len(lines), stdout)
	for i, prefix := range []string{
		"SECRETS PLACE PLAN machine=mini secret=DEEPSEEK_API_KEY path=" + f.remotePath + " mode=0600 file=rowan.yaml head=- blob=",
		"SECRETS PLACE PLAN ssh=" + f.ssh + " target=mini.example writes=" + f.remotePath,
		"SECRETS PLACE PLAN receipt=" + filepath.Join(f.receipts, "mini.receipt") + " action=add",
		"SECRETS PLACE DRY-RUN OK machine=mini secret=DEEPSEEK_API_KEY nothing written, no ssh run",
	} {
		assert.True(t, strings.HasPrefix(lines[i], prefix), "line %d:\n got %s\nwant prefix %s", i, lines[i], prefix)
	}
	plannedBlob := fieldOf(t, lines[0], "blob")

	// The real run does what was planned: the same path, the same sealed file.
	stdout, stderr, code = runNovaSecrets(f.bin, f.placeArgs("mini", "DEEPSEEK_API_KEY", f.remotePath)...)
	require.Equal(t, 0, code, "place exit=%d stdout=%q stderr=%q", code, stdout, stderr)
	got := fieldOf(t, stdout, "blob")
	assert.Equal(t, plannedBlob, got, "the real run wrote blob=%s, the plan said %s", got, plannedBlob)
	{
		got := fieldOf(t, stdout, "path")
		assert.Equal(t, f.remotePath, got, "the real run wrote path=%s, the plan said %s", got, f.remotePath)
	}
	sshArgs, err := os.ReadFile(f.sshArgsFile)
	assert.NoError(t, err, "the real run's ssh call is not the planned target and path: %q (%v)", sshArgs, err)
	assert.Contains(t, string(sshArgs), f.remotePath, "the real run's ssh call is not the planned target and path: %q (%v)", sshArgs, err)
	assert.Contains(t, string(sshArgs), "mini.example", "the real run's ssh call is not the planned target and path: %q (%v)", sshArgs, err)

	// With the receipt now on disk the same dry run says the placement would change nothing.
	again, stderr, code := runNovaSecrets(f.bin, args...)
	require.Equal(t, 0, code, "second place --dry-run exit=%d stderr=%q", code, stderr)
	assert.Contains(t, again, "action=unchanged", "a secret already placed from this sealed file is not reported as unchanged:\n%s", again)
	receipt, err := os.ReadFile(filepath.Join(f.receipts, "mini.receipt"))
	require.NoError(t, err)
	assert.Equal(t, 1, strings.Count(string(receipt), "\n"), "the dry run changed the receipt file: %q", receipt)
}

// TestPlaceDryRunRefusesWhereTheRealRunRefuses: an unregistered machine and an absent
// secret are exit 2 from the dry run, before any plan line, and nothing is written.
func TestPlaceDryRunRefusesWhereTheRealRunRefuses(t *testing.T) {
	t.Parallel()
	f := newPlaceFixture(t)

	stdout, stderr, code := runNovaSecrets(f.bin, append(f.placeArgs("nowhere", "DEEPSEEK_API_KEY", f.remotePath), "--dry-run")...)
	assert.Equal(t, 2, code, "unknown machine: exit=%d stdout=%q stderr=%q", code, stdout, stderr)
	assert.Empty(t, stdout, "unknown machine: exit=%d stdout=%q stderr=%q", code, stdout, stderr)
	assert.Contains(t, stderr, "nowhere", "unknown machine: exit=%d stdout=%q stderr=%q", code, stdout, stderr)
	stdout, stderr, code = runNovaSecrets(f.bin, append(f.placeArgs("mini", "NOT_THERE", f.remotePath), "--dry-run")...)
	assert.Equal(t, 2, code, "absent secret: exit=%d stdout=%q stderr=%q", code, stdout, stderr)
	assert.Empty(t, stdout, "absent secret: exit=%d stdout=%q stderr=%q", code, stdout, stderr)
	assert.Contains(t, stderr, "NOT_THERE", "absent secret: exit=%d stdout=%q stderr=%q", code, stdout, stderr)
	_, err := os.Stat(f.receipts)
	assert.Error(t, err, "a refused dry run created the receipts directory")
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
			require.Equal(t, 0, code, "%s -h exit=%d stderr=%q", verb, code, stderr)
			require.Empty(t, stderr, "%s -h exit=%d stderr=%q", verb, code, stderr)
			flat := spaces.ReplaceAllString(out, " ")
			assert.Contains(t, flat, "--dry-run print what the verb would write and write nothing", "%s -h does not say what --dry-run does:\n%s", verb, out)
			example := false
			for _, l := range strings.Split(out, "\n") {
				l = strings.TrimSpace(l)
				if strings.HasPrefix(spaces.ReplaceAllString(l, " "), "nova-secrets "+verb+" ") && strings.HasSuffix(l, "--dry-run") {
					example = true
				}
			}
			assert.True(t, example, "%s -h shows no example line ending in --dry-run:\n%s", verb, out)
		})
	}

	out, _, code := runNovaSecrets(bin, "help")
	assert.Equal(t, 0, code, "help does not show the dry-run flag (exit %d):\n%s", code, out)
	assert.Contains(t, spaces.ReplaceAllString(out, " "), "[--dry-run]", "help does not show the dry-run flag (exit %d):\n%s", code, out)
}
