package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/redis/go-redis/v9"
)

// The twin: `--redis mem:<file>` (or NOVA_SPRINT_REDIS=mem:<file>) runs every
// table verb against the in-memory store (store.Mem), loaded from the file
// before the verb and saved to it after, so table operations can be tested and
// verified without a Redis server.

const twinPrefix = "mem:"

// isTwin says an address names a twin: mem, or mem:<file>.
func isTwin(addr string) bool {
	return addr == "mem" || strings.HasPrefix(addr, twinPrefix)
}

// twin is one open twin file: the store and the bytes last loaded or saved,
// so a verb that changed nothing writes nothing.
type twin struct {
	path string
	mem  *store.Mem
	last []byte
	mu   sync.Mutex
}

var dummyClient = redis.NewClient(&redis.Options{})

// twinBackend opens the twin file once per process, loading it when it is
// there: a file that is not a twin snapshot is refused, never overwritten.
func (app *application) twinBackend(addr string) (*twin, error) {
	if app.twins == nil {
		app.twins = map[string]*twin{}
	}
	if t, ok := app.twins[addr]; ok {
		return t, nil
	}
	path := strings.TrimPrefix(addr, twinPrefix)
	if addr == "mem" || path == "" {
		return nil, errors.New("a twin is a file: --redis mem:<file> (a store in memory alone would be gone when this command ends); the twin is for learning and tests, not for a fleet")
	}
	t := &twin{path: path, mem: store.NewMem()}
	doc, err := os.ReadFile(path)
	switch {
	case err == nil:
		if err := t.mem.Restore(doc); err != nil {
			return nil, fmt.Errorf("the twin file %s: %w; it is left as it is", path, err)
		}
		t.last = doc
	case errors.Is(err, os.ErrNotExist):
	default:
		return nil, fmt.Errorf("the twin file %s: %w", path, err)
	}
	app.twins[addr] = t
	return t, nil
}

// saveTwins writes every open twin whose state changed, each to its file, in
// one atomic rename (a reader of the file, or a crash, sees the file before or
// after, never half). It is run at the end of every verb.
func (app *application) saveTwins() error {
	var first error
	for _, t := range app.twins {
		doc, err := t.mem.Snapshot()
		if err == nil && string(doc) == string(t.last) {
			continue
		}
		if err == nil {
			err = writeAtomicDoc(t.path, doc)
		}
		if err != nil {
			if first == nil {
				first = fmt.Errorf("the twin file %s was not saved: %w", t.path, err)
			}
			continue
		}
		t.last = doc
	}
	return first
}

// writeAtomicDoc writes the file whole or not at all: a temporary file in the
// same directory, synced, then renamed over the target.
func writeAtomicDoc(path string, doc []byte) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	name := f.Name()
	_, err = f.Write(doc)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(name, 0o600)
	}
	if err == nil {
		err = os.Rename(name, path)
	}
	if err != nil {
		_ = os.Remove(name)
	}
	return err
}

func (t *twin) cmdable() redis.Cmdable {
	return &twinCmdable{
		Cmdable: dummyClient,
		tw:      t,
	}
}

type twinCmdable struct {
	redis.Cmdable
	tw *twin
}

func (c *twinCmdable) FCall(ctx context.Context, id string, keys []string, args ...any) *redis.Cmd {
	cmd := redis.NewCmd(ctx, append([]any{id}, args...)...)
	res, err := c.tw.dispatch(ctx, id, keys, args...)
	if err != nil {
		cmd.SetErr(err)
		return cmd
	}
	cmd.SetVal(res)
	return cmd
}

func (c *twinCmdable) FCallRO(ctx context.Context, id string, keys []string, args ...any) *redis.Cmd {
	return c.FCall(ctx, id, keys, args...)
}

func (c *twinCmdable) Pipeline() redis.Pipeliner {
	return &twinPipeliner{
		Pipeliner: dummyClient.Pipeline(),
		tw:        c.tw,
	}
}

func (c *twinCmdable) TxPipeline() redis.Pipeliner {
	return c.Pipeline()
}

func (c *twinCmdable) Pipelined(ctx context.Context, fn func(redis.Pipeliner) error) ([]redis.Cmder, error) {
	pipe := c.Pipeline()
	if err := fn(pipe); err != nil {
		return nil, err
	}
	return pipe.Exec(ctx)
}

func (c *twinCmdable) TxPipelined(ctx context.Context, fn func(redis.Pipeliner) error) ([]redis.Cmder, error) {
	return c.Pipelined(ctx, fn)
}

type twinPipeliner struct {
	redis.Pipeliner
	tw   *twin
	cmds []redis.Cmder
	fns  []func() error
}

func (p *twinPipeliner) FCall(ctx context.Context, id string, keys []string, args ...any) *redis.Cmd {
	cmd := redis.NewCmd(ctx, append([]any{id}, args...)...)
	p.cmds = append(p.cmds, cmd)
	p.fns = append(p.fns, func() error {
		res, err := p.tw.dispatch(ctx, id, keys, args...)
		if err != nil {
			cmd.SetErr(err)
			return err
		}
		cmd.SetVal(res)
		return nil
	})
	return cmd
}

func (p *twinPipeliner) FCallRO(ctx context.Context, id string, keys []string, args ...any) *redis.Cmd {
	return p.FCall(ctx, id, keys, args...)
}

func (p *twinPipeliner) Exec(ctx context.Context) ([]redis.Cmder, error) {
	var firstErr error
	for _, fn := range p.fns {
		if err := fn(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	p.fns = nil
	return p.cmds, firstErr
}

func (p *twinPipeliner) Discard() {
	p.cmds = nil
	p.fns = nil
}

func receiptWire(epoch, before, after uint64, outcome string) []any {
	return []any{
		"RECEIPT",
		"1-0",
		strconv.FormatUint(epoch, 10),
		strconv.FormatUint(before, 10),
		strconv.FormatUint(after, 10),
		outcome,
	}
}

func refusalReply(err error) []any {
	var ref *ntable.Refusal
	if errors.As(err, &ref) {
		return []any{"REFUSED", ref.Code}
	}
	return []any{"REFUSED", "ARGS", err.Error()}
}

func flatHashSlice(m map[string]string) []any {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var out []any
	for _, k := range keys {
		out = append(out, k, m[k])
	}
	return out
}

func (t *twin) dispatch(ctx context.Context, id string, keys []string, args ...any) (any, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	switch id {
	case ntable.FnCreate:
		name := fmt.Sprint(args[0])
		var fields map[string]string
		switch raw := args[1].(type) {
		case string:
			_ = json.Unmarshal([]byte(raw), &fields)
		case []any:
			fields = map[string]string{}
			for i := 0; i+1 < len(raw); i += 2 {
				fields[fmt.Sprint(raw[i])] = fmt.Sprint(raw[i+1])
			}
		}
		tb, _, err := ntable.DecodeDefinition(name, fields)
		if err != nil {
			return []any{"REFUSED", "ARGS", err.Error()}, nil
		}
		if err := t.mem.Create(ctx, tb); err != nil {
			return refusalReply(err), nil
		}
		return []any{"OK", receiptWire(0, 0, 1, "ok")}, nil

	case ntable.FnDrop, ntable.FnDropDefinition:
		name := fmt.Sprint(args[0])
		epoch, before, _, _, _ := t.mem.TableCheck(ctx, name)
		if err := t.mem.DropTable(ctx, name); err != nil {
			return refusalReply(err), nil
		}
		return []any{"OK", int64(1), receiptWire(epoch, before, before+1, "ok")}, nil

	case ntable.FnList:
		names := t.mem.TableNames()
		var entries []any
		for _, name := range names {
			def, ok := t.mem.Table(name)
			if !ok {
				continue
			}
			cols := int64(len(def.Columns))
			rows := int64(0)
			shapes, err := t.mem.Shapes(ctx, []string{name})
			if err == nil && len(shapes) == 1 {
				rows = int64(len(shapes[0].Rows))
			}
			entries = append(entries, []any{name, cols, rows})
		}
		return append([]any{"OK"}, entries...), nil

	case ntable.FnRead:
		name := fmt.Sprint(args[0])
		shapes, err := t.mem.Shapes(ctx, []string{name})
		if err != nil || len(shapes) == 0 {
			return []any{"REFUSED", "NOTABLE"}, nil
		}
		sh := shapes[0]
		defHash := flatHashSlice(ntable.DefinitionFields(sh))
		var rowsList []any
		for _, r := range sh.Rows {
			rowH := map[string]string{}
			if r.Label != "" {
				rowH["label"] = r.Label
			}
			if r.Hidden {
				rowH["hidden"] = "1"
			}
			for col, val := range r.Texts {
				rowH["text:"+col] = val
			}
			var cellsList []any
			for _, cell := range r.Cells {
				var mems []any
				for _, m := range cell.Members {
					mems = append(mems, m.Member, strconv.FormatFloat(m.Score, 'f', -1, 64))
				}
				cellsList = append(cellsList, []any{"OK", strconv.FormatInt(cell.Count, 10), mems})
			}
			rowsList = append(rowsList, []any{r.Key, flatHashSlice(rowH), cellsList})
		}
		propsSlice := flatHashSlice(sh.Props)
		return []any{"TABLE", defHash, rowsList, propsSlice}, nil

	case ntable.FnRowsAdd:
		name := fmt.Sprint(args[0])
		epoch, before, _, _, _ := t.mem.TableCheck(ctx, name)
		bodyStr := fmt.Sprint(args[1])
		var rows []string
		if strings.HasPrefix(strings.TrimSpace(bodyStr), "{") {
			var req struct {
				Rows []string       `json:"rows"`
				Spec ntable.RowSpec `json:"spec"`
			}
			if err := json.Unmarshal([]byte(bodyStr), &req); err != nil {
				return []any{"REFUSED", "ARGS", err.Error()}, nil
			}
			rows = req.Rows
		} else {
			rows = strings.Split(bodyStr, ",")
		}
		if err := t.mem.RowsAdd(ctx, name, rows); err != nil {
			return refusalReply(err), nil
		}
		_, after, _, _, _ := t.mem.TableCheck(ctx, name)
		return []any{"OK", int64(len(rows)), receiptWire(epoch, before, after, "ok")}, nil

	case ntable.FnRowAdd:
		name := fmt.Sprint(args[0])
		key := fmt.Sprint(args[1])
		epoch, before, _, _, _ := t.mem.TableCheck(ctx, name)
		if err := t.mem.RowsAdd(ctx, name, []string{key}); err != nil {
			return refusalReply(err), nil
		}
		def, _ := t.mem.Table(name)
		defFlat := flatHashSlice(ntable.DefinitionFields(def))
		rowFields := map[string]string{}
		_, after, _, _, _ := t.mem.TableCheck(ctx, name)
		return []any{"OK", defFlat, flatHashSlice(rowFields), receiptWire(epoch, before, after, "ok")}, nil

	case ntable.FnRowDel:
		name := fmt.Sprint(args[0])
		key := fmt.Sprint(args[1])
		epoch, before, _, _, _ := t.mem.TableCheck(ctx, name)
		existed, err := t.mem.RowDel(ctx, name, key)
		if err != nil {
			return refusalReply(err), nil
		}
		count := int64(0)
		if existed {
			count = 1
		}
		_, after, _, _, _ := t.mem.TableCheck(ctx, name)
		return []any{"OK", count, receiptWire(epoch, before, after, "ok")}, nil

	case ntable.FnRowSet:
		name := fmt.Sprint(args[0])
		key := fmt.Sprint(args[1])
		epoch, before, _, _, _ := t.mem.TableCheck(ctx, name)
		var texts map[string]string
		if len(args) > 2 {
			_ = json.Unmarshal([]byte(fmt.Sprint(args[2])), &texts)
		}
		if err := t.mem.RowSet(ctx, name, key, texts); err != nil {
			return refusalReply(err), nil
		}
		_, after, _, _, _ := t.mem.TableCheck(ctx, name)
		return []any{"OK", int64(1), receiptWire(epoch, before, after, "ok")}, nil

	case ntable.FnRowsHide:
		name := fmt.Sprint(args[0])
		flag := fmt.Sprint(args[1])
		hide := flag == "1"
		var keys []string
		if len(args) > 2 {
			_ = json.Unmarshal([]byte(fmt.Sprint(args[2])), &keys)
		}
		epoch, before, _, _, _ := t.mem.TableCheck(ctx, name)
		if err := t.mem.RowsHide(ctx, name, hide, keys); err != nil {
			return refusalReply(err), nil
		}
		_, after, _, _, _ := t.mem.TableCheck(ctx, name)
		return []any{"OK", int64(len(keys)), receiptWire(epoch, before, after, "ok")}, nil

	case ntable.FnSet:
		name := fmt.Sprint(args[0])
		epoch, before, _, _, _ := t.mem.TableCheck(ctx, name)
		specJSON := fmt.Sprint(args[1])
		var raw map[string]json.RawMessage
		if err := json.Unmarshal([]byte(specJSON), &raw); err != nil {
			return []any{"REFUSED", "ARGS", err.Error()}, nil
		}
		var change ntable.SetOpts
		if v, ok := raw["footer"]; ok {
			var f string
			if err := json.Unmarshal(v, &f); err == nil {
				change.Footer = &f
			}
		}
		if v, ok := raw["rename"]; ok {
			_ = json.Unmarshal(v, &change.Rename)
		}
		if v, ok := raw["col_del"]; ok {
			_ = json.Unmarshal(v, &change.ColDel)
		}
		if v, ok := raw["hide"]; ok {
			_ = json.Unmarshal(v, &change.Hide)
		}
		if v, ok := raw["show"]; ok {
			_ = json.Unmarshal(v, &change.Show)
		}
		if v, ok := raw["visible"]; ok {
			var vis bool
			if err := json.Unmarshal(v, &vis); err == nil {
				change.Visible = &vis
			}
		}
		if v, ok := raw["hidden"]; ok {
			var h string
			if err := json.Unmarshal(v, &h); err == nil {
				parts := strings.Split(h, ",")
				change.Hidden = &parts
			}
		}
		if v, ok := raw["row_order"]; ok {
			_ = json.Unmarshal(v, &change.RowOrder)
		}
		if v, ok := raw["row_move"]; ok {
			var rm struct {
				Row   string `json:"row"`
				Where string `json:"where"`
				Ref   string `json:"ref"`
			}
			if err := json.Unmarshal(v, &rm); err == nil {
				change.RowMove = &ntable.Reorder{Item: rm.Row, Place: ntable.Place{Where: rm.Where, Ref: rm.Ref}}
			}
		}
		if v, ok := raw["col_move"]; ok {
			var cm struct {
				Col   string `json:"col"`
				Where string `json:"where"`
				Ref   string `json:"ref"`
			}
			if err := json.Unmarshal(v, &cm); err == nil {
				change.ColMove = &ntable.Reorder{Item: cm.Col, Place: ntable.Place{Where: cm.Where, Ref: cm.Ref}}
			}
		}
		if v, ok := raw["row_sort"]; ok {
			var rs ntable.Sort
			if err := json.Unmarshal(v, &rs); err == nil {
				change.RowSort = &rs
			}
		}
		if v, ok := raw["col_add"]; ok {
			var ca struct {
				Name  string `json:"name"`
				Def   string `json:"def"`
				Where string `json:"where"`
				Ref   string `json:"ref"`
			}
			if err := json.Unmarshal(v, &ca); err == nil {
				col, err := ntable.ParseColumn(ca.Name + ":" + ca.Def)
				if err == nil {
					change.ColAdd = &col
					if ca.Where != "" {
						change.ColAt = &ntable.Place{Where: ca.Where, Ref: ca.Ref}
					}
				}
			}
		}
		if v, ok := raw["columns"]; ok {
			var fields map[string]string
			if err := json.Unmarshal(v, &fields); err == nil {
				t, _, err := ntable.DecodeDefinition(name, fields)
				if err == nil {
					change.Columns = t.Columns
				}
			}
		}
		if err := t.mem.TableSet(ctx, name, change); err != nil {
			return refusalReply(err), nil
		}
		_, after, _, _, _ := t.mem.TableCheck(ctx, name)
		return []any{"OK", int64(0), receiptWire(epoch, before, after, "ok")}, nil

	case ntable.FnClear:
		name := fmt.Sprint(args[0])
		epoch, before, _, _, _ := t.mem.TableCheck(ctx, name)
		n, err := t.mem.TableClear(ctx, name)
		if err != nil {
			return refusalReply(err), nil
		}
		_, after, _, _, _ := t.mem.TableCheck(ctx, name)
		return []any{"OK", n, receiptWire(epoch, before, after, "ok")}, nil

	case ntable.FnCellAdd:
		name := fmt.Sprint(args[0])
		row := fmt.Sprint(args[1])
		col := fmt.Sprint(args[2])
		score, _ := strconv.ParseFloat(fmt.Sprint(args[3]), 64)
		var members []string
		for _, a := range args[4 : len(args)-1] {
			members = append(members, fmt.Sprint(a))
		}
		epoch, before, _, _, _ := t.mem.TableCheck(ctx, name)
		n, err := t.mem.CellAdd(ctx, name, row, col, score, members)
		if err != nil {
			return refusalReply(err), nil
		}
		_, after, _, _, _ := t.mem.TableCheck(ctx, name)
		return []any{"OK", n, receiptWire(epoch, before, after, "ok")}, nil

	case ntable.FnCellRemove:
		name := fmt.Sprint(args[0])
		row := fmt.Sprint(args[1])
		col := fmt.Sprint(args[2])
		var members []string
		for _, a := range args[3 : len(args)-1] {
			members = append(members, fmt.Sprint(a))
		}
		epoch, before, _, _, _ := t.mem.TableCheck(ctx, name)
		n, err := t.mem.CellRemove(ctx, name, row, col, members)
		if err != nil {
			return refusalReply(err), nil
		}
		_, after, _, _, _ := t.mem.TableCheck(ctx, name)
		return []any{"OK", n, receiptWire(epoch, before, after, "ok")}, nil

	case ntable.FnCellMove:
		name := fmt.Sprint(args[0])
		row := fmt.Sprint(args[1])
		fromCol := fmt.Sprint(args[2])
		toCol := fmt.Sprint(args[3])
		var members []string
		for _, a := range args[4 : len(args)-1] {
			members = append(members, fmt.Sprint(a))
		}
		epoch, before, _, _, _ := t.mem.TableCheck(ctx, name)
		n, err := t.mem.CellMove(ctx, name, row, fromCol, toCol, members)
		if err != nil {
			return refusalReply(err), nil
		}
		_, after, _, _, _ := t.mem.TableCheck(ctx, name)
		return []any{"OK", n, receiptWire(epoch, before, after, "ok")}, nil

	case ntable.FnMembers:
		name := fmt.Sprint(args[0])
		row := fmt.Sprint(args[1])
		col := fmt.Sprint(args[2])
		mems, err := t.mem.CellMembers(ctx, name, row, col)
		if err != nil {
			return refusalReply(err), nil
		}
		var flat []any
		for _, m := range mems {
			flat = append(flat, m.Member, strconv.FormatFloat(m.Score, 'f', -1, 64))
		}
		return []any{"MEMBERS", flat}, nil

	case ntable.FnCheck:
		name := fmt.Sprint(args[0])
		epoch, rev, members, cells, err := t.mem.TableCheck(ctx, name)
		if err != nil {
			return refusalReply(err), nil
		}
		return []any{
			"CHECK",
			strconv.FormatUint(epoch, 10),
			strconv.FormatUint(rev, 10),
			strconv.FormatUint(members, 10),
			strconv.FormatUint(cells, 10),
		}, nil

	case ntable.FnMemberFind:
		name := fmt.Sprint(args[0])
		idStr := fmt.Sprint(args[1])
		epoch, rev, state, row, col, err := t.mem.MemberFind(ctx, name, idStr)
		if err != nil {
			return refusalReply(err), nil
		}
		return []any{
			"MEMBER",
			strconv.FormatUint(epoch, 10),
			strconv.FormatUint(rev, 10),
			state,
			row,
			col,
		}, nil

	case ntable.FnMemberCreate:
		name := fmt.Sprint(args[0])
		idStr := fmt.Sprint(args[1])
		epoch, before, _, _, _ := t.mem.TableCheck(ctx, name)
		if err := t.mem.MemberCreate(ctx, name, idStr); err != nil {
			return refusalReply(err), nil
		}
		_, after, _, _, _ := t.mem.TableCheck(ctx, name)
		return []any{"OK", int64(1), receiptWire(epoch, before, after, "ok")}, nil

	case ntable.FnApply:
		var man ntable.BatchManifest
		if err := json.Unmarshal([]byte(fmt.Sprint(args[1])), &man); err != nil {
			return []any{"REFUSED", "ARGS", err.Error()}, nil
		}
		receipt, err := t.mem.Apply(ctx, man)
		if err != nil {
			return refusalReply(err), nil
		}
		wire := []any{
			"RECEIPT",
			receipt.ID,
			strconv.FormatUint(receipt.Epoch, 10),
			strconv.FormatUint(receipt.Before, 10),
			strconv.FormatUint(receipt.After, 10),
			receipt.Outcome,
		}
		if receipt.BatchDelta != nil {
			deltaJSON, _ := json.Marshal(receipt.BatchDelta)
			wire = append(wire, string(deltaJSON))
		}
		reply := []any{"OK", wire}
		if receipt.Replay {
			reply = append(reply, "REPLAY")
		}
		return reply, nil

	case ntable.FnReadSet:
		name := fmt.Sprint(args[0])
		var scope ntable.ReadSetScope
		if len(args) > 1 {
			_ = json.Unmarshal([]byte(fmt.Sprint(args[1])), &scope)
		}
		res, err := t.mem.ReadSet(ctx, name, scope.Members)
		if err != nil {
			return refusalReply(err), nil
		}
		var rawMembers []any
		for _, rm := range res.Members {
			placedStr := "0"
			if rm.Placed {
				placedStr = "1"
			}
			rawMembers = append(rawMembers, []any{
				rm.ID,
				strconv.FormatUint(rm.Revision, 10),
				placedStr,
				rm.Row,
				rm.Col,
				strconv.FormatFloat(rm.Score, 'f', -1, 64),
				flatHashSlice(rm.Fields),
			})
		}
		var rawMissing []any
		for _, m := range res.Missing {
			rawMissing = append(rawMissing, m)
		}
		return []any{
			"SET",
			res.Table,
			strconv.FormatUint(res.Epoch, 10),
			strconv.FormatUint(res.Revision, 10),
			rawMembers,
			rawMissing,
		}, nil

	case "ns_view_state":
		name := fmt.Sprint(args[0])
		text := ""
		if len(args) > 1 {
			text = fmt.Sprint(args[1])
		}
		if err := t.mem.ViewState(ctx, name, text); err != nil {
			return refusalReply(err), nil
		}
		return []any{"OK"}, nil

	case "ns_view_set":
		name := fmt.Sprint(args[0])
		tablesStr := fmt.Sprint(args[1])
		title := fmt.Sprint(args[2])
		summary := fmt.Sprint(args[3])
		var tables []string
		if strings.TrimSpace(tablesStr) != "" {
			tables = strings.Split(tablesStr, ",")
		}
		if err := t.mem.ViewSet(ctx, ntable.View{Name: name, Tables: tables, Title: title, Summary: summary}); err != nil {
			return refusalReply(err), nil
		}
		return []any{"OK"}, nil

	case "ns_view_get":
		name := fmt.Sprint(args[0])
		v, ok := t.mem.View(name)
		if !ok {
			return []any{"REFUSED", "NOVIEW"}, nil
		}
		h := map[string]string{
			"title":   v.Title,
			"summary": v.Summary,
			"state":   v.State,
			"tables":  strings.Join(v.Tables, ","),
		}
		return []any{"OK", flatHashSlice(h)}, nil

	case "ns_view_del":
		name := fmt.Sprint(args[0])
		_ = t.mem.ViewDelete(ctx, name)
		return []any{"OK", int64(1)}, nil

	case "ns_view_list":
		names := t.mem.ViewList()
		var raw []any
		for _, n := range names {
			raw = append(raw, n)
		}
		return []any{"OK", raw}, nil

	default:
		return nil, fmt.Errorf("twin: unsupported function %s", id)
	}
}
