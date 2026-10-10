package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLinksRefusesAnEmptyTreeUnlessAllowEmpty pins looks["links"]: the verb's OK
// line counts the markdown files it read, so a walk that read none is FAILED,
// not a green over nothing (docs/STANDARD.md section 2, exit codes tell the
// truth). The FAILED line names the zero count and the --allow-empty way out,
// the JSON is the same value as the line, and --allow-empty keeps the OK over
// the empty set.
func TestLinksRefusesAnEmptyTreeUnlessAllowEmpty(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	code, stdout, stderr := runCheck(t, "links", "--dir", dir)
	require.EqualValues(t, 1, code, "empty tree: exit = %d, want 1; stdout = %q stderr = %q", code, stdout, stderr)
	assert.Contains(t, stderr, "LINKS FAILED", "no FAILED line over the empty tree:\n%s", stderr)
	assert.Contains(t, stderr, "files=0", "the FAILED line does not name the zero count:\n%s", stderr)
	assert.Contains(t, stderr, "looked at nothing", "the FAILED line does not name the why:\n%s", stderr)
	assert.Contains(t, stderr, "--allow-empty", "the FAILED line does not name the way out:\n%s", stderr)
	assert.EqualValues(t, "", stdout, "a FAILED links wrote to stdout: %q", stdout)

	code, stdout, stderr = runCheck(t, "links", "--dir", dir, "--allow-empty")
	require.EqualValues(t, 0, code, "--allow-empty: exit = %d, want 0; stderr = %q", code, stderr)
	assert.Contains(t, stdout, "LINKS OK", "the deliberate empty set did not print an OK line: %q", stdout)
	assert.Contains(t, stdout, "files=0", "the OK line does not name the zero count: %q", stdout)

	code, stdout, stderr = runCheck(t, "links", "--dir", dir, "--json")
	require.EqualValues(t, 1, code, "json empty tree: exit = %d, want 1; stderr = %q", code, stderr)
	assert.Contains(t, stdout, `"status":"failed"`, "the JSON is not the failed value the line is: %q", stdout)
	assert.Contains(t, stdout, `"looked at nothing: files=0"`, "the JSON does not carry the why: %q", stdout)
	assert.Contains(t, stdout, "--allow-empty", "the JSON remedy does not name the way out: %q", stdout)

	code, stdout, stderr = runCheck(t, "links", "--dir", dir, "--json", "--allow-empty")
	require.EqualValues(t, 0, code, "json --allow-empty: exit = %d, want 0; stderr = %q", code, stderr)
	assert.Contains(t, stdout, `"status":"ok"`, "the JSON is not the ok value: %q", stdout)
}

// TestSpellingRefusesAnEmptyTreeUnlessAllowEmpty pins looks["spelling"]: a --dir
// with no file and a --path glob that matches nothing are both a read of zero
// files, so both are FAILED until --allow-empty says the empty set is the
// answer (docs/STANDARD.md section 2, exit codes tell the truth).
func TestSpellingRefusesAnEmptyTreeUnlessAllowEmpty(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	for _, args := range [][]string{
		{"spelling", "--dir", dir},
		{"spelling", "--dir", dir, "--path", "nomatch/*.md"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			t.Parallel()

			code, stdout, stderr := runCheck(t, args...)
			require.EqualValues(t, 1, code, "%v: exit = %d, want 1; stdout = %q stderr = %q", args, code, stdout, stderr)
			assert.Contains(t, stderr, "SPELLING FAILED", "%v: no FAILED line over the empty read:\n%s", args, stderr)
			assert.Contains(t, stderr, "files=0", "%v: the FAILED line does not name the zero count:\n%s", args, stderr)
			assert.Contains(t, stderr, "looked at nothing", "%v: the FAILED line does not name the why:\n%s", args, stderr)
			assert.Contains(t, stderr, "--allow-empty", "%v: the FAILED line does not name the way out:\n%s", args, stderr)

			withFlag := append(append([]string(nil), args...), "--allow-empty")
			code, stdout, stderr = runCheck(t, withFlag...)
			require.EqualValues(t, 0, code, "%v: exit = %d, want 0; stderr = %q", withFlag, code, stderr)
			assert.Contains(t, stdout, "SPELLING OK", "%v: no OK over the deliberate empty set: %q", withFlag, stdout)

			jsonArgs := append(append([]string(nil), args...), "--json")
			code, stdout, stderr = runCheck(t, jsonArgs...)
			require.EqualValues(t, 1, code, "%v: exit = %d, want 1; stderr = %q", jsonArgs, code, stderr)
			assert.Contains(t, stdout, `"status":"failed"`, "%v: the JSON is not the failed value the line is: %q", jsonArgs, stdout)
			assert.Contains(t, stdout, `"looked at nothing: files=0"`, "%v: the JSON does not carry the why: %q", jsonArgs, stdout)
			assert.Contains(t, stdout, "--allow-empty", "%v: the JSON remedy does not name the way out: %q", jsonArgs, stdout)

			jsonAllowEmptyArgs := append(append([]string(nil), args...), "--json", "--allow-empty")
			code, stdout, stderr = runCheck(t, jsonAllowEmptyArgs...)
			require.EqualValues(t, 0, code, "%v: exit = %d, want 0; stderr = %q", jsonAllowEmptyArgs, code, stderr)
			assert.Contains(t, stdout, `"status":"ok"`, "%v: the JSON is not ok over deliberate empty set: %q", jsonAllowEmptyArgs, stdout)
		})
	}
}

// TestLooksVerbsDeclareAllowEmptyInHelp holds the declaration in the verb's own
// help: a verb that declares looks carries the flag that accepts nothing as the
// answer (docs/STANDARD.md section 3, help is never a refusal).
func TestLooksVerbsDeclareAllowEmptyInHelp(t *testing.T) {
	t.Parallel()

	for _, verb := range []string{"links", "spelling"} {
		code, stdout, stderr := runCheck(t, verb, "-h")
		require.EqualValues(t, 0, code, "%s -h: exit %d; stderr = %q", verb, code, stderr)
		assert.Contains(t, stdout, "--allow-empty", "%s -h does not declare --allow-empty:\n%s", verb, stdout)
	}
}
