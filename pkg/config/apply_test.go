package config

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
		{friend, "stella", map[string]string{"tiers": "frontier,pro", "roles": "builder,reader"}},
		{friend, "rowan", map[string]string{"tiers": "frontier", "roles": "builder"}},
	} {
		row, err := r.k.NewRow(r.n, r.raw)
		require.NoError(t, err)
		_, err = st.Insert(ctx, r.k.Name, row, "rowan")
		require.NoError(t, err)
	}
	// rev 5: the fleet's coordinator machine; rev 6: the sprint's
	// coordinator friend.
	_, _, err := st.Update(ctx, KindFleet, KindFleet, map[string]string{"coordinator": "studio", "redis_port": "6380", "pg_dsn": "postgres://nova_config@localhost:5432/nova"}, "rowan")
	require.NoError(t, err)
	_, _, err = st.Update(ctx, KindSprint, KindSprint, map[string]string{"coordinator": "rowan"}, "rowan")
	require.NoError(t, err)
	return st
}

func TestPlanDiffsRowsAgainstRedis(t *testing.T) {
	t.Parallel()

	friend, _ := Lookup(KindFriend)
	rows := []Row{
		{Name: "stella", Fields: map[string]string{"tiers": "pro", "roles": "builder"}},
		{Name: "rowan", Fields: map[string]string{"tiers": "frontier", "roles": "coordinator"}},
		{Name: "emma", Fields: map[string]string{"tiers": "flash", "roles": ""}},
	}
	views := map[string]View{
		"stella": {"tiers": "pro", "roles": "builder"},
		"rowan":  {"tiers": "frontier,pro", "roles": "coordinator"},
		"gone":   {"tiers": "", "roles": ""},
	}
	var got []string
	for _, op := range Plan(friend, rows, views) {
		got = append(got, op.Op+":"+op.Name+":"+strings.Join(op.Changed, ","))
	}
	want := []string{"set:rowan:tiers", "add:emma:", "remove:gone:"}
	require.Equal(t, strings.Join(want, " "), strings.Join(got, " "), "plan %v, want %v (coordinator first, adds and sets before removes, an unchanged row absent)", got, want)
}

func TestApplyWritesEveryDifferenceThenStamps(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st := seed(t)
	ap := newFake()
	var reported []string
	report := func(op Op) { reported = append(reported, op.Op+":"+op.Name) }

	res, err := Apply(ctx, st, ap, KindMachine, "rowan", false, report)
	require.NoError(t, err)
	assertionMsg157 := []any{"machine result %+v", res}
	require.Equal(t, 2, res.Add, assertionMsg157...)
	require.Equal(t, 0, res.Set, assertionMsg157...)
	require.Equal(t, 0, res.Remove, assertionMsg157...)
	require.Equal(t, int64(2), res.Rev, assertionMsg157...)
	require.Equal(t, int64(0), res.RedisRev, assertionMsg157...)
	res, err = Apply(ctx, st, ap, KindFriend, "rowan", false, report)
	require.NoError(t, err)
	assertionMsg160 := []any{"friend result %+v", res}
	require.Equal(t, 2, res.Add, assertionMsg160...)
	require.Equal(t, int64(4), res.Rev, assertionMsg160...)
	wantLog := []string{
		"add machine hulk as=rowan idem=config:machine:2",
		"add machine studio as=rowan idem=config:machine:2",
		"stamp machine 2",
		"add friend rowan as=rowan idem=config:friend:4",
		"add friend stella as=rowan idem=config:friend:4",
		"stamp friend 4",
	}
	require.Equal(t, wantLog, ap.log, "writes differ from the expected sequence")
	require.Equal(t, "add:hulk add:studio add:rowan add:stella", strings.Join(reported, " "), "reported %v: the sprint's coordinator first", reported)
	require.Equal(t, 2, ap.prepared, "prepared %d times, want once per kind with writes", ap.prepared)
	// The row written for the sprint's coordinator carries the role; the
	// stored row does not.
	got := ap.views[KindFriend]["rowan"]["roles"]
	require.Equal(t, "builder,coordinator", got, "rowan's roles in Redis %q, want builder,coordinator (derived from the sprint row)", got)
	stored, _, _ := st.Get(ctx, KindFriend, "rowan")
	require.Equal(t, "builder", stored.Fields["roles"], "rowan's stored roles %q changed", stored.Fields["roles"])

	// A second apply is a no-op: nothing written, the stamp unchanged.
	ap.log = nil
	res, err = Apply(ctx, st, ap, KindFriend, "rowan", false, report)
	assertionMsg188 := []any{"second apply: %+v %v log %v", res, err, ap.log}
	require.NoError(t, err, assertionMsg188...)
	require.Equal(t, 0, res.Add+res.Set+res.Remove, assertionMsg188...)
	require.Empty(t, ap.log, assertionMsg188...)
	require.Equal(t, int64(4), ap.revs[KindFriend], assertionMsg188...)
	require.Equal(t, 2, ap.prepared, "a no-op apply still prepared the library")

	// A change in Postgres is one set; a removal is one remove.
	_, _, err = st.Update(ctx, KindFriend, "stella", map[string]string{"width": "12"}, "rowan")
	require.NoError(t, err)
	_, _, err = st.Update(ctx, KindSprint, KindSprint, map[string]string{"coordinator": ""}, "rowan")
	require.NoError(t, err)
	_, err = st.Delete(ctx, KindFriend, "rowan", "rowan")
	require.NoError(t, err)
	ap.log = nil
	res, err = Apply(ctx, st, ap, KindFriend, "rowan", false, report)
	assertionMsg206 := []any{"third apply: %+v %v", res, err}
	require.NoError(t, err, assertionMsg206...)
	require.Equal(t, 1, res.Set, assertionMsg206...)
	require.Equal(t, 1, res.Remove, assertionMsg206...)
	require.Equal(t, int64(9), res.Rev, assertionMsg206...)
	require.Equal(t, []string{
		"set friend stella as=rowan idem=config:friend:9",
		"remove friend rowan as=rowan idem=config:friend:9",
		"stamp friend 9",
	}, ap.log, "third apply writes")
}

// TestAHandoverIsTwoRoleSetsNewCoordinatorFirst: `sprint set --coordinator
// stella` changes no friend row, and the next friend apply writes stella's
// roles with the coordinator role before rowan's without it.
func TestAHandoverIsTwoRoleSetsNewCoordinatorFirst(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st := seed(t)
	ap := newFake()
	_, err := Apply(ctx, st, ap, KindFriend, "rowan", false, func(Op) {})
	require.NoError(t, err)
	_, _, err = st.Update(ctx, KindSprint, KindSprint, map[string]string{"coordinator": "stella"}, "rowan")
	require.NoError(t, err)
	var reported []string
	res, err := Apply(ctx, st, ap, KindFriend, "rowan", false, func(op Op) { reported = append(reported, op.Op+":"+op.Name+":"+strings.Join(op.Changed, ",")) })
	assertionMsg231 := []any{"handover apply: %+v %v", res, err}
	require.NoError(t, err, assertionMsg231...)
	require.Equal(t, 2, res.Set, assertionMsg231...)
	require.Equal(t, 0, res.Add+res.Remove, assertionMsg231...)
	require.Equal(t, "set:stella:roles set:rowan:roles", strings.Join(reported, " "), "handover reported %v", reported)
	assertionMsg233 := []any{"roles after the handover %v", ap.views[KindFriend]}
	require.Equal(t, "builder,coordinator,reader", ap.views[KindFriend]["stella"]["roles"], assertionMsg233...)
	require.Equal(t, "builder", ap.views[KindFriend]["rowan"]["roles"], assertionMsg233...)
}

// TestApplyOfASingletonIsASetNeverAnAdd: the fleet and sprint rows exist
// on both sides. An undeclared endpoint refuses before any writes, and a
// declared fleet set is one SET naming the fields;
// a REMOVE is never planned.
func TestApplyOfASingletonIsASetNeverAnAdd(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st := seed(t)
	ap := newFake()
	var reported []string
	report := func(op Op) { reported = append(reported, op.Op+":"+op.Name+":"+strings.Join(op.Changed, ",")) }
	res, err := Apply(ctx, st, ap, KindSprint, "rowan", false, report)
	assertionMsg249 := []any{"sprint: %+v %v reported %v", res, err, reported}
	require.NoError(t, err, assertionMsg249...)
	require.Equal(t, 1, res.Set, assertionMsg249...)
	require.Equal(t, int64(6), res.Rev, assertionMsg249...)
	require.Equal(t, "set:sprint:coordinator,decide_bounce,decide_review,git_identity,max_attempts", strings.Join(reported, " "), assertionMsg249...)
	require.Equal(t, "rowan", ap.views[KindSprint][KindSprint]["coordinator"], "sprint view %v", ap.views[KindSprint])
	reported = nil
	_, _, err = st.Update(ctx, KindFleet, KindFleet, map[string]string{"coordinator": ""}, "rowan")
	require.NoError(t, err)
	empty := NewMem()
	_, err = Apply(ctx, empty, ap, KindFleet, "rowan", false, report)
	require.ErrorContains(t, err, "endpoints are unset: redis_port, pg_dsn")
	require.Empty(t, reported)
	require.Equal(t, "set sprint sprint as=rowan idem=config:sprint:6 stamp sprint 6", strings.Join(ap.log, " "), "empty fleet wrote %v", ap.log)
	reported = nil
	ap.log = nil
	_, _, err = st.Update(ctx, KindFleet, KindFleet, map[string]string{"store": "hulk", "coordinator": "studio"}, "rowan")
	require.NoError(t, err)
	ap.log = nil
	res, err = Apply(ctx, st, ap, KindFleet, "rowan", false, report)
	assertionMsg267 := []any{"fleet set: %+v %v", res, err}
	require.NoError(t, err, assertionMsg267...)
	require.Equal(t, 1, res.Set, assertionMsg267...)
	require.Equal(t, 0, res.Add, assertionMsg267...)
	require.Equal(t, int64(8), res.Rev, assertionMsg267...)
	assertionMsg268 := []any{"fleet set reported %v wrote %v", reported, ap.log}
	require.Equal(t, "set:fleet:store,coordinator,redis_port,pg_dsn,loops_dir", strings.Join(reported, " "), assertionMsg268...)
	require.Equal(t, "set fleet fleet as=rowan idem=config:fleet:8 stamp fleet 8", strings.Join(ap.log, " "), assertionMsg268...)
	require.Equal(t, "hulk", ap.views[KindFleet][KindFleet]["store"], "fleet view %v", ap.views[KindFleet])
	require.Equal(t, "6380", ap.views[KindFleet][KindFleet]["redis_port"], "fleet view %v", ap.views[KindFleet])
}

func TestApplyRefusesMissingFleetEndpointsWithoutRewritingALegacyMember(t *testing.T) {
	t.Parallel()
	for _, check := range []bool{false, true} {
		t.Run(fmt.Sprintf("check=%t", check), func(t *testing.T) {
			t.Parallel()
			st, ap := NewMem(), newFake()
			_, _, err := st.Update(context.Background(), KindFleet, KindFleet, map[string]string{"pg_dsn": "postgres://user@localhost:5432/nova"}, "operator")
			require.NoError(t, err)
			const argv = `["/usr/bin/env","NOVA_SPRINT_REDIS=bench-beta:6380","nova-swarm","member"]`
			legacy := loopView("member-beta", "bench-beta", map[string]string{"argv": argv})
			ap.views[KindLoop] = map[string]View{"member-beta": legacy}
			_, err = Apply(context.Background(), st, ap, KindFleet, "operator", check, func(Op) { assert.Fail(t, "refusal reported an operation") })
			require.ErrorContains(t, err, "endpoints are unset: redis_port")
			require.ErrorContains(t, err, "nova-config fleet set --redis_port <port> --pg_dsn <dsn>")
			require.Empty(t, ap.log)
			require.Zero(t, ap.prepared)
			require.Empty(t, ap.revs)
			require.Equal(t, argv, ap.views[KindLoop]["member-beta"]["argv"])
		})
	}
}

func TestApplyCheckWritesNothing(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st := seed(t)
	ap := newFake()
	var reported []string
	res, err := Apply(ctx, st, ap, KindFriend, "rowan", true, func(op Op) { reported = append(reported, op.Op+":"+op.Name) })
	require.NoError(t, err)
	assertionMsg281 := []any{"check wrote: %+v log %v prepared %d revs %v", res, ap.log, ap.prepared, ap.revs}
	require.True(t, res.Check, assertionMsg281...)
	require.Equal(t, 2, res.Add, assertionMsg281...)
	require.Empty(t, ap.log, assertionMsg281...)
	require.Equal(t, 0, ap.prepared, assertionMsg281...)
	require.Equal(t, int64(0), ap.revs[KindFriend], assertionMsg281...)
	require.Equal(t, "add:rowan add:stella", strings.Join(reported, " "), "check reported %v", reported)
}

func TestApplyRefusesConflictWhenRedisIsAhead(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st := seed(t)
	ap := newFake()
	ap.revs[KindFriend] = 9
	_, err := Apply(ctx, st, ap, KindFriend, "rowan", false, func(Op) {})
	assertionMsg293 := []any{"Redis at rev 9, Postgres at 4: %v", err}
	require.Error(t, err, assertionMsg293...)
	require.True(t, IsConflict(err), assertionMsg293...)
	require.True(t, Refused(err), assertionMsg293...)
	assertionMsg294 := []any{"conflict line %q names neither revision", err}
	require.ErrorContains(t, err, "CONFLICT friend", assertionMsg294...)
	require.ErrorContains(t, err, "rev 9", assertionMsg294...)
	require.ErrorContains(t, err, "rev 4", assertionMsg294...)
	require.Empty(t, ap.log, "a conflict wrote: %v", ap.log)
	// The stamp itself is compare-and-set: a stamp that moved under the
	// apply is the same refusal, after the writes.
	ap = newFake()
	ap.stampErr = Conflict(KindFriend, 5, 4)
	_, err = Apply(ctx, st, ap, KindFriend, "rowan", false, func(Op) {})
	assertionMsg301 := []any{"moved stamp: %v", err}
	require.Error(t, err, assertionMsg301...)
	require.True(t, IsConflict(err), assertionMsg301...)
}

// TestApplyGoesOnPastARefusalAndDoesNotStamp: a refused write names its row
// and the kind's later ops are still written; a kind with any refusal is not
// stamped, and the first refusal is returned (tla/ConfigApply.tla, DoOps and
// DoStamp; StampedKindHasNoRefusal).
func TestApplyGoesOnPastARefusalAndDoesNotStamp(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st := seed(t)
	ap := newFake()
	refusal := &RefusedError{Err: ErrActor, Detail: "roles of rowan: --as rowan does not hold the coordinator role in Redis"}
	ap.refuse["rowan"] = refusal
	var reported []string
	res, err := Apply(ctx, st, ap, KindFriend, "rowan", false, func(op Op) { reported = append(reported, op.Name) })
	require.ErrorIs(t, err, ErrActor, "the first refusal is returned: %v", err)
	require.True(t, Refused(err), "refusal: %v", err)
	require.Equal(t, "add friend stella as=rowan idem=config:friend:4", strings.Join(ap.log, " "), "rowan is refused, stella after it is still written, and no stamp: %v", ap.log)
	require.Equal(t, int64(0), ap.revs[KindFriend], "a kind with a refused op was stamped")
	require.False(t, res.Stamped, "res %+v", res)
	require.Equal(t, 1, res.Written, "res %+v", res)
	require.Len(t, res.Refused, 1, "res %+v", res)
	require.Equal(t, Refusal{Kind: KindFriend, Name: "rowan", Err: refusal, Absent: true}, res.Refused[0], "rowan's refused op was an add: absent from Redis")
	require.Equal(t, "rowan stella", strings.Join(reported, " "), "reported %v: every op is reported before it is tried", reported)

	// The retry, with the refusal gone, writes only the diff and stamps.
	delete(ap.refuse, "rowan")
	ap.log = nil
	res, err = Apply(ctx, st, ap, KindFriend, "rowan", false, func(Op) {})
	require.NoError(t, err)
	require.True(t, res.Stamped, "res %+v", res)
	require.Equal(t, "add friend rowan as=rowan idem=config:friend:4 stamp friend 4", strings.Join(ap.log, " "), "the retry: %v", ap.log)
}

// TestApplyInRunRefusesARowNamingARowRefusedEarlier: the ref rule
// (ConfigApply.tla RefRefused). A row whose ref names a row refused earlier
// in the run is refused with that reason, never written, and its kind is not
// stamped, when the named row is absent from Redis or the op changes the ref
// field; --check names it too.
func TestApplyInRunRefusesARowNamingARowRefusedEarlier(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	for _, absent := range []bool{true, false} {
		for _, check := range []bool{false, true} {
			st := seed(t)
			ap := newFake()
			earlier := Earlier{}
			earlier.Add(KindFriend, "rowan", "roles refused", absent)
			// A fresh Redis: the sprint op sets coordinator "" -> rowan, so
			// the ref field changes and even a present rowan refuses it.
			res, err := ApplyInRun(ctx, st, ap, KindSprint, "rowan", check, false, earlier, func(Op) {})
			require.ErrorIs(t, err, ErrRefusedRef, "absent=%t check=%t: %v", absent, check, err)
			require.Len(t, res.Refused, 1, "absent=%t check=%t: %+v", absent, check, res)
			require.Equal(t, "sprint sprint: its coordinator names friend rowan, refused earlier in this run (roles refused)", res.Refused[0].Err.Error())
			require.Empty(t, ap.log, "absent=%t check=%t: a row naming a refused row was written: %v", absent, check, ap.log)
			require.Zero(t, ap.revs[KindSprint], "absent=%t check=%t", absent, check)
		}
	}
	// A row whose refs name no refused row is written.
	res, err := ApplyInRun(ctx, seed(t), newFake(), KindSprint, "rowan", false, false, Earlier{KindFriend: {"stella": {Why: "roles refused", Absent: true}}}, func(Op) {})
	require.NoError(t, err)
	require.True(t, res.Stamped, "res %+v", res)
}

// TestApplyInRunWritesARowThatOnlyNamesAPresentRefusedRow is the incident
// the ref rule must not bring back: the coordinator friend's set is refused
// (the friend is still in Redis), and the sprint row, whose coordinator does
// not change, is still written, its git_identity with it.
func TestApplyInRunWritesARowThatOnlyNamesAPresentRefusedRow(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st := seed(t)
	ap := newFake()
	_, err := Apply(ctx, st, ap, KindSprint, "rowan", false, func(Op) {})
	require.NoError(t, err)
	_, _, err = st.Update(ctx, KindSprint, KindSprint, map[string]string{FieldGitIdentity: "Rowan <rowan@mas-bandwidth.com>"}, "rowan")
	require.NoError(t, err)
	earlier := Earlier{}
	earlier.Add(KindFriend, "rowan", "roles refused", false)
	var changed []string
	res, err := ApplyInRun(ctx, st, ap, KindSprint, "rowan", false, false, earlier, func(op Op) { changed = op.Changed })
	require.NoError(t, err, "a row naming a present refused row, its ref unchanged, is written")
	require.Equal(t, []string{FieldGitIdentity}, changed)
	require.True(t, res.Stamped, "res %+v", res)
	require.Equal(t, "Rowan <rowan@mas-bandwidth.com>", ap.views[KindSprint][KindSprint][FieldGitIdentity])
}

// TestApplyInRunWritesTheSprintRowWhenTheLiveSeatIsHeld: the live seat in
// Redis is stella, the stored row names rowan, whose set was refused. The row
// written holds stella, so its coordinator does not change and the ref rule
// does not refuse it: git_identity lands (ConfigApply.tla
// SprintRefusedOnlyForItsWrittenRef), in --check too.
func TestApplyInRunWritesTheSprintRowWhenTheLiveSeatIsHeld(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	for _, check := range []bool{false, true} {
		st := seed(t)
		ap := newFake()
		_, err := Apply(ctx, st, ap, KindSprint, "rowan", false, func(Op) {})
		require.NoError(t, err)
		ap.views[KindSprint][KindSprint]["coordinator"] = "stella"
		_, _, err = st.Update(ctx, KindSprint, KindSprint, map[string]string{FieldGitIdentity: "Rowan <rowan@mas-bandwidth.com>"}, "rowan")
		require.NoError(t, err)
		earlier := Earlier{}
		earlier.Add(KindFriend, "rowan", "roles refused", false)
		var held bool
		res, err := ApplyInRun(ctx, st, ap, KindSprint, "rowan", check, false, earlier, func(op Op) { held = held || op.Op == OpHeld })
		require.NoError(t, err, "check=%t: the held row's coordinator does not change", check)
		require.Empty(t, res.Refused, "check=%t: %+v", check, res)
		require.True(t, held, "check=%t: the held line is said", check)
		if !check {
			require.True(t, res.Stamped, "res %+v", res)
			require.Equal(t, "Rowan <rowan@mas-bandwidth.com>", ap.views[KindSprint][KindSprint][FieldGitIdentity])
			require.Equal(t, "stella", ap.views[KindSprint][KindSprint]["coordinator"], "the live seat is held")
		}
	}
}

// TestApplyStopsAtAStoreErrorAfterARefusal: a store or connection error is
// never a refusal, even after a refusal in the same kind: the apply returns
// it at once (ConfigApply.tla StoreError), with the refusal before it still
// in Result.Refused, and stamps nothing.
func TestApplyStopsAtAStoreErrorAfterARefusal(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st := seed(t)
	ap := newFake()
	ap.refuse["rowan"] = &RefusedError{Err: ErrActor, Detail: "roles of rowan: --as rowan does not hold the coordinator role in Redis"}
	ap.refuse["stella"] = fmt.Errorf("redis: connection refused")
	res, err := Apply(ctx, st, ap, KindFriend, "rowan", false, func(Op) {})
	require.EqualError(t, err, "redis: connection refused")
	require.False(t, Refused(err), "a store error classed as a refusal")
	require.Len(t, res.Refused, 1, "res %+v", res)
	require.Equal(t, "rowan", res.Refused[0].Name)
	require.Empty(t, ap.log, "nothing written: %v", ap.log)
	require.Zero(t, ap.revs[KindFriend], "a kind stopped by a store error was stamped")
	// A plain error with no refusal before it stops the same way.
	ap = newFake()
	ap.refuse["rowan"] = fmt.Errorf("redis: connection refused")
	res, err = Apply(ctx, st, ap, KindFriend, "rowan", false, func(Op) {})
	require.EqualError(t, err, "redis: connection refused")
	require.Empty(t, res.Refused, "a store error is not a refusal: %+v", res)
	require.Empty(t, ap.log, "a write after the store error: %v", ap.log)
}

func TestApplyRefusesAnUnknownKind(t *testing.T) {
	t.Parallel()

	_, err := Apply(context.Background(), NewMem(), newFake(), "lane", "rowan", false, func(Op) {})
	assertionMsg324 := []any{"unknown kind: %v", err}
	require.Error(t, err, assertionMsg324...)
	require.ErrorContains(t, err, "unknown kind \"lane\"; the kinds are machine, fleet, friend, sprint, loop, provider, route", assertionMsg324...)
}

// insertRoute is a store with one route row whose raw fields are given.
func insertRoute(t *testing.T, name string, raw map[string]string) (Store, *Kind) {
	t.Helper()
	ctx := context.Background()
	st := NewMem()
	route, _ := Lookup(KindRoute)
	row, err := route.NewRow(name, raw)
	require.NoError(t, err)
	addProvider(t, st, row.Fields["provider"])
	_, err = st.Insert(ctx, KindRoute, row, "operator")
	require.NoError(t, err)
	return st, route
}

// TestApplyRefusesAnEnabledRouteWithNoPrice: an enabled route with no price
// sheet is refused before the plan and any write, at apply and apply --check
// alike, naming the route and its model and the command that sets its price
// (docs/SPEC-CONFIG.md, "route" and "Apply"). Nothing reaches Redis, and a
// provider without a list gets no refresh offer.
func TestApplyRefusesAnEnabledRouteWithNoPrice(t *testing.T) {
	t.Parallel()

	for _, check := range []bool{false, true} {
		t.Run(fmt.Sprintf("check=%t", check), func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			st, _ := insertRoute(t, "pro-mercury3", map[string]string{"tier": "pro", "provider": "inception", "model": "mercury-3-preview-1002", "deadline": "1800"})
			ap := newFake()
			res, err := Apply(ctx, st, ap, KindRoute, "operator", check, func(Op) { assert.Fail(t, "a refusal reported an operation") })
			require.Error(t, err)
			require.True(t, Refused(err), "a refusal (exit 1), not a failure")
			require.ErrorIs(t, err, ErrInvalid)
			require.ErrorContains(t, err, "route pro-mercury3 (inception/mercury-3-preview-1002) is enabled with no price")
			require.ErrorContains(t, err, "nova-config route set pro-mercury3 --price_input <USD per million> --price_output <USD per million>")
			require.NotContains(t, err.Error(), "route prices --refresh")
			require.ErrorContains(t, err, "take it out: --enabled false --note '<why>'")
			require.Zero(t, res.Add+res.Set+res.Remove)
			require.Empty(t, ap.log, "a refused apply wrote: %v", ap.log)
			require.Zero(t, ap.prepared, "a refused apply prepared the library")
			require.Empty(t, ap.revs, "a refused apply stamped a revision: %v", ap.revs)
		})
	}

	// a provider with a list gets the refresh as one more remedy
	ctx := context.Background()
	st, _ := insertRoute(t, "pro-openrouter", map[string]string{"tier": "pro", "provider": "openrouter", "model": "x-ai/grok-4", "deadline": "1800"})
	_, err := Apply(ctx, st, newFake(), KindRoute, "operator", true, func(Op) {})
	require.Error(t, err)
	require.ErrorContains(t, err, "or nova-config route prices --refresh --provider openrouter")
}

// TestApplyTakesADisabledUnpricedRoute: a disabled route is never refused for
// its price (docs/SPEC-CONFIG.md, "route").
func TestApplyTakesADisabledUnpricedRoute(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st, _ := insertRoute(t, "pro-off", map[string]string{"tier": "pro", "provider": "inception", "model": "mercury-3-preview-1002", "deadline": "1800", "enabled": "false", "note": "no price yet"})
	ap := newFake()
	res, err := Apply(ctx, st, ap, KindRoute, "operator", false, func(Op) {})
	require.NoError(t, err)
	require.Equal(t, 1, res.Add)
	require.Equal(t, []string{"add route pro-off as=operator idem=config:route:2", "stamp route 2"}, ap.log)
}

// TestApplyTakesAPricedRoute: a route with any price in its sheet is applied,
// never refused (a sheet with one price is priced: cardcost.Prices.Priced).
func TestApplyTakesAPricedRoute(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st, _ := insertRoute(t, "pro-priced", map[string]string{"tier": "pro", "provider": "inception", "model": "mercury-3-preview-1002", "deadline": "1800", "price_input": "0.30", "price_output": "1.20"})
	ap := newFake()
	res, err := Apply(ctx, st, ap, KindRoute, "operator", false, func(Op) {})
	require.NoError(t, err)
	require.Equal(t, 1, res.Add)
	require.Equal(t, "0.3", ap.views[KindRoute]["pro-priced"]["price_input"])
}

// TestApplyRefusesToMoveTheSeat: a publish never moves the coordinator seat
// (docs/SPEC-CONFIG.md, "sprint"): apply holds a sprint:coordinator the live
// store disagrees with, writes every other sprint field, reports one OpHeld
// whose SaidLine is the one APPLY HELD line, and exits 0. The seat moves by
// nova-sprint's seat verb. ApplyMovingSeat (apply --move-seat) writes a
// differing coordinator. A first apply, with no live key,
// writes the row's coordinator. A check reports the same line and writes nothing.
func TestApplyRefusesToMoveTheSeat(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	friend, _ := Lookup(KindFriend)
	const held = "APPLY HELD kind=sprint field=coordinator live=a row=b: the seat moves by nova-sprint's seat verb or nova-config apply --kind sprint --move-seat; run nova-config sprint set --coordinator a to make the row agree"
	// The live seat holds a; the row names b and an unrelated field.
	st := NewMem()
	fb, err := friend.NewRow("b", map[string]string{"tiers": "flash"})
	require.NoError(t, err)
	_, err = st.Insert(ctx, KindFriend, fb, "rowan")
	require.NoError(t, err)
	_, _, err = st.Update(ctx, KindSprint, KindSprint, map[string]string{"coordinator": "b", FieldDecideBounce: "0.9"}, "rowan")
	require.NoError(t, err)
	ap := newFake()
	ap.views[KindSprint] = map[string]View{KindSprint: {"coordinator": "a", FieldDecideBounce: ""}}
	var said []Op
	report := func(op Op) { said = append(said, op) }
	heldSaid := func() []string {
		t.Helper()
		var lines []string
		for _, op := range said {
			if op.Op == OpHeld {
				lines = append(lines, SaidLine("APPLY", KindSprint, op))
			}
		}
		return lines
	}

	// A check says the held line and writes nothing.
	_, err = Apply(ctx, st, ap, KindSprint, "rowan", true, report)
	require.NoError(t, err)
	require.Equal(t, []string{held}, heldSaid(), "a check says the one held line: %v", said)
	require.Empty(t, ap.log, "a check wrote: %v", ap.log)
	said = nil

	// A publish holds the seat: every other sprint field is written, the
	// one APPLY HELD line is said, and the call exits 0.
	_, err = Apply(ctx, st, ap, KindSprint, "rowan", false, report)
	require.NoError(t, err)
	require.Equal(t, "a", ap.views[KindSprint][KindSprint]["coordinator"], "the live coordinator is moved: %v", ap.views[KindSprint])
	require.Equal(t, "0.9", ap.views[KindSprint][KindSprint][FieldDecideBounce], "the other sprint field is written: %v", ap.views[KindSprint])
	require.Equal(t, []string{held}, heldSaid(), "the one held line: %v", said)
	require.Contains(t, held, "--move-seat", "the line names the flag that moves the seat")

	// ApplyMovingSeat (the owner's word) writes the differing coordinator.
	said = nil
	_, err = ApplyMovingSeat(ctx, st, ap, KindSprint, "rowan", false, report)
	require.NoError(t, err)
	require.Equal(t, "b", ap.views[KindSprint][KindSprint]["coordinator"], "the seat move the owner names: %v", ap.views[KindSprint])
	require.Empty(t, heldSaid(), "a named seat move says no held line: %v", said)

	// A first apply, with no live key, writes the row's coordinator.
	st2 := NewMem()
	fc, err := friend.NewRow("c", map[string]string{"tiers": "flash"})
	require.NoError(t, err)
	_, err = st2.Insert(ctx, KindFriend, fc, "rowan")
	require.NoError(t, err)
	_, _, err = st2.Update(ctx, KindSprint, KindSprint, map[string]string{"coordinator": "c"}, "rowan")
	require.NoError(t, err)
	ap2 := newFake()
	_, err = Apply(ctx, st2, ap2, KindSprint, "rowan", false, report)
	require.NoError(t, err)
	require.Equal(t, "c", ap2.views[KindSprint][KindSprint]["coordinator"], "a first apply writes the row's coordinator: %v", ap2.views[KindSprint])
}
