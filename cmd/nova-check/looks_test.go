package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The no-green-over-nothing rule (STANDARD §2, exit codes tell the truth): a
// check verb whose OK line counts the files it read at zero has looked at
// nothing, so its OK is a green over nothing. links and spelling
// are the two check verbs here whose file count the walk can reach zero; both
// answer zero files with FAILED at exit 1 and name --allow-empty as the way out
// when nothing is the answer. internal/tool has no Verb.Looks on this tree, so
// each verb states the fact itself and addAllowEmpty registers the flag beside
// addMax.
func TestLinksRefusesAnEmptyTreeUnlessAllowEmpty(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	exit, stdout, stderr := runCheck(t, "links", "--dir", dir)
	require.EqualValues(t, 1, exit, "an empty tree: exit %d, want 1\nstdout: %s\nstderr: %s", exit, stdout, stderr)
	assert.Contains(t, stderr, "LINKS FAILED", "the zero-file read is not FAILED:\n%s", stderr)
	assert.Contains(t, stderr, "files=0", "the FAILED line does not name the count:\n%s", stderr)
	assert.Contains(t, stderr, "looked at nothing: files=0", "the FAILED line does not say why:\n%s", stderr)
	assert.Contains(t, stderr, "--allow-empty", "the FAILED line does not name its way out:\n%s", stderr)
	assert.Empty(t, stdout, "a FAILED line belongs on stderr, not stdout")

	exit, stdout, stderr = runCheck(t, "links", "--dir", dir, "--allow-empty")
	require.EqualValues(t, 0, exit, "--allow-empty: exit %d, want 0; stderr: %s", exit, stderr)
	assert.Contains(t, stdout, "LINKS OK", "--allow-empty is not an OK:\n%s", stdout)
	assert.Contains(t, stdout, "files=0", "--allow-empty lost the count:\n%s", stdout)

	exit, stdout, stderr = runCheck(t, "links", "--dir", dir, "--json")
	require.EqualValues(t, 1, exit, "--json empty tree: exit %d, want 1; stderr: %s", exit, stderr)
	assert.Contains(t, stdout, `"status":"failed"`, "the JSON is not the same failed value:\n%s", stdout)
	assert.Contains(t, stdout, `"looked at nothing: files=0"`, "the JSON is missing the why:\n%s", stdout)
	assert.Contains(t, stdout, "--allow-empty", "the JSON is missing the remedy:\n%s", stdout)
}

func TestSpellingRefusesAnEmptyTreeUnlessAllowEmpty(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	exit, stdout, stderr := runSpelling(t, "--dir", dir)
	require.EqualValues(t, 1, exit, "an empty tree: exit %d, want 1\nstdout: %s\nstderr: %s", exit, stdout, stderr)
	assert.Contains(t, stderr, "SPELLING FAILED", "the zero-file read is not FAILED:\n%s", stderr)
	assert.Contains(t, stderr, "looked at nothing: files=0", "the FAILED line does not say why:\n%s", stderr)
	assert.Contains(t, stderr, "--allow-empty", "the FAILED line does not name its way out:\n%s", stderr)

	exit, stdout, stderr = runSpelling(t, "--dir", dir, "--allow-empty")
	require.EqualValues(t, 0, exit, "--allow-empty: exit %d, want 0; stderr: %s", exit, stderr)
	assert.Contains(t, stdout, "SPELLING OK", "--allow-empty is not an OK:\n%s", stdout)
	assert.Contains(t, stdout, "files=0", "--allow-empty lost the count:\n%s", stdout)

	// The card's own spelling probe: a glob that matches nothing in a tree full
	// of files is still a read of zero files.
	exit, stdout, stderr = runSpelling(t, "--path", "nomatch/*.md")
	require.EqualValues(t, 1, exit, "a glob matching nothing: exit %d, want 1\nstdout: %s\nstderr: %s", exit, stdout, stderr)
	assert.Contains(t, stderr, "looked at nothing: files=0", "the glob read is not FAILED for reading nothing:\n%s", stderr)

	exit, stdout, stderr = runSpelling(t, "--path", "nomatch/*.md", "--allow-empty")
	require.EqualValues(t, 0, exit, "--allow-empty glob: exit %d, want 0; stderr: %s", exit, stderr)
	assert.Contains(t, stdout, "SPELLING OK", "--allow-empty glob is not an OK:\n%s", stdout)

	exit, stdout, stderr = runSpelling(t, "--dir", dir, "--json")
	require.EqualValues(t, 1, exit, "--json empty tree: exit %d, want 1; stderr: %s", exit, stderr)
	assert.Contains(t, stdout, `"status":"failed"`, "the JSON is not the same failed value:\n%s", stdout)
	assert.Contains(t, stdout, "--allow-empty", "the JSON is missing the remedy:\n%s", stdout)
}

// TestLooksVerbsDeclareAllowEmptyInHelp pins that the two check verbs whose
// count can reach zero declare the flag that accepts it, so a reader who meets
// the FAILED line can find it in the verb's own help (STANDARD §3).
func TestLooksVerbsDeclareAllowEmptyInHelp(t *testing.T) {
	t.Parallel()

	for _, verb := range []string{"links", "spelling"} {
		exit, stdout, stderr := runCheck(t, verb, "-h")
		require.EqualValues(t, 0, exit, "%s -h: exit %d, want 0; stderr: %s", verb, exit, stderr)
		assert.Contains(t, stdout, "--allow-empty", "%s -h does not declare --allow-empty:\n%s", verb, stdout)
	}
}
