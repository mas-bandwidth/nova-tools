package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/mas-bandwidth/nova-tools/internal/friend"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/tool"
)

// watch is the coordinator's wake. It waits on the coordinator's stream, the
// wake file in the state directory, and events, and it saves the cursor it
// reached so the next run misses nothing (docs/SPEC-FRIEND.md, Watch). The
// decision over one batch is bus.WaitPick. The blocking read is the store's.
// The clock and the wake file are the world's, so a test opens no socket and
// waits no real time.

// watchTick is how long one blocking read waits: the wake file is read once
// a tick, so a line appended to it returns within one (docs/SPEC-FRIEND.md, Watch).
const watchTick = time.Second

// watchLineMax bounds one read of a wake file. A harness appends one line a
// message, and one line a reader can use is far under this.
const watchLineMax = 64 << 10

// watchSkips are the subject prefixes a watch drops. The match without case,
// and the cursor a skipped entry still moves, are bus.WaitPick's
// (docs/SPEC-FRIEND.md, Watch).
var watchSkips = []string{"ping", "pong", "daemon-pong", "keepalive"}

// watchDetail is the verb's help. docs/CLI.md carries this same text
// (docs/SPEC-FRIEND.md, Watch).
const watchDetail = `Watches the coordinator's own stream, the wake file in the state directory, and events. A background session is re-invoked by this verb's exit. The line to run again, from the cursor this run saved, is: nova-friend watch --as <coordinator>
It waits on three things. The stream is the coordinator's (--as). The wake file is <state-dir>/<coordinator>.wake, the file a harness appends one line to; a line appended after this run armed ends the watch. An event is a bus message on that stream whose subject starts with event:, from any sender (event: machine stopped unasked).
Messages from --as itself are skipped. So is a subject that starts with ping, pong, daemon-pong or keepalive, matched without case. A skipped entry moves the cursor and is not printed. Which entries count, and the cursor past them, is the bus wait's decision, so that rule lives in one place: at most 5 lines, and the entries after them stay for the next run.
The cursor is saved in the state directory after each run, by a write and then a rename: the stream entry id and the wake file's byte offset. The next run starts there and misses nothing between runs. A first run arms at the stream's last id (0-0 when the stream is empty) and at the wake file's current size.
It prints, in the order it read them, then the closing line, and exits 0:
WATCH MESSAGE id=<id> from=<f> subject=<s>
WATCH EVENT id=<id> from=<f> subject=<s>
WATCH WAKE line=<text>
WATCH OK after=<cursor>
A wake line ends the watch on its own. Past --timeout (a Go duration; 0, the default, is for ever) it prints WATCH NONE waited=<duration> on standard error and exits 1. --state-dir is where the cursor and the wake file live (default: ~/.nova-friend/<coordinator>). --redis is the bus store (default: NOVA_BUS_REDIS). --json prints one object on stdout, for a result and for a timeout alike: {"status":"ok","word":"OK|NONE","after":"<cursor>","waited":"<duration>","messages":[{"id":"<id>","from":"<f>","subject":"<s>"}],"events":[{"id":"<id>","from":"<f>","subject":"<s>"}],"wake":{"line":"<text>"}} (waited and wake are left out when they hold nothing; messages and events are always arrays). Exit 2 when it could not run: a flag, a name that is not on the roster, a store that did not answer, or a cursor that cannot be read or saved. Nothing is printed when the cursor cannot be saved, so a retry still sees what this run saw.
example: nova-friend watch --as ada --timeout 1s`

// watchVerb is the verb nova-friend watch (docs/SPEC-FRIEND.md, Watch).
func watchVerb(w world) tool.Verb {
	return tool.Verb{
		Name:      "watch",
		Usage:     "watch --as <coordinator> [--timeout <duration>] [--state-dir <d>] [--redis <addr>] [--json]",
		Example:   "", // the banner's example block runs nothing that waits; -h carries the example
		Effect:    "inspection: reads the coordinator's stream and wake file and sends nothing; it records the cursor the next run starts from",
		ExitTable: "0 WATCH OK, after a message, an event or a wake line; 1 WATCH NONE, the timeout ran out; 2 could not run (a flag, an input, a store that did not answer, a cursor that cannot be saved).",
		Detail:    watchDetail,
		Flags: func(f *tool.Flags) {
			f.Required("as", "your name, the coordinator whose stream and wake file are watched")
			f.Duration("timeout", 0, "how long to wait before WATCH NONE, a Go duration (1s, 2m); 0 is for ever")
			f.String("state-dir", "", "where the cursor and the wake file live (default: ~/.nova-friend/<coordinator>)")
			f.String("redis", w.getenv(RedisEnv), "the bus store's Redis address, host:port (default: "+RedisEnv+")")
			f.Check(func(c *tool.Call) {
				if c.Dur("timeout") < 0 {
					c.Problem("--timeout wants a duration of at least 0, 0 for ever (a negative wait is no wait)")
				}
			})
		},
		Run: w.watch,
	}
}

// watchItem is one line a watch prints: a message or an event.
type watchItem struct {
	kind    string // message or event
	id      string
	from    string
	subject string
}

// watchDone is one watch's end: messages and events, or one wake line, or
// the timeout (docs/SPEC-FRIEND.md, Watch).
type watchDone struct {
	word   string
	after  string
	waited time.Duration
	items  []watchItem
	wake   string
	exit   int
}

// watchJSONItem is one message or event of a watch's JSON.
type watchJSONItem struct {
	ID      string `json:"id"`
	From    string `json:"from"`
	Subject string `json:"subject"`
}

// watchJSONWake is the wake line of a watch's JSON.
type watchJSONWake struct {
	Line string `json:"line"`
}

// watchJSON is the --json rendering of one watch: one object, printed when
// the watch ends (docs/SPEC-FRIEND.md, Watch).
type watchJSON struct {
	Status   string          `json:"status"`
	Word     string          `json:"word"`
	After    string          `json:"after"`
	Waited   string          `json:"waited,omitempty"`
	Messages []watchJSONItem `json:"messages"`
	Events   []watchJSONItem `json:"events"`
	Wake     *watchJSONWake  `json:"wake,omitempty"`
}

// object is the JSON of one watch. messages and events are arrays even when
// empty, and waited and wake are left out when they hold nothing
// (docs/SPEC-FRIEND.md, Watch).
func (d watchDone) object() watchJSON {
	o := watchJSON{
		Status:   "ok",
		Word:     d.word,
		After:    d.after,
		Messages: []watchJSONItem{},
		Events:   []watchJSONItem{},
	}
	if d.word == "NONE" {
		o.Waited = d.waited.String()
	}
	for _, it := range d.items {
		row := watchJSONItem{ID: it.id, From: it.from, Subject: oneline.Escape(it.subject)}
		if it.kind == "event" {
			o.Events = append(o.Events, row)
		} else {
			o.Messages = append(o.Messages, row)
		}
	}
	if d.wake != "" {
		o.Wake = &watchJSONWake{Line: oneline.Escape(d.wake)}
	}
	return o
}

// watchFileError is a wake file that cannot be read. It is a refusal, and
// the store's errors stay the store's (docs/SPEC-FRIEND.md, Watch).
type watchFileError struct{ err error }

func (e *watchFileError) Error() string { return "the wake file cannot be read: " + e.err.Error() }
func (e *watchFileError) Unwrap() error { return e.err }

// watchRun is the watch apart from its transport: the clock, the blocking
// read and the wake file are handed in (docs/SPEC-FRIEND.md, Watch).
type watchRun struct {
	me      string
	cursor  string
	offset  int64
	timeout time.Duration
	now     func() time.Time
	wake    string
	read    func(ctx context.Context, stream, after string, block time.Duration, count int) ([]bus.Entry, error)
	line    func(path string, from int64) (string, int64, error)
}

// run waits until a message, an event or a wake line arrives, or the timeout
// runs out. A wake line is looked at before each blocking read. The block is
// one tick, or the time still left when that is shorter. A remainder under a
// millisecond is the timeout (docs/SPEC-FRIEND.md, Watch).
func (r *watchRun) run(ctx context.Context) (watchDone, error) {
	start := r.now()
	for {
		if err := ctx.Err(); err != nil {
			return watchDone{}, err
		}
		if r.wake != "" {
			text, end, err := r.line(r.wake, r.offset)
			if err != nil {
				return watchDone{}, &watchFileError{err}
			}
			r.offset = end
			if text != "" {
				return watchDone{word: "OK", after: r.cursor, wake: text, exit: 0}, nil
			}
		}
		block := watchTick
		if r.timeout > 0 {
			left := r.timeout - r.now().Sub(start)
			if left < time.Millisecond {
				return watchDone{word: "NONE", after: r.cursor, waited: r.timeout, exit: 1}, nil
			}
			if left < block {
				block = left
			}
		}
		got, err := r.read(ctx, bus.StreamOf(r.me), r.cursor, block, bus.WaitRead)
		if err != nil {
			return watchDone{}, err
		}
		kept, after := bus.WaitPick(got, r.me, watchSkips)
		if after != "" {
			r.cursor = after
		}
		if len(kept) == 0 {
			continue
		}
		items := make([]watchItem, 0, len(kept))
		for _, e := range kept {
			m := e.Message()
			items = append(items, watchItem{kind: watchKind(m.Subject), id: m.ID, from: m.From, subject: m.Subject})
		}
		return watchDone{word: "OK", after: r.cursor, items: items, exit: 0}, nil
	}
}

// watchKind is event when the subject starts with event:, and message
// otherwise (docs/SPEC-FRIEND.md, Watch).
func watchKind(subject string) string {
	if strings.HasPrefix(strings.ToLower(subject), "event:") {
		return "event"
	}
	return "message"
}

// watchFileSize is a wake file's end when a watch arms with no saved cursor:
// 0 when the file is not there yet, so its first line, whenever it appears,
// is past the start (docs/SPEC-FRIEND.md, Watch).
func watchFileSize(path string) (int64, error) {
	st, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return st.Size(), nil
}

// watchFileLine reads a wake file from an offset and answers its first line
// past that offset and the offset just past the line's newline. An empty
// answer and the same offset mean no newline is there yet: a fragment is not
// a line. A file that is not there is no wake yet (docs/SPEC-FRIEND.md, Watch).
func watchFileLine(path string, from int64) (line string, end int64, err error) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", from, nil
		}
		return "", from, err
	}
	defer func() {
		c := f.Close()
		if c != nil && err == nil {
			err = c
		}
	}()
	if _, err = f.Seek(from, io.SeekStart); err != nil {
		return "", from, err
	}
	raw, err := io.ReadAll(io.LimitReader(f, watchLineMax))
	if err != nil {
		return "", from, err
	}
	if i := bytes.IndexByte(raw, '\n'); i >= 0 {
		return string(raw[:i]), from + int64(i) + 1, nil
	}
	return "", from, nil
}

// watchLine prints one watch line on stdout. The verb prints as it goes, so
// the lines come before WATCH OK (docs/SPEC-FRIEND.md, Watch).
func watchLine(c *tool.Call, o *tool.Out) {
	o.Verb = "watch"
	o.Render(c.Stdout, false)
}

// watchObject prints the watch's one JSON object and stands for its exit
// (docs/SPEC-FRIEND.md, Watch).
func watchObject(c *tool.Call, v watchJSON, exit int) *tool.Out {
	var b strings.Builder
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return tool.Refuse("the watch result is no JSON: " + err.Error())
	}
	fmt.Fprintf(c.Stdout, "%s", b.String())
	return tool.Exit(exit)
}

// watchPrint writes a finished watch: the lines, then WATCH OK, or WATCH NONE
// on standard error. --json is the one object either way
// (docs/SPEC-FRIEND.md, Watch).
func watchPrint(c *tool.Call, d watchDone) *tool.Out {
	if c.Bool("json") {
		return watchObject(c, d.object(), d.exit)
	}
	if d.exit == 1 {
		o := tool.Fail().As("NONE").Fact("waited", d.waited.String())
		o.Verb = "watch"
		o.Render(c.Stderr, false)
		return tool.Exit(1)
	}
	for _, it := range d.items {
		word := "MESSAGE"
		if it.kind == "event" {
			word = "EVENT"
		}
		watchLine(c, tool.Done().As(word).Fact("id", it.id).Fact("from", it.from).Fact("subject", it.subject))
	}
	if d.wake != "" {
		watchLine(c, tool.Done().As("WAKE").Fact("line", tool.Text(d.wake)))
	}
	watchLine(c, tool.Done().Fact("after", d.after))
	return tool.Exit(0)
}

// watch is the verb. It arms, waits, saves the cursor, and then prints, so a
// cursor that cannot be saved prints nothing and a retry still sees what
// this run saw (docs/SPEC-FRIEND.md, Watch).
func (w world) watch(c *tool.Call) *tool.Out {
	b, closeStore, refused := w.bus(c)
	if refused != nil {
		return refused
	}
	defer closeStore()
	as := c.Str("as")
	ctx := c.Ctx
	state := w.stateDir(c, "")
	saved, offset, found, err := friend.ReadWatchCursor(state)
	if err != nil {
		return tool.Refuse("the watch cursor cannot be read: " + err.Error())
	}
	after := ""
	if found {
		after = saved
	}
	cursor, err := b.WaitArm(ctx, as, after)
	if err != nil {
		return answer(err)
	}
	waiter, err := b.Waiter()
	if err != nil {
		return answer(err)
	}
	wake := friend.ClaudeWakePath(state, as)
	if !found {
		size, err := watchFileSize(wake)
		if err != nil {
			return tool.Refuse("the wake file cannot be read: " + err.Error())
		}
		offset = size
	}
	run := &watchRun{
		me: as, cursor: cursor, offset: offset, timeout: c.Dur("timeout"),
		now: w.now, wake: wake, read: waiter.BlockRead, line: watchFileLine,
	}
	done, err := run.run(ctx)
	if err != nil {
		var fe *watchFileError
		if errors.As(err, &fe) {
			return tool.Refuse(err.Error())
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return tool.Fail(err.Error())
		}
		return answer(err)
	}
	if err := friend.WriteWatchCursor(state, done.after, run.offset); err != nil {
		return tool.Refuse("the watch cursor cannot be saved: " + err.Error())
	}
	return watchPrint(c, done)
}
