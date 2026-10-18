package swarm

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The live sample's cost (2026-10-17): every 5 s per running card, one sqlite3 that
// json_extract-ed every message row of the job, and a read of the whole harness log that
// lowercased every line to count the assistant turns, the square of the card's life. These
// tests hold the incremental reads to the answers of the whole ones, with a fake store
// runner and a log in a temp directory: no sqlite3, no clock.

// TestLogTurnsCountsOnlyWhatTheLogGained: each Count gives what countAssistantLines gives of
// the whole log, reading from the end of the last whole line; an unterminated last line is
// counted and read again; a log that shrank, or that is another file, is counted afresh.
func TestLogTurnsCountsOnlyWhatTheLogGained(t *testing.T) {
	t.Parallel()
	log := filepath.Join(t.TempDir(), "harness-output.log")
	var c LogTurns
	assert.Zero(t, c.Count(log), "no log yet")

	appendLog := func(text string) {
		t.Helper()
		f, err := os.OpenFile(log, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		require.NoError(t, err)
		_, err = f.WriteString(text)
		require.NoError(t, err)
		require.NoError(t, f.Close())
	}
	steps := []string{
		"start\nASSISTANT turn 1\n",
		"tool output\nassistant turn 2\nassis",      // a line cut mid-word
		"tant turn 3\n",                             // ...finished
		"user said assistant\n\nAssistant: x\npart", // a tail with no assistant
		" assistant tail",                           // the tail now carries one
	}
	for i, step := range steps {
		appendLog(step)
		want := countAssistantLines(log)
		assert.Equal(t, want, c.Count(log), "step %d: the whole log's count", i)
		data, err := os.ReadFile(log)
		require.NoError(t, err)
		assert.Equal(t, int64(strings.LastIndexByte(string(data), '\n')+1), c.offset, "step %d: read up to the last whole line, not again", i)
	}
	assert.Equal(t, 6, c.Count(log))

	require.NoError(t, os.WriteFile(log, []byte("assistant\n"), 0o644))
	assert.Equal(t, 1, c.Count(log), "a shorter log is counted from its start")

	require.NoError(t, os.Remove(log))
	assert.Zero(t, c.Count(log), "a log gone counts nothing")
	long := strings.Repeat("assistant ", 30) + "\n"
	require.NoError(t, os.WriteFile(log, []byte(long+long), 0o644))
	assert.Equal(t, 2, c.Count(log), "another file is counted from its start")

	assert.Equal(t, 2, max(c.Count(log), 1), "the card's turns: the log's, or the usage rows where the log has fewer")
}

// TestLogTurnsRecountsFromTheStartEveryTenCounts: a log replaced by one at least as long
// (a file system may hand the new file the old inode) is miscounted only until the next
// count from its start, every logRecountEvery-th Count.
func TestLogTurnsRecountsFromTheStartEveryTenCounts(t *testing.T) {
	t.Parallel()
	log := filepath.Join(t.TempDir(), "harness-output.log")
	require.NoError(t, os.WriteFile(log, []byte("assistant 1\nassistant 2\n"), 0o644))
	var c LogTurns
	require.Equal(t, 2, c.Count(log))
	// the same file rewritten in place, as long, with no assistant line before the offset
	require.NoError(t, os.WriteFile(log, []byte("nothing 1111\nnothing 222\n"), 0o644))
	recounted := 0
	for i := 2; i <= logRecountEvery+1; i++ {
		if n := c.Count(log); n == 0 {
			recounted = i
			break
		}
	}
	assert.Equal(t, logRecountEvery+1, recounted, "the count from the start comes on the Count after the tenth")
}

// fakeStore is a message table the fake runner answers a LiveUsage's statements from: each
// row's rowid, whether it is finished, whether it has tokens, when it was last written, and
// its messageColumns. untimed is a table with no time_updated column.
type fakeStore struct {
	rows    []fakeMessage
	untimed bool
	asked   []string
	answers []int // rows each statement returned
}

type fakeMessage struct {
	id               int64
	finished, tokens bool
	written          int64
	cells            []string
}

var (
	afterRE   = regexp.MustCompile(`WHERE rowid > (-?[0-9]+)`)
	writtenRE = regexp.MustCompile(`COALESCE\(time_updated, time_created\) >= (-?[0-9]+)`)
	inRE      = regexp.MustCompile(`rowid IN \(([0-9,]+)\)`)
)

func (f *fakeStore) query(_ string, sql string, _ time.Duration) ([][]string, error) {
	f.asked = append(f.asked, sql)
	if f.untimed && strings.Contains(sql, "time_updated") {
		return nil, fmt.Errorf("the usage source could not be read: sqlite3: exit status 1: Parse error: no such column: time_updated")
	}
	whole := !strings.Contains(sql, " WHERE ")
	var after, since int64
	in := map[int64]bool{}
	if !whole {
		m, w := afterRE.FindStringSubmatch(sql), writtenRE.FindStringSubmatch(sql)
		if m == nil || w == nil {
			return nil, fmt.Errorf("not a live statement: %s", sql)
		}
		after, _ = strconv.ParseInt(m[1], 10, 64)
		since, _ = strconv.ParseInt(w[1], 10, 64)
		if m := inRE.FindStringSubmatch(sql); m != nil {
			for _, w := range strings.Split(m[1], ",") {
				id, _ := strconv.ParseInt(w, 10, 64)
				in[id] = true
			}
		}
	}
	b := func(v bool) string {
		if v {
			return "1"
		}
		return "0"
	}
	var out [][]string
	for _, r := range f.rows {
		if !whole && r.id <= after && r.written < since && !in[r.id] {
			continue
		}
		row := r.cells
		if row == nil {
			row = make([]string, 2+len(TokenColumns)+1) // a message with no figures: every column NULL
		}
		written := strconv.FormatInt(r.written, 10)
		if f.untimed {
			written = "0"
		}
		out = append(out, append([]string{strconv.FormatInt(r.id, 10), b(r.finished), b(r.tokens), written}, row...))
	}
	f.answers = append(f.answers, len(out))
	return out, nil
}

// whole is what the whole statement (messagesSQL) folds to: every row with tokens.
func (f *fakeStore) whole(t *testing.T) ProviderUsage {
	t.Helper()
	var rows [][]string
	for _, r := range f.rows {
		if r.tokens {
			rows = append(rows, r.cells)
		}
	}
	if len(rows) == 0 {
		return ProviderUsage{Values: map[string]string{}}
	}
	u, err := foldOpenCodeRows(rows)
	require.NoError(t, err)
	return u
}

func cells(model string, input, output int, cost string) []string {
	return []string{"prov", model, strconv.Itoa(input), strconv.Itoa(output), "", "", "", cost}
}

// liveBench is one LiveUsage over a fake store in a temp data home.
type liveBench struct {
	home, db string
	f        *fakeStore
	u        *LiveUsage
}

func newLiveBench(t *testing.T) *liveBench {
	t.Helper()
	home := t.TempDir()
	db := filepath.Join(home, filepath.FromSlash(OpenCodeDB))
	require.NoError(t, os.MkdirAll(filepath.Dir(db), 0o755))
	require.NoError(t, os.WriteFile(db, []byte("a store of sixteen"), 0o644))
	f := &fakeStore{}
	return &liveBench{home: home, db: db, f: f, u: &LiveUsage{query: f.query}}
}

func (b *liveBench) read(t *testing.T) ProviderUsage {
	t.Helper()
	got, err := b.u.Read(b.home, LiveSampleLimit)
	require.NoError(t, err)
	return got
}

// exact reads once and holds the answer to the whole statement's.
func (b *liveBench) exact(t *testing.T, step string) {
	t.Helper()
	assert.Equal(t, b.f.whole(t), b.read(t), "%s: the whole statement's answer", step)
}

func (b *liveBench) last() string { return b.f.asked[len(b.f.asked)-1] }

// TestLiveUsageReadsOnlyWhatMayHaveChanged: the first read is whole; after it a read asks
// only for the rows after the highest rowid, the rows written since the newest write it read
// (less liveMarkSlack), and the unfinished ones, and answers what the whole statement would.
func TestLiveUsageReadsOnlyWhatMayHaveChanged(t *testing.T) {
	t.Parallel()
	b := newLiveBench(t)
	b.exact(t, "an empty store")
	assert.NotContains(t, b.last(), " WHERE ", "the first read is whole")

	b.f.rows = []fakeMessage{
		{id: 1, finished: true, written: 1000_000},                                                    // the user's prompt
		{id: 2, finished: true, tokens: true, written: 1000_100, cells: cells("m1", 100, 10, "0.01")}, // a finished turn
		{id: 3, tokens: true, written: 1000_200, cells: cells("m1", 50, 0, "0.005")},                  // a turn going on
	}
	b.exact(t, "three messages")
	assert.Contains(t, b.last(), "WHERE rowid > 0 OR COALESCE(time_updated, time_created) >= -2000 ORDER BY")

	b.f.rows[2].cells, b.f.rows[2].written = cells("m1", 400, 40, "0.04"), 1010_000 // its step finished, in place
	b.f.rows = append(b.f.rows, fakeMessage{id: 4, finished: true, written: 1010_000})
	b.exact(t, "the unfinished turn grew")
	assert.Contains(t, b.last(), "rowid > 3 OR COALESCE(time_updated, time_created) >= 998200 OR rowid IN (3)")

	b.f.rows[2].finished, b.f.rows[2].written = true, 1020_000
	b.f.rows = append(b.f.rows, fakeMessage{id: 5, finished: true, tokens: true, written: 1020_000, cells: cells("m2", 7, 7, "0.001")})
	b.exact(t, "the turn finished and another came")
	b.f.rows = append(b.f.rows, fakeMessage{id: 6, finished: true, tokens: true, written: 1030_000, cells: cells("m2", 1, 1, "0.001")})
	b.exact(t, "one more")
	assert.Contains(t, b.last(), "rowid > 5 OR COALESCE(time_updated, time_created) >= 1018000 ORDER BY", "nothing is pending")
	assert.Equal(t, 3, b.f.answers[len(b.f.answers)-1], "the new row, and the two written within liveMarkSlack of the newest write read")
	got := b.read(t)
	assert.Equal(t, 1, b.f.answers[len(b.f.answers)-1], "only the newest write read again")
	assert.Equal(t, "m2", got.Values["model"], "the last message's model, as the whole read's")
	assert.Equal(t, 4, got.Turns)
}

// TestLiveUsageReadsAMessageWrittenAfterItsCompletion: a finished message written again later
// (on one machine's store, 1.01 s after its completion) carries a newer time and is read
// again; what was kept for it is replaced, never added to.
func TestLiveUsageReadsAMessageWrittenAfterItsCompletion(t *testing.T) {
	t.Parallel()
	b := newLiveBench(t)
	b.f.rows = []fakeMessage{
		{id: 1, finished: true, tokens: true, written: 1000_000, cells: cells("m1", 100, 10, "0.01")},
		{id: 2, finished: true, tokens: true, written: 1001_000, cells: cells("m1", 5, 5, "0.001")},
	}
	b.exact(t, "two finished")
	b.f.rows[0].cells, b.f.rows[0].written = cells("m1", 900, 90, "0.09"), 1002_010 // rewritten 1.01 s later
	b.exact(t, "the first rewritten after its completion")
	assert.Contains(t, b.last(), "rowid > 2 OR COALESCE(time_updated, time_created) >= 999000")
}

// TestLiveUsageReadsAReusedRowid: the table's rowid is implicit, with no AUTOINCREMENT, so a
// deleted highest row's rowid is given to the next insert; the new message is a new write,
// read and kept in place of the old.
func TestLiveUsageReadsAReusedRowid(t *testing.T) {
	t.Parallel()
	b := newLiveBench(t)
	b.f.rows = []fakeMessage{
		{id: 1, finished: true, tokens: true, written: 1000_000, cells: cells("m1", 100, 10, "0.01")},
		{id: 2, finished: true, tokens: true, written: 1001_000, cells: cells("m1", 5, 5, "0.001")},
	}
	b.exact(t, "two finished")
	b.f.rows[1] = fakeMessage{id: 2, finished: true, tokens: true, written: 1005_000, cells: cells("m2", 700, 70, "0.07")}
	b.exact(t, "row 2 deleted and its rowid reused")
}

// TestLiveUsageReconcilesWithinTenReads: a store replaced under the same name by one whose
// rows are no newer and no higher than those read (the identity check passed: same inode,
// no smaller), or a deleted kept row, can leave the incremental count wrong; every
// liveWholeEvery-th read is whole and answers the store exactly. A deleted row errs high
// until then, never low.
func TestLiveUsageReconcilesWithinTenReads(t *testing.T) {
	t.Parallel()
	b := newLiveBench(t)
	b.f.rows = []fakeMessage{
		{id: 1, finished: true, tokens: true, written: 1000_000, cells: cells("m1", 100, 10, "0.01")},
		{id: 2, finished: true, tokens: true, written: 1100_000, cells: cells("m1", 5, 5, "0.001")},
	}
	b.exact(t, "the first read, whole")
	b.f.rows = b.f.rows[1:] // row 1 deleted
	got := b.read(t)
	assert.Equal(t, "105", got.Values["tokens_in"], "a deleted row stays counted: high, never low")
	b.f.rows = []fakeMessage{{id: 1, finished: true, tokens: true, written: 900_000, cells: cells("m9", 1, 1, "0.0001")}} // a replaced store, all older
	wholes := 0
	for i := 3; i <= liveWholeEvery+1; i++ {
		got = b.read(t)
		if !strings.Contains(b.last(), " WHERE ") {
			wholes++
			assert.Equal(t, i, liveWholeEvery+1, "the whole read is the liveWholeEvery-th after the last")
		}
	}
	assert.Equal(t, 1, wholes)
	assert.Equal(t, b.f.whole(t), got, "reconciled by the whole read")
}

// TestLiveUsageReadsAStoreWithNoTimeWholeEachTime: a table with no time_updated refuses the
// timed statement; the read falls back at once to the whole one, every read, with no failure
// for the sampler to count.
func TestLiveUsageReadsAStoreWithNoTimeWholeEachTime(t *testing.T) {
	t.Parallel()
	b := newLiveBench(t)
	b.f.untimed = true
	b.f.rows = []fakeMessage{{id: 1, finished: true, tokens: true, cells: cells("m1", 100, 10, "0.01")}}
	b.exact(t, "the first read")
	b.f.rows = append(b.f.rows, fakeMessage{id: 2, tokens: true, cells: cells("m1", 3, 3, "0.001")})
	b.exact(t, "the second read")
	assert.Len(t, b.f.asked, 3, "one refused timed statement, then whole statements")
	assert.NotContains(t, b.last(), "time_updated")
	assert.NotContains(t, b.last(), " WHERE ")
}

// TestLiveUsageReadsAReplacedOrVanishedStoreWhole: a store that is smaller than at the last
// read, or gone between two reads, is read whole at the next read.
func TestLiveUsageReadsAReplacedOrVanishedStoreWhole(t *testing.T) {
	t.Parallel()
	b := newLiveBench(t)
	b.f.rows = []fakeMessage{{id: 1, finished: true, tokens: true, written: 1000_000, cells: cells("m1", 100, 10, "0.01")}}
	b.exact(t, "first")
	b.exact(t, "second, incremental")
	require.Contains(t, b.last(), " WHERE ")
	require.NoError(t, os.Remove(b.db))
	require.NoError(t, os.WriteFile(b.db, []byte("x"), 0o644))
	b.exact(t, "a smaller store")
	assert.NotContains(t, b.last(), " WHERE ")

	require.NoError(t, os.Remove(b.db))
	got := b.read(t)
	assert.False(t, got.Observed, "no store is an absence")
	require.NoError(t, os.WriteFile(b.db, []byte("x"), 0o644))
	b.exact(t, "the store back")
	assert.NotContains(t, b.last(), " WHERE ")
}

// TestTheLiveStatementsAskTheStoreWhatTheFakeAnswers pins the statements' shape.
func TestTheLiveStatementsAskTheStoreWhatTheFakeAnswers(t *testing.T) {
	t.Parallel()
	sql := liveSQL(9, 5000, map[int64]bool{4: true, 2: true})
	assert.Contains(t, sql, "WHERE rowid > 9 OR COALESCE(time_updated, time_created) >= 3000 OR rowid IN (2,4) ORDER BY rowid")
	assert.Contains(t, sql, "json_extract(data, '$.time.completed') IS NOT NULL")
	assert.Contains(t, sql, messageColumns)
	assert.NotContains(t, liveWholeSQL(true), " WHERE ")
	assert.NotContains(t, liveWholeSQL(false), "time_")
	assert.Equal(t, "SELECT "+messageColumns+"FROM message WHERE json_extract(data, '$.tokens') IS NOT NULL", messagesSQL)
}
