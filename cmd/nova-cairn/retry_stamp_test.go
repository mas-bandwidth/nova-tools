package main

import (
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/testkit"
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
			c := newRig(t)
			path := c.path("entries", "s", "e.json")
			wantStamp := "2026-09-28T01:02:03.456Z"
			if bench {
				path = c.path("s.md")
				testkit.WriteFile(t, path, "# Session s\n")
				// Bench headings store whole seconds; the receipt must match that record.
				wantStamp = "2026-09-28T01:02:03Z"
			} else {
				c.ok("open", "--session", "s", "--publish", "manual", "--source", "original-session-source")
			}
			args := []string{"--session", "s", "--entry", "e", "--text", "the original words", "--publish", "manual", "--now"}
			first := c.ok("append", append(args, "2026-09-28T01:02:03.456Z")...)
			before := testkit.ReadFile(t, path)
			retryArgs := append(args, "2026-09-29T04:05:06Z")
			if !bench {
				retryArgs = append(retryArgs, "--source", "different-retry-source")
			}
			retry := c.ok("append", retryArgs...)
			for _, output := range []string{first, retry} {
				if !bench {
					assert.Contains(t, output, "source=original-session-source ", "receipt lost the stored source")
				}
				assert.Contains(t, output, "stamp="+wantStamp+"\n", "receipt does not report the stored stamp")
			}
			assert.Contains(t, retry, "duplicate=true")
			after := testkit.ReadFile(t, path)
			require.Equal(t, before, after, "retry changed the stored entry")
			// Corrupt persisted time cannot be replaced by this invocation's clock.
			broken := strings.Replace(after, wantStamp, "2026-99-28T01:02:03Z", 1)
			require.NotEqual(t, after, broken, "fixture did not replace the stored stamp")
			testkit.WriteFile(t, path, broken)
			r := c.run("append", append(args, "2026-09-30T04:05:06Z")...)
			require.Equal(t, 2, r.Code, "corrupt stored stamp: %+v", r)
			require.Empty(t, r.Stdout)
			require.Contains(t, r.Stderr, "invalid stamp")
			require.Equal(t, broken, testkit.ReadFile(t, path), "refusal changed stored entry")
		})
	}
}
