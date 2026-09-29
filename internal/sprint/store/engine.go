package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
// pending: check and where show it, and repair says why it cannot finish.

// Store runs sprint steps against a Backend.
type Store struct {
	B        Backend
	Names    sprint.Names
	Actor    string
	Now      func() time.Time
	NewID    func() string       // a fresh operation id family
	Sleep    func(time.Duration) // backoff between attempts on a busy fence; nil does not wait
	Attempts int                 // plans per step before giving up on a busy fence; default 12
	Resends  int                 // sends of one manifest after a lost reply; default 3
	// Grace is how long an operation is taken as in flight (its writer alive)
	// before a writer that finds its first manifest unapplied abandons it.
	Grace time.Duration
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
	return 12
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

func (st *Store) backoff(attempt int) {
	if st.Sleep != nil && attempt > 1 {
		st.Sleep(time.Duration(attempt*attempt) * 5 * time.Millisecond)
	}
}

// Fenced reads the tables with the fence read before and after, finishing a
// pending operation first: the snapshot is no partial state of any operation,
// and gen is the fence's generation it was read at.
func (st *Store) Fenced(ctx context.Context, tables []string, extras func(*sprint.Snapshot) map[string][]string, repaired *[]string) (*sprint.Snapshot, uint64, error) {
	for i := 1; i <= st.attempts(); i++ {
		st.backoff(i)
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
				if st.Now().Sub(f.Pending.At) < st.grace() {
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
	return nil, 0, fmt.Errorf("the sprint is busy: other operations kept the fence moving through %d reads; run the verb again", st.attempts())
}

// Run plans and applies a step as one operation.
func (st *Store) Run(ctx context.Context, step Step) (Result, error) {
	res := Result{Verb: step.Verb}
	if step.CallerOp != "" {
		if raw, ok, err := st.B.Done(ctx, step.CallerOp); err != nil {
			return res, err
		} else if ok {
			if err := json.Unmarshal([]byte(raw), &res); err != nil {
				return res, fmt.Errorf("operation %s: its recorded result is unreadable: %w", step.CallerOp, err)
			}
			res.Replay = true
			return res, nil
		}
	}
	family := step.CallerOp
	if family == "" {
		family = strings.ReplaceAll(step.Verb, " ", "-") + "-" + st.NewID()
	}
	rowsAdded := false
	for res.Attempts < st.attempts() {
		res.Attempts++
		snap, gen, err := st.Fenced(ctx, step.Load, step.Extras, &res.Repaired)
		if err != nil {
			return res, err
		}
		if step.CallerOp != "" {
			if raw, ok, err := st.B.Done(ctx, step.CallerOp); err == nil && ok {
				_ = json.Unmarshal([]byte(raw), &res)
				res.Replay = true
				return res, nil
			}
		}
		plan := step.Plan(snap)
		if len(plan.Rows) > 0 && !rowsAdded {
			if err := st.addRows(ctx, plan.Rows); err != nil {
				return res, err
			}
			rowsAdded = true
			res.Attempts--
			continue
		}
		res.Refused = plan.Refused
		res.Moved = nil
		for _, u := range plan.Units {
			if u.Moved != "" {
				res.Moved = append(res.Moved, u.Moved)
			}
		}
		op, err := st.operation(step.Verb, family+"-"+strconv.Itoa(res.Attempts), plan, snap)
		if err != nil {
			return res, err
		}
		if len(op.Manifests) == 0 && len(op.Notes)+len(op.Decided)+len(op.Closes) == 0 {
			res.Moved = nil
			return st.after(ctx, step, res)
		}
		op.CallerOp = step.CallerOp
		res.Op = op.ID
		res.Notes = len(op.Notes) + len(op.Decided)
		body, _ := json.Marshal(res)
		op.Result = string(body)
		ok, err := st.B.Acquire(ctx, gen, op)
		if err != nil {
			return res, fmt.Errorf("%w: acquiring the fence for %s: %v; run: nova-sprint repair", ErrUnknown, op.ID, err)
		}
		if !ok {
			st.backoff(res.Attempts)
			continue
		}
		applied, err := st.apply(ctx, op)
		if err != nil {
			res.Pending = op.ID
			return res, err
		}
		if !applied {
			if err := st.B.Release(ctx, op, false); err != nil {
				res.Pending = op.ID
				return res, fmt.Errorf("%w: releasing %s after its first manifest was refused: %v; run: nova-sprint repair", ErrUnknown, op.ID, err)
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
	for _, k := range keys {
		res.Refused = append(res.Refused, sprint.Refusal{Key: k, Why: fmt.Sprintf("the sprint kept changing under this step (%d attempts); run it again", res.Attempts)})
	}
	return res, nil
}

func (st *Store) after(ctx context.Context, step Step, res Result) (Result, error) {
	if step.Mirrors {
		if err := st.SyncMirrors(ctx); err != nil {
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

func hasChanges(e ntable.BatchMemberEntry) bool {
	return e.Create != nil || e.Move != nil || e.Remove || len(e.Set) > 0 || len(e.Unset) > 0
}

// operation builds the step's operation from its plan: per table, in
// ApplyOrder, the units' entries in order (a repeated guard once, the bumps
// of a counter card summed into one entry), split into manifests of at most
// ntable.LimitChangedEntries changed and ntable.LimitGuardEntries guard-only
// entries, each expecting the revision the one before it leaves; then the
// notifications and the answers.
func (st *Store) operation(verb, id string, plan sprint.Plan, snap *sprint.Snapshot) (OpRecord, error) {
	op := OpRecord{ID: id, Verb: verb, At: snap.Now}
	entries := map[string][]ntable.BatchMemberEntry{}
	seen := map[entryKey]int{} // index+1 in entries[table]
	bumps := map[entryKey]map[string]int{}
	var bumpOrder []entryKey
	streams := map[string]bool{}
	for _, u := range plan.Units {
		if u.Stream != "" {
			streams[u.Stream] = true
		}
		for _, c := range u.Changes {
			k := entryKey{c.Table, c.Entry.ID}
			if i := seen[k]; i > 0 {
				if hasChanges(c.Entry) || hasChanges(entries[c.Table][i-1]) {
					return op, fmt.Errorf("card %s is changed twice in one step of %s", c.Entry.ID, c.Table)
				}
				continue
			}
			entries[c.Table] = append(entries[c.Table], c.Entry)
			seen[k] = len(entries[c.Table])
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
		entries[k.table] = append(entries[k.table], ntable.BatchMemberEntry{ID: c.ID,
			Expect: &ntable.MemberExpect{Revision: strconv.FormatUint(c.Rev, 10), Place: &ntable.PlaceExpect{Row: c.Row, Col: c.Col}}, Set: set})
	}
	k := 0
	for _, t := range sprint.ApplyOrder {
		tb := snap.T(t)
		rev := uint64(0)
		if tb != nil {
			rev = tb.Revision
		}
		var cur []ntable.BatchMemberEntry
		changed, guards := 0, 0
		flush := func() {
			if len(cur) == 0 {
				return
			}
			k++
			op.Manifests = append(op.Manifests, ntable.BatchManifest{Schema: 1, Table: st.Names.Table(t), Epoch: strconv.FormatUint(tb.Epoch, 10),
				ExpectedTableRevision: strconv.FormatUint(rev, 10), OperationID: fmt.Sprintf("%s-%d", id, k), Actor: st.Actor, Members: cur})
			rev++
			cur, changed, guards = nil, 0, 0
		}
		for _, e := range entries[t] {
			if hasChanges(e) {
				if changed == ntable.LimitChangedEntries {
					flush()
				}
				changed++
			} else {
				if guards == ntable.LimitGuardEntries {
					flush()
				}
				guards++
			}
			cur = append(cur, e)
		}
		flush()
	}
	var all []sprint.Note
	var closes []sprint.Open
	for _, u := range plan.Units {
		all = append(all, u.Notes...)
		closes = append(closes, u.Closes...)
	}
	all = append(all, plan.Notes...)
	closes = append(closes, plan.Closes...)
	var kept []sprint.Note
	for _, n := range all {
		if n.Type != "" {
			kept = append(kept, n)
			if n.Stream != "" {
				streams[n.Stream] = true
			}
		}
	}
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
			d = &sprint.Note{Kind: sprint.Decided, Type: o.Note.Type, Stream: o.Note.Stream, Answers: o.Note.ID,
				What: "answered by " + verb, Who: st.Actor, At: snap.Now}
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
		if i == 0 && ntable.IsRefusal(err) {
			return false, nil
		}
		return false, &CutError{Op: op.ID, Table: man.Table, Cause: err}
	}
	return true, nil
}

// unreadableReceipt says the store answered with its RECEIPT and this build
// could not read the receipt's delta (a store whose function library is of
// another build): the batch committed. ntable.ApplyBatch parses the delta
// only after it has seen the RECEIPT reply.
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
			return ntable.Receipt{Outcome: "committed; its delta unreadable by this build"}, nil
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
	Op     string `json:"op"`
	Verb   string `json:"verb"`
	Done   string `json:"done"` // finished, abandoned, or open
	Detail string `json:"detail,omitempty"`
}

// finish completes a pending operation from its record: each manifest sent
// again in order (an applied one replays), then the release with its
// notifications. When its first manifest never applied, nothing of it
// happened: past the grace it is abandoned, within it it is left to its
// writer. One that cannot finish stays pending, and says why.
func (st *Store) finish(ctx context.Context, op OpRecord) (RepairResult, error) {
	r := RepairResult{Op: op.ID, Verb: op.Verb, Done: "finished"}
	for i, man := range op.Manifests {
		_, err := st.send(ctx, man)
		if err == nil || refusalCode(err) == "OPCONFLICT" {
			continue
		}
		if errors.Is(err, ErrUnknown) {
			return RepairResult{Op: op.ID, Verb: op.Verb, Done: "open", Detail: "the store did not answer: " + err.Error()}, nil
		}
		if refusalCode(err) == "REVISION" {
			if _, ferr := st.resendFresh(ctx, man, err); ferr == nil {
				continue
			} else if i > 0 {
				return RepairResult{Op: op.ID, Verb: op.Verb, Done: "open", Detail: fmt.Sprintf("table %s cannot finish: %v", man.Table, ferr)}, nil
			}
		}
		if i == 0 {
			if st.Now().Sub(op.At) < st.grace() {
				return RepairResult{Op: op.ID, Verb: op.Verb, Done: "open", Detail: "in flight: its first manifest has not applied yet"}, nil
			}
			if err := st.B.Release(ctx, op, false); err != nil {
				return r, err
			}
			return RepairResult{Op: op.ID, Verb: op.Verb, Done: "abandoned", Detail: "its first manifest never applied: " + err.Error()}, nil
		}
		return RepairResult{Op: op.ID, Verb: op.Verb, Done: "open", Detail: fmt.Sprintf("table %s cannot finish: %v", man.Table, err)}, nil
	}
	if err := st.B.Release(ctx, op, true); err != nil {
		return RepairResult{Op: op.ID, Verb: op.Verb, Done: "open", Detail: "the commit was not confirmed: " + err.Error()}, nil
	}
	return r, nil
}

// Repair finishes the pending operation, if any.
func (st *Store) Repair(ctx context.Context) ([]RepairResult, error) {
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
