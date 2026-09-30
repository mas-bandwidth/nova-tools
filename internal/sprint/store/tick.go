package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/redis/go-redis/v9"
)

// The machine (docs/SPEC-SPRINT.md, "The machine"): one state flag in the
// store, RUNNING or STOPPED, set by start and stop; one heartbeat record,
// written by every tick; and the tick itself, run through the engine, one
// operation per part, so the tick and every verb share the fence and
// interleave safely.

// The machine's keys, under the deployment's prefix.
const (
	keyMachine   = "machine"   // STRING, the state record (JSON)
	keyHeartbeat = "heartbeat" // STRING, the last tick's record (JSON)
)

// The machine's states.
const (
	Running = "RUNNING"
	Stopped = "STOPPED"
)

const (
	// TickEvery is the time between two ticks of run.
	TickEvery = time.Second
	// HeartbeatIdleEvery is how often, at most, a tick that did nothing (an
	// idle tick, or a STOPPED machine's look) writes the heartbeat: a tick
	// that did anything or failed writes it always.
	HeartbeatIdleEvery = 5 * time.Second
	// TickBackoffCap bounds the wait after consecutive failed ticks.
	TickBackoffCap = 5 * time.Second
	// MachineSilence is how long a RUNNING machine goes without a heartbeat
	// before the sprint line says it is STOPPED. It stays above the longest
	// gap a live run loop leaves between two heartbeats:
	// HeartbeatIdleEvery + TickEvery when idle, TickBackoffCap when failing.
	MachineSilence = 15 * time.Second
	// MaxStopSpans bounds the STOPPED spans the state record keeps.
	MaxStopSpans = 1000
)

// KV is the part of a store the machine keeps its records in.
type KV interface {
	GetKey(ctx context.Context, name string) (string, bool, error)
	SetKey(ctx context.Context, name, value string) error
	// SetKeyShowing writes a record and a stored view's state text in one
	// atomic step (one MULTI/EXEC on Redis), so the view never shows a state
	// the record does not hold; a view that is not there is left alone
	// (docs/SPEC-SPRINT.md sections 1 and 14).
	SetKeyShowing(ctx context.Context, name, value, view, state string) error
	// ShowState writes only a stored view's state text; a view that is not
	// there is left alone.
	ShowState(ctx context.Context, view, state string) error
}

// Both stores keep the machine's records.
var (
	_ KV = (*Mem)(nil)
	_ KV = (*Redis)(nil)
)

// Span is one time the machine was STOPPED; To is zero while it still is.
type Span = sprint.Span

// Machine is the state record: the state, when it last changed and by whom,
// the total time STOPPED before the current span, and the STOPPED spans.
// No record is STOPPED.
type Machine struct {
	State      string        `json:"state"`
	Since      time.Time     `json:"since"`
	Who        string        `json:"who,omitempty"`
	StoppedFor time.Duration `json:"stopped_ns"`
	Spans      []Span        `json:"spans,omitempty"`
	// Cause is why a STOPPED machine stopped when it stopped itself:
	// sprint.DoneCause when the tick's done part found the sprint done
	// (errata 3 amendment 6). A stop by hand, a clear, and a start leave it
	// empty; an add of work to a done sprint empties it too, as the sprint is
	// no longer done.
	Cause string `json:"cause,omitempty"`
}

// Done says the machine is STOPPED because the sprint is done.
func (m Machine) Done() bool { return !m.Running() && m.Cause == sprint.DoneCause }

// FirstStart is the machine's first start after a clock reading (the sprint's
// epoch began, zero for the first epoch): the end of the first STOPPED span
// that ends after it. Every epoch begins STOPPED (init, clear), so the span
// open at the epoch's start ends at its first start. Zero when it has not
// started since.
func (m Machine) FirstStart(after time.Time) time.Time {
	for _, sp := range m.Spans {
		if !sp.To.IsZero() && sp.To.After(after) {
			return sp.To
		}
	}
	return time.Time{}
}

// Heartbeat is the last tick: when, how many so far, and the error of the
// last tick if it failed, with the count of failures in a row.
type Heartbeat struct {
	At       time.Time `json:"at"`
	Ticks    int64     `json:"ticks"`
	Error    string    `json:"error,omitempty"`
	Failures int       `json:"failures,omitempty"`
	// Due is how many moves and judgments the last tick left past its
	// bounds: the next ticks catch up on them.
	Due int `json:"due,omitempty"`
	// What the last tick saw: the tables' revisions, the landed and all
	// primaries, and when it last read the whole sprint (every tick of a
	// RUNNING machine does).
	Revisions [4]uint64 `json:"revisions"`
	Landed    int64     `json:"landed"`
	All       int64     `json:"all"`
	Full      time.Time `json:"full"`
	// Fresh is the fleet members whose beat was fresh at the last tick: a
	// member coming or going makes the next tick a full one.
	Fresh []string `json:"fresh,omitempty"`
	// Looked is when a tick last read the machine's state, RUNNING or
	// STOPPED: a run loop is alive while it is recent, whatever the state.
	Looked time.Time `json:"looked,omitempty"`
}

// Alive is the last clock reading a tick was seen at, ticking or looking.
func (hb Heartbeat) Alive() time.Time {
	if hb.Looked.After(hb.At) {
		return hb.Looked
	}
	return hb.At
}

// Running says the state is RUNNING.
func (m Machine) Running() bool { return m.State == Running }

// StateWord is RUNNING or STOPPED.
func (m Machine) StateWord() string {
	if m.Running() {
		return Running
	}
	return Stopped
}

// StoppedBetween is the time the machine was STOPPED between from and to.
func (m Machine) StoppedBetween(from, to time.Time) time.Duration {
	return sprint.StoppedBetween(m.Spans, from, to)
}

// StoppedTotal is the total time the machine has been STOPPED, until now.
func (m Machine) StoppedTotal(now time.Time) time.Duration {
	d := m.StoppedFor
	if n := len(m.Spans); n > 0 && m.Spans[n-1].To.IsZero() && now.After(m.Spans[n-1].From) {
		d += now.Sub(m.Spans[n-1].From)
	}
	return d
}

// ViewState is the state text the sprint's stored view shows as its summary
// line for the machine (docs/SPEC-SPRINT.md section 1): STOPPED alone, with
// no counts, percent or ETA, while the machine is STOPPED (no record is
// STOPPED); DONE alone while it is STOPPED because the sprint is done (errata
// 3 amendment 6); none, so the counts show, while it is RUNNING.
func ViewState(m Machine) string {
	switch {
	case m.Running():
		return ""
	case m.Done():
		return DoneState
	}
	return Stopped
}

// DoneState is the view's state text, and the machine line's state, of a
// machine STOPPED because the sprint is done.
const DoneState = "DONE"

// putMachine writes the state record and the view's state text for it in one
// atomic step (section 14): the view's summary line and the record never
// disagree.
func (st *Store) putMachine(ctx context.Context, m Machine) error {
	kv, err := st.kv()
	if err != nil {
		return err
	}
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return kv.SetKeyShowing(ctx, keyMachine, string(b), st.Names.View(), ViewState(m))
}

// MachineLine is the machine's part of the sprint line: running, running
// and catching up when the last tick left moves due past its bounds, STOPPED,
// or STOPPED because a RUNNING machine has not ticked for MachineSilence; a
// last tick that failed is shown with its error.
func MachineLine(now time.Time, m Machine, hb Heartbeat) string {
	if m.Done() {
		return "machine: " + DoneState
	}
	if !m.Running() {
		return "machine: STOPPED"
	}
	last := hb.At
	if m.Since.After(last) {
		last = m.Since
	}
	line := "machine: running"
	if gap := now.Sub(last); gap > MachineSilence {
		line = fmt.Sprintf("machine: STOPPED (no tick for %ds)", int(gap/time.Second))
	} else if hb.Due > 0 && !hb.At.Before(m.Since) {
		line = fmt.Sprintf("machine: running (catching up: %d moves due)", hb.Due)
	}
	if hb.Error != "" && !hb.At.Before(m.Since) {
		line += "; last tick failed: " + hb.Error
	}
	return line
}

func (st *Store) kv() (KV, error) {
	kv, ok := st.B.(KV)
	if !ok {
		return nil, errors.New("this store keeps no machine records")
	}
	return kv, nil
}

func (st *Store) getJSON(ctx context.Context, key string, v any) error {
	kv, err := st.kv()
	if err != nil {
		return err
	}
	raw, ok, err := kv.GetKey(ctx, key)
	if err != nil || !ok {
		return err
	}
	if err := json.Unmarshal([]byte(raw), v); err != nil {
		return fmt.Errorf("the machine's %s record is unreadable: %w", key, err)
	}
	return nil
}

func (st *Store) putJSON(ctx context.Context, key string, v any) error {
	kv, err := st.kv()
	if err != nil {
		return err
	}
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return kv.SetKey(ctx, key, string(b))
}

// Machine reads the state record and the heartbeat.
func (st *Store) Machine(ctx context.Context) (Machine, Heartbeat, error) {
	var m Machine
	var hb Heartbeat
	if err := st.getJSON(ctx, keyMachine, &m); err != nil {
		return m, hb, err
	}
	err := st.getJSON(ctx, keyHeartbeat, &hb)
	return m, hb, err
}

// MachineLine reads the machine and says its line at the clock's reading;
// "" when the store keeps no machine records.
func (st *Store) MachineLine(ctx context.Context) string {
	m, hb, err := st.Machine(ctx)
	if err != nil {
		return ""
	}
	return MachineLine(st.now(), m, hb)
}

// SetMachine sets the state RUNNING (start) or STOPPED (stop). Setting the
// state it has changes nothing and writes nothing. A change is recorded as a
// happened notification (who, when); a STOPPED span is opened by stop and
// closed by start, and its time added to the total time STOPPED. The flag is
// read by the tick before every part: the part in flight finishes, and no
// part begins after the flag says STOPPED.
func (st *Store) SetMachine(ctx context.Context, running bool) (before, after Machine, res Result, err error) {
	verb, typ := "stop", sprint.NMachineStopped
	if running {
		verb, typ = "start", sprint.NMachineStarted
	}
	res = Result{Verb: verb}
	if before, _, err = st.Machine(ctx); err != nil {
		return before, before, res, err
	}
	if before.Running() == running && before.Cause != "" {
		// A stop of a machine STOPPED by itself (the sprint done) keeps it
		// STOPPED and takes the cause off: it is a stop by hand now, and the
		// view says STOPPED. No span opens and no note is written.
		after = before
		after.Cause = ""
		if err := st.putMachine(ctx, after); err != nil {
			return before, before, res, err
		}
		return before, after, res, nil
	}
	if before.Running() == running {
		// The record is not written; the view's state is written again from
		// it, so a view that lost its state (or was stored before it had
		// one) shows the machine as it is (section 1).
		if kv, ok := st.B.(KV); ok {
			if err := kv.ShowState(ctx, st.Names.View(), ViewState(before)); err != nil {
				return before, before, res, err
			}
		}
		return before, before, res, nil
	}
	now := st.now()
	after = before
	after.Spans = append([]Span(nil), before.Spans...)
	if running {
		if n := len(after.Spans); n > 0 && after.Spans[n-1].To.IsZero() {
			after.Spans[n-1].To = now
			after.StoppedFor += now.Sub(after.Spans[n-1].From)
		}
		after.State = Running
	} else {
		after.Spans = append(after.Spans, Span{From: now})
		if len(after.Spans) > MaxStopSpans {
			after.Spans = after.Spans[len(after.Spans)-MaxStopSpans:]
		}
		after.State = Stopped
	}
	after.Since, after.Who, after.Cause = now, st.Actor, ""
	if err := st.putMachine(ctx, after); err != nil {
		return before, before, res, err
	}
	res, err = st.Run(ctx, Step{Verb: verb, Plan: func(s *sprint.Snapshot) sprint.Plan {
		n := sprint.Note{Kind: sprint.Happened, Type: typ, Who: st.Actor, At: s.Now,
			What: fmt.Sprintf("%s -> %s by %s", before.StateWord(), after.StateWord(), st.Actor)}
		return sprint.Plan{Notes: []sprint.Note{n}}
	}})
	res.Moved = []string{fmt.Sprintf("machine %s -> %s", before.StateWord(), after.StateWord())}
	return before, after, res, err
}

// PartResult is what one part of a tick did.
type PartResult struct {
	Name string `json:"name"`
	Result
}

// TickResult is what a tick did: the state it found, whether it was idle
// (every table read and planned, and none had anything to do), the pending
// operation it finished,
// its parts that wrote, why it stopped short (the sprint's epoch changed
// under it, or the machine was stopped), and how many moves and judgments it
// left due past its bounds.
type TickResult struct {
	State string `json:"state"`
	// TickEnd is the count of the tick-end note the tick wrote (tickend.go):
	// the notes for the coordinator it covered, 0 for none written.
	TickEnd  int            `json:"tick_end,omitempty"`
	Idle     bool           `json:"idle,omitempty"`
	Repaired []RepairResult `json:"repaired,omitempty"`
	Parts    []PartResult   `json:"parts,omitempty"`
	Stale    string         `json:"stale,omitempty"`
	Halted   string         `json:"halted,omitempty"`
	Due      int            `json:"due,omitempty"`
	// Tables is each of the four tables, in the store's order, with the rows
	// the tick's parts changed in it: every tick reads and plans every table
	// (errata 3 amendment 10: "each table should be updated per-tick at least
	// once"), and a table with nothing to do shows no rows.
	Tables []TableRows `json:"tables"`
	// Done is the words of "the sprint is done" when this tick's done part
	// found the sprint done and stopped the machine (errata 3 amendment 6),
	// and Hint what to do next.
	Done string `json:"done,omitempty"`
	Hint string `json:"hint,omitempty"`
	// Epoch is the epoch the tick ran at: the log the run loop waits on
	// after it (waitlog.go).
	Epoch uint64 `json:"-"`
	// Order is the tables the tick updated, in the order it updated them:
	// work, readers, merge and fleet, then each table another update wrote,
	// in the order written, then "end" (errata 3 amendment 12).
	Order []string `json:"order,omitempty"`
	// Times is how long each part the tick ran took, in order, its drains
	// before it included: what a tick spends its time on.
	Times []PartTime `json:"times,omitempty"`
}

// PartTime is one part of a tick and the time its step took.
type PartTime struct {
	Table string        `json:"table,omitempty"`
	Name  string        `json:"name"`
	Took  time.Duration `json:"took_ns"`
}

// TableRows is one table of a tick and the rows its parts changed in it.
type TableRows struct {
	Table string   `json:"table"`
	Rows  []string `json:"rows"`
}

// newTables is the four tables, none changed yet.
func newTables() []TableRows {
	out := make([]TableRows, len(All))
	for i, t := range All {
		out[i] = TableRows{Table: t, Rows: []string{}}
	}
	return out
}

// addRows adds the rows a part's plan changed to the tick's tables.
func (r *TickResult) addRows(rows map[string][]string) {
	for i := range r.Tables {
		for _, row := range rows[r.Tables[i].Table] {
			if !slices.Contains(r.Tables[i].Rows, row) {
				r.Tables[i].Rows = append(r.Tables[i].Rows, row)
			}
		}
		slices.Sort(r.Tables[i].Rows)
	}
}

// Moved is every line the tick's parts moved.
func (r TickResult) Moved() []string {
	var out []string
	for _, p := range r.Parts {
		out = append(out, p.Moved...)
	}
	return out
}

// Notes is the notifications the tick wrote.
func (r TickResult) Notes() int {
	n := 0
	for _, p := range r.Parts {
		n += p.Notes
	}
	return n
}

// tickExtras is what the tick's parts read beyond the placed cards: the needs
// of waiting primaries that are off the table, which may have been dropped.
func tickExtras(s *sprint.Snapshot) map[string][]string {
	// and the read card ids the ask part could create for primaries in review
	// with no read card placed at their attempt: one retired there means that
	// reader already read it.
	var reads []string
	if s.Readers != nil {
		for _, c := range s.Work.Column(sprint.Review) {
			attempt := c.Int("attempt")
			placed := false
			for _, rd := range s.Readers.Rows() {
				placed = placed || s.Readers.Placed(sprint.ReadCardID(c.ID, attempt, rd)) != nil
			}
			if placed {
				continue
			}
			for _, rd := range s.Readers.Rows() {
				reads = append(reads, sprint.ReadCardID(c.ID, attempt, rd))
			}
		}
	}
	return map[string][]string{sprint.Work: sprint.ResolveExtras(s), sprint.Readers: reads}
}

// TickPartStep is one part of the tick as a step of the engine: fenced,
// replayable, planned again on a fresh read when it loses to another writer.
// It holds the epoch the tick began at (nil is any): a clear since refuses it
// as stale. guard refuses the part on any other ground. due, when not nil, is
// set to what the last plan left due past the part's bounds.
func TickPartStep(name string, fn sprint.TickPartFn, r sprint.TickReq, epoch *uint64, guard func(*sprint.Snapshot) string, due *int) Step {
	return Step{Verb: "tick " + name, Actor: sprint.MachineActor, Load: All, Extras: tickExtras, Epoch: epoch,
		Mirrors: name == "presence" || name == "deal" || name == "level" || name == "resume",
		Plan: func(s *sprint.Snapshot) sprint.Plan {
			if guard != nil {
				if why := guard(s); why != "" {
					return sprint.Plan{Refused: []sprint.Refusal{{Key: "tick", Why: why}}}
				}
			}
			p, d := fn(s, r)
			if due != nil {
				*due = d
			}
			return unchangedNotWritten(s, p)
		}}
}

// unchangedNotWritten is the plan of a part with the writes that change no
// row taken out: an entry that only sets fields to the values the card holds
// is no write (it keeps the guard of the card it names), and a unit that held
// nothing else is passed over. A table's queue holds an entry only when a
// write changed a row, so an update that finds nothing to change leaves every
// queue as it was and the tick ends (tla/DirtyTick.tla, W13: an update that
// queues an entry each time it runs, changed or not, never ends the tick). An
// entry that changes a row in any field is written as the part planned it.
func unchangedNotWritten(s *sprint.Snapshot, p sprint.Plan) sprint.Plan {
	var units []sprint.Unit
	for i, u := range p.Units {
		var changes []sprint.Change
		had, kept := false, false
		for _, ch := range u.Changes {
			e, c := ch.Entry, s.T(ch.Table).Card(ch.Entry.ID)
			if c != nil && c.Placed() && e.Create == nil && e.Move == nil && !e.Remove && len(e.Unset) == 0 && len(e.Set) > 0 {
				same := true
				for k, v := range e.Set {
					same = same && c.Has(k) && c.F(k) == v
				}
				if same {
					if units == nil {
						units = slices.Clone(p.Units)
					}
					had = true
					e.Set = nil
					ch.Entry = e
				}
			}
			kept = kept || changesRow(ch.Entry)
			changes = append(changes, ch)
		}
		if !had {
			continue
		}
		u.Changes = changes
		if !kept && len(u.Notes) == 0 && len(u.Closes) == 0 && len(u.Bumps) == 0 {
			u.Changes, u.Moved = nil, ""
		}
		units[i] = u
	}
	if units == nil {
		return p
	}
	p.Units = slices.DeleteFunc(units, func(u sprint.Unit) bool {
		return u.Changes == nil && u.Moved == "" && len(u.Notes) == 0 && len(u.Closes) == 0 && len(u.Bumps) == 0
	})
	return p
}

// changesRow says a batch entry changes the row it names: it creates, moves
// or removes the card, or sets or unsets a field. An entry that only guards
// the card (its revision and place) changes none.
func changesRow(e ntable.BatchMemberEntry) bool {
	return e.Create != nil || e.Move != nil || e.Remove || len(e.Set) > 0 || len(e.Unset) > 0
}

// staleRefusal says a step holding epoch at was refused because the sprint
// left it.
func staleRefusal(refused []sprint.Refusal, at uint64) bool {
	for _, r := range refused {
		if r.Key == "epoch "+strconv.FormatUint(at, 10) {
			return true
		}
	}
	return false
}

// Tick runs one tick when the machine is RUNNING, and records it on the
// heartbeat, its error with it when it failed, with the count of failed
// ticks in a row; when the machine is STOPPED it moves nothing and only says
// it looked, and shows each fleet member's status and load as their beats
// say. A tick that did nothing writes the heartbeat at most once every
// HeartbeatIdleEvery. The tick holds the epoch it reads before the machine's
// state: every step it runs carries that epoch, and a clear since (which sets
// the machine STOPPED first) stops the tick without writing anything at the
// new epoch.
func (st *Store) Tick(ctx context.Context) (TickResult, error) {
	st, err := st.repin(ctx)
	if err != nil {
		return TickResult{}, err
	}
	m, hb, err := st.Machine(ctx)
	if err != nil {
		return TickResult{}, err
	}
	res := TickResult{State: m.StateWord(), Epoch: st.epoch}
	if !m.Running() {
		// A STOPPED machine moves nothing; the tick shows the fleet as its
		// beats say and says it looked, so start can tell a run loop is
		// waiting.
		shape, beats, err := st.fleetBeats(ctx, nil)
		if err == nil {
			_, err = st.showFleet(ctx, shape, beats, st.now())
		}
		if err != nil {
			return res, fmt.Errorf("fleet: %w", err)
		}
		now := st.now()
		if now.Sub(hb.Alive()) < HeartbeatIdleEvery && !hb.Looked.IsZero() {
			return res, nil
		}
		hb.Looked = now
		return res, st.putJSON(ctx, keyHeartbeat, hb)
	}
	seen, err := st.tick(ctx, m, hb, &res)
	if err == nil && res.Halted == "" && res.Done == "" {
		// The reminder duty is a part too: it begins only while RUNNING.
		if halted, herr := st.halted(ctx, &res, "remind"); herr != nil {
			err = herr
		} else if !halted {
			if rerr := st.remind(ctx, m, &res); rerr != nil {
				err = fmt.Errorf("remind: %w", rerr)
			}
		}
	}
	if err == nil && res.Stale == "" {
		// the coordinator's one wake of the tick, last (tickend.go); a tick the
		// clear overtook writes nothing more
		ended := time.Now()
		res.TickEnd, err = st.tickEnd(ctx)
		res.Times = append(res.Times, PartTime{Name: "tick end", Took: time.Since(ended)})
	}
	now := st.now()
	if err == nil && res.Idle && res.Halted == "" && len(res.Parts) == 0 && hb.Error == "" && now.Sub(hb.At) < HeartbeatIdleEvery && !hb.At.Before(m.Since) &&
		seen.Revisions == hb.Revisions && slices.Equal(seen.Fresh, hb.Fresh) {
		return res, nil
	}
	hb.At, hb.Ticks = now, hb.Ticks+1
	if err != nil {
		// A failed tick leaves a full read due: what it did not finish is
		// read from the state by the next tick.
		hb.Error, hb.Failures, hb.Full = err.Error(), hb.Failures+1, time.Time{}
	} else {
		hb.Error, hb.Failures = "", 0
		hb.Revisions, hb.Landed, hb.All, hb.Full, hb.Fresh = seen.Revisions, seen.Landed, seen.All, seen.Full, seen.Fresh
		hb.Due = res.Due
	}
	if werr := st.putJSON(ctx, keyHeartbeat, hb); werr != nil && err == nil {
		err = werr
	}
	return res, err
}

// halted reads the machine's state before a part of a tick begins: STOPPED
// halts the tick there, and says so on the result.
func (st *Store) halted(ctx context.Context, res *TickResult, part string) (bool, error) {
	var m Machine
	if err := st.getJSON(ctx, keyMachine, &m); err != nil {
		return false, err
	}
	if m.Running() {
		return false, nil
	}
	res.State = Stopped
	res.Halted = "the machine was stopped during the tick: the part " + part + " did not begin"
	return true, nil
}

// look is the cheap read of a tick: the four tables' shapes in one exchange,
// their revisions, and the landed and all primaries of the work table.
func (st *Store) look(ctx context.Context) (Heartbeat, []ntable.Table, error) {
	var seen Heartbeat
	names := make([]string, len(All))
	for i, t := range All {
		names[i] = st.Names.Table(t)
	}
	shapes, err := st.B.Shapes(ctx, names)
	if err != nil {
		return seen, nil, err
	}
	for i, sh := range shapes {
		seen.Revisions[i] = sh.Revision
	}
	j := shapes[0].Column(sprint.Landed)
	for _, r := range shapes[0].Rows {
		for k, c := range shapes[0].Columns {
			if !c.HasSet() || k >= len(r.Cells) {
				continue
			}
			seen.All += r.Cells[k].Count
			if k == j {
				seen.Landed += r.Cells[k].Count
			}
		}
	}
	return seen, shapes, nil
}

// tick finishes a pending operation past its grace (T5), then reads the whole
// sprint and plans every part over all four tables, every tick (errata 3
// amendment 10), each part that has something to do run as its own operation
// on a fresh read, the presence part with the beats read. A part is not run
// when it has
// nothing to do on the tick's first read and nothing has moved before it in
// this tick. Before each part it reads the machine's state: STOPPED halts the
// tick there. It returns what it saw, for the heartbeat; a tick that did not
// finish what was due (a part that lost to other writers, a stale epoch, a
// halt, or moves due past a bound) leaves a full read due (Full zero), so the
// next tick reads the state and does the rest.
func (st *Store) tick(ctx context.Context, m Machine, last Heartbeat, res *TickResult) (Heartbeat, error) {
	f, err := st.B.ReadFence(ctx)
	if err != nil {
		return last, err
	}
	if f.Pending != nil && st.now().Sub(f.Pending.At) >= st.grace() {
		if _, left, err := st.left(ctx); err != nil || left {
			if left {
				res.Stale = fmt.Sprintf("the sprint was cleared before the tick's repair (epoch %d): the tick stops here", st.epoch)
			}
			return last, err
		}
		rr, err := st.Repair(ctx)
		res.Repaired = rr
		if err != nil {
			return last, err
		}
		for _, r := range rr {
			if r.Done == RepairOpen {
				if err := st.markStuck(ctx, *f.Pending, r.Detail); err != nil {
					return last, err
				}
				return last, fmt.Errorf("operation %s (%s) is pending past its grace and the tick could not finish it: %s; run: nova-sprint repair", r.Op, r.Verb, r.Detail)
			}
		}
	}
	if _, ok, err := st.stuck(ctx); err != nil {
		return last, err
	} else if ok {
		if halted, err := st.halted(ctx, res, "stuck"); err != nil || halted {
			return last, err
		}
		// A stuck operation repaired since: its judgment, once, by a step
		// that writes nothing else.
		at := st.epoch
		r, err := st.Run(ctx, Step{Verb: "tick stuck", Actor: sprint.MachineActor, Epoch: &at, Plan: func(*sprint.Snapshot) sprint.Plan { return sprint.Plan{} }})
		if err != nil {
			return last, fmt.Errorf("tick stuck: %w", err)
		}
		if staleRefusal(r.Refused, at) {
			res.Stale = fmt.Sprintf("the sprint was cleared during the tick (epoch %d): the tick stops here", at)
			return last, nil
		}
		res.Parts = append(res.Parts, PartResult{Name: "stuck", Result: r})
	}
	// An unknown machine that beats is told of once.
	if r, err := st.tellStrangers(ctx); err != nil {
		if st.clearedUnder(ctx, res) {
			return last, nil
		}
		return last, fmt.Errorf("tick strangers: %w", err)
	} else if r.Notes > 0 {
		res.Parts = append(res.Parts, PartResult{Name: "strangers", Result: r})
	}
	seen, shapes, err := st.look(ctx)
	if err != nil {
		return last, err
	}
	fleet, beats, err := st.fleetBeats(ctx, shapes)
	if err != nil {
		return last, err
	}
	now := st.now()
	seen.Fresh = freshOf(fleet, beats, now)
	// Every tick reads and plans every table, whatever changed since the last
	// (errata 3 amendment 10: "each table should be updated per-tick at least
	// once"): no tick is skipped because nothing changed, and no part waits for
	// a full read due every so often.
	seen.Full = now
	// Every fleet cell up to date before the parts, the control cards read:
	// the revisions it leaves are what this tick saw.
	synced := time.Now()
	wrote, err := st.SyncFleet(ctx)
	res.Times = append(res.Times, PartTime{Name: "fleet display", Took: time.Since(synced)})
	if err != nil && st.clearedUnder(ctx, res) {
		return last, nil
	}
	if err != nil {
		return last, err
	}
	if wrote {
		again, _, err := st.look(ctx)
		if err != nil {
			return last, err
		}
		seen.Revisions = again.Revisions
	}
	// The tick holds the epoch of its first read: a clear during the tick
	// refuses the next part as stale, and the tick stops there; the next tick
	// reads the new epoch.
	pinned, err := st.pin(ctx)
	if err != nil {
		return last, err
	}
	read := time.Now()
	snap, _, err := pinned.Fenced(withBudget(ctx), All, tickExtras, nil)
	res.Times = append(res.Times, PartTime{Name: "first read", Took: time.Since(read)})
	unfinished := seen
	unfinished.Full = time.Time{}
	if errors.Is(err, errCleared) {
		res.Stale = "the sprint was cleared as the tick read it: the tick stops here"
		return unfinished, nil
	}
	if err != nil {
		return last, err
	}
	at := snap.Epoch
	res.Tables = newTables()
	req := sprint.TickReq{Who: sprint.MachineActor, Stopped: m.StoppedBetween, Beats: beats, Started: m.FirstStart(snap.Cleared)}
	t := &tickRun{st: st, ctx: ctx, res: res, req: req, at: at, snap: snap, queues: map[string]int{}}
	updates := st.Updates
	if updates == nil {
		updates = sprint.TickTables
	}
	byTable := map[string]sprint.TableUpdate{}
	for _, u := range updates {
		byTable[u.Table] = u
	}
	// 1-2. The first pass: every table's update once, in the owner's order
	// ("1. work streams, 2. readers, 3. merge, 4. fleet"); the work table's is
	// the pump, and it runs only here.
	for _, u := range updates {
		if out := t.update(u); out != tickOn {
			return t.end(out, last, unfinished, seen)
		}
	}
	// 3. "dirty bits are acted on IMMEDIATELY", and "the tick doesn't end
	// until all dirty bits are cleared": a table another update wrote is
	// updated next, in the order the tables were written, until no queue
	// holds anything. The work table's queue waits for the next tick's pump.
	for n := 0; len(t.dirtied) > 0; n++ {
		if n == MaxSettle {
			return last, fmt.Errorf("the tick did not settle: after %d updates past the first pass the tables %s are still written by each other's updates", MaxSettle, strings.Join(t.dirtied, ", "))
		}
		if out := t.update(byTable[t.dirtied[0]]); out != tickOn {
			return t.end(out, last, unfinished, seen)
		}
	}
	// 4. The end: the checks, the deadlines, the overdue judgments and the
	// done part, once the tables are settled.
	t.res.Order = append(t.res.Order, "end")
	if out := t.parts("", sprint.TickEnd); out != tickOn && out != tickDone {
		return t.end(out, last, unfinished, seen)
	}
	// 5. The tick-end note, the coordinator's one wake, is Tick's last step
	// (tickend.go).
	if t.lost {
		seen.Full = time.Time{}
	}
	if res.Due > 0 {
		seen.Full = time.Time{}
	}
	res.Idle = len(res.Parts) == 0 && len(res.Repaired) == 0
	if f.Pending != nil {
		// An operation in flight at the tick's start may have finished during
		// it (its writer, or a part that repaired it): its fleet cells are
		// brought up to date now, not left to the next tick.
		if _, err := st.SyncFleet(ctx); err != nil {
			return last, err
		}
	}
	// What it saw is the read before its own moves: a change by anyone after
	// that read, its own moves included, makes the next tick read the whole
	// sprint again, so nothing that happens during a tick is missed.
	return seen, nil
}

// MaxSettle bounds the updates a tick makes past its first pass while the
// readers', merge's and fleet's updates write each other's tables: a tick
// past it fails, naming the tables still written, so it never spins. Only
// the pump creates new work, so the chain ends (tla/DirtyTick.tla).
const MaxSettle = 64

// tickOutcome is how a table update or a part of a tick ended.
type tickOutcome int

const (
	tickOn      tickOutcome = iota // go on
	tickDone                       // the sprint is done and the machine stopped itself: the end is written
	tickHalted                     // the machine was stopped during the tick
	tickStale                      // the sprint was cleared during the tick
	tickCleared                    // the sprint was cleared as a part finished
	tickFailed                     // a part failed: err
)

// tickRun is one tick's updates as they run: the queue of each table other
// than the work table (the entries the tick's other updates wrote to it,
// which its update drains), and the order the tables were written in.
type tickRun struct {
	st      *Store
	ctx     context.Context
	res     *TickResult
	req     sprint.TickReq
	at      uint64
	snap    *sprint.Snapshot // the tick's first read
	ran     bool             // a part ran: every later part plans on a fresh read
	queues  map[string]int
	dirtied []string
	lost    bool
	err     error
}

// update is one table's update: its queue drained (the entries other updates
// wrote to it), then its parts in order.
func (t *tickRun) update(u sprint.TableUpdate) tickOutcome {
	t.res.Order = append(t.res.Order, u.Table)
	t.queues[u.Table] = 0
	t.dirtied = slices.DeleteFunc(t.dirtied, func(x string) bool { return x == u.Table })
	return t.parts(u.Table, u.Parts)
}

// parts runs parts of the table's update ("" for the end) in order, each as
// its own operation on a fresh read. A part is passed over when nothing has
// run yet in the tick and its plan on the tick's first read is empty.
func (t *tickRun) parts(table string, parts []sprint.TickPartDef) tickOutcome {
	for _, part := range parts {
		drain := part.Name == sprint.PartDrain && part.Fn == nil
		if !t.ran {
			if drain && t.snap.QueueLen == 0 {
				continue
			}
			if !drain {
				if p, due := part.Fn(t.snap, t.req); p.Empty() && due == 0 {
					continue
				}
			}
		}
		if halted, err := t.st.halted(t.ctx, t.res, part.Name); err != nil {
			t.err = err
			return tickFailed
		} else if halted {
			return tickHalted
		}
		due := 0
		var done *sprint.Note
		var planned sprint.Plan
		fn := func(s *sprint.Snapshot, r sprint.TickReq) (sprint.Plan, int) {
			if drain {
				planned = sprint.Drain(s, s.Queue, sprint.MachineActor)
				return planned, 0
			}
			p, d := part.Fn(s, r)
			planned = p
			if part.Name == sprint.PartDone {
				done = nil
				if len(p.Notes) > 0 {
					n := p.Notes[0]
					done = &n
				}
			}
			return p, d
		}
		step := TickPartStep(part.Name, fn, t.req, &t.at, nil, &due)
		step.Pump, step.Drain = table == sprint.Work, drain
		began := time.Now()
		r, err := t.st.Run(t.ctx, step)
		for _, d := range r.Drained {
			// a drain the part's step made before it planned is the tick's
			// move too: named in its report, never silent
			t.res.Parts = append(t.res.Parts, PartResult{Name: sprint.PartDrain, Result: d})
		}
		for i := 1; drain && err == nil && i < MaxDrains && len(planned.Requeue) > 0; i++ {
			// a card created and taken off the table in one queue: its
			// removal was left for the next drain, which runs now, so the
			// pump's resolve and deal never see it
			var more Result
			if more, err = t.st.Run(t.ctx, step); err == nil && len(more.Moved) > 0 {
				t.res.Parts = append(t.res.Parts, PartResult{Name: part.Name, Result: more})
				t.res.addRows(sprint.PlanRows(planned))
			}
		}
		t.res.Times = append(t.res.Times, PartTime{Table: table, Name: part.Name, Took: time.Since(began)})
		t.ran = true
		var cleared *ClearedError
		if errors.As(err, &cleared) {
			t.res.Stale = fmt.Sprintf("the sprint was cleared during the tick (epoch %d) as the part %s finished: the tick stops here", t.at, part.Name)
			return tickCleared
		}
		if len(r.Moved) > 0 || len(r.Refused) > 0 || r.Notes > 0 || len(r.Repaired) > 0 {
			t.res.Parts = append(t.res.Parts, PartResult{Name: part.Name, Result: r})
		}
		if err == nil && staleRefusal(r.Refused, t.at) {
			t.res.Stale = fmt.Sprintf("the sprint was cleared during the tick (epoch %d): the part %s was refused as stale and the tick stops here", t.at, part.Name)
			return tickStale
		}
		if err != nil {
			t.err = fmt.Errorf("tick %s: %w", part.Name, err)
			return tickFailed
		}
		if !r.Lost && len(r.Moved) > 0 {
			t.res.addRows(sprint.PlanRows(planned))
		}
		// Each table this part wrote, other than its own and the work table,
		// holds what it wrote in its queue until its update runs.
		for _, x := range All {
			if n := r.Tables[x]; n > 0 && x != table && x != sprint.Work {
				t.queues[x] += n
				if !slices.Contains(t.dirtied, x) {
					t.dirtied = append(t.dirtied, x)
				}
			}
		}
		if done != nil && r.Notes > 0 && !r.Lost {
			// The sprint is done: the machine stops itself as the part's step
			// commits, and the tick ends here (errata 3 amendment 6).
			if err := t.st.stopDone(t.ctx, *done, t.res); err != nil {
				t.err = fmt.Errorf("tick %s: stopping the machine: %w", part.Name, err)
				return tickFailed
			}
			return tickDone
		}
		t.res.Due += due
		if r.Lost {
			// The part lost every attempt to other writers: what it had to do
			// stays due, and the next tick reads it.
			t.lost = true
		}
	}
	return tickOn
}

// end is what the tick returns when an update or a part ended it early.
func (t *tickRun) end(out tickOutcome, last, unfinished, seen Heartbeat) (Heartbeat, error) {
	switch out {
	case tickFailed:
		return last, t.err
	case tickCleared:
		return last, nil
	case tickHalted, tickStale:
		return unfinished, nil
	}
	return seen, nil
}

// GetKey reads a machine record.
func (m *Mem) GetKey(_ context.Context, name string) (string, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.fail("kv"); err != nil {
		return "", false, err
	}
	v, ok := m.kv[name]
	return v, ok, nil
}

// SetKeyShowing writes a machine record and the view's state under one lock.
func (m *Mem) SetKeyShowing(_ context.Context, name, value, view, state string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.fail("kv"); err != nil {
		return err
	}
	if m.kv == nil {
		m.kv = map[string]string{}
	}
	m.kv[name] = value
	m.showState(view, state)
	return nil
}

// ShowState writes only the view's state.
func (m *Mem) ShowState(_ context.Context, view, state string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.fail("kv"); err != nil {
		return err
	}
	m.showState(view, state)
	return nil
}

func (m *Mem) showState(view, state string) {
	if v, ok := m.views[view]; ok {
		v.State = state
		m.views[view] = v
	}
}

// SetKey writes a machine record.
func (m *Mem) SetKey(_ context.Context, name, value string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.fail("kv"); err != nil {
		return err
	}
	if m.kv == nil {
		m.kv = map[string]string{}
	}
	m.kv[name] = value
	return nil
}

// GetKey reads a machine record.
func (r *Redis) GetKey(ctx context.Context, name string) (string, bool, error) {
	v, err := r.C.Get(ctx, r.Names.Key(name)).Result()
	if errors.Is(err, redis.Nil) {
		return "", false, nil
	}
	return v, err == nil, err
}

// SetKey writes a machine record.
func (r *Redis) SetKey(ctx context.Context, name, value string) error {
	return r.C.Set(ctx, r.Names.Key(name), value, 0).Err()
}

// SetKeyShowing writes a machine record and the view's state in one
// MULTI/EXEC: both or, when the store refuses the transaction, neither. A
// view that is not there is left alone and is no error.
func (r *Redis) SetKeyShowing(ctx context.Context, name, value, view, state string) error {
	var shown *redis.Cmd
	_, err := r.C.TxPipelined(ctx, func(p redis.Pipeliner) error {
		p.Set(ctx, r.Names.Key(name), value, 0)
		shown = ntable.QueueViewState(ctx, p, view, state)
		return nil
	})
	if err != nil {
		return err
	}
	return viewShown(ntable.ViewStateResult(view, shown))
}

// ShowState writes only the view's state.
func (r *Redis) ShowState(ctx context.Context, view, state string) error {
	return viewShown(ntable.ViewState(ctx, r.C, view, state))
}

// viewShown is a view state write's error, with a view that is not there no
// error: nothing shows the machine, so nothing can disagree with it.
func viewShown(err error) error {
	if errors.Is(err, ntable.ErrNoView) {
		return nil
	}
	return err
}

// GetKeys reads machine records in one call.
func (m *Mem) GetKeys(_ context.Context, names []string) ([]string, []bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.fail("kv"); err != nil {
		return nil, nil, err
	}
	vals, oks := make([]string, len(names)), make([]bool, len(names))
	for i, n := range names {
		vals[i], oks[i] = m.kv[n]
	}
	return vals, oks, nil
}

// GetKeys reads machine records in one round trip.
func (r *Redis) GetKeys(ctx context.Context, names []string) ([]string, []bool, error) {
	keys := make([]string, len(names))
	for i, n := range names {
		keys[i] = r.Names.Key(n)
	}
	got, err := r.C.MGet(ctx, keys...).Result()
	if err != nil {
		return nil, nil, err
	}
	vals, oks := make([]string, len(names)), make([]bool, len(names))
	for i := range names {
		if i < len(got) {
			vals[i], oks[i] = got[i].(string)
		}
	}
	return vals, oks, nil
}

// clearedUnder says the sprint left the tick's epoch (a clear) and marks the
// tick stale: an error of a read or write at the old epoch is that clear.
func (st *Store) clearedUnder(ctx context.Context, res *TickResult) bool {
	if _, left, err := st.left(ctx); err != nil || !left {
		return false
	}
	res.Stale = fmt.Sprintf("the sprint was cleared during the tick (epoch %d): the tick stops here", st.epoch)
	return true
}

// stopDone stops the machine because the sprint is done, as the done part's
// step commits (errata 3 amendment 6; sprint.TickDone): the record STOPPED
// with the cause sprint.DoneCause and a STOPPED span opened, the view's state
// DONE, in one write; then the coordinator's goal route, when the coordinator
// has a goal, is pushed the note, once. The tick's result says it. A record
// written by a stop or a clear since the tick read it is left as it is.
func (st *Store) stopDone(ctx context.Context, n sprint.Note, res *TickResult) error {
	m, _, err := st.Machine(ctx)
	if err != nil {
		return err
	}
	res.State, res.Done, res.Hint = Stopped, n.What, n.Hint
	if !m.Running() {
		return nil
	}
	now := st.now()
	after := m
	after.Spans = append(append([]Span(nil), m.Spans...), Span{From: now})
	if len(after.Spans) > MaxStopSpans {
		after.Spans = after.Spans[len(after.Spans)-MaxStopSpans:]
	}
	after.State, after.Since, after.Who, after.Cause = Stopped, now, sprint.MachineActor, sprint.DoneCause
	if err := st.putMachine(ctx, after); err != nil {
		return err
	}
	return st.pushDone(ctx, n, res)
}

// pushDone delivers "the sprint is done" down the route of the goal of the
// one it is addressed to (the coordinator), when they have a goal: the text
// the route carries is the note's, with the hint, and the goal's pushes are
// not counted. A route that fails is refused on the tick's result, as a
// reminder's is; the note stays in the inbox either way.
func (st *Store) pushDone(ctx context.Context, n sprint.Note, res *TickResult) error {
	if n.To == "" {
		return nil
	}
	g, err := st.Goals(ctx)
	if err != nil {
		return err
	}
	i := g.Find(n.To)
	if i < 0 {
		return nil
	}
	p := g.People[i]
	epoch, err := st.workEpoch(ctx)
	if err != nil {
		return err
	}
	r := Reminder{N: p.Count, To: p.Name, At: st.now(), Epoch: epoch,
		Text: sprint.NSprintDone + ": " + n.What + "\n" + n.Hint + "\n"}
	part := PartResult{Name: "remind", Result: Result{Verb: "tick remind"}}
	d, err := NewDeliverer(p.Route)
	if err == nil {
		err = d.Deliver(r)
	}
	if err != nil {
		part.Refused = append(part.Refused, sprint.Refusal{Key: p.Name, Why: "the sprint is done, pushed over " + p.Route + ", failed: " + err.Error()})
	} else {
		part.Moved = append(part.Moved, fmt.Sprintf("DONE to %s over %s", p.Name, p.Route))
	}
	res.Parts = append(res.Parts, part)
	return nil
}

// SinceFirstStart is the wall time from the machine's first start of the
// sprint's epoch to the clock's reading; false when it has not started in
// this epoch, or the records are not read.
func (st *Store) SinceFirstStart(ctx context.Context) (time.Duration, bool) {
	m, _, err := st.Machine(ctx)
	if err != nil {
		return 0, false
	}
	es, err := st.EpochNow(ctx)
	if err != nil {
		return 0, false
	}
	first, now := m.FirstStart(es.Cleared), st.now()
	if first.IsZero() || now.Before(first) {
		return 0, false
	}
	return now.Sub(first), true
}

// undone takes the cause off a machine STOPPED because the sprint was done
// once work is added to the sprint: it stays STOPPED, and the view says
// STOPPED, until the coordinator starts it (errata 3 amendment 6).
func (st *Store) undone(ctx context.Context) error {
	if _, ok := st.B.(KV); !ok {
		return nil
	}
	m, _, err := st.Machine(ctx)
	if err != nil || !m.Done() {
		return err
	}
	m.Cause = ""
	return st.putMachine(ctx, m)
}
