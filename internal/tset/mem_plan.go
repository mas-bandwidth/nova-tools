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
	if record == nil {
		return nil
	}
	b.fetchedBytes += len(record.epoch) + len(record.revision) + len(record.score)
	if record.place != nil {
		b.fetchedBytes += len(record.place.row) + len(record.place.col)
	}
	for name := range selected {
		b.fieldObservations++
		b.fetchedBytes += len(name) // HKEY/HMGET and field-name accounting
		if value, ok := record.fields[name]; ok {
			b.fetchedBytes += len(value)
		}
	}
	if b.fieldObservations > 768000 {
		return memRefusal("LIMIT", RefusalDetail{Budget: "field_observations"})
	}
	if b.fetchedBytes > 8<<20 {
		return memRefusal("LIMIT", RefusalDetail{Budget: "raw_fetched_bytes"})
	}
	return nil
}

func (m *Mem) planStep(ctx context.Context, pre, next *memSpace, step Step) (Reply, error) {
	writeEpoch := step.Epoch
	advance := false
	if len(step.Entries) != 0 && step.Entries[0].Kind == "advance" {
		advance = true
		if step.Entries[0].AdvanceFrom != step.Epoch {
			return Reply{}, memEpochRefusal("ADVANCE", pre.active)
		}
		var ok bool
		writeEpoch, ok = memNextDecimal(step.Epoch)
		if !ok {
			return Reply{}, memRefusal("OVERFLOW", RefusalDetail{EntryIndex: memIndex(0)})
		}
	}
	for i, entry := range step.Entries {
		if entry.Kind == "advance" && (!advance || i != 0) {
			return Reply{}, memRefusal("ADVANCE", RefusalDetail{EntryIndex: memIndex(i), ActiveEpoch: pre.active})
		}
	}
	if pre.epochs[step.Epoch] == nil {
		return Reply{}, memRefusal("DRIFT", RefusalDetail{})
	}
	if advance {
		if len(next.receipts[writeEpoch]) != 0 {
			return Reply{}, memRefusal("DRIFT", RefusalDetail{EntryIndex: memIndex(0)})
		}
		if existing := next.epochs[writeEpoch]; existing != nil && !memEpochEmpty(existing) {
			return Reply{}, memRefusal("DRIFT", RefusalDetail{EntryIndex: memIndex(0)})
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
	seenID := make(map[string]bool)
	candidates, guarded, changed := 0, 0, 0
	budget := memWorkBudget{}
	for i, entry := range step.Entries {
		if err := ctx.Err(); err != nil {
			return Reply{}, err
		}
		switch entry.Kind {
		case "advance", "rows":
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
			didChange, err := memPlanMember(pre, next, preEpoch, workEpoch, rowOps, entry, i, j)
			if err != nil {
				return Reply{}, err
			}
			if didChange {
				changed++
				changedPer[i]++
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
	reply := Reply{Status: "ok", EpochBefore: step.Epoch, EpochAfter: writeEpoch,
		Changed: changed, Guarded: guarded, ChangedPerEntry: changedPer,
		FirstSeq: "0", LastSeq: "0", Lines: 0, Result: step.Result, Replay: false}
	budget.planWrites(pre, next, step, writeEpoch, rowOrder)
	if step.Op != nil {
		r := memReceipt{IntentDigest: intentDigest(*step.Intent), Status: reply.Status,
			EpochBefore: reply.EpochBefore, EpochAfter: reply.EpochAfter,
			FirstSeq: reply.FirstSeq, LastSeq: reply.LastSeq,
			Changed: reply.Changed, Result: reply.Result}
		encoded := encodeMemReceipt(r)
		if len(encoded) > maxReceiptBytes {
			return Reply{}, memRefusal("LIMIT", RefusalDetail{Budget: "receipt_bytes", Limit: memInt64(maxReceiptBytes), Actual: memInt64(int64(len(encoded)))})
		}
		budget.charge("HSET", step.Space+"sprint:done@"+string(step.Epoch), *step.Op, string(encoded))
	}
	if budget.plannedBytes > 8<<20 {
		return Reply{}, memRefusal("LIMIT", RefusalDetail{Budget: "planned_argv_bytes", Limit: memInt64(8 << 20), Actual: memInt64(int64(budget.plannedBytes))})
	}
	if budget.plannedCommands > 65536 {
		return Reply{}, memRefusal("LIMIT", RefusalDetail{Budget: "planned_commands", Limit: memInt64(65536), Actual: memInt64(int64(budget.plannedCommands))})
	}
	// L1-only profile has no L2 semantic log. Counters are intentionally limited
	// to work this independent model can measure without Redis command choices.
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
	}{candidates, guarded, changed, len(rowOrder), budget.fieldObservations, budget.cellProbes,
		budget.fetchedBytes, budget.plannedBytes, budget.plannedCommands})
	reply.Counters = counters
	return reply, nil
}

func memEpochEmpty(e *memEpoch) bool {
	for _, t := range e.tables {
		if len(t.rows) != 0 || len(t.records) != 0 {
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

func (b *memWorkBudget) planWrites(pre, next *memSpace, step Step, epoch Decimal, rowOrder []struct{ table, row string }) {
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

func memPlanMember(pre, next *memSpace, preEpoch, workEpoch *memEpoch, rowOps map[string]map[string]*memRowOp, entry Entry, index, memberIndex int) (bool, error) {
	id := entry.IDs[memberIndex]
	def := next.defs[entry.Table]
	before := preEpoch.tables[entry.Table]
	work := workEpoch.tables[entry.Table]
	if before == nil || work == nil {
		return false, memRefusal("DRIFT", RefusalDetail{EntryIndex: memIndex(index), Table: entry.Table, IDs: []string{id}})
	}
	if entry.Kind == "create" {
		for _, e := range pre.epochs {
			if e.tables[entry.Table] != nil && e.tables[entry.Table].records[id] != nil {
				return false, memRefusal("EXISTS", RefusalDetail{EntryIndex: memIndex(index), Table: entry.Table, IDs: []string{id}})
			}
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
		ensureMemCell(work, to.row, to.col)[id] = score
		return true, nil
	}
	r := before.records[id]
	if r == nil {
		for epoch, e := range pre.epochs {
			if epoch != entryEpoch(pre, next) && e.tables[entry.Table] != nil && e.tables[entry.Table].records[id] != nil {
				return false, memRefusal("MEMBEREPOCH", RefusalDetail{EntryIndex: memIndex(index), Table: entry.Table, IDs: []string{id}})
			}
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

func entryEpoch(pre, next *memSpace) Decimal {
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
