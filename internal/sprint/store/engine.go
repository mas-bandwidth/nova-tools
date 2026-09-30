package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// What is atomic, and what is not.
//
// One manifest is atomic: the table layer applies every entry of it against
// one pre-state or none (ns_table_apply, one call), and advances that table's
// revision once.
//
// A step is one operation, whatever it touches. Every mutating verb reads the
// sprint-wide fence (its generation and the pending operation) before and
// after it reads the tables, so its pre-state is never a partial state of
// another operation: a pending operation is finished first (repair), and a
// generation that moved while the tables were read means the read is taken
// again. The step then puts its operation in the fence, only if the
// generation is still the one it read (WATCH + MULTI/EXEC): no other sprint
// writer has applied anything since its read. Its manifests apply in order,
// one table at a time, every manifest of a table before the next table, the
// work table last; a table's changes over the table layer's bound are several
// manifests of that table. The fence is released in one MULTI/EXEC with the
// notifications, the judgments opened and closed, the streams' progress and
// the caller's result: that release is the logical commit, and nothing of the
// notifications is visible before it. The manifests and the release are not
// one atomic call: a step cut between them leaves its operation in the fence,
// and the next mutating verb, or repair, finishes it by sending each recorded
// manifest again (an applied one replays its receipt, so nothing applies
// twice), each still guarded by its own expectations, so a finish never
// overwrites newer work.
//
// A manifest refused only because its table's revision moved (a writer
// outside the sprint's fence: the display cells) is sent again against the
// fresh revision when every member it names still has the revision and place
// it expects. A first manifest refused otherwise applied nothing: the
// operation is released without effect and the step is planned again from a
// fresh read. A later manifest refused otherwise leaves the operation
// pending (check and where show it) until repair, or the next mutating verb,
// finishes it: every entry whose expectation still holds applies, every other
// is skipped and listed in one judgment notification, and the fence is
// released. Repair never overwrites newer state, and never blocks the sprint
// for good.

// Store runs sprint steps against a Backend.
type Store struct {
	B        Backend
	Names    sprint.Names
	Actor    string
	Now      func() time.Time
	NewID    func() string       // a fresh operation id family; nil is NewID
	Sleep    func(time.Duration) // the wait between tries on busy tables or fence; nil is time.Sleep
	Rand     func(n int64) int64 // the jitter of that wait, a number in [0, n); nil is math/rand/v2
	Attempts int                 // plans per step before giving up on a busy fence; default FenceTries
	Resends  int                 // sends of one manifest after a lost reply; default 3
	// Grace is how long an operation is taken as in flight (its writer alive)
	// before a writer that finds its first manifest unapplied abandons it.
	Grace   time.Duration
	root    Backend   // the backend before pinning
	epoch   uint64    // the epoch the store is pinned to
	cleared time.Time // when the pinned epoch began
	pinned  bool
	old     bool // pinned to an earlier epoch, for reading
}

// Step is one verb's step: the tables its plan reads, any records it reads
// beyond them, and its plan.
type Step struct {
	Verb     string
	Load     []string
	Extras   func(*sprint.Snapshot) map[string][]string
	Plan     func(s *sprint.Snapshot) sprint.Plan
	Mirrors  bool   // bring the fleet's and merge's display cells up to date after
	CallerOp string // the caller's operation id: a retry returns the recorded result
	// Epoch, when set, is the epoch the caller holds (a worker's card, a
	// reader's read card, the driver's merge step): a sprint at another epoch
	// refuses the step, naming the clear.
	Epoch *uint64
	// Args is the step's arguments in one canonical form (ArgsOf of its
	// request): a caller's operation id replays only for the same verb and
	// the same arguments.
	Args string
	// Actor, when set, is who the step's lines are by (the machine, for a
	// tick's part), whoever runs it; else the store's actor.
	Actor string
	// Named says the step names its cards or notes (ids, a group's members,
	// an ack's notes): it applies all or none, and one refusal refuses the
	// whole step, naming every one.
	Named bool
}

// ArgsOf is a request's arguments in one canonical form: a digest of its JSON
// (map keys sorted).
func ArgsOf(req any) string {
	b, err := json.Marshal(req)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:12])
}

// OpConflictError is a caller's operation id recorded for another verb, or
// for the same verb with other arguments: the step is not a retry of it.
type OpConflictError struct {
	Op, Verb, Recorded string
	OtherArgs          bool
}

func (e *OpConflictError) Error() string {
	if e.OtherArgs {
		return fmt.Sprintf("operation id %s is recorded for %s with other arguments: this %s is not a retry of it; nothing was done; give this step a fresh --op", e.Op, e.Recorded, e.Verb)
	}
	return fmt.Sprintf("operation id %s is recorded for %s: this %s is not a retry of it; nothing was done; give this step a fresh --op", e.Op, e.Recorded, e.Verb)
}

// replay is the recorded result of the step's caller operation id, refused as
// a conflict when it was recorded for another verb or other arguments.
func replay(step Step, raw string) (Result, error) {
	var rec Result
	if err := json.Unmarshal([]byte(raw), &rec); err != nil {
		return Result{Verb: step.Verb}, fmt.Errorf("operation %s: its recorded result is unreadable: %w", step.CallerOp, err)
	}
	if rec.Verb != step.Verb {
		return Result{Verb: step.Verb}, &OpConflictError{Op: step.CallerOp, Verb: step.Verb, Recorded: rec.Verb}
	}
	if step.Args != "" && rec.Args != "" && step.Args != rec.Args {
		return Result{Verb: step.Verb}, &OpConflictError{Op: step.CallerOp, Verb: step.Verb, Recorded: rec.Verb, OtherArgs: true}
	}
	rec.Replay = true
	return rec, nil
}

// Result is what a step did.
type Result struct {
	Verb     string           `json:"verb"`
	Op       string           `json:"op,omitempty"`
	Moved    []string         `json:"moved"`
	Refused  []sprint.Refusal `json:"refused"`
	Notes    int              `json:"notes"`
	Attempts int              `json:"attempts"`
	Replay   bool             `json:"replay,omitempty"` // the recorded result of the caller's operation id
	Repaired []string         `json:"repaired,omitempty"`
	Pending  string           `json:"pending,omitempty"` // an operation left in the fence
	// Skipped is each entry a repair of this operation did not apply because
	// its expectation no longer held: recorded with the result, so a replay of
	// the caller's operation id returns it.
	Skipped []string `json:"skipped,omitempty"`
	Args    string   `json:"args,omitempty"` // the step's arguments (ArgsOf), recorded with the result
	// Lost says the step lost every attempt to other writers and applied
	// nothing: what it had to do is still to do.
	Lost bool `json:"lost,omitempty"`
}

// ErrUnknown is a write the store did not confirm: changed=unknown.
var ErrUnknown = errors.New("the store did not confirm the write (changed=unknown)")

// PendingError is a step that cannot run because an operation is pending
// that cannot be finished now.
type PendingError struct {
	Op, Why string
}

func (e *PendingError) Error() string {
	return fmt.Sprintf("operation %s is pending: %s; run: nova-sprint repair", e.Op, e.Why)
}

// CutError is an operation whose later manifest could not apply: it stays in
// the fence.
type CutError struct {
	Op, Table string
	Cause     error
}

func (e *CutError) Error() string {
	return fmt.Sprintf("operation %s cut at table %s: %v; run: nova-sprint repair", e.Op, e.Table, e.Cause)
}

func (e *CutError) Unwrap() error { return e.Cause }

func (st *Store) attempts() int {
	if st.Attempts > 0 {
		return st.Attempts
	}
	return FenceTries
}

func (st *Store) resends() int {
	if st.Resends > 0 {
		return st.Resends
	}
	return 3
}

func (st *Store) grace() time.Duration {
	if st.Grace > 0 {
		return st.Grace
	}
	return time.Minute
}

// Fenced reads the tables with the fence read before and after, finishing a
// pending operation first: the snapshot is no partial state of any operation,
// and gen is the fence's generation it was read at.
func (st *Store) Fenced(ctx context.Context, tables []string, extras func(*sprint.Snapshot) map[string][]string, repaired *[]string) (*sprint.Snapshot, uint64, error) {
	r := st.retry(ctx)
	for r.next(st.attempts()) {
		f, err := st.B.ReadFence(ctx)
		if err != nil {
			return nil, 0, err
		}
		if f.Pending != nil {
			r, err := st.finish(ctx, *f.Pending)
			if err != nil {
				return nil, 0, err
			}
			if r.Done == "open" {
				if st.now().Sub(f.Pending.At) < st.grace() {
					continue // in flight: its writer is at it
				}
				return nil, 0, &PendingError{Op: r.Op, Why: r.Detail}
			}
			if repaired != nil {
				*repaired = append(*repaired, r.Op+" "+r.Done)
			}
			continue
		}
		snap, err := st.Load(ctx, tables, extras)
		if err != nil {
			return nil, 0, err
		}
		f2, err := st.B.ReadFence(ctx)
		if err != nil {
			return nil, 0, err
		}
		if f2.Pending != nil || f2.Gen != f.Gen {
			continue
		}
		return snap, f.Gen, nil
	}
	return nil, 0, fmt.Errorf("the sprint is busy: other operations kept the fence moving, %d reads in %s; nothing was changed; run the verb again", r.tries, r.slept().Round(time.Millisecond))
}

// Run plans and applies a step as one operation.
func (st *Store) Run(ctx context.Context, step Step) (Result, error) {
	ctx = withBudget(ctx)
	res := Result{Verb: step.Verb, Args: step.Args}
	if strings.Contains(step.CallerOp, "~") {
		return res, fmt.Errorf("operation id %s holds '~', which marks the epoch in the sprint's ids; nothing was done; give the step an --op without '~'", step.CallerOp)
	}
	st, err := st.pin(ctx)
	if err != nil {
		return res, err
	}
	if step.CallerOp != "" {
		if r, done, err := st.callerOp(ctx, step, res); done || err != nil {
			return r, err
		}
	}
	// The operation's ids carry the epoch it runs at (OpFamily): none is the
	// same in two epochs.
	family := step.CallerOp
	if family == "" {
		family = strings.ReplaceAll(step.Verb, " ", "-") + "-" + st.newID()
	}
	rowsAdded := false
	plans := st.retry(ctx)
	for res.Attempts < st.attempts() {
		res.Attempts++
		snap, gen, err := st.Fenced(ctx, step.Load, step.Extras, &res.Repaired)
		if errors.Is(err, errCleared) && step.Epoch == nil {
			// The sprint was cleared while this step read it: read the new
			// epoch.
			if st, err = st.repin(ctx); err != nil {
				return res, err
			}
			continue
		}
		if err != nil && !errors.Is(err, errCleared) {
			return res, err
		}
		if step.Epoch != nil && (err != nil || *step.Epoch != snap.Epoch) {
			now, eerr := st.EpochNow(ctx)
			if eerr != nil {
				return res, eerr
			}
			why := (&ClearedError{Held: *step.Epoch, Now: now.N, At: now.Cleared}).Error()
			// Nothing of an earlier attempt's plan was written.
			res.Moved, res.Op, res.Notes = nil, "", 0
			res.Refused = []sprint.Refusal{{Key: "epoch " + strconv.FormatUint(*step.Epoch, 10), Why: why}}
			return res, nil
		}
		if step.CallerOp != "" {
			if r, done, err := st.callerOp(ctx, step, res); done || err != nil {
				return r, err
			}
		}
		// Every plan is held to the lifecycle here, whatever step built it.
		plan := sprint.Applied(snap, step.Plan(snap))
		if step.Named && len(plan.Refused) > 0 && len(plan.Units)+len(plan.Notes)+len(plan.Closes)+len(plan.Rows) > 0 {
			return allOrNone(res, plan), nil
		}
		// The fence is free: a stuck operation's judgment rides with this
		// step, once.
		stuck, isStuck, err := st.stuck(ctx)
		if err != nil {
			return res, err
		}
		if isStuck {
			plan.Notes = append(plan.Notes, stuckNote(stuck, snap.Now, st.Actor))
		}
		res.Refused = plan.Refused
		res.Moved = nil
		for _, u := range plan.Units {
			if u.Moved != "" {
				res.Moved = append(res.Moved, u.Moved)
			}
		}
		actor := st.Actor
		if step.Actor != "" {
			actor = step.Actor
		}
		op, err := st.operation(step.Verb, actor, sprint.OpFamily(family, st.epoch)+"-"+strconv.Itoa(res.Attempts), plan, snap)
		var twice *twiceError
		if errors.As(err, &twice) {
			return refuseWhole(res, plan, twice.Error())
		}
		if err != nil {
			return res, err
		}
		if isStuck {
			op.Stuck = stuck.Op
		}
		if why := unwritable(plan, op); why != "" {
			return refuseWhole(res, plan, why)
		}
		// A step refused whole writes nothing: its rows are declared only
		// with a unit to write.
		if len(plan.Rows) > 0 && !rowsAdded && len(plan.Units) > 0 {
			if err := st.addRows(ctx, plan.Rows); err != nil {
				return res, err
			}
			rowsAdded = true
			res.Attempts--
			continue
		}
		if len(op.Manifests) == 0 && len(op.Notes)+len(op.Decided)+len(op.Closes)+len(op.Updates) == 0 {
			res.Moved = nil
			return st.after(ctx, step, res)
		}
		// Every operation's result is recorded at its commit, under the
		// caller's operation id or its own: a writer whose operation another
		// writer finished reads it there.
		op.CallerOp = step.CallerOp
		if op.CallerOp == "" {
			op.CallerOp = op.ID
		}
		res.Op = op.ID
		res.Notes = len(op.Notes) + len(op.Decided)
		body, _ := json.Marshal(res)
		op.Result = string(body)
		if n := recordSize(op); n > MaxOpRecord {
			return refuseWhole(res, plan, fmt.Sprintf("the step's record is %d bytes, over the bound of %d bytes one write to the store takes; nothing was changed; do it in parts (fewer cards at once)", n, MaxOpRecord))
		}
		ok, err := st.B.Acquire(ctx, gen, op)
		if err != nil {
			// A write the store did not take leaves the fence without this
			// operation: nothing was changed, and the store's reason says why.
			if f, ferr := st.B.ReadFence(ctx); ferr == nil && (f.Pending == nil || f.Pending.ID != op.ID) {
				res.Op = ""
				return refuseWhole(res, plan, fmt.Sprintf("the store did not take the step's record (%d bytes): %v; nothing was changed", recordSize(op), err))
			}
			return res, fmt.Errorf("%w: acquiring the fence for %s: %v; run: nova-sprint repair", ErrUnknown, op.ID, err)
		}
		if !ok {
			if !plans.wait() {
				break
			}
			continue
		}
		applied, err := st.apply(ctx, op)
		var bound *boundError
		var cut *CutError
		if (errors.As(err, &cut) || err == nil && !applied) && st.finishedElsewhere(ctx, op) {
			// Another writer (the tick, another verb, repair) finished this
			// operation: its recorded result is this step's, as a replay.
			raw, _, _ := st.B.Done(ctx, op.CallerOp)
			if rec, rerr := replay(step, raw); rerr == nil {
				return st.after(ctx, step, rec)
			}
		}
		if err != nil && !errors.As(err, &bound) {
			res.Pending = op.ID
			return res, err
		}
		if !applied {
			if err := st.B.Release(ctx, op, false); err != nil {
				res.Pending = op.ID
				return res, fmt.Errorf("%w: releasing %s after its first manifest was refused: %v; run: nova-sprint repair", ErrUnknown, op.ID, err)
			}
			if bound != nil {
				res.Op = ""
				return refuseWhole(res, plan, bound.Error())
			}
			continue
		}
		if err := st.B.Release(ctx, op, true); err != nil {
			res.Pending = op.ID
			return res, fmt.Errorf("%w: the commit of %s: %v; run: nova-sprint repair", ErrUnknown, op.ID, err)
		}
		return st.after(ctx, step, res)
	}
	var keys []string
	for _, r := range res.Refused {
		keys = append(keys, r.Key)
	}
	for _, m := range res.Moved {
		keys = append(keys, strings.Fields(m)[0])
	}
	res.Moved = nil
	res.Refused = nil
	res.Notes, res.Op, res.Lost = 0, "", true
	for _, k := range keys {
		res.Refused = append(res.Refused, sprint.Refusal{Key: k, Why: fmt.Sprintf("the sprint kept changing under this step (%d attempts); run it again", res.Attempts)})
	}
	return res, nil
}

// callerOp is the recorded result of the step's caller operation id, when
// done: at the pinned epoch it replays; recorded at an earlier epoch the step
// is refused, naming the epoch, and never run again as new work.
func (st *Store) callerOp(ctx context.Context, step Step, res Result) (Result, bool, error) {
	raw, ok, err := st.B.Done(ctx, step.CallerOp)
	if err != nil {
		return res, true, err
	}
	if ok {
		r, err := replay(step, raw)
		return r, true, err
	}
	if st.epoch == 0 {
		return res, false, nil
	}
	e, ok, err := st.B.DoneBefore(ctx, step.CallerOp, st.epoch)
	if err != nil || !ok {
		return res, err != nil, err
	}
	now, err := st.EpochNow(ctx)
	if err != nil {
		return res, true, err
	}
	res.Moved, res.Op, res.Notes = nil, "", 0
	res.Refused = []sprint.Refusal{{Key: "op " + step.CallerOp, Why: fmt.Sprintf("the sprint was cleared at %s: operation %s belongs to epoch %d, and the sprint's epoch is now %d; it is not run again as new work; nothing was changed; read the sprint again (nova-sprint queue, where) and give new work a fresh --op",
		now.Cleared.UTC().Format(time.RFC3339), step.CallerOp, e, now.N)}}
	return res, true, nil
}

// finishedElsewhere says the fence no longer holds the operation and its
// result is recorded: another writer finished it. An operation released
// without a record was abandoned: nothing of it happened.
func (st *Store) finishedElsewhere(ctx context.Context, op OpRecord) bool {
	f, err := st.B.ReadFence(ctx)
	if err != nil || f.Pending != nil && f.Pending.ID == op.ID {
		return false
	}
	_, ok, err := st.B.Done(ctx, op.CallerOp)
	return err == nil && ok
}

// after brings the display cells up to date after a step. A step finished at
// an epoch a clear closed as it ran (the clear finished its operation there)
// is told the sprint was cleared, not what the sync of the display cells met.
func (st *Store) after(ctx context.Context, step Step, res Result) (Result, error) {
	if step.Verb == "add" && len(res.Moved) > 0 {
		// work added to a done sprint: the machine stays STOPPED, no longer
		// done (errata 3 amendment 6)
		if err := st.undone(ctx); err != nil {
			return res, err
		}
	}
	if step.Mirrors {
		if err := st.SyncMirrors(ctx); err != nil {
			if es, left, lerr := st.left(ctx); lerr == nil && left {
				return res, &ClearedError{Held: st.epoch, Now: es.N, At: es.Cleared, Finished: true}
			}
			return res, err
		}
	}
	return res, nil
}

func (st *Store) addRows(ctx context.Context, rows []sprint.RowAdd) error {
	by := map[string][]string{}
	var order []string
	for _, r := range rows {
		if _, ok := by[r.Table]; !ok {
			order = append(order, r.Table)
		}
		by[r.Table] = append(by[r.Table], r.Row)
	}
	for _, t := range order {
		if err := st.B.RowsAdd(ctx, st.Names.Table(t), by[t]); err != nil {
			return err
		}
	}
	return nil
}

type entryKey struct{ table, id string }

// manifestBudget is the bytes a manifest of a step is split at: the table
// layer's bound, less room for a longer table revision when a manifest is
// sent again against a fresher one.
const manifestBudget = ntable.LimitManifestBytes - 64

// MaxCardTextBytes bounds each text field a card carries (CardTextFields): a
// step that would write a longer one is refused before anything is written.
const MaxCardTextBytes = 8 << 10

// CardTextFields are the text fields a card carries.
var CardTextFields = []string{"brief", "fix", "finding", "report", "reason", "note", "return_reason", "ci_note", "did"}

// unwritable is why a step's plan cannot be written, before anything is: a
// card text field over MaxCardTextBytes, or a manifest the table layer's own
// validation refuses (its bounds and rules); "" when it can be.
func unwritable(plan sprint.Plan, op OpRecord) string {
	for _, u := range plan.Units {
		for _, c := range u.Changes {
			for _, f := range CardTextFields {
				if v, ok := c.Entry.Set[f]; ok && len(v) > MaxCardTextBytes {
					return fmt.Sprintf("card %s: field %s is %d bytes, over the bound of %d bytes; shorten it, or point to a file or a comment", c.Entry.ID, f, len(v), MaxCardTextBytes)
				}
			}
		}
	}
	for _, man := range op.Manifests {
		if why := invalid(man); why != "" {
			return why
		}
	}
	return ""
}

// invalid is the table layer's own refusal of a manifest that needs no store
// (its bounds and rules), in the store's words; "" when it is valid.
func invalid(man ntable.BatchManifest) string {
	if man.Members == nil {
		man.Members = []ntable.BatchMemberEntry{}
	}
	raw, err := json.Marshal(man)
	if err != nil {
		return "table " + man.Table + ": " + err.Error()
	}
	_, verr := ntable.ValidateBatchManifestRaw(raw)
	var le *ntable.LimitError
	var re *ntable.RuleError
	switch {
	case verr == nil:
		return ""
	case errors.As(verr, &le):
		where := ""
		if le.Member != "" {
			where = " member " + le.Member + ":"
		}
		return fmt.Sprintf("table %s:%s LIMIT: %s; %s", man.Table, where, le.Error(), le.Advice())
	case errors.As(verr, &re):
		return fmt.Sprintf("table %s: %s: %s", man.Table, re.Code, re.Msg)
	}
	return fmt.Sprintf("table %s: invalid batch manifest: %v", man.Table, verr)
}

// curable says a refusal of a manifest may not hold on a fresh read (a
// revision, a place, a member, a field of the store's state); a bound or a
// rule of the table layer holds whatever the state, and is never retried.
func curable(err error, man ntable.BatchManifest) bool {
	if errors.Is(err, ntable.ErrMalformedManifest) || refusalCode(err) == "LIMIT" {
		return false
	}
	return invalid(man) == ""
}

// refuseWhole is a step refused whole before anything of it was written: every
// card it was to move is refused with why.
func refuseWhole(res Result, plan sprint.Plan, why string) (Result, error) {
	res.Moved = nil
	res.Refused = append([]sprint.Refusal(nil), plan.Refused...)
	seen := map[string]bool{}
	for _, r := range res.Refused {
		seen[r.Key] = true
	}
	for _, u := range plan.Units {
		if u.Key != "" && !seen[u.Key] {
			seen[u.Key] = true
			res.Refused = append(res.Refused, sprint.Refusal{Key: u.Key, Why: "the step cannot be written, nothing was written: " + why})
		}
	}
	if len(res.Refused) == len(plan.Refused) {
		return res, errors.New("the step cannot be written, nothing was written: " + why)
	}
	return res, nil
}

// allOrNone is a step that names its cards or notes with one of them
// refused: nothing is written, and every one it would have changed is named.
func allOrNone(res Result, plan sprint.Plan) Result {
	res.Moved = nil
	res.Refused = append([]sprint.Refusal(nil), plan.Refused...)
	seen := map[string]bool{}
	for _, r := range res.Refused {
		seen[r.Key] = true
	}
	why := fmt.Sprintf("not written: the verb names several and applies all or none, and %d of them %s refused", len(plan.Refused), map[bool]string{true: "was", false: "were"}[len(plan.Refused) == 1])
	for _, u := range plan.Units {
		if u.Key != "" && !seen[u.Key] {
			seen[u.Key] = true
			res.Refused = append(res.Refused, sprint.Refusal{Key: u.Key, Why: why})
		}
	}
	return res
}

// twiceError is a step whose plan changes one card twice in ways that do not
// agree: the step is refused whole, naming both causes.
type twiceError struct {
	Card, Table, First, Second, Why string
}

func (e *twiceError) Error() string {
	return fmt.Sprintf("card %s of %s is changed twice in one step, by %s and by %s, and the changes disagree (%s)", e.Card, e.Table, e.First, e.Second, e.Why)
}

// MaxOpRecord is the largest operation record one step writes, well under
// a store's bulk length bound (Redis's 512 MiB by default): a step over it
// is refused before any write, naming the bound.
const MaxOpRecord = 256 << 20

// recordSize is an operation record's size as it is written.
func recordSize(op OpRecord) int {
	b, err := json.Marshal(op)
	if err != nil {
		return 0
	}
	return len(b)
}

// moveLine is the log's line of one card's change in a step: its place
// before and after, its generation after, the words given with it, and the
// judgments the step answered on it.
func moveLine(k entryKey, e ntable.BatchMemberEntry, units []sprint.Unit, snap *sprint.Snapshot, op, verb, actor string) sprint.Line {
	l := sprint.Line{Kind: sprint.LineMove, At: snap.Now, Epoch: snap.Epoch, Op: op, Card: k.id, Table: k.table, Verb: verb, Actor: actor}
	pre := snap.T(k.table).Card(k.id)
	if pre.Placed() {
		l.From = pre.Row + ":" + pre.Col
	}
	l.To, l.Gen = l.From, pre.Int("gen")
	switch {
	case e.Remove:
		l.To, l.Removed = "", true
	case e.Create != nil:
		l.To = e.Create.Row + ":" + e.Create.Col
		l.Set = map[string]string{"score": strconv.FormatFloat(e.Create.Score, 'f', -1, 64)}
	case e.Move != nil:
		l.To = e.Move.Row + ":" + e.Move.Col
		if e.Move.Score != nil {
			l.Set = map[string]string{"score": strconv.FormatFloat(*e.Move.Score, 'f', -1, 64)}
		}
	}
	field := func(name string) string {
		if v, ok := e.Set[name]; ok {
			return v
		}
		if slices.Contains(e.Unset, name) {
			return ""
		}
		return pre.F(name)
	}
	l.Gen, _ = strconv.Atoi(field("gen"))
	l.Primary, l.Stream = field("primary"), field("stream")
	if k.table == sprint.Work {
		l.Primary = k.id
		if l.Stream == "" {
			l.Stream, _, _ = strings.Cut(l.To, ":")
		}
	}
	if k.table == sprint.Merge && l.Primary == "" {
		l.Primary = k.id
	}
	for f, v := range e.Set {
		if slices.Contains(sprint.TextFields, f) {
			if l.Text == nil {
				l.Text = map[string]string{}
			}
			l.Text[f] = v
			continue
		}
		if l.Set == nil {
			l.Set = map[string]string{}
		}
		l.Set[f] = v
	}
	// The cause is this card's own: a unit about this card gives its moved
	// line; one about another card (a sentinel inserted in front of many)
	// names that card, never its whole line, so no step's record grows with
	// the square of the cards it moves.
	var causes []string
	for _, u := range units {
		cause := u.Moved
		if u.Key != k.id && u.Key != l.Primary {
			cause = "with " + u.Key
		}
		if cause != "" && !slices.Contains(causes, cause) {
			causes = append(causes, cause)
		}
		for _, o := range u.Closes {
			if o.Subject() == k.id || o.Subject() == l.Primary {
				if !slices.Contains(l.Answers, o.Note.ID) {
					l.Answers = append(l.Answers, o.Note.ID)
				}
			}
		}
	}
	l.Cause = strings.Join(causes, "; ")
	if len(l.Cause) > sprint.MaxCause {
		l.Cause = l.Cause[:sprint.MaxCause-3] + "..."
	}
	return l
}

func unitCause(u sprint.Unit) string {
	if u.Moved != "" {
		return u.Key + " (" + u.Moved + ")"
	}
	return u.Key
}

// mergeEntries is two changes of one card, planned on one pre-state, as one
// entry: the same expectation, at most one place change, and fields that do
// not disagree; else why not.
func mergeEntries(a, b ntable.BatchMemberEntry) (ntable.BatchMemberEntry, string) {
	ja, _ := json.Marshal(a.Expect)
	jb, _ := json.Marshal(b.Expect)
	switch {
	case a.Create != nil || b.Create != nil:
		return a, "a card is created once"
	case string(ja) != string(jb):
		return a, "they expect the card at different revisions or places"
	case a.Remove && b.Move != nil || b.Remove && a.Move != nil:
		return a, "one moves it and one takes it off the table"
	}
	out := a
	if b.Move != nil {
		if a.Move != nil {
			mj, _ := json.Marshal(a.Move)
			nj, _ := json.Marshal(b.Move)
			if string(mj) != string(nj) {
				return a, "they move it to different places"
			}
		}
		out.Move = b.Move
	}
	out.Remove = a.Remove || b.Remove
	if len(b.Set) > 0 {
		out.Set = map[string]string{}
		for k, v := range a.Set {
			out.Set[k] = v
		}
		for k, v := range b.Set {
			if w, ok := out.Set[k]; ok && w != v {
				return a, "they set " + k + " to " + w + " and to " + v
			}
			out.Set[k] = v
		}
	}
	out.Unset = append([]string(nil), a.Unset...)
	for _, k := range b.Unset {
		if !contains(out.Unset, k) {
			out.Unset = append(out.Unset, k)
		}
	}
	for _, k := range out.Unset {
		if _, ok := out.Set[k]; ok {
			return a, "one sets " + k + " and one unsets it"
		}
	}
	return out, ""
}

func hasChanges(e ntable.BatchMemberEntry) bool {
	return e.Create != nil || e.Move != nil || e.Remove || len(e.Set) > 0 || len(e.Unset) > 0
}

// operation builds the step's operation from its plan: per table, in
// ApplyOrder, the units' entries in order (a repeated guard once, the bumps
// of a counter card summed into one entry), split into manifests of at most
// ntable.LimitChangedEntries changed and ntable.LimitGuardEntries guard-only
// entries, each expecting the revision the one before it leaves; then the
// notifications and the answers.
func (st *Store) operation(verb, actor, id string, plan sprint.Plan, snap *sprint.Snapshot) (OpRecord, error) {
	op := OpRecord{ID: id, Verb: verb, At: snap.Now}
	entries := map[string][]ntable.BatchMemberEntry{}
	seen := map[entryKey]int{} // index+1 in entries[table]
	cause := map[entryKey]string{}
	var logOrder []entryKey                  // the cards changed, in the order of their first change
	logUnits := map[entryKey][]sprint.Unit{} // the units that changed each
	bumps := map[entryKey]map[string]int{}
	var bumpOrder []entryKey
	streams := map[string]bool{}
	for _, u := range plan.Units {
		if u.Stream != "" {
			streams[u.Stream] = true
		}
		for _, c := range u.Changes {
			k := entryKey{c.Table, c.Entry.ID}
			if len(logUnits[k]) == 0 {
				logOrder = append(logOrder, k)
			}
			logUnits[k] = append(logUnits[k], u)
			if i := seen[k]; i > 0 {
				// Two changes of one card in one step are one entry when they
				// agree; else the step is refused, naming both.
				e := c.Entry
				e.ID = entries[c.Table][i-1].ID
				merged, why := mergeEntries(entries[c.Table][i-1], e)
				if why != "" {
					return op, &twiceError{Card: c.Entry.ID, Table: c.Table, First: cause[k], Second: unitCause(u), Why: why}
				}
				entries[c.Table][i-1] = merged
				continue
			}
			e := c.Entry
			e.ID = sprint.StoredID(e.ID, snap.Epoch)
			entries[c.Table] = append(entries[c.Table], e)
			seen[k] = len(entries[c.Table])
			cause[k] = unitCause(u)
		}
		for _, b := range u.Bumps {
			k := entryKey{b.Table, b.ID}
			if bumps[k] == nil {
				bumps[k] = map[string]int{}
				bumpOrder = append(bumpOrder, k)
			}
			bumps[k][b.Field] += b.Delta
		}
	}
	for _, k := range bumpOrder {
		if seen[k] > 0 {
			return op, fmt.Errorf("counter card %s is changed and counted in one step", k.id)
		}
		c := snap.T(k.table).Placed(k.id)
		if c == nil {
			return op, fmt.Errorf("counter card %s is not on table %s", k.id, k.table)
		}
		set := map[string]string{}
		for f, d := range bumps[k] {
			set[f] = strconv.Itoa(c.Int(f) + d)
		}
		entries[k.table] = append(entries[k.table], ntable.BatchMemberEntry{ID: sprint.StoredID(c.ID, snap.Epoch),
			Expect: &ntable.MemberExpect{Revision: strconv.FormatUint(c.Rev, 10), Place: &ntable.PlaceExpect{Row: c.Row, Col: c.Col}}, Set: set})
	}
	// The table properties the step writes go in the first manifest of their
	// table, with its members, guarded on the values the plan read (L1
	// contract amendment, table properties, section 4).
	props := map[string]*ntable.BatchManifest{}
	for _, pw := range plan.Props {
		m := props[pw.Table]
		if m == nil {
			m = &ntable.BatchManifest{Props: map[string]string{}}
			props[pw.Table] = m
		}
		m.Props[pw.Name] = pw.Value
		if pw.WasAbsent {
			m.PropAbsent = append(m.PropAbsent, pw.Name)
		} else {
			if m.PropExpect == nil {
				m.PropExpect = map[string]string{}
			}
			m.PropExpect[pw.Name] = pw.Was
		}
	}
	k := 0
	for _, t := range sprint.ApplyOrder {
		tb := snap.T(t)
		rev := uint64(0)
		if tb != nil {
			rev = tb.Revision
		}
		if len(entries[t]) == 0 && props[t] == nil {
			continue
		}
		var cur []ntable.BatchMemberEntry
		changed, guards, size := 0, 0, 0
		first := true
		manifest := func(members []ntable.BatchMemberEntry) ntable.BatchManifest {
			m := ntable.BatchManifest{Schema: 1, Table: st.Names.Table(t), Epoch: strconv.FormatUint(tb.Epoch, 10),
				ExpectedTableRevision: strconv.FormatUint(rev, 10), OperationID: fmt.Sprintf("%s-%d", id, k+1), Actor: st.Actor, Members: members}
			if p := props[t]; first && p != nil {
				m.Props, m.PropExpect, m.PropAbsent = p.Props, p.PropExpect, p.PropAbsent
			}
			return m
		}
		flush := func() {
			if len(cur) == 0 && !(first && props[t] != nil) {
				return
			}
			if cur == nil {
				cur = []ntable.BatchMemberEntry{}
			}
			op.Manifests = append(op.Manifests, manifest(cur))
			first = false
			k++
			rev++
			cur, changed, guards, size = nil, 0, 0, 0
		}
		for _, e := range entries[t] {
			b, err := json.Marshal(e)
			if err != nil {
				return op, err
			}
			envelope, err := json.Marshal(manifest([]ntable.BatchMemberEntry{}))
			if err != nil {
				return op, err
			}
			if hasChanges(e) && changed == ntable.LimitChangedEntries || !hasChanges(e) && guards == ntable.LimitGuardEntries ||
				len(cur) > 0 && len(envelope)+size+1+len(b) > manifestBudget {
				flush()
			}
			if hasChanges(e) {
				changed++
			} else {
				guards++
			}
			if len(cur) > 0 {
				size++
			}
			size += len(b)
			cur = append(cur, e)
		}
		flush()
	}
	// Every card the step changes is a move line of the log, written with
	// the step's commit: the log replays to every card's place.
	for _, k := range logOrder {
		op.Log = append(op.Log, moveLine(k, entries[k.table][seen[k]-1], logUnits[k], snap, id, verb, actor))
	}
	op.Log = sprint.GroupSets(op.Log)
	var all []sprint.Note
	var closes []sprint.Open
	for _, u := range plan.Units {
		all = append(all, u.Notes...)
		closes = append(closes, u.Closes...)
	}
	all = append(all, plan.Notes...)
	closes = append(closes, plan.Closes...)
	// A step's own answer to a judgment it closes (ack's reason) is the one
	// decided note of that judgment, not a second one beside it.
	closing := map[string]bool{}
	for _, o := range closes {
		closing[o.Note.ID] = true
	}
	answers := map[string]string{}
	var kept []sprint.Note
	for _, n := range all {
		if n.Kind == sprint.Decided && closing[n.Answers] {
			answers[n.Answers] = n.What
			continue
		}
		if n.Type != "" {
			kept = append(kept, n)
			if n.Stream != "" {
				streams[n.Stream] = true
			}
		}
	}
	op.Updates = plan.Updates
	op.Notes = sprint.MergeNotes(kept)
	for i := range op.Notes {
		op.Notes[i].ID = fmt.Sprintf("%s.%d", id, i+1)
	}
	byNote := map[string]*sprint.Note{}
	var order []string
	done := map[string]bool{}
	for _, o := range closes {
		if done[o.Key] {
			continue
		}
		done[o.Key] = true
		op.Closes = append(op.Closes, o.Key)
		d := byNote[o.Note.ID]
		if d == nil {
			what := "answered by " + verb
			if a, ok := answers[o.Note.ID]; ok {
				what = a
			}
			d = &sprint.Note{Kind: sprint.Decided, Type: o.Note.Type, Stream: o.Note.Stream, Answers: o.Note.ID,
				What: what, Who: actor, At: snap.Now}
			byNote[o.Note.ID] = d
			order = append(order, o.Note.ID)
		}
		d.Primaries = append(d.Primaries, o.Subject())
		d.Count++
	}
	for i, nid := range order {
		d := *byNote[nid]
		sort.Strings(d.Primaries)
		d.ID = fmt.Sprintf("%s.d%d", id, i+1)
		op.Decided = append(op.Decided, d)
	}
	for s := range streams {
		op.Streams = append(op.Streams, s)
	}
	sort.Strings(op.Streams)
	return op, nil
}

// boundError is a first manifest the store refused on a bound or a rule of
// the table layer: nothing of the operation applied, and sending it again
// cannot cure it.
type boundError struct {
	Table string
	Cause error
}

func (e *boundError) Error() string {
	return fmt.Sprintf("table %s: the store refused it on a bound or a rule: %v", e.Table, e.Cause)
}

// apply sends the operation's manifests in order. applied is false when the
// first manifest was refused: nothing of the operation applied.
func (st *Store) apply(ctx context.Context, op OpRecord) (bool, error) {
	for i, man := range op.Manifests {
		_, err := st.send(ctx, man)
		if err == nil || refusalCode(err) == "OPCONFLICT" {
			continue
		}
		if errors.Is(err, ErrUnknown) {
			return false, fmt.Errorf("%w: %s at table %s: %v; run: nova-sprint repair, then nova-sprint check", ErrUnknown, man.OperationID, man.Table, err)
		}
		if refusalCode(err) == "REVISION" {
			if _, err = st.resendFresh(ctx, man, err); err == nil {
				continue
			}
		}
		if i == 0 && (ntable.IsRefusal(err) || errors.Is(err, ntable.ErrMalformedManifest)) {
			if !curable(err, man) {
				return false, &boundError{Table: man.Table, Cause: err}
			}
			return false, nil
		}
		return false, &CutError{Op: op.ID, Table: man.Table, Cause: err}
	}
	return true, nil
}

// unreadableReceipt says the store answered with a receipt this build cannot
// read: its table functions are not this build's. Nothing says whether the
// batch applied.
func unreadableReceipt(err error) bool {
	return err != nil && strings.Contains(err.Error(), "unmarshal batch delta")
}

// send applies a manifest, sending the same bytes again after a lost reply:
// the table layer returns the original receipt when it had applied it.
func (st *Store) send(ctx context.Context, man ntable.BatchManifest) (ntable.Receipt, error) {
	var last error
	for i := 0; i < st.resends(); i++ {
		rc, err := st.B.Apply(ctx, man)
		if err == nil {
			return rc, nil
		}
		if unreadableReceipt(err) {
			return ntable.Receipt{}, fmt.Errorf("%w: the store's receipt could not be read (%v): its table functions do not match this build; run: nova-redis fn load --addr <host:port>", ErrUnknown, err)
		}
		if ntable.IsRefusal(err) || errors.Is(err, ntable.ErrMalformedManifest) {
			return rc, err
		}
		last = err
	}
	return ntable.Receipt{}, fmt.Errorf("%w: %v", ErrUnknown, last)
}

func refusalCode(err error) string {
	var r *ntable.Refusal
	if errors.As(err, &r) {
		return r.Code
	}
	return ""
}

// resendFresh sends a manifest again after its table's revision moved: only
// when every member it names still has the revision and place it expects (and
// an absent one is still absent); an operation id already applied with other
// bytes (a refreshed send of the same manifest by another writer finishing
// the operation) counts as applied.
func (st *Store) resendFresh(ctx context.Context, man ntable.BatchManifest, cause error) (ntable.Receipt, error) {
	for i := 0; i < st.attempts(); i++ {
		switch refusalCode(cause) {
		case "OPCONFLICT":
			return ntable.Receipt{Outcome: "applied"}, nil
		case "REVISION":
		default:
			return ntable.Receipt{}, cause
		}
		rev, err := st.stillExpected(ctx, man)
		if err != nil {
			return ntable.Receipt{}, err
		}
		man.ExpectedTableRevision = strconv.FormatUint(rev, 10)
		rc, err := st.send(ctx, man)
		if err == nil {
			return rc, nil
		}
		if errors.Is(err, ErrUnknown) || !ntable.IsRefusal(err) {
			return ntable.Receipt{}, err
		}
		cause = err
	}
	return ntable.Receipt{}, cause
}

// stillExpected reads the manifest's members and returns the table revision
// when every expectation of the manifest still holds.
func (st *Store) stillExpected(ctx context.Context, man ntable.BatchManifest) (uint64, error) {
	ids := make([]string, len(man.Members))
	for i, e := range man.Members {
		ids[i] = e.ID
	}
	var rev uint64
	found := map[string]ntable.ReadSetMember{}
	for start := 0; start < len(ids); start += ntable.LimitReadSetMembers {
		end := min(start+ntable.LimitReadSetMembers, len(ids))
		rs, err := st.B.ReadSet(ctx, man.Table, ids[start:end])
		if err != nil {
			return 0, err
		}
		if start > 0 && rs.Revision != rev {
			return 0, errMoved
		}
		rev = rs.Revision
		for _, m := range rs.Members {
			found[m.ID] = m
		}
	}
	for _, e := range man.Members {
		m, ok := found[e.ID]
		x := e.Expect
		switch {
		case x != nil && x.Absent:
			if ok {
				return 0, fmt.Errorf("member %s was created by another writer", e.ID)
			}
		case !ok:
			return 0, fmt.Errorf("member %s is gone", e.ID)
		case x != nil && x.Revision != "" && x.Revision != strconv.FormatUint(m.Revision, 10):
			return 0, fmt.Errorf("member %s changed under the step (revision %s, now %d)", e.ID, x.Revision, m.Revision)
		case x != nil && x.Place != nil && (!m.Placed || m.Row != x.Place.Row || m.Col != x.Place.Col):
			return 0, fmt.Errorf("member %s moved under the step (to %s:%s)", e.ID, m.Row, m.Col)
		}
	}
	return rev, nil
}

// RepairResult is what finishing one pending operation did.
type RepairResult struct {
	Op      string   `json:"op"`
	Verb    string   `json:"verb"`
	Done    string   `json:"done"` // finished, finished-with-skips, abandoned, or open
	Detail  string   `json:"detail,omitempty"`
	Skipped []string `json:"skipped,omitempty"` // each entry skipped: card, table, expected, found
}

// Repair outcomes.
const (
	RepairFinished  = "finished"
	RepairSkipped   = "finished-with-skips"
	RepairAbandoned = "abandoned"
	RepairOpen      = "open"
)

// NRepairSkipped is the judgment a repair writes when it skipped entries of a
// cut operation: their expectations no longer held (a writer outside the
// fence changed those members), or the store refused them on a bound or a
// rule. It lists each skipped entry.
const NRepairSkipped = sprint.NRepairSkipped

// RepairSkippedDecisions are the decisions open on a repair's skips.
var RepairSkippedDecisions = sprint.Decisions[sprint.NRepairSkipped]

// Skip is one entry of a cut operation that repair did not apply because its
// expectation no longer held.
type Skip struct {
	Card, Primary, Table, Expected, Found string
	Refused                               string // the store's own refusal
	Prop                                  string // a table property skipped, in place of a card
}

func (k Skip) String() string {
	if k.Prop != "" {
		return fmt.Sprintf("table property %s on %s; the store: %s", k.Prop, k.Table, k.Refused)
	}
	return fmt.Sprintf("card %s (primary %s) on %s: expected %s, found %s; the store: %s", k.Card, k.Primary, k.Table, k.Expected, k.Found, k.Refused)
}

// finish completes a pending operation from its record: each manifest sent
// again in order (an applied one replays), then the release with its
// notifications. A first manifest refused within the grace is left to its
// writer; past it, it is applied entry by entry as a later one is, and only
// when none of its changes applied, nor its writer's own send of it, nothing
// of the operation happened and it is abandoned. A later manifest whose expectations no longer all hold (a writer
// outside the fence changed a member) is applied entry by entry: every entry
// whose expectation holds applies, every other is skipped, never overwriting
// newer state, and the release writes one judgment listing the skips (the
// model's Repair: a move applies only where its expectation holds). Before
// the first manifest goes entry by entry, the entries left are judged
// together by the lifecycle (sprint.Lawful) against a fresh read, the skipped
// ones counted as not happening: an entry it refuses (a waiter whose landing
// is skipped) is skipped too, and the one judgment lists it with why. One the
// store does not answer stays pending, and says why.
func (st *Store) finish(ctx context.Context, op OpRecord) (RepairResult, error) {
	r, err := st.finishOp(ctx, op)
	if err != nil {
		return r, err
	}
	return r, st.repaired(ctx, r)
}

func (st *Store) finishOp(ctx context.Context, op OpRecord) (RepairResult, error) {
	r := RepairResult{Op: op.ID, Verb: op.Verb, Done: RepairFinished}
	var skips []Skip
	// barred is the work-table entries the lifecycle refuses, judged once,
	// when the first manifest goes entry by entry: a later manifest holding
	// one goes entry by entry too, so it is skipped, never applied.
	var barred map[string]string
	byEntry := func(i int, man ntable.BatchManifest) *RepairResult {
		if barred == nil {
			b, err := st.rejudge(ctx, op, i)
			if err != nil {
				return &RepairResult{Op: op.ID, Verb: op.Verb, Done: RepairOpen, Detail: fmt.Sprintf("table %s cannot be judged: %v", man.Table, err)}
			}
			barred = b
		}
		sk, err := st.applyEntries(ctx, man, barred)
		if err != nil {
			return &RepairResult{Op: op.ID, Verb: op.Verb, Done: RepairOpen, Detail: fmt.Sprintf("table %s cannot finish: %v", man.Table, err)}
		}
		skips = append(skips, sk...)
		return nil
	}
	for i, man := range op.Manifests {
		if st.bars(man, barred) {
			if open := byEntry(i, man); open != nil {
				return *open, nil
			}
			continue
		}
		_, err := st.send(ctx, man)
		if err == nil || refusalCode(err) == "OPCONFLICT" {
			continue
		}
		if errors.Is(err, ErrUnknown) {
			return RepairResult{Op: op.ID, Verb: op.Verb, Done: RepairOpen, Detail: "the store did not answer: " + err.Error()}, nil
		}
		if refusalCode(err) == "REVISION" {
			ferr := err
			if _, ferr = st.resendFresh(ctx, man, err); ferr == nil {
				continue
			}
			if errors.Is(ferr, ErrUnknown) {
				return RepairResult{Op: op.ID, Verb: op.Verb, Done: RepairOpen, Detail: fmt.Sprintf("table %s: the store did not answer: %v", man.Table, ferr)}, nil
			}
		}
		if i > 0 {
			if open := byEntry(i, man); open != nil {
				return *open, nil
			}
			continue
		}
		if i == 0 {
			// a first manifest refused on a bound or a rule can never apply:
			// it is abandoned at once, whoever its writer is
			if st.now().Sub(op.At) < st.grace() && curable(err, man) {
				return RepairResult{Op: op.ID, Verb: op.Verb, Done: RepairOpen, Detail: "in flight: its first manifest has not applied yet"}, nil
			}
			// Past the grace its writer may still be alive: the first manifest
			// goes entry by entry, as a later one does. When none of its
			// changes applies, the table layer's record says whether its
			// writer applied it whole (a refreshed send): then it counts as
			// applied; else nothing of the operation happened, and it is
			// abandoned.
			if curable(err, man) {
				before := len(skips)
				if open := byEntry(0, man); open != nil {
					return *open, nil
				}
				if changedAny(man, skips[before:]) {
					continue
				}
				sent, perr := st.sentBefore(ctx, man)
				if perr != nil {
					return RepairResult{Op: op.ID, Verb: op.Verb, Done: RepairOpen, Detail: fmt.Sprintf("table %s: the store did not answer: %v", man.Table, perr)}, nil
				}
				skips = skips[:before]
				if sent {
					continue
				}
			}
			if err := st.B.Release(ctx, abandonment(op, st.Actor, st.now()), true); err != nil {
				return r, err
			}
			return RepairResult{Op: op.ID, Verb: op.Verb, Done: RepairAbandoned, Detail: "its first manifest never applied: " + err.Error()}, nil
		}
	}
	if len(skips) > 0 {
		op = st.withSkips(op, skips)
		r.Done = RepairSkipped
		for _, k := range skips {
			r.Skipped = append(r.Skipped, k.String())
		}
		r.Detail = fmt.Sprintf("%d entries skipped, the store refused them as recorded; judgment %s: %s", len(skips), op.ID+".skip", strings.Join(r.Skipped, "; "))
	}
	if err := st.B.Release(ctx, op, true); err != nil {
		return RepairResult{Op: op.ID, Verb: op.Verb, Done: RepairOpen, Detail: "the commit was not confirmed: " + err.Error()}, nil
	}
	return r, nil
}

// applyEntries applies a later manifest of a cut operation entry by entry,
// each under its own operation id derived from the manifest's, so an entry
// applied by an earlier repair replays (or conflicts, when sent against
// another table revision) and counts as applied. An entry the table layer
// refuses on its own expectations is skipped, with what it expected and what
// the store holds; so is one refused on a bound or a rule, with the store's
// own text; so is one the lifecycle refuses (barred, by stored id), with why,
// and it is never sent.
func (st *Store) applyEntries(ctx context.Context, man ntable.BatchManifest, barred map[string]string) ([]Skip, error) {
	var skips []Skip
	changedOne := false
	for j, e := range man.Members {
		one := man
		one.Props, one.PropExpect, one.PropAbsent = nil, nil, nil
		one.Members = []ntable.BatchMemberEntry{e}
		one.OperationID = fmt.Sprintf("%s.e%d", man.OperationID, j+1)
		outcome := ""
		for a := 0; a < st.attempts() && outcome == ""; a++ {
			rs, err := st.B.ReadSet(ctx, man.Table, []string{e.ID})
			if err != nil {
				return nil, err
			}
			if why, ok := barred[e.ID]; ok && man.Table == st.Names.Table(sprint.Work) {
				k := skipOf(man.Table, e, rs)
				k.Refused = "the lifecycle, judged against a fresh read with the skipped entries not happening: " + why
				skips = append(skips, k)
				outcome = "skipped"
				break
			}
			one.ExpectedTableRevision = strconv.FormatUint(rs.Revision, 10)
			_, err = st.send(ctx, one)
			switch {
			case err == nil, refusalCode(err) == "OPCONFLICT":
				outcome = "applied"
				changedOne = changedOne || hasChanges(e)
			case refusalCode(err) == "REVISION":
				// the table moved between the read and the send: read again
			case !ntable.IsRefusal(err) && !errors.Is(err, ntable.ErrMalformedManifest):
				return nil, err
			default:
				// its own expectation, or a bound or a rule of the table layer
				k := skipOf(man.Table, e, rs)
				k.Refused = err.Error()
				skips = append(skips, k)
				outcome = "skipped"
			}
		}
		if outcome == "" {
			return nil, fmt.Errorf("member %s: the table kept moving through %d sends", e.ID, st.attempts())
		}
	}
	propSkips, err := st.applyProps(ctx, man, changedOne)
	if err != nil {
		return nil, err
	}
	return append(skips, propSkips...), nil
}

// applyProps applies the table properties of a manifest finished entry by
// entry: only when one of its member changes applied, in a manifest of its own
// holding the properties and their expectations, which refuses them all
// when an expectation no longer holds. Otherwise they are skipped: a deal none
// of whose cards applied leaves its index where it was (L1 contract
// amendment, table properties, section 4).
func (st *Store) applyProps(ctx context.Context, man ntable.BatchManifest, changedOne bool) ([]Skip, error) {
	if len(man.Props)+len(man.PropExpect)+len(man.PropAbsent) == 0 {
		return nil, nil
	}
	skipAll := func(why string) []Skip {
		var out []Skip
		for _, name := range sortedKeys(man.Props) {
			out = append(out, Skip{Table: man.Table, Prop: name, Refused: why})
		}
		return out
	}
	if !changedOne {
		return skipAll("no member change of its manifest applied"), nil
	}
	one := man
	one.Members = []ntable.BatchMemberEntry{}
	one.OperationID = man.OperationID + ".props"
	for a := 0; a < st.attempts(); a++ {
		shapes, err := st.B.Shapes(ctx, []string{man.Table})
		if err != nil {
			return nil, err
		}
		one.ExpectedTableRevision = strconv.FormatUint(shapes[0].Revision, 10)
		_, err = st.send(ctx, one)
		switch {
		case err == nil, refusalCode(err) == "OPCONFLICT":
			return nil, nil
		case refusalCode(err) == "REVISION":
		case !ntable.IsRefusal(err) && !errors.Is(err, ntable.ErrMalformedManifest):
			return nil, err
		default:
			return skipAll(err.Error()), nil
		}
	}
	return nil, fmt.Errorf("the properties of %s: the table kept moving through %d sends", man.OperationID, st.attempts())
}

// sortedKeys is a map's keys in order.
func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// rejudge holds the entries of the operation's manifests from the from-th on
// to the lifecycle, together (sprint.Rejudge), against a fresh read of the
// work table: an entry whose expectation no longer holds there is skipped by
// the table layer, so it counts as not happening, as do the entries already
// skipped. It returns each work-table entry the lifecycle refuses, by stored
// id, with why.
func (st *Store) rejudge(ctx context.Context, op OpRecord, from int) (map[string]string, error) {
	pre, err := st.Load(ctx, []string{sprint.Work}, nil)
	if err != nil {
		return nil, err
	}
	work := st.Names.Table(sprint.Work)
	stored := map[string]string{}
	var changes []sprint.Change
	for _, man := range op.Manifests[from:] {
		if man.Table != work {
			continue
		}
		for _, e := range man.Members {
			if !holds(e, pre.Work) {
				continue
			}
			id := sprint.CardID(e.ID)
			stored[id] = e.ID
			e.ID = id
			changes = append(changes, sprint.Change{Table: sprint.Work, Entry: e})
		}
	}
	out := map[string]string{}
	for _, r := range sprint.Rejudge(pre, op.Verb, changes) {
		out[stored[r.Key]] = r.Why
	}
	return out, nil
}

// holds says an entry's expectation holds in the table as read: its card
// absent when it expects absent, else present at the revision and place it
// expects.
func holds(e ntable.BatchMemberEntry, t *sprint.Table) bool {
	c := t.Placed(sprint.CardID(e.ID))
	x := e.Expect
	switch {
	case x == nil:
		return true
	case x.Absent:
		return t.Card(sprint.CardID(e.ID)) == nil
	case c == nil:
		return false
	case x.Revision != "" && x.Revision != strconv.FormatUint(c.Rev, 10):
		return false
	case x.Place != nil && (c.Row != x.Place.Row || c.Col != x.Place.Col):
		return false
	}
	return true
}

// changedAny says an entry of the manifest that changes its member applied:
// it is not among the skips.
func changedAny(man ntable.BatchManifest, skips []Skip) bool {
	skipped := map[string]bool{}
	for _, k := range skips {
		skipped[k.Card] = true
	}
	for _, e := range man.Members {
		if hasChanges(e) && !skipped[e.ID] {
			return true
		}
	}
	return false
}

// sentBefore says the table layer records the manifest's operation id as
// applied (its writer sent it, perhaps against a refreshed revision): a send
// of it against a revision no table reaches is answered from that record (a
// replay, or a conflict with the other bytes) before the revision is judged,
// and is refused on the revision, applying nothing, when there is none.
func (st *Store) sentBefore(ctx context.Context, man ntable.BatchManifest) (bool, error) {
	probe := man
	probe.ExpectedTableRevision = strconv.FormatUint(math.MaxUint64, 10)
	_, err := st.send(ctx, probe)
	switch {
	case err == nil, refusalCode(err) == "OPCONFLICT":
		return true, nil
	case errors.Is(err, ErrUnknown):
		return false, err
	}
	return false, nil
}

// bars says the manifest is the work table's and holds an entry the
// lifecycle refused.
func (st *Store) bars(man ntable.BatchManifest, barred map[string]string) bool {
	if man.Table != st.Names.Table(sprint.Work) {
		return false
	}
	for _, e := range man.Members {
		if _, ok := barred[e.ID]; ok {
			return true
		}
	}
	return false
}

// skipOf describes a skipped entry: what it expected and what the store holds.
func skipOf(table string, e ntable.BatchMemberEntry, rs ntable.ReadSetResult) Skip {
	// The card and its primary are named by their card ids: a stored id of a
	// later epoch (id~n) never leaks into a judgment or a command.
	k := Skip{Card: sprint.CardID(e.ID), Primary: sprint.CardID(e.ID), Table: table, Expected: "the member present", Found: "no member"}
	var m *ntable.ReadSetMember
	for i := range rs.Members {
		if rs.Members[i].ID == e.ID {
			m = &rs.Members[i]
		}
	}
	if p := e.Set["primary"]; p != "" {
		k.Primary = sprint.CardID(p)
	} else if m != nil && m.Fields["primary"] != "" {
		k.Primary = sprint.CardID(m.Fields["primary"])
	}
	if x := e.Expect; x != nil {
		var want []string
		if x.Absent {
			want = append(want, "absent")
		}
		if x.Revision != "" {
			want = append(want, "revision "+x.Revision)
		}
		if x.Place != nil {
			want = append(want, "at "+x.Place.Row+":"+x.Place.Col)
		}
		names := make([]string, 0, len(x.Fields))
		for f := range x.Fields {
			names = append(names, f)
		}
		sort.Strings(names)
		for _, f := range names {
			g := x.Fields[f]
			switch {
			case g.Equals != nil:
				want = append(want, f+"="+*g.Equals)
			case g.Absent != nil:
				want = append(want, f+" absent")
			case g.OneOf != nil:
				want = append(want, f+" one of "+strings.Join(g.OneOf, "|"))
			}
		}
		if len(want) > 0 {
			k.Expected = strings.Join(want, ", ")
		}
	}
	if m != nil {
		have := []string{"revision " + strconv.FormatUint(m.Revision, 10)}
		if m.Placed {
			have = append(have, "at "+m.Row+":"+m.Col)
		} else {
			have = append(have, "not placed")
		}
		if x := e.Expect; x != nil {
			names := make([]string, 0, len(x.Fields))
			for f := range x.Fields {
				names = append(names, f)
			}
			sort.Strings(names)
			for _, f := range names {
				if v, ok := m.Fields[f]; ok {
					have = append(have, f+"="+v)
				} else {
					have = append(have, f+" absent")
				}
			}
		}
		k.Found = strings.Join(have, ", ")
	}
	return k
}

// withSkips is the operation as its repair releases it: one judgment listing
// every skipped entry, its primaries the subjects, and the caller's recorded
// result carrying the skips.
func (st *Store) withSkips(op OpRecord, skips []Skip) OpRecord {
	var prims, lines []string
	seen := map[string]bool{}
	for _, k := range skips {
		lines = append(lines, k.String())
		// a table property's skip has no primary: it is a line of the
		// judgment, and no subject of it
		if k.Primary != "" && !seen[k.Primary] {
			seen[k.Primary] = true
			prims = append(prims, k.Primary)
		}
	}
	sort.Strings(prims)
	n := sprint.Note{ID: op.ID + ".skip", Kind: sprint.Judgment, Type: NRepairSkipped, Primaries: prims, Count: len(prims),
		What: fmt.Sprintf("repair of %s (%s) skipped %d entries the store refused as recorded: %s", op.ID, op.Verb, len(skips), strings.Join(lines, "; ")),
		Who:  st.Actor, At: st.now(), Decisions: append([]string(nil), RepairSkippedDecisions...)}
	op.Notes = append(append([]sprint.Note(nil), op.Notes...), n)
	// a skipped entry did not happen: its move line is not written, and the
	// skip's judgment says what was skipped
	var kept []sprint.Line
	for _, l := range op.Log {
		skipped := false
		for _, k := range skips {
			skipped = skipped || l.Card == k.Card && (k.Table == l.Table || k.Table == st.Names.Table(l.Table))
		}
		if !skipped {
			kept = append(kept, l)
		}
	}
	op.Log = kept
	var res Result
	if op.Result != "" && json.Unmarshal([]byte(op.Result), &res) == nil {
		res.Skipped = lines
		res.Notes++
		if body, err := json.Marshal(res); err == nil {
			op.Result = string(body)
		}
	}
	return op
}

// Repair finishes the pending operation, if any.
func (st *Store) Repair(ctx context.Context) ([]RepairResult, error) {
	st, err := st.pin(ctx)
	if err != nil {
		return nil, err
	}
	f, err := st.B.ReadFence(ctx)
	if err != nil || f.Pending == nil {
		return nil, err
	}
	r, err := st.finish(ctx, *f.Pending)
	if err != nil {
		return nil, err
	}
	return []RepairResult{r}, nil
}

// abandonment is the release of an operation that never started: nothing of it
// is written but one notification that it was abandoned, which verb, by whom,
// how old, and who abandoned it.
func abandonment(op OpRecord, by string, now time.Time) OpRecord {
	who := ""
	if len(op.Manifests) > 0 {
		who = op.Manifests[0].Actor
	}
	n := sprint.Note{ID: op.ID + ".abandoned", Kind: sprint.Happened, Type: sprint.NAbandoned, At: now, Who: by,
		What: fmt.Sprintf("operation %s (%s) by %s, %s old: its first manifest never applied", op.ID, op.Verb, who, now.Sub(op.At).Round(time.Second))}
	return OpRecord{ID: op.ID, Verb: op.Verb, At: op.At, Notes: []sprint.Note{n}}
}
