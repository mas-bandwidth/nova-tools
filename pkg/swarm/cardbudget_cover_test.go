package swarm

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The unit cover for cardbudget.go: the turn count a budget stop reads
// (cardbudget.go:45), the harness-log scan behind it (cardbudget.go:51) and
// the report line the stop writes (cardbudget.go:87). Every test is named
// TestCardbudgetCover* so `-run TestCardbudgetCover` selects them.

// writeCardbudgetLog writes a harness log body into the test's temp dir and
// returns its path: the named log CountCardTurnsIn counts is a plain file.
func writeCardbudgetLog(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "harness-output.log")
	require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
	return path
}

// CountCardTurnsIn takes the log's assistant lines or the usage row count,
// whichever is larger, and falls back to the usage row where the log is absent.
func TestCardbudgetCoverCountCardTurnsIn(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		body   string
		absent bool
		turns  int
		want   int
	}{
		{
			name:  "the log's assistant lines win where the usage row is behind",
			body:  "user: hello\nassistant: one\nassistant: two\n",
			turns: 1,
			want:  2,
		},
		{
			name:  "the usage row count wins where the log has fewer",
			body:  "assistant: one\n",
			turns: 4,
			want:  4,
		},
		{
			name:  "the count is case-insensitive over the harness's spelling",
			body:  "ASSISTANT one\nassistant two\nuser: hello\n",
			turns: 0,
			want:  2,
		},
		{
			name:   "an absent log falls back to the usage row count",
			absent: true,
			turns:  3,
			want:   3,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			logPath := ""
			if !tc.absent {
				logPath = writeCardbudgetLog(t, tc.body)
			}
			assert.Equal(t, tc.want, CountCardTurnsIn(logPath, ProviderUsage{Turns: tc.turns}),
				"the count is the larger of the log's assistant lines and the usage row count")
		})
	}
}

// countAssistantLines counts the log's assistant lines case-insensitively and
// answers zero for a path that is not a readable regular file.
func TestCardbudgetCoverCountAssistantLines(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		body   string
		absent bool
		dir    bool
		want   int
	}{
		{
			name: "assistant lines are counted and other lines are not",
			body: "assistant: one\nuser: hello\nAssistant: two\nsystem: boot\n",
			want: 2,
		},
		{
			name: "a log without assistant lines counts zero",
			body: "user: hello\nsystem: boot\n",
			want: 0,
		},
		{
			name:   "an absent log counts zero",
			absent: true,
			want:   0,
		},
		{
			name: "a directory is refused and counts zero",
			dir:  true,
			want: 0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			path := ""
			switch {
			case tc.absent:
				path = filepath.Join(t.TempDir(), "missing.log")
			case tc.dir:
				path = t.TempDir()
			default:
				path = writeCardbudgetLog(t, tc.body)
			}
			assert.Equal(t, tc.want, countAssistantLines(path),
				"the scan counts the log's assistant lines, or zero where it cannot read one")
		})
	}
}

// PromptDefectLine renders the one report line a budget stop writes, the
// fields in the one-line output grammar's order.
func TestCardbudgetCoverPromptDefectLine(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		id   string
		cr   int
		max  int
		turn int
		want string
	}{
		{
			name: "a cache stop names the observed read, the budget and the turns",
			id:   "card-1", cr: 1500, max: 1200, turn: 9,
			want: "PROMPT-DEFECT task=card-1 reason=budget cache_read=1500 max=1200 turns=9",
		},
		{
			name: "a turn stop carries zeros for the cache fields it did not fire",
			id:   "card-2", cr: 0, max: 4, turn: 7,
			want: "PROMPT-DEFECT task=card-2 reason=budget cache_read=0 max=4 turns=7",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, PromptDefectLine(tc.id, tc.cr, tc.max, tc.turn),
				"the report line is one line of the one-line output grammar")
		})
	}
}
