package swarm

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// THE TIMELINE READER IS TESTED AT ITS OWN SEAMS: NewTimeline, Write, Rows, observe and
// close timestamp the harness's NOVA-TIMELINE report lines; cutWord, metaValue, cmdValue and
// tokenOrEmpty parse one line; WriteTimeline, ReadTimeline, scrubCell and cell fold the rows
// into and out of a card's timeline.tsv; ProfileJobs, profileLine, profileRows and
// phaseOfTool print one PROFILE line per job. Nothing here sleeps, starts a child, opens a
// socket or touches a store: the recorder stamps itself with the clock, and every fold test
// writes the instants it wants through a time it makes, never a time it waits for.

func TestTimelineCoverNewTimelineStartsEmpty(t *testing.T) {
	t.Parallel()

	tl := NewTimeline()
	require.NotNil(t, tl)
	assert.Empty(t, tl.Rows(), "a fresh recorder has observed no span yet")
}

func TestTimelineCoverRecorderFoldsReportLinesIntoRows(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		writes []string
		want   []TimelineRow
	}{
		{
			name:   "a turn keeps the token counts the harness reported",
			writes: []string{"NOVA-TIMELINE TURN BEGIN\n", "NOVA-TIMELINE TURN END in=120 out=34\n"},
			want:   []TimelineRow{{Tool: "model", InputTokens: "120", OutputTokens: "34"}},
		},
		{
			name:   "the dash is an absence, never a zero",
			writes: []string{"NOVA-TIMELINE TURN BEGIN\n", "NOVA-TIMELINE TURN END in=- out=-\n"},
			want:   []TimelineRow{{Tool: "model"}},
		},
		{
			name:   "a tool row carries name, command and rc",
			writes: []string{"NOVA-TIMELINE TOOL BEGIN name=edit cmd=go mod tidy\n", "NOVA-TIMELINE TOOL END name=edit rc=1\n"},
			want:   []TimelineRow{{Tool: "edit go mod tidy rc=1"}},
		},
		{
			name:   "the end renames the open tool and keeps its command",
			writes: []string{"NOVA-TIMELINE TOOL BEGIN name=edit cmd=go test ./...\n", "NOVA-TIMELINE TOOL END name=bash rc=0\n"},
			want:   []TimelineRow{{Tool: "bash go test ./... rc=0"}},
		},
		{
			name:   "a tool with neither name nor command still rows",
			writes: []string{"NOVA-TIMELINE TOOL BEGIN\n", "NOVA-TIMELINE TOOL END\n"},
			want:   []TimelineRow{{Tool: "tool"}},
		},
		{
			name:   "the next begin closes the span left open",
			writes: []string{"NOVA-TIMELINE TURN BEGIN\n", "NOVA-TIMELINE TOOL BEGIN name=x\n", "NOVA-TIMELINE TOOL END rc=2\n"},
			want:   []TimelineRow{{Tool: "model"}, {Tool: "x rc=2"}},
		},
		{
			name:   "an end with no begin is refused",
			writes: []string{"NOVA-TIMELINE TOOL END rc=1\n"},
			want:   nil,
		},
		{
			name:   "an end of the wrong kind leaves the span open",
			writes: []string{"NOVA-TIMELINE TURN BEGIN\n", "NOVA-TIMELINE TOOL END rc=0\n"},
			want:   nil,
		},
		{
			name:   "plain child output is not an event",
			writes: []string{"hello world\n", "go: building...\n"},
			want:   nil,
		},
		{
			name:   "a kind with no verb is refused",
			writes: []string{"NOVA-TIMELINE TURN\n", "NOVA-TIMELINE\n"},
			want:   nil,
		},
		{
			name:   "the grammar reads across case",
			writes: []string{"NOVA-TIMELINE turn begin\n", "NOVA-TIMELINE turn end in=1 out=2\n"},
			want:   []TimelineRow{{Tool: "model", InputTokens: "1", OutputTokens: "2"}},
		},
		{
			name:   "a partial line waits for its newline",
			writes: []string{"NOVA-TIMELINE TURN", " BEGIN\n", "NOVA-TIMELINE TURN END in=- out=-\n"},
			want:   []TimelineRow{{Tool: "model"}},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tl := NewTimeline()
			for _, w := range c.writes {
				n, err := tl.Write([]byte(w))
				require.NoError(t, err, "Write of %q", w)
				require.Equal(t, len(w), n, "Write takes the bytes it is given")
			}
			rows := tl.Rows()
			require.Len(t, rows, len(c.want))
			for i, want := range c.want {
				assert.Equal(t, want.Tool, rows[i].Tool)
				assert.Equal(t, want.InputTokens, rows[i].InputTokens)
				assert.Equal(t, want.OutputTokens, rows[i].OutputTokens)
				assert.False(t, rows[i].Start.IsZero(), "a row stamps when its span began")
				assert.False(t, rows[i].End.Before(rows[i].Start), "a span's end is not before its start")
			}
		})
	}
}

func TestTimelineCoverRowsReturnsACopyNotTheLiveSlice(t *testing.T) {
	t.Parallel()

	tl := NewTimeline()
	_, err := tl.Write([]byte("NOVA-TIMELINE TURN BEGIN\nNOVA-TIMELINE TURN END in=5 out=6\n"))
	require.NoError(t, err)
	rows := tl.Rows()
	require.Len(t, rows, 1)
	rows[0].Tool = "tampered"
	fresh := tl.Rows()
	require.Len(t, fresh, 1)
	assert.Equal(t, "model", fresh[0].Tool, "the caller cannot rewrite the recorder through a returned row")
}

func TestTimelineCoverCloseWithNoSpanOpenWritesNoRow(t *testing.T) {
	t.Parallel()

	tl := NewTimeline()
	tl.close(time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC), "", "", "")
	assert.Empty(t, tl.Rows(), "closing a span that was never opened is nothing, not a panic or a row")
}

func TestTimelineCoverCutWordSplitsTheFirstWord(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		in       string
		wantWord string
		wantRest string
		wantOK   bool
	}{
		{"verb and meta", "BEGIN name=edit", "BEGIN", "name=edit", true},
		{"a tab divides words", "END\tin=1", "END", "in=1", true},
		{"a lone word has no rest", "END", "END", "", true},
		{"padding is not a word", "  a b  ", "a", "b", true},
		{"empty refuses", "", "", "", false},
		{"spaces alone refuse", " \t ", "", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			word, rest, ok := cutWord(c.in)
			assert.Equal(t, c.wantWord, word)
			assert.Equal(t, c.wantRest, rest)
			assert.Equal(t, c.wantOK, ok)
		})
	}
}

func TestTimelineCoverMetaValueAndCmdValueReadFields(t *testing.T) {
	t.Parallel()

	metaCases := []struct {
		name string
		meta string
		key  string
		want string
	}{
		{"the first key", "name=edit rc=1", "name", "edit"},
		{"a later key", "name=edit rc=1", "rc", "1"},
		{"a missing key reads as absent", "name=edit", "rc", ""},
		{"a longer key is not the wanted one", "names=x name=y", "name", "y"},
		{"empty meta", "", "name", ""},
	}
	for _, c := range metaCases {
		t.Run("metaValue "+c.name, func(t *testing.T) {
			assert.Equal(t, c.want, metaValue(c.meta, c.key))
		})
	}
	cmdCases := []struct {
		name string
		in   string
		want string
	}{
		{"cmd runs to the end of the line", "name=edit cmd=go test ./...", "go test ./..."},
		{"no cmd reads as absent", "name=edit", ""},
		{"cmd alone", "cmd=tail", "tail"},
		{"the command is trimmed", "cmd=   ls -l  ", "ls -l"},
	}
	for _, c := range cmdCases {
		t.Run("cmdValue "+c.name, func(t *testing.T) {
			assert.Equal(t, c.want, cmdValue(c.in))
		})
	}
}

func TestTimelineCoverTokenOrEmptyTreatsDashAsAbsence(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   string
		want string
	}{
		{"a count the harness gave is kept", "42", "42"},
		{"a padded count is trimmed", " 7 ", "7"},
		{"the dash is the absence the harness means", "-", ""},
		{"an empty field stays empty", "", ""},
		{"two dashes are not the reported absence", "--", "--"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, tokenOrEmpty(c.in))
		})
	}
}

func TestTimelineCoverScrubCellFlattensAndCellMapsColumns(t *testing.T) {
	t.Parallel()

	scrubCases := []struct {
		name string
		in   string
		want string
	}{
		{"tabs, newlines and carriage returns become spaces", "a\tb\nc\rd", "a b c d"},
		{"padding is trimmed", "  x  ", "x"},
		{"a plain cell is unchanged", "model", "model"},
	}
	for _, c := range scrubCases {
		t.Run("scrubCell "+c.name, func(t *testing.T) {
			assert.Equal(t, c.want, scrubCell(c.in))
		})
	}

	cells := []string{"t0", "model", "extra"}
	at := map[string]int{"t_start": 0, "tool": 1, "far": 4, "negative": -1}
	cellCases := []struct {
		name string
		col  string
		want string
	}{
		{"a column is read by its header name", "tool", "model"},
		{"a column past the row is empty", "far", ""},
		{"an unnamed column is empty", "output_tokens", ""},
		{"a negative index is empty", "negative", ""},
	}
	for _, c := range cellCases {
		t.Run("cell "+c.name, func(t *testing.T) {
			assert.Equal(t, c.want, cell(cells, at, c.col))
		})
	}
}

func TestTimelineCoverWriteTimelineReadTimelineRoundTrip(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	rows := []TimelineRow{
		{Start: base, End: base.Add(250 * time.Millisecond), Tool: "model", InputTokens: "10", OutputTokens: "20"},
		{Start: base.Add(time.Second), End: base.Add(1500 * time.Millisecond), Tool: "edit\tgo\ntest\rx"},
	}
	t.Run("a written timeline is one header and one row per event, and reads back whole", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), TimelineFileName)
		require.NoError(t, WriteTimeline(path, rows))
		raw, err := os.ReadFile(path)
		require.NoError(t, err)
		wantFile := strings.Join([]string{
			"t_start\tt_end\ttool\twall_ms\tinput_tokens\toutput_tokens",
			"2026-01-01T12:00:00Z\t2026-01-01T12:00:00.25Z\tmodel\t250\t10\t20",
			"2026-01-01T12:00:01Z\t2026-01-01T12:00:01.5Z\tedit go test x\t500\t\t",
			"",
		}, "\n")
		assert.Equal(t, wantFile, string(raw), "every row is scrubbed to one row of six cells")

		got, err := ReadTimeline(path)
		require.NoError(t, err)
		require.Len(t, got, len(rows))
		for i, want := range rows {
			assert.True(t, got[i].Start.Equal(want.Start), "row %d start: %s", i, got[i].Start)
			assert.True(t, got[i].End.Equal(want.End), "row %d end: %s", i, got[i].End)
			assert.Equal(t, scrubCell(want.Tool), got[i].Tool)
			assert.Equal(t, want.InputTokens, got[i].InputTokens)
			assert.Equal(t, want.OutputTokens, got[i].OutputTokens)
		}
	})
	t.Run("an absent file is an empty timeline and no error", func(t *testing.T) {
		got, err := ReadTimeline(filepath.Join(t.TempDir(), "never-written.tsv"))
		assert.NoError(t, err)
		assert.Empty(t, got)
	})
	t.Run("a renamed header still maps its columns by name", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), TimelineFileName)
		require.NoError(t, os.WriteFile(path, []byte(
			"tool\tt_start\tt_end\nmodel\t2026-01-01T12:00:00Z\t2026-01-01T12:00:02Z\n"), 0o644))
		got, err := ReadTimeline(path)
		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Equal(t, "model", got[0].Tool)
		assert.True(t, got[0].Start.Equal(base), "t_start read from its header position")
		assert.True(t, got[0].End.Equal(base.Add(2*time.Second)), "t_end read from its header position")
	})
	t.Run("a short row leaves its missing cells empty", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), TimelineFileName)
		require.NoError(t, os.WriteFile(path, []byte(
			strings.Join(TimelineColumns, "\t")+"\n\n2026\t2027\tc\n"), 0o644))
		got, err := ReadTimeline(path)
		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Equal(t, "c", got[0].Tool, "the blank line between header and row is skipped")
		assert.Empty(t, got[0].InputTokens, "a cell the row does not carry reads as absent")
		assert.True(t, got[0].Start.IsZero(), "an unparseable time is the zero time, not a failure")
	})
	t.Run("an empty file phases as nothing", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), TimelineFileName)
		require.NoError(t, os.WriteFile(path, nil, 0o644))
		got, err := ReadTimeline(path)
		assert.NoError(t, err)
		assert.Empty(t, got)
	})
	t.Run("reading a directory is an error, not an absent file", func(t *testing.T) {
		got, err := ReadTimeline(t.TempDir())
		assert.Error(t, err)
		assert.Nil(t, got)
	})
	t.Run("a write whose directory does not exist is refused", func(t *testing.T) {
		err := WriteTimeline(filepath.Join(t.TempDir(), "no-such-dir", TimelineFileName), rows)
		assert.Error(t, err)
	})
	t.Run("no rows still writes the header line alone", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), TimelineFileName)
		require.NoError(t, WriteTimeline(path, nil))
		raw, err := os.ReadFile(path)
		require.NoError(t, err)
		assert.Equal(t, strings.Join(TimelineColumns, "\t")+"\n", string(raw))
	})
}

func TestTimelineCoverProfileRowsFoldsTurnsToolsAndPhases(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	at := func(ms int) time.Time { return base.Add(time.Duration(ms) * time.Millisecond) }
	cases := []struct {
		name       string
		rows       []TimelineRow
		wantTurns  int
		wantTools  int
		wantWall   float64
		wantSecond map[string]float64
	}{
		{
			name: "turns count and each tool's seconds land in the phase its command names",
			rows: []TimelineRow{
				{Start: at(0), End: at(1000), Tool: "model"},
				{Start: at(1000), End: at(3000), Tool: "read cat x"},
				{Start: at(3000), End: at(4000), Tool: ""},
			},
			wantTurns:  2,
			wantTools:  1,
			wantWall:   4,
			wantSecond: map[string]float64{"read": 2},
		},
		{
			name: "a test run that follows a failing test run is retry",
			rows: []TimelineRow{
				{Start: at(0), End: at(1000), Tool: "go test ./... rc=1"},
				{Start: at(1000), End: at(2000), Tool: "go test ./... rc=0"},
			},
			wantTurns:  0,
			wantTools:  2,
			wantWall:   2,
			wantSecond: map[string]float64{"test": 1, "retry": 1},
		},
		{
			name: "only the first go build is a deps cost",
			rows: []TimelineRow{
				{Start: at(0), End: at(1000), Tool: "go build ./..."},
				{Start: at(1000), End: at(2000), Tool: "go build ./..."},
			},
			wantTurns:  0,
			wantTools:  2,
			wantWall:   2,
			wantSecond: map[string]float64{"deps": 1},
		},
		{
			name:       "a span that ends where it began adds no wall",
			rows:       []TimelineRow{{Start: at(0), End: at(0), Tool: "model"}},
			wantTurns:  1,
			wantTools:  0,
			wantWall:   0,
			wantSecond: map[string]float64{},
		},
		{
			name:       "no rows fold to nothing",
			rows:       nil,
			wantTurns:  0,
			wantTools:  0,
			wantWall:   0,
			wantSecond: map[string]float64{},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			j := profileRows("card1", c.rows)
			assert.Equal(t, "card1", j.Label)
			assert.Equal(t, c.wantTurns, j.Turns)
			assert.Equal(t, c.wantTools, j.Tools)
			assert.Equal(t, c.wantWall, j.Wall)
			assert.Equal(t, c.wantSecond, j.Seconds)
		})
	}
}

func TestTimelineCoverProfileLinePrintsTheFixedPhaseOrder(t *testing.T) {
	t.Parallel()

	j := JobProfile{
		Label:   "a\tb",
		Wall:    1.5,
		Turns:   1,
		Tools:   1,
		Seconds: map[string]float64{"clone": 1, "edit": 0.2},
	}
	assert.Equal(t,
		"PROFILE job=a b wall=1.5 turns=1 tools=1 clone=1.0 deps=0.0 read=0.0 edit=0.2 test=0.0 retry=0.0 result=0.0",
		profileLine(j))
}

func TestTimelineCoverPhaseOfToolNamesThePhaseOrNothing(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		tool    string
		saw     bool
		want    string
		wantSaw bool
	}{
		{"a RESULT.md write is the result though it is also an edit", "write RESULT.md", false, "result", false},
		{"clone", "git clone url", false, "clone", false},
		{"fetch is a clone too", "git fetch origin", false, "clone", false},
		{"a test is a test before it is a read", "go test ./...", false, "test", false},
		{"the gate script is a test", "bash run-tests.sh", false, "test", false},
		{"make test is a test", "make test", false, "test", false},
		{"go mod is deps", "go mod download", false, "deps", false},
		{"the first go build is deps", "go build ./...", false, "deps", true},
		{"a later go build owns no phase", "go build ./...", true, "", true},
		{"grep reads", "grep -rn foo", false, "read", false},
		{"cat reads", "cat notes.md", false, "read", false},
		{"sed reads", "sed -n 1,20p notes.md", false, "read", false},
		{"the read verb reads", "read file.txt", false, "read", false},
		{"edit edits", "edit main.go", false, "edit", false},
		{"write edits", "write notes.txt", false, "edit", false},
		{"an unlisted command owns no phase", "ls -l", false, "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			saw := c.saw
			assert.Equal(t, c.want, phaseOfTool(c.tool, &saw))
			assert.Equal(t, c.wantSaw, saw, "the sawBuild flag records exactly the builds seen")
		})
	}
}

func TestTimelineCoverProfileJobsFoldsAGlob(t *testing.T) {
	t.Parallel()

	const jobTSV = "t_start\tt_end\ttool\twall_ms\tinput_tokens\toutput_tokens\n" +
		"2026-01-01T12:00:00Z\t2026-01-01T12:00:01Z\tmodel\t1000\t10\t20\n" +
		"2026-01-01T12:00:01Z\t2026-01-01T12:00:03Z\tread cat x\t2000\t\t\n" +
		"2026-01-01T12:00:03Z\t2026-01-01T12:00:03.5Z\tedit main.go\t500\t\t\n"
	const oneTurnTSV = "t_start\tt_end\ttool\twall_ms\tinput_tokens\toutput_tokens\n" +
		"2026-01-01T12:00:00Z\t2026-01-01T12:00:02Z\tmodel\t2000\t\t\n"
	phases := "clone=0.0 deps=0.0 read=2.0 edit=0.5 test=0.0 retry=0.0 result=0.0"
	quiet := "clone=0.0 deps=0.0 read=0.0 edit=0.0 test=0.0 retry=0.0 result=0.0"

	cases := []struct {
		name       string
		build      func(t *testing.T, dir string)
		pattern    func(dir string) string
		wantExit   int
		wantStdout string
		wantStderr string
	}{
		{
			name: "a job directory folds to one PROFILE line and the summary averages the fleet",
			build: func(t *testing.T, dir string) {
				require.NoError(t, os.MkdirAll(filepath.Join(dir, "job1"), 0o755))
				require.NoError(t, os.WriteFile(filepath.Join(dir, "job1", TimelineFileName), []byte(jobTSV), 0o644))
				require.NoError(t, os.MkdirAll(filepath.Join(dir, "job2"), 0o755))
				require.NoError(t, os.WriteFile(filepath.Join(dir, "README.txt"), []byte("notes\n"), 0o644))
			},
			pattern:    func(dir string) string { return filepath.Join(dir, "*") },
			wantExit:   0,
			wantStdout: "PROFILE job=job1 wall=3.5 turns=1 tools=2 " + phases + "\nPROFILE SUMMARY jobs=1 mean_wall=3.5 " + phases + "\n",
		},
		{
			name: "the timeline file itself matches, labelled by its parent directory",
			build: func(t *testing.T, dir string) {
				require.NoError(t, os.MkdirAll(filepath.Join(dir, "file"), 0o755))
				require.NoError(t, os.WriteFile(filepath.Join(dir, "file", TimelineFileName), []byte(oneTurnTSV), 0o644))
			},
			pattern:    func(dir string) string { return filepath.Join(dir, "file", "*.tsv") },
			wantExit:   0,
			wantStdout: "PROFILE job=file wall=2.0 turns=1 tools=0 " + quiet + "\nPROFILE SUMMARY jobs=1 mean_wall=2.0 " + quiet + "\n",
		},
		{
			name:       "a glob that matches nothing is a measurement, not a refusal",
			build:      func(t *testing.T, dir string) {},
			pattern:    func(dir string) string { return filepath.Join(dir, "no-card-*") },
			wantExit:   0,
			wantStdout: "PROFILE SUMMARY jobs=0 mean_wall=0.0 " + quiet + "\n",
		},
		{
			name:       "a bad pattern is refused at exit 2 with the remedy on stderr",
			build:      func(t *testing.T, dir string) {},
			pattern:    func(dir string) string { return "[" },
			wantExit:   2,
			wantStdout: "",
			wantStderr: "nova-worker profile: --jobs wants a glob",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			c.build(t, dir)
			var out, errOut bytes.Buffer
			assert.Equal(t, c.wantExit, ProfileJobs(c.pattern(dir), &out, &errOut))
			assert.Equal(t, c.wantStdout, out.String())
			assert.Contains(t, errOut.String(), c.wantStderr)
		})
	}
}
