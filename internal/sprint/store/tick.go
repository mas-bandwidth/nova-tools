package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

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
	// MachineSilence is how long a RUNNING machine goes without a tick before
	// the sprint line says it is STOPPED.
	MachineSilence = 5 * time.Second
	// TickBackoffCap bounds the wait after consecutive failed ticks.
	TickBackoffCap = 5 * time.Second
	// MaxStopSpans bounds the STOPPED spans the state record keeps.
	MaxStopSpans = 1000
	// TickFullEvery is how often a tick reads the whole sprint when nothing
	// changed since the last tick: the deadlines and the check run on time.
	TickFullEvery = time.Minute
)

// KV is the part of a store the machine keeps its records in.
type KV interface {
	GetKey(ctx context.Context, name string) (string, bool, error)
	SetKey(ctx context.Context, name, value string) error
}

// Span is one time the machine was STOPPED; To is zero while it still is.
type Span struct {
	From time.Time `json:"from"`
	To   time.Time `json:"to,omitempty"`
}

// Machine is the state record: the state, when it last changed and by whom,
// the total time STOPPED before the current span, and the STOPPED spans.
// No record is STOPPED.
type Machine struct {
	State      string        `json:"state"`
	Since      time.Time     `json:"since"`
	Who        string        `json:"who,omitempty"`
	StoppedFor time.Duration `json:"stopped_ns"`
	Spans      []Span        `json:"spans,omitempty"`
}

// Heartbeat is the last tick: when, how many so far, and the error of the
// last tick if it failed, with the count of failures in a row.
type Heartbeat struct {
	At       time.Time `json:"at"`
	Ticks    int64     `json:"ticks"`
	Error    string    `json:"error,omitempty"`
	Failures int       `json:"failures,omitempty"`
	// What the last tick saw: the tables' revisions, the landed and all
	// primaries, and when it last read the whole sprint. An idle tick reads
	// the tables' shapes, finds them unchanged, and does nothing else.
	Revisions [4]uint64 `json:"revisions"`
	Landed    int64     `json:"landed"`
	All       int64     `json:"all"`
	Full      time.Time `json:"full"`
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
	var d time.Duration
	for _, s := range m.Spans {
		end := s.To
		if end.IsZero() || end.After(to) {
			end = to
		}
		start := s.From
		if start.Before(from) {
			start = from
		}
		if end.After(start) {
			d += end.Sub(start)
		}
	}
	return d
}

// StoppedTotal is the total time the machine has been STOPPED, until now.
func (m Machine) StoppedTotal(now time.Time) time.Duration {
	d := m.StoppedFor
	if n := len(m.Spans); n > 0 && m.Spans[n-1].To.IsZero() && now.After(m.Spans[n-1].From) {
		d += now.Sub(m.Spans[n-1].From)
	}
	return d
}

// MachineLine is the machine's part of the sprint line: running, STOPPED,
// or STOPPED because a RUNNING machine has not ticked for MachineSilence; a
// last tick that failed is shown with its error.
func MachineLine(now time.Time, m Machine, hb Heartbeat) string {
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
// read by run before every tick: a tick in flight finishes, and no tick
// begins after the flag says STOPPED.
func (st *Store) SetMachine(ctx context.Context, running bool) (before, after Machine, res Result, err error) {
	verb, typ := "stop", sprint.NMachineStopped
	if running {
		verb, typ = "start", sprint.NMachineStarted
	}
	res = Result{Verb: verb}
	if before, _, err = st.Machine(ctx); err != nil {
		return before, before, res, err
	}
	if before.Running() == running {
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
	after.Since, after.Who = now, st.Actor
	if err := st.putJSON(ctx, keyMachine, after); err != nil {
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
// (nothing changed since the last tick), the pending operation it finished,
// its parts that wrote, and why it stopped short (the sprint's epoch changed
// under it).
type TickResult struct {
	State    string         `json:"state"`
	Idle     bool           `json:"idle,omitempty"`
	Repaired []RepairResult `json:"repaired,omitempty"`
	Parts    []PartResult   `json:"parts,omitempty"`
	Stale    string         `json:"stale,omitempty"`
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
	return map[string][]string{sprint.Work: sprint.ResolveExtras(s)}
}

// TickPartStep is one part of the tick as a step of the engine: fenced,
// replayable, planned again on a fresh read when it loses to another writer.
// guard refuses the part when the sprint is not the one the tick began on.
func TickPartStep(name string, fn func(*sprint.Snapshot, sprint.TickReq) sprint.Plan, r sprint.TickReq, guard func(*sprint.Snapshot) string) Step {
	return Step{Verb: "tick " + name, Load: All, Extras: tickExtras,
		Mirrors: name == "deal" || name == "level" || name == "resume",
		Plan: func(s *sprint.Snapshot) sprint.Plan {
			if guard != nil {
				if why := guard(s); why != "" {
					return sprint.Plan{Refused: []sprint.Refusal{{Key: "tick", Why: why}}}
				}
			}
			return fn(s, r)
		}}
}

// epochs is the epochs of the four tables a snapshot read.
func epochs(s *sprint.Snapshot) [4]uint64 {
	var out [4]uint64
	for i, t := range []*sprint.Table{s.Work, s.Readers, s.Merge, s.Fleet} {
		if t != nil {
			out[i] = t.Epoch
		}
	}
	return out
}

// Tick runs one tick when the machine is RUNNING, and records it on the
// heartbeat, its error with it when it failed; when the machine is STOPPED
// it does nothing and writes nothing.
func (st *Store) Tick(ctx context.Context) (TickResult, error) {
	m, hb, err := st.Machine(ctx)
	if err != nil {
		return TickResult{}, err
	}
	res := TickResult{State: m.StateWord()}
	if !m.Running() {
		return res, nil
	}
	seen, err := st.tick(ctx, m, hb, &res)
	hb.At, hb.Ticks = st.now(), hb.Ticks+1
	hb.Error, hb.Failures = "", 0
	if err != nil {
		hb.Error, hb.Failures = err.Error(), hb.Failures+1
	} else {
		hb.Revisions, hb.Landed, hb.All, hb.Full = seen.Revisions, seen.Landed, seen.All, seen.Full
	}
	if werr := st.putJSON(ctx, keyHeartbeat, hb); werr != nil && err == nil {
		err = werr
	}
	return res, err
}

// look is the cheap read of a tick: the four tables' shapes in one exchange,
// their revisions, and the landed and all primaries of the work table.
func (st *Store) look(ctx context.Context) (Heartbeat, error) {
	var seen Heartbeat
	names := make([]string, len(All))
	for i, t := range All {
		names[i] = st.Names.Table(t)
	}
	shapes, err := st.B.Shapes(ctx, names)
	if err != nil {
		return seen, err
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
	return seen, nil
}

// tick finishes a pending operation past its grace (T5), then, when a table
// changed since the last tick, this is the first tick after start, or
// TickFullEvery has passed, runs each part that has something to do as its
// own operation on a fresh read. A part is skipped when it has nothing to do
// on the tick's first read and nothing has moved before it in this tick. It
// returns what it saw, for the heartbeat.
func (st *Store) tick(ctx context.Context, m Machine, last Heartbeat, res *TickResult) (Heartbeat, error) {
	f, err := st.B.ReadFence(ctx)
	if err != nil {
		return last, err
	}
	if f.Pending != nil && st.now().Sub(f.Pending.At) >= st.grace() {
		rr, err := st.Repair(ctx)
		res.Repaired = rr
		if err != nil {
			return last, err
		}
		for _, r := range rr {
			if r.Done == RepairOpen {
				return last, fmt.Errorf("operation %s (%s) is pending past its grace and the tick could not finish it: %s; run: nova-sprint repair", r.Op, r.Verb, r.Detail)
			}
		}
	}
	seen, err := st.look(ctx)
	if err != nil {
		return last, err
	}
	now := st.now()
	first := last.At.Before(m.Since)
	seen.Full = last.Full
	if !first && len(res.Repaired) == 0 && seen.Revisions == last.Revisions && now.Sub(last.Full) < TickFullEvery {
		res.Idle = true
		return seen, nil
	}
	seen.Full = now
	snap, _, err := st.Fenced(withBudget(ctx), All, tickExtras, nil)
	if err != nil {
		return last, err
	}
	at := epochs(snap)
	stale := ""
	guard := func(s *sprint.Snapshot) string {
		if epochs(s) != at {
			stale = "the sprint's epoch changed under the tick (a clear): the tick stops here"
			return stale
		}
		return ""
	}
	req := sprint.TickReq{Who: sprint.MachineActor, Stopped: m.StoppedBetween,
		Scan: first || seen.Landed != last.Landed || seen.All != last.All}
	dirty := false // something ran: every later part runs on a fresh read
	for _, part := range sprint.TickParts {
		if !dirty && part.Fn(snap, req).Empty() {
			continue
		}
		r, err := st.Run(ctx, TickPartStep(part.Name, part.Fn, req, guard))
		dirty = true
		if len(r.Moved) > 0 || len(r.Refused) > 0 || r.Notes > 0 || len(r.Repaired) > 0 {
			res.Parts = append(res.Parts, PartResult{Name: part.Name, Result: r})
		}
		if stale != "" {
			res.Stale = stale
			return seen, nil
		}
		if err != nil {
			return last, fmt.Errorf("tick %s: %w", part.Name, err)
		}
	}
	// What it saw is the read before its own moves: a change by anyone after
	// that read, its own moves included, makes the next tick read the whole
	// sprint again, so nothing that happens during a tick is missed.
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
	v, err := r.C.Get(ctx, r.key(name)).Result()
	if errors.Is(err, redis.Nil) {
		return "", false, nil
	}
	return v, err == nil, err
}

// SetKey writes a machine record.
func (r *Redis) SetKey(ctx context.Context, name, value string) error {
	return r.C.Set(ctx, r.key(name), value, 0).Err()
}
