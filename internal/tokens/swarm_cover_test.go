package tokens

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// swarmHeader joins the sixteen SwarmColumns in order, the header a usage file
// the swarm writes must carry (SPEC-SWARM rule 12).
func swarmHeader() string {
	return strings.Join(SwarmColumns, "\t")
}

// swarmRow builds one well-formed usage row in SwarmColumns order. The job cell
// is the parameter so a case can pass "" to pin the no-id path; attempt 1, a UTC
// ended stamp the day reader turns into 2026-09-11, repo /w/schema/a.go which the
// fixture rule attributes to `schema`, and a value in each of the five token
// columns. Every cell a column names is present, so len(cells) == len(SwarmColumns).
func swarmRow(job string) string {
	return strings.Join([]string{
		job, "1", "-",
		"2026-09-11T10:00:00Z", "2026-09-11T10:05:00Z", "0", "-", "-",
		"gpt-4", "/w/schema/a.go",
		"1000", "100", "-", "-", "40", "2.50",
	}, "\t")
}

// writeSwarmRules writes a one-rule rules file and returns it loaded, so a read
// test can attribute the repo column to a real bucket instead of `other`.
func writeSwarmRules(t *testing.T) *Rules {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "repos.tsv")
	require.NoError(t, os.WriteFile(path, []byte("schema\t(^|/)schema($|/)\n"), 0o644))
	rules, err := LoadRules(path)
	require.NoError(t, err)
	return rules
}

// writePool lays files (path-within-pool -> content) and dirs into the pool
// tree ReadSwarm walks, so a case sets up exactly the shape it wants.
func writePool(t *testing.T, pool string, files map[string]string, dirs []string) {
	t.Helper()
	for _, d := range dirs {
		require.NoError(t, os.MkdirAll(filepath.Join(pool, d), 0o755))
	}
	for rel, content := range files {
		require.NoError(t, os.MkdirAll(filepath.Join(pool, filepath.Dir(rel)), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(pool, rel), []byte(content), 0o644))
	}
}

// TestSwarmCoverWrongColumn pins wrongColumn: the main path is a header whose
// sixteen cells match SwarmColumns in order and yields the empty string; each
// refusal names the first defect it found, in position order.
func TestSwarmCoverWrongColumn(t *testing.T) {
	t.Parallel()
	hdr := strings.Split(swarmHeader(), "\t")
	correct := append([]string{}, hdr...)
	badName := append([]string{}, hdr...)
	badName[14] = "not-reasoning"
	missing := append([]string{}, hdr[:15]...)
	extra := append(append([]string{}, hdr...), "boom")

	for _, tc := range []struct {
		name      string
		cells     []string
		wantEmpty bool
		wantSub   string
	}{
		{name: "main path: all sixteen columns in order", cells: correct, wantEmpty: true},
		{name: "refusal: a column that is not the one expected", cells: badName, wantSub: "column 15 is not-reasoning, want reasoning"},
		{name: "refusal: the header is short a column", cells: missing, wantSub: "the header is missing the column usd"},
		{name: "refusal: the header has too many columns", cells: extra, wantSub: "the header has 17 columns, want 16"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := wrongColumn(tc.cells)
			if tc.wantEmpty {
				assert.Truef(t, got == "", "wrongColumn(%d cols) = %q, want empty (the header is right)", len(tc.cells), got)
				return
			}
			assert.Truef(t, strings.Contains(got, tc.wantSub), "wrongColumn(%d cols) = %q, want it to contain %q", len(tc.cells), got, tc.wantSub)
		})
	}
}

// TestSwarmCoverUnparsed pins unparsed: a method that only records -- each call
// increments Stat.Unparsed and appends one Unparsed carrying the note, line and
// text, and calls are never collapsed onto one another.
func TestSwarmCoverUnparsed(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		notes    []string
		lines    []int
		texts    []string
		wantStat int
	}{
		{
			name:     "main path: a call records the note, line and text",
			notes:    []string{"job-1.tsv"},
			lines:    []int{2},
			texts:    []string{"column 15 is bogus, want reasoning"},
			wantStat: 1,
		},
		{
			name:     "each call is recorded, never collapsed",
			notes:    []string{"job-1.tsv", "job-1.tsv"},
			lines:    []int{2, 7},
			texts:    []string{"column 15 is bogus, want reasoning", "3 columns, want 16"},
			wantStat: 2,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := &Source{Label: Label(KindSwarm, "lab")}
			for i := range tc.notes {
				s.unparsed(tc.notes[i], tc.lines[i], tc.texts[i])
			}
			require.Equalf(t, tc.wantStat, s.Stat.Unparsed, "stat.unparsed")
			require.Lenf(t, s.Unparseds, tc.wantStat, "unparsed entries")
			for i := range tc.notes {
				u := s.Unparseds[i]
				assert.Equalf(t, s.Label, u.Label, "entry %d label", i)
				assert.Equalf(t, tc.notes[i], u.Note, "entry %d note", i)
				assert.Equalf(t, tc.lines[i], u.Line, "entry %d line", i)
				assert.Equalf(t, tc.texts[i], u.Text, "entry %d text", i)
			}
		})
	}
}

// TestSwarmCoverSwarmRead pins ReadSwarm: the main path reads a pool whose
// usage/ holds a valid tsv and yields one message per row; each refusal below is
// the seam the function keeps for a fixture it cannot read.
func TestSwarmCoverSwarmRead(t *testing.T) {
	t.Parallel()
	rules := writeSwarmRules(t)
	hdr := swarmHeader()

	for _, tc := range []struct {
		name  string
		files map[string]string
		dirs  []string
		check func(t *testing.T, s *Source)
	}{
		{
			name:  "main path: a valid usage file yields its rows",
			files: map[string]string{"usage/job-1.tsv": hdr + "\n" + swarmRow("job-1") + "\n"},
			check: func(t *testing.T, s *Source) {
				assert.Equal(t, 1, s.Stat.Files, "files")
				assert.Equal(t, 1, s.Stat.Messages, "messages")
				assert.Equal(t, AllTypes, s.Reports, "reports")
				require.Len(t, s.Stream, 1)
				m := s.Stream[0]
				assert.Equal(t, "2026-09-11", m.Day, "day from ended")
				assert.Equal(t, "gpt-4", m.Model, "model")
				assert.Equal(t, "schema", m.Repo, "repo attributed through the rules")
				assert.Equal(t, UTC, m.Basis, "basis")
				in, ok := m.Counts.Get(Input)
				assert.True(t, ok, "input present")
				assert.EqualValues(t, int64(1000), in, "input")
				out, ok := m.Counts.Get(Output)
				assert.True(t, ok, "output present")
				assert.EqualValues(t, int64(100), out, "output")
				reasoning, ok := m.Counts.Get(Reasoning)
				assert.True(t, ok, "reasoning present")
				assert.EqualValues(t, int64(40), reasoning, "reasoning")
				_, ok = m.Counts.Get(CacheWrite)
				assert.False(t, ok, "cache_write absent is a dash, not zero")
				_, ok = m.Counts.Get(CacheRead)
				assert.False(t, ok, "cache_read absent is a dash, not zero")
				assert.True(t, m.Priced, "usd priced")
				assert.EqualValues(t, int64(2500000), m.Usd, "usd in micro-dollars")
			},
		},
		{
			name: "refusal: a pool whose usage directory is missing is unreadable",
			check: func(t *testing.T, s *Source) {
				assert.Equal(t, 1, s.Stat.Unreadable, "unreadable")
				require.Len(t, s.Unreadables, 1)
				assert.Contains(t, s.Unreadables[0].Path, "usage", "names the unreadable path")
				assert.NotEmpty(t, s.Unreadables[0].Why, "gives a reason")
			},
		},
		{
			name:  "refusal: a usage file with a wrong header is unparsed",
			files: map[string]string{"usage/job-1.tsv": strings.Replace(swarmHeader(), "reasoning", "not-reasoning", 1) + "\n" + swarmRow("job-1") + "\n"},
			check: func(t *testing.T, s *Source) {
				assert.Equal(t, 1, s.Stat.Unparsed, "unparsed")
				assert.Equal(t, 1, s.Stat.Files, "files")
				assert.Equal(t, 0, s.Stat.Messages, "no row read past the bad header")
				require.Len(t, s.Unparseds, 1)
				assert.Contains(t, s.Unparseds[0].Text, "column 15", "names the bad column")
			},
		},
		{
			name:  "a row with an empty job is a no-id, not a message",
			files: map[string]string{"usage/job-1.tsv": hdr + "\n" + swarmRow("") + "\n"},
			check: func(t *testing.T, s *Source) {
				assert.Equal(t, 1, s.Stat.NoID, "noid")
				assert.Equal(t, 0, s.Stat.Messages, "no message")
			},
		},
		{
			name:  "a repeated (job, attempt) is a duplicate, not a second message",
			files: map[string]string{"usage/job-1.tsv": hdr + "\n" + swarmRow("job-1") + "\n" + swarmRow("job-1") + "\n"},
			check: func(t *testing.T, s *Source) {
				assert.Equal(t, 1, s.Stat.Messages, "messages")
				assert.Equal(t, 1, s.Stat.Dup, "dup")
			},
		},
		{
			name: "a done/ job directory with no usage file is no-usage",
			dirs: []string{"usage", "done/orphan-job"},
			check: func(t *testing.T, s *Source) {
				assert.Equal(t, 1, s.Stat.NoUsage, "nousage")
				require.Len(t, s.Unreadables, 0, "usage/ exists so the pool is readable")
				require.Len(t, s.Stream, 0)
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			pool := t.TempDir()
			writePool(t, pool, tc.files, tc.dirs)
			s := ReadSwarm("lab", pool, rules)
			if tc.check != nil {
				tc.check(t, s)
			}
		})
	}
}
