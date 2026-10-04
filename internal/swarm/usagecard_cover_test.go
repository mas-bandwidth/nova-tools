package swarm

// SLICE 10 COVER: the usage rows per card (usagecard.go, lesson 10).
//
// The named tests are TestUsagecardCover*, so `go test -run TestUsagecardCover` selects
// exactly this file. Each covers one listed function's unit-tier paths and the refusals it
// answers without a subprocess. THE TWO THAT NEED ONE: queryCardMessages runs the one
// statement through `sqlite3` (SPEC-SWARM rule 13, usagecard.go), so every path below its
// LookPath needs a subprocess or a live store, and the unit tier starts neither; the same
// holds for ReadCardUsageAfter's main path past that LookPath. Its no-store and
// is-a-directory refusals answer before any program runs, and they are covered here.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUsagecardCoverCardMessagesSQL(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		startedMs int64
		endedMs   int64
		wantIn    []string
	}{
		{
			name:      "the window bounds are the caller's milliseconds, printed as given",
			startedMs: 1699999995000,
			endedMs:   1700000205000,
			wantIn: []string{
				"time_created >= 1699999995000",
				"time_created <= 1700000205000",
			},
		},
		{
			name:      "a first launch with no floor still prints its bounds",
			startedMs: 0,
			endedMs:   0,
			wantIn:    []string{"time_created >= 0", "time_created <= 0"},
		},
		{
			name:      "a negative bound prints with its minus, never rewritten",
			startedMs: -1000,
			endedMs:   5000,
			wantIn:    []string{"time_created >= -1000", "time_created <= 5000"},
		},
		{
			name:      "the read is grouped assistant rows with the fold's aggregates",
			startedMs: 1,
			endedMs:   2,
			wantIn: []string{
				`json_extract(data, '$.role') = 'assistant'`,
				"GROUP BY json_extract(data, '$.providerID'), json_extract(data, '$.modelID')",
				"COUNT(*)",
				"MAX(COALESCE(json_extract(data, '$.tokens.input'), 0)",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			sql := cardMessagesSQL(tc.startedMs, tc.endedMs)
			for _, want := range tc.wantIn {
				assert.Contains(t, sql, want, "the statement")
			}
		})
	}
	t.Run("every token type and the cost are summed, the width the fold accepts", func(t *testing.T) {
		t.Parallel()
		sql := cardMessagesSQL(0, 1)
		// foldCardMessages skips a row whose width is not 2+len(TokenColumns)+3, so the
		// statement must select exactly that: two names, the five token sums and the cost
		// sum (six SUMs), the request count and the largest prompt.
		assert.Equal(t, len(TokenColumns)+1, strings.Count(sql, "SUM(json_extract(data"),
			"the five token sums plus the cost sum")
		for _, path := range []string{"'$.tokens.input'", "'$.tokens.output'", "'$.tokens.cache.write'",
			"'$.tokens.cache.read'", "'$.tokens.reasoning'", "'$.cost'"} {
			assert.Contains(t, sql, "SUM(json_extract(data, "+path+")", "the summed column")
		}
	})
}

func TestUsagecardCoverCardStoreLocations(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		home string
	}{
		{name: "the run's own data directory", home: "/srv/jobs/a1/data"},
		{name: "an empty home answers relative paths", home: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := cardStoreLocations(tc.home)
			want := []string{
				filepath.Join(tc.home, filepath.FromSlash(OpenCodeDB)),
				filepath.Join(tc.home, ".local", "share", "opencode", "opencode.db"),
			}
			assert.Equal(t, want, got, "both stores under the job's own data home, the primary first")
			assert.Equal(t, OpenCodeStoreLocations(tc.home), got, "the reader is the package's own location seam, not a copy")
		})
	}
	t.Run("it answers paths and touches no disk", func(t *testing.T) {
		t.Parallel()
		home := filepath.Join(t.TempDir(), "absent")
		for _, p := range cardStoreLocations(home) {
			_, err := os.Stat(p)
			assert.True(t, os.IsNotExist(err), "listing the stores creates nothing: %s", p)
		}
	})
}

func TestUsagecardCoverDashCardTokens(t *testing.T) {
	t.Parallel()
	values := dashCardTokens()
	t.Run("a store that reported nothing is every number a dash, never a zero", func(t *testing.T) {
		t.Parallel()
		assert.Len(t, values, len(TokenColumns)+3, "provider, model, usd and the five token types")
		for _, k := range append([]string{"provider", "model", "usd"}, TokenColumns...) {
			assert.Equal(t, Dash, values[k], "%s", k)
			assert.NotEqual(t, "0", values[k], "%s: a dash is an absence, a zero is a measurement", k)
		}
	})
	t.Run("the row reads as nothing observed, never as a spend measured at zero", func(t *testing.T) {
		t.Parallel()
		sum, seen, partial := ProviderUsage{Values: values}.Sum()
		assert.Zero(t, sum)
		assert.Zero(t, seen, "a dashed column is not read")
		assert.False(t, partial, "nothing observed is not a PARTIAL observation")
		assert.Equal(t, Dash+"/100", BudgetWord(false, 100, sum, false, partial),
			"the word carries the dash, never 0/100")
	})
}

func TestUsagecardCoverAppendCardUsage(t *testing.T) {
	t.Parallel()
	full := UsageRow{
		"job": "cover-x", "attempt": "1", "started": "2026-10-04T00:00:00Z",
		"ended": "2026-10-04T00:10:00Z", "end": EndDone, "rc": "0",
		"provider": "anthropic", "model": "claude-x", "tokens_in": "10", "tokens_out": "2",
		"cache_write": Dash, "cache_read": "40", "reasoning": "1", "usd": "0.0010",
	}
	t.Run("a new file gets the header and the row; a retried attempt appends the row only", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "usage.tsv")
		retry := UsageRow{"job": "cover-x", "attempt": "2", "end": EndBudget}
		require.NoError(t, AppendCardUsage(path, full))
		require.NoError(t, AppendCardUsage(path, retry))

		header := strings.Join(CardUsageColumns, "\t")
		lines := strings.Split(strings.TrimSuffix(mustReadCoverFile(t, path), "\n"), "\n")
		require.Len(t, lines, 3, "one header and one row per attempt")
		assert.Equal(t, header, lines[0], "the header is the fourteen columns in order")
		assert.Equal(t, 1, strings.Count(strings.Join(lines, "\n"), header), "the header is written once, never again")
		fields := strings.Split(lines[1], "\t")
		require.Len(t, fields, len(CardUsageColumns))
		for i, c := range CardUsageColumns {
			assert.Equal(t, full[c], fields[i], "column %s of the first row", c)
		}
		second := strings.Split(lines[2], "\t")
		require.Len(t, second, len(CardUsageColumns))
		for i, c := range CardUsageColumns {
			want := retry[c]
			if want == "" {
				want = Dash
			}
			assert.Equal(t, want, second[i], "column %s of the retried row: an absence stays a dash", c)
		}
	})
	t.Run("a value is scrubbed to one row and an emptiness is written as a dash", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "usage.tsv")
		require.NoError(t, AppendCardUsage(path, UsageRow{
			"job":      "a\tb\nc\rd",
			"rc":       " 0 ",
			"end":      EndDone,
			"usd":      "",
			"provider": " ",
		}))
		lines := strings.Split(strings.TrimSuffix(mustReadCoverFile(t, path), "\n"), "\n")
		require.Len(t, lines, 2)
		fields := strings.Split(lines[1], "\t")
		require.Len(t, fields, len(CardUsageColumns), "a tab inside a value never adds a column")
		assert.Equal(t, "a b c d", fields[0], "tab, newline and carriage return become spaces")
		assert.Equal(t, "0", fields[indexOfCoverColumn(t, "rc")], "the value is trimmed")
		for _, c := range []string{"usd", "provider"} {
			assert.Equal(t, Dash, fields[indexOfCoverColumn(t, c)], "%s: an empty value is the literal dash", c)
		}
	})
	cases := []struct {
		name  string
		isDir bool
	}{
		{name: "a parent directory that does not exist is refused", isDir: false},
		{name: "the path itself a directory is refused", isDir: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			path := filepath.Join(dir, "absent", "usage.tsv")
			if tc.isDir {
				path = filepath.Join(dir, "usage.tsv")
				require.NoError(t, os.Mkdir(path, 0o755))
			}
			assert.Error(t, AppendCardUsage(path, full), "an unusable path is a refusal, never a silent nothing")
			if !tc.isDir {
				_, err := os.Stat(filepath.Join(dir, "absent"))
				assert.True(t, os.IsNotExist(err), "a refused write creates no directory and no file")
			}
		})
	}
}

func TestUsagecardCoverReadCardUsageAfterNoStore(t *testing.T) {
	t.Parallel()
	started := time.Unix(1700000000, 0)
	ended := time.Unix(1700000100, 0)
	cases := []struct {
		name      string
		notBefore time.Time
		mkdir     bool
	}{
		{name: "a first launch under no floor", notBefore: time.Time{}},
		{name: "a retried launch clamps the widened window at the earlier end", notBefore: started.Add(-2 * time.Second)},
		{name: "a store path that is a directory is skipped, not read", notBefore: time.Time{}, mkdir: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			home := t.TempDir()
			locations := cardStoreLocations(home)
			if tc.mkdir {
				require.NoError(t, os.MkdirAll(locations[0], 0o755))
			}
			usage, note, store, state := ReadCardUsageAfter(home, started, ended, tc.notBefore)
			assert.Equal(t, "no-store", state, "no store at either location is an absence, and the reader stops")
			assert.Empty(t, store, "no store path was opened")
			assert.Equal(t, "no harness store: looked at "+locations[0]+" and "+locations[1], note,
				"the note names both locations the reader tried, in order")
			assert.False(t, usage.Observed, "nothing was observed")
			assert.Zero(t, usage.Turns)
			for _, c := range append([]string{"provider", "model", "usd"}, TokenColumns...) {
				assert.Equal(t, Dash, usage.Values[c], "%s of a store nobody found is a dash, never a zero", c)
			}
		})
	}
}

// mustReadCoverFile reads one of this file's fixtures whole.
func mustReadCoverFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	require.NoError(t, err, path)
	return string(b)
}

// indexOfCoverColumn is the position of one usage.tsv column in the written row.
func indexOfCoverColumn(t *testing.T, name string) int {
	t.Helper()
	for i, c := range CardUsageColumns {
		if c == name {
			return i
		}
	}
	t.Fatalf("%s is not a usage.tsv column", name)
	return -1
}
