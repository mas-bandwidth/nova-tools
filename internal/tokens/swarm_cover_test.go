package tokens

// The cover card for swarm.go: the three functions the unit tier's table showed at 0.0% --
// ReadSwarm, wrongColumn and unparsed -- each with its main path and a refusal, folded from
// a pool a test writes into its own temporary directory. Every test name begins
// TestSwarmCover so `-run TestSwarmCover` selects the set.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// swarmCoverRules is a one-rule rules file, the way every test here loads one: `schema`
// names any path that mentions it.
func swarmCoverRules(t *testing.T) *Rules {
	t.Helper()
	path := filepath.Join(t.TempDir(), "repos.tsv")
	require.NoError(t, os.WriteFile(path, []byte("schema\t(^|/)schema($|/)\n"), 0o644))
	rules, err := LoadRules(path)
	require.NoError(t, err)
	return rules
}

// swarmCoverPool makes a pool directory with a usage/ directory under it and returns the
// pool path.
func swarmCoverPool(t *testing.T) string {
	t.Helper()
	pool := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(pool, "usage"), 0o700))
	return pool
}

// swarmCoverUsage writes one usage file under the pool's usage/ directory.
func swarmCoverUsage(t *testing.T, pool, name, body string) {
	t.Helper()
	path := filepath.Join(pool, "usage", name)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
}

// swarmCoverHeader is the sixteen-column header the swarm writes.
func swarmCoverHeader() string { return strings.Join(SwarmColumns, "\t") }

// swarmCoverRow is one usage row, tab-joined from the cells given.
func swarmCoverRow(cells ...string) string { return strings.Join(cells, "\t") }

// swarmCoverMessage finds one folded message by model, so an assertion names the row it pins.
func swarmCoverMessage(stream []Message, model string) Message {
	for _, m := range stream {
		if m.Model == model {
			return m
		}
	}
	return Message{}
}

// TestSwarmCoverReadSwarmFoldsUsageFiles: every *.tsv under <pool>/usage is read in sorted
// path order, its header is the first NON-BLANK line, and each row folds through the named
// columns -- a blank job is noid=, a repeated (job, attempt) is dup=, a stamp this tool
// cannot read and a row of the wrong width are unparsed=, a dash or empty cell is an
// absence, and a done/ or failed/ directory with no usage file is nousage=.
func TestSwarmCoverReadSwarmFoldsUsageFiles(t *testing.T) {
	t.Parallel()

	pool := swarmCoverPool(t)
	swarmCoverUsage(t, pool, "a.tsv", strings.Join([]string{
		swarmCoverHeader(),
		swarmCoverRow("job1", "1", "fromA", "2026-09-16T07:00:00Z", "2026-09-16T08:00:00Z", "endA", "0", "anthropic", "claude-x", "/w/schema", "10", "2", "5", "7", "-", "1.23"),
		swarmCoverRow("", "1", "", "2026-09-16T07:00:00Z", "2026-09-16T08:00:00Z", "", "0", "", "", "/w/schema", "9", "-", "-", "-", "-", "-"),
		swarmCoverRow("job1", "1", "fromA", "2026-09-16T07:00:00Z", "2026-09-16T08:00:00Z", "endA", "0", "anthropic", "claude-x", "/w/schema", "10", "2", "5", "7", "-", "1.23"),
		swarmCoverRow("job1", "2", "", "2026-09-16T07:00:00Z", "yesterday", "", "0", "", "", "/w/schema", "1", "1", "-", "-", "-", "-"),
		swarmCoverRow("job1", "3", "too-few-columns"),
		"",
	}, "\n"))
	// The leading blank line proves the header is the first non-blank line, not line 1.
	swarmCoverUsage(t, pool, "b.tsv", strings.Join([]string{
		"",
		swarmCoverHeader(),
		swarmCoverRow("job2", "2", "", "2026-09-16T09:00:00Z", "2026-09-16T09:00:00Z", "", "1", "openai", "gpt-5", "/w/other", "3", "", "-", "", "", "-"),
		swarmCoverRow("job3", "1", "", "2026-09-16T10:00:00Z", "2026-09-16T10:00:00Z", "", "0", "", "", "", "1", "1", "-", "-", "-", "-"),
		"",
	}, "\n"))
	swarmCoverUsage(t, pool, "notes.txt", "not a usage file")
	require.NoError(t, os.MkdirAll(filepath.Join(pool, "usage", "subdir"), 0o700))
	require.NoError(t, os.MkdirAll(filepath.Join(pool, "done", "a"), 0o700))
	require.NoError(t, os.MkdirAll(filepath.Join(pool, "done", "orphan"), 0o700))
	require.NoError(t, os.MkdirAll(filepath.Join(pool, "failed", "orphan2"), 0o700))

	s := ReadSwarm("mylabel", pool, os.DirFS(pool), swarmCoverRules(t))

	assert.Equalf(t, "swarm:mylabel", s.Label, "label=%q, want swarm:mylabel", s.Label)
	assert.Equalf(t, KindSwarm, s.Kind, "kind=%q, want %q", s.Kind, KindSwarm)
	assert.Equalf(t, pool, s.Path, "path=%q, want the pool", s.Path)
	assert.Equalf(t, UTC, s.Basis, "basis=%q, want %q: every swarm row is dated from a stamp", s.Basis, UTC)
	assert.Equal(t, AllTypes, s.Reports, "a swarm source reports all five types")
	assert.Equalf(t, 2, s.Stat.Files, "files=%d, want 2: the .txt and the subdirectory are not usage files", s.Stat.Files)
	assert.Equalf(t, 0, s.Stat.Unreadable, "unreadable=%d, want 0", s.Stat.Unreadable)
	assert.Equalf(t, 3, s.Stat.Messages, "messages=%d, want 3: job1, job2 and job3", s.Stat.Messages)
	assert.Equalf(t, 1, s.Stat.NoID, "noid=%d, want 1: the blank job cell", s.Stat.NoID)
	assert.Equalf(t, 1, s.Stat.Dup, "dup=%d, want 1: job1 attempt 1 on two rows", s.Stat.Dup)
	assert.Equalf(t, 2, s.Stat.Unparsed, "unparsed=%d, want 2: the bad stamp and the short row", s.Stat.Unparsed)
	assert.Equalf(t, 2, s.Stat.NoUsage, "nousage=%d, want 2: done/orphan and failed/orphan2 have no usage file", s.Stat.NoUsage)

	job1 := swarmCoverMessage(s.Stream, "claude-x")
	assert.Equalf(t, "2026-09-16", job1.Day, "job1 day=%q, want 2026-09-16", job1.Day)
	assert.Equalf(t, "schema", job1.Repo, "job1 repo=%q, want schema: the rules file named it", job1.Repo)
	assert.Equalf(t, "anthropic", job1.Provider, "job1 provider=%q, want anthropic", job1.Provider)
	assert.Equalf(t, "10", job1.Counts.Cell(Input), "job1 input=%s, want 10", job1.Counts.Cell(Input))
	assert.Equalf(t, "2", job1.Counts.Cell(Output), "job1 output=%s, want 2", job1.Counts.Cell(Output))
	assert.Equalf(t, "5", job1.Counts.Cell(CacheWrite), "job1 cache_write=%s, want 5", job1.Counts.Cell(CacheWrite))
	assert.Equalf(t, "7", job1.Counts.Cell(CacheRead), "job1 cache_read=%s, want 7", job1.Counts.Cell(CacheRead))
	assert.Equalf(t, Dash, job1.Counts.Cell(Reasoning), "job1 reasoning=%s, want a dash: the cell was `-`", job1.Counts.Cell(Reasoning))
	assert.Equalf(t, int64(1230000), job1.Usd, "job1 usd=%d, want 1230000 micro-dollars", job1.Usd)
	assert.Truef(t, job1.Priced, "job1 priced=%t, want true: the usd column carried a cost", job1.Priced)

	job2 := swarmCoverMessage(s.Stream, "gpt-5")
	assert.Equalf(t, Other, job2.Repo, "job2 repo=%q, want other: the path matched no rule", job2.Repo)
	assert.Equalf(t, "openai", job2.Provider, "job2 provider=%q, want openai", job2.Provider)
	assert.Equalf(t, "3", job2.Counts.Cell(Input), "job2 input=%s, want 3", job2.Counts.Cell(Input))
	assert.Equalf(t, Dash, job2.Counts.Cell(Output), "job2 output=%s, want a dash: the cell was empty", job2.Counts.Cell(Output))
	assert.Falsef(t, job2.Priced, "job2 priced=%t, want false: no cost was reported", job2.Priced)

	job3 := swarmCoverMessage(s.Stream, "")
	assert.Equalf(t, Unknown, job3.Repo, "job3 repo=%q, want unknown: no path was ever seen", job3.Repo)
	assert.Equalf(t, "1", job3.Counts.Cell(Input), "job3 input=%s, want 1", job3.Counts.Cell(Input))
	assert.Equalf(t, "1", job3.Counts.Cell(Output), "job3 output=%s, want 1", job3.Counts.Cell(Output))
}

// TestSwarmCoverReadSwarmRefusesWhatItCannotRead: an absent usage directory, a usage file
// that will not open, a header that is not the sixteen in order, a data row of the wrong
// width and a done/ directory with no usage file are each counted and named -- never
// silent, and never one instead of the other.
func TestSwarmCoverReadSwarmRefusesWhatItCannotRead(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		write func(t *testing.T) string
		want  func(t *testing.T, s *Source)
	}{
		{
			name: "an absent usage directory is one unreadable naming it",
			write: func(t *testing.T) string {
				t.Helper()
				return filepath.Join(t.TempDir(), "absent")
			},
			want: func(t *testing.T, s *Source) {
				t.Helper()
				require.Equalf(t, 1, s.Stat.Unreadable, "unreadable=%d, want 1", s.Stat.Unreadable)
				require.Lenf(t, s.Unreadables, 1, "unreadables=%d, want 1", len(s.Unreadables))
				assert.Equalf(t, filepath.Join(s.Path, "usage"), s.Unreadables[0].Path, "the unreadable names the usage directory, got %q", s.Unreadables[0].Path)
				assert.Containsf(t, s.Unreadables[0].Why, "no such file", "why=%q, want the read's own error", s.Unreadables[0].Why)
				assert.Equalf(t, 0, s.Stat.Files, "files=%d, want 0", s.Stat.Files)
				assert.Empty(t, s.Stream, "stream=%d, want 0", len(s.Stream))
			},
		},
		{
			name: "a usage file that will not open is counted and named",
			write: func(t *testing.T) string {
				t.Helper()
				pool := swarmCoverPool(t)
				require.NoError(t, os.Symlink(filepath.Join(pool, "nowhere"), filepath.Join(pool, "usage", "broken.tsv")))
				return pool
			},
			want: func(t *testing.T, s *Source) {
				t.Helper()
				assert.Equalf(t, 1, s.Stat.Files, "files=%d, want 1: the file was attempted", s.Stat.Files)
				require.Equalf(t, 1, s.Stat.Unreadable, "unreadable=%d, want 1", s.Stat.Unreadable)
				assert.Truef(t, strings.HasSuffix(s.Unreadables[0].Path, "broken.tsv"), "path=%q, want the file that would not open", s.Unreadables[0].Path)
				assert.Containsf(t, s.Unreadables[0].Why, "no such file", "why=%q, want the open's own error", s.Unreadables[0].Why)
			},
		},
		{
			name: "a header that is not the sixteen in order is unparsed and the file is left",
			write: func(t *testing.T) string {
				t.Helper()
				pool := swarmCoverPool(t)
				swarmCoverUsage(t, pool, "bad.tsv", "job\tattempt\njob1\t1\n")
				return pool
			},
			want: func(t *testing.T, s *Source) {
				t.Helper()
				assert.Equalf(t, 1, s.Stat.Files, "files=%d, want 1", s.Stat.Files)
				assert.Equalf(t, 0, s.Stat.Unreadable, "unreadable=%d, want 0: a wrong header is unparsed, not unreadable", s.Stat.Unreadable)
				require.Equalf(t, 1, s.Stat.Unparsed, "unparsed=%d, want 1", s.Stat.Unparsed)
				assert.Containsf(t, s.Unparseds[0].Text, "missing the column", "text=%q, want the named missing column", s.Unparseds[0].Text)
				assert.Equalf(t, 1, s.Unparseds[0].Line, "line=%d, want 1: the header is the first non-blank line", s.Unparseds[0].Line)
				assert.Empty(t, s.Stream, "stream=%d, want 0: the file was left at its bad header", len(s.Stream))
			},
		},
		{
			name: "a data row of the wrong width is unparsed and the file goes on",
			write: func(t *testing.T) string {
				t.Helper()
				pool := swarmCoverPool(t)
				swarmCoverUsage(t, pool, "rows.tsv", strings.Join([]string{
					swarmCoverHeader(),
					"too\tfew",
					swarmCoverRow("job1", "1", "", "2026-09-16T08:00:00Z", "2026-09-16T08:00:00Z", "", "0", "", "m", "/w/schema", "1", "1", "-", "-", "-", "-"),
					"",
				}, "\n"))
				return pool
			},
			want: func(t *testing.T, s *Source) {
				t.Helper()
				require.Equalf(t, 1, s.Stat.Unparsed, "unparsed=%d, want 1: the short row", s.Stat.Unparsed)
				assert.Containsf(t, s.Unparseds[0].Text, "2 columns, want 16", "text=%q, want the width named", s.Unparseds[0].Text)
				assert.Equalf(t, 1, s.Stat.Messages, "messages=%d, want 1: the row after the short one still folds", s.Stat.Messages)
			},
		},
		{
			name: "a done directory with no usage file beside it is nousage",
			write: func(t *testing.T) string {
				t.Helper()
				pool := swarmCoverPool(t)
				require.NoError(t, os.MkdirAll(filepath.Join(pool, "done", "orphan"), 0o700))
				require.NoError(t, os.MkdirAll(filepath.Join(pool, "failed", "orphan2"), 0o700))
				return pool
			},
			want: func(t *testing.T, s *Source) {
				t.Helper()
				assert.Equalf(t, 2, s.Stat.NoUsage, "nousage=%d, want 2", s.Stat.NoUsage)
				assert.Equalf(t, 0, s.Stat.Messages, "messages=%d, want 0", s.Stat.Messages)
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			pool := tc.write(t)
			s := ReadSwarm("cover", pool, os.DirFS(pool), swarmCoverRules(t))
			tc.want(t, s)
		})
	}
}

// TestSwarmCoverWrongColumn: the header is the sixteen names in order; a column out of
// place, a missing column and a header wider than sixteen are each refused by name, and the
// right header is the empty string.
func TestSwarmCoverWrongColumn(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		cells []string
		want  string
	}{
		{
			name:  "the sixteen in order are accepted",
			cells: append([]string(nil), SwarmColumns...),
			want:  "",
		},
		{
			name:  "a column out of place is named with its position and the column wanted",
			cells: []string{"job", "attempt", "from", "model"},
			want:  "column 4 is model, want started (SPEC-WORKER rule 12 names sixteen, in order)",
		},
		{
			name:  "a missing column is named",
			cells: SwarmColumns[:15],
			want:  "the header is missing the column usd (SPEC-WORKER rule 12 names sixteen, in order)",
		},
		{
			name:  "a header wider than sixteen is refused",
			cells: append(append([]string(nil), SwarmColumns...), "extra"),
			want:  "the header has 17 columns, want 16",
		},
		{
			name:  "an empty header names the first column",
			cells: nil,
			want:  "the header is missing the column job (SPEC-WORKER rule 12 names sixteen, in order)",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := wrongColumn(tc.cells)
			assert.Equalf(t, tc.want, got, "wrongColumn(%v) = %q, want %q", tc.cells, got, tc.want)
		})
	}
}

// TestSwarmCoverUnparsedCountsAndNames: an unparsed line is counted and carries the source's
// label, the note naming the file, the line and the text -- the reason the run reports it.
func TestSwarmCoverUnparsedCountsAndNames(t *testing.T) {
	t.Parallel()

	s := &Source{Label: Label(KindSwarm, "cover")}
	s.unparsed("/w/usage/a.tsv", 4, "2 columns, want 16")
	s.unparsed("/w/usage/b.tsv", 9, "the ended stamp is not a date this tool can read: yesterday")

	assert.Equalf(t, 2, s.Stat.Unparsed, "unparsed=%d, want 2", s.Stat.Unparsed)
	assert.Equal(t, []Unparsed{
		{Label: "swarm:cover", Note: "/w/usage/a.tsv", Line: 4, Text: "2 columns, want 16"},
		{Label: "swarm:cover", Note: "/w/usage/b.tsv", Line: 9, Text: "the ended stamp is not a date this tool can read: yesterday"},
	}, s.Unparseds, "each unparsed names the label, the file, the line and the text: %+v", s.Unparseds)
}
