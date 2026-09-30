package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A retry is a receipt for the existing entry, not a new event. Exercise both
// store shapes through the CLI, with a clock different from the original append.
func TestAppendRetryReportsTheStoredStamp(t *testing.T) {
	t.Parallel()
	for _, bench := range []bool{false, true} {
		name := "nested"
		if bench {
			name = "bench"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			store := t.TempDir()
			path := filepath.Join(store, "entries", "s", "e.json")
			wantStamp := "2026-09-28T01:02:03.456Z"
			if bench {
				path = filepath.Join(store, "s.md")
				require.NoError(t, os.WriteFile(path, []byte("# Session s\n"), 0600))
				// Bench headings store whole seconds; the receipt must match that record.
				wantStamp = "2026-09-28T01:02:03Z"
			} else {
				runOK(t, "", "open", "--store", store, "--session", "s", "--publish", "manual", "--source", "original-session-source")
			}
			args := []string{"append", "--store", store, "--session", "s", "--entry", "e", "--text", "the original words", "--publish", "manual", "--now"}
			first, _ := runOK(t, "", append(append([]string{}, args...), "2026-09-28T01:02:03.456Z")...)
			before, err := os.ReadFile(path)
			require.NoError(t, err)
			retryArgs := append(append([]string{}, args...), "2026-09-29T04:05:06Z")
			if !bench {
				retryArgs = append(retryArgs, "--source", "different-retry-source")
			}
			retry, _ := runOK(t, "", retryArgs...)
			for _, output := range []string{first, retry} {
				if !bench && !strings.Contains(output, "source=original-session-source ") {
					t.Errorf("receipt lost the stored source: %s", output)
				}
				assert.Contains(t, output, "stamp="+wantStamp+"\n", "receipt does not report the stored stamp %s: %s", wantStamp, output)
			}
			assert.Contains(t, retry, "duplicate=true", "retry: %s", retry)
			after, err := os.ReadFile(path)
			require.NoError(t, err)
			require.Equal(t, string(before), string(after), "retry changed the stored entry")
			// Corrupt persisted time cannot be replaced by this invocation's clock.
			broken := strings.Replace(string(after), wantStamp, "2026-99-28T01:02:03Z", 1)
			require.NotEqual(t, string(after), broken, "fixture did not replace the stored stamp")
			require.NoError(t, os.WriteFile(path, []byte(broken), 0600))
			code, out, errOut := runCode("", append(append([]string{}, args...), "2026-09-30T04:05:06Z")...)
			if code != 2 || out != "" || !strings.Contains(errOut, "invalid stamp") {
				t.Fatalf("corrupt stored stamp: code=%d out=%q err=%q", code, out, errOut)
			}
			kept, err := os.ReadFile(path)
			require.NoError(t, err, "refusal changed stored entry: %v", err)
			require.Equal(t, broken, string(kept), "refusal changed stored entry: %v", err)
		})
	}
}
