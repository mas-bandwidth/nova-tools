package store

import (
	"context"
	"sort"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
)

// TableNames returns all table names in the memory store, in sorted order.
func (m *Mem) TableNames() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	names := make([]string, 0, len(m.tables))
	for n := range m.tables {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Table returns the definition of table, or false if not found.
func (m *Mem) Table(name string) (ntable.Table, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.tables[name]
	if !ok {
		return ntable.Table{}, false
	}
	return t.def, true
}

// RowDel removes a row from table.
func (m *Mem) RowDel(_ context.Context, table, row string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, err := m.table(table)
	if err != nil {
		return false, err
	}
	if err := m.writeEpoch(t); err != nil {
		return false, err
	}
	ep := t.at(m.active(t))
	idx := -1
	for i, r := range ep.rows {
		if r == row {
			idx = i
			break
		}
	}
	if idx == -1 {
		return false, nil
	}
	ep.rows = append(ep.rows[:idx], ep.rows[idx+1:]...)
	delete(ep.texts, row)
	t.rev++
	t.wrote[m.active(t)] = true
	return true, nil
}

// RowsHide sets or clears the hidden flag for the given row keys.
func (m *Mem) RowsHide(_ context.Context, table string, hide bool, keys []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, err := m.table(table)
	if err != nil {
		return err
	}
	if err := m.writeEpoch(t); err != nil {
		return err
	}
	hiddenMap := map[string]bool{}
	for _, k := range t.def.Hidden {
		hiddenMap[k] = true
	}
	for _, k := range keys {
		if hide {
			hiddenMap[k] = true
		} else {
			delete(hiddenMap, k)
		}
	}
	t.def.Hidden = nil
	for k := range hiddenMap {
		t.def.Hidden = append(t.def.Hidden, k)
	}
	sort.Strings(t.def.Hidden)
	t.rev++
	t.wrote[m.active(t)] = true
	return nil
}

// TableSet applies SetOpts to the table.
func (m *Mem) TableSet(_ context.Context, table string, change ntable.SetOpts) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, err := m.table(table)
	if err != nil {
		return err
	}
	if err := m.writeEpoch(t); err != nil {
		return err
	}
	active := m.active(t)
	ep := t.at(active)

	if change.Footer != nil {
		t.def.FooterLabel = *change.Footer
	}
	if change.Rename != "" {
		delete(m.tables, table)
		t.def.Name = change.Rename
		m.tables[change.Rename] = t
	}
	if len(change.Columns) > 0 {
		t.def.Columns = append([]ntable.Column(nil), change.Columns...)
	}
	if change.ColAdd != nil {
		col := *change.ColAdd
		if change.ColAt != nil {
			names := make([]string, len(t.def.Columns))
			for i, c := range t.def.Columns {
				names[i] = c.Name
			}
			newOrder := placeItem(names, col.Name, *change.ColAt)
			var newCols []ntable.Column
			for _, name := range newOrder {
				if name == col.Name {
					newCols = append(newCols, col)
				} else {
					for _, c := range t.def.Columns {
						if c.Name == name {
							newCols = append(newCols, c)
							break
						}
					}
				}
			}
			t.def.Columns = newCols
		} else {
			t.def.Columns = append(t.def.Columns, col)
		}
	}
	if change.ColDel != "" {
		idx := -1
		for i, c := range t.def.Columns {
			if c.Name == change.ColDel {
				idx = i
				break
			}
		}
		if idx >= 0 {
			t.def.Columns = append(t.def.Columns[:idx], t.def.Columns[idx+1:]...)
		}
	}
	if change.ColMove != nil {
		names := make([]string, len(t.def.Columns))
		for i, c := range t.def.Columns {
			names[i] = c.Name
		}
		newOrder := placeItem(names, change.ColMove.Item, change.ColMove.Place)
		var newCols []ntable.Column
		for _, name := range newOrder {
			for _, c := range t.def.Columns {
				if c.Name == name {
					newCols = append(newCols, c)
					break
				}
			}
		}
		t.def.Columns = newCols
	}
	if change.RowMove != nil {
		ep.rows = placeItem(ep.rows, change.RowMove.Item, change.RowMove.Place)
	}
	if len(change.RowOrder) > 0 {
		seen := map[string]bool{}
		var newRows []string
		for _, r := range change.RowOrder {
			seen[r] = true
			newRows = append(newRows, r)
		}
		for _, r := range ep.rows {
			if !seen[r] {
				newRows = append(newRows, r)
			}
		}
		ep.rows = newRows
	}
	if change.RowSort != nil {
		if change.RowSort.Manual {
			t.def.Sort = ""
		} else {
			sort.SliceStable(ep.rows, func(i, j int) bool {
				cmp := ep.rows[i] < ep.rows[j]
				if change.RowSort.Desc {
					return !cmp
				}
				return cmp
			})
			if change.RowSort.Keep {
				t.def.Sort = change.RowSort.By
			}
		}
	}
	if len(change.Hide) > 0 {
		hideMap := map[string]bool{}
		for _, h := range t.def.Hidden {
			hideMap[h] = true
		}
		for _, h := range change.Hide {
			hideMap[h] = true
		}
		t.def.Hidden = nil
		for h := range hideMap {
			t.def.Hidden = append(t.def.Hidden, h)
		}
		sort.Strings(t.def.Hidden)
	}
	if len(change.Show) > 0 {
		showMap := map[string]bool{}
		for _, s := range change.Show {
			showMap[s] = true
		}
		var newHidden []string
		for _, h := range t.def.Hidden {
			if !showMap[h] {
				newHidden = append(newHidden, h)
			}
		}
		t.def.Hidden = newHidden
	}
	if change.Visible != nil {
		t.def.HiddenTable = !*change.Visible
	}
	if change.Hidden != nil {
		t.def.Hidden = append([]string(nil), *change.Hidden...)
	}
	t.rev++
	t.wrote[active] = true
	return nil
}

func placeItem(items []string, item string, p ntable.Place) []string {
	var remaining []string
	for _, it := range items {
		if it != item {
			remaining = append(remaining, it)
		}
	}
	switch p.Where {
	case "first":
		return append([]string{item}, remaining...)
	case "last":
		return append(remaining, item)
	case "before":
		var out []string
		placed := false
		for _, it := range remaining {
			if it == p.Ref && !placed {
				out = append(out, item)
				placed = true
			}
			out = append(out, it)
		}
		if !placed {
			out = append(out, item)
		}
		return out
	case "after":
		var out []string
		placed := false
		for _, it := range remaining {
			out = append(out, it)
			if it == p.Ref && !placed {
				out = append(out, item)
				placed = true
			}
		}
		if !placed {
			out = append(out, item)
		}
		return out
	}
	return items
}

// TableClear removes all members from table.
func (m *Mem) TableClear(_ context.Context, table string) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, err := m.table(table)
	if err != nil {
		return 0, err
	}
	if err := m.writeEpoch(t); err != nil {
		return 0, err
	}
	active := m.active(t)
	n := int64(0)
	for id, mm := range t.members {
		if mm.epoch == active && mm.placed {
			delete(t.members, id)
			n++
		}
	}
	t.rev++
	t.wrote[active] = true
	return n, nil
}

// CellAdd adds members to row and column in table.
func (m *Mem) CellAdd(_ context.Context, table, row, col string, score float64, members []string) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, err := m.table(table)
	if err != nil {
		return 0, err
	}
	if err := m.writeEpoch(t); err != nil {
		return 0, err
	}
	active := m.active(t)
	if !containsStr(t.at(active).rows, row) {
		return 0, refusal("NOROW", "no row "+row)
	}
	hasCol := false
	for _, c := range t.def.Columns {
		if c.Name == col {
			hasCol = true
			break
		}
	}
	if !hasCol {
		return 0, refusal("NOCOL", "no column "+col)
	}
	n := int64(0)
	for _, id := range members {
		mm := t.members[id]
		if mm == nil {
			mm = &memMember{epoch: active, rev: 0}
			t.members[id] = mm
		}
		mm.placed = true
		mm.row = row
		mm.col = col
		mm.score = score
		mm.rev++
		n++
	}
	t.rev++
	t.wrote[active] = true
	return n, nil
}

// CellRemove removes members from row and column in table.
func (m *Mem) CellRemove(_ context.Context, table, row, col string, members []string) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, err := m.table(table)
	if err != nil {
		return 0, err
	}
	if err := m.writeEpoch(t); err != nil {
		return 0, err
	}
	active := m.active(t)
	n := int64(0)
	for _, id := range members {
		if mm, ok := t.members[id]; ok && mm.epoch == active && mm.placed && mm.row == row && mm.col == col {
			mm.placed = false
			mm.row = ""
			mm.col = ""
			mm.rev++
			n++
		}
	}
	t.rev++
	t.wrote[active] = true
	return n, nil
}

// CellMove moves members from fromCol to toCol on row in table.
func (m *Mem) CellMove(_ context.Context, table, row, fromCol, toCol string, members []string) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, err := m.table(table)
	if err != nil {
		return 0, err
	}
	if err := m.writeEpoch(t); err != nil {
		return 0, err
	}
	active := m.active(t)
	n := int64(0)
	for _, id := range members {
		if mm, ok := t.members[id]; ok && mm.epoch == active && mm.placed && mm.row == row && mm.col == fromCol {
			mm.col = toCol
			mm.rev++
			n++
		}
	}
	t.rev++
	t.wrote[active] = true
	return n, nil
}

// CellMembers returns the members of row and column in table, sorted by score and ID.
func (m *Mem) CellMembers(_ context.Context, table, row, col string) ([]ntable.Member, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, err := m.table(table)
	if err != nil {
		return nil, err
	}
	active := m.active(t)
	var out []ntable.Member
	for id, mm := range t.members {
		if mm.epoch == active && mm.placed && mm.row == row && mm.col == col {
			out = append(out, ntable.Member{Member: id, Score: mm.score})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score < out[j].Score
		}
		return out[i].Member < out[j].Member
	})
	return out, nil
}

// MemberFind locates a member in table.
func (m *Mem) MemberFind(_ context.Context, table, id string) (uint64, uint64, string, string, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, err := m.table(table)
	if err != nil {
		return 0, 0, "missing", "", "", nil
	}
	active := m.active(t)
	mm := t.members[id]
	if mm == nil || mm.epoch != active {
		return 0, 0, "missing", "", "", nil
	}
	if !mm.placed {
		return mm.epoch, mm.rev, "unplaced", "", "", nil
	}
	return mm.epoch, mm.rev, "placed", mm.row, mm.col, nil
}

// MemberCreate creates an unplaced member in table.
func (m *Mem) MemberCreate(_ context.Context, table, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, err := m.table(table)
	if err != nil {
		return err
	}
	active := m.active(t)
	if mm, ok := t.members[id]; ok && mm.epoch == active {
		return refusal("EXISTS", "member already exists")
	}
	t.members[id] = &memMember{epoch: active, rev: 0}
	t.rev++
	t.wrote[active] = true
	return nil
}

// ViewList returns all view names.
func (m *Mem) ViewList() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var names []string
	for n := range m.views {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// ViewState sets a view's state string.
func (m *Mem) ViewState(_ context.Context, name, state string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.views[name]
	if !ok {
		return refusal("NOVIEW", "no such view "+name)
	}
	v.State = state
	m.views[name] = v
	return nil
}

// TableCheck audits the members and cells count of table.
func (m *Mem) TableCheck(_ context.Context, table string) (epoch, rev, members, cells uint64, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, err := m.table(table)
	if err != nil {
		return 0, 0, 0, 0, err
	}
	active := m.active(t)
	ep := t.at(active)
	memberCount := uint64(0)
	for _, mm := range t.members {
		if mm.epoch == active && mm.placed {
			memberCount++
		}
	}
	cellsCount := uint64(len(ep.rows) * len(t.def.Columns))
	return active, t.rev, memberCount, cellsCount, nil
}
