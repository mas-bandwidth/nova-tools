package swarm

import (
	"cmp"
	"fmt"
	"os"
	"path/filepath"
	"slices"
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
		assert.Equal(t, int64(len(data)), c.offset, "step %d: read to the end, and not again", i)
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

// TestLogTurnsStreamsTheLogAndCarriesAnOpenLine (the second cold read of PR 5953): the
// first count read the whole log at once, and an unterminated tail over 1 MiB was counted as
// a line of its own, so its rest could be counted again and max_turns fire early. The log is
// streamed in logReadChunk pieces, a word cut across two pieces is still found, and a line
// open across Counts is carried and counted once.
func TestLogTurnsStreamsTheLogAndCarriesAnOpenLine(t *testing.T) {
	t.Parallel()
	log := filepath.Join(t.TempDir(), "harness-output.log")
	// a log three chunks long whose second line has "assistant" across the first chunk's end
	body := strings.Repeat("x", logReadChunk-5) + "\n" + "ASSISTANT turn across the chunk boundary\n" +
		strings.Repeat("filler line\n", 2*logReadChunk/12) + "assistant at the end\n"
	require.NoError(t, os.WriteFile(log, []byte(body), 0o644))
	var c LogTurns
	assert.Equal(t, countAssistantLines(log), c.Count(log), "the whole log's count, streamed")
	assert.Equal(t, 2, c.Count(log))

	// an open line of 2 MiB that shows the word, then its rest with the word again
	open := filepath.Join(t.TempDir(), "harness-output.log")
	require.NoError(t, os.WriteFile(open, []byte("assistant 1\n"+strings.Repeat("y", 1<<20)+" assistant "+strings.Repeat("y", 1<<20)), 0o644))
	var o LogTurns
	assert.Equal(t, 2, o.Count(open), "the open line counts while it is open")
	assert.Equal(t, 2, o.Count(open), "and not again on the next Count")
	f, err := os.OpenFile(open, os.O_APPEND|os.O_WRONLY, 0o644)
	require.NoError(t, err)
	_, err = f.WriteString(" its rest, assistant again\nplain\n")
	require.NoError(t, err)
	require.NoError(t, f.Close())
	assert.Equal(t, 2, o.Count(open), "the open line closed: one line, counted once")
}

// fakeStore answers a LiveUsage's statements by evaluating them over its table
// (livesql_fake_test.go): the predicate the SQL states is the one applied, so the SQL cannot
// drift from what the tests mean without a test failing.
type fakeStore struct {
	table   sqlTable
	asked   []string
	answers []int // rows each statement returned
}

func (f *fakeStore) query(_ string, sql string, _ time.Duration) ([][]string, error) {
	f.asked = append(f.asked, sql)
	rows, err := f.table.run(sql)
	if err != nil {
		return nil, fmt.Errorf("the usage source could not be read: sqlite3: exit status 1: %v", err)
	}
	f.answers = append(f.answers, len(rows))
	return rows, nil
}

// whole is what the whole statement (messagesSQL, the final read's), evaluated by the same
// fake, folds to.
func (f *fakeStore) whole(t *testing.T) ProviderUsage {
	t.Helper()
	rows, err := f.table.run(messagesSQL)
	require.NoError(t, err)
	if len(rows) == 0 {
		return ProviderUsage{Values: map[string]string{}}
	}
	u, err := foldOpenCodeRows(rows)
	require.NoError(t, err)
	return u
}

// set puts message m in the table at its rowid, in place of any there.
func (f *fakeStore) set(m sqlMessage) {
	for i, r := range f.table.rows {
		if r.rowid == m.rowid {
			f.table.rows[i] = m
			return
		}
	}
	f.table.rows = append(f.table.rows, m)
	slices.SortFunc(f.table.rows, func(a, b sqlMessage) int { return cmp.Compare(a.rowid, b.rowid) })
}

func (f *fakeStore) remove(rowid int64) {
	f.table.rows = slices.DeleteFunc(f.table.rows, func(m sqlMessage) bool { return m.rowid == rowid })
}

// user is a user's message: no tokens.
func user(rowid, at int64) sqlMessage {
	return sqlMessage{rowid: rowid, created: at, data: `{"role":"user","time":{"created":` + strconv.FormatInt(at, 10) + `}}`}
}

// turn is an assistant's message with its tokens and cost, completed when done; written is
// its time_updated (nil: NULL).
func turn(rowid, at int64, written *int64, done bool, model string, input, output int, cost string) sqlMessage {
	completed := ""
	if done {
		completed = `,"completed":` + strconv.FormatInt(at+1, 10)
	}
	data := fmt.Sprintf(`{"role":"assistant","providerID":"prov","modelID":%q,"time":{"created":%d%s},`+
		`"tokens":{"input":%d,"output":%d,"reasoning":0,"cache":{"read":0,"write":0}},"cost":%s}`,
		model, at, completed, input, output, cost)
	return sqlMessage{rowid: rowid, created: at, updated: written, data: data}
}

func ms(n int64) *int64 { return &n }

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

func (b *liveBench) lastAnswer() int { return b.f.answers[len(b.f.answers)-1] }

// TestLiveUsageReadsOnlyWhatMayHaveChanged: the first read is whole; after it a read asks
// only for the rows after the highest rowid, the rows written since the newest write it read
// (less liveMarkSlack), and the unfinished ones, and answers what the whole statement would.
func TestLiveUsageReadsOnlyWhatMayHaveChanged(t *testing.T) {
	t.Parallel()
	b := newLiveBench(t)
	b.exact(t, "an empty store")
	assert.NotContains(t, b.last(), " WHERE ", "the first read is whole")

	b.f.set(user(1, 990_000))
	b.f.set(turn(2, 995_000, ms(995_001), true, "m1", 100, 10, "0.01"))
	b.f.set(turn(3, 1000_200, nil, false, "m1", 50, 0, "0.005")) // going on, time_updated NULL
	b.exact(t, "three messages")
	assert.Contains(t, b.last(), "WHERE rowid > 0 OR COALESCE(time_updated, time_created) >= -2000 ORDER BY")

	b.f.set(turn(3, 1000_200, ms(1010_000), false, "m1", 400, 40, "0.04")) // its step finished, in place
	b.f.set(user(4, 1010_000))
	b.exact(t, "the unfinished turn grew")
	assert.Contains(t, b.last(), "rowid > 3 OR COALESCE(time_updated, time_created) >= 998200 OR rowid IN (3)")
	assert.Equal(t, 2, b.lastAnswer(), "two rows read, not four")

	b.f.set(turn(3, 1000_200, ms(1020_000), true, "m1", 400, 40, "0.04"))
	b.f.set(turn(5, 1020_000, nil, true, "m2", 7, 7, "0.001"))
	b.exact(t, "the turn finished and another came")
	b.f.set(turn(6, 1030_000, nil, true, "m2", 1, 1, "0.001"))
	b.exact(t, "one more")
	assert.Contains(t, b.last(), "rowid > 5 OR COALESCE(time_updated, time_created) >= 1018000 ORDER BY", "nothing is pending")
	assert.Equal(t, 3, b.lastAnswer(), "the new row, and the two written within liveMarkSlack of the newest write read")
	got := b.read(t)
	assert.Equal(t, 1, b.lastAnswer(), "only the newest write read again")
	assert.Equal(t, "m2", got.Values["model"], "the last message's model, as the whole read's")
	assert.Equal(t, 4, got.Turns)
}

// TestLiveUsageReadsAMessageWrittenAfterItsCompletion: a finished message written again later
// (on one machine's store, 1,013 ms after its completion) carries a newer time_updated and
// is read again; what was kept for it is replaced, never added to.
func TestLiveUsageReadsAMessageWrittenAfterItsCompletion(t *testing.T) {
	t.Parallel()
	b := newLiveBench(t)
	b.f.set(turn(1, 1000_000, ms(1000_001), true, "m1", 100, 10, "0.01"))
	b.f.set(turn(2, 1001_000, ms(1001_001), true, "m1", 5, 5, "0.001"))
	b.exact(t, "two finished")
	b.f.set(turn(1, 1000_000, ms(1002_014), true, "m1", 900, 90, "0.09")) // written again 1,013 ms after
	b.exact(t, "the first rewritten after its completion")
	assert.Contains(t, b.last(), "rowid > 2 OR COALESCE(time_updated, time_created) >= 999001")
}

// TestLiveUsageReadsAReusedRowid: the table's rowid is implicit, with no AUTOINCREMENT, so a
// deleted highest row's rowid is given to the next insert; the new message is a new write,
// read and kept in place of the old.
func TestLiveUsageReadsAReusedRowid(t *testing.T) {
	t.Parallel()
	b := newLiveBench(t)
	b.f.set(turn(1, 1000_000, nil, true, "m1", 100, 10, "0.01"))
	b.f.set(turn(2, 1001_000, nil, true, "m1", 5, 5, "0.001"))
	b.exact(t, "two finished")
	b.f.remove(2)
	b.f.set(turn(2, 1005_000, nil, true, "m2", 700, 70, "0.07"))
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
	b.f.set(turn(1, 1000_000, nil, true, "m1", 100, 10, "0.01"))
	b.f.set(turn(2, 1100_000, nil, true, "m1", 5, 5, "0.001"))
	b.exact(t, "the first read, whole")
	b.f.remove(1)
	got := b.read(t)
	assert.Equal(t, "105", got.Values["tokens_in"], "a deleted row stays counted: high, never low")
	b.f.table.rows = nil
	b.f.set(turn(1, 900_000, nil, true, "m9", 1, 1, "0.0001")) // a replaced store, all older
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
	b.f.table.untimed = true
	b.f.set(turn(1, 1000_000, nil, true, "m1", 100, 10, "0.01"))
	b.exact(t, "the first read")
	b.f.set(turn(2, 1001_000, nil, false, "m1", 3, 3, "0.001"))
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
	b.f.set(turn(1, 1000_000, nil, true, "m1", 100, 10, "0.01"))
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

// TestTheLiveStatementsMeanWhatTheReadAssumes evaluates the statements themselves through the
// fake (livesql_fake_test.go) against messages built to tell each clause apart: finished is
// "not the assistant's, or completed"; tokens is "a tokens object"; written is time_updated,
// else time_created; and the filter takes exactly the rows after seen, written at or after
// the mark less liveMarkSlack, or pending. A change to any of them fails here.
func TestTheLiveStatementsMeanWhatTheReadAssumes(t *testing.T) {
	t.Parallel()
	tbl := sqlTable{rows: []sqlMessage{
		user(1, 100), // finished, no tokens, old
		turn(2, 200, nil, true, "m", 1, 1, "0.1"),           // finished, old, time_updated NULL
		turn(3, 300, nil, false, "m", 1, 1, "0.1"),          // unfinished, old: pending only
		turn(4, 400, ms(9_000), true, "m", 1, 1, "0.1"),     // rewritten recently: by time_updated
		{rowid: 5, created: 500, data: `{"role":"system"}`}, // another role, finished
		turn(6, 600, nil, true, "m", 1, 1, "0.1"),           // old
		turn(11, 1100, nil, true, "m", 1, 1, "0.1"),         // after seen
		{rowid: 12, created: 1200, data: `{"time":{}}`},     // no role at all: finished, no tokens
	}}
	rows, err := tbl.run(liveSQL(10, 10_000, map[int64]bool{3: true}))
	require.NoError(t, err)
	var got []string
	for _, r := range rows {
		got = append(got, strings.Join(r[:4], " "))
	}
	assert.Equal(t, []string{"3 0 1 300", "4 1 1 9000", "11 1 1 1100", "12 1 0 1200"}, got,
		"pending 3, 4 written at 9000 >= 10000-2000, 11 and 12 after rowid 10")

	whole, err := tbl.run(liveWholeSQL(true))
	require.NoError(t, err)
	assert.Len(t, whole, len(tbl.rows), "a whole read takes every row")
	assert.Equal(t, "1 1 0 100", strings.Join(whole[0][:4], " "), "a user's message: finished, no tokens")
	assert.Equal(t, "5 1 0 500", strings.Join(whole[4][:4], " "), "another role: finished")
	assert.Equal(t, 4+2+len(TokenColumns)+1, len(whole[1]), "the four live columns, then messageColumns")
	assert.Equal(t, []string{"prov", "m", "1", "1", "0", "0", "0", "0.1"}, whole[1][4:], "messageColumns read from the JSON")

	tbl.untimed = true
	_, err = tbl.run(liveWholeSQL(true))
	require.ErrorContains(t, err, "no such column", "a table with no time_updated refuses the timed statement")
	untimed, err := tbl.run(liveWholeSQL(false))
	require.NoError(t, err)
	assert.Equal(t, "2 1 1 0", strings.Join(untimed[1][:4], " "), "untimed: written is 0")

	final, err := tbl.run(messagesSQL)
	require.NoError(t, err)
	assert.Len(t, final, 5, "the final read's statement takes the rows with tokens")
	assert.Equal(t, "SELECT "+messageColumns+"FROM message WHERE json_extract(data, '$.tokens') IS NOT NULL", messagesSQL)
}
