package decide

import (
	"fmt"
	"strconv"
	"strings"
)

// fakeSchemaDB is a table model that enforces the two things a database
// enforces and this contract turns on: a column that does not exist rejects an
// INSERT naming it, and a NOT NULL column rejects a NULL.
//
// It understands exactly the statements schema.sql contains, matched by
// prefix, which is legitimate because those statements are ours and fixed. It
// is NOT a SQL engine and is not pretending to be one: the dialect is the live
// Postgres test's job, and that test says out loud when it is skipped.
type fakeSchemaDB struct {
	tables   map[string]*fakeTable
	versions []int
	inserts  int
}

type fakeTable struct {
	// columns maps a column name to whether it is NOT NULL.
	columns map[string]bool
	rows    []DecisionRow
}

func newFakeSchemaDB() *fakeSchemaDB {
	return &fakeSchemaDB{tables: map[string]*fakeTable{}}
}

// installOldDecisionsTable is the hand-made six-column shape SPEC-DECIDE
// describes in prose: no source column, and a provider_confidence that cannot
// be null. It is what a database someone set up before this migration existed
// looks like.
func (f *fakeSchemaDB) installOldDecisionsTable() {
	f.tables["decisions"] = &fakeTable{columns: map[string]bool{
		"question_hash":       true,
		"kind":                true,
		"answer":              true,
		"provider_confidence": true, // NOT NULL: the old shape's whole problem
		"floor":               true,
		"outcome":             false,
	}}
}

func (f *fakeSchemaDB) hasColumn(table, column string) bool {
	t, ok := f.tables[table]
	if !ok {
		return false
	}
	_, ok = t.columns[column]
	return ok
}

func (f *fakeSchemaDB) notNull(table, column string) bool {
	t, ok := f.tables[table]
	if !ok {
		return false
	}
	return t.columns[column]
}

// Exec interprets one statement of schema.sql.
func (f *fakeSchemaDB) Exec(stmt string) error {
	s := strings.Join(strings.Fields(stmt), " ")
	switch {
	case strings.HasPrefix(s, "CREATE TABLE IF NOT EXISTS decisions_schema_version"):
		if _, ok := f.tables["decisions_schema_version"]; !ok {
			f.tables["decisions_schema_version"] = &fakeTable{columns: map[string]bool{"version": true, "applied_at": true}}
		}
		return nil
	case strings.HasPrefix(s, "CREATE TABLE IF NOT EXISTS decisions ("):
		if _, ok := f.tables["decisions"]; !ok {
			f.tables["decisions"] = &fakeTable{columns: map[string]bool{
				"question_hash": true, "kind": true, "answer": true,
				"provider_confidence": false, // nullable from the start on a fresh database
				"floor":               true, "outcome": false, "source": false,
			}}
		}
		return nil
	case strings.HasPrefix(s, "ALTER TABLE decisions ADD COLUMN IF NOT EXISTS source"):
		t, ok := f.tables["decisions"]
		if !ok {
			return fmt.Errorf("fake: ALTER on a table that does not exist")
		}
		if _, ok := t.columns["source"]; !ok {
			t.columns["source"] = false
		}
		return nil
	case strings.HasPrefix(s, "ALTER TABLE decisions ALTER COLUMN provider_confidence DROP NOT NULL"):
		t, ok := f.tables["decisions"]
		if !ok {
			return fmt.Errorf("fake: ALTER on a table that does not exist")
		}
		t.columns["provider_confidence"] = false
		return nil
	case strings.HasPrefix(s, "INSERT INTO decisions_schema_version"):
		if _, ok := f.tables["decisions_schema_version"]; !ok {
			return fmt.Errorf("fake: INSERT into a version table that does not exist")
		}
		v, err := versionIn(s)
		if err != nil {
			return err
		}
		for _, have := range f.versions { // ON CONFLICT DO NOTHING
			if have == v {
				return nil
			}
		}
		f.versions = append(f.versions, v)
		return nil
	case strings.HasPrefix(s, "CREATE INDEX"):
		return nil
	default:
		return fmt.Errorf("fake: unrecognised statement %q; the model understands only schema.sql's own statements", s)
	}
}

// MaxSchemaVersion is 0 where the version table is absent or empty, which is
// what an unmigrated database looks like.
func (f *fakeSchemaDB) MaxSchemaVersion() (int, error) {
	if _, ok := f.tables["decisions_schema_version"]; !ok {
		return 0, nil
	}
	max := 0
	for _, v := range f.versions {
		if v > max {
			max = v
		}
	}
	return max, nil
}

func (f *fakeSchemaDB) Insert(row DecisionRow) error {
	t, ok := f.tables["decisions"]
	if !ok {
		return fmt.Errorf("fake: relation \"decisions\" does not exist")
	}
	if _, ok := t.columns["source"]; !ok {
		return fmt.Errorf("fake: column \"source\" of relation \"decisions\" does not exist")
	}
	if !row.HasProviderConfidence && t.columns["provider_confidence"] {
		return fmt.Errorf("fake: null value in column \"provider_confidence\" violates not-null constraint")
	}
	t.rows = append(t.rows, row)
	f.inserts++
	return nil
}

func (f *fakeSchemaDB) Select(kind string) ([]DecisionRow, error) {
	t, ok := f.tables["decisions"]
	if !ok {
		return nil, fmt.Errorf("fake: relation \"decisions\" does not exist")
	}
	out := []DecisionRow{}
	for _, r := range t.rows {
		if r.Kind == kind {
			out = append(out, r)
		}
	}
	return out, nil
}

// versionIn reads the number out of `INSERT INTO … VALUES (2) …`.
func versionIn(stmt string) (int, error) {
	open := strings.Index(stmt, "VALUES (")
	if open < 0 {
		return 0, fmt.Errorf("fake: no VALUES in %q", stmt)
	}
	rest := stmt[open+len("VALUES ("):]
	close := strings.Index(rest, ")")
	if close < 0 {
		return 0, fmt.Errorf("fake: unterminated VALUES in %q", stmt)
	}
	return strconv.Atoi(strings.TrimSpace(rest[:close]))
}
