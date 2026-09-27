package config

import (
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

func TestMachinesApplyBeforeFriends(t *testing.T) {
	t.Parallel()

	names := KindNames()
	if len(names) < 2 || names[0] != KindMachine || names[1] != KindFriend {
		t.Fatalf("kinds %v: a friend's slots are guarded by its machine's ceiling, so machine applies first", names)
	}
}

func TestCanonicalValidatesEveryType(t *testing.T) {
	t.Parallel()

	friend, _ := Lookup(KindFriend)
	machine, _ := Lookup(KindMachine)
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
		{field(friend, "roles"), "coordinator,builder", "builder,coordinator", ""},
		{field(friend, "roles"), "builder builder", "builder", ""},
		{field(friend, "roles"), "", "", ""},
		{field(friend, "roles"), "king", "", "want a comma list of builder, coordinator, may-hold, reader"},
		{field(friend, "logins"), "Rowan-claude,stella", "Rowan-claude,stella", ""},
		{field(friend, "logins"), "a b", "a,b", ""},
		{field(friend, "logins"), "a=b", "", "holds no ="},
		{field(friend, "logins"), "-x", "", "letters, digits and dashes"},
		{field(friend, "wake"), "unit:rowan@studio", "unit:rowan@studio", ""},
		{field(friend, "wake"), "human:#rowan", "human:#rowan", ""},
		{field(friend, "wake"), "", "", ""},
		{field(friend, "wake"), "unit:rowan", "", "want unit:<label>@<host>"},
		{field(friend, "wake"), "human:", "", "want human:<channel>"},
		{field(friend, "wake"), "cron", "", "unit:<label>@<host>, human:<channel> or empty"},
		{field(friend, "machine"), "studio", "studio", ""},
		{field(friend, "machine"), "Studio", "", "lower-case"},
		{field(friend, "machine"), "", "", "want the name of a machine row"},
		{field(friend, "note"), " two words ", "two words", ""},
		{field(friend, "note"), "two\nlines", "", "want one line"},
		{field(machine, "os_arch"), "linux/x64", "linux/x64", ""},
		{field(machine, "os_arch"), "linux", "", "want one of darwin/amd64, darwin/arm64, linux/arm64, linux/x64"},
		{field(machine, "roles"), "runner,bench", "bench,runner", ""},
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
	for _, want := range []string{"lower-case", "--machine is required", "--slots \"x\"", "--roles \"king\"", "--colour is not a friend field"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q:\n%s", want, err)
		}
	}
	row, err := friend.NewRow("rowan", map[string]string{"machine": "studio", "slots": "64"})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range friend.Fields {
		if _, ok := row.Fields[f.Name]; !ok {
			t.Errorf("a new row lacks field %s; every field is present, empty when not given", f.Name)
		}
	}
	if row.Fields["note"] != "" || row.Fields["slots"] != "64" || row.Int("slots") != 64 {
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
	got, err := friend.Changes(map[string]string{"roles": "reader,builder", "note": ""})
	if err != nil || got["roles"] != "builder,reader" || got["note"] != "" {
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
