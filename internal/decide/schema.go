// The decisions table's owned, versioned schema contract (Stella, 2026-09-19,
// on #1925 at 788dc953: "Do not degrade the machinery receipt into old
// provider-shaped columns or invent confidence. A nontransactional warning or
// receipt is not compatibility.").
//
// The machinery receipt needs two things the six-column shape SPEC-DECIDE
// describes in prose cannot hold: a `source` saying which of the two decided,
// and a provider_confidence that can be NULL because no provider answered. A
// writer that quietly dropped the source, or wrote a zero where there was no
// measurement, would be manufacturing calibration evidence about a call nobody
// made -- so instead the writer REFUSES a database that cannot hold the row,
// by name, and says which verb fixes it.
//
// Ownership was searched before this was written and is in
// jev-tune/SCHEMA-OWNER.md: no `decisions` DDL existed anywhere in the
// organisation, this repository holds the only writer, the only reader, the
// declaring spec and the org's one versioned migration pattern. So the schema
// is declared here rather than guessed at somewhere else.
package decide

import (
	_ "embed"
	"errors"
	"fmt"
	"strings"
)

// DecisionsSchemaVersion is the schema version this writer requires. A
// database whose decisions_schema_version says less than this -- including a
// database that has no such table at all -- is unmigrated.
const DecisionsSchemaVersion = 1

// MigrateVerb is the exact prerequisite a refusal names.
const MigrateVerb = "nova-decide migrate --dsn <dsn>"

//go:embed schema.sql
var decisionsSchemaSQL string

// ErrDecisionsSchemaUnmigrated is the typed refusal a database earns by not
// carrying this migration. It is a sentinel so a caller names it in one word
// and prints no receipt: an append that did not happen is not a row.
var ErrDecisionsSchemaUnmigrated = errors.New("decide: the decisions table has not been migrated to this writer's schema")

// ErrDecisionsSchemaTooNew is the typed refusal for the other side of the
// version gate: a database a NEWER migration has moved past this writer's
// version. This writer does not know what that version changed -- a renamed
// column, a new NOT NULL, a different meaning for source -- so it declares no
// compatibility with it: it neither writes a row nor reads one, and it does not
// run its own older migration over it. The fix is a nova-decide built at or
// above the database's version, never a downgrade.
var ErrDecisionsSchemaTooNew = errors.New("decide: the decisions table is at a newer schema version than this writer knows")

// decisionsSchema is the schema half of a decisions store: applying the
// migration, and reading back the version that is installed. It is a seam so
// the migration's EFFECT and the version gate are testable without standing up
// a database; the SQL text itself is exercised by the live-Postgres round trip,
// which states its skip reason.
type decisionsSchema interface {
	// ExecInOneTransaction runs every statement in ONE transaction: all of
	// them take effect or none does. A failure part way rolls back what the
	// earlier statements did, so a migration never leaves partial DDL behind
	// (Postgres DDL is transactional).
	ExecInOneTransaction(stmts []string) error
	// MaxSchemaVersion is 0 where no version table exists, which is what an
	// unmigrated database looks like whatever else it carries.
	MaxSchemaVersion() (int, error)
}

// decisionsRows is the row half: one writer, one read.
type decisionsRows interface {
	Insert(row DecisionRow) error
	Select(kind string) ([]DecisionRow, error)
}

// Migrator is a decisions store that has a schema to install. The TSV
// fallback is deliberately not one: it writes its own header and a
// six-column file written before `source` existed is still read, so the file
// path stays independently valid and needs nothing from this contract.
type Migrator interface{ Migrate() error }

// Migrate installs this package's schema into the Postgres decisions table.
func (p *postgresDriver) Migrate() error { return MigrateDecisions(p.schema) }

// MigrateDecisions applies schema.sql in ONE transaction: a statement that
// fails rolls back every statement before it, so the database is left exactly
// as it was found and never half-migrated (Stella's hold on #1990 at bab19122).
// It is idempotent in both directions -- a fresh database gets the whole table,
// a database carrying the hand-made six-column shape is upgraded in place -- so
// it is safe to run on every start, and running it twice is a no-op.
//
// A database already at a NEWER version is refused, not migrated: this
// writer's older statements declare nothing about a shape it does not know.
func MigrateDecisions(db decisionsSchema) error {
	have, err := db.MaxSchemaVersion()
	if err != nil {
		return fmt.Errorf("decide: reading the decisions schema version: %w", err)
	}
	if have > DecisionsSchemaVersion {
		return tooNew(have)
	}
	if err := db.ExecInOneTransaction(splitDecisionsSQL(decisionsSchemaSQL)); err != nil {
		return fmt.Errorf("decide: migrate decisions (rolled back, nothing applied): %w", err)
	}
	return nil
}

func tooNew(have int) error {
	return fmt.Errorf("%w: it is at version %d and this writer knows only up to %d, so it declares no compatibility with that shape and neither writes nor reads a row; run a nova-decide built for schema version %d or later",
		ErrDecisionsSchemaTooNew, have, DecisionsSchemaVersion, have)
}

// EnsureDecisionsSchema is the gate in front of every write and every read. The
// contract is EXACT: below the required version it refuses as unmigrated,
// naming the version it found, the version it needs and the verb that installs
// it; ABOVE it, it refuses as too new, because a future version's shape is
// undeclared here. Either way the caller writes nothing and claims no receipt.
func EnsureDecisionsSchema(db decisionsSchema) error {
	have, err := db.MaxSchemaVersion()
	if err != nil {
		return fmt.Errorf("decide: reading the decisions schema version: %w", err)
	}
	if have < DecisionsSchemaVersion {
		return fmt.Errorf("%w: it is at version %d and this writer needs %d, so the row's source and its absent provider confidence have nowhere to go; run: %s",
			ErrDecisionsSchemaUnmigrated, have, DecisionsSchemaVersion, MigrateVerb)
	}
	if have > DecisionsSchemaVersion {
		return tooNew(have)
	}
	return nil
}

// splitDecisionsSQL splits the migration into statements.
//
// Comment lines are dropped FIRST and the split comes after, which is not a
// detail: this file's comments are prose and prose contains semicolons, so
// splitting first cut a sentence in half and handed the tail to the database
// as a statement. The file is ours and carries no semicolon inside a literal,
// so with the comments gone this is the whole of the parsing it needs.
func splitDecisionsSQL(body string) []string {
	code := []string{}
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "--") {
			continue
		}
		code = append(code, line)
	}
	out := []string{}
	for _, chunk := range strings.Split(strings.Join(code, "\n"), ";") {
		if stmt := strings.TrimSpace(chunk); stmt != "" {
			out = append(out, stmt)
		}
	}
	return out
}
