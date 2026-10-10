package store

import (
	"context"
	"errors"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/pkg/ntable"
)

// EnsureReaderTiers adds readers.tiers to a table created before the column
// (empty means every tier, so a row that never set it keeps today's behaviour).
// A table that is not there yet is left for Init to create with the column.
// A table that already has it is left as it is. Mem and Redis are the backends
// init and the reader verbs write through.
func (st *Store) EnsureReaderTiers(ctx context.Context) error {
	if st == nil || st.B == nil {
		return nil
	}
	name := st.Names.Table(sprint.Readers)
	col, ok := readerTiersColumn(st.Names)
	if !ok {
		return nil
	}
	switch b := st.B.(type) {
	case *Mem:
		return b.ensureReaderTiers(name, col)
	case *Redis:
		return b.ensureReaderTiers(ctx, name, col)
	default:
		return nil
	}
}

// readerTiersColumn is the tiers column schema.go defines, so a migration
// adds the same column Init creates.
func readerTiersColumn(names sprint.Names) (ntable.Column, bool) {
	for _, t := range names.Definitions() {
		if t.Name != names.Table(sprint.Readers) {
			continue
		}
		for _, c := range t.Columns {
			if c.Name == sprint.ReaderTiers {
				return c, true
			}
		}
	}
	return ntable.Column{}, false
}

func (m *Mem) ensureReaderTiers(table string, col ntable.Column) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	t := m.tables[table]
	if t == nil {
		return nil
	}
	if t.def.Column(col.Name) >= 0 {
		return nil
	}
	t.def.Columns = append(t.def.Columns, col)
	return nil
}

func (r *Redis) ensureReaderTiers(ctx context.Context, table string, col ntable.Column) error {
	shapes, err := r.Shapes(ctx, []string{table})
	if err != nil {
		if tableMissing(err) {
			return nil
		}
		return err
	}
	if len(shapes) == 0 || shapes[0].Column(col.Name) >= 0 {
		return nil
	}
	_, err = ntable.Set(ctx, r.C, table, ntable.SetOpts{ColAdd: &col}, r.writeOpts())
	return err
}

// tableMissing says the readers table has not been created yet.
func tableMissing(err error) bool {
	var ref *ntable.Refusal
	if errors.As(err, &ref) && ref.Code == "NOTABLE" {
		return true
	}
	return err != nil && strings.Contains(err.Error(), "no such table")
}
