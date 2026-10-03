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
// that a friend would just know, is runtime redis data". Four fields:
// slots, tiers, roles and width (2026-10-02, the jobs she works at once); no
// machine, harness, logins, wake or note; and no coordinator role, which is
// the sprint row's.
func TestTheFriendRowIsWhatSomeoneDecidesForHer(t *testing.T) {
	t.Parallel()

	friend, _ := Lookup(KindFriend)
	scopedGot97 := strings.Join(friend.FieldNames(), ",")
	require.Equal(t, "slots,tiers,roles,width", scopedGot97, "friend fields %s, want slots,tiers,roles,width", scopedGot97)
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
		assert.Equal(t, "flash,frontier,pro", strings.Join(Tiers, ","), assertionMsg98...)
	}()
	sprint, _ := Lookup(KindSprint)
	assertionMsg100 := []any{"sprint %+v: one row, one optional ref to a friend", sprint}
	require.True(t, sprint.Singleton, assertionMsg100...)
	require.Len(t, sprint.Fields, 1, assertionMsg100...)
	require.Equal(t, "coordinator", sprint.Fields[0].Name, assertionMsg100...)
	require.Equal(t, TypeRef, sprint.Fields[0].Type, assertionMsg100...)
	require.Equal(t, KindFriend, sprint.Fields[0].Ref, assertionMsg100...)
	require.False(t, sprint.Fields[0].Required, assertionMsg100...)
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
	require.Equal(t, "store,coordinator,redis_port,pg_dsn,loops_dir", scopedGot169, "fleet fields %s", scopedGot169)
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
	row, err := fleet.NewRow(KindFleet, map[string]string{"store": "hulk", "loops_dir": seededLoopsDir})
	assertionMsg153 := []any{"fleet row %+v %v", row.Fields, err}
	require.NoError(t, err, assertionMsg153...)
	require.Equal(t, "hulk", row.Fields["store"], assertionMsg153...)
	require.Equal(t, "", row.Fields["coordinator"], assertionMsg153...)
	require.Empty(t, row.Fields["redis_port"], assertionMsg153...)
	require.Equal(t, "", row.Fields["pg_dsn"], assertionMsg153...)
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
		{"zero port", map[string]string{"redis_port": "0"}, "1 through 65535"},
		{"empty port", map[string]string{"redis_port": ""}, "non-negative integer"},
		{"large port", map[string]string{"redis_port": "65536"}, "1 through 65535"},
		{"malformed dsn", map[string]string{"pg_dsn": "not a URI"}, "password-free postgres://"},
		{"password in userinfo", map[string]string{"pg_dsn": "postgres://user:do-not-print@localhost:5432/nova"}, "carries a password"},
		{"password in query", map[string]string{"pg_dsn": "postgres://user@localhost:5432/nova?password=do-not-print"}, "carries a password"},
		{"encoded mixed-case password key", map[string]string{"pg_dsn": "postgres://user@localhost:5432/nova?%70aSsWoRd=do-not-print"}, "carries a password"},
		{"semicolon query", map[string]string{"pg_dsn": "postgres://user@localhost:5432/nova?password=do-not-print;sslmode=disable"}, "valid URI query"},
		{"invalid query escape", map[string]string{"pg_dsn": "postgres://user@localhost:5432/nova?password=do-not-print%zz"}, "valid URI query"},
		{"zero Postgres port", map[string]string{"pg_dsn": "postgres://user@localhost:0/nova"}, "TCP port from 1 through 65535"},
		{"large Postgres port", map[string]string{"pg_dsn": "postgres://user@localhost:65536/nova"}, "TCP port from 1 through 65535"},
		{"empty Postgres port", map[string]string{"pg_dsn": "postgres://user@localhost:/nova"}, "TCP port from 1 through 65535"},
		{"nonnumeric Postgres port", map[string]string{"pg_dsn": "postgres://user@localhost:do-not-print/nova"}, "password-free postgres://"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := fleet.NewRow(KindFleet, tc.raw)
			require.ErrorContains(t, err, tc.want)
			assert.NotContains(t, err.Error(), "do-not-print")
		})
	}
	row, err = fleet.NewRow(KindFleet, map[string]string{"redis_port": "6380", "pg_dsn": "postgres://user@localhost:5432/nova", "loops_dir": seededLoopsDir})
	require.NoError(t, err)
	assert.Equal(t, "6380", row.Fields["redis_port"])
	assert.Equal(t, "postgres://user@localhost:5432/nova", row.Fields["pg_dsn"])
	_, err = fleet.NewRow(KindFleet, map[string]string{"pg_dsn": "postgres://user@localhost/nova?sslmode=require&application_name=nova%3Bconfig", "loops_dir": seededLoopsDir})
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
		{field(friend, "tiers"), "ultra", "", "want a comma list of flash, frontier, pro"},
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

// TestLoopLogIsTheFleetRowsDirectory pins the log path to the fleet row's
// loops_dir: the migration's seed reproduces the path apply wrote before the
// field, another directory changes it, and an empty directory is refused.
func TestLoopLogIsTheFleetRowsDirectory(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "~/nova-bench/loops/l1.log", LoopLog(seededLoopsDir, "l1"))
	assert.Equal(t, "/var/log/loops/l1.log", LoopLog("/var/log/loops", "l1"))
	fleet, ok := Lookup(KindFleet)
	require.True(t, ok)
	err := fleet.Check(Row{Name: KindFleet, Fields: map[string]string{"loops_dir": ""}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "fleet set --loops-dir")
}
