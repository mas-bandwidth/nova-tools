package main

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/testbin"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Receipts for a verb the list does not declare, at the command line: they are
// counted on the ledger and gate lines, and a not-ok one is a failure, so no
// receipt vanishes from the arithmetic. A count that silently leaves evidence
// out is worse than no count.

// The number is on the line both reads print, so nobody has to read a note to
// learn that evidence was discarded.
func TestDogfoodLineCountsTheUnmatchedReceipts(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cli := writeCLI(t, dir)
	receipts := filepath.Join(dir, "receipts")
	writeReceipt(t, receipts, "a.json", map[string]any{
		"tool": "nova-example", "verb": "links", "by": "Stella",
		"at": "2026-09-18T09:00:00Z", "ok": true, "notes": "real work",
	})
	writeReceipt(t, receipts, "b.json", map[string]any{
		"tool": "nova-example", "verb": "harvest", "by": "Stella",
		"at": "2026-09-18T09:05:00Z", "ok": true, "notes": "real work",
	})
	code, stdout, stderr := dogfoodRun(t, "dogfood", "ledger", "--cli", cli, "--receipts", receipts)
	require.EqualValues(t, 0, code, "exit %d\n%s", code, stderr)
	assert.Contains(t, stdout, "unmatched=1", "the ledger line does not count what it threw away:\n%s", stdout)
	code, stdout, stderr = dogfoodRun(t, "dogfood", "gate", "--cli", cli, "--receipts", receipts)
	require.EqualValues(t, 0, code, "gate exit %d, want 0 (an unmatched OK receipt is not a failure)\n%s", code, stderr)
	assert.Contains(t, stdout, "unmatched=1", "the gate line does not count what it threw away:\n%s", stdout)
}

// A not-ok receipt that matched nothing is a FAILURE, and the failure names the
// receipt's file and the verb it claimed.
func TestDogfoodGateFailsOnAnUnmatchedNotOkReceipt(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cli := writeCLI(t, dir)
	receipts := filepath.Join(dir, "receipts")
	writeReceipt(t, receipts, "a.json", map[string]any{
		"tool": "nova-example", "verb": "links", "by": "Stella",
		"at": "2026-09-18T09:00:00Z", "ok": true, "notes": "real work",
	})
	writeReceipt(t, receipts, "bad.json", map[string]any{
		"tool": "nova-merge", "verb": "batch", "by": "Stella",
		"at": "2026-09-18T09:05:00Z", "ok": false, "notes": "it refused a batch that was on dev",
	})
	code, stdout, stderr := dogfoodRun(t, "dogfood", "gate", "--cli", cli, "--receipts", receipts)
	require.EqualValues(t, 1, code, "exit %d, want 1: a not-ok receipt nobody can match is not an open-edges=0 gate\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	for _, want := range []string{"bad.json", "nova-merge", "batch"} {
		assert.Contains(t, stderr, want, "the gate does not name %q:\n%s", want, stderr)
	}
	assert.Contains(t, stderr, "unmatched=1", "the red count line does not carry the unmatched count:\n%s", stderr)
}

// `record --tools <dir>` takes the verb list from the binaries themselves, and
// that is the list the spelling is checked against.
func TestDogfoodRecordChecksTheSpellingAgainstTheBinaries(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == "windows" {
		t.Skip("the fixture is a shell script")
	}
	tools := t.TempDir()
	script := "#!/bin/sh\ncat <<'EOF'\nnova-example: a fixture\n\nusage:\n  nova-example links --dir <dir>\n  nova-example ask   delivers ONE unit to the FRIEND who owns it\nEOF\n"
	require.NoError(t, testbin.WriteExecutable(filepath.Join(tools, "nova-example"), []byte(script), 0o755))
	receipts := filepath.Join(t.TempDir(), "receipts")
	// `ask` is in the binary and in no reference: --tools alone accepts it.
	code, stdout, stderr := dogfoodRun(t, "dogfood", "record", "--tools", tools,
		"--tool", "nova-example", "--verb", "ask", "--by", "Stella", "--ok",
		"--notes", "one real ask sent", "--receipts", receipts)
	require.EqualValues(t, 0, code, "exit %d, want 0\n%s", code, stderr)
	require.True(t, strings.HasPrefix(stdout, "DOGFOOD RECORD OK "), "record said:\n%s", stdout)
	// And a spelling neither source declares is still refused, by name.
	code, _, stderr = dogfoodRun(t, "dogfood", "record", "--tools", tools,
		"--tool", "nova-example", "--verb", "aks", "--by", "Stella", "--ok",
		"--notes", "real work", "--receipts", receipts)
	require.EqualValues(t, 2, code, "exit %d, want 2", code)
	assert.Contains(t, stderr, "nova-example ask", "the refusal does not name the nearest declared verb:\n%s", stderr)
}
