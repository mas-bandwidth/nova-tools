package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// childEndAttemptScript is a harness whose first launch is a retryable fast
// failure with an exit code at or above 128 and the marker ALPHA on stderr, and
// whose second launch writes the marker BRAVO on stderr and exits with secondRC.
// The first launch's words are the harness's own catalog refusal and its
// UnknownError envelope, so the run retries the start (harnessStartFailed) in
// the same job. It changes no module state and opens no socket.
func childEndAttemptScript(firstRC, secondRC int) string {
	return fmt.Sprintf(`#!/bin/bash
if [ ! -f .child-end-tried ]; then
  : > .child-end-tried
  echo 'marker-ALPHA' >&2
  echo 'timestamp=2026-10-01T20:02:53.818Z level=ERROR run=5c22c68d message=failed ref=err_1ad647ee error="ProviderModelNotFoundError: Model not found: fake/fake-model. Did you mean: fake/other?"' >&2
  echo 'Error: {"name":"UnknownError","data":{"message":"Unexpected server error. Check server logs for details.","ref":"err_fake_attempt"}}' >&2
  exit %d
fi
echo 'marker-BRAVO' >&2
exit %d
`, firstRC, secondRC)
}

// childEndLine is the one NATIVE CHILD-END line the slot log carries.
func childEndLine(t *testing.T, log string) string {
	t.Helper()
	i := strings.Index(log, "NATIVE CHILD-END")
	require.NotEqual(t, -1, i, "the slot log carries no child-end line:\n%s", log)
	line := log[i:]
	if end := strings.IndexByte(line, '\n'); end != -1 {
		line = line[:end]
	}
	return line
}

// A retried card's child-end line is the LAST attempt's, or none: a fast failure
// at or above 128 on the first attempt records the line, and a later attempt's
// clean exit must not inherit it, nor must the later attempt's line carry the
// first attempt's stderr.
func TestAChildEndIsTheLastAttemptsOnly(t *testing.T) {
	t.Parallel()

	t.Run("a clean last attempt writes no child-end line", func(t *testing.T) {
		t.Parallel()
		res, report, log := nativeAbortRun(t, "childend-clean-last", childEndAttemptScript(128, 0))
		require.Equal(t, 0, res.rc, "the last attempt exited 0:\n%s", report)
		require.NotContains(t, log, "NATIVE CHILD-END", "a clean last attempt writes no child-end line:\n%s", log)
		require.NotContains(t, report, "NATIVE CHILD-END", "a clean last attempt's report carries no child-end line:\n%s", report)
	})

	t.Run("a failed last attempt names only its own exit and stderr", func(t *testing.T) {
		t.Parallel()
		res, report, log := nativeAbortRun(t, "childend-134-last", childEndAttemptScript(128, 134))
		require.Equal(t, 134, res.rc, "the last attempt exited 134:\n%s", report)
		line := childEndLine(t, log)
		require.Contains(t, line, "rc=134", "the line names the last attempt's exit code:\n%s", line)
		require.Contains(t, line, "marker-BRAVO", "the line carries the last attempt's stderr:\n%s", line)
		require.NotContains(t, line, "marker-ALPHA", "the line does not carry the first attempt's stderr:\n%s", line)
		require.Contains(t, report, "NATIVE CHILD-END rc=134", "the report names the last attempt's exit code:\n%s", report)
		require.NotContains(t, report, "marker-ALPHA", "the report does not carry the first attempt's stderr:\n%s", report)
	})
}
