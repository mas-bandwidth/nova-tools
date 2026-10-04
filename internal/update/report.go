package update

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/tool"
)

func shaText(s string) string { sum := sha256.Sum256([]byte(s)); return hex.EncodeToString(sum[:]) }

// report is the report verb's one value: the run's facts on its first line, an
// item per tool read (TOOL, or UNKNOWN with its reason), per tool changed since
// --snapshot (CHANGED) and per delivery (SENT), and a note for what is true and
// not a finding. Under --draft the value is the note itself, its payload: the
// headers and this same value's lines, which is also what --send delivers.
func report(ctx context.Context, verb string, all, entries []Entry, o options, kinds, help string, started time.Time, env Environment) *tool.Out {
	state := emptySnapshot()
	var err error
	if o.snapshot != "" {
		release, lockErr := lockSnapshot(ctx, o.snapshot)
		if lockErr != nil {
			return refused(verb, help, lockErr.Error())
		}
		defer release()
		state, err = readSnapshot(o.snapshot)
		if err != nil {
			return refused(verb, help, err.Error())
		}
	}
	rs := readEntries(ctx, entries, o, env, true)
	res := &tool.Out{Verb: verb, Status: tool.OK}
	seen := map[string]observed{}
	known := 0
	at := started.UTC().Format(time.RFC3339)
	for _, x := range rs {
		r := x.Installed
		status := "unknown"
		if r.Known() {
			status = "known"
			known++
			res.Item("tool", "name", x.Entry.Name, "kind", x.Entry.Kind, "version", r.Version, "raw", r.Raw, "path", r.Path)
		} else {
			res.Item("unknown", "name", x.Entry.Name, "kind", x.Entry.Kind, "path", r.Path, "raw", r.Raw, "reason", r.Reason, "remedy", r.Remedy)
		}
		seen[x.Entry.Name] = observed{r.Raw, status, at}
	}
	changed := "-"
	if o.snapshot != "" {
		changed = "no"
		if !sameObserved(state.Observed, seen) {
			changed = "yes"
		}
		keys := map[string]bool{}
		for k := range state.Observed {
			keys[k] = true
		}
		for k := range seen {
			keys[k] = true
		}
		for _, k := range slices.Sorted(maps.Keys(keys)) {
			a, b := state.Observed[k], seen[k]
			if a.Raw != b.Raw || a.Status != b.Status {
				res.Item("changed", "name", k, "was", a.Raw, "now", b.Raw)
			}
		}
		state.Observed = seen
		if err = writeSnapshot(o.snapshot, state); err != nil {
			return refused(verb, help, err.Error())
		}
	}
	if known != len(entries) {
		res.Status, res.Exit = tool.Failed, 1
	}
	facts := func(sent string) tool.Fields {
		return tool.Fields{{K: "checked", V: len(entries)}, {K: "known", V: known}, {K: "unknown", V: len(entries) - known},
			{K: "changed", V: changed}, {K: "sent", V: sent}, {K: "took", V: env.Now().Sub(started).Round(time.Millisecond).String()},
			{K: "file", V: o.file}, {K: "host", V: o.host}, {K: "as", V: o.as}, {K: "entries", V: len(all)}, {K: "kinds", V: kinds},
			{K: "at", V: at}, {K: "timeout", V: o.timeout.String()}, {K: "budget", V: o.budget.String()}, {K: "max", V: o.max},
			{K: "snapshot", V: o.snapshot}}
	}
	res.Facts = facts("-")
	subject := fmt.Sprintf("versions on %s at %s", dash(o.host), at)
	var note bytes.Buffer
	text(&note, capped(res, o.max))
	if o.draft {
		// The draft shows the headers the bus message carries, then its body (rule 24).
		return &tool.Out{Verb: verb, Status: res.Status, Exit: res.Exit, Payload: fmt.Sprintf("From: %s\nTo: %s\nSubject: %s\n\n%s", o.as, o.to, subject, note.String())}
	}
	if o.send {
		sent, err := deliver(ctx, o, state, seen, subject, note.String(), res, env)
		if err != nil {
			res.Note(err.Error())
			res.Status, res.Exit = tool.Failed, 1
		}
		res.Facts = facts(sent)
	}
	return res
}
func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// deliveryAllowance is what is left of the budget. A delivery that cannot start
// inside it is a pending gate, not a kill.
func deliveryAllowance(ctx context.Context, now time.Time) time.Duration {
	deadline, ok := ctx.Deadline()
	if !ok {
		return 0
	}
	return deadline.Sub(now)
}

// deliver is --send (SPEC-UPDATE rules 24 and 25). A pending note stands first:
// it is resolved through the bus (found on the log if it landed, else sent)
// before anything newer is composed. The note is saved in the snapshot before
// it is sent, and delivered advances only on the bus's confirmation.
func deliver(ctx context.Context, o options, s *snapshot, seen map[string]observed, subject, note string, res *tool.Out, env Environment) (string, error) {
	scope := snapshotScope(o)
	save := func() error {
		if o.snapshot != "" {
			return writeSnapshot(o.snapshot, s)
		}
		return nil
	}
	send := func(p pending, replayed bool) (string, error) {
		a, err := validatePending(p.Artifact)
		if err != nil || a.ID != p.ID {
			return "uncertain", fmt.Errorf("invalid pending note (preserve the snapshot and repair it before retry)")
		}
		allowance := deliveryAllowance(ctx, env.Now())
		if allowance <= 0 {
			return "uncertain", fmt.Errorf("pending %s not sent: the budget is spent (retry this --send with the same --snapshot; do not compose again)", p.ID)
		}
		saved, _ := time.Parse(time.RFC3339, a.At) // ignored: validatePending parsed it
		child, cancel := context.WithTimeout(ctx, allowance)
		line, id, err := sendNote(child, env, o, bus.Message{From: o.as, To: recipients(o.to), Subject: a.Subject, Body: a.Note}, saved, replayed)
		cancel()
		if err != nil {
			return "uncertain", fmt.Errorf("pending %s not confirmed: %s (retry this --send with the same --snapshot; do not compose again)", p.ID, clip(oneline.Err(err), 300))
		}
		s.Delivered[scope] = delivery{cloneObserved(p.Observed), id, env.Now().UTC().Format(time.RFC3339)}
		delete(s.Pending, scope)
		if err := save(); err != nil {
			return "uncertain", err
		}
		res.Item("sent", "to", o.to, "via", "redis "+o.redis, "line", line)
		return "yes", nil
	}
	sent := "no"
	if p, ok := s.Pending[scope]; ok {
		var err error
		// A pending read from the snapshot may have landed before its run died.
		sent, err = send(p, true)
		if err != nil {
			return sent, err
		}
	}
	if d, ok := s.Delivered[scope]; ok && sameObserved(d.Observed, seen) {
		if sent == "no" {
			res.Note(fmt.Sprintf("unchanged since %s to %s; nothing sent", d.ID, o.to))
		}
		return sent, nil
	}
	if ctx.Err() != nil {
		return sent, fmt.Errorf("delivery budget exhausted (retry --send with the same --snapshot)")
	}
	np := newPending(subject, note, env.Now())
	raw, err := json.Marshal(map[string]string{"schema": np.Schema, "id": np.ID, "subject": np.Subject, "note": np.Note, "sha256": np.SHA256, "at": np.At})
	if err != nil {
		return sent, err
	}
	p := pending{raw, cloneObserved(seen), np.ID}
	s.Pending[scope] = p
	if err = save(); err != nil {
		return sent, err
	}
	return send(p, false)
}
