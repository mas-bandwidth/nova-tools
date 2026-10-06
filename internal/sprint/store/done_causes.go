package store

import (
	"context"
	"slices"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// EnsureDoneCauses brings a fleet table created before the causes of a failed attempt
// (sprint's cause.go) to the shape schema.go defines: the hidden brief and machinery
// columns added at the end, and done redefined to count them, so a failed finish can be
// placed by its cause and ok% stays the work-fault rate. A table that is not there yet is
// left for Init to create with them; one that has them is left as it is. Each change is
// one step that checks the stored shape first, so a migration cut short is finished by the
// next. Mem and Redis are the backends init and the verbs write through.
func (st *Store) EnsureDoneCauses(ctx context.Context) error {
	if st == nil || st.B == nil {
		return nil
	}
	def, ok := fleetDefinition(st.Names)
	if !ok {
		return nil
	}
	switch b := st.B.(type) {
	case *Mem:
		return b.ensureDoneCauses(def)
	case *Redis:
		return b.ensureDoneCauses(ctx, def)
	default:
		return nil
	}
}

// doneCauseColumns is the columns the migration adds, in the order Init has them.
var doneCauseColumns = []string{sprint.DoneBrief, sprint.DoneMachinery}

// fleetDefinition is the fleet table as schema.go defines it.
func fleetDefinition(names sprint.Names) (ntable.Table, bool) {
	for _, t := range names.Definitions() {
		if t.Name == names.Table(sprint.Fleet) {
			return t, true
		}
	}
	return ntable.Table{}, false
}

// columnOf is the column of def named name.
func columnOf(def ntable.Table, name string) ntable.Column {
	return def.Columns[def.Column(name)]
}

func (m *Mem) ensureDoneCauses(def ntable.Table) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	t := m.tables[def.Name]
	if t == nil {
		return nil
	}
	for _, name := range doneCauseColumns {
		if t.def.Column(name) < 0 {
			t.def.Columns = append(t.def.Columns, columnOf(def, name))
		}
		if !slices.Contains(t.def.Hidden, name) {
			t.def.Hidden = append(t.def.Hidden, name)
		}
	}
	if j := t.def.Column(sprint.Done); j >= 0 {
		t.def.Columns[j] = columnOf(def, sprint.Done)
	}
	return nil
}

func (r *Redis) ensureDoneCauses(ctx context.Context, def ntable.Table) error {
	shape := func() (ntable.Table, bool, error) {
		shapes, err := r.Shapes(ctx, []string{def.Name})
		if err != nil {
			if tableMissing(err) {
				return ntable.Table{}, false, nil
			}
			return ntable.Table{}, false, err
		}
		if len(shapes) == 0 {
			return ntable.Table{}, false, nil
		}
		return shapes[0], true, nil
	}
	set := func(o ntable.SetOpts) error {
		_, err := ntable.Set(ctx, r.C, def.Name, o, r.writeOpts())
		return err
	}
	t, ok, err := shape()
	if err != nil || !ok {
		return err
	}
	for _, name := range doneCauseColumns {
		if t.Column(name) < 0 {
			col := columnOf(def, name)
			if err := set(ntable.SetOpts{ColAdd: &col}); err != nil {
				return err
			}
		}
		if !t.IsHidden(name) {
			if err := set(ntable.SetOpts{Hide: []string{name}}); err != nil {
				return err
			}
		}
	}
	// done counts every finished card, whatever its cause: removed and added again in its
	// place with the new sum (a formula column holds no member, so it can go)
	want := columnOf(def, sprint.Done)
	if j := t.Column(sprint.Done); j >= 0 && t.Columns[j].Projection != want.Projection {
		var at *ntable.Place
		if j+1 < len(t.Columns) {
			at = &ntable.Place{Where: "before", Ref: t.Columns[j+1].Name}
		}
		if err := set(ntable.SetOpts{ColDel: sprint.Done}); err != nil {
			return err
		}
		if err := set(ntable.SetOpts{ColAdd: &want, ColAt: at}); err != nil {
			return err
		}
	}
	return nil
}
