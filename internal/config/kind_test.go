package config

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEveryKindHasATableAndUniqueFields(t *testing.T) {
	t.Parallel()

	seen := map[string]bool{}
	for _, k := range Kinds {
		assertionMsg17 := []any{"kind %+v: name, table and doc are required", k}
		func() {
			if !assert.NotEqual(t, "", k.Name, assertionMsg17...) {
				return
			}
			if !assert.NotEqual(t, "", k.Table, assertionMsg17...) {
				return
			}
			assert.NotEqual(t, "", k.Doc, assertionMsg17...)
		}()
		assert.False(t, seen[k.Name], "kind %s is declared twice", k.Name)
		seen[k.Name] = true
		fields := map[string]bool{}
		for _, f := range k.Fields {
			assertionMsg22 := []any{"kind %s: field %q is the row key or repeated", k.Name, f.Name}
			func() {
				if !assert.NotEqual(t, "name", f.Name, assertionMsg22...) {
					return
				}
				assert.False(t, fields[f.Name], assertionMsg22...)
			}()
			fields[f.Name] = true
			assert.NotEqual(t, "", f.Help, "kind %s: field %s has no help line", k.Name, f.Name)
			if f.Type == TypeEnum || f.Type == TypeList {
				assert.NotEmpty(t, f.Enum, "kind %s: field %s is an enum with no words", k.Name, f.Name)
			}
			if f.Type == TypeRef {
				_, scopedOk42 := Lookup(f.Ref)
				assert.True(t, scopedOk42, "kind %s: field %s refers to unknown kind %q", k.Name, f.Name, f.Ref)
			}
		}
	}
	_, scopedOk49 := Lookup("nothing")
	assert.False(t, scopedOk49, "Lookup found a kind that is not declared")
}

func TestKindsApplyInDependencyOrder(t *testing.T) {
	t.Parallel()

	names := KindNames()
	require.Equal(t, "machine,fleet,friend,sprint,loop,route,tier", strings.Join(names, ","), "kinds %v: machines first (ceilings), the fleet next (a friend's slots are charged to its coordinator machine when her beat names none), friends, the sprint row (it names a friend), loops (each names a machine), routes, tiers last (each names routes)", names)
}

// TestTheMachineRowIsTheDeclaredFactsSomethingReads: Glenn 2026-09-27, "I
// only want the fleet to have actual defined useful things associated with
// each machine, not invented rando stuff". Six declared fields and the note
// (Glenn 2026-10-02: "these should be saved somewhere permanent with notes
// (ideally, nova-config)"), no address (the name is the tailnet host), no
// measured fact. width is the
// sprint member's width, set directly (the owner, 2026-10-01: "we should just
// be able to set width specifically in nova-config and it just works"). tla
// marks a TLC record machine, read by the tools play and tlacheck run --bench.
func TestTheMachineRowIsTheDeclaredFactsSomethingReads(t *testing.T) {
	t.Parallel()

	machine, _ := Lookup(KindMachine)
	scopedGot70 := strings.Join(machine.FieldNames(), ",")
	require.Equal(t, "user,seat,slots,runners,width,tla,note", scopedGot70, "machine fields %s, want user,seat,slots,runners,width,tla,note", scopedGot70)
	for _, f := range machine.Fields {
		scopedWant75 := f.Name != "runners" && f.Name != "width" && f.Name != "tla" && f.Name != "note"
		assert.Equal(t, scopedWant75, f.Required, "--%s required=%v, want %v (runners and width default to 0, tla to false and the note to empty; the rest are typed on add)", f.Name, f.Required, scopedWant75)
	}
	for _, invented := range []string{"ssh", "address", "os_arch", "os", "arch", "cores", "memory_gb", "roles", "store", "coordinator", "machine", "harness", "logins", "wake"} {
		_, scopedOk81 := machine.Field(invented)
		assert.False(t, scopedOk81, "machine has a field %s: an address is the name, a measured fact comes live from the beat, a fleet fact is the fleet's", invented)
	}
	assert.False(t, machine.Singleton, "machine is many rows")
}

// TestTheFriendRowIsWhatSomeoneDecidesForHer: Glenn 2026-09-27, "anything
// that a friend would just know, is runtime redis data". Her slots, tiers,
// roles, width, mode, config_dir, token cap, optional work restriction,
// working directory and billing; no machine, harness, logins, wake or note;
// and no coordinator role, which is the sprint row's.
func TestTheFriendRowIsWhatSomeoneDecidesForHer(t *testing.T) {
	t.Parallel()

	friend, _ := Lookup(KindFriend)
	scopedGot97 := strings.Join(friend.FieldNames(), ",")
	require.Equal(t, "slots,tiers,roles,width,mode,config_dir,token_cap,streams,kinds,dir,billing", scopedGot97, "friend fields %s, want slots,tiers,roles,width,mode,config_dir,token_cap,streams,kinds,dir,billing", scopedGot97)
	for _, f := range friend.Fields {
		scopedWant102 := f.Name == "slots" || f.Name == "tiers"
		assert.Equal(t, scopedWant102, f.Required, "--%s required=%v, want %v", f.Name, f.Required, scopedWant102)
	}
	for _, invented := range []string{"machine", "harness", "logins", "wake", "note", "coordinator"} {
		_, scopedOk108 := friend.Field(invented)
		assert.False(t, scopedOk108, "friend has a field %s: what she would just know is runtime data, who coordinates is the sprint's", invented)
	}
	assertionMsg98 := []any{"roles %v tiers %v", FriendRoles, Tiers}
	func() {
		if !assert.Equal(t, "builder,may-hold,reader", strings.Join(FriendRoles, ","), assertionMsg98...) {
			return
		}
		assert.Equal(t, "flash,frontier,heavy,pro", strings.Join(Tiers, ","), assertionMsg98...)
	}()
	sprint, _ := Lookup(KindSprint)
	assertionMsg100 := []any{"sprint %+v: one row, one optional ref to a friend, the decide read's two bars, the landed score's bar, layer 2's three, the gate decision's two, the judgment bar, the brief bar and the rules turned off", sprint}
	require.True(t, sprint.Singleton, assertionMsg100...)
	require.Len(t, sprint.Fields, 12, assertionMsg100...)
	require.Equal(t, "coordinator", sprint.Fields[0].Name, assertionMsg100...)
	require.Equal(t, TypeRef, sprint.Fields[0].Type, assertionMsg100...)
	require.Equal(t, KindFriend, sprint.Fields[0].Ref, assertionMsg100...)
	require.False(t, sprint.Fields[0].Required, assertionMsg100...)
}

// The sprint row holds the two bars a flash card's decide read is routed by
// (docs/SPEC-SPRINT.md section 6), the calibration's by default (0.5 and 0.3), so the
// owner sets them in nova-config and no number is in the code: both are probabilities,
// the review bar at most the bounce bar, and both empty turns the read off.
func TestTheSprintRowHoldsTheDecideBarsTogether(t *testing.T) {
	t.Parallel()
	sprint, _ := Lookup(KindSprint)
	bounce, _ := sprint.Field(FieldDecideBounce)
	review, _ := sprint.Field(FieldDecideReview)
	assert.Equal(t, []string{"0.5", "0.3"}, []string{bounce.Default, review.Default})
	assert.Equal(t, TypeDecimal, bounce.Type)
	for _, tc := range []struct {
		bounce, review, says string
	}{
		{"0.5", "0.3", ""},
		{"0.4", "0.4", ""},
		{"", "", ""},
		{"0.3", "0.5", "decide_review 0.5 is above decide_bounce 0.3"},
		{"1.5", "0.3", "decide_bounce 1.5 is not a probability"},
		{"0.5", "", "decide_review \"\" is not a decimal"},
	} {
		err := sprint.Check(Row{Name: KindSprint, Fields: map[string]string{FieldDecideBounce: tc.bounce, FieldDecideReview: tc.review}})
		if tc.says == "" {
			assert.NoError(t, err, "%+v", tc)
		} else {
			assert.ErrorContains(t, err, tc.says, "%+v", tc)
		}
	}
}

// The sprint row holds the landed score's bar (docs/SPEC-SPRINT.md section 7, the landed
// score): a decimal, a probability or empty, and empty by default (no judgment).
func TestTheSprintRowHoldsTheLandedScoreBar(t *testing.T) {
	t.Parallel()
	sprint, _ := Lookup(KindSprint)
	bar, ok := sprint.Field(FieldDecideScoreBar)
	require.True(t, ok)
	assert.Equal(t, "", bar.Default, "report only until a review round labels cards independently")
	assert.Equal(t, TypeDecimal, bar.Type)
	for raw, says := range map[string]string{"0.5": "", "0": "", "1": "", "": "", "1.5": "decide_score_bar \"1.5\" is not a probability", "x": "decide_score_bar \"x\" is not a probability"} {
		err := sprint.Check(Row{Name: KindSprint, Fields: map[string]string{FieldDecideScoreBar: raw}})
		if says == "" {
			assert.NoError(t, err, "%q", raw)
		} else {
			assert.ErrorContains(t, err, says, "%q", raw)
		}
	}
}

// The sprint row holds layer 2's three bars (docs/SPEC-SPRINT.md sections 2 and 5): the
// attempt decision's no-result and nothing-to-do bars, each its own named field, and the
// grade's, all empty by default (nothing routes on a decision until a review round labels
// cards independently); each a probability or empty, every problem named at once.
func TestTheSprintRowHoldsTheAttemptAndGradeBars(t *testing.T) {
	t.Parallel()
	sprint, _ := Lookup(KindSprint)
	noResult, _ := sprint.Field(FieldDecideAttemptNoResult)
	nothing, _ := sprint.Field(FieldDecideAttemptNothingToDo)
	grade, _ := sprint.Field(FieldDecideGrade)
	assert.Equal(t, []string{"", "", ""}, []string{noResult.Default, nothing.Default, grade.Default})
	assert.Equal(t, []Type{TypeDecimal, TypeDecimal, TypeDecimal}, []Type{noResult.Type, nothing.Type, grade.Type})
	for _, tc := range []struct {
		noResult, nothing, grade, says string
	}{
		{"0.7", "", "", ""},
		{"", "", "", ""},
		{"0.7", "0.8", "0.8", ""},
		{"1.2", "", "", "decide_attempt_no_result 1.2 is not a probability"},
		{"", "-1", "", "decide_attempt_nothing_to_do -1 is not a probability"},
		{"x", "", "2", `decide_attempt_no_result "x" is not a decimal; decide_grade 2 is not a probability`},
	} {
		err := sprint.Check(Row{Name: KindSprint, Fields: map[string]string{FieldDecideBounce: "0.5", FieldDecideReview: "0.3",
			FieldDecideAttemptNoResult: tc.noResult, FieldDecideAttemptNothingToDo: tc.nothing, FieldDecideGrade: tc.grade}})
		if tc.says == "" {
			assert.NoError(t, err, "%+v", tc)
		} else {
			assert.ErrorContains(t, err, tc.says, "%+v", tc)
		}
	}
}

// The sprint row holds the bar a card's brief is added at (docs/SPEC-NOVA-DECIDE.md
// section 14): empty by default, which asks the brief decision and reports only, else a
// probability nova-sprint add refuses a card under.
func TestTheSprintRowHoldsTheBriefBar(t *testing.T) {
	t.Parallel()
	sprint, _ := Lookup(KindSprint)
	bar, ok := sprint.Field(FieldDecideBriefBar)
	require.True(t, ok)
	assert.Equal(t, "", bar.Default)
	assert.Equal(t, TypeDecimal, bar.Type)
	for raw, says := range map[string]string{"": "", "0.6": "", "1.5": "decide_brief_bar \"1.5\" is not a probability in [0, 1]"} {
		err := sprint.Check(Row{Name: KindSprint, Fields: map[string]string{FieldDecideBriefBar: raw}})
		if says == "" {
			assert.NoError(t, err, raw)
		} else {
			assert.ErrorContains(t, err, says, raw)
		}
	}
}

// TestDeriveGivesTheSprintCoordinatorTheRole: the rows apply plans carry
// the coordinator role on the friend the sprint row names and on no other;
// the stored rows are untouched.
func TestDeriveGivesTheSprintCoordinatorTheRole(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st := seed(t)
	friend, _ := Lookup(KindFriend)
	rows, _ := st.List(ctx, KindFriend)
	derived, err := friend.Derive(ctx, st, rows)
	require.NoError(t, err)
	byName := func(rs []Row, n string) string {
		for _, r := range rs {
			if r.Name == n {
				return r.Fields["roles"]
			}
		}
		return "?"
	}
	require.Equal(t, "builder,coordinator", byName(derived, "rowan"), "derived rowan roles")
	require.Equal(t, "builder,reader", byName(derived, "stella"), "derived stella roles")
	require.Equal(t, "builder", byName(rows, "rowan"), "Derive changed its input")
	_, _, setupErr5775 := st.Update(ctx, KindSprint, KindSprint, map[string]string{"coordinator": ""}, "rowan")
	require.NoError(t, setupErr5775)
	derived, _ = friend.Derive(ctx, st, rows)
	require.Equal(t, "builder", byName(derived, "rowan"), "no coordinator is named")
}

// TestTheFleetIsOneRowOfEndpointsAndMachineRefs: the two machine names and
// both store endpoints are fleet-wide facts. Both stay unset until
// explicitly declared; Postgres never holds a
// password.
func TestTheFleetIsOneRowOfEndpointsAndMachineRefs(t *testing.T) {
	t.Parallel()

	fleet, _ := Lookup(KindFleet)
	assertionMsg144 := []any{"fleet %+v: one row in config.fleet", fleet}
	require.True(t, fleet.Singleton, assertionMsg144...)
	require.Equal(t, "fleet", fleet.Table, assertionMsg144...)
	scopedGot169 := strings.Join(fleet.FieldNames(), ",")
	require.Equal(t, "store,coordinator,redis_port,pg_dsn,bus,loops_dir", scopedGot169, "fleet fields %s", scopedGot169)
	for _, name := range []string{"store", "coordinator"} {
		f, ok := fleet.Field(name)
		require.True(t, ok)
		assertionMsg158 := []any{"--%s %+v: an optional ref to a machine row", f.Name, f}
		func() {
			if !assert.Equal(t, TypeRef, f.Type, assertionMsg158...) {
				return
			}
			if !assert.Equal(t, KindMachine, f.Ref, assertionMsg158...) {
				return
			}
			assert.False(t, f.Required, assertionMsg158...)
		}()
	}
	row, err := fleet.NewRow(KindFleet, map[string]string{"store": "hulk", "loops_dir": "~/nova-bench/loops"})
	assertionMsg153 := []any{"fleet row %+v %v", row.Fields, err}
	require.NoError(t, err, assertionMsg153...)
	require.Equal(t, "hulk", row.Fields["store"], assertionMsg153...)
	require.Equal(t, "", row.Fields["coordinator"], assertionMsg153...)
	require.Empty(t, row.Fields["redis_port"], assertionMsg153...)
	require.Equal(t, "", row.Fields["pg_dsn"], assertionMsg153...)
	require.Equal(t, "~/nova-bench/loops", row.Fields["loops_dir"], assertionMsg153...)
	{
		_, err := fleet.NewRow(KindFleet, map[string]string{"store": "Hulk"})
		assertionMsg156 := []any{"a ref that is not a name: %v", err}
		require.Error(t, err, assertionMsg156...)
		require.ErrorContains(t, err, "--store: name \"Hulk\": want lower-case", assertionMsg156...)
	}
	{
		got, err := fleet.Changes(map[string]string{"coordinator": ""})
		assertionMsg160 := []any{"clearing a fleet field: %v %v", got, err}
		require.NoError(t, err, assertionMsg160...)
		require.Equal(t, "", got["coordinator"], assertionMsg160...)
	}
	for _, tc := range []struct {
		name string
		raw  map[string]string
		want string
	}{
		{"zero port", map[string]string{"redis_port": "0", "loops_dir": "~/nova-bench/loops"}, "1 through 65535"},
		{"empty port", map[string]string{"redis_port": "", "loops_dir": "~/nova-bench/loops"}, "non-negative integer"},
		{"large port", map[string]string{"redis_port": "65536", "loops_dir": "~/nova-bench/loops"}, "1 through 65535"},
		{"malformed dsn", map[string]string{"pg_dsn": "not a URI", "loops_dir": "~/nova-bench/loops"}, "password-free postgres://"},
		{"password in userinfo", map[string]string{"pg_dsn": "postgres://user:do-not-print@localhost:5432/nova", "loops_dir": "~/nova-bench/loops"}, "carries a password"},
		{"password in query", map[string]string{"pg_dsn": "postgres://user@localhost:5432/nova?password=do-not-print", "loops_dir": "~/nova-bench/loops"}, "carries a password"},
		{"encoded mixed-case password key", map[string]string{"pg_dsn": "postgres://user@localhost:5432/nova?%70aSsWoRd=do-not-print", "loops_dir": "~/nova-bench/loops"}, "carries a password"},
		{"semicolon query", map[string]string{"pg_dsn": "postgres://user@localhost:5432/nova?password=do-not-print;sslmode=disable", "loops_dir": "~/nova-bench/loops"}, "valid URI query"},
		{"invalid query escape", map[string]string{"pg_dsn": "postgres://user@localhost:5432/nova?password=do-not-print%zz", "loops_dir": "~/nova-bench/loops"}, "valid URI query"},
		{"zero Postgres port", map[string]string{"pg_dsn": "postgres://user@localhost:0/nova", "loops_dir": "~/nova-bench/loops"}, "TCP port from 1 through 65535"},
		{"large Postgres port", map[string]string{"pg_dsn": "postgres://user@localhost:65536/nova", "loops_dir": "~/nova-bench/loops"}, "TCP port from 1 through 65535"},
		{"empty Postgres port", map[string]string{"pg_dsn": "postgres://user@localhost:/nova", "loops_dir": "~/nova-bench/loops"}, "TCP port from 1 through 65535"},
		{"nonnumeric Postgres port", map[string]string{"pg_dsn": "postgres://user@localhost:do-not-print/nova", "loops_dir": "~/nova-bench/loops"}, "password-free postgres://"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := fleet.NewRow(KindFleet, tc.raw)
			require.ErrorContains(t, err, tc.want)
			assert.NotContains(t, err.Error(), "do-not-print")
		})
	}
	row, err = fleet.NewRow(KindFleet, map[string]string{"redis_port": "6380", "pg_dsn": "postgres://user@localhost:5432/nova", "loops_dir": "~/nova-bench/loops"})
	require.NoError(t, err)
	assert.Equal(t, "6380", row.Fields["redis_port"])
	assert.Equal(t, "postgres://user@localhost:5432/nova", row.Fields["pg_dsn"])
	_, err = fleet.NewRow(KindFleet, map[string]string{"pg_dsn": "postgres://user@localhost/nova?sslmode=require&application_name=nova%3Bconfig", "loops_dir": "~/nova-bench/loops"})
	assert.NoError(t, err, "an omitted port and a percent-encoded query value are valid")
}

func TestCanonicalValidatesEveryType(t *testing.T) {
	t.Parallel()

	friend, _ := Lookup(KindFriend)
	machine, _ := Lookup(KindMachine)
	fleet, _ := Lookup(KindFleet)
	sprint, _ := Lookup(KindSprint)
	field := func(k *Kind, name string) Field {
		f, ok := k.Field(name)
		require.True(t, ok, "%s has no field %s", k.Name, name)
		return f
	}
	cases := []struct {
		f       Field
		raw     string
		want    string
		refused string // a fragment of the refusal, "" for accepted
	}{
		{field(friend, "slots"), "64", "64", ""},
		{field(friend, "slots"), "007", "7", ""},
		{field(friend, "slots"), "-1", "", "non-negative integer"},
		{field(friend, "slots"), "x", "", "non-negative integer"},
		{field(friend, "slots"), "", "", "non-negative integer"},
		{field(friend, "roles"), "reader,builder", "builder,reader", ""},
		{field(friend, "roles"), "builder builder", "builder", ""},
		{field(friend, "roles"), "", "", ""},
		{field(friend, "roles"), "coordinator", "", "want a comma list of builder, may-hold, reader"},
		{field(friend, "roles"), "king", "", "want a comma list of builder, may-hold, reader"},
		{field(friend, "tiers"), "pro,frontier", "frontier,pro", ""},
		{field(friend, "tiers"), "a=b", "", "holds no ="},
		{field(friend, "tiers"), "ultra", "", "want a comma list of flash, frontier, heavy, pro"},
		{field(sprint, "coordinator"), "stella", "stella", ""},
		{field(sprint, "coordinator"), "", "", ""},
		{field(sprint, "coordinator"), "Stella", "", "lower-case"},
		{field(machine, "user"), " gaffer ", "gaffer", ""},
		{field(machine, "user"), "", "", ""},
		{field(machine, "runners"), "2", "2", ""},
		{field(machine, "runners"), "two", "", "non-negative integer"},
		{field(fleet, "store"), "hulk", "hulk", ""},
		{field(fleet, "store"), "", "", ""},
		{field(fleet, "store"), "Hulk", "", "lower-case"},
	}
	for _, c := range cases {
		got, err := c.f.Canonical(c.raw)
		if c.refused == "" {
			if assert.NoError(t, err, "--%s %q: want %q", c.f.Name, c.raw, c.want) {
				assert.Equal(t, c.want, got, "--%s %q", c.f.Name, c.raw)
			}
		} else if assert.Error(t, err, "--%s %q: accepted as %q, want a refusal saying %q", c.f.Name, c.raw, got, c.refused) {
			assert.Contains(t, err.Error(), c.refused, "--%s %q", c.f.Name, c.raw)
		}
		if c.refused != "" && err != nil {
			assert.True(t, strings.HasPrefix(err.Error(), "--"+c.f.Name), "--%s: refusal %q does not name the flag first", c.f.Name, err)
		}
	}
}

func TestNewRowNamesEveryProblemAtOnce(t *testing.T) {
	t.Parallel()

	friend, _ := Lookup(KindFriend)
	_, err := friend.NewRow("Rowan", map[string]string{"slots": "x", "roles": "king", "colour": "red"})
	require.Error(t, err, "a row with four problems was accepted")
	for _, want := range []string{"lower-case", "--tiers is required", "--slots \"x\"", "--roles \"king\"", "--colour is not a friend field"} {
		assert.ErrorContains(t, err, want, "the refusal does not name %q:\n%s", want, err)
	}
	machine, _ := Lookup(KindMachine)
	_, err = machine.NewRow("hulk", map[string]string{"slots": "40", "ssh": "hulk", "os_arch": "linux/x64"})
	require.Error(t, err, "a machine row with no user, no seat and two invented fields was accepted")
	for _, want := range []string{"--user is required", "--seat is required", "--ssh is not a machine field; the fields are user, seat, slots, runners", "--os_arch is not a machine field"} {
		assert.ErrorContains(t, err, want, "the machine refusal does not name %q:\n%s", want, err)
	}
	row, err := friend.NewRow("rowan", map[string]string{"tiers": "frontier", "slots": "64"})
	require.NoError(t, err)
	for _, f := range friend.Fields {
		_, scopedOk277 := row.Fields[f.Name]
		assert.True(t, scopedOk277, "a new row lacks field %s; every field is present, empty when not given", f.Name)
	}
	assertionMsg257 := []any{"row %+v", row.Fields}
	func() {
		if !assert.Equal(t, "", row.Fields["roles"], assertionMsg257...) {
			return
		}
		if !assert.Equal(t, "64", row.Fields["slots"], assertionMsg257...) {
			return
		}
		assert.Equal(t, 64, row.Int("slots"), assertionMsg257...)
	}()
}

func TestChangesRefusesNoFieldAndUnknownField(t *testing.T) {
	t.Parallel()

	friend, _ := Lookup(KindFriend)
	{
		_, err := friend.Changes(map[string]string{})
		assertionMsg266 := []any{"set with no field: %v", err}
		func() {
			if !assert.Error(t, err, assertionMsg266...) {
				return
			}
			assert.ErrorContains(t, err, "names no field", assertionMsg266...)
		}()
	}
	{
		_, err := friend.Changes(map[string]string{"colour": "red"})
		assertionMsg270 := []any{"set with an unknown field: %v", err}
		func() {
			if !assert.Error(t, err, assertionMsg270...) {
				return
			}
			assert.ErrorContains(t, err, "--colour is not a friend field", assertionMsg270...)
		}()
	}
	got, err := friend.Changes(map[string]string{"roles": "reader,builder", "tiers": ""})
	assertionMsg273 := []any{"changes %v %v", got, err}
	func() {
		if !assert.NoError(t, err, assertionMsg273...) {
			return
		}
		if !assert.Equal(t, "builder,reader", got["roles"], assertionMsg273...) {
			return
		}
		assert.Equal(t, "", got["tiers"], assertionMsg273...)
	}()
}

func TestSortedPutsTheCoordinatorFirst(t *testing.T) {
	t.Parallel()

	friend, _ := Lookup(KindFriend)
	rows := []Row{
		{Name: "stella", Fields: map[string]string{"roles": "builder"}},
		{Name: "rowan", Fields: map[string]string{"roles": "builder,coordinator"}},
		{Name: "emma", Fields: map[string]string{"roles": ""}},
	}
	var got []string
	for _, r := range friend.Sorted(rows) {
		got = append(got, r.Name)
	}
	scopedWant344 := "rowan,emma,stella"
	require.Equal(t, scopedWant344, strings.Join(got, ","), "apply order %v, want %s (coordinator first, then by name)", got, scopedWant344)
	require.Equal(t, "stella", rows[0].Name, "Sorted reordered its input")
}

// A friend's width is the jobs she works at once (the owner, 2026-10-02: "6/1
// seems a bit wrong -- need to setup width for friends? Start at 8 for
// each?"): add stores DefaultFriendWidth when it is not given, FriendWidth
// reads a row without the field as the default, and a width below 1 is
// refused by the kind's Check in one line naming the flag.
func TestAFriendsWidthDefaultsToEightAndIsAtLeastOne(t *testing.T) {
	t.Parallel()

	friend, _ := Lookup(KindFriend)
	row, err := friend.NewRow("amy", map[string]string{"slots": "2", "tiers": "flash"})
	require.NoError(t, err)
	assert.Equal(t, "8", row.Fields["width"], "add stores the default width")
	assert.Equal(t, DefaultFriendWidth, FriendWidth(row))
	assert.Equal(t, 8, FriendWidth(Row{Name: "amy", Fields: map[string]string{"slots": "2"}}), "a row without the field reads as the default")
	assert.Equal(t, 3, FriendWidth(Row{Name: "amy", Fields: map[string]string{"width": "3"}}))

	_, err = friend.NewRow("amy", map[string]string{"slots": "2", "tiers": "flash", "width": "0"})
	require.Error(t, err)
	assert.Equal(t, "friend amy has width 0; a friend's width is the jobs she works at once, at least 1: want --width <n> with n >= 1", err.Error(), "one refusal")
	assert.NoError(t, friend.Check(Row{Name: "amy", Fields: map[string]string{"width": "1"}}))
	assert.Error(t, friend.Check(Row{Name: "amy", Fields: map[string]string{"width": "0"}}))
}

// The sprint row holds the two bars a failed gate's decisions are routed by
// (docs/SPEC-SPRINT.md section 5, the gate verdict), empty by default: the decisions are
// recorded and nothing is routed until the owner sets one. Each is a probability or empty;
// two set ones sum above 1, so no failure meets both; a problem of each pair is named in one
// error.
func TestTheSprintRowHoldsTheGateBarsTogether(t *testing.T) {
	t.Parallel()
	sprint, _ := Lookup(KindSprint)
	flaky, _ := sprint.Field(FieldDecideGateFlaky)
	pre, _ := sprint.Field(FieldDecideGatePreexisting)
	assert.Equal(t, []string{"", ""}, []string{flaky.Default, pre.Default})
	assert.Equal(t, TypeDecimal, flaky.Type)
	for _, tc := range []struct {
		flaky, pre, says string
	}{
		{"0.8", "0.8", ""},
		{"0.6", "0.5", ""},
		{"", "", ""},
		{"0.5", "0.5", "decide_gate_flaky 0.5 and decide_gate_preexisting 0.5 sum to at most 1"},
		{"1.5", "0.8", "decide_gate_flaky 1.5 is not a probability"},
		{"0.8", "", ""},
		{"", "0.8", ""},
		{"x", "", "decide_gate_flaky \"x\" is not a decimal; set each gate bar (--decide_gate_flaky, --decide_gate_preexisting) to a probability, or empty to record the decisions and route none"},
	} {
		err := sprint.Check(Row{Name: KindSprint, Fields: map[string]string{FieldDecideGateFlaky: tc.flaky, FieldDecideGatePreexisting: tc.pre}})
		if tc.says == "" {
			assert.NoError(t, err, "%+v", tc)
		} else {
			assert.ErrorContains(t, err, tc.says, "%+v", tc)
		}
	}
	err := sprint.Check(Row{Name: KindSprint, Fields: map[string]string{FieldDecideBounce: "0.3", FieldDecideReview: "0.5", FieldDecideGateFlaky: "0.4", FieldDecideGatePreexisting: "0.4"}})
	assert.ErrorContains(t, err, "decide_review 0.5 is above decide_bounce 0.3", "both pairs' problems in one error")
	assert.ErrorContains(t, err, "sum to at most 1")
}

// The sprint row holds the bar a judgment decision is applied at (nova-sprint answer
// --decide), empty by default (nothing is applied until the coordinator sets it); a value
// that is no probability is refused, naming the flag.
func TestTheSprintRowHoldsTheJudgmentBar(t *testing.T) {
	t.Parallel()
	sprint, _ := Lookup(KindSprint)
	bar, ok := sprint.Field(FieldDecideJudgment)
	require.True(t, ok)
	assert.Empty(t, bar.Default)
	assert.NoError(t, checkSprint(Row{Name: "sprint", Fields: map[string]string{FieldDecideJudgment: ""}}), "empty is no bar")
	assert.Equal(t, TypeDecimal, bar.Type)
	assert.NoError(t, checkSprint(Row{Name: "sprint", Fields: map[string]string{FieldDecideJudgment: "0.9"}}))
	assert.ErrorContains(t, checkSprint(Row{Name: "sprint", Fields: map[string]string{FieldDecideJudgment: "1.5"}}), "want --decide_judgment_bar <p>, a probability")
}

// A friend row's mode is batch unless it says one-shot: FriendMode reads a row
// written before migration 0030 (no field) as the default.
func TestAFriendRowsModeDefaultsToBatch(t *testing.T) {
	t.Parallel()
	assert.Equal(t, FriendModeBatch, FriendMode(Row{Name: "amy", Fields: map[string]string{}}))
	assert.Equal(t, FriendModeOneShot, FriendMode(Row{Name: "amy", Fields: map[string]string{"mode": "one-shot"}}))
	assert.Equal(t, []string{"batch", "one-shot"}, FriendModes)
}

func TestCheckFleetRefusesASpacePaddedPasswordKey(t *testing.T) {
	t.Parallel()

	fleet, _ := Lookup(KindFleet)

	// Space before "password" key
	row := Row{Name: KindFleet, Fields: map[string]string{"pg_dsn": "postgres://cfgu@db.invalid:5432/nova? password=x"}}
	err := fleet.Check(row)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "carries a password")

	// Space after "password" key
	row = Row{Name: KindFleet, Fields: map[string]string{"pg_dsn": "postgres://cfgu@db.invalid:5432/nova?password =x"}}
	err = fleet.Check(row)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "carries a password")

	// Un-padded password should also be refused
	row = Row{Name: KindFleet, Fields: map[string]string{"pg_dsn": "postgres://cfgu@db.invalid:5432/nova?password=x"}}
	err = fleet.Check(row)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "carries a password")

	// No password should be accepted
	row = Row{Name: KindFleet, Fields: map[string]string{"pg_dsn": "postgres://cfgu@db.invalid:5432/nova"}}
	err = fleet.Check(row)
	require.NoError(t, err)
}

// noLibrary is the RedisApplier with Prepare answered: miniredis has no
// FUNCTION command, and the fleet and loop writes apply makes are plain SET,
// SADD and HSET that need no library.
type noLibrary struct{ *RedisApplier }

func (noLibrary) Prepare(context.Context) error { return nil }

// TestLoopLogIsTheFleetRowsDirectory: a loop's log path is the fleet row's
// loops_dir and its name, so the value migration 0033 seeds reproduces
// today's literal, another directory changes it, and a row that carries no
// directory is refused by the kind's Check with a remedy naming the set that
// declares one. apply writes that path into the loop's hash, on a first
// apply and on a later one of the loop kind alone.
func TestLoopLogIsTheFleetRowsDirectory(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fleet, _ := Lookup(KindFleet)
	machine, _ := Lookup(KindMachine)
	loop, _ := Lookup(KindLoop)

	assert.Equal(t, "~/nova-bench/loops/member-m1.log", LoopLog("~/nova-bench/loops", "member-m1"), "the seeded value reproduces today's string for a name")
	assert.Equal(t, "/var/loops/tick.log", LoopLog("/var/loops", "tick"), "another directory changes it")
	for _, raw := range []map[string]string{{}, {"loops_dir": ""}, {"loops_dir": "   "}} {
		_, err := fleet.NewRow(KindFleet, raw)
		require.Error(t, err, "a row carrying %q", raw["loops_dir"])
		assert.ErrorContains(t, err, "--loops_dir wants a non-empty directory path")
		assert.ErrorContains(t, err, "nova-config fleet set --loops_dir")
	}
	assert.NoError(t, fleet.Check(Row{Name: KindFleet, Fields: map[string]string{"loops_dir": "/valid/path"}}))

	// A fleet row as migration 0033 seeds it, one machine and one loop on
	// it, applied into a store that holds none of them yet. The in-memory
	// store carries the seed the migration writes, and no code default does.
	st := NewMem()
	seeded, _, err := st.Get(ctx, KindFleet, KindFleet)
	require.NoError(t, err)
	assert.Equal(t, "~/nova-bench/loops", seeded.Fields["loops_dir"], "NewMem carries the seed")
	field, _ := fleet.Field("loops_dir")
	assert.Empty(t, field.Default, "loops_dir has no code default")
	all, err := Migrations()
	require.NoError(t, err)
	var seed string
	for _, mg := range all {
		if mg.Version == 33 {
			seed = mg.SQL
		}
	}
	assert.Contains(t, seed, "loops_dir text NOT NULL DEFAULT '~/nova-bench/loops'", "migration 0033 seeds the value NewMem carries")
	m1, err := machine.NewRow("m1", map[string]string{"user": "u", "seat": "s", "slots": "4"})
	require.NoError(t, err)
	_, err = st.Insert(ctx, KindMachine, m1, "t")
	require.NoError(t, err)
	_, _, err = st.Update(ctx, KindFleet, KindFleet, map[string]string{"redis_port": "6380", "pg_dsn": "postgres://nova_config@localhost:5432/nova"}, "t")
	require.NoError(t, err)
	l1, err := loop.NewRow("member-m1", map[string]string{"machine": "m1", "argv": `["/bin/member"]`, "keepalive": "true"})
	require.NoError(t, err)
	_, err = st.Insert(ctx, KindLoop, l1, "t")
	require.NoError(t, err)

	applier, c, _ := coverStore(t)
	a := noLibrary{applier}
	_, err = Apply(ctx, st, a, KindFleet, "t", false, func(Op) {})
	require.NoError(t, err)
	_, err = Apply(ctx, st, a, KindLoop, "t", false, func(Op) {})
	require.NoError(t, err)
	assert.Equal(t, "~/nova-bench/loops/member-m1.log", c.HGet(ctx, LoopKey("member-m1"), "log").Val(), "the first apply writes the seeded path")

	// A later run that applies the loop kind alone takes the directory from
	// the store's fleet row: the rewritten hash keeps the same log.
	_, _, err = st.Update(ctx, KindLoop, "member-m1", map[string]string{"every": "60", "keepalive": "false"}, "t")
	require.NoError(t, err)
	_, err = Apply(ctx, st, noLibrary{&RedisApplier{Client: c}}, KindLoop, "t", false, func(Op) {})
	require.NoError(t, err)
	assert.Equal(t, "60", c.HGet(ctx, LoopKey("member-m1"), "every").Val(), "the loop kind alone wrote the row again")
	assert.Equal(t, "~/nova-bench/loops/member-m1.log", c.HGet(ctx, LoopKey("member-m1"), "log").Val(), "the store's fleet row is the directory")
}

// TestLoopApplyAloneTakesTheStoresDirectory: apply of the loop kind alone,
// into a Redis that holds no applied fleet row, writes the log path the
// store's fleet row decides, and a store whose fleet row carries no directory
// is refused with the set that declares one, writing no hash.
func TestLoopApplyAloneTakesTheStoresDirectory(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	machine, _ := Lookup(KindMachine)
	loop, _ := Lookup(KindLoop)
	store := func(loopsDir string) *Mem {
		st := NewMem()
		m1, err := machine.NewRow("m1", map[string]string{"user": "u", "seat": "s", "slots": "4"})
		require.NoError(t, err)
		_, err = st.Insert(ctx, KindMachine, m1, "t")
		require.NoError(t, err)
		// The fleet row carries the directory given; "" is a row that carries
		// none, which no write through the kind's Check can leave.
		st.rows[KindFleet][KindFleet].Fields["loops_dir"] = loopsDir
		l1, err := loop.NewRow("member-m1", map[string]string{"machine": "m1", "argv": `["/bin/member"]`, "keepalive": "true"})
		require.NoError(t, err)
		_, err = st.Insert(ctx, KindLoop, l1, "t")
		require.NoError(t, err)
		return st
	}

	applier, c, _ := coverStore(t)
	_, err := Apply(ctx, store("~/nova-bench/loops"), noLibrary{applier}, KindLoop, "t", false, func(Op) {})
	require.NoError(t, err)
	assert.Equal(t, "~/nova-bench/loops/member-m1.log", c.HGet(ctx, LoopKey("member-m1"), "log").Val(), "the store's fleet row is the directory when Redis holds no applied fleet row")

	applier, c, _ = coverStore(t)
	_, err = Apply(ctx, store(""), noLibrary{applier}, KindLoop, "t", false, func(Op) {})
	require.ErrorIs(t, err, ErrInvalid)
	assert.ErrorContains(t, err, "run: nova-config fleet set --loops_dir <path>")
	assert.Zero(t, c.Exists(ctx, LoopKey("member-m1")).Val(), "a refused apply writes no hash")
}

// A friend's token cap defaults to 6000000, and 0 is no cap rather than the default.
func TestAFriendsTokenCapDefaultsToSixMillionAndZeroIsNone(t *testing.T) {
	t.Parallel()
	assert.Equal(t, int64(6000000), FriendTokenCap(Row{}))
	assert.Equal(t, int64(6000000), FriendTokenCap(Row{Fields: map[string]string{}}))
	assert.Equal(t, int64(0), FriendTokenCap(Row{Fields: map[string]string{"token_cap": "0"}}))
	assert.Equal(t, int64(100), FriendTokenCap(Row{Fields: map[string]string{"token_cap": "100"}}))
	assert.Equal(t, int64(6000000), FriendTokenCap(Row{Fields: map[string]string{"token_cap": "-1"}}))
	assert.Equal(t, int64(6000000), FriendTokenCap(Row{Fields: map[string]string{"token_cap": "nope"}}))
	friend, ok := Lookup(KindFriend)
	require.True(t, ok)
	row, err := friend.NewRow("amy", map[string]string{"slots": "1", "tiers": "flash"})
	require.NoError(t, err)
	assert.Equal(t, "6000000", row.Fields["token_cap"], "add with no token_cap stores the default")
	row, err = friend.NewRow("amy", map[string]string{"slots": "1", "tiers": "flash", "token_cap": "0"})
	require.NoError(t, err)
	assert.Equal(t, "0", row.Fields["token_cap"], "an explicit 0 is stored, and is no cap")
	_, err = friend.NewRow("amy", map[string]string{"slots": "1", "tiers": "flash", "token_cap": "-1"})
	require.Error(t, err)
}

// A friend row's config_dir is optional, and absolute when set: CLAUDE_CONFIG_DIR
// is read as given, never expanded.
func TestAFriendRowsConfigDirIsAbsoluteWhenSet(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ dir, want string }{
		{"", ""},
		{"/accounts/heavy-a", ""},
		{"~/accounts/heavy-a", "want --config_dir <an absolute path>"},
		{"accounts", "want --config_dir <an absolute path>"},
	} {
		t.Run(tc.dir, func(t *testing.T) {
			t.Parallel()
			err := checkFriend(Row{Name: "amy", Fields: map[string]string{"config_dir": tc.dir}})
			if tc.want == "" {
				assert.NoError(t, err)
			} else {
				assert.ErrorContains(t, err, tc.want)
			}
		})
	}
}

// A friend row's streams restriction is a comma list of globs the matcher reads: a
// pattern it cannot parse is refused with the field named.
func TestFriendStreamRestrictionRejectsMalformedGlob(t *testing.T) {
	t.Parallel()
	friend, ok := Lookup(KindFriend)
	require.True(t, ok)
	err := friend.Check(Row{Name: "friend-a", Fields: map[string]string{"width": "1", "streams": "["}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid stream glob")
}
