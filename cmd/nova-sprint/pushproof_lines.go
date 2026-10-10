package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"io"
	"strings"
)

// seatPushLinesForArgs reads flags through the verb's own parser, never guessing
// which word is a value. Its store is read-only (SPEC-SPRINT section 8).
func (a *app) seatPushLinesForArgs(name string, args []string) ([]sprint.SeatPushLine, bool, error) {
	v := readVerb(append(strings.Fields(name), args...))
	if v.err != nil || v.help {
		return nil, false, nil
	}
	fs, c := a.verbSetup("")
	var err error
	v.fs.Visit(func(f *flag.Flag) {
		if fs.Lookup(f.Name) != nil && err == nil {
			err = fs.Set(f.Name, f.Value.String())
		}
	})
	if err != nil {
		return nil, false, err
	}
	st, err := a.store(*c)
	if err != nil {
		return nil, c.json, err
	}
	seat, err := st.SeatState(context.Background())
	if err != nil || seat.Holder == "" || !pushArmed(seat.Holder) {
		return nil, c.json, err
	}
	set, ok, err := st.SeatPushes(context.Background(), seat.Holder)
	if err != nil {
		return nil, c.json, err
	}
	set.Name = seat.Holder
	return sprint.SeatPushLines(set, ok, seat.Epoch, seat.Generation, a.now()), c.json, nil
}

// withSeatPushLines gives every seat-changing verb the same four observations.
// JSON keeps one result object; text prints each proof before the operation.
func (a *app) withSeatPushLines(name string, args []string, o, e io.Writer, run func(*app, []string, io.Writer, io.Writer) int) int {
	if !pushArmedDefault && !pushArmed(a.getenv("NOVA_SPRINT_ACTOR")) {
		return run(a, args, o, e)
	}
	if len(args) > 0 && verbflag.IsHelp(args[0]) {
		return run(a, args, o, e)
	}
	rows, asJSON, err := a.seatPushLinesForArgs(name, args)
	if err != nil {
		return refuse(e, name, err.Error())
	}
	if len(rows) == 0 {
		return run(a, args, o, e)
	}
	if asJSON {
		var out bytes.Buffer
		code := run(a, args, &out, e)
		if out.Len() == 0 {
			return code
		}
		var result map[string]any
		if err := json.Unmarshal(out.Bytes(), &result); err != nil {
			return a.readFailed(name, fmt.Errorf("operation result is not one JSON object: %w", err), e)
		}
		result["pushes"] = rows
		body, err := json.Marshal(result)
		if err != nil {
			return a.readFailed(name, err, e)
		}
		fmt.Fprintln(o, string(body))
		return code
	}
	writeSeatPushLines(o, rows)
	return run(a, args, o, e)
}

// writeSeatPushLines renders the same four proofs for verbs and each where frame.
func writeSeatPushLines(target io.Writer, rows []sprint.SeatPushLine) {
	for _, r := range rows {
		at := "-"
		if !r.At.IsZero() {
			at = r.At.UTC().Format("2006-01-02T15:04:05Z07:00")
		}
		status := "UP"
		if !r.Live {
			status = "DOWN"
		}
		fmt.Fprintf(target, "PUSH source=%s status=%s last=%s period=%s command=%s\n", r.Source, status, at, r.Period, oneline.Quote(r.Command))
	}
}
