package config

import (
	"context"
	"strings"
	"testing"
)

func TestEveryKindHasATableAndUniqueFields(t *testing.T) {
	t.Parallel()

	seen := map[string]bool{}
	for _, k := range Kinds {
		if k.Name == "" || k.Table == "" || k.Doc == "" {
			t.Errorf("kind %+v: name, table and doc are required", k)
		}
		if seen[k.Name] {
			t.Errorf("kind %s is declared twice", k.Name)
		}
		seen[k.Name] = true
		fields := map[string]bool{}
		for _, f := range k.Fields {
			if f.Name == "name" || fields[f.Name] {
				t.Errorf("kind %s: field %q is the row key or repeated", k.Name, f.Name)
			}
			fields[f.Name] = true
			if f.Help == "" {
				t.Errorf("kind %s: field %s has no help line", k.Name, f.Name)
			}
			if (f.Type == TypeEnum || f.Type == TypeList) && len(f.Enum) == 0 {
				t.Errorf("kind %s: field %s is an enum with no words", k.Name, f.Name)
			}
			if f.Type == TypeRef {
				if _, ok := Lookup(f.Ref); !ok {
					t.Errorf("kind %s: field %s refers to unknown kind %q", k.Name, f.Name, f.Ref)
				}
			}
		}
	}
	if _, ok := Lookup("nothing"); ok {
		t.Error("Lookup found a kind that is not declared")
	}
}

func TestKindsApplyInDependencyOrder(t *testing.T) {
	t.Parallel()

	names := KindNames()
	if strings.Join(names, ",") != "machine,fleet,friend,sprint" {
		t.Fatalf("kinds %v: machines first (ceilings), the fleet next (a friend's slots are charged to its coordinator machine when her beat names none), friends, the sprint row last (it names a friend)", names)
	}
}

// TestTheMachineRowIsTheDeclaredFactsSomethingReads: Glenn 2026-09-27, "I
// only want the fleet to have actual defined useful things associated with
// each machine, not invented rando stuff". Four declared fields, no address
// (the name is the tailnet host), no measured fact, no note.
func TestTheMachineRowIsTheDeclaredFactsSomethingReads(t *testing.T) {
	t.Parallel()

	machine, _ := Lookup(KindMachine)
	if got := strings.Join(machine.FieldNames(), ","); got != "user,seat,slots,runners" {
		t.Fatalf("machine fields %s, want user,seat,slots,runners", got)
	}
	for _, f := range machine.Fields {
		if want := f.Name != "runners"; f.Required != want {
			t.Errorf("--%s required=%v, want %v (runners defaults to 0; the rest are typed on add)", f.Name, f.Required, want)
		}
	}
	for _, invented := range []string{"ssh", "address", "os_arch", "os", "arch", "cores", "memory_gb", "roles", "note", "store", "coordinator", "machine", "harness", "logins", "wake"} {
		if _, ok := machine.Field(invented); ok {
			t.Errorf("machine has a field %s: an address is the name, a measured fact comes live from the beat, a fleet fact is the fleet's, a note is history", invented)
		}
	}
	if machine.Singleton {
		t.Error("machine is many rows")
	}
}

// TestTheFriendRowIsWhatSomeoneDecidesForHer: Glenn 2026-09-27, "anything
// that a friend would just know, is runtime redis data". Three fields:
// slots, tiers, roles; no machine, harness, logins, wake or note; and no
// coordinator role, which is the sprint row's.
func TestTheFriendRowIsWhatSomeoneDecidesForHer(t *testing.T) {
	t.Parallel()

	friend, _ := Lookup(KindFriend)
	if got := strings.Join(friend.FieldNames(), ","); got != "slots,tiers,roles" {
		t.Fatalf("friend fields %s, want slots,tiers,roles", got)
	}
	for _, f := range friend.Fields {
		if want := f.Name != "roles"; f.Required != want {
			t.Errorf("--%s required=%v, want %v", f.Name, f.Required, want)
		}
	}
	for _, invented := range []string{"machine", "harness", "logins", "wake", "note", "coordinator"} {
		if _, ok := friend.Field(invented); ok {
			t.Errorf("friend has a field %s: what she would just know is runtime data, who coordinates is the sprint's", invented)
		}
	}
	if strings.Join(FriendRoles, ",") != "builder,may-hold,reader" || strings.Join(Tiers, ",") != "flash,frontier,pro" {
		t.Errorf("roles %v tiers %v", FriendRoles, Tiers)
	}
	sprint, _ := Lookup(KindSprint)
	if !sprint.Singleton || len(sprint.Fields) != 1 || sprint.Fields[0].Name != "coordinator" || sprint.Fields[0].Type != TypeRef || sprint.Fields[0].Ref != KindFriend || sprint.Fields[0].Required {
		t.Fatalf("sprint %+v: one row, one optional ref to a friend", sprint)
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
	if err != nil {
		t.Fatal(err)
	}
	byName := func(rs []Row, n string) string {
		for _, r := range rs {
			if r.Name == n {
				return r.Fields["roles"]
			}
		}
		return "?"
	}
	if byName(derived, "rowan") != "builder,coordinator" || byName(derived, "stella") != "builder,reader" {
		t.Fatalf("derived roles rowan=%s stella=%s", byName(derived, "rowan"), byName(derived, "stella"))
	}
	if byName(rows, "rowan") != "builder" {
		t.Fatal("Derive changed its input")
	}
	if _, _, err := st.Update(ctx, KindSprint, KindSprint, map[string]string{"coordinator": ""}, "rowan"); err != nil {
		t.Fatal(err)
	}
	derived, _ = friend.Derive(ctx, st, rows)
	if byName(derived, "rowan") != "builder" {
		t.Fatalf("no coordinator named and rowan still derives %s", byName(derived, "rowan"))
	}
}

// TestTheFleetIsOneRowOfTwoMachineRefs: Glenn 2026-09-27, "in the fleet
// there is only one coordinator at a time": the store and the coordinator
// are fleet facts, one value each, each a machine row or empty.
func TestTheFleetIsOneRowOfTwoMachineRefs(t *testing.T) {
	t.Parallel()

	fleet, _ := Lookup(KindFleet)
	if !fleet.Singleton || fleet.Table != "fleet" {
		t.Fatalf("fleet %+v: one row in config.fleet", fleet)
	}
	if got := strings.Join(fleet.FieldNames(), ","); got != "store,coordinator" {
		t.Fatalf("fleet fields %s, want store,coordinator", got)
	}
	for _, f := range fleet.Fields {
		if f.Type != TypeRef || f.Ref != KindMachine || f.Required {
			t.Errorf("--%s %+v: an optional ref to a machine row", f.Name, f)
		}
	}
	row, err := fleet.NewRow(KindFleet, map[string]string{"store": "hulk"})
	if err != nil || row.Fields["store"] != "hulk" || row.Fields["coordinator"] != "" {
		t.Fatalf("fleet row %+v %v", row.Fields, err)
	}
	if _, err := fleet.NewRow(KindFleet, map[string]string{"store": "Hulk"}); err == nil || !strings.Contains(err.Error(), "--store: name \"Hulk\": want lower-case") {
		t.Fatalf("a ref that is not a name: %v", err)
	}
	if got, err := fleet.Changes(map[string]string{"coordinator": ""}); err != nil || got["coordinator"] != "" {
		t.Fatalf("clearing a fleet field: %v %v", got, err)
	}
}

func TestCanonicalValidatesEveryType(t *testing.T) {
	t.Parallel()

	friend, _ := Lookup(KindFriend)
	machine, _ := Lookup(KindMachine)
	fleet, _ := Lookup(KindFleet)
	sprint, _ := Lookup(KindSprint)
	field := func(k *Kind, name string) Field {
		f, ok := k.Field(name)
		if !ok {
			t.Fatalf("%s has no field %s", k.Name, name)
		}
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
		switch {
		case c.refused == "" && err != nil:
			t.Errorf("--%s %q: refused %v, want %q", c.f.Name, c.raw, err, c.want)
		case c.refused == "" && got != c.want:
			t.Errorf("--%s %q: canonical %q, want %q", c.f.Name, c.raw, got, c.want)
		case c.refused != "" && err == nil:
			t.Errorf("--%s %q: accepted as %q, want a refusal saying %q", c.f.Name, c.raw, got, c.refused)
		case c.refused != "" && !strings.Contains(err.Error(), c.refused):
			t.Errorf("--%s %q: refusal %q does not say %q", c.f.Name, c.raw, err, c.refused)
		}
		if c.refused != "" && err != nil && !strings.HasPrefix(err.Error(), "--"+c.f.Name) {
			t.Errorf("--%s: refusal %q does not name the flag first", c.f.Name, err)
		}
	}
}

func TestNewRowNamesEveryProblemAtOnce(t *testing.T) {
	t.Parallel()

	friend, _ := Lookup(KindFriend)
	_, err := friend.NewRow("Rowan", map[string]string{"slots": "x", "roles": "king", "colour": "red"})
	if err == nil {
		t.Fatal("a row with four problems was accepted")
	}
	for _, want := range []string{"lower-case", "--tiers is required", "--slots \"x\"", "--roles \"king\"", "--colour is not a friend field"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q:\n%s", want, err)
		}
	}
	machine, _ := Lookup(KindMachine)
	_, err = machine.NewRow("hulk", map[string]string{"slots": "40", "ssh": "hulk", "os_arch": "linux/x64"})
	if err == nil {
		t.Fatal("a machine row with no user, no seat and two invented fields was accepted")
	}
	for _, want := range []string{"--user is required", "--seat is required", "--ssh is not a machine field; the fields are user, seat, slots, runners", "--os_arch is not a machine field"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the machine refusal does not name %q:\n%s", want, err)
		}
	}
	row, err := friend.NewRow("rowan", map[string]string{"tiers": "frontier", "slots": "64"})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range friend.Fields {
		if _, ok := row.Fields[f.Name]; !ok {
			t.Errorf("a new row lacks field %s; every field is present, empty when not given", f.Name)
		}
	}
	if row.Fields["roles"] != "" || row.Fields["slots"] != "64" || row.Int("slots") != 64 {
		t.Errorf("row %+v", row.Fields)
	}
}

func TestChangesRefusesNoFieldAndUnknownField(t *testing.T) {
	t.Parallel()

	friend, _ := Lookup(KindFriend)
	if _, err := friend.Changes(map[string]string{}); err == nil || !strings.Contains(err.Error(), "names no field") {
		t.Errorf("set with no field: %v", err)
	}
	if _, err := friend.Changes(map[string]string{"colour": "red"}); err == nil || !strings.Contains(err.Error(), "--colour is not a friend field") {
		t.Errorf("set with an unknown field: %v", err)
	}
	got, err := friend.Changes(map[string]string{"roles": "reader,builder", "tiers": ""})
	if err != nil || got["roles"] != "builder,reader" || got["tiers"] != "" {
		t.Errorf("changes %v %v", got, err)
	}
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
	if want := "rowan,emma,stella"; strings.Join(got, ",") != want {
		t.Fatalf("apply order %v, want %s (coordinator first, then by name)", got, want)
	}
	if rows[0].Name != "stella" {
		t.Fatal("Sorted reordered its input")
	}
}
