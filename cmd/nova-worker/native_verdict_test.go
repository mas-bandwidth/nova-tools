//go:build slow || functional

package main

// `NATIVE OK` was printed for a run that produced nothing (nova-tools #1844).
//
// THE RECEIPT, from the tools12 shift, 2026-09-19T18:00:28Z. Card `tools12c18` on bench
// `bench-1`: rc=1 on both attempts, zero tokens, zero dollars, no RESULT.md, no repo, and no
// NATIVE line in the slot's own native.log. The provider was the cause -- attempt 1
// `Error: {"name":"UnknownError","data":{"message":"Unexpected server error. Check server
// logs for details.","ref":"err_fb35c63e"}}`, attempt 2 `Cannot connect to API`. And the
// launcher's one log line for it read:
//
//	bench-1 tools12c18 attempt=1 wall=159s NATIVE OK label=tools12c18 job=/home/worker/...
//
// A fill loop or a manager counting its in-flight cards by that line counts a card that
// never ran as delivered, and a card that made zero paid calls as done. The shift caught it
// only because the returned bundle was zero bytes.
//
// So the word is earned. Every field on the line is unchanged; only the verdict word is,
// plus a `why=` naming which of the three conditions failed.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// nativeVerdict runs one card through `native` with the fake harness and returns stdout.
func nativeVerdict(t *testing.T, label, card string) string {
	t.Helper()
	stdout, _, _ := nativeVerdictRun(t, label, card)
	return stdout
}

// nativeVerdictRun is nativeVerdict plus stderr and the process exit, so a test can
// pin that a finished card never exits 255 (#2058). Local ssh(1) 255 is any error,
// not proof the remote command never started.
func nativeVerdictRun(t *testing.T, label, card string) (stdout, stderr string, code int) {
	t.Helper()
	windowsIsNotABench(t)
	bin := nativeHarness(t)
	root, slot := aSlot(t)
	cardPath := filepath.Join(root, label+".md")
	require.NoError(t, os.WriteFile(cardPath, []byte(card), 0o644))
	var out, errb bytes.Buffer
	code = run([]string{"native", "--slots-store", nativeStore(t), "--owner", "fake-1",
		"--harness", bin, "--model", "fake/fake-model", "--label", label,
		"--card", cardPath, "--slot", slot, "--root", root,
		"--tokens", "unmetered",
		"--deadline", "30s", "--no-wall"},
		strings.NewReader(""), &out, &errb, time.Now())
	return out.String(), errb.String(), code
}
