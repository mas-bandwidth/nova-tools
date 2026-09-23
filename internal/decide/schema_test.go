package decide

import (
	"errors"
	"os"
	"strings"
	"testing"
)

// THE DECISIONS TABLE'S SCHEMA IS OWNED, VERSIONED, AND CHECKED BEFORE A ROW
// IS WRITTEN.
//
// Stella on #1925 at 788dc953: "Do not degrade the machinery receipt into old
// provider-shaped columns or invent confidence. A nontransactional warning or
// receipt is not compatibility. The `source` plus nullable-confidence shape
// needs an owned, versioned migration/schema contract, with fresh-schema and
// upgraded-schema round trips and a clear refusal on old schema without a
// false `recorded` receipt."
//
// Ownership was searched first and is settled: no `decisions` DDL exists
// anywhere in the organisation (an org-wide code search for
// `provider_confidence` returns four hits, all of them in this repository),
// SPEC-DECIDE declares the six-column shape in prose and names no owner, and
// this repository already owns the one versioned migration pattern the org has
// -- internal/record/schema.sql plus its Migrate. So the home is here, and the
// prerequisite is to DECLARE the table rather than to guess at somebody's DDL.
//
// The three round trips below run against a table model that enforces what a
// database enforces: a column that does not exist rejects an INSERT naming it,
// and a NOT NULL column rejects a NULL. That is the property under test -- does
// the machinery receipt survive this schema. The SQL TEXT itself is exercised
// only by the live-Postgres test at the bottom, which states its skip reason.

func TestAFreshSchemaTakesTheMachineryReceiptWholeAndRoundTrips(t *testing.T) {
	db := newFakeSchemaDB() // an empty database: no decisions table at all
	if err := MigrateDecisions(db); err != nil {
		t.Fatalf("migrating a fresh database: %v", err)
	}
	if got, err := db.MaxSchemaVersion(); err != nil || got != DecisionsSchemaVersion {
		t.Fatalf("schema version is %d (%v), want %d", got, err, DecisionsSchemaVersion)
	}
	driver := &postgresDriver{schema: db, rows: db}
	settled := DecisionRow{
		QuestionHash: "h1", Kind: "choice", Answer: "security-designate",
		Floor: 0.65, Source: SourceMachinery, // no provider confidence at all
	}
	if err := driver.Append(settled); err != nil {
		t.Fatalf("a fresh schema refused the settled row: %v", err)
	}
	got := onlyRow(t, driver, "choice")
	if got.Source != SourceMachinery {
		t.Errorf("the row came back with source %q, want %q", got.Source, SourceMachinery)
	}
	if got.HasProviderConfidence {
		t.Errorf("the row came back carrying a provider confidence of %v; nobody answered", got.ProviderConfidence)
	}
}

func TestAnOldSchemaUpgradesAndThenTakesTheSameRow(t *testing.T) {
	db := newFakeSchemaDB()
	db.installOldDecisionsTable() // the hand-made six-column shape: no source, NOT NULL confidence

	// Before the migration the row does not fit, and the writer says so rather
	// than dropping the source or inventing a confidence to make it fit.
	driver := &postgresDriver{schema: db, rows: db}
	err := driver.Append(DecisionRow{QuestionHash: "h0", Kind: "choice", Answer: "x", Source: SourceMachinery})
	if err == nil {
		t.Fatal("an unmigrated schema accepted the row")
	}
	if !errors.Is(err, ErrDecisionsSchemaUnmigrated) {
		t.Errorf("the refusal is not the typed one: %v", err)
	}

	if err := MigrateDecisions(db); err != nil {
		t.Fatalf("upgrading the old shape: %v", err)
	}
	if !db.hasColumn("decisions", "source") {
		t.Error("the migration did not add the source column to the existing table")
	}
	if db.notNull("decisions", "provider_confidence") {
		t.Error("the migration did not drop NOT NULL from provider_confidence")
	}
	if err := driver.Append(DecisionRow{
		QuestionHash: "h2", Kind: "choice", Answer: "design-authority",
		Floor: 0.65, Source: SourceMachinery,
	}); err != nil {
		t.Fatalf("the upgraded schema refused the row: %v", err)
	}
	got := onlyRow(t, driver, "choice")
	if got.Source != SourceMachinery || got.HasProviderConfidence {
		t.Errorf("round trip lost the shape: source=%q hasConfidence=%v", got.Source, got.HasProviderConfidence)
	}

	// And the migration is idempotent, which is what makes it safe to run on
	// every start.
	if err := MigrateDecisions(db); err != nil {
		t.Fatalf("running the migration twice: %v", err)
	}
}

func TestAnUnmigratedSchemaIsATypedRefusalThatNamesThePrerequisite(t *testing.T) {
	for _, tc := range []struct {
		name string
		set  func(*fakeSchemaDB)
	}{
		{"no decisions table at all", func(db *fakeSchemaDB) {}},
		{"the old six-column table, never migrated", func(db *fakeSchemaDB) { db.installOldDecisionsTable() }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := newFakeSchemaDB()
			tc.set(db)
			driver := &postgresDriver{schema: db, rows: db}
			err := driver.Append(DecisionRow{QuestionHash: "h", Kind: "choice", Answer: "a", Source: SourceMachinery})
			if err == nil {
				t.Fatal("an unmigrated schema accepted a row")
			}
			if !errors.Is(err, ErrDecisionsSchemaUnmigrated) {
				t.Fatalf("want ErrDecisionsSchemaUnmigrated, got %v", err)
			}
			// The refusal has to be actionable: it names the verb that fixes it.
			if !strings.Contains(err.Error(), "nova-decide migrate") {
				t.Errorf("the refusal does not name the prerequisite: %v", err)
			}
			if db.inserts != 0 {
				t.Errorf("%d row(s) were written by a refused append", db.inserts)
			}
		})
	}
}

// A MIGRATION THAT FAILS PART WAY LEAVES NOTHING BEHIND (Stella's hold on
// #1990 at bab19122: statements ran one by one through sql.DB.Exec, so a later
// failure left partial DDL). Every statement after the first is broken in turn
// on the old six-column table; each time the table must come back exactly as
// it was: no source column, provider_confidence still NOT NULL, no version.
func TestAMigrationThatFailsPartWayRollsBackEveryStatement(t *testing.T) {
	stmts := splitDecisionsSQL(decisionsSchemaSQL)
	if len(stmts) < 3 {
		t.Fatalf("the migration has %d statements; this control needs a later one to fail", len(stmts))
	}
	for _, stmt := range stmts[1:] {
		prefix := strings.Join(strings.Fields(stmt), " ")
		if len(prefix) > 48 {
			prefix = prefix[:48]
		}
		t.Run(prefix, func(t *testing.T) {
			db := newFakeSchemaDB()
			db.installOldDecisionsTable()
			db.failOn = prefix
			err := MigrateDecisions(db)
			if err == nil {
				t.Fatal("a migration with a failing statement reported success")
			}
			if db.hasColumn("decisions", "source") {
				t.Error("a failed migration left the source column behind")
			}
			if !db.notNull("decisions", "provider_confidence") {
				t.Error("a failed migration left provider_confidence nullable")
			}
			if _, ok := db.tables["decisions_schema_version"]; ok {
				t.Error("a failed migration left the version table behind")
			}
			if v, _ := db.MaxSchemaVersion(); v != 0 {
				t.Errorf("a failed migration left version %d", v)
			}
			// And the gate still refuses it as unmigrated: nothing half-done
			// can pass for migrated.
			driver := &postgresDriver{schema: db, rows: db}
			if err := driver.Append(DecisionRow{QuestionHash: "h", Kind: "choice", Answer: "a", Source: SourceMachinery}); !errors.Is(err, ErrDecisionsSchemaUnmigrated) {
				t.Errorf("after a rolled-back migration, want ErrDecisionsSchemaUnmigrated, got %v", err)
			}
		})
	}
}

// A DATABASE AT A FUTURE VERSION IS REFUSED, NOT ACCEPTED (Stella's hold on
// #1990 at bab19122: version 2 passed a gate that checked only "below 1").
// This writer declares compatibility with exactly DecisionsSchemaVersion, so
// write, read and its own migration all refuse, with a sentinel of their own.
func TestAFutureSchemaVersionIsATypedRefusalOnWriteReadAndMigrate(t *testing.T) {
	db := newFakeSchemaDB()
	db.installVersion(DecisionsSchemaVersion + 1)
	driver := &postgresDriver{schema: db, rows: db}

	err := driver.Append(DecisionRow{QuestionHash: "h", Kind: "choice", Answer: "a", Source: SourceMachinery})
	if !errors.Is(err, ErrDecisionsSchemaTooNew) {
		t.Fatalf("Append on version %d: want ErrDecisionsSchemaTooNew, got %v", DecisionsSchemaVersion+1, err)
	}
	if errors.Is(err, ErrDecisionsSchemaUnmigrated) {
		t.Error("a too-new database was called unmigrated; the two refusals must stay distinct")
	}
	if db.inserts != 0 {
		t.Errorf("%d row(s) were written into a too-new schema", db.inserts)
	}
	if _, err := driver.Rows("choice"); !errors.Is(err, ErrDecisionsSchemaTooNew) {
		t.Errorf("Rows on a too-new schema: want ErrDecisionsSchemaTooNew, got %v", err)
	}
	if err := MigrateDecisions(db); !errors.Is(err, ErrDecisionsSchemaTooNew) {
		t.Errorf("MigrateDecisions on a too-new schema: want ErrDecisionsSchemaTooNew, got %v", err)
	}
	// The exact version is still accepted: the gate is equality, not a floor.
	ok := newFakeSchemaDB()
	if err := MigrateDecisions(ok); err != nil {
		t.Fatal(err)
	}
	if err := EnsureDecisionsSchema(ok); err != nil {
		t.Errorf("the writer's own version was refused: %v", err)
	}
}

// The migration file is the contract, so it says both halves out loud.
func TestTheMigrationCarriesBothTheFreshAndTheUpgradePath(t *testing.T) {
	raw, err := os.ReadFile("schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	for _, want := range []string{
		"CREATE TABLE IF NOT EXISTS decisions_schema_version",
		"CREATE TABLE IF NOT EXISTS decisions",
		"ADD COLUMN IF NOT EXISTS source",
		"DROP NOT NULL",
		"ON CONFLICT (version) DO NOTHING",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("schema.sql does not carry %q; the migration is not idempotent over both paths", want)
		}
	}
}

// The live-Postgres round trip. It is the only thing that exercises the SQL
// TEXT, and it states why it is not running rather than passing silently.
func TestTheSchemaAppliesToARealPostgres(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("NOVA_DECIDE_PG_DSN"))
	if dsn == "" {
		t.Skip("SKIPPED WITH REASON: no Postgres. $NOVA_DECIDE_PG_DSN is unset, hulk reports postgresql inactive and has no psql, and a database is never stood up on the Studio. The schema's SQL text is unexercised until this runs; the table model in this file proves the migration's effect and the version gate, not the dialect.")
	}
	store, err := OpenDecisions(dsn)
	if err != nil {
		t.Fatalf("open %s: %v", DecisionsEnv, err)
	}
	defer store.Close()
	pg, ok := store.(*postgresDriver)
	if !ok {
		t.Fatalf("NOVA_DECIDE_PG_DSN is not a postgres DSN: %T", store)
	}
	if err := MigrateDecisions(pg.schema); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := pg.Append(DecisionRow{
		QuestionHash: "live-" + t.Name(), Kind: "choice", Answer: "security-designate",
		Floor: 0.65, Source: SourceMachinery,
	}); err != nil {
		t.Fatalf("append: %v", err)
	}
	rows, err := pg.Rows("choice")
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if r.QuestionHash != "live-"+t.Name() {
			continue
		}
		if r.Source != SourceMachinery || r.HasProviderConfidence {
			t.Errorf("live round trip lost the shape: %+v", r)
		}
		return
	}
	t.Error("the row written was not read back")
}

func onlyRow(t *testing.T, d *postgresDriver, kind string) DecisionRow {
	t.Helper()
	rows, err := d.Rows(kind)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("want exactly one row, got %d", len(rows))
	}
	return rows[0]
}
