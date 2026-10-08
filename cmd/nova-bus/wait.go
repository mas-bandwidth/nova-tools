// wait.go holds the wait verb: its flags, its run and the helpers only it uses.

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/tool"
)

// The wait verb: the wake a harness runs beside a session, made general for
// any AI on the bus (docs/SPEC-BUS.md, the verbs: wait). A wait takes
// nothing: it reads the recipient's stream past a cursor with XREAD, never
// the consumer group, so a later recv still delivers and acks what the wait
// saw, and its cursor is the only state, the caller's to hold between runs.
// The decision over one batch is bus.WaitPick, a pure function; the blocking
// read is the store's; the clock and the wake file are the world's, so no
// test opens a socket or waits real time.

// WaitTick is how long one blocking read of a wait with a --wake-file is: the
// file is looked at once a tick, so a line appended to it is returned within
// one. A wait with neither a wake file nor a timeout parks on one read that
// never runs out (docs/SPEC-BUS.md, the verbs: wait).
const WaitTick = time.Second

// waitMessage is one message of a wait's JSON, the fields the help names.
type waitMessage struct {
	ID      string `json:"id"`
	From    string `json:"from"`
	Subject string `json:"subject"`
	Bytes   int    `json:"bytes"`
}

// waitWake is the wake of a wait's JSON.
type waitWake struct {
	File string `json:"file"`
	Line string `json:"line"`
}

// waitJSON is the --json rendering of one wait: one object, printed when the
// wait ends (the ARMED line is the text form's). The subject and the wake
// line are oneline-escaped, so nothing they hold can reorder the line a
// reader reads.
type waitJSON struct {
	Status   string        `json:"status"`
	Word     string        `json:"word"`
	After    string        `json:"after"`
	Messages []waitMessage `json:"messages"`
	Wake     *waitWake     `json:"wake,omitempty"`
}

// waitLine prints one wait line on stdout: the verb prints as it goes (the
// ARMED line first, so a caller that re-arms with that id misses nothing
// between two runs), as recv --forever prints each message.
func waitLine(c *tool.Call, o *tool.Out) {
	o.Verb = "wait"
	o.Render(c.Stdout, false)
}

// waitObject prints the wait's one JSON object and stands for its exit.
func waitObject(c *tool.Call, v waitJSON, exit int) *tool.Out {
	var b strings.Builder
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return tool.Refuse("the wait's result is no JSON: " + err.Error())
	}
	fmt.Fprintf(c.Stdout, "%s", b.String())
	return tool.Exit(exit)
}

// wait is the verb: it arms, prints WAIT ARMED, and returns on the first
// entries past the cursor that count, on a wake line, or at the timeout
// (docs/SPEC-BUS.md, the verbs: wait).
func (w world) wait(c *tool.Call) *tool.Out {
	// wait's --timeout is how long the wait parks (0 is for ever), not the
	// call bound. Non-blocking reads use CallTimeout; a blocking XREAD carries
	// its block and the margin more (SPEC-BUS.md, the deadlines).
	b, login, closeStore, refused := w.bus(c, bus.CallTimeout)
	if refused != nil {
		return refused
	}
	defer closeStore()
	as, refused := identity(c, login)
	if refused != nil {
		return refused
	}
	ctx := context.Background()
	cursor, err := b.WaitArm(ctx, as, c.Str("after"))
	if err != nil {
		return answer(err)
	}
	waiter, err := b.Waiter()
	if err != nil {
		return answer(err)
	}
	jsonOut := c.Bool("json")
	if !jsonOut {
		waitLine(c, tool.Done().As("ARMED").Fact("after", cursor))
	}
	wakePath := c.Str("wake-file")
	var offset int64
	if wakePath != "" {
		size, err := w.fileSize(wakePath)
		if err != nil {
			return tool.Refuse("the wake file cannot be read: " + err.Error())
		}
		offset = size
	}
	// --skip-subject is a comma list of prefixes, empty words dropped; the
	// match without case is WaitPick's, the one place the rule lives
	// (docs/SPEC-BUS.md, the verbs: wait).
	skips := names(c.Str("skip-subject"))
	start := w.now()
	timeout := c.Dur("timeout")
	for {
		if wakePath != "" {
			text, end, err := w.fileLine(wakePath, offset)
			if err != nil {
				return tool.Refuse("the wake file cannot be read: " + err.Error())
			}
			offset = end
			if text != "" {
				if jsonOut {
					return waitObject(c, waitJSON{Status: "ok", Word: "WAKE", After: cursor,
						Messages: []waitMessage{}, Wake: &waitWake{File: wakePath, Line: oneline.Escape(text)}}, 0)
				}
				waitLine(c, tool.Done().As("WAKE").Fact("file", wakePath).Fact("line", tool.Text(text)))
				return tool.Exit(0)
			}
		}
		block := time.Duration(0) // park for ever: nothing else is watched
		if wakePath != "" {
			block = WaitTick // the file is looked at once a tick
		}
		if timeout > 0 {
			left := timeout - w.now().Sub(start)
			if left <= 0 {
				if jsonOut {
					return waitObject(c, waitJSON{Status: "ok", Word: "NONE", After: cursor, Messages: []waitMessage{}}, 1)
				}
				o := tool.Fail().As("NONE").Fact("after", cursor).Fact("waited", timeout.String())
				o.Verb = "wait"
				o.Render(c.Stderr, false)
				return tool.Exit(1)
			}
			if block == 0 || left < block {
				block = left
			}
		}
		got, err := waiter.BlockRead(ctx, bus.StreamOf(as), cursor, block, bus.WaitRead)
		if err != nil {
			return answer(err)
		}
		kept, after := bus.WaitPick(got, as, skips)
		if after != "" {
			cursor = after
		}
		if len(kept) == 0 {
			continue // a skipped entry moved the cursor; the wait goes on
		}
		if jsonOut {
			msgs := make([]waitMessage, 0, len(kept))
			for _, e := range kept {
				m := e.Message()
				msgs = append(msgs, waitMessage{ID: m.ID, From: m.From, Subject: oneline.Escape(m.Subject), Bytes: len(m.Body)})
			}
			return waitObject(c, waitJSON{Status: "ok", Word: "OK", After: cursor, Messages: msgs}, 0)
		}
		for _, e := range kept {
			m := e.Message()
			waitLine(c, tool.Done().As("MESSAGE").Fact("id", m.ID).Fact("from", m.From).Fact("subject", m.Subject).Fact("bytes", len(m.Body)))
		}
		waitLine(c, tool.Done().Fact("after", cursor))
		return tool.Exit(0)
	}
}
