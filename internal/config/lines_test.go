package config

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRowLineNamesEveryFieldAndEscapesValues(t *testing.T) {
	t.Parallel()

	friend, _ := Lookup(KindFriend)
	row := Row{Name: "rowan", Fields: map[string]string{"slots": "64", "roles": "builder,reader"}, CreatedAt: "2026-09-27T01:00:00Z", UpdatedAt: "2026-09-27T02:00:00Z"}
	want := `FRIEND name=rowan slots=64 tiers=- roles=builder,reader`
	{
		got := RowLine(friend, row)
		require.False(t, got != want, "row line\n got %s\nwant %s", got, want)
	}
	{
		got := ShowLine(friend, row)
		require.False(t, got != want+" created=2026-09-27T01:00:00Z updated=2026-09-27T02:00:00Z", "show line %s", got)
	}
	// One token per field: a scanner splitting on whitespace sees exactly
	// name plus the kind's fields; a value with spaces is escaped.
	{
		n := len(strings.Fields(RowLine(friend, row)))
		require.False(t, n != 1+1+len(friend.Fields), "%d tokens, want %d", n, 2+len(friend.Fields))
	}
	machine, _ := Lookup(KindMachine)
	spaced := Row{Name: "studio", Fields: map[string]string{"user": "glenn f", "seat": "studio", "slots": "64", "runners": "0"}}
	{
		got, want := RowLine(machine, spaced), `MACHINE name=studio user=glenn\x20f seat=studio slots=64 runners=0`
		require.False(t, got != want, "escaped line\n got %s\nwant %s", got, want)
	}
}

func TestHistoryLineShowsWhatChanged(t *testing.T) {
	t.Parallel()

	add := Change{ID: 1, Kind: "friend", Name: "rowan", Op: OpAdd, After: map[string]string{"slots": "32", "note": ""}, Actor: "rowan", At: "2026-09-27T01:00:00Z"}
	{
		got, want := HistoryLine(add), "HISTORY id=1 kind=friend name=rowan op=add actor=rowan at=2026-09-27T01:00:00Z note=- slots=32"
		assert.False(t, got != want, "add\n got %s\nwant %s", got, want)
	}
	set := Change{ID: 2, Kind: "friend", Name: "rowan", Op: OpSet, Before: map[string]string{"slots": "32", "note": ""}, After: map[string]string{"slots": "64", "note": "wider now"}, Actor: "stella", At: "2026-09-27T02:00:00Z"}
	{
		got, want := HistoryLine(set), `HISTORY id=2 kind=friend name=rowan op=set actor=stella at=2026-09-27T02:00:00Z note=->wider\x20now slots=32>64`
		assert.False(t, got != want, "set\n got %s\nwant %s", got, want)
	}
	remove := Change{ID: 3, Kind: "friend", Name: "rowan", Op: OpRemove, Before: map[string]string{"slots": "64"}, Actor: "rowan", At: "2026-09-27T03:00:00Z"}
	{
		got, want := HistoryLine(remove), "HISTORY id=3 kind=friend name=rowan op=remove actor=rowan at=2026-09-27T03:00:00Z slots=64"
		assert.False(t, got != want, "remove\n got %s\nwant %s", got, want)
	}
}

func TestOpAndKindLines(t *testing.T) {
	t.Parallel()

	{
		got, want := OpLine("APPLY", "friend", Op{Op: OpSet, Name: "rowan", Changed: []string{"slots", "roles"}}), "APPLY SET kind=friend name=rowan changed=slots,roles"
		assert.False(t, got != want, "set\n got %s\nwant %s", got, want)
	}
	{
		got, want := OpLine("CHECK", "machine", Op{Op: OpRemove, Name: "mini"}), "CHECK REMOVE kind=machine name=mini"
		assert.False(t, got != want, "remove\n got %s\nwant %s", got, want)
	}
	friend, _ := Lookup(KindFriend)
	{
		got, want := KindLine(friend), "CONFIG KIND name=friend table=config.friends fields=slots,tiers,roles required=slots,tiers rows=many"
		assert.False(t, got != want, "kind\n got %s\nwant %s", got, want)
	}
	fleet, _ := Lookup(KindFleet)
	{
		got, want := KindLine(fleet), "CONFIG KIND name=fleet table=config.fleet fields=store,coordinator required=- rows=one"
		assert.False(t, got != want, "fleet kind\n got %s\nwant %s", got, want)
	}
}

// TestLiveLineIsTheBeatOrNone: the measured facts are never stored; a
// machine's line carries what its beat says, `-` for a fact the beat does
// not carry yet, and beat=none for a machine with no beat.
func TestLiveLineIsTheBeatOrNone(t *testing.T) {
	t.Parallel()

	{
		got, want := LiveLine(nil), " beat=none"
		assert.False(t, got != want, "no beat %q, want %q", got, want)
	}
	{
		got, want := LiveLine(&Beat{Cores: "64", At: "2026-09-27T03:00:00Z"}), " os=- arch=- cores=64 memory_gb=- beat=2026-09-27T03:00:00Z"
		assert.False(t, got != want, "today's beat %q, want %q", got, want)
	}
	{
		got, want := LiveLine(&Beat{OS: "linux", Arch: "amd64", Cores: "64", MemoryGB: "251", At: "2026-09-27T03:00:00Z"}), " os=linux arch=amd64 cores=64 memory_gb=251 beat=2026-09-27T03:00:00Z"
		assert.False(t, got != want, "a full beat %q, want %q", got, want)
	}
}

// TestEveryKindHasAMigrationDeclaringItsColumns: a kind is one descriptor,
// one migration and one Redis writer (docs/SPEC-CONFIG.md); the migration
// must declare a column per field, quoted, and the table the descriptor
// names.
func TestEveryKindHasAMigrationDeclaringItsColumns(t *testing.T) {
	t.Parallel()

	all, err := Migrations()
	require.NoError(t, err)
	require.False(t, len(all) < 1+len(Kinds), "%d migrations for the schema and %d kinds", len(all), len(Kinds))
	for i, m := range all {
		assert.False(t, m.Version != i+1, "migration %s is version %d at position %d; the numbers run 1, 2, 3 with no gap", m.Name, m.Version, i+1)
	}
	joined := ""
	for _, m := range all {
		joined += m.SQL + "\n"
	}
	for _, want := range []string{"CREATE SCHEMA IF NOT EXISTS config", "config.schema_migrations", "config.history", "op     text NOT NULL CHECK (op IN ('add', 'set', 'remove'))"} {
		assert.False(t, !strings.Contains(joined, want), "no migration declares %q", want)
	}
	for _, k := range Kinds {
		assert.False(t, !strings.Contains(joined, "CREATE TABLE IF NOT EXISTS config."+k.Table+" ("), "no migration creates config.%s for kind %s", k.Table, k.Name)
		assert.False(t, k.Singleton && !strings.Contains(joined, "INSERT INTO config."+k.Table+" (name) VALUES ('"+k.Name+"') ON CONFLICT (name) DO NOTHING"), "kind %s is a singleton and no migration creates its row", k.Name)
		for _, f := range k.Fields {
			col := f.Name
			if f.Name == "user" {
				col = `"user"`
			}
			assert.False(t, !strings.Contains(joined, "\n    "+col+" ") && !strings.Contains(joined, "\n    "+col+"\t"), "kind %s: field %s has no column in any migration", k.Name, f.Name)
		}
	}
}
