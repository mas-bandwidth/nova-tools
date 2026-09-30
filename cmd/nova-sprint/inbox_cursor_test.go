package main

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	spverbs "github.com/mas-bandwidth/nova-tools/internal/sprint/verbs"
)

// The coordinator's loop from the command alone (errata 3 amendment 8): inbox
// --wait, inbox --read (act on all), inbox --wait, with the cursor on the
// sprint between the calls. Each command opens its own client, as a process
// does, so nothing but the store carries the cursor from one to the next.

// streamRig is the notes stream the command's wait blocks on, mirrored from
// the twin's log line by line, and a clock its blocks advance: a block that
// runs out spends its time on the test's clock and returns at once.
type streamRig struct {
	ms       *spverbs.MemStream
	mirrored int
}

func newStreamRig(na *npApp) *streamRig {
	r := &streamRig{ms: spverbs.NewMemStream()}
	r.ms.After = func(d time.Duration) <-chan time.Time {
		na.mu.Lock()
		na.now = na.now.Add(d)
		now := na.now
		na.mu.Unlock()
		ch := make(chan time.Time, 1)
		ch <- now
		return ch
	}
	na.a.noteStream = func(string) (spverbs.NoteStream, func() error, error) { return r.ms, nil, nil }
	return r
}

// mirror appends the log's lines not yet on the stream, as the store's XADD
// of each.
func (r *streamRig) mirror(t *testing.T, na *npApp) {
	t.Helper()
	lines := na.log.Stored(npPrefix, "0")
	for ; r.mirrored < len(lines); r.mirrored++ {
		l := lines[r.mirrored]
		if err := r.ms.Append(npPrefix+"sprint:log@0", string(l.Seq)+"-0", map[string]string{"n": l.N, "d": l.D}); err != nil {
			t.Fatal(err)
		}
	}
}

// field is the value of key=value on the line of out that holds it.
func field(t *testing.T, out, key string) string {
	t.Helper()
	for _, f := range strings.Fields(out) {
		if v, ok := strings.CutPrefix(f, key+"="); ok {
			return v
		}
	}
	t.Fatalf("no %s= in:\n%s", key, out)
	return ""
}

// TestCoordinatorLoopFromTheCommand: three ticks, each after a verb's
// judgment, and the loop the command gives the coordinator over them: each
// wait wakes on its tick's end from the stored cursor the read before it moved,
// each read shows that tick's judgment and moves the cursor to the last line
// the log holds, and a wait after the last read finds nothing. A caller that
// keeps its own cursor reads the same batches with --after, and the stored
// cursor is where the coordinator's reads left it.
func TestCoordinatorLoopFromTheCommand(t *testing.T) {
	t.Parallel()
	na := newNPApp(t)
	r := newStreamRig(na)
	for _, l := range npTicking {
		na.ok(l)
	}
	na.ok("stop")
	na.ok("start")
	na.ok("tick")
	r.mirror(t, na)
	// The first wait, with nothing raised since the ticks: the loop's first
	// read takes what is there, and the cursor then stands at the last line.
	if out := na.ok("inbox --wait --timeout 5m"); !strings.Contains(out, "INBOX WAIT") {
		t.Fatalf("the first wait:\n%s", out)
	}
	na.ok("inbox --read")
	stored := func() string {
		t.Helper()
		_, out, _ := na.do("inbox --json")
		return field2(t, out, "cursor")
	}
	own := uint64(0) // the caller that keeps its own cursor
	for i := 1; i <= 3; i++ {
		card := "s1-" + strconv.Itoa(i)
		na.ok("ci " + card + " --red")
		na.ok("tick")
		r.mirror(t, na)
		woke := na.ok("inbox --wait --timeout 5m")
		if !strings.Contains(woke, "INBOX WAIT tick-end=") || field(t, woke, "cursor") != stored() {
			t.Fatalf("tick %d's wait (stored cursor %s):\n%s", i, stored(), woke)
		}
		before := stored()
		read := na.ok("inbox --read")
		if !strings.Contains(read, "JUDGMENT") || !strings.Contains(read, "INBOX CURSOR "+field(t, read, "last")+" (was "+before+")") {
			t.Fatalf("tick %d's read (cursor was %s):\n%s", i, before, read)
		}
		if stored() != field(t, read, "last") {
			t.Fatalf("after tick %d's read the stored cursor is %s, want %s", i, stored(), field(t, read, "last"))
		}
		// The caller with its own cursor reads the same batch from where its
		// last left it, and keeps the stored cursor out of it.
		o := na.ok("inbox --after " + strconv.FormatUint(own, 10))
		if field(t, o, "cursor") != strconv.FormatUint(own, 10) || field(t, o, "last") != field(t, read, "last") {
			t.Fatalf("tick %d's inbox --after %d:\n%s\nwant last %s", i, own, o, field(t, read, "last"))
		}
		own, _ = strconv.ParseUint(field(t, read, "last"), 10, 64)
	}
	// Nothing to read, no wake: the wait after the last read runs out.
	if out := na.ok("inbox --wait --timeout 5m"); !strings.Contains(out, "INBOX WAIT nothing") {
		t.Fatalf("a wait after the last read:\n%s", out)
	}
	// A caller that keeps its own cursor waits from it: the tick-end of the
	// first loop is still after cursor 0.
	if out := na.ok("inbox --wait --after 0 --timeout 5m"); !strings.Contains(out, "INBOX WAIT tick-end=") || field(t, out, "cursor") != "0" {
		t.Fatalf("a wait with --after 0:\n%s", out)
	}
}

// field2 is the string value of a top-level key of a JSON line.
func field2(t *testing.T, line, key string) string {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(line), &m); err != nil {
		t.Fatalf("%v in %q", err, line)
	}
	s, _ := m[key].(string)
	return s
}
