package task

// A read is done only by the verb (nova-tools #3897): `nova-sprint task done
// --id <read-task> --line 'SCORE who=<f> head=<sha40> score=N/10 gates=...:
// <finding>'` validates the line against the card and stores it with the
// close in one call (ns_task_read_done, internal/nsprint/fn/lua/task_read_done.lua).
// A read of a PR closed any other way refuses NOLINE with that remedy, so a
// score that lives only on the bus cannot close a read.

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/line"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
)

// FunctionReadDone is registered by internal/nsprint/fn/lua/task_read_done.lua.
const FunctionReadDone = "ns_task_read_done"

const (
	// DoneNoLine refuses a read of a PR closed without its typed line; Why
	// is the remedy, naming --line (exit 1).
	DoneNoLine DoneStatus = "NOLINE"
	// DoneBadLine refuses a read's line: malformed, not SCORE or HOLD, who
	// not the card's friend, head not the card's head, no PR record, or a
	// typed gate the measure disagrees with. Nothing is written (exit 1).
	DoneBadLine DoneStatus = "BADLINE"
)

// ReadDoneRequest is one read done by its line. Scope is the scope gate as
// the caller measured it in the mirror (read.MeasureScope); the ci and base
// gates are measured in the call.
type ReadDoneRequest struct {
	Sprint, ID, Token, As, Actor, Idem string
	// Evidence defaults to the line's first line.
	Evidence string
	// Cost, when set, is appended to Evidence as its cost clause (#3105).
	Cost  *Cost
	Line  string
	Scope line.Scope
}

// ReadDoneOutcome is DoneOutcome with the stored line: its record key and
// the PR's line count after it.
type ReadDoneOutcome struct {
	DoneOutcome
	LineKey string
	Lines   int
}

// ReadDone stores a read's typed line and closes its card in one call. A
// line the parser refuses is BADLINE before Redis is touched.
func ReadDone(ctx context.Context, st *store.Store, req ReadDoneRequest) (ReadDoneOutcome, error) {
	if st == nil {
		return ReadDoneOutcome{}, fmt.Errorf("task done: nil store")
	}
	if req.Sprint == "" || req.ID == "" {
		return ReadDoneOutcome{}, fmt.Errorf("task done: sprint and id are required")
	}
	var l line.Line
	if strings.TrimSpace(req.Line) != "" {
		var err error
		if l, err = line.Parse(req.Line); err != nil {
			return ReadDoneOutcome{DoneOutcome: DoneOutcome{Status: DoneBadLine, Why: err.Error()}}, nil
		}
	}
	if req.Evidence == "" && l.Text != "" {
		req.Evidence, _, _ = strings.Cut(l.Text, "\n")
	}
	if req.Cost != nil {
		req.Evidence = WithCost(req.Evidence, *req.Cost)
	}
	if _, _, err := ParseCost(req.Evidence); err != nil {
		return ReadDoneOutcome{}, fmt.Errorf("task done %s: evidence cost: %w", req.ID, err)
	}
	score := ""
	if l.Score >= 0 && l.Kind != "" {
		score = strconv.Itoa(l.Score)
	}
	words, err := st.Client().FCall(ctx, FunctionReadDone, nil,
		req.Sprint, req.ID, req.Token, req.As, req.Actor, req.Idem, req.Evidence,
		l.Head, l.Who, l.Kind, score, l.Gates, req.Scope.Word, strings.Join(req.Scope.Outside, " "), l.Text).StringSlice()
	if err != nil {
		return ReadDoneOutcome{}, fmt.Errorf("task done %s: %s: %w", req.ID, FunctionReadDone, err)
	}
	if len(words) == 0 {
		return ReadDoneOutcome{}, fmt.Errorf("task done %s: empty reply", req.ID)
	}
	switch s := DoneStatus(words[0]); s {
	case DoneClosed:
		out := ReadDoneOutcome{DoneOutcome: DoneOutcome{Status: s}}
		if len(words) > 2 {
			out.LineKey = words[1]
			out.Lines, _ = strconv.Atoi(words[2])
		}
		return out, nil
	case DoneNoLine, DoneBadLine:
		return ReadDoneOutcome{DoneOutcome: DoneOutcome{Status: s, Why: strings.Join(words[1:], " ")}}, nil
	case DoneRepeat, DoneFenced, DoneConflict, DoneInvalid:
		return ReadDoneOutcome{DoneOutcome: DoneOutcome{Status: s}}, nil
	default:
		return ReadDoneOutcome{}, fmt.Errorf("task done %s: %s", req.ID, strings.Join(words, " "))
	}
}
