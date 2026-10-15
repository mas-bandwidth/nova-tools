package friend

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Every line of testdata/limits.tsv is one text a harness prints, the kind it
// parses to and the reset it names: a text it does not recognise stays an
// ordinary failure (kind none).
func TestEachHarnessUsageLimitAndCreditsTextParsesWithItsReset(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile("testdata/limits.tsv")
	require.NoError(t, err)
	now := time.Date(2026, 10, 5, 10, 0, 0, 0, time.UTC)
	seen := map[string]map[string]bool{}
	for _, line := range strings.Split(string(raw), "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.SplitN(line, "\t", 4)
		require.Len(t, f, 4, line)
		harness, kind, until, text := f[0], f[1], f[2], f[3]
		hit, ok := ParseLimit(harness, "working on it\n"+text+"\n", now, time.Hour)
		if kind == "none" {
			assert.False(t, ok, "%s: %s is an ordinary failure", harness, text)
			continue
		}
		if !assert.True(t, ok, "%s: %s", harness, text) {
			continue
		}
		assert.Equal(t, kind, hit.Kind, "%s: %s", harness, text)
		want := now.Add(time.Hour)
		if until != "default" {
			want, err = time.Parse(time.RFC3339, until)
			require.NoError(t, err, line)
		}
		assert.True(t, want.Equal(hit.Until), "%s: %s: until %s, want %s", harness, text, hit.Until.Format(time.RFC3339), want.Format(time.RFC3339))
		assert.Equal(t, until != "default", hit.Named, "%s: %s", harness, text)
		if seen[harness] == nil {
			seen[harness] = map[string]bool{}
		}
		seen[harness][kind] = true
	}
	for _, h := range []string{"claude", "codex", "opencode", "grok", "antigravity", "dsh", "gemini"} {
		assert.True(t, seen[h][KindLimit] && seen[h][KindCredits], "%s has a limit text and a credits text in the fixtures: %v", h, seen[h])
	}
	_, ok := ParseLimit("claude", "Credit balance is too low", now, 0)
	assert.True(t, ok, "no rest given: DefaultLimitWait")
	_, ok = ParseLimit("nobody", "Credit balance is too low", now, 0)
	assert.False(t, ok, "a harness with no words has no limit")
}

// A turn that succeeded and only talks of a limit sends no one down, and a
// failed one whose text is not a limit is an ordinary failure.
func TestAReplyThatTalksOfLimitsIsNoLimit(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 5, 10, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		out    string
		exit   int
		limits bool
	}{
		{"The credit balance is too low in the test I wrote.\n", 0, false},
		{"Credit balance is too low\n", 1, true},
		{"cannot open file: permission denied\n", 1, false},
	} {
		downs := 0
		l := &Limits{Now: func() time.Time { return now }, Harness: "claude", Down: func(time.Time, string) { downs++ }}
		_, _, _ = l.Watch(func(context.Context, string, string, []string, string) (string, int, error) {
			return tc.out, tc.exit, nil
		})(context.Background(), "", "x", nil, "")
		_, _, limited := l.Limited()
		assert.Equal(t, tc.limits, limited, tc.out)
		if tc.limits {
			assert.Equal(t, KindCredits, l.Kind())
			assert.Equal(t, 1, downs)
		}
	}
}
