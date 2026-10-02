package tokens

// G5's red test: the fold of a fixture session matches a HAND COUNT. The numbers below were
// added up by hand and written here as constants, so a change to the arithmetic is red and a
// change to the reader that quietly counts a streamed message twice is red too.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fixtureSession is five turns of a Claude Code window, as the transcript writes them: an
// id per turn, a usage block per line, and one STREAMED turn whose id appears twice with a
// growing usage block. Six lines, five turns.
//
// The hand count, turn by turn:
//
//	turn 1  input   12  cache_write  2000  cache_read      0  output  300
//	turn 2  input    4  cache_write   500  cache_read  20000  output  120
//	turn 3  input    9  cache_write     0  cache_read  21000  output   45
//	turn 4  input    2  cache_write   150  cache_read  22000  output  900
//	turn 5  input    7  cache_write     0  cache_read  23000  output  210   (streamed, last line wins)
//	        ------      -----------        ----------         ------
//	          34             2650              86000            1575
const (
	wantTurns      = 5
	wantInput      = 34
	wantCacheWrite = 2650
	wantCacheRead  = 86000
	wantOutput     = 1575
	// weighted = 34 + 1.25 x 2650 + 0.1 x 86000 + 5 x 1575 = 34 + 3312.5 + 8600 + 7875
	wantWeighted = 19821
	// avg_context = (34 + 2650 + 86000) / 5 = 88684 / 5 = 17736 (whole tokens)
	wantAvgContext = 17736
)

func writeFixtureSession(t *testing.T, dir string) string {
	t.Helper()
	line := func(stamp, id string, in, cw, cr, out int) string {
		return fmt.Sprintf(`{"timestamp":%q,"cwd":"/repo","message":{"id":%q,"model":"claude-opus-5","usage":{"input_tokens":%d,"cache_creation_input_tokens":%d,"cache_read_input_tokens":%d,"output_tokens":%d}}}`,
			stamp, id, in, cw, cr, out)
	}
	body := strings.Join([]string{
		line("2026-09-16T09:00:00Z", "msg-1", 12, 2000, 0, 300),
		`{"timestamp":"2026-09-16T09:00:05Z","cwd":"/repo","message":{"content":"a user turn carries no usage"}}`,
		line("2026-09-16T09:01:00Z", "msg-2", 4, 500, 20000, 120),
		line("2026-09-16T09:02:00Z", "msg-3", 9, 0, 21000, 45),
		line("2026-09-16T09:03:00Z", "msg-4", 2, 150, 22000, 900),
		// The streamed turn: the same id twice, the usage growing. The LAST line is the
		// message; a reader that added both would report 420 output tokens for one turn.
		line("2026-09-16T09:04:00Z", "msg-5", 7, 0, 23000, 210-100),
		line("2026-09-16T09:04:02Z", "msg-5", 7, 0, 23000, 210),
		`{"timestamp":"2026-09-16T09:05:00Z","message":{"id":"msg-6","model":"<synthetic>","usage":{"input_tokens":9999,"output_tokens":9999}}}`,
		"",
	}, "\n")
	path := filepath.Join(dir, "session.jsonl")
	require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
	return path
}

// TestReadClaudeSessionMatchesTheHandCount is the whole claim of G5.
func TestReadClaudeSessionMatchesTheHandCount(t *testing.T) {
	t.Parallel()

	sum, err := ReadClaudeSession(writeFixtureSession(t, t.TempDir()))
	require.NoError(t, err)
	for _, c := range []struct {
		name      string
		got, want int64
	}{
		{"turns", int64(sum.Turns), wantTurns},
		{"input", sum.Input, wantInput},
		{"cache_write", sum.CacheWrite, wantCacheWrite},
		{"cache_read", sum.CacheRead, wantCacheRead},
		{"output", sum.Output, wantOutput},
		{"weighted", sum.Weighted(), wantWeighted},
		{"avg_context", sum.AvgContext(), wantAvgContext},
	} {
		assert.EqualValuesf(t, c.want, c.got, "%s=%d, want %d (the hand count)", c.name, c.got, c.want)
	}
	want := fmt.Sprintf("SESSION turns=%d input=%d cache_write=%d cache_read=%d output=%d weighted=%d avg_context=%d",
		wantTurns, wantInput, wantCacheWrite, wantCacheRead, wantOutput, wantWeighted, wantAvgContext)
	assert.EqualValuesf(t, want, sum.Line(), "the line is\n  %s\nwant\n  %s", sum.Line(), want)
	assert.EqualValuesf(t, 0, sum.Unstamped, "unstamped=%d, want 0: every turn in the fixture carries a stamp", sum.Unstamped)
}

// TestSessionRowIsTheCoordinatorsOwnLine: the fold writes the coordinator as a model of its
// own, per day, with the four counts and a reasoning cell that is a dash -- a transcript
// carries no reasoning count, and a zero there would sum into a month claiming to be whole.
func TestSessionRowIsTheCoordinatorsOwnLine(t *testing.T) {
	t.Parallel()

	sum, err := ReadClaudeSession(writeFixtureSession(t, t.TempDir()))
	require.NoError(t, err)
	days := sum.DayList()
	require.Equalf(t, []string{"2026-09-16"}, days, "days=%v, want one day 2026-09-16", days)
	rows := sum.Rows(days[0])
	require.Lenf(t, rows, 1, "rows=%d, want one row for the one model the transcript names", len(rows))
	row := rows[0]
	assert.Equalf(t, "claude-opus-5/coordinator", row.Model, "row is (%s, %s): the model the transcript names", row.Model, row.Repo)
	assert.Equalf(t, CoordinatorRepo, row.Repo, "row is (%s, %s): the repo is %s", row.Model, row.Repo, CoordinatorRepo)
	assert.EqualValuesf(t, Dash, row.Counts.Cell(Reasoning), "the reasoning cell is %q, want %q: a transcript reports none", row.Counts.Cell(Reasoning), Dash)
	for _, c := range []struct {
		t    Type
		want string
	}{{Input, "34"}, {CacheWrite, "2650"}, {CacheRead, "86000"}, {Output, "1575"}} {
		{
			got := row.Counts.Cell(c.t)
			assert.EqualValuesf(t, c.want, got, "%s cell=%s, want %s", TypeNames[c.t], got, c.want)
		}
	}
	assert.Equalf(t, []string{SessionLabel}, row.Sources, "every number names the flag that wrote it")
}

// TestSessionSplitsAcrossMidnight: a window that runs past midnight is two rows on two days,
// never one row dated by the file it lives in.
func TestSessionSplitsAcrossMidnight(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "s.jsonl")
	body := `{"timestamp":"2026-09-16T23:59:00Z","message":{"id":"a","model":"m","usage":{"input_tokens":10,"output_tokens":1}}}
{"timestamp":"2026-09-17T00:01:00Z","message":{"id":"b","model":"m","usage":{"input_tokens":20,"output_tokens":2}}}
{"message":{"id":"c","model":"m","usage":{"input_tokens":5,"output_tokens":5}}}
`
	require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
	sum, err := ReadClaudeSession(path)
	require.NoError(t, err)
	{
		got := sum.DayList()
		require.Equalf(t, []string{"2026-09-16", "2026-09-17"}, got, "want the two days the turns fell on")
	}
	assert.EqualValuesf(t, 10, sum.Days["2026-09-16"].Input, "the first day's input")
	assert.EqualValuesf(t, 20, sum.Days["2026-09-17"].Input, "the second day's input")
	// The undated turn is in the totals and in no day, and it is COUNTED so a reader knows.
	assert.EqualValuesf(t, 3, sum.Turns, "turns")
	assert.EqualValuesf(t, 35, sum.Input, "input")
	assert.EqualValuesf(t, 1, sum.Unstamped, "unstamped")
}

// TestSessionRowsAreBookedUnderTheModelTheTranscriptNames: a transcript of one model is that
// model's row, a window that changes model is one row per model, and a turn naming no model
// makes the session unbookable instead of booking it under a guess.
func TestSessionRowsAreBookedUnderTheModelTheTranscriptNames(t *testing.T) {
	t.Parallel()

	write := func(body string) string {
		path := filepath.Join(t.TempDir(), "s.jsonl")
		require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
		return path
	}

	sum, err := ReadClaudeSession(write(`{"timestamp":"2026-09-16T09:00:00Z","message":{"id":"a","model":"other-model-9","usage":{"input_tokens":3,"output_tokens":1}}}
{"timestamp":"2026-09-16T09:01:00Z","message":{"id":"b","model":"claude-opus-5","usage":{"input_tokens":4,"output_tokens":2}}}
`))
	require.NoError(t, err)
	{
		why := sum.UnbookableReason()
		require.EqualValuesf(t, "", why, "a transcript naming a model on every turn is bookable, got %q", why)
	}
	rows := sum.Rows("2026-09-16")
	require.Lenf(t, rows, 2, "rows=%+v, want one row per model", rows)
	require.Equalf(t, "claude-opus-5/coordinator", rows[0].Model, "rows=%+v, want one row per model, sorted", rows)
	require.Equalf(t, "other-model-9/coordinator", rows[1].Model, "rows=%+v, want one row per model, sorted", rows)
	assert.Equalf(t, "4", rows[0].Counts.Cell(Input), "each model's row carries its own turns")
	assert.Equalf(t, "3", rows[1].Counts.Cell(Input), "each model's row carries its own turns")

	none, err := ReadClaudeSession(write(`{"timestamp":"2026-09-16T09:00:00Z","message":{"id":"a","usage":{"input_tokens":3,"output_tokens":1}}}
`))
	require.NoError(t, err)
	{
		why := none.UnbookableReason()
		assert.Containsf(t, why, "names no model on any of its 1 turns", "a transcript that names no model is unbookable with a named reason")
	}
	{
		got := none.Rows("2026-09-16")
		assert.Lenf(t, got, 0, "a transcript that names no model has no rows, got %+v", got)
	}

	some, err := ReadClaudeSession(write(`{"timestamp":"2026-09-16T09:00:00Z","message":{"id":"a","model":"m","usage":{"input_tokens":3,"output_tokens":1}}}
{"timestamp":"2026-09-16T09:01:00Z","message":{"id":"b","usage":{"input_tokens":4,"output_tokens":2}}}
`))
	require.NoError(t, err)
	{
		why := some.UnbookableReason()
		assert.Containsf(t, why, "names no model on 1 of its 2 turns", "a turn with no model is not booked under another's")
	}
}
