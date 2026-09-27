package config

import (
	"strings"
	"testing"
)

func TestRowLineNamesEveryFieldAndEscapesValues(t *testing.T) {
	t.Parallel()

	friend, _ := Lookup(KindFriend)
	row := Row{Name: "rowan", Fields: map[string]string{"machine": "studio", "slots": "64", "roles": "builder,coordinator", "note": "two words = one", "wake": "unit:rowan@studio"}, CreatedAt: "2026-09-27T01:00:00Z", UpdatedAt: "2026-09-27T02:00:00Z"}
	want := `FRIEND name=rowan machine=studio slots=64 harness=- wake=unit:rowan@studio roles=builder,coordinator logins=- note=two\x20words\x20\x3d\x20one`
	if got := RowLine(friend, row); got != want {
		t.Fatalf("row line\n got %s\nwant %s", got, want)
	}
	if got := ShowLine(friend, row); got != want+" created=2026-09-27T01:00:00Z updated=2026-09-27T02:00:00Z" {
		t.Fatalf("show line %s", got)
	}
	// One token per field: a scanner splitting on whitespace sees exactly
	// name plus the kind's fields.
	if n := len(strings.Fields(RowLine(friend, row))); n != 1+1+len(friend.Fields) {
		t.Fatalf("%d tokens, want %d", n, 2+len(friend.Fields))
	}
}

func TestHistoryLineShowsWhatChanged(t *testing.T) {
	t.Parallel()

	add := Change{ID: 1, Kind: "friend", Name: "rowan", Op: OpAdd, After: map[string]string{"slots": "32", "note": ""}, Actor: "rowan", At: "2026-09-27T01:00:00Z"}
	if got, want := HistoryLine(add), "HISTORY id=1 kind=friend name=rowan op=add actor=rowan at=2026-09-27T01:00:00Z note=- slots=32"; got != want {
		t.Errorf("add\n got %s\nwant %s", got, want)
	}
	set := Change{ID: 2, Kind: "friend", Name: "rowan", Op: OpSet, Before: map[string]string{"slots": "32", "note": ""}, After: map[string]string{"slots": "64", "note": "wider now"}, Actor: "stella", At: "2026-09-27T02:00:00Z"}
	if got, want := HistoryLine(set), `HISTORY id=2 kind=friend name=rowan op=set actor=stella at=2026-09-27T02:00:00Z note=->wider\x20now slots=32>64`; got != want {
		t.Errorf("set\n got %s\nwant %s", got, want)
	}
	remove := Change{ID: 3, Kind: "friend", Name: "rowan", Op: OpRemove, Before: map[string]string{"slots": "64"}, Actor: "rowan", At: "2026-09-27T03:00:00Z"}
	if got, want := HistoryLine(remove), "HISTORY id=3 kind=friend name=rowan op=remove actor=rowan at=2026-09-27T03:00:00Z slots=64"; got != want {
		t.Errorf("remove\n got %s\nwant %s", got, want)
	}
}

func TestOpAndKindLines(t *testing.T) {
	t.Parallel()

	if got, want := OpLine("APPLY", "friend", Op{Op: OpSet, Name: "rowan", Changed: []string{"slots", "roles"}}), "APPLY SET kind=friend name=rowan changed=slots,roles"; got != want {
		t.Errorf("set\n got %s\nwant %s", got, want)
	}
	if got, want := OpLine("CHECK", "machine", Op{Op: OpRemove, Name: "mini"}), "CHECK REMOVE kind=machine name=mini"; got != want {
		t.Errorf("remove\n got %s\nwant %s", got, want)
	}
	friend, _ := Lookup(KindFriend)
	if got, want := KindLine(friend), "CONFIG KIND name=friend table=config.friends fields=machine,slots,harness,wake,roles,logins,note required=machine,slots"; got != want {
		t.Errorf("kind\n got %s\nwant %s", got, want)
	}
}

// TestEveryKindHasAMigrationDeclaringItsColumns: a kind is one descriptor,
// one migration and one Redis writer (docs/SPEC-CONFIG.md); the migration
// must declare a column per field, quoted, and the table the descriptor
// names.
func TestEveryKindHasAMigrationDeclaringItsColumns(t *testing.T) {
	t.Parallel()

	all, err := Migrations()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) < 1+len(Kinds) {
		t.Fatalf("%d migrations for the schema and %d kinds", len(all), len(Kinds))
	}
	for i, m := range all {
		if m.Version != i+1 {
			t.Errorf("migration %s is version %d at position %d; the numbers run 1, 2, 3 with no gap", m.Name, m.Version, i+1)
		}
	}
	joined := ""
	for _, m := range all {
		joined += m.SQL + "\n"
	}
	for _, want := range []string{"CREATE SCHEMA IF NOT EXISTS config", "config.schema_migrations", "config.history", "op     text NOT NULL CHECK (op IN ('add', 'set', 'remove'))"} {
		if !strings.Contains(joined, want) {
			t.Errorf("no migration declares %q", want)
		}
	}
	for _, k := range Kinds {
		if !strings.Contains(joined, "CREATE TABLE IF NOT EXISTS config."+k.Table+" (") {
			t.Errorf("no migration creates config.%s for kind %s", k.Table, k.Name)
		}
		for _, f := range k.Fields {
			col := f.Name
			if f.Name == "user" {
				col = `"user"`
			}
			if !strings.Contains(joined, "\n    "+col+" ") && !strings.Contains(joined, "\n    "+col+"\t") {
				t.Errorf("kind %s: field %s has no column in any migration", k.Name, f.Name)
			}
		}
	}
}
