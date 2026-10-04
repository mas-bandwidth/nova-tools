package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The unit tier of the wait seam: the five functions the per-function coverage table
// showed at 0.0% (printLockScan, repairLog.add, wallClock.Sleep, onNoteWakes,
// onNoteFrame), driven through their own inputs only -- buffers, a temp bus directory of
// note files, a fake clock where the code takes one -- with no sleep on the clock, no
// network, no subprocess and no store.

// TestWaitCoverPrintLockScanPrintsTheCounts pins printLockScan's single line: the counts
// of a scan that classified every process, and the zero-count line a refusal follows when
// the scan classified nothing.
func TestWaitCoverPrintLockScanPrintsTheCounts(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		scan bus.LockScan
		want string
	}{
		{"classified scan", bus.LockScan{Owner: 2, Foreign: 1, Unknown: 3}, "WAIT SCAN index.lock owner=2 foreign=1 unknown=3\n"},
		{"no counts", bus.LockScan{}, "WAIT SCAN index.lock owner=0 foreign=0 unknown=0\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			var b bytes.Buffer
			printLockScan(&b, c.scan)
			assert.Equal(t, c.want, b.String())
		})
	}
}

// TestWaitCoverRepairLogAddIsIdempotent pins repairLog.add: a fresh item appends in
// order, an empty item is refused, a duplicate is refused, and a nil log adds nothing.
func TestWaitCoverRepairLogAddIsIdempotent(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		start []string
		item  string
		want  []string
	}{
		{"fresh append", nil, "index.lock", []string{"index.lock"}},
		{"empty item refused", []string{"index.lock"}, "", []string{"index.lock"}},
		{"duplicate refused", []string{"index.lock"}, "index.lock", []string{"index.lock"}},
		{"second item appended", []string{"index.lock"}, "from-ada/BEAT", []string{"index.lock", "from-ada/BEAT"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			r := &repairLog{pending: c.start}
			r.add(c.item)
			assert.Equal(t, c.want, r.pending)
		})
	}
	t.Run("nil log adds nothing", func(t *testing.T) {
		t.Parallel()
		var r *repairLog
		assert.NotPanics(t, func() { r.add("index.lock") })
	})
}

// TestWaitCoverWallClockSleepAcceptsADurationWithoutBlocking pins wallClock.Sleep: a zero
// or negative duration returns at once, so the seam a waitLoop sleeps on never turns a
// fake-clock test into a real one. A Sleep that blocked would be caught by the test's
// own timeout; the bound under it is only the house-legal witness of that.
func TestWaitCoverWallClockSleepAcceptsADurationWithoutBlocking(t *testing.T) {
	t.Parallel()
	c := wallClock{}
	require.False(t, c.Now().IsZero(), "wallClock.Now is the wall clock")
	for _, d := range []time.Duration{0, -time.Second} {
		t.Run(d.String(), func(t *testing.T) {
			t.Parallel()
			begin := time.Now()
			c.Sleep(d)
			assert.Less(t, time.Since(begin), 10*time.Second, "Sleep(%s) blocked", d)
		})
	}
}

// TestWaitCoverOnNoteWakesFilterFreshToAddressedTo pins onNoteWakes: the new, unheard
// notes addressed To: the reader wake, and a heard note, a Cc: note, a receipt and an
// unreadable file do not.
func TestWaitCoverOnNoteWakesFilterFreshToAddressedTo(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		fresh []bus.OpenEntry
		want  []bus.OpenEntry
	}{
		{
			name:  "new addressed note wakes",
			fresh: []bus.OpenEntry{{ID: "bo-111111111111", Kind: bus.OpenNote, Addr: "to", Path: "from-bo/a.md"}},
			want:  []bus.OpenEntry{{ID: "bo-111111111111", Kind: bus.OpenNote, Addr: "to", Path: "from-bo/a.md"}},
		},
		{
			name: "heard, cc, receipt and unreadable do not wake",
			fresh: []bus.OpenEntry{
				{ID: "bo-222222222222", Kind: bus.OpenNote, Heard: true, Addr: "to", Path: "from-bo/b.md"},
				{ID: "bo-333333333333", Kind: bus.OpenNote, Addr: "cc", Path: "from-bo/c.md"},
				{ID: "bo-444444444444", Kind: bus.OpenReceipt, Addr: "to", Path: "from-bo/d.md"},
				{Path: "from-bo/e.md"},
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, c.want, onNoteWakes(inboxReading{Fresh: c.fresh}))
		})
	}
}

// wakeNote is the note file an onNoteFrame row points its wake at.
func wakeNote(id, body string) string {
	return "From: Bo\nTo: Ada\nDate: 2026-09-09T12:00:00Z\nId: " + id + "\nSubject: the subject\n\n" + body
}

// wakeEntry is the open entry a wake carries.
func wakeEntry(id, path string) bus.OpenEntry {
	return bus.OpenEntry{ID: id, Kind: bus.OpenNote, From: "Bo", Addr: "to",
		Date: "2026-09-09T12:00:00Z", Subject: "the subject", Path: path}
}

// writeWakeFile lays one note down in the temp bus directory a row runs against.
func writeWakeFile(t *testing.T, busDir, path, text string) {
	t.Helper()
	p := filepath.Join(busDir, filepath.FromSlash(path))
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
	require.NoError(t, os.WriteFile(p, []byte(text), 0o644))
}

// TestWaitCoverOnNoteFramePrintsTheWake pins onNoteFrame's main path: one WAIT OK id=
// line naming the first note, then each note's INBOX NOTE and body frame; a second wake
// whose body would spend past the byte budget is left whole for the next wake, and a
// first body already past it prints the OVERSIZE line instead of its body.
func TestWaitCoverOnNoteFramePrintsTheWake(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		files   map[string]string
		wakes   []bus.OpenEntry
		maxByte int64
		want    string
	}{
		{
			name:  "one wake within budget",
			files: map[string]string{"from-bo/one.md": wakeNote("bo-111111111111", "the body text")},
			wakes: []bus.OpenEntry{wakeEntry("bo-111111111111", "from-bo/one.md")},
			want: "WAIT OK id=bo-111111111111 from=Bo path=from-bo/one.md bytes=13\n" +
				"INBOX NOTE id=bo-111111111111 from=Bo addr=to at=2026-09-09T12:00:00Z path=from-bo/one.md: the subject\n" +
				"INBOX BODY id=bo-111111111111 bytes=13\nthe body text\n" +
				"INBOX BODY END id=bo-111111111111\n",
		},
		{
			name: "second wake past the budget is left whole",
			files: map[string]string{
				"from-bo/one.md": wakeNote("bo-111111111111", "short"),
				"from-bo/two.md": wakeNote("bo-222222222222", "a longer body"),
			},
			wakes: []bus.OpenEntry{
				wakeEntry("bo-111111111111", "from-bo/one.md"),
				wakeEntry("bo-222222222222", "from-bo/two.md"),
			},
			maxByte: 6,
			want: "WAIT OK id=bo-111111111111 from=Bo path=from-bo/one.md bytes=5\n" +
				"INBOX NOTE id=bo-111111111111 from=Bo addr=to at=2026-09-09T12:00:00Z path=from-bo/one.md: the subject\n" +
				"INBOX BODY id=bo-111111111111 bytes=5\nshort\n" +
				"INBOX BODY END id=bo-111111111111\n",
		},
		{
			name:    "first wake past the budget prints the oversize line",
			files:   map[string]string{"from-bo/one.md": wakeNote("bo-111111111111", "the body text")},
			wakes:   []bus.OpenEntry{wakeEntry("bo-111111111111", "from-bo/one.md")},
			maxByte: 4,
			want: "WAIT OK id=bo-111111111111 from=Bo path=from-bo/one.md bytes=13\n" +
				"INBOX NOTE id=bo-111111111111 from=Bo addr=to at=2026-09-09T12:00:00Z path=from-bo/one.md: the subject\n" +
				"INBOX BODY OVERSIZE id=bo-111111111111 bytes=13 max-bytes=4 path=from-bo/one.md\n",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			busDir := t.TempDir()
			for path, text := range c.files {
				writeWakeFile(t, busDir, path, text)
			}
			maxByte := c.maxByte
			if maxByte == 0 {
				maxByte = 1024
			}
			frame, err := onNoteFrame(inboxOpts{busDir: busDir, maxNotes: 5, maxBytes: maxByte}, c.wakes)
			require.NoError(t, err)
			assert.Equal(t, c.want, frame)
			assert.NotContains(t, frame, "bo-222222222222", "a wake left whole is not printed")
		})
	}
}

// TestWaitCoverOnNoteFrameRefusesAnUnreadableWake pins onNoteFrame's refusals: a wake
// whose file is gone and a wake whose file does not parse both return the error rather
// than a half frame.
func TestWaitCoverOnNoteFrameRefusesAnUnreadableWake(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		path string
		text string
	}{
		{"missing file", "from-bo/gone.md", ""},
		{"unparsable file", "from-bo/broken.md", "a sentence standing where the header is\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			busDir := t.TempDir()
			if c.text != "" {
				writeWakeFile(t, busDir, c.path, c.text)
			}
			frame, err := onNoteFrame(inboxOpts{busDir: busDir, maxNotes: 5, maxBytes: 1024},
				[]bus.OpenEntry{wakeEntry("bo-111111111111", c.path)})
			require.Error(t, err)
			assert.Empty(t, frame)
		})
	}
}
