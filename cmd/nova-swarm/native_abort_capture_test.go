package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// nativeAbortRun runs one native card whose harness is the given shell script and
// returns the result, the finish report (the printed output), and the slot's
// native.log. The harness is a plain shell script, so it is launched as the
// child itself (the providers table's argv is ignored by the script) and opens
// no socket.
func nativeAbortRun(t *testing.T, label, script string) (nativeRunResult, string, string) {
	t.Helper()
	root, slot := aSlot(t)
	bin := filepath.Join(t.TempDir(), "harness.sh")
	require.NoError(t, os.WriteFile(bin, []byte(script), 0o755))
	cfg := nativeRunConfig{
		binary: bin, model: "fake/fake-model", label: label,
		card: []byte("a card\n"), slotDir: slot, root: root,
		deadline: 30 * time.Second, noWall: true,
	}
	var errOut, stdout, stderr bytes.Buffer
	res, code := nativeRun(cfg, &errOut)
	require.Equal(t, 0, code, "the native run started:\n%s", errOut.String())
	printNativeResult(&stdout, &stderr, cfg, res, code, false)
	raw, err := os.ReadFile(filepath.Join(slot, "native.log"))
	require.NoError(t, err, "the slot's native.log was not written")
	return res, stdout.String() + stderr.String() + errOut.String(), string(raw)
}

// A child that aborts itself with SIGABRT leaves its end in the slot's native.log
// and in the finish report: native's watch used to record only the exit code, so
// a child that died by signal left the slot log without a word on how it ended.
func TestNativeAbortedChildIsRecordedInTheSlotLog(t *testing.T) {
	t.Parallel()
	_, report, log := nativeAbortRun(t, "abort", "#!/bin/bash\necho 'thinking about a step'\nkill -ABRT $$\n")
	require.Contains(t, log, "NATIVE CHILD-END", "the slot's native.log names the aborted child's end:\n%s", log)
	require.Contains(t, report, "NATIVE CHILD-END", "the finish report carries the child-end line:\n%s", report)
	require.Contains(t, log, "signal=abort", "the line names the signal:\n%s", log)
	require.Contains(t, log, "stderr-tail=", "the line carries the stderr tail:\n%s", log)
}

// A child that exits 0 writes no child-end line.
func TestNativeCleanExitWritesNoChildEndLine(t *testing.T) {
	t.Parallel()
	_, _, log := nativeAbortRun(t, "clean", "#!/bin/bash\necho 'done'\nexit 0\n")
	require.NotContains(t, log, "NATIVE CHILD-END", "a clean exit writes no child-end line:\n%s", log)
}

// The stderr tail is bounded to 4 KiB, so a chatty child cannot blow the slot log.
func TestNativeChildEndTailIsBounded(t *testing.T) {
	t.Parallel()
	var b strings.Builder
	fmt.Fprintln(&b, "#!/bin/bash")
	for i := 0; i < 200; i++ {
		fmt.Fprintf(&b, "echo 'bulk %d payload payload payload' >&2\n", i)
	}
	fmt.Fprintln(&b, "kill -ABRT $$")
	_, _, log := nativeAbortRun(t, "bounded", b.String())
	idx := strings.Index(log, "NATIVE CHILD-END ")
	require.NotEqual(t, -1, idx, "no child-end line:\n%s", log)
	line := log[idx:]
	if end := strings.IndexByte(line, '\n'); end != -1 {
		line = line[:end]
	}
	marker := "stderr-tail="
	start := strings.Index(line, marker)
	require.NotEqual(t, -1, start, "the line carries no stderr-tail field:\n%s", line)
	tail := line[start+len(marker):]
	if i := strings.Index(tail, " abort="); i != -1 {
		tail = tail[:i]
	}
	require.NotEmpty(t, tail, "the stderr-tail field is empty:\n%s", line)
	require.LessOrEqual(t, len(tail), 4096, "the tail is bounded to 4 KiB")
}
