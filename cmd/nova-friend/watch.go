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

// wakeLineMax bounds the bytes one read of a wake file takes: lines are one
// per message, and a line over this is cut.
const wakeLineMax = 64 * 1024

// WaitTick is how long one blocking read of a wait with a wake file is: the
// file is checked once a tick.
const WaitTick = time.Second

// watchSkips are the subjects watch skips: ping, pong, daemon-pong and keepalive
// (docs/SPEC-FRIEND.md, Watch).
var watchSkips = []string{"ping", "pong", "daemon-pong", "keepalive"}

// realFileSize answers a wake file's size (0 when the file is not there):
// the offset a later line must lie past.
func realFileSize(path string) (int64, error) {
	st, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return st.Size(), nil
}

// realFileLine reads a wake file from an offset, answering its first line
// past the offset and the offset just past that line's newline: "" and the
// same offset when no newline is there yet (a fragment is not a line). A
// file that is not there is no wake yet, never an error.
func realFileLine(path string, from int64) (string, int64, error) {
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

// isEvent reports whether a subject is an event (starts with "event:", case-insensitive).
func isEvent(subject string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(subject)), "event:")
}

// watchLine prints one watch line on stdout.
func watchLine(c *tool.Call, o *tool.Out) {
	o.Verb = "watch"
	o.Render(c.Stdout, false)
}

type watchJSON struct {
	Status   string      `json:"status"`
	Word     string      `json:"word"`
	After    string      `json:"after,omitempty"`
	Waited   string      `json:"waited,omitempty"`
	Messages []watchItem `json:"messages"`
	Events   []watchItem `json:"events"`
	Wake     *watchWake  `json:"wake,omitempty"`
}

type watchItem struct {
	ID      string `json:"id"`
	From    string `json:"from"`
	Subject string `json:"subject"`
}

type watchWake struct {
	Line string `json:"line"`
}

func watchObject(c *tool.Call, v watchJSON, exit int) *tool.Out {
	var b strings.Builder
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return tool.Refuse("the watch's result is no JSON: " + err.Error())
	}
	fmt.Fprintf(c.Stdout, "%s", b.String())
	return tool.Exit(exit)
}

// watch is the verb: waits on the coordinator's stream, the coordinator's wake
// file (in its state dir, where the claude adapter appends) and events (bus messages
// whose subject starts event:), skipping its own messages, ping, pong, daemon-pong
// and keepalive; the cursor is saved in the state dir after each run
// (docs/SPEC-FRIEND.md, Watch).
func (w world) watch(c *tool.Call) *tool.Out {
	as := c.Str("as")
	b, closeStore, refused := w.bus(c)
	if refused != nil {
		return refused
	}
	defer closeStore()

	stateDir := w.stateDir(c, "")
	cursor, _, err := friend.ReadWatchCursor(stateDir)
	if err != nil {
		return tool.Refuse("the cursor file cannot be read: " + err.Error())
	}

	ctx := context.Background()
	cursor, err = b.WaitArm(ctx, as, cursor)
	if err != nil {
		return answer(err)
	}

	waiter, err := b.Waiter()
	if err != nil {
		return answer(err)
	}

	wakePath := friend.ClaudeWakePath(stateDir, as)
	fileSize := w.fileSize
	if fileSize == nil {
		fileSize = realFileSize
	}
	fileLine := w.fileLine
	if fileLine == nil {
		fileLine = realFileLine
	}

	var offset int64
	size, err := fileSize(wakePath)
	if err != nil {
		return tool.Refuse("the wake file cannot be read: " + err.Error())
	}
	offset = size

	jsonOut := c.Bool("json")
	start := w.now()
	timeout := c.Dur("timeout")

	for {
		// 1. Check wake file
		text, end, err := fileLine(wakePath, offset)
		if err != nil {
			return tool.Refuse("the wake file cannot be read: " + err.Error())
		}
		offset = end
		if text != "" {
			if err := friend.WriteWatchCursor(stateDir, cursor); err != nil {
				return tool.Refuse("the cursor file cannot be written: " + err.Error())
			}
			if jsonOut {
				return watchObject(c, watchJSON{
					Status:   "ok",
					Word:     "OK",
					After:    cursor,
					Messages: []watchItem{},
					Events:   []watchItem{},
					Wake:     &watchWake{Line: oneline.Escape(text)},
				}, 0)
			}
			watchLine(c, tool.Done().As("WAKE").Fact("line", tool.Text(text)))
			watchLine(c, tool.Done().Fact("after", cursor))
			return tool.Exit(0)
		}

		// 2. Check timeout
		block := WaitTick
		if timeout > 0 {
			left := timeout - w.now().Sub(start)
			if left <= 0 {
				if err := friend.WriteWatchCursor(stateDir, cursor); err != nil {
					return tool.Refuse("the cursor file cannot be written: " + err.Error())
				}
				if jsonOut {
					return watchObject(c, watchJSON{
						Status:   "ok",
						Word:     "NONE",
						After:    cursor,
						Waited:   timeout.String(),
						Messages: []watchItem{},
						Events:   []watchItem{},
					}, 1)
				}
				o := tool.Fail().As("NONE").Fact("waited", timeout.String())
				o.Verb = "watch"
				o.Render(c.Stderr, false)
				return tool.Exit(1)
			}
			if left < block {
				block = left
			}
		}

		// 3. Block-read stream
		got, err := waiter.BlockRead(ctx, bus.StreamOf(as), cursor, block, bus.WaitRead)
		if err != nil {
			return answer(err)
		}

		kept, after := bus.WaitPick(got, as, watchSkips)
		if after != "" {
			cursor = after
		}
		if len(kept) == 0 {
			continue
		}
		if len(kept) > 5 {
			kept = kept[:5]
		}

		if err := friend.WriteWatchCursor(stateDir, cursor); err != nil {
			return tool.Refuse("the cursor file cannot be written: " + err.Error())
		}

		if jsonOut {
			msgs := []watchItem{}
			evts := []watchItem{}
			for _, e := range kept {
				m := e.Message()
				item := watchItem{ID: m.ID, From: m.From, Subject: oneline.Escape(m.Subject)}
				if isEvent(m.Subject) {
					evts = append(evts, item)
				} else {
					msgs = append(msgs, item)
				}
			}
			return watchObject(c, watchJSON{
				Status:   "ok",
				Word:     "OK",
				After:    cursor,
				Messages: msgs,
				Events:   evts,
			}, 0)
		}

		for _, e := range kept {
			m := e.Message()
			if isEvent(m.Subject) {
				watchLine(c, tool.Done().As("EVENT").Fact("id", m.ID).Fact("from", m.From).Fact("subject", m.Subject))
			} else {
				watchLine(c, tool.Done().As("MESSAGE").Fact("id", m.ID).Fact("from", m.From).Fact("subject", m.Subject))
			}
		}
		watchLine(c, tool.Done().Fact("after", cursor))
		return tool.Exit(0)
	}
}
