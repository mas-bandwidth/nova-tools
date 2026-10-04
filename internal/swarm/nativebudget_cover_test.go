package swarm

import (
	"os/exec"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The unit cover for nativebudget.go: the budget source a native launch reads
// (nativebudget.go:29), the refusal that stops it before anything is made
// (nativebudget.go:47), the field names the refusal points at
// (nativebudget.go:82), the one program a source needs (nativebudget.go:99)
// and the interval the reader runs at (nativebudget.go:119). Every test is
// named TestNativebudgetCover* so `-run TestNativebudgetCover` selects them.

// NativeUsageSource names the worker description's usage, and opencode when
// there is no description at all.
func TestNativebudgetCoverUsageSource(t *testing.T) {
	t.Parallel()

	assert.Equal(t, UsageOpenCode, NativeUsageSource(nil),
		"no worker description reads opencode")
	assert.Equal(t, UsageNone, NativeUsageSource(&Worker{Usage: UsageNone}),
		"a description that says none reads none")
	assert.Equal(t, UsageOpenCode, NativeUsageSource(&Worker{Usage: UsageOpenCode}),
		"a description that says opencode reads opencode")
}

// NativeBudgetSourceRefusal refuses a budget nothing can observe, names why,
// and lets unmetered-with-no-budget run under both sources.
func TestNativebudgetCoverBudgetSourceRefusal(t *testing.T) {
	t.Parallel()

	opencodeNumeric := ""
	if !SQLiteOnPath() {
		opencodeNumeric = SQLiteBinary
	}
	cases := []struct {
		name        string
		source      string
		tokens      int
		unmetered   bool
		usd         bool
		worker      *Worker
		wantEmpty   bool
		wantContain []string
	}{
		{
			name:      "unmetered with no card budget runs under both",
			source:    UsageNone,
			unmetered: true,
			wantEmpty: true,
		},
		{
			name:        "a numeric --tokens under usage none is refused",
			source:      UsageNone,
			tokens:      100,
			wantContain: []string{"a numeric --tokens", "usage: none"},
		},
		{
			name:        "a dollar budget under usage none is refused",
			source:      UsageNone,
			usd:         true,
			wantContain: []string{"a dollar budget --usd", "usage: none"},
		},
		{
			name:        "a usage this tool does not read is refused",
			source:      "mystery",
			tokens:      100,
			wantContain: []string{"not one this tool reads"},
		},
		{
			name:        "a card budget names the field the caller wrote",
			source:      UsageNone,
			unmetered:   true,
			worker:      &Worker{MaxTurns: 5},
			wantContain: []string{"max_turns"},
		},
		{
			name:      "opencode with a numeric --tokens is read when sqlite3 resolves",
			source:    UsageOpenCode,
			tokens:    100,
			wantEmpty: opencodeNumeric == "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := NativeBudgetSourceRefusal(tc.source, tc.tokens, tc.unmetered, tc.usd, tc.worker)
			if tc.wantEmpty {
				assert.Empty(t, got, "the launch may proceed")
				return
			}
			if opencodeNumeric != "" && tc.source == UsageOpenCode {
				assert.Contains(t, got, opencodeNumeric, "the refusal names the missing program")
				return
			}
			require.NotEmpty(t, got, "the launch is refused")
			for _, want := range tc.wantContain {
				assert.Contains(t, got, want, "the refusal names the reason")
			}
		})
	}
}

// cardBudgetFields names exactly the fields a description set.
func TestNativebudgetCoverCardBudgetFields(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		worker *Worker
		want   string
	}{
		{"no description", nil, "card budget"},
		{"both fields", &Worker{MaxTurns: 1, MaxCacheRead: 1}, "max_turns and max_cache_read"},
		{"max_turns alone", &Worker{MaxTurns: 1}, "max_turns"},
		{"max_cache_read alone", &Worker{MaxCacheRead: 1}, "max_cache_read"},
		{"neither field", &Worker{}, "card budget"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, cardBudgetFields(tc.worker), "the field name points at the line the caller wrote")
		})
	}
}

// SQLiteOnPath answers the same way the one program resolves on PATH.
func TestNativebudgetCoverSQLiteOnPath(t *testing.T) {
	t.Parallel()

	_, err := exec.LookPath(SQLiteBinary)
	assert.Equal(t, err == nil, SQLiteOnPath(), "SQLiteOnPath reports whether %s resolves", SQLiteBinary)
}

// NativeUsageIntervalRefusal refuses below the floor and at or past the
// deadline, and accepts anything between them.
func TestNativebudgetCoverUsageIntervalRefusal(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		interval    time.Duration
		deadline    time.Duration
		wantEmpty   bool
		wantContain string
	}{
		{"below the floor is refused", UsageIntervalFloor - time.Millisecond, 0, false, "at least"},
		{"at the deadline is refused", 5 * time.Second, 5 * time.Second, false, "shorter than --deadline"},
		{"past the deadline is refused", 10 * time.Second, 5 * time.Second, false, "shorter than --deadline"},
		{"no deadline has no ceiling", DefaultUsageInterval, 0, true, ""},
		{"under the deadline is accepted", DefaultUsageInterval, time.Minute, true, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := NativeUsageIntervalRefusal(tc.interval, tc.deadline)
			if tc.wantEmpty {
				assert.Empty(t, got, "the interval is taken")
				return
			}
			require.NotEmpty(t, got, "the interval is refused")
			assert.Contains(t, got, tc.wantContain, "the refusal names the bound")
		})
	}
}
