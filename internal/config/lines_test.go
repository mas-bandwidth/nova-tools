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
	scopedGot17 := RowLine(friend, row)
	require.Equal(t, want, scopedGot17, "row line\n got %s\nwant %s", scopedGot17, want)
	scopedGot21 := ShowLine(friend, row)
	require.Equal(t, want+" created=2026-09-27T01:00:00Z updated=2026-09-27T02:00:00Z", scopedGot21, "show line %s", scopedGot21)
	// One token per field: a scanner splitting on whitespace sees exactly
	// name plus the kind's fields; a value with spaces is escaped.
	scopedN27 := len(strings.Fields(RowLine(friend, row)))
	require.Equal(t, 1+1+len(friend.Fields), scopedN27, "%d tokens, want %d", scopedN27, 2+len(friend.Fields))
	machine, _ := Lookup(KindMachine)
	spaced := Row{Name: "studio", Fields: map[string]string{"user": "glenn f", "seat": "studio", "slots": "64", "runners": "0", "width": "32"}}
	scopedGot33, scopedWant33 := RowLine(machine, spaced), `MACHINE name=studio user=glenn\x20f seat=studio slots=64 runners=0 width=32`
	require.Equal(t, scopedWant33, scopedGot33, "escaped line\n got %s\nwant %s", scopedGot33, scopedWant33)
}

func TestHistoryLineShowsWhatChanged(t *testing.T) {
	t.Parallel()

	add := Change{ID: 1, Kind: "friend", Name: "rowan", Op: OpAdd, After: map[string]string{"slots": "32", "note": ""}, Actor: "rowan", At: "2026-09-27T01:00:00Z"}
	scopedGot43, scopedWant43 := HistoryLine(add), "HISTORY id=1 kind=friend name=rowan op=add actor=rowan at=2026-09-27T01:00:00Z note=- slots=32"
	assert.Equal(t, scopedWant43, scopedGot43, "add\n got %s\nwant %s", scopedGot43, scopedWant43)
	set := Change{ID: 2, Kind: "friend", Name: "rowan", Op: OpSet, Before: map[string]string{"slots": "32", "note": ""}, After: map[string]string{"slots": "64", "note": "wider now"}, Actor: "stella", At: "2026-09-27T02:00:00Z"}
	scopedGot48, scopedWant48 := HistoryLine(set), `HISTORY id=2 kind=friend name=rowan op=set actor=stella at=2026-09-27T02:00:00Z note=->wider\x20now slots=32>64`
	assert.Equal(t, scopedWant48, scopedGot48, "set\n got %s\nwant %s", scopedGot48, scopedWant48)
	remove := Change{ID: 3, Kind: "friend", Name: "rowan", Op: OpRemove, Before: map[string]string{"slots": "64"}, Actor: "rowan", At: "2026-09-27T03:00:00Z"}
	scopedGot53, scopedWant53 := HistoryLine(remove), "HISTORY id=3 kind=friend name=rowan op=remove actor=rowan at=2026-09-27T03:00:00Z slots=64"
	assert.Equal(t, scopedWant53, scopedGot53, "remove\n got %s\nwant %s", scopedGot53, scopedWant53)
}

func TestOpAndKindLines(t *testing.T) {
	t.Parallel()

	scopedGot62, scopedWant62 := OpLine("APPLY", "friend", Op{Op: OpSet, Name: "rowan", Changed: []string{"slots", "roles"}}), "APPLY SET kind=friend name=rowan changed=slots,roles"
	assert.Equal(t, scopedWant62, scopedGot62, "set\n got %s\nwant %s", scopedGot62, scopedWant62)
	scopedGot66, scopedWant66 := OpLine("CHECK", "machine", Op{Op: OpRemove, Name: "mini"}), "CHECK REMOVE kind=machine name=mini"
	assert.Equal(t, scopedWant66, scopedGot66, "remove\n got %s\nwant %s", scopedGot66, scopedWant66)
	friend, _ := Lookup(KindFriend)
	scopedGot71, scopedWant71 := KindLine(friend), "CONFIG KIND name=friend table=config.friends fields=slots,tiers,roles required=slots,tiers rows=many"
	assert.Equal(t, scopedWant71, scopedGot71, "kind\n got %s\nwant %s", scopedGot71, scopedWant71)
	fleet, _ := Lookup(KindFleet)
	scopedGot76, scopedWant76 := KindLine(fleet), "CONFIG KIND name=fleet table=config.fleet fields=store,coordinator,redis_port,pg_dsn,loops_dir required=- rows=one"
	assert.Equal(t, scopedWant76, scopedGot76, "fleet kind\n got %s\nwant %s", scopedGot76, scopedWant76)
}

// TestLiveLineIsTheBeatOrNone: the measured facts are never stored; a
// machine's line carries what its beat says, `-` for a fact the beat does
// not carry yet, and beat=none for a machine with no beat.
func TestLiveLineIsTheBeatOrNone(t *testing.T) {
	t.Parallel()

	scopedGot88, scopedWant88 := LiveLine(nil), " beat=none"
	assert.Equal(t, scopedWant88, scopedGot88, "no beat %q, want %q", scopedGot88, scopedWant88)
	scopedGot92, scopedWant92 := LiveLine(&Beat{Cores: "64", At: "2026-09-27T03:00:00Z"}), " os=- arch=- cores=64 memory_gb=- beat=2026-09-27T03:00:00Z"
	assert.Equal(t, scopedWant92, scopedGot92, "today's beat %q, want %q", scopedGot92, scopedWant92)
	scopedGot96, scopedWant96 := LiveLine(&Beat{OS: "linux", Arch: "amd64", Cores: "64", MemoryGB: "251", At: "2026-09-27T03:00:00Z"}), " os=linux arch=amd64 cores=64 memory_gb=251 beat=2026-09-27T03:00:00Z"
	assert.Equal(t, scopedWant96, scopedGot96, "a full beat %q, want %q", scopedGot96, scopedWant96)
}

// TestEveryKindHasAMigrationDeclaringItsColumns: a kind is one descriptor,
// one migration and one Redis writer (docs/SPEC-CONFIG.md); the migration
// must declare a column per field, quoted, and the table the descriptor
// names.
func TestEveryKindHasAMigrationDeclaringItsColumns(t *testing.T) {
	t.Parallel()

	all, err := Migrations()
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(all), 1+len(Kinds), "%d migrations for the schema and %d kinds", len(all), len(Kinds))
	for i, m := range all {
		assert.Equal(t, i+1, m.Version, "migration %s is version %d at position %d; the numbers run 1, 2, 3 with no gap", m.Name, m.Version, i+1)
	}
	joined := ""
	for _, m := range all {
		joined += m.SQL + "\n"
	}
	for _, want := range []string{"CREATE SCHEMA IF NOT EXISTS config", "config.schema_migrations", "config.history", "op     text NOT NULL CHECK (op IN ('add', 'set', 'remove'))"} {
		assert.Contains(t, joined, want, "no migration declares %q", want)
	}
	for _, k := range Kinds {
		assert.Contains(t, joined, "CREATE TABLE IF NOT EXISTS config."+k.Table+" (", "no migration creates config.%s for kind %s", k.Table, k.Name)
		if k.Singleton {
			assert.Contains(t, joined, "INSERT INTO config."+k.Table+" (name) VALUES ('"+k.Name+"') ON CONFLICT (name) DO NOTHING", "kind %s is a singleton and no migration creates its row", k.Name)
		}
		for _, f := range k.Fields {
			col := f.Name
			if f.Name == "user" {
				col = `"user"`
			}
			// a column of the CREATE TABLE, or one a later migration adds to it
			assert.True(t, strings.Contains(joined, "\n    "+col+" ") || strings.Contains(joined, "\n    "+col+"\t") ||
				strings.Contains(joined, "\n    ADD COLUMN IF NOT EXISTS "+col+" "), "kind %s: field %s has no column in any migration", k.Name, f.Name)
		}
	}
}
