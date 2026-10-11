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

	"github.com/mas-bandwidth/nova-tools/pkg/bus"
	"github.com/mas-bandwidth/nova-tools/pkg/friend"
	"github.com/mas-bandwidth/nova-tools/pkg/oneline"
	"github.com/mas-bandwidth/nova-tools/pkg/tool"
)

// The watch verb: the coordinator's wake, one run of it (docs/SPEC-FRIEND.md,
// Watch). It is the coordinator's use of the bus's wait: the decision over a
// batch of entries is bus.WaitPick, the cursor and the blocking read are the
// bus's (WaitArm, Waiter), and what this file adds is the wake file, the
// events, the saved cursor and the lines. The logic (watchRun) is a function
// apart from its transport: the world's clock, the wake file's reads and the
// cursor file's reads and writes are passed in, so no test opens a socket or
// waits real time.

// WatchTick is how long one blocking read of a watch is: the wake file is
// looked at once a tick, so a line appended to it is seen within one.
const WatchTick = time.Second

// WatchSkips are the subject prefixes a watch never wakes on, matched without
// case: the coordinator's own machinery (SPEC-FRIEND.md, Watch).
var WatchSkips = []string{"ping", "pong", "daemon-pong", "keepalive"}

// EventPrefix starts the subject of an event: a bus message any tool sends the
// coordinator to say something happened (SPEC-FRIEND.md, Watch).
const EventPrefix = "event:"

// wakeLineMax bounds one read of a wake file: one line a reader can use is
// far under this.
const wakeLineMax = 64 << 10

// The kinds of one wake line.
const (
	WatchMessage = "MESSAGE"
	WatchEvent   = "EVENT"
	WatchWake    = "WAKE"
)

// watchWake is one wake of a watch: a message, an event or a wake file's
// line. It is the JSON of one wake as well.
type watchWake struct {
	Kind    string `json:"kind"`
	ID      string `json:"id,omitempty"`
	From    string `json:"from,omitempty"`
	Subject string `json:"subject,omitempty"`
	Line    string `json:"line,omitempty"`
}

// watchResult is what one run found: the wakes (none past the timeout), the
// cursor to save, and the wake file's offset to save.
type watchResult struct {
	Wakes  []watchWake
	After  string
	Offset int64
}

// watchEnv is what watchRun reaches outside itself.
type watchEnv struct {
	waiter   bus.Waiter
	now      func() time.Time
	fileLine func(path string, from int64) (line string, end int64, err error)
}

// watchRun waits on the stream of me past the cursor after and on the wake
// file at wakePath past offset, and returns on the first wakes, up to
// bus.WaitMax of them, the wake file's lines first (SPEC-FRIEND.md, Watch).
// Own messages, ping, pong, daemon-pong and keepalive are skipped by
// bus.WaitPick, which moves the cursor past them; a subject starting
// EventPrefix is an event, any other kept entry a message. Past timeout (0 is
// for ever) it returns no wakes, the cursor moved over what it skipped.
func watchRun(ctx context.Context, env watchEnv, me, wakePath, after string, offset int64, timeout time.Duration) (watchResult, error) {
	res := watchResult{After: after, Offset: offset}
	start := env.now()
	for {
		for len(res.Wakes) < bus.WaitMax {
			text, end, err := env.fileLine(wakePath, res.Offset)
			if err != nil {
				return res, err
			}
			if end == res.Offset {
				break // no whole line past the offset
			}
			res.Offset = end
			if text != "" {
				res.Wakes = append(res.Wakes, watchWake{Kind: WatchWake, Line: text})
			}
		}
		if len(res.Wakes) > 0 {
			return res, nil // the stream's entries stay past the cursor for the next run
		}
		block := WatchTick
		if timeout > 0 {
			left := timeout - env.now().Sub(start)
			if left <= 0 {
				return res, nil
			}
			block = min(block, left)
		}
		got, err := env.waiter.BlockRead(ctx, bus.StreamOf(me), res.After, block, bus.WaitRead)
		if err != nil {
			return res, err
		}
		kept, next := bus.WaitPick(got, me, WatchSkips)
		if next != "" {
			res.After = next
		}
		for _, e := range kept {
			m := e.Message()
			kind := WatchMessage
			if strings.HasPrefix(strings.ToLower(m.Subject), EventPrefix) {
				kind = WatchEvent
			}
			res.Wakes = append(res.Wakes, watchWake{Kind: kind, ID: m.ID, From: m.From, Subject: m.Subject})
		}
	}
}

// watchJSON is the --json rendering of one run: one object.
type watchJSON struct {
	Status string      `json:"status"`
	Word   string      `json:"word"`
	After  string      `json:"after"`
	Waited string      `json:"waited,omitempty"`
	Wakes  []watchWake `json:"wakes"`
}

// watchLine is one wake as its text line: values that could break a line or
// a field are escaped (pkg/oneline), a subject and a wake line quoted.
func watchLine(v watchWake) string {
	switch v.Kind {
	case WatchWake:
		return "WATCH WAKE line=" + oneline.Quote(oneline.Escape(v.Line))
	default:
		return "WATCH " + v.Kind + " id=" + oneline.Field(v.ID) + " from=" + oneline.Field(v.From) + " subject=" + oneline.Quote(oneline.Escape(v.Subject))
	}
}

// watch is the verb (docs/SPEC-FRIEND.md, Watch): it reads the saved cursor,
// waits, saves the cursor and prints the wakes and then WATCH OK, or
// WATCH NONE past the timeout at exit 1.
func (w world) watch(c *tool.Call) *tool.Out {
	b, closeStore, refused := w.bus(c)
	if refused != nil {
		return refused
	}
	defer closeStore()
	as, timeout := c.Str("as"), c.Dur("timeout")
	state := w.stateDir(c, "")
	ctx := context.Background()
	saved, found, err := friend.ReadWatch(state)
	if err != nil {
		return tool.Refuse("the watch cursor cannot be read: " + err.Error())
	}
	after, err := b.WaitArm(ctx, as, saved.After) // the tail when there is no cursor; refuses a name the roster lacks
	if err != nil {
		return answer(err)
	}
	waiter, err := b.Waiter()
	if err != nil {
		return answer(err)
	}
	wakePath := friend.ClaudeWakePath(state, as)
	offset := saved.WakeOffset
	if !found {
		size, err := w.wakeSize(wakePath)
		if err != nil {
			return tool.Refuse("the wake file cannot be read: " + err.Error())
		}
		offset = size
	}
	res, err := watchRun(ctx, watchEnv{waiter: waiter, now: w.now, fileLine: w.wakeLine}, as, wakePath, after, offset, timeout)
	if err != nil {
		var r *bus.Refusal
		if errors.As(err, &r) {
			return answer(err)
		}
		return tool.Refuse("the watch could not read: " + err.Error())
	}
	if err := friend.WriteWatch(state, friend.Watch{After: res.After, WakeOffset: res.Offset}); err != nil {
		return tool.Refuse("the watch cursor cannot be saved: " + err.Error())
	}
	none := len(res.Wakes) == 0
	if c.Bool("json") {
		v := watchJSON{Status: "ok", Word: "OK", After: res.After, Wakes: res.Wakes}
		if none {
			v.Word, v.Waited = "NONE", timeout.String()
		}
		if v.Wakes == nil {
			v.Wakes = []watchWake{}
		}
		for i := range v.Wakes {
			v.Wakes[i].Subject, v.Wakes[i].Line = oneline.Escape(v.Wakes[i].Subject), oneline.Escape(v.Wakes[i].Line)
		}
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(false)
		if err := enc.Encode(v); err != nil {
			return tool.Refuse("the watch's result is no JSON: " + err.Error())
		}
		fmt.Fprint(c.Stdout, buf.String())
		if none {
			return tool.Exit(1)
		}
		return tool.Exit(0)
	}
	if none {
		fmt.Fprintf(c.Stderr, "WATCH NONE waited=%s\n", timeout)
		return tool.Exit(1)
	}
	for _, v := range res.Wakes {
		fmt.Fprintln(c.Stdout, watchLine(v))
	}
	fmt.Fprintf(c.Stdout, "WATCH OK after=%s\n", oneline.Field(res.After))
	return tool.Exit(0)
}

// wakeSize is the wake file's end when a watch first runs (0 when the file is
// not there): its lines from then on are wakes.
func (w world) wakeSize(path string) (int64, error) {
	if w.wake != nil {
		return w.wake.size(path)
	}
	st, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return st.Size(), nil
}

// wakeLine is the first line of the wake file past an offset and the offset
// past it: the same offset and "" when no whole line is there yet (a fragment
// is not a line); a file that is not there is no wake, never an error.
func (w world) wakeLine(path string, from int64) (string, int64, error) {
	if w.wake != nil {
		return w.wake.line(path, from)
	}
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", from, nil
		}
		return "", 0, err
	}
	defer f.Close() // ignored: read-only, nothing to flush
	if _, err := f.Seek(from, io.SeekStart); err != nil {
		return "", 0, err
	}
	raw, err := io.ReadAll(io.LimitReader(f, wakeLineMax))
	if err != nil {
		return "", 0, err
	}
	if i := bytes.IndexByte(raw, '\n'); i >= 0 {
		return string(raw[:i]), from + int64(i) + 1, nil
	}
	return "", from, nil
}

// wakeFS is the wake file's reads, a test's own in place of the disk.
type wakeFS struct {
	size func(path string) (int64, error)
	line func(path string, from int64) (string, int64, error)
}
