package config

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// View is one row as Redis holds it: the kind's fields in the same canonical
// text as a Row, read back from the keys the runtime tools read. The diff is
// Row.Fields against View, field by field, so Redis is always a copy of
// Postgres and never the other way round (docs/SPEC-CONFIG.md, "Apply").
type View map[string]string

// Applier is the Redis side of apply. RedisApplier is the one over
// go-redis; the unit tests hand in a fake.
type Applier interface {
	// Read returns Redis's rows of the kind by name, and the revision of the
	// kind stamped in config:decl (0 when none). It writes nothing, so
	// --check can call it against a store whose function library is
	// missing.
	Read(ctx context.Context, kind string) (map[string]View, int64, error)
	// Prepare is called once before the first write: it installs the Redis
	// Function library when the store has none (fn.LoadMissing).
	Prepare(ctx context.Context) error
	// Write adds (prev nil) or updates one row through the runtime's own
	// functions. A refusal (CEILING, an actor without the role) is a
	// *RefusedError.
	Write(ctx context.Context, kind string, row Row, prev View, actor, idem string) error
	// Remove removes one row's keys. A row the runtime still holds (a
	// machine with consumers) is a *RefusedError naming them, and
	// nothing is written.
	Remove(ctx context.Context, kind, name, actor, idem string) error
	// Stamp records rev as the kind's applied revision, compare-and-set
	// against prev: a store whose stamp moved past prev is ErrConflict.
	Stamp(ctx context.Context, kind string, prev, rev int64) error
}

// Op is one line of a plan.
type Op struct {
	Op      string   // OpAdd, OpSet, OpRemove or OpHeld
	Name    string   // the row's name, or the whole held line for OpHeld (SaidLine prints it verbatim)
	Changed []string // the fields that differ, for OpSet
	Row     Row      // the row to write, for OpAdd and OpSet
	Prev    View     // Redis's row, for OpSet and OpRemove
}

// OpHeld is the held write a publish reports instead of moving the sprint
// seat (docs/SPEC-CONFIG.md, "sprint"): its Name is the one line. SaidLine
// prints that name verbatim. nova-config apply prints through OpLine, which
// wraps the name; OpLine is outside this change.
const OpHeld = "held"

// Plan diffs the kind's rows against Redis's views: an add for a row Redis
// lacks, a set for one that differs in any field, a remove for a name in
// Redis that Postgres has not. Adds and sets come in the kind's apply order,
// then removes by name, so a machine's ceiling is written before a friend
// on it is, and a friend is unregistered after everything else.
func Plan(k *Kind, rows []Row, views map[string]View) []Op {
	var ops []Op
	have := map[string]bool{}
	for _, row := range k.Sorted(rows) {
		have[row.Name] = true
		prev, ok := views[row.Name]
		if !ok {
			ops = append(ops, Op{Op: OpAdd, Name: row.Name, Row: row})
			continue
		}
		var changed []string
		for _, f := range k.Fields {
			if row.Fields[f.Name] != prev[f.Name] {
				changed = append(changed, f.Name)
			}
		}
		if len(changed) > 0 {
			ops = append(ops, Op{Op: OpSet, Name: row.Name, Changed: changed, Row: row, Prev: prev})
		}
	}
	var gone []string
	for name := range views {
		if !have[name] {
			gone = append(gone, name)
		}
	}
	sort.Strings(gone)
	for _, name := range gone {
		ops = append(ops, Op{Op: OpRemove, Name: name, Prev: views[name]})
	}
	return ops
}

// Result is one kind's apply.
type Result struct {
	Kind                string
	Rev                 int64 // Postgres's revision of the kind
	Add, Set, Remove    int
	Ops                 []Op
	Check               bool // --check: nothing was written
	RedisRev            int64
	PreparedForWritesOK bool
}

// ErrConflict is the compare-and-set refusal: Redis holds a revision of
// the kind newer than the Postgres this process read.
type conflictError struct {
	Kind          string
	Redis, Wanted int64
}

func (e *conflictError) Error() string {
	return fmt.Sprintf("CONFLICT %s: Redis holds rev %d and this Postgres is at rev %d; a newer Postgres applied it", e.Kind, e.Redis, e.Wanted)
}

// IsConflict reports whether err is the compare-and-set refusal.
func IsConflict(err error) bool {
	var c *conflictError
	return errors.As(err, &c)
}

// Conflict is the refusal Stamp returns when the stamp moved.
func Conflict(kind string, redis, wanted int64) error {
	return &RefusedError{Err: &conflictError{Kind: kind, Redis: redis, Wanted: wanted}, Detail: (&conflictError{Kind: kind, Redis: redis, Wanted: wanted}).Error()}
}

// Idem is the idempotency marker every apply write carries into cap:log:
// the kind and the revision it applied.
func Idem(kind string, rev int64) string { return fmt.Sprintf("config:%s:%d", kind, rev) }

// Apply applies one kind from the store into Redis (docs/SPEC-CONFIG.md,
// "Apply"): read Postgres and its revision, read Redis and its stamp, refuse
// CONFLICT when Redis is ahead, plan, and unless check is set write every op
// in order and stamp the revision. Every op is reported to report before it
// is written, so a refusal part way names what was written before it.
// A publish never moves the sprint seat (docs/SPEC-CONFIG.md, "sprint"): a
// sprint:coordinator the live store disagrees with is held, every other
// sprint field is written and one OpHeld line is said. The seat moves by
// nova-sprint's seat verb, or by ApplyMovingSeat (nova-config apply --move-seat).
func Apply(ctx context.Context, st Store, ap Applier, kind, actor string, check bool, report func(Op)) (Result, error) {
	return apply(ctx, st, ap, kind, actor, check, false, report)
}

// ApplyMovingSeat is Apply with the seat move named (docs/SPEC-CONFIG.md,
// "sprint"): it writes a sprint:coordinator the live store disagrees with.
// nova-config apply --move-seat calls it.
func ApplyMovingSeat(ctx context.Context, st Store, ap Applier, kind, actor string, check bool, report func(Op)) (Result, error) {
	return apply(ctx, st, ap, kind, actor, check, true, report)
}

// apply is Apply with the seat move named: moveSeat (ApplyMovingSeat, the
// owner's word) writes a sprint:coordinator the live store disagrees with;
// without it the publish holds it, keeps the live value and says the one
// OpHeld line (docs/SPEC-CONFIG.md, "sprint").
func apply(ctx context.Context, st Store, ap Applier, kind, actor string, check, moveSeat bool, report func(Op)) (Result, error) {
	k, ok := Lookup(kind)
	if !ok {
		return Result{}, fmt.Errorf("unknown kind %q; the kinds are %s", kind, strings.Join(KindNames(), ", "))
	}
	rows, err := st.List(ctx, kind)
	if err != nil {
		return Result{}, err
	}
	if kind == KindFleet {
		for _, row := range rows {
			if err := ValidateFleetEndpoints(View(row.Fields)); err != nil {
				return Result{}, &RefusedError{Err: ErrInvalid, Detail: err.Error()}
			}
		}
	}
	if k.Derive != nil {
		if rows, err = k.Derive(ctx, st, rows); err != nil {
			return Result{}, err
		}
	}
	rev, err := st.Rev(ctx, kind)
	if err != nil {
		return Result{}, err
	}
	views, redisRev, err := ap.Read(ctx, kind)
	if err != nil {
		return Result{}, err
	}
	res := Result{Kind: kind, Rev: rev, Check: check, RedisRev: redisRev}
	if redisRev > rev {
		return res, Conflict(kind, redisRev, rev)
	}
	res.Ops = Plan(k, rows, views)
	for _, op := range res.Ops {
		switch op.Op {
		case OpAdd:
			res.Add++
		case OpSet:
			res.Set++
		case OpRemove:
			res.Remove++
		}
	}
	if check {
		for _, op := range res.Ops {
			report(op)
			reportSeatHold(kind, moveSeat, op, report)
		}
		return res, nil
	}
	if len(res.Ops) > 0 {
		if err := ap.Prepare(ctx); err != nil {
			return res, err
		}
		res.PreparedForWritesOK = true
	}
	type friendPrefetcher interface {
		PrefetchFriends(ctx context.Context, names []string) error
	}
	if pf, ok := ap.(friendPrefetcher); ok && kind == KindFriend {
		var names []string
		for _, op := range res.Ops {
			if op.Op == OpAdd || op.Op == OpSet {
				names = append(names, op.Name)
			}
		}
		if len(names) > 0 {
			if err := pf.PrefetchFriends(ctx, names); err != nil {
				return res, err
			}
		}
	}
	idem := Idem(kind, rev)
	for _, op := range res.Ops {
		report(op)
		var err error
		// A publish never moves the sprint seat (docs/SPEC-CONFIG.md,
		// "sprint"): a live coordinator the row disagrees with is held —
		// every other field is written, the live value stands and the one
		// OpHeld line is said — unless the caller names the seat move.
		row, _ := reportSeatHold(kind, moveSeat, op, report)
		switch op.Op {
		case OpAdd, OpSet:
			err = ap.Write(ctx, kind, row, op.Prev, actor, idem)
		case OpRemove:
			err = ap.Remove(ctx, kind, op.Name, actor, idem)
		}
		if err != nil {
			return res, err
		}
	}
	if len(res.Ops) > 0 || redisRev != rev {
		if err := ap.Stamp(ctx, kind, redisRev, rev); err != nil {
			return res, err
		}
	}
	return res, nil
}

// heldLine is the one line a held seat says: the live coordinator and the
// row's, and the two ways on (docs/SPEC-CONFIG.md, "sprint"): make the row
// agree, or name the move with apply --move-seat.
func heldLine(live, row string) string {
	return fmt.Sprintf("APPLY HELD kind=sprint field=coordinator live=%s row=%s: the seat moves by nova-sprint's seat verb or nova-config apply --kind sprint --move-seat; run nova-config sprint set --coordinator %s to make the row agree", live, row, live)
}

// SaidLine is the line a caller prints for one op. An OpHeld name is the
// whole line, said verbatim, so a publish's held seat is one line
// (docs/SPEC-CONFIG.md, "sprint") and not wrapped. Every other op is OpLine.
// nova-config apply prints every op through it.
func SaidLine(word, kind string, op Op) string {
	if op.Op == OpHeld {
		return op.Name
	}
	return OpLine(word, kind, op)
}

// reportSeatHold says the one OpHeld line and returns the row to write when
// a publish must leave sprint:coordinator as the live store has it
// (docs/SPEC-CONFIG.md, "sprint"). A first apply (no live value), a row that
// already agrees, and ApplyMovingSeat write the row's coordinator.
func reportSeatHold(kind string, moveSeat bool, op Op, report func(Op)) (Row, bool) {
	row := op.Row
	if kind != KindSprint || moveSeat || (op.Op != OpAdd && op.Op != OpSet) || op.Prev == nil {
		return row, false
	}
	live := op.Prev["coordinator"]
	want := ""
	if row.Fields != nil {
		want = row.Fields["coordinator"]
	}
	if live == "" || live == want {
		return row, false
	}
	report(Op{Op: OpHeld, Name: heldLine(live, want)})
	row = op.Row.Clone()
	row.Fields["coordinator"] = live
	return row, true
}
