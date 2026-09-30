package tset

import (
	"context"
	"encoding/json"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var memScoreLexeme = regexp.MustCompile(`^[+-]?(?:[0-9]+(?:\.[0-9]*)?|\.[0-9]+)(?:[eE][+-]?[0-9]+)?$`)

// parseMemScore models the finite part of the target Redis ZADD score domain.
// In particular ParseFloat alone would accept hexadecimal and underflowed
// values that ZADD refuses, and would accept spellings outside the wire rule.
func parseMemScore(s string) (float64, error) {
	if !memScoreLexeme.MatchString(s) {
		return 0, memRefusal("REQUEST", RefusalDetail{})
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsInf(f, 0) || math.IsNaN(f) || (f == 0 && memNonzeroDecimal(s)) {
		return 0, memRefusal("REQUEST", RefusalDetail{})
	}
	return f, nil
}

func memNonzeroDecimal(s string) bool {
	end := strings.IndexAny(s, "eE")
	if end < 0 {
		end = len(s)
	}
	for i := 0; i < end; i++ {
		if s[i] >= '1' && s[i] <= '9' {
			return true
		}
	}
	return false
}

func memScoreText(f float64) string {
	if f == 0 {
		return "0"
	}
	return strconv.FormatFloat(f, 'g', -1, 64)
}

type memBound struct {
	v         float64
	exclusive bool
}

func parseMemBound(s string) (memBound, error) {
	parsed, ok := parseReadBound(s)
	if !ok {
		return memBound{}, memRefusal("REQUEST", RefusalDetail{})
	}
	return memBound{v: parsed.value, exclusive: parsed.exclusive}, nil
}

func memWithinBounds(v float64, min, max memBound) bool {
	if v < min.v || (min.exclusive && v == min.v) {
		return false
	}
	if v > max.v || (max.exclusive && v == max.v) {
		return false
	}
	return true
}

type memRowOp struct {
	add   bool
	del   bool
	index int
}

type memWorkBudget struct {
	fieldObservations int
	cellProbes        int
	fetchedBytes      int
	plannedBytes      int
	plannedCommands   int
}

// rowsetRead accounts for one typed row-index observation. ZCARD contributes
// the decimal count text; each ZMSCORE piece contributes the returned score
// strings (absent names contribute no payload). Carry this work into the rest
// of the step rather than starting a fresh budget after the guard.
func (b *memWorkBudget) rowsetRead(index int, table string, payload int) error {
	b.cellProbes++
	if b.cellProbes > 20000 {
		return memRefusal("LIMIT", RefusalDetail{EntryIndex: memIndex(index), Table: table, Budget: "cell_probes", Limit: memInt64(20000), Actual: memInt64(int64(b.cellProbes))})
	}
	b.fetchedBytes += payload
	if b.fetchedBytes > 8<<20 {
		return memRefusal("LIMIT", RefusalDetail{EntryIndex: memIndex(index), Table: table, Budget: "raw_fetched_bytes", Limit: memInt64(8 << 20), Actual: memInt64(int64(b.fetchedBytes))})
	}
	return nil
}

func (b *memWorkBudget) observeMember(record *memRecord, entry Entry, memberIndex int) error {
	b.cellProbes++
	if entry.Kind == "move" && entry.To != "" && entry.To != entry.From {
		b.cellProbes++
	}
	if b.cellProbes > 20000 {
		return memRefusal("LIMIT", RefusalDetail{Budget: "cell_probes"})
	}
	selected := make(map[string]bool)
	for name := range entry.Set {
		selected[name] = true
	}
	if len(entry.Each) != 0 && memberIndex < len(entry.Each) {
		for name := range entry.Each[memberIndex] {
			selected[name] = true
		}
	}
	for _, name := range entry.Unset {
		selected[name] = true
	}
	for _, name := range entry.BeforeFields {
		selected[name] = true
	}
	if len(selected) > 128 {
		return memRefusal("LIMIT", RefusalDetail{Budget: "field_projection"})
	}
	// Presence is observed for every selected field, including when the record
	// hash is absent. Only returned field names/values contribute fetched bytes.
	b.fieldObservations += len(selected)
	if b.fieldObservations > 768000 {
		return memRefusal("LIMIT", RefusalDetail{Budget: "field_observations"})
	}
	if record == nil {
		return nil
	}
	b.fetchedBytes += len(record.epoch) + len(record.revision) + len(record.score)
	if record.place != nil {
		b.fetchedBytes += len(record.place.row) + len(record.place.col)
	}
	for name := range selected {
		b.fetchedBytes += len(name) // HKEY/HMGET and field-name accounting
		if value, ok := record.fields[name]; ok {
			b.fetchedBytes += len(value)
		}
	}
	if b.fetchedBytes > 8<<20 {
		return memRefusal("LIMIT", RefusalDetail{Budget: "raw_fetched_bytes"})
	}
	return nil
}

func (m *Mem) planStep(ctx context.Context, pre, next *memNamespace, step Step) (Reply, error) {
	writeEpoch := step.Epoch
	rowsetPrefix := 0
	for rowsetPrefix < len(step.Entries) && step.Entries[rowsetPrefix].Kind == "rowset" {
		rowsetPrefix++
	}
	advance := false
	if rowsetPrefix < len(step.Entries) && step.Entries[rowsetPrefix].Kind == "advance" {
		advance = true
		if step.Entries[rowsetPrefix].AdvanceFrom != step.Epoch {
			return Reply{}, memEpochRefusal("ADVANCE", pre.active)
		}
		var ok bool
		writeEpoch, ok = memNextDecimal(step.Epoch)
		if !ok {
			return Reply{}, memRefusal("OVERFLOW", RefusalDetail{EntryIndex: memIndex(rowsetPrefix)})
		}
	}
	for i, entry := range step.Entries {
		if entry.Kind == "advance" && (!advance || i != rowsetPrefix) {
			return Reply{}, memRefusal("ADVANCE", RefusalDetail{EntryIndex: memIndex(i), ActiveEpoch: pre.active})
		}
		if entry.Kind == "rowset" && i >= rowsetPrefix {
			return Reply{}, memRefusal("REQUEST", RefusalDetail{EntryIndex: memIndex(i), Table: entry.Table})
		}
	}
	if rowsetPrefix != 0 && !advance {
		return Reply{}, memRefusal("REQUEST", RefusalDetail{EntryIndex: memIndex(rowsetPrefix - 1)})
	}
	if pre.epochs[step.Epoch] == nil {
		return Reply{}, memRefusal("DRIFT", RefusalDetail{})
	}
	budget := memWorkBudget{}
	// H1 rowset observes the complete named table row index in the request
	// epoch. Do this before constructing an advance successor or prospective
	// row state. A count mismatch has no singled-out row; a named score
	// mismatch identifies the first row in request order.
	for i := 0; i < rowsetPrefix; i++ {
		entry := step.Entries[i]
		requestTable := pre.epochs[step.Epoch].tables[entry.Table]
		if requestTable == nil {
			return Reply{}, memRefusal("DRIFT", RefusalDetail{EntryIndex: memIndex(i), Table: entry.Table, ActiveEpoch: pre.active})
		}
		if err := budget.rowsetRead(i, entry.Table, len(strconv.Itoa(len(requestTable.rows)))); err != nil {
			return Reply{}, err
		}
		if len(requestTable.rows) != len(entry.Rows) {
			return Reply{}, memRefusal("ROWSET", RefusalDetail{EntryIndex: memIndex(i), Table: entry.Table, ActiveEpoch: pre.active})
		}
		// The model has no enclosing preplan cache. Every expected name is
		// fetched in one of the same 1,000-name ZMSCORE pieces as the store.
		for first := 0; first < len(entry.Rows); first += 1000 {
			last := min(first+1000, len(entry.Rows))
			payload := 0
			for _, wanted := range entry.Rows[first:last] {
				if rank, present := requestTable.rows[wanted.Row]; present {
					payload += len(rank)
				}
			}
			if err := budget.rowsetRead(i, entry.Table, payload); err != nil {
				return Reply{}, err
			}
		}
		for _, wanted := range entry.Rows {
			rank, present := requestTable.rows[wanted.Row]
			if !present {
				return Reply{}, memRefusal("ROWSET", RefusalDetail{EntryIndex: memIndex(i), Table: entry.Table, Rows: []string{wanted.Row}, ActiveEpoch: pre.active})
			}
			if !memValidDecimal(rank) || memCompareDecimal(rank, "9007199254740991") > 0 {
				return Reply{}, memRefusal("DRIFT", RefusalDetail{EntryIndex: memIndex(i), Table: entry.Table, Rows: []string{wanted.Row}, ActiveEpoch: pre.active})
			}
			if rank != wanted.Rank {
				return Reply{}, memRefusal("ROWSET", RefusalDetail{EntryIndex: memIndex(i), Table: entry.Table, Rows: []string{wanted.Row}, ActiveEpoch: pre.active})
			}
		}
	}
	if advance {
		if len(next.receipts[writeEpoch]) != 0 {
			return Reply{}, memRefusal("DRIFT", RefusalDetail{EntryIndex: memIndex(rowsetPrefix)})
		}
		if existing := next.epochs[writeEpoch]; existing != nil && !memEpochEmpty(existing) {
			return Reply{}, memRefusal("DRIFT", RefusalDetail{EntryIndex: memIndex(rowsetPrefix)})
		}
		e := &memEpoch{tables: make(map[string]*memTableEpoch, len(next.defs))}
		for table := range next.defs {
			e.tables[table] = newMemTableEpoch()
		}
		next.epochs[writeEpoch] = e
		next.active = writeEpoch
	}
	preEpoch := pre.epochs[writeEpoch]
	if preEpoch == nil {
		preEpoch = &memEpoch{tables: make(map[string]*memTableEpoch)}
		for table := range pre.defs {
			preEpoch.tables[table] = newMemTableEpoch()
		}
	}
	workEpoch := next.epochs[writeEpoch]
	if workEpoch == nil {
		return Reply{}, memRefusal("DRIFT", RefusalDetail{})
	}

	// Rows are a step-wide prospective set.  Repeated same-direction names
	// normalize into their first occurrence, and ranks follow that order.
	rowOps := make(map[string]map[string]*memRowOp)
	rowOrder := make([]struct{ table, row string }, 0)
	tableSeen := make(map[string]bool)
	for i, entry := range step.Entries {
		if entry.Kind == "advance" {
			continue
		}
		if next.defs[entry.Table] == nil {
			return Reply{}, memRefusal("NOTABLE", RefusalDetail{EntryIndex: memIndex(i), Table: entry.Table})
		}
		tableSeen[entry.Table] = true
		if len(tableSeen) > 4 {
			return Reply{}, memRefusal("LIMIT", RefusalDetail{EntryIndex: memIndex(i)})
		}
		if entry.Kind != "rows" {
			continue
		}
		if rowOps[entry.Table] == nil {
			rowOps[entry.Table] = make(map[string]*memRowOp)
		}
		for _, direction := range []struct {
			rows []string
			add  bool
		}{{entry.Add, true}, {entry.Del, false}} {
			for _, row := range direction.rows {
				if !validMemRow(row) {
					return Reply{}, memRefusal("REQUEST", RefusalDetail{EntryIndex: memIndex(i), Table: entry.Table, Rows: []string{row}})
				}
				op := rowOps[entry.Table][row]
				if op == nil {
					op = &memRowOp{index: i}
					rowOps[entry.Table][row] = op
					rowOrder = append(rowOrder, struct{ table, row string }{entry.Table, row})
				}
				if direction.add {
					op.add = true
				} else {
					op.del = true
				}
				if op.add && op.del {
					return Reply{}, memRefusal("ROWCONFLICT", RefusalDetail{EntryIndex: memIndex(i), Table: entry.Table, Rows: []string{row}})
				}
			}
		}
	}
	rowCap := 100
	if advance {
		rowCap = 1024
	}
	if len(rowOrder) > rowCap {
		return Reply{}, memRefusal("LIMIT", RefusalDetail{Limit: memInt64(int64(rowCap)), Actual: memInt64(int64(len(rowOrder)))})
	}
	rowUnion := make(map[string]bool)
	for i := 0; i < rowsetPrefix; i++ {
		entry := step.Entries[i]
		for _, row := range entry.Rows {
			rowUnion[entry.Table+"\x00"+row.Row] = true
		}
	}
	for _, key := range rowOrder {
		rowUnion[key.table+"\x00"+key.row] = true
	}
	if len(rowUnion) > 1024 {
		return Reply{}, memRefusal("LIMIT", RefusalDetail{Budget: "rows", Limit: memInt64(1024), Actual: memInt64(int64(len(rowUnion)))})
	}
	for _, key := range rowOrder {
		op := rowOps[key.table][key.row]
		if !op.add || workEpoch.tables[key.table].rows[key.row] != "" {
			continue
		}
		t := workEpoch.tables[key.table]
		maxRank := Decimal("")
		for _, rank := range t.rows {
			if maxRank == "" || memCompareDecimal(rank, maxRank) > 0 {
				maxRank = rank
			}
		}
		if maxRank == "" {
			t.rows[key.row] = "0"
		} else {
			rank, ok := memNextDecimal(maxRank)
			if !ok || memCompareDecimal(rank, "9007199254740991") > 0 {
				return Reply{}, memRefusal("OVERFLOW", RefusalDetail{EntryIndex: memIndex(op.index), Table: key.table, Rows: []string{key.row}})
			}
			t.rows[key.row] = rank
		}
	}

	changedPer := make([]int, len(step.Entries))
	plan := &MemPlan{Entries: make([]MemPlanEntry, len(step.Entries)), Before: make(map[string]map[string]MemberRecord)}
	for i, entry := range step.Entries {
		normalized := cloneMemPlanInput(entry)
		if entry.Kind == "create" || entry.Kind == "move" || entry.Kind == "remove" || entry.Kind == "guard" {
			normalized.IDs = nil
			normalized.Scores = nil
			normalized.Revs = nil
			normalized.About = nil
			normalized.Set = nil
			normalized.Each = nil
			normalized.Unset = nil
		}
		if entry.Kind == "rows" {
			normalized.Add = nil
			normalized.Del = nil
		}
		plan.Entries[i] = MemPlanEntry{Index: i, Entry: normalized}
	}
	seenID := make(map[string]bool)
	propCounts, propObserved := make(map[string]int), make(map[string]bool)
	candidates, guarded, changed := 0, 0, 0
	for i, entry := range step.Entries {
		if err := ctx.Err(); err != nil {
			return Reply{}, err
		}
		switch entry.Kind {
		case "advance", "rowset", "rows":
			continue
		case "count":
			budget.cellProbes += memUniqueStrings(entry.Cells)
			if budget.cellProbes > 20000 {
				return Reply{}, memRefusal("LIMIT", RefusalDetail{EntryIndex: memIndex(i), Budget: "cell_probes"})
			}
			if err := memPlanCount(preEpoch.tables[entry.Table], next.defs[entry.Table], entry, i); err != nil {
				return Reply{}, err
			}
			continue
		case "rcount":
			budget.cellProbes += memUniqueStrings(entry.Cells)
			if budget.cellProbes > 20000 {
				return Reply{}, memRefusal("LIMIT", RefusalDetail{EntryIndex: memIndex(i), Budget: "cell_probes"})
			}
			if err := memPlanRCount(preEpoch.tables[entry.Table], next.defs[entry.Table], entry, i); err != nil {
				return Reply{}, err
			}
			continue
		case "prop", "propguard":
			prior, didChange, err := memPlanProp(preEpoch, workEpoch, entry, i, propCounts, propObserved, &budget)
			if err != nil {
				return Reply{}, err
			}
			plan.Entries[i].Prop = &prior
			if didChange {
				changed++
				changedPer[i]++
			}
			continue
		case "create", "move", "remove", "guard":
		default:
			return Reply{}, memRefusal("REQUEST", RefusalDetail{EntryIndex: memIndex(i)})
		}
		for j, id := range entry.IDs {
			key := entry.Table + "\x00" + id
			if seenID[key] {
				return Reply{}, memRefusal("TWICE", RefusalDetail{EntryIndex: memIndex(i), Table: entry.Table, IDs: []string{id}})
			}
			seenID[key] = true
			if entry.Kind == "guard" {
				guarded++
				if guarded > 4000 {
					return Reply{}, memRefusal("LIMIT", RefusalDetail{EntryIndex: memIndex(i), Table: entry.Table, IDs: []string{id}})
				}
			} else {
				candidates++
				if candidates > 2000 {
					return Reply{}, memRefusal("LIMIT", RefusalDetail{EntryIndex: memIndex(i), Table: entry.Table, IDs: []string{id}})
				}
			}
			if err := budget.observeMember(preEpoch.tables[entry.Table].records[id], entry, j); err != nil {
				return Reply{}, err
			}
			var before *memRecord
			if table := preEpoch.tables[entry.Table]; table != nil {
				before = table.records[id]
			}
			fields := memPlanProjection(entry, j)
			prior := memProjectPlanRecord(id, before, fields)
			didChange, err := memPlanMember(pre, next, preEpoch, workEpoch, rowOps, entry, i, j)
			if err != nil {
				return Reply{}, err
			}
			if plan.Before[entry.Table] == nil {
				plan.Before[entry.Table] = make(map[string]MemberRecord)
			}
			plan.Before[entry.Table][id] = cloneMemPlanRecord(prior)
			if didChange {
				changed++
				changedPer[i]++
				planned := &plan.Entries[i]
				after := memProjectPlanRecord(id, workEpoch.tables[entry.Table].records[id], fields)
				planned.Entry.IDs = append(planned.Entry.IDs, id)
				if len(entry.Scores) != 0 {
					planned.Entry.Scores = append(planned.Entry.Scores, entry.Scores[j])
				}
				if len(entry.Revs) != 0 {
					planned.Entry.Revs = append(planned.Entry.Revs, entry.Revs[j])
				}
				if len(entry.About) != 0 {
					planned.Entry.About = append(planned.Entry.About, entry.About[j])
				}
				planned.Before = append(planned.Before, prior)
				planned.After = append(planned.After, after)
				planned.FieldChanges = append(planned.FieldChanges, memPlanFieldChange(prior, after, fields))
			}
		}
	}
	// A5: deletion looks at final occupancy, including same-step removals.
	for _, key := range rowOrder {
		op := rowOps[key.table][key.row]
		if !op.del {
			continue
		}
		t := workEpoch.tables[key.table]
		if t.rows[key.row] == "" {
			continue
		}
		budget.cellProbes += len(next.defs[key.table].columns)
		if budget.cellProbes > 20000 {
			return Reply{}, memRefusal("LIMIT", RefusalDetail{EntryIndex: memIndex(op.index), Table: key.table, Rows: []string{key.row}, Budget: "cell_probes"})
		}
		for _, members := range t.cells[key.row] {
			if len(members) != 0 {
				return Reply{}, memRefusal("OCCUPIED", RefusalDetail{EntryIndex: memIndex(op.index), Table: key.table, Rows: []string{key.row}})
			}
		}
		delete(t.rows, key.row)
		delete(t.cells, key.row)
	}
	for _, key := range rowOrder {
		op := rowOps[key.table][key.row]
		beforeRank := preEpoch.tables[key.table].rows[key.row]
		afterRank := workEpoch.tables[key.table].rows[key.row]
		planned := &plan.Entries[op.index]
		if op.add && beforeRank == "" && afterRank != "" {
			planned.Entry.Add = append(planned.Entry.Add, key.row)
			planned.Added = append(planned.Added, RowRank{Row: key.row, Rank: afterRank})
		}
		if op.del && beforeRank != "" && afterRank == "" {
			planned.Entry.Del = append(planned.Entry.Del, key.row)
			planned.Deleted = append(planned.Deleted, key.row)
		}
	}
	reply := Reply{Status: "ok", EpochBefore: step.Epoch, EpochAfter: writeEpoch,
		Changed: changed, Guarded: guarded, ChangedPerEntry: changedPer,
		FirstSeq: "0", LastSeq: "0", Lines: 0, Result: step.Result, Replay: false}
	budget.planWrites(pre, next, step, writeEpoch, rowOrder)
	if err := budget.planReceipt(step, reply); err != nil {
		return Reply{}, err
	}
	if err := budget.checkPlannedLimits(); err != nil {
		return Reply{}, err
	}
	// L1-only profile has no L2 semantic log. Counters are intentionally limited
	// to work this independent model can measure without Redis command choices.
	plan.PlannedCommands = int64(budget.plannedCommands)
	plan.PlannedArgvBytes = int64(budget.plannedBytes)
	reply.Counters = budget.counters(candidates, guarded, changed, len(rowUnion))
	reply.MemPlan = plan
	return reply, nil
}

// The table plan exposes only the fields the L1 planner observed: the
// effective mutation-field union and declared before_fields. In particular,
// unrelated stored application fields are not exposed to an enclosing plan.
func memPlanProjection(entry Entry, memberIndex int) []string {
	selected := make(map[string]bool, len(entry.Set)+len(entry.Unset)+len(entry.BeforeFields))
	for name := range entry.Set {
		selected[name] = true
	}
	if len(entry.Each) != 0 {
		for name := range entry.Each[memberIndex] {
			selected[name] = true
		}
	}
	for _, name := range entry.Unset {
		selected[name] = true
	}
	for _, name := range entry.BeforeFields {
		selected[name] = true
	}
	fields := make([]string, 0, len(selected))
	for name := range selected {
		fields = append(fields, name)
	}
	sort.Strings(fields)
	return fields
}

func memProjectPlanRecord(id string, record *memRecord, fields []string) MemberRecord {
	out := MemberRecord{ID: id, Fields: make(map[string]FieldValue, len(fields))}
	if record != nil {
		out.Exists = true
		out.Epoch = record.epoch
		out.Revision = record.revision
		out.Score = record.score
		if record.place != nil {
			out.Place = &CellPlace{Row: record.place.row, Col: record.place.col}
		}
	}
	for _, name := range fields {
		if record != nil {
			if value, present := record.fields[name]; present {
				out.Fields[name] = FieldValue{Present: true, Value: value}
				continue
			}
		}
		out.Fields[name] = FieldValue{Present: false}
	}
	return out
}

func cloneMemPlanRecord(record MemberRecord) MemberRecord {
	out := record
	if record.Place != nil {
		place := *record.Place
		out.Place = &place
	}
	if record.Fields != nil {
		out.Fields = make(map[string]FieldValue, len(record.Fields))
		for name, value := range record.Fields {
			out.Fields[name] = value
		}
	}
	return out
}

// cloneMemPlan detaches the public observation returned by Plan from the
// prepared candidate. A caller may annotate or edit its maps and slices for
// composition without changing what Commit will publish or return.
func cloneMemPlan(plan *MemPlan) *MemPlan {
	if plan == nil {
		return nil
	}
	out := &MemPlan{Replay: plan.Replay, PlannedCommands: plan.PlannedCommands,
		PlannedArgvBytes: plan.PlannedArgvBytes, Entries: make([]MemPlanEntry, len(plan.Entries)),
		Before: make(map[string]map[string]MemberRecord, len(plan.Before))}
	for table, records := range plan.Before {
		out.Before[table] = make(map[string]MemberRecord, len(records))
		for id, record := range records {
			out.Before[table][id] = cloneMemPlanRecord(record)
		}
	}
	for i, entry := range plan.Entries {
		copyEntry := &out.Entries[i]
		copyEntry.Index = entry.Index
		copyEntry.Entry = cloneMemPlanInput(entry.Entry)
		copyEntry.Before = make([]MemberRecord, len(entry.Before))
		for j, record := range entry.Before {
			copyEntry.Before[j] = cloneMemPlanRecord(record)
		}
		copyEntry.After = make([]MemberRecord, len(entry.After))
		for j, record := range entry.After {
			copyEntry.After[j] = cloneMemPlanRecord(record)
		}
		copyEntry.FieldChanges = make([]MemFieldChange, len(entry.FieldChanges))
		for j, change := range entry.FieldChanges {
			copyEntry.FieldChanges[j] = MemFieldChange{Unset: append([]string(nil), change.Unset...)}
			if change.Set != nil {
				copyEntry.FieldChanges[j].Set = cloneFields(change.Set)
			}
		}
		copyEntry.Added = append([]RowRank(nil), entry.Added...)
		copyEntry.Deleted = append([]string(nil), entry.Deleted...)
		if entry.Prop != nil {
			prop := *entry.Prop
			copyEntry.Prop = &prop
		}
	}
	return out
}

func memPlanFieldChange(before, after MemberRecord, fields []string) MemFieldChange {
	out := MemFieldChange{Set: make(map[string]string)}
	for _, name := range fields {
		prior, next := before.Fields[name], after.Fields[name]
		if next.Present && (!prior.Present || prior.Value != next.Value) {
			out.Set[name] = next.Value
		} else if prior.Present && !next.Present {
			out.Unset = append(out.Unset, name)
		}
	}
	return out
}

func cloneMemPlanInput(entry Entry) Entry {
	out := entry
	out.IDs = append([]string(nil), entry.IDs...)
	out.Scores = append([]string(nil), entry.Scores...)
	out.Revs = append([]Decimal(nil), entry.Revs...)
	if entry.Set != nil {
		out.Set = cloneFields(entry.Set)
	}
	if entry.Each != nil {
		out.Each = make([]map[string]string, len(entry.Each))
		for i, fields := range entry.Each {
			if fields != nil {
				out.Each[i] = cloneFields(fields)
			}
		}
	}
	out.Unset = append([]string(nil), entry.Unset...)
	out.BeforeFields = append([]string(nil), entry.BeforeFields...)
	out.About = append([]string(nil), entry.About...)
	out.Meta = append([]byte(nil), entry.Meta...)
	out.Add = append([]string(nil), entry.Add...)
	out.Del = append([]string(nil), entry.Del...)
	out.Rows = append([]RowRank(nil), entry.Rows...)
	out.Cells = append([]string(nil), entry.Cells...)
	out.CountMax = append([]uint64(nil), entry.CountMax...)
	if entry.AtLeast != nil {
		v := *entry.AtLeast
		out.AtLeast = &v
	}
	if entry.AtMost != nil {
		v := *entry.AtMost
		out.AtMost = &v
	}
	if entry.Value != nil {
		v := *entry.Value
		out.Value = &v
	}
	return out
}

// planFence prepares the one receipt command without entering table, row,
// member or log planning. The caller publishes the cloned namespace only after
// saveReceipt succeeds, giving the fence and the original operation one
// atomic receipt winner.
func (m *Mem) planFence(step Step) (Reply, error) {
	reply := Reply{Status: "fenced", EpochBefore: step.Epoch, EpochAfter: step.Epoch,
		ChangedPerEntry: []int{}, FirstSeq: "0", LastSeq: "0", Result: ""}
	budget := memWorkBudget{}
	if err := budget.planReceipt(step, reply); err != nil {
		return Reply{}, err
	}
	if err := budget.checkPlannedLimits(); err != nil {
		return Reply{}, err
	}
	reply.Counters = budget.counters(0, 0, 0, 0)
	return reply, nil
}

func (b *memWorkBudget) planReceipt(step Step, reply Reply) error {
	if step.Op == nil {
		return nil
	}
	if step.Intent == nil {
		return memRefusal("REQUEST", RefusalDetail{})
	}
	encoded := encodeMemReceipt(memReceiptForReply(step, reply))
	if len(encoded) > maxReceiptBytes {
		return memRefusal("LIMIT", RefusalDetail{Budget: "receipt_bytes", Limit: memInt64(maxReceiptBytes), Actual: memInt64(int64(len(encoded)))})
	}
	b.charge("HSET", step.Space+"sprint:done@"+string(step.Epoch), *step.Op, string(encoded))
	return nil
}

func (b *memWorkBudget) checkPlannedLimits() error {
	if b.plannedBytes > 8<<20 {
		return memRefusal("LIMIT", RefusalDetail{Budget: "planned_argv_bytes", Limit: memInt64(8 << 20), Actual: memInt64(int64(b.plannedBytes))})
	}
	if b.plannedCommands > 65536 {
		return memRefusal("LIMIT", RefusalDetail{Budget: "planned_commands", Limit: memInt64(65536), Actual: memInt64(int64(b.plannedCommands))})
	}
	return nil
}

func (b *memWorkBudget) counters(candidates, guarded, changed, rows int) json.RawMessage {
	counters, _ := json.Marshal(struct {
		Candidates        int `json:"candidates"`
		Guarded           int `json:"guarded"`
		Changed           int `json:"changed"`
		Rows              int `json:"rows"`
		FieldObservations int `json:"field_observations"`
		CellProbes        int `json:"cell_probes"`
		FetchedBytes      int `json:"raw_fetched_bytes"`
		PlannedBytes      int `json:"planned_argv_bytes"`
		PlannedCommands   int `json:"planned_commands"`
	}{candidates, guarded, changed, rows, b.fieldObservations, b.cellProbes,
		b.fetchedBytes, b.plannedBytes, b.plannedCommands})
	return counters
}

func memEpochEmpty(e *memEpoch) bool {
	for _, t := range e.tables {
		if len(t.rows) != 0 || len(t.records) != 0 || len(t.props) != 0 {
			return false
		}
		for _, columns := range t.cells {
			for _, members := range columns {
				if len(members) != 0 {
					return false
				}
			}
		}
	}
	return true
}

func (b *memWorkBudget) planWrites(pre, next *memNamespace, step Step, epoch Decimal, rowOrder []struct{ table, row string }) {
	// Count the scalar argv bytes of each prospective command. The model does
	// not issue Redis commands, but admission must still use concrete keys and
	// field/value occurrences, with no RESP framing or arbitrary allowance.
	for _, entry := range step.Entries {
		if entry.Kind != "create" && entry.Kind != "move" && entry.Kind != "remove" {
			continue
		}
		for i, id := range entry.IDs {
			var old *memRecord
			if e := pre.epochs[epoch]; e != nil && e.tables[entry.Table] != nil {
				old = e.tables[entry.Table].records[id]
			}
			newRecord := next.epochs[epoch].tables[entry.Table].records[id]
			if newRecord == nil || old != nil && newRecord.revision == old.revision {
				continue
			}
			recordKey := next.defs[entry.Table].memberPrefix + id
			hset := []string{"HSET", recordKey, "revision", string(newRecord.revision)}
			if old == nil {
				hset = append(hset, "epoch", string(newRecord.epoch))
			}
			if newRecord.place != nil {
				hset = append(hset, "place:"+entry.Table, newRecord.place.row+":"+newRecord.place.col)
			}
			fieldNames := make([]string, 0, len(newRecord.fields))
			for name := range newRecord.fields {
				fieldNames = append(fieldNames, name)
			}
			sort.Strings(fieldNames)
			for _, name := range fieldNames {
				value := newRecord.fields[name]
				if oldValue, ok := memOldField(old, name); !ok || oldValue != value {
					hset = append(hset, name, value)
				}
			}
			b.charge(hset...)
			unsets := make([]string, 0)
			if old != nil {
				for name := range old.fields {
					if _, present := newRecord.fields[name]; !present {
						unsets = append(unsets, name)
					}
				}
			}
			sort.Strings(unsets)
			if old != nil && old.place != nil && newRecord.place == nil {
				unsets = append(unsets, "place:"+entry.Table)
			}
			if len(unsets) != 0 {
				b.charge(append([]string{"HDEL", recordKey}, unsets...)...)
			}
			// Every changed member removes its old cell entry, even when only a
			// field changed, then adds its new cell entry if still placed.
			if old != nil && old.place != nil {
				b.charge("ZREM", memCellKey(step.Space, entry.Table, epoch, *old.place), id)
			}
			if newRecord.place != nil {
				score := newRecord.score
				if len(entry.Scores) != 0 {
					score = entry.Scores[i] // ZADD receives the validated original lexeme.
				}
				b.charge("ZADD", memCellKey(step.Space, entry.Table, epoch, *newRecord.place), score, id)
			}
		}
	}
	// A changed property is one HSET on its table's property hash at the
	// write epoch (amendment 2026-09-30, property, section 2).
	for _, entry := range step.Entries {
		if entry.Kind != "prop" || entry.Value == nil {
			continue
		}
		var old string
		present := false
		if e := pre.epochs[epoch]; e != nil && e.tables[entry.Table] != nil {
			old, present = e.tables[entry.Table].props[entry.Name]
		}
		if !present || old != *entry.Value {
			b.charge("HSET", memPropsKey(step.Space, entry.Table, epoch), entry.Name, *entry.Value)
		}
	}
	// Row mutations are batched by table and direction, up to 1,000 rows per
	// ZREM and 1,000 rank/row pairs per ZADD.
	for _, add := range []bool{false, true} {
		byTable := make(map[string][]struct {
			row  string
			rank Decimal
		})
		for _, key := range rowOrder {
			var beforeRank, afterRank Decimal
			if e := pre.epochs[epoch]; e != nil && e.tables[key.table] != nil {
				beforeRank = e.tables[key.table].rows[key.row]
			}
			if e := next.epochs[epoch]; e != nil && e.tables[key.table] != nil {
				afterRank = e.tables[key.table].rows[key.row]
			}
			if add && beforeRank == "" && afterRank != "" || !add && beforeRank != "" && afterRank == "" {
				byTable[key.table] = append(byTable[key.table], struct {
					row  string
					rank Decimal
				}{key.row, afterRank})
			}
		}
		for table, rows := range byTable {
			for start := 0; start < len(rows); start += 1000 {
				end := min(start+1000, len(rows))
				command := "ZREM"
				if add {
					command = "ZADD"
				}
				argv := []string{command, memTablePrefix(step.Space, table, epoch) + ":rows"}
				for _, row := range rows[start:end] {
					if add {
						argv = append(argv, string(row.rank))
					}
					argv = append(argv, row.row)
				}
				b.charge(argv...)
			}
		}
	}
	if next.active != pre.active {
		tables := make([]string, 0, len(next.defs))
		for table := range next.defs {
			tables = append(tables, table)
		}
		sort.Strings(tables)
		for _, table := range tables {
			fields := next.rawDefinition(table)
			names := make([]string, 0, len(fields))
			for name := range fields {
				names = append(names, name)
			}
			sort.Strings(names)
			argv := []string{"HSET", memTablePrefix(step.Space, table, epoch) + ":definition"}
			for _, name := range names {
				argv = append(argv, name, fields[name])
			}
			b.charge(argv...)
		}
		marker := next.rawEpochMarker(epoch)
		b.charge("HSET", step.Space+"sprint:epoch@"+string(epoch),
			"engine", marker["engine"], "n", marker["n"], "tables", marker["tables"])
		b.charge("HSET", next.epochKey, next.epochField, string(epoch))
	}
}

func (b *memWorkBudget) charge(argv ...string) {
	b.plannedCommands++
	for _, arg := range argv {
		b.plannedBytes += len(arg)
	}
}

func memTablePrefix(space, table string, epoch Decimal) string {
	prefix := space + "table:" + table
	if epoch != "0" {
		prefix += ":" + string(epoch)
	}
	return prefix
}

// memPropsKey is the table's property hash at one epoch, beside its rows key
// (amendment 2026-09-30, property, section 1).
func memPropsKey(space, table string, epoch Decimal) string {
	return memTablePrefix(space, table, epoch) + ":props"
}

// memPlanProp plans one prop or propguard entry (L1 contract amendment
// 2026-09-30, property, section 2). Both compare with the write epoch's
// pre-state, so a guard beside a write on the same pair reads the value from
// before the step. It returns that pre-state and whether the entry changes it.
func memPlanProp(preEpoch, workEpoch *memEpoch, entry Entry, index int, counts map[string]int, observed map[string]bool, b *memWorkBudget) (FieldValue, bool, error) {
	pre, work := preEpoch.tables[entry.Table], workEpoch.tables[entry.Table]
	if pre == nil || work == nil {
		return FieldValue{}, false, memRefusal("DRIFT", RefusalDetail{EntryIndex: memIndex(index), Table: entry.Table})
	}
	before, present := pre.props[entry.Name]
	if key := entry.Table + "\x00" + entry.Name; !observed[key] {
		// One field observation per distinct property, as the store's HGET.
		observed[key] = true
		b.fieldObservations++
		if present {
			b.fetchedBytes += len(before)
		}
		if b.fieldObservations > 768000 {
			return FieldValue{}, false, memRefusal("LIMIT", RefusalDetail{Budget: "field_observations"})
		}
		if b.fetchedBytes > 8<<20 {
			return FieldValue{}, false, memRefusal("LIMIT", RefusalDetail{Budget: "raw_fetched_bytes"})
		}
	}
	prior := FieldValue{Present: present, Value: before}
	if entry.Kind == "propguard" {
		if entry.Value == nil && present || entry.Value != nil && (!present || before != *entry.Value) {
			return prior, false, memRefusal("PROPGUARD", RefusalDetail{EntryIndex: memIndex(index), Table: entry.Table, Name: entry.Name})
		}
		return prior, false, nil
	}
	if entry.Value == nil {
		return prior, false, memRefusal("REQUEST", RefusalDetail{EntryIndex: memIndex(index), Table: entry.Table})
	}
	if present && before == *entry.Value {
		return prior, false, nil
	}
	if !present {
		n, seen := counts[entry.Table]
		if !seen {
			n = len(pre.props)
			b.cellProbes++ // the store's HLEN of the property hash
		}
		n++
		counts[entry.Table] = n
		if n > MaxPropsPerTable {
			return prior, false, memRefusal("LIMIT", RefusalDetail{EntryIndex: memIndex(index), Table: entry.Table,
				Budget: "properties", Limit: memInt64(MaxPropsPerTable), Actual: memInt64(int64(n))})
		}
	}
	if work.props == nil {
		work.props = make(map[string]string)
	}
	work.props[entry.Name] = *entry.Value
	return prior, true, nil
}

func memCellKey(space, table string, epoch Decimal, place memPlace) string {
	return memTablePrefix(space, table, epoch) + ":cell:" + place.row + ":" + place.col
}

func memOldField(r *memRecord, name string) (string, bool) {
	if r == nil {
		return "", false
	}
	v, ok := r.fields[name]
	return v, ok
}

func memIndex(i int) *int     { return &i }
func memInt64(i int64) *int64 { return &i }

func memUniqueStrings(values []string) int {
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		seen[value] = true
	}
	return len(seen)
}

func memCell(ref string, def *memTableDef, index int) (memPlace, error) {
	at := strings.LastIndexByte(ref, ':')
	if at <= 0 || at == len(ref)-1 {
		return memPlace{}, memRefusal("REQUEST", RefusalDetail{EntryIndex: memIndex(index), Cells: []string{ref}})
	}
	place := memPlace{row: ref[:at], col: ref[at+1:]}
	if !validMemRow(place.row) || !validMemColumn(place.col) {
		return memPlace{}, memRefusal("REQUEST", RefusalDetail{EntryIndex: memIndex(index), Cells: []string{ref}})
	}
	if !def.columns[place.col] {
		return memPlace{}, memRefusal("NOCOL", RefusalDetail{EntryIndex: memIndex(index), Table: def.name, Cells: []string{ref}})
	}
	return place, nil
}

func memPlanCount(t *memTableEpoch, def *memTableDef, entry Entry, index int) error {
	if len(entry.Cells) == 0 || len(entry.Cells) != len(entry.CountMax) {
		return memRefusal("REQUEST", RefusalDetail{EntryIndex: memIndex(index), Table: entry.Table})
	}
	// Read each distinct cell once, but apply every aligned max in input
	// order so the first failing cell agrees with the Redis planner.
	counts := make(map[string]int, len(entry.Cells))
	for j, cell := range entry.Cells {
		p, err := memCell(cell, def, index)
		if err != nil {
			return err
		}
		if t.rows[p.row] == "" {
			return memRefusal("NOROW", RefusalDetail{EntryIndex: memIndex(index), Table: entry.Table, Rows: []string{p.row}, Cells: []string{cell}})
		}
		count, seen := counts[cell]
		if !seen {
			count = len(t.cells[p.row][p.col])
			counts[cell] = count
		}
		if uint64(count) > entry.CountMax[j] {
			return memRefusal("CELLFULL", RefusalDetail{EntryIndex: memIndex(index), Table: entry.Table, Cells: []string{cell}})
		}
	}
	return nil
}

func memPlanRCount(t *memTableEpoch, def *memTableDef, entry Entry, index int) error {
	min, err := parseMemBound(entry.ScoreMin)
	if err != nil {
		return memRefusal("REQUEST", RefusalDetail{EntryIndex: memIndex(index), Table: entry.Table})
	}
	max, err := parseMemBound(entry.ScoreMax)
	if err != nil {
		return memRefusal("REQUEST", RefusalDetail{EntryIndex: memIndex(index), Table: entry.Table})
	}
	if entry.AtLeast == nil && entry.AtMost == nil {
		return memRefusal("REQUEST", RefusalDetail{EntryIndex: memIndex(index), Table: entry.Table})
	}
	if entry.AtLeast != nil && entry.AtMost != nil && *entry.AtLeast > *entry.AtMost {
		return memRefusal("REQUEST", RefusalDetail{EntryIndex: memIndex(index), Table: entry.Table})
	}
	seen := make(map[string]bool)
	var sum uint64
	for _, cell := range entry.Cells {
		if seen[cell] {
			continue
		}
		seen[cell] = true
		p, err := memCell(cell, def, index)
		if err != nil {
			return err
		}
		if t.rows[p.row] == "" {
			return memRefusal("NOROW", RefusalDetail{EntryIndex: memIndex(index), Table: entry.Table, Rows: []string{p.row}, Cells: []string{cell}})
		}
		for _, score := range t.cells[p.row][p.col] {
			f, err := parseMemScore(score)
			if err != nil {
				return memRefusal("DRIFT", RefusalDetail{EntryIndex: memIndex(index), Table: entry.Table, Cells: []string{cell}})
			}
			if memWithinBounds(f, min, max) {
				sum++
			}
		}
	}
	if entry.AtLeast != nil && sum < *entry.AtLeast || entry.AtMost != nil && sum > *entry.AtMost {
		return memRefusal("RANGECOUNT", RefusalDetail{EntryIndex: memIndex(index), Table: entry.Table, Cells: entry.Cells})
	}
	return nil
}

func memPlanMember(pre, next *memNamespace, preEpoch, workEpoch *memEpoch, rowOps map[string]map[string]*memRowOp, entry Entry, index, memberIndex int) (bool, error) {
	id := entry.IDs[memberIndex]
	def := next.defs[entry.Table]
	before := preEpoch.tables[entry.Table]
	work := workEpoch.tables[entry.Table]
	if before == nil || work == nil {
		return false, memRefusal("DRIFT", RefusalDetail{EntryIndex: memIndex(index), Table: entry.Table, IDs: []string{id}})
	}
	if entry.Kind == "create" {
		if _, _, exists := pre.recordTable(entry.Table, id); exists {
			return false, memRefusal("EXISTS", RefusalDetail{EntryIndex: memIndex(index), Table: entry.Table, IDs: []string{id}})
		}
		if len(entry.Scores) != len(entry.IDs) {
			return false, memRefusal("REQUEST", RefusalDetail{EntryIndex: memIndex(index), Table: entry.Table, IDs: []string{id}})
		}
		to, err := memCell(entry.To, def, index)
		if err != nil {
			return false, err
		}
		if err := memDestination(work, rowOps[entry.Table], entry.Table, to, index); err != nil {
			return false, err
		}
		if _, occupied := before.cells[to.row][to.col][id]; occupied {
			return false, memRefusal("DRIFT", RefusalDetail{EntryIndex: memIndex(index), Table: entry.Table, IDs: []string{id}})
		}
		f, err := parseMemScore(entry.Scores[memberIndex])
		if err != nil {
			return false, memRefusal("REQUEST", RefusalDetail{EntryIndex: memIndex(index), Table: entry.Table, IDs: []string{id}})
		}
		fields, err := memEffectiveFields(entry, memberIndex, index)
		if err != nil {
			return false, err
		}
		if len(fields) > 128 {
			return false, memRefusal("LIMIT", RefusalDetail{EntryIndex: memIndex(index), Table: entry.Table, IDs: []string{id}})
		}
		if work.records[id] != nil {
			return false, memRefusal("DRIFT", RefusalDetail{EntryIndex: memIndex(index), Table: entry.Table, IDs: []string{id}})
		}
		score := memScoreText(f)
		work.records[id] = &memRecord{epoch: entryEpoch(pre, next), revision: "1", place: &to, score: score, fields: fields}
		if next.recordEpoch[entry.Table] == nil {
			next.recordEpoch[entry.Table] = make(map[string]Decimal)
		}
		next.recordEpoch[entry.Table][id] = entryEpoch(pre, next)
		ensureMemCell(work, to.row, to.col)[id] = score
		return true, nil
	}
	r := before.records[id]
	if r == nil {
		if _, epoch, exists := pre.recordTable(entry.Table, id); exists && epoch != entryEpoch(pre, next) {
			return false, memRefusal("MEMBEREPOCH", RefusalDetail{EntryIndex: memIndex(index), Table: entry.Table, IDs: []string{id}})
		}
		return false, memRefusal("MISSING", RefusalDetail{EntryIndex: memIndex(index), Table: entry.Table, IDs: []string{id}})
	}
	if r.epoch != entryEpoch(pre, next) {
		return false, memRefusal("MEMBEREPOCH", RefusalDetail{EntryIndex: memIndex(index), Table: entry.Table, IDs: []string{id}})
	}
	if r.revision == "0" || len(r.fields) > 128 {
		return false, memRefusal("DRIFT", RefusalDetail{EntryIndex: memIndex(index), Table: entry.Table, IDs: []string{id}})
	}
	from, err := memCell(entry.From, def, index)
	if err != nil {
		return false, err
	}
	if before.rows[from.row] == "" {
		return false, memRefusal("NOROW", RefusalDetail{EntryIndex: memIndex(index), Table: entry.Table, Rows: []string{from.row}})
	}
	if r.place == nil || *r.place != from {
		return false, memRefusal("PLACE", RefusalDetail{EntryIndex: memIndex(index), Table: entry.Table, IDs: []string{id}, Cells: []string{entry.From}})
	}
	cellScore, ok := before.cells[from.row][from.col][id]
	if !ok {
		return false, memRefusal("DRIFT", RefusalDetail{EntryIndex: memIndex(index), Table: entry.Table, IDs: []string{id}, Cells: []string{entry.From}})
	}
	stored, err := parseMemScore(cellScore)
	if err != nil {
		return false, memRefusal("DRIFT", RefusalDetail{EntryIndex: memIndex(index), Table: entry.Table, IDs: []string{id}})
	}
	recordScore, err := parseMemScore(r.score)
	if err != nil || stored != recordScore {
		return false, memRefusal("DRIFT", RefusalDetail{EntryIndex: memIndex(index), Table: entry.Table, IDs: []string{id}})
	}
	if len(entry.Revs) != 0 && entry.Revs[memberIndex] != r.revision {
		return false, memRefusal("REVISION", RefusalDetail{EntryIndex: memIndex(index), Table: entry.Table, IDs: []string{id}})
	}
	if entry.Kind == "guard" {
		return false, nil
	}
	to := from
	if entry.Kind == "move" && entry.To != "" {
		to, err = memCell(entry.To, def, index)
		if err != nil {
			return false, err
		}
		if to != from {
			if err := memDestination(work, rowOps[entry.Table], entry.Table, to, index); err != nil {
				return false, err
			}
			if _, occupied := before.cells[to.row][to.col][id]; occupied {
				return false, memRefusal("DRIFT", RefusalDetail{EntryIndex: memIndex(index), Table: entry.Table, IDs: []string{id}})
			}
		}
	}
	newScore := stored
	if len(entry.Scores) != 0 {
		newScore, err = parseMemScore(entry.Scores[memberIndex])
		if err != nil {
			return false, memRefusal("REQUEST", RefusalDetail{EntryIndex: memIndex(index), Table: entry.Table, IDs: []string{id}})
		}
	}
	set, err := memEffectiveFields(entry, memberIndex, index)
	if err != nil {
		return false, err
	}
	wr := work.records[id]
	if wr == nil {
		return false, memRefusal("DRIFT", RefusalDetail{EntryIndex: memIndex(index), Table: entry.Table, IDs: []string{id}})
	}
	fieldChange := false
	for name, value := range set {
		old, exists := wr.fields[name]
		if !exists || old != value {
			fieldChange = true
			wr.fields[name] = value
		}
	}
	seenUnset := make(map[string]bool)
	for _, name := range entry.Unset {
		if seenUnset[name] {
			continue
		}
		seenUnset[name] = true
		if _, exists := set[name]; exists {
			return false, memRefusal("FIELDOVERLAP", RefusalDetail{EntryIndex: memIndex(index), Table: entry.Table, IDs: []string{id}})
		}
		if _, exists := wr.fields[name]; exists {
			fieldChange = true
			delete(wr.fields, name)
		}
	}
	if len(wr.fields) > 128 {
		return false, memRefusal("LIMIT", RefusalDetail{EntryIndex: memIndex(index), Table: entry.Table, IDs: []string{id}})
	}
	placementChange := entry.Kind == "remove" || to != from
	scoreChange := entry.Kind != "remove" && newScore != stored
	if !placementChange && !scoreChange && !fieldChange {
		return false, nil
	}
	// A changed stay rewrites its placement and therefore requires the row to
	// survive the step. An unchanged stay emits no destination write; final row
	// occupancy instead decides whether its deletion can proceed.
	if entry.Kind == "move" && to == from {
		if err := memDestination(work, rowOps[entry.Table], entry.Table, to, index); err != nil {
			return false, err
		}
	}
	rev, ok := memNextDecimal(wr.revision)
	if !ok {
		return false, memRefusal("OVERFLOW", RefusalDetail{EntryIndex: memIndex(index), Table: entry.Table, IDs: []string{id}})
	}
	wr.revision = rev
	if placementChange || scoreChange {
		// ZREM precedes ZADD even when the destination is the source cell.
		delete(work.cells[from.row][from.col], id)
		if len(work.cells[from.row][from.col]) == 0 {
			delete(work.cells[from.row], from.col)
			if len(work.cells[from.row]) == 0 {
				delete(work.cells, from.row)
			}
		}
	}
	if entry.Kind == "remove" {
		wr.place = nil
		wr.score = ""
	} else {
		wr.place = &to
		wr.score = memScoreText(newScore)
		if placementChange || scoreChange {
			ensureMemCell(work, to.row, to.col)[id] = wr.score
		}
	}
	return true, nil
}

func entryEpoch(pre, next *memNamespace) Decimal {
	if next.active != pre.active {
		return next.active
	}
	return pre.active
}

func memDestination(t *memTableEpoch, ops map[string]*memRowOp, table string, to memPlace, index int) error {
	if op := ops[to.row]; op != nil && op.del {
		return memRefusal("ROWCONFLICT", RefusalDetail{EntryIndex: memIndex(index), Table: table, Rows: []string{to.row}})
	}
	if t.rows[to.row] == "" {
		return memRefusal("NOROW", RefusalDetail{EntryIndex: memIndex(index), Table: table, Rows: []string{to.row}})
	}
	return nil
}

func memEffectiveFields(entry Entry, memberIndex, index int) (map[string]string, error) {
	fields := cloneFields(entry.Set)
	if len(entry.Each) != 0 {
		if len(entry.Each) != len(entry.IDs) {
			return nil, memRefusal("REQUEST", RefusalDetail{EntryIndex: memIndex(index), Table: entry.Table})
		}
		for k, v := range entry.Each[memberIndex] {
			fields[k] = v
		}
	}
	if len(fields) > 128 || len(entry.Unset) > 128 || len(entry.BeforeFields) > 128 {
		return nil, memRefusal("LIMIT", RefusalDetail{EntryIndex: memIndex(index), Table: entry.Table})
	}
	names := make([]string, 0, len(fields)+len(entry.Unset)+len(entry.BeforeFields))
	for name, value := range fields {
		if len(value) > 64<<10 {
			return nil, memRefusal("LIMIT", RefusalDetail{EntryIndex: memIndex(index), Table: entry.Table})
		}
		names = append(names, name)
	}
	names = append(names, entry.Unset...)
	names = append(names, entry.BeforeFields...)
	sort.Strings(names)
	for _, name := range names {
		if !validMemName(name) {
			return nil, memRefusal("REQUEST", RefusalDetail{EntryIndex: memIndex(index), Table: entry.Table})
		}
		if name == "epoch" || name == "revision" || strings.HasPrefix(name, "place:") {
			return nil, memRefusal("FIELDNAME", RefusalDetail{EntryIndex: memIndex(index), Table: entry.Table})
		}
	}
	for _, name := range entry.Unset {
		if _, ok := fields[name]; ok {
			return nil, memRefusal("FIELDOVERLAP", RefusalDetail{EntryIndex: memIndex(index), Table: entry.Table})
		}
	}
	return fields, nil
}
