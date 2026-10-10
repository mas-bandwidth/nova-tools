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
	// TickBusyRetries is how many times a tick runs its parts again, within
	// the same tick, when other operations kept the fence moving under it
	// (FenceBusyError) before the tick counts as failed: on 2026-10-06, under
	// load, a tick's read lost the fence 12 times in 3 s while the verbs it
	// raced finished, and the next try of the parts would have passed.
	TickBusyRetries = 3
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
	State string    `json:"state"`
	Since time.Time `json:"since"`
	Who   string    `json:"who,omitempty"`
	// RunSeq identifies each explicit START, including two at the same clock
	// reading. A delayed tick may stop only the run it observed.
	RunSeq uint64 `json:"run_seq,omitempty"`
	// StopIssued distinguishes a STOP (manual or automatic) from the initial
	// STOPPED setup record, which has never had an active run.
	StopIssued bool `json:"stop_issued,omitempty"`
	// StopDebt is the active owner leases captured when this run stopped.
	// Only a same-owner stop-return at the captured generation settles one;
	// START checks the receipts against the live tables under its fence.
	StopDebt   []StopLease   `json:"stop_debt,omitempty"`
	StoppedFor time.Duration `json:"stopped_ns"`
	Spans      []Span        `json:"spans,omitempty"`
	// Cause is why a STOPPED machine stopped when it stopped itself:
	// sprint.DoneCause when the tick's done part found the sprint done.
	// A stop by hand, a clear, and a start leave it empty; an add of work
	// to a done sprint empties it too, as the sprint is no longer done.
	Cause string `json:"cause,omitempty"`
	// Reason and Until are a stop by hand's --reason and --until
	// (docs/SPEC-SPRINT.md section 14): why, and when the tick starts the
	// machine itself again; a start, and the machine's own stops, leave
	// them empty.
	Reason string    `json:"reason,omitempty"`
	Until  time.Time `json:"until,omitzero"`
}

// StopLease is one child the owner must confirm stopped before a new run.
type StopLease struct {
	Table string `json:"table"`
	Row   string `json:"row"`
	ID    string `json:"id"`
	Gen   int    `json:"gen"`
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
	// TickOverrun is how many ticks of a run loop ran past their deadline and
	// were given up, the loop going on (CountTickOverrun; docs/SPEC-SPRINT.md
	// section 14, The server, "The tick's deadline").
	TickOverrun int64 `json:"tick_overrun,omitempty"`
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
	// Quiet is the judgments the last tick's lane check kept from rising
	// (sprint.LaneChecked): a friend's lane live inside its cap, readers busy,
	// a read tier question the rule answered. Its length is the count the
	// coordinator reads beside the judgments that rose.
	Quiet []sprint.LaneQuiet `json:"quiet,omitempty"`
	// Suppressed is the count of the judgments the lane check kept from
	// rising since the epoch began, by cause (sprint.Suppressed.Counted):
	// what view coordinator prints as suppressed.
	Suppressed sprint.Suppressed `json:"suppressed,omitzero"`
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

// stopRevoked includes older machine records written before StopIssued was
// persisted, so a restarted binary still fences their stopped runs
// (tla/StopReturn.tla Stop and Report).
func (m Machine) stopRevoked() bool {
	return !m.Running() && (m.StopIssued || m.RunSeq > 0 || m.Reason != "" || m.Cause != "" || len(m.StopDebt) > 0)
}

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
// STOPPED); DONE alone while it is STOPPED because the sprint is done; none,
// so the counts show, while it is RUNNING.
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

// MachineLine is the machine's part of the sprint line: the state word alone,
// running, STOPPED or DONE, with no suffix of any kind but a RUNNING machine's
// late tick: "running (tick late 16s)", the whole seconds since its last tick,
// once that is longer ago than MachineSilence (docs/SPEC-SPRINT.md section 14),
// and a STOPPED machine's why: the cause of its own stop, or a stop by hand's
// who, reason and back-by time (section 14). STOPPED is a stop's alone, the
// record's state; a late tick is never one. A silent loop, a failing tick and
// moves due are the inbox's judgments.
func MachineLine(now time.Time, m Machine, hb Heartbeat) string {
	if m.Done() {
		return "machine: " + DoneState
	}
	if !m.Running() && m.Cause == sprint.FundsCause {
		return "machine: STOPPED (" + sprint.FundsCause + ")"
	}
	if !m.Running() && m.Reason != "" && !m.Until.IsZero() {
		// a stop by hand says who, why and when it is back (section 14)
		return "machine: " + sprint.StoppedText(m.Who, m.Reason, m.Until, now)
	}
	if !m.Running() {
		return "machine: STOPPED"
	}
	last := hb.At
	if m.Since.After(last) {
		last = m.Since
	}
	if late := now.Sub(last); late > MachineSilence {
		// running, late: the server ticks 7 to 16 s apart at times, and a gap
		// is not a stop (section 14)
		return fmt.Sprintf("machine: running (tick late %ds)", int64(late/time.Second))
	}
	return "machine: running"
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

// CountTickOverrun adds one to the heartbeat's count of ticks given up past
// their deadline (Heartbeat.TickOverrun), moves its clock (At, Ticks) as a tick
// would, and returns the count. A tick given up writes no heartbeat, and the
// server is alive: without the move, a deadline past MachineSilence would read
// as a silent machine (machine:silent, where's record not kept). The run loop
// calls it once the tick it gave up has ended, so no tick writes the heartbeat
// beside it; a tick after reads the count with the heartbeat and keeps it.
func (st *Store) CountTickOverrun(ctx context.Context) (int64, error) {
	var hb Heartbeat
	if err := st.getJSON(ctx, keyHeartbeat, &hb); err != nil {
		return 0, err
	}
	hb.TickOverrun++
	hb.At, hb.Ticks = st.now(), hb.Ticks+1
	return hb.TickOverrun, st.putJSON(ctx, keyHeartbeat, hb)
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
// "" when the store keeps no machine records. A store ticked by hand (ByHand)
// has no silence: nothing ticks between its commands, so a RUNNING machine
// is running however long since its last tick.
func (st *Store) MachineLine(ctx context.Context) string {
	m, hb, err := st.Machine(ctx)
	if err != nil {
		return ""
	}
	return st.MachineLineOf(m, hb)
}

// MachineLineOf is MachineLine of the records read.
func (st *Store) MachineLineOf(m Machine, hb Heartbeat) string {
	if st.ByHand && m.Running() {
		return "machine: running"
	}
	return MachineLine(st.now(), m, hb)
}

// SetMachine sets the state RUNNING (start) or STOPPED (stop). Setting the
// state it has changes nothing and writes nothing. A change is recorded as a
// happened notification (who, when); a STOPPED span is opened by stop and
// closed by start, and its time added to the total time STOPPED. The flag is
// read by the tick before every part: the part in flight finishes, and no
// part begins after the flag says STOPPED. A stop with no reason (a clear's)
// of a machine stopped by hand takes the stop's reason and back-by time off,
// so nothing starts it again (section 14).
func (st *Store) SetMachine(ctx context.Context, running bool) (before, after Machine, res Result, err error) {
	return st.setMachine(ctx, running, st.Actor, "", time.Time{})
}

// StopUntil is a stop by hand (docs/SPEC-SPRINT.md section 14): the machine
// STOPPED, recorded with who, the reason and the time it is back by, when the
// tick starts it again (backAt). A stop of a machine STOPPED already is a stop
// again: the reason and the time are replaced, and the span goes on.
func (st *Store) StopUntil(ctx context.Context, reason string, until time.Time) (before, after Machine, res Result, err error) {
	return st.setMachine(ctx, false, st.Actor, reason, until)
}

// setMachine is start, stop, and a stop by hand with its reason and time, as
// who (docs/SPEC-SPRINT.md section 14).
func (st *Store) setMachine(ctx context.Context, running bool, who, reason string, until time.Time) (before, after Machine, res Result, err error) {
	verb, typ := "stop", sprint.NMachineStopped
	if running {
		verb, typ = "start", sprint.NMachineStarted
	}
	res = Result{Verb: verb}
	var changed bool
	before, after, changed, err = st.machineTransition(ctx, running, who, reason, until)
	if err != nil {
		return before, before, res, err
	}
	if !changed {
		return before, after, res, nil
	}
	what := fmt.Sprintf("%s -> %s by %s", before.StateWord(), after.StateWord(), who)
	if reason != "" {
		what = fmt.Sprintf("%s -> %s", before.StateWord(), sprint.StoppedText(who, reason, until, after.Since))
	}
	res, err = st.Run(ctx, Step{Verb: verb, Plan: func(s *sprint.Snapshot) sprint.Plan {
		n := sprint.Note{Kind: sprint.Happened, Type: typ, Who: who, At: s.Now, What: what}
		return sprint.Plan{Notes: []sprint.Note{n}}
	}})
	res.Moved = []string{fmt.Sprintf("machine %s -> %s", before.StateWord(), after.StateWord())}
	return before, after, res, err
}

// backAt keeps the old tick hook inert: an official STOP's --until is display
// metadata, never permission to restart children without an explicit start.
func (st *Store) backAt(_ context.Context, m Machine, _ *TickResult) (Machine, error) {
	return m, nil
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
	// Quiet is the judgments the tick's lane check kept from rising, each
	// once (sprint.LaneChecked), kept on the heartbeat (Heartbeat.Quiet).
	Quiet []sprint.LaneQuiet `json:"quiet,omitempty"`
	// Tables is each of the four tables, in the store's order, with the rows
	// the tick's parts changed in it: every tick reads and plans every table,
	// each table updated at least once per tick, and a table with nothing to
	// do shows no rows.
	Tables []TableRows `json:"tables"`
	// Done is the words of "the sprint is done" when this tick's done part
	// found the sprint done and stopped the machine, and Hint what to do next.
	Done string `json:"done,omitempty"`
	Hint string `json:"hint,omitempty"`
	// Archive is the streams the tick archived, their last card landed, and
	// the archived ones it drew again, a card not landed in them again
	// (archive.go).
	Archive *ArchiveResult `json:"archive,omitempty"`
	// Epoch is the epoch the tick ran at: the log the run loop waits on
	// after it (waitlog.go).
	Epoch uint64 `json:"-"`
	// Order is the tables the tick updated, in the order it updated them:
	// work, readers, merge and fleet, then each table another update wrote,
	// in the order written, then "end".
	Order []string `json:"order,omitempty"`
	// Times is how long each part the tick ran took, in order, its drains
	// before it included: what a tick spends its time on.
	Times []PartTime `json:"times,omitempty"`
	// Took is the tick's wall time, from its first read of the machine's
	// state to its heartbeat.
	Took time.Duration `json:"took_ns"`
	// BusyRetries is how many times the tick ran its parts again because
	// other operations kept the fence moving under it (TickBusyRetries).
	BusyRetries int `json:"busy_retries,omitempty"`
	// RouteTrips is the round trips of the tick's one read of the routes
	// (routes.go): 1 with none, 2 with routes, 0 when no part dealt or checked.
	RouteTrips int64 `json:"route_trips,omitempty"`
	// Said is what the tick's reads met that it says once: a grant the
	// store's user lacks (its read then reads the table whole).
	Said []string `json:"said,omitempty"`
}

// PartTime is one part of a tick and the time its step took.
type PartTime struct {
	Table string        `json:"table,omitempty"`
	Name  string        `json:"name"`
	Took  time.Duration `json:"took_ns"`
	// Trips is the round trips the part made to the store, Reads the
	// whole-table reads it took, Rows the records its reads brought back,
	// and Stale the reads of the tick's twin it refused because another
	// writer wrote since (stats.go).
	Trips int64 `json:"trips"`
	Reads int64 `json:"reads"`
	Rows  int64 `json:"rows"`
	Stale int64 `json:"stale,omitempty"`
	// Mismatch is the tables the twin read whole because its records did
	// not add up to the store's counts (twin.go): 0 in a correct twin.
	Mismatch int64 `json:"mismatch,omitempty"`
	// Asked and Refused are the ask part's (tick_ask.go): the reads it asked
	// and the primaries it refused in this tick.
	Asked   int `json:"asked,omitempty"`
	Refused int `json:"refused,omitempty"`
	// Lost is the ask part's primaries of a batch that lost its tries and
	// was not tried again before the ask stopped: due for the next tick.
	Lost int `json:"lost,omitempty"`
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
	// and the read card ids the ask part could create for every primary in
	// review, at its attempt: one retired there (read, taken back or returned)
	// means that reader already had it, whatever is placed beside it (a read
	// placed on a reader since gone away is taken back by the same ask). Both
	// identities of a read are listed (sprint.ReadCardIDs): an away-retired
	// plain card is re-asked as .g1, and a retired .g1 must be in a sparse
	// snapshot too (docs/SPEC-SPRINT.md, the ask).
	var reads []string
	if s.Readers != nil {
		for _, c := range s.Work.Column(sprint.Review) {
			attempt := c.Int("attempt")
			for _, rd := range s.Readers.Rows() {
				for _, id := range sprint.ReadCardIDs(c.ID, attempt, rd) {
					if s.Readers.Placed(id) == nil {
						reads = append(reads, id)
					}
				}
			}
		}
	}
	// and the read cards of each primary in review at its attempt, on the fleet table
	// (sprint read_cards.go): retired with its verdict a read still stands (friendReadLive),
	// so an ok counts toward the read rule and a reader that closed or returned one is not
	// dealt the attempt again
	return map[string][]string{sprint.Work: sprint.ResolveExtras(s), sprint.Readers: reads, sprint.Fleet: sprint.ReadCardExtras(s)}
}

// TickPartStep is one part of the tick as a step of the engine: fenced,
// replayable, planned again on a fresh read when it loses to another writer.
// It holds the epoch the tick began at (nil is any): a clear since refuses it
// as stale. guard refuses the part on any other ground. due, when not nil, is
// set to what the last plan left due past the part's bounds.
func TickPartStep(name string, fn sprint.TickPartFn, r sprint.TickReq, epoch *uint64, guard func(*sprint.Snapshot) string, due *int) Step {
	return Step{Verb: "tick " + name, Actor: sprint.MachineActor, Load: All, Extras: tickExtras, Epoch: epoch,
		Mirrors: mirrors(name),
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
			// no judgment for a stream the tables lack: its next step is refused
			return unchangedNotWritten(s, sprint.ForTables(s, p))
		}}
}

// mirrors says the part's step brings the display cells up to date after it.
func mirrors(name string) bool {
	return name == "presence" || name == "deal" || name == "level" || name == "resume" || name == sprint.PartCapDeal || name == sprint.PartFriendStall
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
	return slices.ContainsFunc(refused, func(r sprint.Refusal) bool { return r.Key == "epoch "+strconv.FormatUint(at, 10) })
}

// Tick runs one tick when the machine is RUNNING, and records it on the
// heartbeat, its error with it when it failed, with the count of failed
// ticks in a row; when the machine is STOPPED it moves nothing and only says
// it looked, and shows each fleet member's status and load as their beats
// say. A tick that did nothing writes the heartbeat at most once every
// HeartbeatIdleEvery. A tick that fails with an error text the last one did
// not fail with writes one note to the coordinator, and the first tick that
// works after failures writes one more, with the count (section 14). The tick holds the epoch it reads before the machine's
// state: every step it runs carries that epoch, and a clear since (which sets
// the machine STOPPED first) stops the tick without writing anything at the
// new epoch.
func (st *Store) Tick(ctx context.Context) (res TickResult, err error) {
	began := time.Now()
	// the store the tick was given, never the one repin makes: a repin that fails returns
	// nil, and the notes are on the counters both share
	defer func(given *Store) { res.Said = append(res.Said, given.stats().takeNotes()...) }(st)
	st.stats()
	st.twin() // made on the store the run loop keeps: its ticks share it
	defer func() { res.Took = time.Since(began) }()
	st, err = st.repin(ctx)
	if err != nil {
		return TickResult{}, err
	}
	m, hb, err := st.Machine(ctx)
	if err != nil {
		return TickResult{}, err
	}
	res = TickResult{Epoch: st.epoch}
	// a stop by hand whose --until has come: the machine starts itself
	// (section 14) and this tick runs it
	if m, err = st.backAt(ctx, m, &res); err != nil {
		return res, fmt.Errorf("back: %w", err)
	}
	res.State = m.StateWord()
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
		// a card added to an archived stream draws it again, STOPPED or not
		if err := st.archivePart(ctx, false, &res); err != nil {
			return res, err
		}
		// a verb moves cards while the machine is STOPPED: where's record
		// follows them
		if err := st.keepWhere(ctx, m); err != nil {
			return res, fmt.Errorf("where: %w", err)
		}
		if err := st.stoppedAssignments(ctx, &res); err != nil {
			return res, err
		}
		if res.TickEnd, err = st.tickEnd(ctx); err != nil {
			return res, err
		}
		now := st.now()
		if now.Sub(hb.Alive()) < HeartbeatIdleEvery && !hb.Looked.IsZero() {
			return res, nil
		}
		hb.Looked = now
		return res, st.putJSON(ctx, keyHeartbeat, hb)
	}
	seen, err := st.tick(ctx, m, hb, &res)
	// other operations kept the fence moving under a part: its read changed
	// nothing, and the parts run again within this tick, up to TickBusyRetries
	// times, before the tick counts as failed
	for res.BusyRetries < TickBusyRetries && IsFenceBusy(err) && ctx.Err() == nil && res.Stale == "" && res.Halted == "" {
		res.BusyRetries++
		seen, err = st.tick(ctx, m, hb, &res)
	}
	if err == nil && res.Stale == "" && res.Halted == "" && res.Done == "" {
		// The reminder duty is a part too: it begins only while RUNNING.
		mt := st.meter()
		if halted, herr := st.halted(ctx, &res, "remind", m.RunSeq); herr != nil {
			err = herr
		} else if !halted {
			if rerr := st.remind(ctx, m, &res); rerr != nil {
				err = fmt.Errorf("remind: %w", rerr)
			}
		}
		res.Times = append(res.Times, mt.part("", "remind"))
	}
	if err == nil && res.Stale == "" && res.Halted == "" && res.Done == "" {
		// The timer duty is a part too: it begins only while RUNNING, and a
		// timer counts running time (timers.go).
		mt := st.meter()
		if halted, herr := st.halted(ctx, &res, "timers", m.RunSeq); herr != nil {
			err = herr
		} else if !halted {
			if terr := st.timers(ctx, m, &res); terr != nil {
				err = fmt.Errorf("timers: %w", terr)
			}
		}
		res.Times = append(res.Times, mt.part("", "timers"))
	}
	if err == nil && res.Stale == "" && hb.Failures > 0 {
		// the tick works again after failing: one note, with the count
		if nerr := st.tellTick(ctx, "tick recovered", sprint.NTickRecovered, fmt.Sprintf("failed=%d; the last error: %s", hb.Failures, hb.Error), ""); nerr != nil {
			err = fmt.Errorf("tick recovered: %w", nerr)
		}
	}
	if err == nil && res.Stale == "" {
		// the streams whose last card landed leave the tables (archive.go), the
		// last landing of a sprint the done part stopped included; a tick a stop
		// halted only draws again a stream with work in it
		mt := st.meter()
		err = st.archivePart(ctx, res.Halted == "", &res)
		res.Times = append(res.Times, mt.part("", "archive"))
	}
	if err == nil && res.Stale == "" {
		// where's record counted from what the tick left (where.go)
		mt := st.meter()
		if werr := st.keepWhere(ctx, m); werr != nil {
			err = fmt.Errorf("where: %w", werr)
		}
		res.Times = append(res.Times, mt.part("", "where"))
	}
	if err == nil && res.Stale == "" {
		// the coordinator's one wake of the tick, last (tickend.go); a tick the
		// clear overtook writes nothing more
		mt := st.meter()
		res.TickEnd, err = st.tickEnd(ctx)
		res.Times = append(res.Times, mt.part("", "tick end"))
	}
	defer func(mt meter) { res.Times = append(res.Times, mt.part("", "heartbeat")) }(st.meter())
	now := st.now()
	if err == nil && res.Idle && res.Halted == "" && len(res.Parts) == 0 && hb.Error == "" && now.Sub(hb.At) < HeartbeatIdleEvery && !hb.At.Before(m.Since) &&
		seen.Revisions == hb.Revisions && slices.Equal(seen.Fresh, hb.Fresh) && slices.Equal(res.Quiet, hb.Quiet) {
		return res, nil
	}
	failingSame := hb.Error != "" && !hb.At.Before(m.Since)
	prevError := hb.Error
	hb.At, hb.Ticks = now, hb.Ticks+1
	if err != nil && res.Stale == "" && (!failingSame || prevError != err.Error()) {
		// a failure with an error text the tick was not already failing with
		// in this run: one note, and the wake (best effort: the store that
		// failed the tick may refuse it)
		if st.tellTick(ctx, "tick failed", sprint.NTickFailed, fmt.Sprintf("tick %d failed at %s: %s", hb.Ticks, now.UTC().Format(time.RFC3339), err), "nova-sprint tick; nova-sprint check") == nil {
			_, _ = st.tickEnd(ctx) // ignored: the wake is best effort; the note is written, and the next tick end counts it
		}
	}
	if err != nil {
		// A failed tick leaves a full read due: what it did not finish is
		// read from the state by the next tick.
		hb.Error, hb.Failures, hb.Full = err.Error(), hb.Failures+1, time.Time{}
	} else {
		hb.Error, hb.Failures = "", 0
		hb.Revisions, hb.Landed, hb.All, hb.Full, hb.Fresh = seen.Revisions, seen.Landed, seen.All, seen.Full, seen.Fresh
		hb.Suppressed = hb.Suppressed.Counted(st.epoch, hb.Quiet, res.Quiet)
		hb.Due, hb.Quiet = res.Due, res.Quiet
	}
	if werr := st.putJSON(ctx, keyHeartbeat, hb); werr != nil && err == nil {
		err = werr
	}
	return res, err
}

// archivePart runs the tick's archive part (keepArchive) and puts what it did
// on the result.
func (st *Store) archivePart(ctx context.Context, running bool, res *TickResult) error {
	a, err := st.keepArchive(ctx, running)
	if err != nil {
		return fmt.Errorf("archive: %w", err)
	}
	if len(a.Archived)+len(a.Shown) > 0 {
		res.Archive = &a
	}
	return nil
}

// tellTick writes one happened note addressed to the coordinator about the
// tick itself, in a notes-only step of the machine's (docs/SPEC-SPRINT.md
// section 14, the inbox paragraph): the tick-end note
// counts it, so inbox --wait wakes on it.
func (st *Store) tellTick(ctx context.Context, verb, typ, what, hint string) error {
	to, err := st.B.Coordinator(ctx)
	if err != nil {
		return err
	}
	_, err = st.Run(ctx, Step{Verb: verb, Actor: sprint.MachineActor, Plan: func(s *sprint.Snapshot) sprint.Plan {
		return sprint.Plan{Notes: []sprint.Note{{Kind: sprint.Happened, Type: typ, Who: sprint.MachineActor, At: s.Now, To: to, What: what, Hint: hint}}}
	}})
	return err
}

// halted reads the machine's state before a part of a tick begins: STOPPED
// halts the tick there, and says so on the result.
func (st *Store) halted(ctx context.Context, res *TickResult, part string, runSeq uint64) (bool, error) {
	var m Machine
	if err := st.getJSON(ctx, keyMachine, &m); err != nil {
		return false, err
	}
	if m.RunSeq != runSeq {
		res.State = m.StateWord()
		res.Stale = "the machine restarted during the tick: the old " + part + " duty did not begin"
		return true, nil
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
// sprint and plans every part over all four tables, every tick, each part
// that has something to do run as its own operation on a fresh read, the
// presence part with the beats read. A part is not run
// when it has
// nothing to do on the tick's first read and nothing has moved before it in
// this tick. Before each part it reads the machine's state: STOPPED halts the
// tick there. It returns what it saw, for the heartbeat; a tick that did not
// finish what was due (a part that lost to other writers, a stale epoch, a
// halt, or moves due past a bound) leaves a full read due (Full zero), so the
// next tick reads the state and does the rest.
func (st *Store) tick(ctx context.Context, m Machine, last Heartbeat, res *TickResult) (Heartbeat, error) {
	looked := st.meter()
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
		if halted, err := st.halted(ctx, res, "stuck", m.RunSeq); err != nil || halted {
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
	res.Times = append(res.Times, looked.part("", "look"))
	// Every tick reads and plans every table, whatever changed since the last,
	// each table updated at least once per tick: no tick is skipped because
	// nothing changed, and no part waits for a full read due every so often.
	seen.Full = now
	// Every fleet cell up to date before the parts, the control cards read:
	// the revisions it leaves are what this tick saw.
	synced := st.meter()
	wrote, err := st.SyncFleet(ctx)
	res.Times = append(res.Times, synced.part("", "fleet display"))
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
	// The tick's one read of the sprint: its twin brought up to date
	// (twin.go), which every part then plans on; the run loop keeps it from
	// one tick to the next.
	twin := st.twin()
	read := st.meter()
	var snap *sprint.Snapshot
	if twin.mu.TryLock() {
		snap, _, err = pinned.twinRead(withBudget(ctx), twin, All, tickExtras, nil)
		twin.mu.Unlock()
	} else {
		// another step of this process holds the twin: this read is the store's
		st.stats().note("the tick's first read found its twin held by another step, and read the store whole")
		snap, _, err = pinned.Fenced(withBudget(ctx), All, tickExtras, nil)
	}
	res.Times = append(res.Times, read.part("", "first read"))
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
	req := sprint.TickReq{Who: sprint.MachineActor, Stopped: m.StoppedBetween, Beats: beats, Started: m.FirstStart(snap.Cleared), AnswerRules: st.AnswerRules, IdleAlarm: st.IdleAlarm, WakeFriend: st.WakeFriend}
	// the server's measured store round trip, for the store slow alarm (idle.go)
	if req.StoreRTTP50MS, req.StoreRTTFresh, err = st.StoreRTT(ctx); err != nil {
		return last, err
	}
	// the first read as it was: the twin it came from moves on with every
	// part's writes, and with any other writer in this process
	first := *snap
	first.Work, first.Readers, first.Merge, first.Fleet = snap.Work.Frozen(), snap.Readers.Frozen(), snap.Merge.Frozen(), snap.Fleet.Frozen()
	// who is up is read once, with the tick's one read: every part plans on it
	if err := pinned.readerStatesInto(ctx, &first); err != nil {
		return last, err
	}
	// the friends the deal may give a friend's card to and the level evens, read every
	// tick while the roster has one (sprint.TickDeal, sprint.FriendLevel)
	if req.Friends, err = pinned.friendSeats(ctx, &first, now); err != nil {
		return last, err
	}
	// each friend's session for the coordinator's pass (sprint.TickCoordinatorPass): every
	// friend of the roster, whether or not she holds a card; a sprint with no friend reads
	// the roster alone
	if req.Sessions, err = pinned.FriendSessions(ctx); err != nil {
		return last, err
	}
	t := &tickRun{st: st, ctx: ctx, res: res, req: req, at: at, runSeq: m.RunSeq, snap: &first, queues: map[string]int{}, twin: twin, readers: first.ReaderStates}
	defer func() { res.RouteTrips = t.routes.Trips }()
	updates := st.Updates
	if updates == nil {
		updates = sprint.TickTables
	}
	byTable := map[string]sprint.TableUpdate{}
	for _, u := range updates {
		byTable[u.Table] = u
	}
	// What names a stream the work and merge tables lack retires first, each with
	// a note (sprint.TickRetireGone): a judgment open before stream remove took its
	// stream off, whose next step would only be refused.
	if out := t.parts("", []sprint.TickPartDef{{Name: sprint.PartRetire, Fn: sprint.TickRetireGone}}); out != tickOn {
		return t.end(out, last, unfinished, seen)
	}
	// 0. The start: the fleet's and the readers' rebalance, once, before any
	// table's update (sprint.TickStart). What it writes is in the tables
	// the first pass updates next.
	t.res.Order = append(t.res.Order, "start")
	if out := t.parts("", sprint.TickStart); out != tickOn {
		return t.end(out, last, unfinished, seen)
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
	// the machine's own answers, when the loop gives them: the rule parts and the idle alarm
	end := sprint.TickEndWithStoppedAssignments(snap, t.req.AnswerRules, t.req.IdleAlarm)
	if out := t.parts("", end); out != tickOn && out != tickDone {
		return t.end(out, last, unfinished, seen)
	}
	if t.unshown {
		shown := st.meter()
		err := st.SyncMirrors(ctx)
		res.Times = append(res.Times, shown.part("", "display"))
		if err != nil {
			if st.clearedUnder(ctx, res) {
				return unfinished, nil
			}
			return last, err
		}
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

// routesPart says a tick part plans with the routes: the deal and the ask draw
// from them, and the check asks what the next deal does.
func routesPart(name string) bool {
	// the rebalance draws a route for a card it moves off a friend onto a machine
	// the readers' level too: a fleet reader whose row names no tier reads flash only while
	// the store holds routes (sprint fleetReadsFlashOnly), so the level plans with them
	return name == "deal" || name == sprint.PartRebalance || name == "ask" || name == "check" || name == sprint.PartLevelReads || sprint.IsRulePart(name)
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
	runSeq  uint64           // START generation observed before this tick began
	snap    *sprint.Snapshot // the tick's first read
	twin    *Twin            // the tick's twin: every part's read
	ran     bool             // a part ran: every later part plans on a fresh read
	queues  map[string]int
	dirtied []string
	lost    bool
	err     error
	// unshown says a part that brings the display cells up to date was
	// passed over after a part ran: the tick's end brings them up to date.
	unshown bool
	routes  RouteCache // read once, by the tick's first part that deals or checks
	// readers is each reader's state as the tick's first read found it (nil: a
	// store that keeps no beats).
	readers map[string]string
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
		// A part with nothing to do is passed over: on the tick's first read
		// until a part has run, and after, on the twin as the tick's own
		// writes left it (twin.go, peek), read from no store: what another
		// writer did since is the next tick's.
		view := t.snap
		if t.ran {
			view = t.st.peek(t.twin, table == sprint.Work)
		}
		if view != nil && view.ReaderStates == nil && t.readers != nil {
			v := *view
			v.ReaderStates = t.readers
			view = &v
		}
		if view != nil && view.Friends == nil && t.req.Friends != nil {
			// the friends are the tick's, read once: the ask asks a frontier read of
			// them, and the check asks what the ask does (sprint.enoughReadersUp)
			v := *view
			v.Friends = t.req.Friends
			view = &v
		}
		if view != nil && view.Routes == nil && routesPart(part.Name) {
			// a part that plans with the routes asks what it would do with them: the
			// deal's judgment of reads whose tier no route serves (route.go,
			// readRouteMissing) has nothing else to show it; read once a tick, the
			// cache the parts share
			set, err := t.st.cached(t.ctx, &t.routes)
			if err != nil {
				t.err = fmt.Errorf("tick %s: %w", part.Name, err)
				return tickFailed
			}
			v := *view
			set.into(&v)
			view = &v
		}
		if view != nil {
			if drain && view.QueueLen == 0 {
				continue
			}
			if !drain {
				// a plan made to see whether the part has work wakes no one
				probe := t.req
				probe.WakeFriend = nil
				p, due := part.Fn(view, probe)
				p = t.laneChecked(view, probe, part.Name, p)
				if p.Empty() && due == 0 {
					// a part that brings the display cells up to date after
					// it leaves them to the tick's end (tickRun.display)
					t.unshown = t.unshown || t.ran && mirrors(part.Name)
					continue
				}
			}
		}
		began := t.st.meter()
		due := 0
		var done *sprint.Note
		stop := "" // the deal's: every provider out of credit (sprint.FundsCause)
		var planned sprint.Plan
		// the stall ladder's wakes of the plan last made (sprint.TickFriendStall): kept,
		// never sent, as the part plans, and sent once its step commits (tickRun.wake;
		// tla/StallLadder.tla, NoWakeWithoutRung): a plan made again after a commit lost
		// to another writer wakes her once, not once a plan
		var wakes []stallWake
		fn := func(s *sprint.Snapshot, r sprint.TickReq) (sprint.Plan, int) {
			if drain {
				planned = sprint.Drain(s, s.Queue, sprint.MachineActor)
				return planned, 0
			}
			wakes = nil
			if s.Friends == nil {
				s.Friends = r.Friends
			}
			if r.WakeFriend != nil {
				r.WakeFriend = func(friend string, rung int, d time.Duration) error {
					wakes = append(wakes, stallWake{friend, rung, d})
					return nil
				}
			}
			p, d := part.Fn(s, r)
			// a judgment checks the lane before it rises (sprint.LaneChecked)
			p = t.laneChecked(s, r, part.Name, p)
			planned = p
			stop = p.Stop
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
		step.TickRunSeq = &t.runSeq
		step.Pump, step.Drain, step.Twin = table == sprint.Work, drain, t.twin
		// the deal and the ask draw from the routes (a read card's route,
		// route.go readRouteOf), and the check asks what the next deal does
		step.Routes, step.RouteCache = routesPart(part.Name), &t.routes
		// the ask, and the parts that ask what the ask does, plan with the readers' states
		step.Readers = part.Name == "ask" || part.Name == "check" || part.Name == sprint.PartLevelReads
		step.ReaderStates = t.readers
		// the machine's state is read with the step's fence: STOPPED halts the
		// tick before the part begins
		step.Halts = true
		var r Result
		var err error
		var asked *askTally
		if part.Name == "ask" {
			// the ask writes in small fenced steps within its budget (tick_ask.go)
			var tally askTally
			r, tally, err = t.askInSteps(step)
			asked = &tally
		} else {
			r, err = t.st.Run(t.ctx, step)
		}
		if err == nil && r.StaleRun {
			t.res.Stale = "the machine restarted during the tick: the part " + part.Name + " did not begin"
			t.res.State = Running
			if r.Halted {
				t.res.State = Stopped
			}
			return tickStale
		}
		if err == nil && r.Halted {
			t.res.State = Stopped
			t.res.Halted = "the machine was stopped during the tick: the part " + part.Name + " did not begin"
			return tickHalted
		}
		for _, d := range r.Drained {
			// a drain the part's step made before it planned is the tick's
			// move too: named in its report, never silent
			t.res.Parts = append(t.res.Parts, PartResult{Name: sprint.PartDrain, Result: d})
		}
		for i := 1; drain && err == nil && i < MaxDrains && len(planned.Requeue) > 0; i++ {
			// a card created and taken off the table in one queue: its
			// removal was left for the next drain, which runs now and takes
			// only what the drain before it requeued (a change queued since
			// waits for the next tick), so the pump's resolve and deal never
			// see the card
			again := step
			again.DrainMax = len(planned.Requeue)
			var more Result
			if more, err = t.st.Run(t.ctx, again); err == nil && len(more.Moved) > 0 {
				t.res.Parts = append(t.res.Parts, PartResult{Name: part.Name, Result: more})
				t.res.addRows(sprint.PlanRows(planned))
			}
		}
		pt := began.part(table, part.Name)
		if asked != nil {
			pt.Asked, pt.Refused, pt.Lost = asked.asked, asked.refused, asked.lost
		}
		t.res.Times = append(t.res.Times, pt)
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
		switch {
		case asked != nil:
			// the rows of the ask's steps that committed, not of its last plan
			t.res.addRows(asked.rows)
			if asked.unfinished {
				t.lost = true
				due += asked.left + asked.lost
			}
		case !r.Lost && len(r.Moved) > 0:
			t.res.addRows(sprint.PlanRows(planned))
		}
		if !r.Lost {
			t.wake(wakes)
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
		if stop != "" && !r.Lost {
			// Every provider is out of credit: the machine stops itself as the part's step
			// commits, with the cause, and the tick ends here (nova-tools#5199).
			if err := t.st.stopFor(t.ctx, sprint.FundsCause, stop, t.runSeq, t.res); err != nil {
				t.err = fmt.Errorf("tick %s: stopping the machine: %w", part.Name, err)
				return tickFailed
			}
			return tickDone
		}
		if done != nil && r.Notes > 0 && !r.Lost {
			// The sprint is done: the machine stops itself as the part's step
			// commits, and the tick ends here.
			if err := t.st.stopDone(t.ctx, *done, t.runSeq, t.res); err != nil {
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

// laneChecked is the part's plan with what the lane check keeps from rising
// taken out (sprint.LaneChecked), each quiet put on the tick's result once.
func (t *tickRun) laneChecked(s *sprint.Snapshot, r sprint.TickReq, part string, p sprint.Plan) sprint.Plan {
	p, quiet := sprint.LaneChecked(s, r, part, p)
	for _, q := range quiet {
		if !slices.ContainsFunc(t.res.Quiet, func(x sprint.LaneQuiet) bool { return x.Type == q.Type && x.Subject == q.Subject }) {
			t.res.Quiet = append(t.res.Quiet, q)
		}
	}
	return p
}

// stallWake is one wake of the friend stall ladder a part's plan made (rung 1 or 2).
type stallWake struct {
	friend string
	rung   int
	idle   time.Duration
}

// wake sends the stall ladder's wakes of a part's plan, its step committed
// (Store.WakeFriend; tla/StallLadder.tla, WokenAtEveryWakeRung): each once, a send
// that fails said on the tick's result and never failing the tick, as the rung it
// climbed is written and stands.
func (t *tickRun) wake(ws []stallWake) {
	if t.st.WakeFriend == nil {
		return
	}
	for _, w := range ws {
		if err := t.st.WakeFriend(w.friend, w.rung, w.idle); err != nil {
			t.res.Said = append(t.res.Said, fmt.Sprintf("the stall wake %d of friend %s was not sent: %v", w.rung, w.friend, err))
		}
	}
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
	m.count("kv")
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
	m.count("kv")
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
	m.count("kv")
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
	m.count("kv")
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
// tick stale: an error of a read or write at the epoch the tick holds is
// that clear.
func (st *Store) clearedUnder(ctx context.Context, res *TickResult) bool {
	if _, left, err := st.left(ctx); err != nil || !left {
		return false
	}
	res.Stale = fmt.Sprintf("the sprint was cleared during the tick (epoch %d): the tick stops here", st.epoch)
	return true
}

// stopDone stops the machine because the sprint is done, as the done part's
// step commits (sprint.TickDone): the record STOPPED
// with the cause sprint.DoneCause and a STOPPED span opened, the view's state
// DONE, in one write; then the coordinator's goal route, when the coordinator
// has a goal, is pushed the note, once. The tick's result says it. A record
// written by a stop or a clear since the tick read it is left as it is.
func (st *Store) stopDone(ctx context.Context, n sprint.Note, runSeq uint64, res *TickResult) error {
	_, after, changed, err := st.stopWithCause(ctx, sprint.DoneCause, runSeq)
	if err != nil {
		return err
	}
	if !changed {
		res.State = after.StateWord()
		if after.Running() {
			res.Stale = "the machine restarted during the tick: the old DONE judgment did not stop the new run"
		} else {
			res.Halted = "the machine was stopped during the tick: the old DONE judgment did not replace its stop"
		}
		return nil
	}
	res.State, res.Done, res.Hint = Stopped, n.What, n.Hint
	return st.pushDone(ctx, n, res)
}

// stopFor stops the machine as the tick's step commits, its record STOPPED with the cause
// and a STOPPED span opened (the done part's stop, stopDone, is the other): the tick's
// result says why. A machine STOPPED already is left as it is.
func (st *Store) stopFor(ctx context.Context, cause, why string, runSeq uint64, res *TickResult) error {
	_, after, changed, err := st.stopWithCause(ctx, cause, runSeq)
	if err != nil {
		return err
	}
	if !changed {
		res.State = after.StateWord()
		if after.Running() {
			res.Stale = "the machine restarted during the tick: the old " + cause + " judgment did not stop the new run"
		} else {
			res.Halted = "the machine was stopped during the tick: the old " + cause + " judgment did not replace its stop"
		}
		return nil
	}
	res.State, res.Halted = Stopped, why
	return nil
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

// LandingRate is sprint.LandingRate at the clock's reading: landed is the
// landed cards' stamps (LandedAt; nil for the whole-sprint average alone) and
// total the landed count; 0 when the machine has not started in this epoch,
// or the records are not read.
func (st *Store) LandingRate(ctx context.Context, landed []time.Time, total int64) float64 {
	m, _, err := st.Machine(ctx)
	if err != nil {
		return 0
	}
	es, err := st.EpochNow(ctx)
	if err != nil {
		return 0
	}
	return sprint.LandingRate(landed, total, m.Spans, m.FirstStart(es.Cleared), st.now())
}

// undone takes the cause off a machine STOPPED because the sprint was done
// once work is added to the sprint: it stays STOPPED, and the view says
// STOPPED, until the coordinator starts it.
func (st *Store) undone(ctx context.Context) error {
	if _, ok := st.B.(KV); !ok {
		return nil
	}
	return st.clearDoneCause(ctx)
}

// ErrReadOnly is the refusal of every write a read-only store is asked for
// (ReadOnly): a shadow tick's store holds no write it can reach.
var ErrReadOnly = errors.New("a read-only store writes nothing: this is a shadow tick's store (nova-sprint tick --shadow), which plans and applies nothing")

// readOnly is a backend with every write refused (ReadOnly). It holds the
// backend unexported and has no Unwrap, so nothing reaches the writes beneath
// it; the reads pass through. Of the optional interfaces it keeps only the
// reads (KV's and KeysGetter's gets, RouteReader, PriceReader): the optional
// writers (BatchApplier, Relocker, RowsSetter, TableChanger, ...) are not
// there to be found.
type readOnly struct{ b Backend }

// ReadOnly is the backend b with every write refused with ErrReadOnly, its
// reads as they were: the store user of a shadow tick (docs/SPEC-SPRINT.md
// section 14, install-canary-shadow-tick-r.w1). A read-only backend given
// again is itself.
func ReadOnly(b Backend) Backend {
	if r, ok := b.(readOnly); ok {
		return r
	}
	return readOnly{b: b}
}

var (
	_ Backend    = readOnly{}
	_ KV         = readOnly{}
	_ KeysGetter = readOnly{}
)

func (r readOnly) Shapes(ctx context.Context, tables []string) ([]ntable.Table, error) {
	return r.b.Shapes(ctx, tables)
}
func (r readOnly) CellIDs(ctx context.Context, shapes []ntable.Table) (map[string][]string, error) {
	return r.b.CellIDs(ctx, shapes)
}
func (r readOnly) ReadSet(ctx context.Context, table string, ids []string) (ntable.ReadSetResult, error) {
	return r.b.ReadSet(ctx, table, ids)
}
func (r readOnly) Apply(context.Context, ntable.BatchManifest) (ntable.Receipt, error) {
	return ntable.Receipt{}, ErrReadOnly
}
func (r readOnly) Create(context.Context, ntable.Table) error       { return ErrReadOnly }
func (r readOnly) RowsAdd(context.Context, string, []string) error  { return ErrReadOnly }
func (r readOnly) RowsHide(context.Context, string, []string) error { return ErrReadOnly }
func (r readOnly) RowsShow(context.Context, string, []string) error { return ErrReadOnly }
func (r readOnly) RowsDel(context.Context, string, []string) error  { return ErrReadOnly }
func (r readOnly) RowsDelIf(context.Context, string, []RowGuard) ([]string, error) {
	return nil, ErrReadOnly
}
func (r readOnly) KeysDelIf(context.Context, string, []RowGuard) ([]string, error) {
	return nil, ErrReadOnly
}
func (r readOnly) Place(context.Context, string, string, string, string, float64) error {
	return ErrReadOnly
}
func (r readOnly) RowSet(context.Context, string, string, map[string]string) error {
	return ErrReadOnly
}
func (r readOnly) ViewSet(context.Context, ntable.View) error { return ErrReadOnly }
func (r readOnly) ViewDelete(context.Context, string) error   { return ErrReadOnly }
func (r readOnly) DropTable(context.Context, string) error    { return ErrReadOnly }
func (r readOnly) CheckTable(context.Context, string) error   { return ErrReadOnly }
func (r readOnly) Epoch(ctx context.Context) (EpochState, error) {
	return r.b.Epoch(ctx)
}
func (r readOnly) AdvanceEpoch(context.Context, uint64, time.Time) (bool, error) {
	return false, ErrReadOnly
}
func (r readOnly) SettleEpoch(context.Context, uint64) error { return ErrReadOnly }
func (r readOnly) AtEpoch(epoch uint64, old bool) Backend {
	return readOnly{b: r.b.AtEpoch(epoch, old)}
}
func (r readOnly) ReadFence(ctx context.Context) (Fence, error) { return r.b.ReadFence(ctx) }
func (r readOnly) QueueRead(ctx context.Context) ([]sprint.QueuedChange, error) {
	return r.b.QueueRead(ctx)
}
func (r readOnly) Acquire(context.Context, uint64, OpRecord) (bool, error) {
	return false, ErrReadOnly
}
func (r readOnly) Release(context.Context, OpRecord, bool) error { return ErrReadOnly }
func (r readOnly) Done(ctx context.Context, callerOp string) (string, bool, error) {
	return r.b.Done(ctx, callerOp)
}
func (r readOnly) DoneBefore(ctx context.Context, callerOp string, before uint64) (uint64, bool, error) {
	return r.b.DoneBefore(ctx, callerOp, before)
}
func (r readOnly) SetReview(context.Context, string, time.Time, time.Time) error {
	return ErrReadOnly
}
func (r readOnly) Progress(ctx context.Context) (map[string]time.Time, error) {
	return r.b.Progress(ctx)
}
func (r readOnly) OpenNotes(ctx context.Context) ([]sprint.Open, error) { return r.b.OpenNotes(ctx) }
func (r readOnly) Aliases(ctx context.Context, aliases []string) (map[string]string, error) {
	return r.b.Aliases(ctx, aliases)
}
func (r readOnly) Answered(ctx context.Context, ids []string) (map[string]string, error) {
	return r.b.Answered(ctx, ids)
}
func (r readOnly) NotesSince(ctx context.Context, after string, max int) ([]sprint.Note, []string, error) {
	return r.b.NotesSince(ctx, after, max)
}
func (r readOnly) LogSince(ctx context.Context, after string, max int) ([]sprint.Line, []string, error) {
	return r.b.LogSince(ctx, after, max)
}
func (r readOnly) Tails(ctx context.Context) (string, string, error) { return r.b.Tails(ctx) }
func (r readOnly) Cursor(ctx context.Context) (string, error)        { return r.b.Cursor(ctx) }
func (r readOnly) SetCursor(context.Context, string) error           { return ErrReadOnly }
func (r readOnly) Coordinator(ctx context.Context) (string, error)   { return r.b.Coordinator(ctx) }
func (r readOnly) SetCoordinator(context.Context, string) error      { return ErrReadOnly }
func (r readOnly) RecordIDs(ctx context.Context, table string) ([]string, error) {
	return r.b.RecordIDs(ctx, table)
}
func (r readOnly) DeleteKeys(context.Context, []string) (int, error) { return 0, ErrReadOnly }

// GetKey reads a machine record; a backend that keeps none reads none.
func (r readOnly) GetKey(ctx context.Context, name string) (string, bool, error) {
	kv, ok := r.b.(KV)
	if !ok {
		return "", false, nil
	}
	return kv.GetKey(ctx, name)
}

// GetKeys reads machine records, as GetKey each.
func (r readOnly) GetKeys(ctx context.Context, names []string) ([]string, []bool, error) {
	kv, ok := r.b.(KV)
	if !ok {
		return make([]string, len(names)), make([]bool, len(names)), nil
	}
	return getKeys(ctx, kv, names)
}
func (r readOnly) SetKey(context.Context, string, string) error { return ErrReadOnly }
func (r readOnly) SetKeyShowing(context.Context, string, string, string, string) error {
	return ErrReadOnly
}
func (r readOnly) ShowState(context.Context, string, string) error { return ErrReadOnly }

// Routes reads the routes, as the backend beneath does; none when it keeps none.
func (r readOnly) Routes(ctx context.Context) (RouteSet, int64, error) {
	rr, ok := r.b.(RouteReader)
	if !ok {
		return RouteSet{Routes: []sprint.Route{}}, 0, nil
	}
	return rr.Routes(ctx)
}

// PriceRoutes reads the routes a worker's step prices with; none when it keeps none.
func (r readOnly) PriceRoutes(ctx context.Context) ([]sprint.Route, int64, error) {
	pr, ok := r.b.(PriceReader)
	if !ok {
		return []sprint.Route{}, 0, nil
	}
	return pr.PriceRoutes(ctx)
}

// ShadowPart is one part of a shadow tick's plan: its table's update ("" for
// the start and the end), its name, the plan's size (units, notes, rows,
// closes and updates it would write) and what it would leave due.
type ShadowPart struct {
	Table string `json:"table"`
	Name  string `json:"name"`
	Size  int    `json:"size"`
	Due   int    `json:"due,omitempty"`
}

// ShadowPlan is what a shadow tick planned: the epoch and the machine's state
// it read, each part with something to do, the plan's whole size, and how
// long it took.
type ShadowPlan struct {
	Epoch uint64        `json:"epoch"`
	State string        `json:"state"`
	Parts []ShadowPart  `json:"parts"`
	Size  int           `json:"size"`
	Took  time.Duration `json:"took_ns"`
	// Reads is why each read waits, read cards on (sprint.ReadCardsWhy), planned with the
	// readers' ask
	Reads []string `json:"reads,omitempty"`
}

// planSize is how much a plan would write: its units, notes, rows, closes and
// updates.
func planSize(p sprint.Plan) int {
	return len(p.Units) + len(p.Notes) + len(p.Rows) + len(p.Closes) + len(p.Updates)
}

// ShadowTick is the tick's plan with nothing applied (docs/SPEC-SPRINT.md
// section 14, install-canary-shadow-tick-r.w1): on a read-only copy of the
// store (ReadOnly), one fenced read of the sprint, and every part of the
// tick's start, its tables' updates in order and its end planned on that one
// read, as the tick's first pass plans them. It writes nothing: no heartbeat,
// no repair of a pending operation, no restore a clear owes, no beat; each of
// those is a refusal, and a read that meets one says so. It plans whether the
// machine is RUNNING or STOPPED (the state is reported): it is the canary of
// the planning code a server would run. A part that panics is not recovered:
// the shadow is the canary of a crash too.
func (st *Store) ShadowTick(ctx context.Context) (ShadowPlan, error) {
	began := time.Now()
	c := st.clone()
	root := c.root
	if root == nil {
		root = c.B
	}
	c.root, c.B, c.pinned = ReadOnly(root), ReadOnly(root), false
	c.tw, c.CheckTwin = nil, nil
	ro, es, err := c.pinOnly(ctx)
	if err != nil {
		return ShadowPlan{}, err
	}
	if es.Owed {
		return ShadowPlan{}, fmt.Errorf("the clear of %s still owes the restore of epoch %d's shape: a shadow tick writes nothing and does not perform it; run: nova-sprint tick", es.Cleared.UTC().Format(time.RFC3339), es.N-1)
	}
	m, _, err := ro.Machine(ctx)
	if err != nil {
		return ShadowPlan{}, err
	}
	out := ShadowPlan{Epoch: ro.epoch, State: m.StateWord(), Parts: []ShadowPart{}}
	snap, err := ro.shadowRead(ctx)
	if err != nil {
		return out, err
	}
	first := *snap
	first.Work, first.Readers, first.Merge, first.Fleet = snap.Work.Frozen(), snap.Readers.Frozen(), snap.Merge.Frozen(), snap.Fleet.Frozen()
	if err := ro.readerStatesInto(ctx, &first); err != nil {
		return out, err
	}
	now := ro.now()
	_, beats, err := ro.fleetBeats(ctx, nil)
	if err != nil {
		return out, fmt.Errorf("fleet: %w", err)
	}
	// a shadow tick wakes no friend: it writes nothing and sends nothing
	req := sprint.TickReq{Who: sprint.MachineActor, Stopped: m.StoppedBetween, Beats: beats, Started: m.FirstStart(first.Cleared), AnswerRules: st.AnswerRules, IdleAlarm: st.IdleAlarm}
	// the server's measured store round trip, for the store slow alarm (idle.go)
	if req.StoreRTTP50MS, req.StoreRTTFresh, err = ro.StoreRTT(ctx); err != nil {
		return out, err
	}
	if req.Friends, err = ro.friendSeats(ctx, &first, now); err != nil {
		return out, err
	}
	if req.Sessions, err = ro.FriendSessions(ctx); err != nil {
		return out, err
	}
	var routes RouteCache
	plan := func(table string, parts []sprint.TickPartDef) error {
		for _, part := range parts {
			view := &first
			var p sprint.Plan
			due := 0
			if part.Name == sprint.PartDrain && part.Fn == nil {
				if view.QueueLen == 0 {
					continue
				}
				q, err := ro.B.QueueRead(ctx)
				if err != nil {
					return fmt.Errorf("shadow %s: %w", part.Name, err)
				}
				p = sprint.Drain(sprint.WithQueue(view, q), q, sprint.MachineActor)
			} else {
				if view.Routes == nil && routesPart(part.Name) {
					set, err := ro.cached(ctx, &routes)
					if err != nil {
						return fmt.Errorf("shadow %s: %w", part.Name, err)
					}
					v := *view
					set.into(&v)
					view = &v
				}
				p, due = part.Fn(view, req)
				if table == sprint.Readers && part.Name == "ask" {
					out.Reads = sprint.ReadCardsWhy(view, req.Friends)
				}
			}
			if p.Empty() && due == 0 {
				continue
			}
			n := planSize(p)
			out.Parts = append(out.Parts, ShadowPart{Table: table, Name: part.Name, Size: n, Due: due})
			out.Size += n
		}
		return nil
	}
	updates := st.Updates
	if updates == nil {
		updates = sprint.TickTables
	}
	if err := plan("", sprint.TickStart); err != nil {
		return out, err
	}
	for _, u := range updates {
		if err := plan(u.Table, u.Parts); err != nil {
			return out, err
		}
	}
	if err := plan("", sprint.TickEndWithStoppedAssignments(snap, st.AnswerRules, st.IdleAlarm)); err != nil {
		return out, err
	}
	out.Took = time.Since(began)
	return out, nil
}

// shadowRead is the shadow tick's one read: the tables between two reads of
// the fence at one generation with no operation pending. An operation in
// flight is waited for, as a fenced read waits; one pending past the tries is
// a failure, never repaired here (a repair writes).
func (st *Store) shadowRead(ctx context.Context) (*sprint.Snapshot, error) {
	r := st.retry(ctx)
	for r.next(st.attempts()) {
		f, err := st.B.ReadFence(ctx)
		if err != nil {
			return nil, err
		}
		if f.Pending != nil {
			continue
		}
		snap, f2, err := st.PipelinedLoadWithFence(ctx, All, tickExtras)
		if errors.Is(err, errCleared) {
			return nil, errors.New("the sprint was cleared as the shadow tick read it; run it again")
		}
		if err != nil {
			return nil, err
		}
		if f2.Pending != nil || f2.Gen != f.Gen {
			continue
		}
		snap.QueueLen, snap.Running = f2.Queued, f2.Running
		return snap, nil
	}
	return nil, fmt.Errorf("the sprint is busy: an operation was pending or the fence moved on each of %d reads in %s; a shadow tick repairs nothing; run it again", r.tries, r.slept().Round(time.Millisecond))
}
