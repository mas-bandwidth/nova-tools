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

// fakeStore is a message table the fake runner answers liveSQL from: each row's rowid,
// whether it is finished, whether it has tokens, and its messageColumns.
type fakeStore struct {
	rows    []fakeMessage
	asked   []string
	answers []int // rows each read returned
}

type fakeMessage struct {
	id               int64
	finished, tokens bool
	cells            []string
}

var (
	afterRE = regexp.MustCompile(`rowid > ([0-9]+)`)
	inRE    = regexp.MustCompile(`rowid IN \(([0-9,]+)\)`)
)

func (f *fakeStore) query(_ string, sql string, _ time.Duration) ([][]string, error) {
	f.asked = append(f.asked, sql)
	m := afterRE.FindStringSubmatch(sql)
	if m == nil {
		return nil, fmt.Errorf("not a live statement: %s", sql)
	}
	after, _ := strconv.ParseInt(m[1], 10, 64)
	in := map[int64]bool{}
	if m := inRE.FindStringSubmatch(sql); m != nil {
		for _, w := range strings.Split(m[1], ",") {
			id, _ := strconv.ParseInt(w, 10, 64)
			in[id] = true
		}
	}
	var out [][]string
	for _, r := range f.rows {
		if r.id <= after && !in[r.id] {
			continue
		}
		b := func(v bool) string {
			if v {
				return "1"
			}
			return "0"
		}
		row := r.cells
		if row == nil {
			row = make([]string, 2+len(TokenColumns)+1) // a message with no figures: every column NULL
		}
		out = append(out, append([]string{strconv.FormatInt(r.id, 10), b(r.finished), b(r.tokens)}, row...))
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

// TestLiveUsageReadsOnlyNewAndUnfinishedMessages: every read answers what the whole
// statement would; after the first it asks only for the rows after the highest it saw and
// for the ones it saw unfinished, so a finished message is read once; an unfinished one
// whose tokens grow in place is read again until it is finished; a store that is another
// file is read from its start.
func TestLiveUsageReadsOnlyNewAndUnfinishedMessages(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	db := filepath.Join(home, filepath.FromSlash(OpenCodeDB))
	require.NoError(t, os.MkdirAll(filepath.Dir(db), 0o755))
	require.NoError(t, os.WriteFile(db, nil, 0o644))
	f := &fakeStore{}
	u := &LiveUsage{query: f.query}
	read := func(step string) {
		t.Helper()
		got, err := u.Read(home, LiveSampleLimit)
		require.NoError(t, err, step)
		assert.Equal(t, f.whole(t), got, "%s: the whole statement's answer", step)
	}

	read("an empty store")
	f.rows = []fakeMessage{
		{id: 1, finished: true}, // the user's prompt
		{id: 2, finished: true, tokens: true, cells: cells("m1", 100, 10, "0.01")}, // a finished turn
		{id: 3, tokens: true, cells: cells("m1", 50, 0, "0.005")},                  // a turn going on
	}
	read("three messages")
	assert.Contains(t, f.asked[1], "rowid > 0", "the first read with rows asks from the start")

	f.rows[2].cells = cells("m1", 400, 40, "0.04") // its step finished: written in place
	f.rows = append(f.rows, fakeMessage{id: 4, finished: true})
	read("the unfinished turn grew")
	assert.Contains(t, f.asked[2], "rowid > 3 OR rowid IN (3)", "after the highest seen, and the unfinished one")
	assert.Equal(t, 2, f.answers[2], "two rows read, not four")

	f.rows[2].finished = true
	f.rows = append(f.rows, fakeMessage{id: 5, finished: true, tokens: true, cells: cells("m2", 7, 7, "0.001")})
	read("the turn finished and another came")
	f.rows = append(f.rows, fakeMessage{id: 6, finished: true, tokens: true, cells: cells("m2", 1, 1, "0.001")})
	read("one more")
	assert.Contains(t, f.asked[4], "rowid > 5 ORDER BY", "nothing is pending: only what is new")
	assert.Equal(t, 1, f.answers[4], "one new row read")
	got, err := u.Read(home, LiveSampleLimit)
	require.NoError(t, err)
	assert.Equal(t, 0, f.answers[5], "nothing new, nothing read")
	assert.Equal(t, "m2", got.Values["model"], "the last message's model, as the whole read's")
	assert.Equal(t, 4, got.Turns)

	// the store replaced by another file: read again from its start
	require.NoError(t, os.Remove(db))
	require.NoError(t, os.WriteFile(db, []byte("x"), 0o644))
	f.rows = f.rows[:2]
	read("another store")
	assert.Contains(t, f.asked[len(f.asked)-1], "rowid > 0 ORDER BY")

	// the statement asks the store what the fake answers from
	sql := liveSQL(9, map[int64]bool{4: true, 2: true})
	assert.Contains(t, sql, "WHERE rowid > 9 OR rowid IN (2,4) ORDER BY rowid")
	assert.Contains(t, sql, "json_extract(data, '$.time.completed') IS NOT NULL")
	assert.Contains(t, sql, messageColumns)
	assert.Equal(t, "SELECT "+messageColumns+"FROM message WHERE json_extract(data, '$.tokens') IS NOT NULL", messagesSQL)
}
