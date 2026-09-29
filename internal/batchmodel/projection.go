package batchmodel

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
)

// FiniteBaseline is frozen immediately after the owned Redis fixture is built.
// The ordinary setup writes are represented by offsets, never discarded or
// recomputed after a batch. BatchReplayInit has zero counters.
type FiniteBaseline struct {
	TableRevision  string
	MemberRevision map[string]string
	Operations     uint64
	Receipts       uint64
}

// ProjectionControl records only trace-control facts that do not live in
// Redis. Accepted actions have already had their complete physical receipts
// checked against independent snapshots. The current action enters Accepted
// only after a newly accepted result; retry/refusal retains the prior list.
type ProjectionControl struct {
	SeenEpoch string
	Accepted  []AcceptedEvidence
	// NonBatchEvents are independently captured ordinary stream entries (for
	// example the epoch-2 bind). They are physical events, not model receipts.
	NonBatchEvents []StreamEvent
	Outcome        string
	Attempt        *BatchAction
	Returned       *Receipt
}

// AcceptedEvidence keeps the original prestate needed to decode a sparse
// receipt again. The durable result and event must still be present, byte for
// byte, in every later captured Redis image. This prevents a count-only trace
// from inventing operations or receipt contents.
type AcceptedEvidence struct {
	Action  BatchAction
	Request Request
	Before  Snapshot
	Receipt Receipt
	Event   StreamEvent
	Record  OperationRecord
}

// ProjectFinite maps the two-epoch, three-member owned-store observation into
// all 18 BatchReplayState components. The finite instance has virtual epoch-2
// table identity before that generation is materialized, as BatchReplayInit
// does. Unsupported records, rows, IDs, scores and fields fail closed.
func ProjectFinite(s Snapshot, base FiniteBaseline, control ProjectionControl) (ModelState, error) {
	var out ModelState
	if s.Image == nil || s.Definitions == nil || s.Rows == nil || s.RowScores == nil || s.RowFields == nil || s.RevisionFields == nil || s.EpochFields == nil || s.Bindings == nil || s.CellTypes == nil || s.CellMembers == nil || s.Members == nil {
		return out, fmt.Errorf("incomplete physical observation")
	}
	if s.StreamInfo.EntriesAdded != s.Receipts || s.StreamInfo.MaxDeletedID != "0-0" {
		return out, fmt.Errorf("change-stream hidden history differs from finite fixture")
	}
	lastID := "0-0"
	if len(s.Events) > 0 {
		lastID = s.Events[len(s.Events)-1].ID
	}
	if s.StreamInfo.LastGeneratedID != lastID {
		return out, fmt.Errorf("change-stream last generated ID differs from retained tail")
	}
	if len(s.Bindings) != 0 {
		return out, fmt.Errorf("bound cells outside this unbound replay fixture")
	}
	if s.Epoch != "1" && s.Epoch != "2" {
		return out, fmt.Errorf("active epoch %q outside finite model", s.Epoch)
	}
	if control.SeenEpoch != "1" && control.SeenEpoch != "2" {
		return out, fmt.Errorf("missing finite writer observation epoch")
	}
	if s.Operations != base.Operations+uint64(len(control.Accepted)) || s.Receipts != base.Receipts+uint64(len(control.Accepted)+len(control.NonBatchEvents)) {
		return out, fmt.Errorf("physical operation/receipt counts differ from validated history")
	}
	if base.Operations != 0 || len(s.OperationRecords) != len(control.Accepted) || len(s.Events) != int(s.Receipts) {
		return out, fmt.Errorf("physical operation/event history is incomplete")
	}
	remaining := map[string]StreamEvent{}
	for _, event := range control.NonBatchEvents {
		if event.ID == "" || event.Fields["verb"] == "apply" {
			return out, fmt.Errorf("invalid non-batch stream evidence")
		}
		if _, duplicate := remaining[event.ID]; duplicate {
			return out, fmt.Errorf("duplicate non-batch stream evidence")
		}
		remaining[event.ID] = event
	}
	for _, e := range control.Accepted {
		if _, duplicate := remaining[e.Event.ID]; duplicate {
			return out, fmt.Errorf("duplicate accepted stream evidence")
		}
		remaining[e.Event.ID] = e.Event
	}
	if len(remaining) != len(control.Accepted)+len(control.NonBatchEvents) {
		return out, fmt.Errorf("duplicate stream evidence")
	}
	acceptedIndex := 0
	for _, observed := range s.Events[int(base.Receipts):] {
		wanted, ok := remaining[observed.ID]
		if !ok || !reflect.DeepEqual(observed, wanted) {
			return out, fmt.Errorf("unvalidated or changed stream event %s", observed.ID)
		}
		delete(remaining, observed.ID)
		if observed.Fields["verb"] == "apply" {
			if acceptedIndex >= len(control.Accepted) || observed.ID != control.Accepted[acceptedIndex].Event.ID {
				return out, fmt.Errorf("batch receipt stream order differs")
			}
			acceptedIndex++
		}
	}
	if len(remaining) != 0 || acceptedIndex != len(control.Accepted) {
		return out, fmt.Errorf("validated stream event omitted")
	}
	for _, e := range control.Accepted {
		epoch, err := decimal(e.Request.Epoch)
		if err != nil {
			return out, err
		}
		key := ntable.EpochPrefix(e.Request.Table, epoch) + ":op:" + e.Request.OperationID
		stored, ok := s.OperationRecords[key]
		if !ok || !reflect.DeepEqual(stored, e.Record) || !sameOperation(stored, e.Request, e.Receipt) {
			return out, fmt.Errorf("durable operation %s differs from validated evidence", key)
		}
		var reply any
		if err := json.Unmarshal(stored.ResultJSON, &reply); err != nil {
			return out, fmt.Errorf("durable result decode: %w", err)
		}
		fromRecord, err := DecodeAcceptedReply(reply, e.Request, e.Before)
		if err != nil || !reflect.DeepEqual(*fromRecord, e.Receipt) {
			return out, fmt.Errorf("durable result differs from validated receipt: %v", err)
		}
		if e.Event.ID != e.Receipt.StreamID || e.Event.Fields["verb"] != "apply" {
			return out, fmt.Errorf("validated event identity differs")
		}
		streamReply := []any{"OK", []any{"RECEIPT", e.Event.ID, e.Event.Fields["epoch"], e.Event.Fields["rev_before"], e.Event.Fields["rev_after"], e.Event.Fields["outcome"], e.Event.Fields["batch_delta"]}}
		fromStream, err := DecodeAcceptedReply(streamReply, e.Request, e.Before)
		if err != nil || !reflect.DeepEqual(*fromStream, e.Receipt) {
			return out, fmt.Errorf("change stream differs from validated receipt: %v", err)
		}
	}
	tables := []Expr{finiteTable("1"), finiteTable("2")}
	out.Values[0] = Set(tables...)
	rows := []Expr{}
	data := []Expr{}
	cellKeys := []Expr{}
	cellTypes := []Expr{}
	permissions := []Expr{}
	for _, epoch := range []string{"1", "2"} {
		def := s.Definitions[epoch]
		// Epoch advance precedes the writer's refresh and shape declaration.
		// The model already has the epoch-2 identity at this point, while
		// Redis has no epoch-2 definition until SecondBind.
		unshapedSecond := epoch == "2" && len(def) == 0 && (control.Outcome == "advance" || control.Outcome == "refresh")
		if epoch == s.Epoch && def["_present"] != "1" && !unshapedSecond {
			return out, fmt.Errorf("active epoch %s lacks live table definition", epoch)
		}
		if len(def) != 0 {
			if err := checkFiniteDefinition(def); err != nil {
				return out, fmt.Errorf("epoch %s definition: %w", epoch, err)
			}
			if err := checkFiniteRows(s, epoch); err != nil {
				return out, err
			}
		}
		for _, row := range []string{"r1", "r2"} {
			if len(s.RowFields[epoch][row]) != 0 {
				return out, fmt.Errorf("epoch %s row %s has unmodelled metadata", epoch, row)
			}
		}
		seenRows := map[string]bool{}
		for _, row := range s.Rows[epoch] {
			if row != "r1" && row != "r2" || seenRows[row] {
				return out, fmt.Errorf("unmodelled or duplicate row %q", row)
			}
			seenRows[row] = true
			rows = append(rows, Tuple(finiteTable(epoch), String(row)))
		}
		if len(def) == 0 && len(s.Rows[epoch]) != 0 {
			return out, fmt.Errorf("epoch %s has rows without definition", epoch)
		}
		for _, row := range []string{"r1", "r2"} {
			for _, col := range []string{"c1", "c2"} {
				cell := Cell{Table: "t1", Epoch: epoch, Row: row, Column: col}
				ce, _ := cell.expr()
				kind, ok := s.CellTypes[cell]
				if !ok {
					return out, fmt.Errorf("cell %v not observed", cell)
				}
				mode := "zset"
				if kind != "zset" && kind != "none" {
					mode = "wrong"
				}
				cellKeys = append(cellKeys, ce)
				cellTypes = append(cellTypes, String(mode))
				permissions = append(permissions, Boolean(true))
				members, ok := s.CellMembers[cell]
				if !ok {
					return out, fmt.Errorf("cell %v members not observed", cell)
				}
				if mode == "wrong" && len(members) != 0 {
					return out, fmt.Errorf("wrong-type cell reported members")
				}
				for id, score := range members {
					if !finiteMember(id) {
						return out, fmt.Errorf("unmodelled cell member %q", id)
					}
					n, err := modelScore(score)
					if err != nil {
						return out, err
					}
					data = append(data, Tuple(ce, String(id), Number(n)))
				}
			}
		}
	}
	out.Values[1] = Set(rows...)
	out.Values[2] = Set() // this fixture has no bound cells
	out.Values[3] = Set(data...)
	memberKeys := []Expr{}
	places := []Expr{}
	revisions := []Expr{}
	fields := []Expr{}
	present := []Expr{}
	for _, id := range []string{"m1", "m2", "m3"} {
		m, ok := s.Members[id]
		if !ok {
			return out, fmt.Errorf("member %s not observed", id)
		}
		if err := checkMember(m); err != nil {
			return out, err
		}
		if m.Exists {
			present = append(present, String(id))
		}
		baseRev, ok := base.MemberRevision[id]
		if !ok {
			return out, fmt.Errorf("member %s baseline missing", id)
		}
		rev, err := ProjectRevision(m.Revision, baseRev)
		if err != nil {
			return out, err
		}
		if rev > 3 {
			return out, fmt.Errorf("member %s exceeds model revision bound", id)
		}
		revisions = append(revisions, Number(rev))
		memberKeys = append(memberKeys, String(id))
		byTable := []Expr{}
		for _, epoch := range []string{"1", "2"} {
			value := modelSymbol("NoPlace")
			if m.Place != nil && m.Epoch == epoch {
				cell := Cell{Table: "t1", Epoch: epoch, Row: m.Place.Row, Column: m.Place.Column}
				if cell.Row != "r1" && cell.Row != "r2" || cell.Column != "c1" && cell.Column != "c2" {
					return out, fmt.Errorf("member %s has unmodelled placement", id)
				}
				value, _ = cell.expr()
			}
			byTable = append(byTable, value)
		}
		places = append(places, Function(tables, byTable))
		for name := range m.Fields {
			if name != "status" && name != "token" {
				return out, fmt.Errorf("member %s has unmodelled field %s", id, name)
			}
		}
		fieldValues := []Expr{}
		for _, name := range []string{"status", "token"} {
			value, ok := m.Fields[name]
			if !ok {
				fieldValues = append(fieldValues, modelSymbol("NoValue"))
			} else {
				if err := modelValue(value); err != nil {
					return out, err
				}
				fieldValues = append(fieldValues, String(value))
			}
		}
		fields = append(fields, Function([]Expr{String("status"), String("token")}, fieldValues))
	}
	out.Values[4] = Function(memberKeys, places)
	active, _ := decimal(s.Epoch)
	out.Values[5] = Number(active)
	seen, _ := decimal(control.SeenEpoch)
	out.Values[6] = Function([]Expr{String("w1")}, []Expr{Number(seen)})
	out.Values[7] = Set(present...)
	out.Values[8] = Function(memberKeys, revisions)
	out.Values[9] = Function(memberKeys, fields)
	tableRevs := []Expr{}
	for _, epoch := range []string{"1", "2"} {
		rev := uint64(0)
		if raw := s.Definitions[epoch]["_revision"]; raw != "" {
			var err error
			rev, err = ProjectRevision(raw, base.TableRevision)
			if err != nil {
				return out, err
			}
		}
		if rev > 3 {
			return out, fmt.Errorf("epoch %s table revision exceeds model bound", epoch)
		}
		tableRevs = append(tableRevs, Number(rev))
	}
	out.Values[10] = Function(tables, tableRevs)
	operations := []Expr{}
	receipts := []Expr{}
	for _, e := range control.Accepted {
		r, err := observedReceiptExpr(e, base)
		if err != nil {
			return out, err
		}
		epoch, _ := decimal(e.Request.Epoch)
		o := Record(map[string]Expr{"key": Tuple(finiteTable(e.Request.Epoch), Number(epoch), String(e.Record.OperationID)),
			"bytes": String(hexBytes(e.Record.Canonical)), "digest": String(e.Record.Digest), "result": r})
		operations = append(operations, o)
		receipts = append(receipts, r)
	}
	out.Values[11] = Set(operations...)
	out.Values[12] = Tuple(receipts...)
	if control.Outcome == "" {
		return out, fmt.Errorf("missing model outcome")
	}
	out.Values[13] = String(control.Outcome)
	out.Values[14] = String("none")
	if control.Returned != nil {
		found := false
		for _, e := range control.Accepted {
			if reflect.DeepEqual(e.Receipt, *control.Returned) {
				r, err := observedReceiptExpr(e, base)
				if err != nil {
					return out, err
				}
				out.Values[14] = r
				found = true
				break
			}
		}
		if !found {
			return out, fmt.Errorf("returned receipt is absent from validated history")
		}
	}
	out.Values[15] = String("none")
	if control.Attempt != nil {
		a, err := control.Attempt.ValueExpr()
		if err != nil {
			return out, err
		}
		out.Values[15] = a
	}
	out.Values[16] = Function(cellKeys, cellTypes)
	out.Values[17] = Function(cellKeys, permissions)
	if _, err := out.TLA(); err != nil {
		return ModelState{}, err
	}
	return out, nil
}

func checkFiniteRows(s Snapshot, epoch string) error {
	rows := s.Rows[epoch]
	if len(rows) != 2 || rows[0] != "r1" || rows[1] != "r2" || len(s.RowScores[epoch]) != 2 || s.RowScores[epoch]["r1"] != "1" || s.RowScores[epoch]["r2"] != "2" {
		return fmt.Errorf("epoch %s row order or scores differ from finite fixture", epoch)
	}
	return nil
}

func checkFiniteDefinition(def map[string]string) error {
	var expected map[string]string
	if err := json.Unmarshal([]byte(finiteFields), &expected); err != nil {
		return err
	}
	if len(def) != len(expected)+2 || def["_present"] != "1" || def["_revision"] == "" {
		return fmt.Errorf("definition differs from finite fixture shape")
	}
	for key, value := range expected {
		if def[key] != value {
			return fmt.Errorf("definition field %s differs from fixture", key)
		}
	}
	if _, err := decimal(def["_revision"]); err != nil {
		return err
	}
	return nil
}

func finiteTable(epoch string) Expr { n, _ := decimal(epoch); return Tuple(String("t1"), Number(n)) }
func finiteMember(id string) bool   { return id == "m1" || id == "m2" || id == "m3" }
func hexBytes(raw []byte) string    { return hex.EncodeToString(raw) }

// observedReceiptExpr renders every receipt component as a literal derived
// from the strict wire decode and independent snapshots. It must never call
// the model's Receipt(q) operator in an observation: that operator reads the
// current model prestate, which differs after an accepted action.
func observedReceiptExpr(e AcceptedEvidence, base FiniteBaseline) (Expr, error) {
	r := e.Receipt
	epoch, err := decimal(r.Epoch)
	if err != nil {
		return Expr{}, err
	}
	beforeRev, err := ProjectRevision(r.BeforeRevision, base.TableRevision)
	if err != nil {
		return Expr{}, err
	}
	afterRev, err := ProjectRevision(r.AfterRevision, base.TableRevision)
	if err != nil {
		return Expr{}, err
	}
	selected := []Expr{}
	changed := []Expr{}
	keys := []Expr{}
	deltas := []Expr{}
	for _, d := range r.Members {
		if !finiteMember(d.ID) {
			return Expr{}, fmt.Errorf("receipt member %s outside finite model", d.ID)
		}
		memberBase, ok := base.MemberRevision[d.ID]
		if !ok {
			return Expr{}, fmt.Errorf("missing member baseline %s", d.ID)
		}
		br, err := ProjectRevision(d.Before.Revision, memberBase)
		if err != nil {
			return Expr{}, err
		}
		ar, err := ProjectRevision(d.After.Revision, memberBase)
		if err != nil {
			return Expr{}, err
		}
		bp, err := memberPlaceExpr(r.Epoch, d.Before)
		if err != nil {
			return Expr{}, err
		}
		ap, err := memberPlaceExpr(r.Epoch, d.After)
		if err != nil {
			return Expr{}, err
		}
		bs, err := memberScoreExpr(d.Before)
		if err != nil {
			return Expr{}, err
		}
		as, err := memberScoreExpr(d.After)
		if err != nil {
			return Expr{}, err
		}
		bf, err := memberFieldsExpr(d.Before)
		if err != nil {
			return Expr{}, err
		}
		af, err := memberFieldsExpr(d.After)
		if err != nil {
			return Expr{}, err
		}
		selected = append(selected, String(d.ID))
		keys = append(keys, String(d.ID))
		if d.Before.Exists != d.After.Exists || d.Before.Epoch != d.After.Epoch || !reflect.DeepEqual(d.Before.Place, d.After.Place) || !reflect.DeepEqual(nonNil(d.Before.Fields), nonNil(d.After.Fields)) {
			changed = append(changed, String(d.ID))
		}
		deltas = append(deltas, Record(map[string]Expr{"beforePlace": bp, "afterPlace": ap, "beforeRevision": Number(br), "afterRevision": Number(ar),
			"beforeScore": bs, "afterScore": as, "beforeFields": bf, "afterFields": af}))
	}
	return Record(map[string]Expr{
		"key": Tuple(finiteTable(r.Epoch), Number(epoch), String(r.OperationID)), "bytes": String(hexBytes(e.Request.Canonical)), "digest": String(r.Digest), "actor": String(r.Actor),
		"beforeRevision": Number(beforeRev), "afterRevision": Number(afterRev), "changed": Set(changed...), "selected": Set(selected...), "delta": Function(keys, deltas),
		"changedCount": Number(r.ChangedCount), "guardCount": Number(r.GuardCount), "selectedCount": Number(r.SelectedCount), "kind": String(r.Kind),
	}), nil
}

func memberPlaceExpr(epoch string, m Member) (Expr, error) {
	if m.Place == nil {
		return modelSymbol("NoPlace"), nil
	}
	cell := Cell{Table: "t1", Epoch: epoch, Row: m.Place.Row, Column: m.Place.Column}
	if cell.Row != "r1" && cell.Row != "r2" || cell.Column != "c1" && cell.Column != "c2" {
		return Expr{}, fmt.Errorf("receipt place outside finite model")
	}
	return cell.expr()
}
func memberScoreExpr(m Member) (Expr, error) {
	if m.Place == nil {
		return modelSymbol("NoScore"), nil
	}
	score, err := modelScore(m.Place.Score)
	if err != nil {
		return Expr{}, err
	}
	return Number(score), nil
}
func memberFieldsExpr(m Member) (Expr, error) {
	values := []Expr{}
	for _, name := range []string{"status", "token"} {
		v, ok := m.Fields[name]
		if !ok {
			values = append(values, modelSymbol("NoValue"))
		} else {
			if err := modelValue(v); err != nil {
				return Expr{}, err
			}
			values = append(values, String(v))
		}
	}
	for k := range m.Fields {
		if k != "status" && k != "token" {
			return Expr{}, fmt.Errorf("receipt field %s outside finite model", k)
		}
	}
	return Function([]Expr{String("status"), String("token")}, values), nil
}
