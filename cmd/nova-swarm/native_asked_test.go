//go:build slow || functional

package main

// A CARD WHOSE LAST TURN IS A QUESTION ENDS AS ASKED (nova-tools #2548).
//
// THE RECEIPT, canary run 3, 2026-09-22 02:05Z, route openrouter/openai/gpt-5-nano: the
// worker wrote its test file into a phantom nested path, committed nothing, published no
// `RESULT.md`, and ended its last turn with `Would you like me to proceed with moving
// RESULT.md into the nested repo and finalize the commit?`.
//
// THE RUNTIME DOGFOOD OF THE SAME DAY REFUTED THE MECHANISM the issue's title named: the
// run does NOT hold its slot. `opencode run` finishes the turn and exits 0, measured at
// 5.22 s on hulk and 5.49 s on the Studio against a card whose STEP 2 tells the model to
// ask and wait. What it leaves behind is the fault: `NATIVE INCOMPLETE ... why=no-result`,
// the token for a model that chose to publish nothing, with the question recorded nowhere.
// These tests are about what the run now writes down, and about the verdict NOT moving
// because it wrote it.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// nativeAsked runs one card through `native` with the fake harness and returns the job
// directory beside stdout and stderr, because the report this card is about is a FILE.
func nativeAsked(t *testing.T, label, card string) (job, stdout, stderr string) {
	t.Helper()
	windowsIsNotABench(t)
	bin := nativeHarness(t)
	root, slot := aSlot(t)
	cardPath := filepath.Join(root, label+".md")
	require.NoError(t, os.WriteFile(cardPath, []byte(card), 0o644))
	var out, errBuf strings.Builder
	run([]string{"native", "--tokens", "unmetered", "--slots-store", nativeStore(t), "--owner", "fake-1",
		"--harness", bin, "--model", "fake/fake-model", "--label", label,
		"--card", cardPath, "--slot", slot, "--root", root,
		"--deadline", "30s", "--no-wall"},
		strings.NewReader(""), &out, &errBuf, time.Now())
	return filepath.Join(slot, "jobs", label), out.String(), errBuf.String()
}
