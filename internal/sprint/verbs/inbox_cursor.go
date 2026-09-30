package verbs

import (
	"context"
	"fmt"
	"strconv"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/sprintfn"
	"github.com/mas-bandwidth/nova-tools/internal/tset"
)

// The coordinator's inbox cursor (section 3, inbox: "the cursor (--read): read,
// then one small step"; errata 3 amendment 8, the coordinator's loop: wait,
// read all, act on all, wait).
//
// The cursor is the last line of the epoch's log the coordinator has been
// shown. It lives on the sprint as a Layer 1 table property, CursorProp of
// CursorTable, written by a step's `prop` entry (L1 contract amendment,
// table properties). The sprint holds no table of its own, so the property is
// the work table's: the table the log's lines are about. A property is
// epoch-scoped, so a clear starts the next epoch with none, as its log starts
// at seq 1: no stored cursor is a cursor of 0. It needs no store part beyond
// Layer 1's: the control record would need a new field on the sprint part, in
// Go, its wire, the twin and the Lua part.
//
// inbox reads from it when no --after is given; inbox --read moves it to the
// last line shown, in a step after the read, guarded on the value it read and
// never lowering it (moveCursor); inbox --wait starts from it and leaves it
// where it is: the wait consumes nothing, the read does. A caller that keeps
// its own cursor gives --after, and the stored one is neither read nor
// written.

// CursorTable and CursorProp name the stored cursor.
const (
	CursorTable = sprint.Work
	CursorProp  = "inbox_cursor"
	// cursorVerb is the verb of the write, X's coordinator verb: the cursor
	// is the coordinator's alone (NOTCOORD, 1.5.3).
	cursorVerb = "inbox --read"
)

// cursorQuery reads the stored cursor with any read.
func cursorQuery() tset.ReadQuery {
	return tset.ReadQuery{Kind: "props", Table: CursorTable, Names: []string{CursorProp}}
}

// cursorAnswer is the stored cursor from the answer to cursorQuery: 0 when
// none is stored, and whether one is.
func cursorAnswer(verb string, a tset.ReadAnswer) (uint64, bool, error) {
	v, ok := a.Props[CursorProp]
	if !ok {
		return 0, false, nil
	}
	n, err := strconv.ParseUint(v, 10, 64)
	if err != nil {
		return 0, true, fmt.Errorf("%s: the stored inbox cursor %q is not a seq", verb, v)
	}
	return n, true, nil
}

// storedCursor reads the stored cursor: one trip, at the active epoch.
func storedCursor(ctx context.Context, e *Env, verb string) (uint64, Result, error) {
	res, err := e.Do(ctx, Planned{Verb: verb, Read: func(epoch tset.Decimal) *sprintfn.ReadRequest {
		return &sprintfn.ReadRequest{Epoch: epoch, Tset: []tset.ReadQuery{cursorQuery()}}
	}})
	if err != nil {
		return 0, res, err
	}
	if len(res.Read.Tset) != 1 {
		return 0, res, fmt.Errorf("%s: the read answered the wrong number of queries", verb)
	}
	c, _, err := cursorAnswer(verb, res.Read.Tset[0])
	return c, res, err
}

// moveCursor moves the stored cursor to the last line an inbox showed, of the
// epoch it read: a read of the property and, when it is below to, one step
// that guards the value it read (a propguard) and sets it. A writer that moved
// it first is a race, planned again on the new value, and a value at or above
// to stays: the cursor never goes down. An epoch moved since the read (a
// clear) stays as it is, the new epoch's log starting from its first line.
func moveCursor(ctx context.Context, e *Env, epoch, to uint64) (Result, error) {
	var said string
	res, err := e.Do(ctx, Planned{Verb: cursorVerb,
		Read: func(ep tset.Decimal) *sprintfn.ReadRequest {
			return &sprintfn.ReadRequest{Epoch: ep, Tset: []tset.ReadQuery{cursorQuery()}}
		},
		Plan: func(rd *sprintfn.ReadReply) (Part, error) {
			if at, ok := undec(rd.Epoch); !ok || at != epoch {
				said = "INBOX CURSOR unchanged: the sprint was cleared since the read; the cursor of the new epoch is 0\n"
				return Part{}, nil
			}
			if len(rd.Tset) != 1 {
				return Part{}, fmt.Errorf("%s: the read answered the wrong number of queries", cursorVerb)
			}
			cur, has, err := cursorAnswer(cursorVerb, rd.Tset[0])
			if err != nil {
				return Part{}, err
			}
			if cur >= to {
				said = fmt.Sprintf("INBOX CURSOR %d\n", cur)
				return Part{}, nil
			}
			guard := tset.Entry{Kind: "propguard", Table: CursorTable, Name: CursorProp}
			if has {
				was := strconv.FormatUint(cur, 10)
				guard.Value = &was
			}
			value := strconv.FormatUint(to, 10)
			said = fmt.Sprintf("INBOX CURSOR %d (was %d)\n", to, cur)
			return Part{Req: &sprintfn.Request{Body: sprintfn.Body{Entries: []tset.Entry{guard,
				{Kind: "prop", Table: CursorTable, Name: CursorProp, Value: &value}}}}}, nil
		}})
	if err == nil {
		res.Said = said
	}
	return res, err
}
