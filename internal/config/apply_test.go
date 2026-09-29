package config

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// fakeApplier is the unit tests' Redis: views per kind, a stamp per kind
// and a log of every write, so a test reads what apply asked for without a
// server.
type fakeApplier struct {
	views    map[string]map[string]View
	revs     map[string]int64
	log      []string
	prepared int
	refuse   map[string]error // name -> the refusal Write or Remove answers
	stampErr error
}

func newFake() *fakeApplier {
	return &fakeApplier{views: map[string]map[string]View{}, revs: map[string]int64{}, refuse: map[string]error{}}
}

func (f *fakeApplier) Read(_ context.Context, kind string) (map[string]View, int64, error) {
	out := map[string]View{}
	for n, v := range f.views[kind] {
		c := View{}
		for k, s := range v {
			c[k] = s
		}
		out[n] = c
	}
	// A singleton's view is always there, empty until written (the real
	// reader's shape: the row exists on both sides).
	if k, _ := Lookup(kind); k != nil && k.Singleton && out[kind] == nil {
		out[kind] = View{}
		for _, fl := range k.Fields {
			out[kind][fl.Name] = ""
		}
	}
	return out, f.revs[kind], nil
}

func (f *fakeApplier) Prepare(context.Context) error { f.prepared++; return nil }

func (f *fakeApplier) Write(_ context.Context, kind string, row Row, prev View, actor, idem string) error {
	if err := f.refuse[row.Name]; err != nil {
		return err
	}
	op := OpAdd
	if prev != nil {
		op = OpSet
	}
	f.log = append(f.log, fmt.Sprintf("%s %s %s as=%s idem=%s", op, kind, row.Name, actor, idem))
	if f.views[kind] == nil {
		f.views[kind] = map[string]View{}
	}
	f.views[kind][row.Name] = View(row.Clone().Fields)
	return nil
}

func (f *fakeApplier) Remove(_ context.Context, kind, name, actor, idem string) error {
	if err := f.refuse[name]; err != nil {
		return err
	}
	f.log = append(f.log, fmt.Sprintf("%s %s %s as=%s idem=%s", OpRemove, kind, name, actor, idem))
	delete(f.views[kind], name)
	return nil
}

func (f *fakeApplier) Stamp(_ context.Context, kind string, prev, rev int64) error {
	if f.stampErr != nil {
		return f.stampErr
	}
	if f.revs[kind] != prev {
		return Conflict(kind, f.revs[kind], rev)
	}
	f.revs[kind] = rev
	f.log = append(f.log, fmt.Sprintf("stamp %s %d", kind, rev))
	return nil
}

func seed(t *testing.T) *Mem {
	t.Helper()
	ctx := context.Background()
	st := NewMem()
	machine, _ := Lookup(KindMachine)
	friend, _ := Lookup(KindFriend)
	for _, r := range []struct {
		k   *Kind
		n   string
		raw map[string]string
	}{
		{machine, "studio", map[string]string{"user": "glenn", "seat": "studio", "slots": "64", "runners": "1"}},
		{machine, "hulk", map[string]string{"user": "gaffer", "seat": "swarm-hulk", "slots": "64"}},
		{friend, "stella", map[string]string{"slots": "32", "tiers": "frontier,pro", "roles": "builder,reader"}},
		{friend, "rowan", map[string]string{"slots": "32", "tiers": "frontier", "roles": "builder"}},
	} {
		row, err := r.k.NewRow(r.n, r.raw)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := st.Insert(ctx, r.k.Name, row, "rowan"); err != nil {
			t.Fatal(err)
		}
	}
	// rev 5: the fleet's coordinator machine; rev 6: the sprint's
	// coordinator friend.
	if _, _, err := st.Update(ctx, KindFleet, KindFleet, map[string]string{"coordinator": "studio"}, "rowan"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.Update(ctx, KindSprint, KindSprint, map[string]string{"coordinator": "rowan"}, "rowan"); err != nil {
		t.Fatal(err)
	}
	return st
}

func TestPlanDiffsRowsAgainstRedis(t *testing.T) {
	t.Parallel()

	friend, _ := Lookup(KindFriend)
	rows := []Row{
		{Name: "stella", Fields: map[string]string{"slots": "32", "tiers": "pro", "roles": "builder"}},
		{Name: "rowan", Fields: map[string]string{"slots": "64", "tiers": "frontier", "roles": "coordinator"}},
		{Name: "emma", Fields: map[string]string{"slots": "8", "tiers": "flash", "roles": ""}},
	}
	views := map[string]View{
		"stella": {"slots": "32", "tiers": "pro", "roles": "builder"},
		"rowan":  {"slots": "32", "tiers": "frontier,pro", "roles": "coordinator"},
		"gone":   {"slots": "1", "tiers": "", "roles": ""},
	}
	var got []string
	for _, op := range Plan(friend, rows, views) {
		got = append(got, op.Op+":"+op.Name+":"+strings.Join(op.Changed, ","))
	}
	want := []string{"set:rowan:slots,tiers", "add:emma:", "remove:gone:"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("plan %v, want %v (coordinator first, adds and sets before removes, an unchanged row absent)", got, want)
	}
}

func TestApplyWritesEveryDifferenceThenStamps(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st := seed(t)
	ap := newFake()
	var reported []string
	report := func(op Op) { reported = append(reported, op.Op+":"+op.Name) }

	res, err := Apply(ctx, st, ap, KindMachine, "rowan", false, report)
	if err != nil {
		t.Fatal(err)
	}
	if res.Add != 2 || res.Set != 0 || res.Remove != 0 || res.Rev != 2 || res.RedisRev != 0 {
		t.Fatalf("machine result %+v", res)
	}
	res, err = Apply(ctx, st, ap, KindFriend, "rowan", false, report)
	if err != nil {
		t.Fatal(err)
	}
	if res.Add != 2 || res.Rev != 4 {
		t.Fatalf("friend result %+v", res)
	}
	wantLog := []string{
		"add machine hulk as=rowan idem=config:machine:2",
		"add machine studio as=rowan idem=config:machine:2",
		"stamp machine 2",
		"add friend rowan as=rowan idem=config:friend:4",
		"add friend stella as=rowan idem=config:friend:4",
		"stamp friend 4",
	}
	if strings.Join(ap.log, "\n") != strings.Join(wantLog, "\n") {
		t.Fatalf("writes:\n%s\nwant:\n%s", strings.Join(ap.log, "\n"), strings.Join(wantLog, "\n"))
	}
	if strings.Join(reported, " ") != "add:hulk add:studio add:rowan add:stella" {
		t.Fatalf("reported %v: the sprint's coordinator first", reported)
	}
	if ap.prepared != 2 {
		t.Fatalf("prepared %d times, want once per kind with writes", ap.prepared)
	}
	// The row written for the sprint's coordinator carries the role; the
	// stored row does not.
	if got := ap.views[KindFriend]["rowan"]["roles"]; got != "builder,coordinator" {
		t.Fatalf("rowan's roles in Redis %q, want builder,coordinator (derived from the sprint row)", got)
	}
	if stored, _, _ := st.Get(ctx, KindFriend, "rowan"); stored.Fields["roles"] != "builder" {
		t.Fatalf("rowan's stored roles %q changed", stored.Fields["roles"])
	}

	// A second apply is a no-op: nothing written, the stamp unchanged.
	ap.log = nil
	res, err = Apply(ctx, st, ap, KindFriend, "rowan", false, report)
	if err != nil || res.Add+res.Set+res.Remove != 0 || len(ap.log) != 0 || ap.revs[KindFriend] != 4 {
		t.Fatalf("second apply: %+v %v log %v", res, err, ap.log)
	}
	if ap.prepared != 2 {
		t.Fatal("a no-op apply still prepared the library")
	}

	// A change in Postgres is one set; a removal is one remove.
	if _, _, err := st.Update(ctx, KindFriend, "stella", map[string]string{"slots": "48"}, "rowan"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.Update(ctx, KindSprint, KindSprint, map[string]string{"coordinator": ""}, "rowan"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Delete(ctx, KindFriend, "rowan", "rowan"); err != nil {
		t.Fatal(err)
	}
	ap.log = nil
	res, err = Apply(ctx, st, ap, KindFriend, "rowan", false, report)
	if err != nil || res.Set != 1 || res.Remove != 1 || res.Rev != 9 {
		t.Fatalf("third apply: %+v %v", res, err)
	}
	if strings.Join(ap.log, "\n") != "set friend stella as=rowan idem=config:friend:9\nremove friend rowan as=rowan idem=config:friend:9\nstamp friend 9" {
		t.Fatalf("third apply writes:\n%s", strings.Join(ap.log, "\n"))
	}
}

// TestAHandoverIsTwoRoleSetsNewCoordinatorFirst: `sprint set --coordinator
// stella` changes no friend row, and the next friend apply writes stella's
// roles with the coordinator role before rowan's without it.
func TestAHandoverIsTwoRoleSetsNewCoordinatorFirst(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st := seed(t)
	ap := newFake()
	if _, err := Apply(ctx, st, ap, KindFriend, "rowan", false, func(Op) {}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.Update(ctx, KindSprint, KindSprint, map[string]string{"coordinator": "stella"}, "rowan"); err != nil {
		t.Fatal(err)
	}
	var reported []string
	res, err := Apply(ctx, st, ap, KindFriend, "rowan", false, func(op Op) { reported = append(reported, op.Op+":"+op.Name+":"+strings.Join(op.Changed, ",")) })
	if err != nil || res.Set != 2 || res.Add+res.Remove != 0 {
		t.Fatalf("handover apply: %+v %v", res, err)
	}
	if strings.Join(reported, " ") != "set:stella:roles set:rowan:roles" {
		t.Fatalf("handover reported %v", reported)
	}
	if ap.views[KindFriend]["stella"]["roles"] != "builder,coordinator,reader" || ap.views[KindFriend]["rowan"]["roles"] != "builder" {
		t.Fatalf("roles after the handover %v", ap.views[KindFriend])
	}
}

// TestApplyOfASingletonIsASetNeverAnAdd: the fleet and sprint rows exist
// on both sides, so an empty row applies nothing (the stamp still moves to
// the revision) and a set is one SET naming the fields; a REMOVE is never
// planned.
func TestApplyOfASingletonIsASetNeverAnAdd(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st := seed(t)
	ap := newFake()
	var reported []string
	report := func(op Op) { reported = append(reported, op.Op+":"+op.Name+":"+strings.Join(op.Changed, ",")) }
	res, err := Apply(ctx, st, ap, KindSprint, "rowan", false, report)
	if err != nil || res.Set != 1 || res.Rev != 6 || strings.Join(reported, " ") != "set:sprint:coordinator" {
		t.Fatalf("sprint: %+v %v reported %v", res, err, reported)
	}
	if ap.views[KindSprint][KindSprint]["coordinator"] != "rowan" {
		t.Fatalf("sprint view %v", ap.views[KindSprint])
	}
	reported = nil
	if _, _, err := st.Update(ctx, KindFleet, KindFleet, map[string]string{"coordinator": ""}, "rowan"); err != nil {
		t.Fatal(err)
	}
	empty := NewMem()
	res, err = Apply(ctx, empty, ap, KindFleet, "rowan", false, report)
	if err != nil || res.Add+res.Set+res.Remove != 0 || res.Rev != 0 || len(reported) != 0 {
		t.Fatalf("empty fleet: %+v %v reported %v", res, err, reported)
	}
	if strings.Join(ap.log, " ") != "set sprint sprint as=rowan idem=config:sprint:6 stamp sprint 6" {
		t.Fatalf("empty fleet wrote %v", ap.log)
	}
	ap.log = nil
	if _, _, err := st.Update(ctx, KindFleet, KindFleet, map[string]string{"store": "hulk", "coordinator": "studio"}, "rowan"); err != nil {
		t.Fatal(err)
	}
	ap.log = nil
	res, err = Apply(ctx, st, ap, KindFleet, "rowan", false, report)
	if err != nil || res.Set != 1 || res.Add != 0 || res.Rev != 8 {
		t.Fatalf("fleet set: %+v %v", res, err)
	}
	if strings.Join(reported, " ") != "set:fleet:store,coordinator" || strings.Join(ap.log, " ") != "set fleet fleet as=rowan idem=config:fleet:8 stamp fleet 8" {
		t.Fatalf("fleet set reported %v wrote %v", reported, ap.log)
	}
	if ap.views[KindFleet][KindFleet]["store"] != "hulk" {
		t.Fatalf("fleet view %v", ap.views[KindFleet])
	}
}

func TestApplyCheckWritesNothing(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st := seed(t)
	ap := newFake()
	var reported []string
	res, err := Apply(ctx, st, ap, KindFriend, "rowan", true, func(op Op) { reported = append(reported, op.Op+":"+op.Name) })
	if err != nil {
		t.Fatal(err)
	}
	if !res.Check || res.Add != 2 || len(ap.log) != 0 || ap.prepared != 0 || ap.revs[KindFriend] != 0 {
		t.Fatalf("check wrote: %+v log %v prepared %d revs %v", res, ap.log, ap.prepared, ap.revs)
	}
	if strings.Join(reported, " ") != "add:rowan add:stella" {
		t.Fatalf("check reported %v", reported)
	}
}

func TestApplyRefusesConflictWhenRedisIsAhead(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st := seed(t)
	ap := newFake()
	ap.revs[KindFriend] = 9
	_, err := Apply(ctx, st, ap, KindFriend, "rowan", false, func(Op) {})
	if err == nil || !IsConflict(err) || !Refused(err) {
		t.Fatalf("Redis at rev 9, Postgres at 4: %v", err)
	}
	if !strings.Contains(err.Error(), "CONFLICT friend") || !strings.Contains(err.Error(), "rev 9") || !strings.Contains(err.Error(), "rev 4") {
		t.Fatalf("conflict line %q names neither revision", err)
	}
	if len(ap.log) != 0 {
		t.Fatalf("a conflict wrote: %v", ap.log)
	}
	// The stamp itself is compare-and-set: a stamp that moved under the
	// apply is the same refusal, after the writes.
	ap = newFake()
	ap.stampErr = Conflict(KindFriend, 5, 4)
	_, err = Apply(ctx, st, ap, KindFriend, "rowan", false, func(Op) {})
	if err == nil || !IsConflict(err) {
		t.Fatalf("moved stamp: %v", err)
	}
}

func TestApplyStopsAtARefusalAndNamesIt(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st := seed(t)
	ap := newFake()
	ceiling := &RefusedError{Err: ErrCeiling, Detail: "CEILING studio: friend stella makes the sum 70 over the machine ceiling 64"}
	ap.refuse["stella"] = ceiling
	var reported []string
	_, err := Apply(ctx, st, ap, KindFriend, "rowan", false, func(op Op) { reported = append(reported, op.Name) })
	if !errors.Is(err, ErrCeiling) || !Refused(err) {
		t.Fatalf("ceiling: %v", err)
	}
	if strings.Join(ap.log, " ") != "add friend rowan as=rowan idem=config:friend:4" {
		t.Fatalf("writes before the refusal %v: rowan is written, stella refused, nothing after and no stamp", ap.log)
	}
	if ap.revs[KindFriend] != 0 {
		t.Fatal("a refused apply stamped the revision")
	}
	if strings.Join(reported, " ") != "rowan stella" {
		t.Fatalf("reported %v: the refused op is reported before it is tried", reported)
	}
}

func TestApplyRefusesAnUnknownKind(t *testing.T) {
	t.Parallel()

	_, err := Apply(context.Background(), NewMem(), newFake(), "loop", "rowan", false, func(Op) {})
	if err == nil || !strings.Contains(err.Error(), "unknown kind \"loop\"; the kinds are machine, fleet, friend, sprint") {
		t.Fatalf("unknown kind: %v", err)
	}
}
