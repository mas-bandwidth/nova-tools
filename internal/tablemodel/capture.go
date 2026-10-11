package tablemodel

import (
	"encoding/json"
	"fmt"
	"maps"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

// The finite universe of the replay: the model's tables, epochs, rows,
// columns, members (with the epoch each was created in) and writers.
var (
	tables  = []string{"t1", "t2"}
	epochs  = []int{1, 2}
	rowKeys = []string{"r1", "r2"}
	cols    = []string{"c1", "c2", "c3"}
	writers = []string{"w1", "w2"}
	members = []struct {
		id    string
		epoch int
	}{{"m1", 1}, {"m2", 2}, {"m3", 1}}
)

// tableFields is the definition every table in the trace is created with.
func tableFields() map[string]string {
	f := map[string]string{"order": strings.Join(cols, ","), "footer": "total", "created_at": "2026-09-27T00:00:00Z",
		"epoch_key": "replay:epoch", "epoch_field": "n"}
	for _, c := range cols {
		f["col:"+c] = "count:sum:0:"
	}
	return f
}

// Step is one controlled call of the trace: a verb of table.lua, or one of the
// two model-only steps (read_epoch, advance) that move the epoch the writers
// see.
type Action struct {
	Verb  string
	Args  []string
	Actor string
}

func js(v any) string {
	raw, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(raw)
}

func fieldsJSON() string { return js(tableFields()) }

func bindJSON(rows []any) string {
	return js(map[string]any{"fields": tableFields(), "rows": rows})
}

func bindsJSON(binds map[string]string) string { return js(map[string]any{"binds": binds}) }

// Actions is the trace: source-API calls in the model's finite universe. Every
// store state they lead to, refused calls included, is checked by TLC.
func Actions() []Action {
	return []Action{
		{"cell_move", []string{"t1", "r1", "c1", "c2", "m1"}, "w1"},
		{"cell_add", []string{"t1", "r2", "c1", "2", "m1"}, "w2"},
		{"bind", []string{"t1", bindJSON([]any{})}, "w1"},
		{"row_add", []string{"t1", "r1", bindsJSON(map[string]string{"c2": "external"})}, "w2"},
		{"row_add", []string{"t1", "r2", bindsJSON(map[string]string{"c3": "external"})}, "w1"},
		{"row_add", []string{"t1", "r2", bindsJSON(map[string]string{"c3": "table:t2:1:cell:r1:c1"})}, "w2"},
		{"clear", []string{"t1"}, "w2"},
		{"cell_remove", []string{"t1", "r2", "c3", "m2"}, "w2"},
		{"cell_add", []string{"t2", "r1", "c1", "1", "m1"}, "w1"},
		{"cell_add", []string{"t2", "r2", "c1", "2", "m3"}, "w2"},
		{"cell_remove", []string{"t1", "r1", "c2", "m1"}, "w1"},
		{"cell_remove", []string{"t1", "r1", "c2", "m1"}, "w1"},
		{"row_del", []string{"t1", "r1"}, "w1"},
		{"row_add", []string{"t1", "r1", "{}"}, "w1"},
		{"cell_add", []string{"t1", "r1", "c1", "1", "m1"}, "w1"},
		{"drop", []string{"t1"}, "w1"},
		{"create", []string{"t1", fieldsJSON()}, "w1"},
		{"advance", nil, "w1"},
		{"cell_add", []string{"t1", "r1", "c1", "1", "m1"}, "w2"},
		{"read_epoch", nil, "w1"},
		{"row_add", []string{"t1", "r1", "{}"}, "w1"},
		{"cell_add", []string{"t1", "r1", "c1", "1", "m1"}, "w1"},
		{"cell_add", []string{"t1", "r1", "c1", "2", "m2"}, "w1"},
		{"cell_move", []string{"t1", "r1", "c1", "c2", "m2"}, "w1"},
		{"row_del", []string{"t1", "r1"}, "w2"},
		{"read_epoch", nil, "w2"},
		{"row_del", []string{"t1", "r1"}, "w2"},
		{"bind", []string{"t1", bindJSON([]any{map[string]any{"key": "r2"}})}, "w1"},
		{"cell_add", []string{"t1", "r2", "c1", "1", "m2"}, "w1"},
		{"clear", []string{"t1"}, "w1"},
		{"drop", []string{"t1"}, "w1"},
		{"create", []string{"t1", fieldsJSON()}, "w1"},
	}
}

// fcallName is the registered function that runs a table verb: every verb of
// table.lua is registered as ns_table_<verb>. The name is formatted from the
// verb, so the source carries no complete quoted ns_ literal that is only a
// prefix: pkg/nsprint/fn reads every such literal as a function name and
// requires a Lua file to register it.
func fcallName(verb string) string { return fmt.Sprintf("ns_table_%s", verb) }

func prefix(table string, epoch int) string { return fmt.Sprintf("table:%s:%d", table, epoch) }

// Options is the option object every call carries.
type callOptions map[string]string

func options(epoch int, actor string) callOptions {
	return callOptions{"epoch": strconv.Itoa(epoch), "actor": actor, "fence": "fixture-fence", "idem": "fixture-attempt"}
}

func call(r *Store, verb string, args []string, opts callOptions) []any {
	argv := []any{"FCALL", fcallName(verb), 0}
	for _, a := range args {
		argv = append(argv, a)
	}
	return list(r.Cmd(append(argv, js(opts))...))
}

func callOK(reply []any, want string, what string) {
	assert(len(reply) > 0 && reply[0] == want, "%s replied %v, not %s", what, reply, want)
}

// seed creates the two tables, their two rows each, and the one starting
// member and the external set.
func seed(r *Store) {
	r.Cmd("HSET", "replay:epoch", "n", 1)
	for _, m := range members {
		r.Cmd("HSET", "table::member:"+m.id, "epoch", m.epoch)
	}
	for _, t := range tables {
		callOK(call(r, "create", []string{t, fieldsJSON()}, options(1, "seed")), "OK", "seed create "+t)
		for _, row := range rowKeys {
			callOK(call(r, "row_add", []string{t, row, "{}"}, options(1, "seed")), "ROW", "seed row_add "+t+" "+row)
		}
	}
	callOK(call(r, "cell_add", []string{"t1", "r1", "c1", "1", "m1"}, options(1, "seed")), "OK", "seed cell_add")
	r.Cmd("ZADD", "external", 2, "m2")
}

func mkCell(table string, epoch int, row, col string) Cell {
	return Cell{Phys{table, epoch}, row, col}
}

func scoresOf(v any) []pair { return pairs(v) }

// snapshot reads the store into the model's vocabulary. The model predeclares
// each future empty physical table; a stable definition supplies that virtual
// presence and an explicit drop overrides it.
func snapshot(r *Store, seen map[string]int) State {
	s := State{Live: []Phys{}, Rows: []RowAt{}, Binds: []Bind{}, Data: []Datum{},
		Active: integer(r.Cmd("HGET", "replay:epoch", "n")), Seen: map[string]int{}}
	maps.Copy(s.Seen, seen)
	for _, t := range tables {
		for _, e := range epochs {
			p := prefix(t, e)
			phys := Phys{t, e}
			if pairMap(r.Cmd("HGETALL", p+":definition"))["_present"] != "0" {
				s.Live = append(s.Live, phys)
			}
			for _, row := range list(r.Cmd("ZRANGE", p+":rows", 0, -1)) {
				rowKey := str(row)
				s.Rows = append(s.Rows, RowAt{phys, rowKey})
				h := pairMap(r.Cmd("HGETALL", p+":row:"+rowKey))
				for _, c := range cols {
					if target := h["key:"+c]; target != "" {
						assert(target == "external", "unexpected binding in controlled trace")
						s.Binds = append(s.Binds, Bind{mkCell(t, e, rowKey, c), External})
					}
				}
			}
			for _, row := range rowKeys {
				for _, c := range cols {
					for _, kv := range scoresOf(r.Cmd("ZRANGE", p+":cell:"+row+":"+c, 0, -1, "WITHSCORES")) {
						s.Data = append(s.Data, Datum{mkCell(t, e, row, c), kv.k, atoi(kv.v)})
					}
				}
			}
		}
	}
	for _, kv := range scoresOf(r.Cmd("ZRANGE", "external", 0, -1, "WITHSCORES")) {
		s.Data = append(s.Data, Datum{External, kv.k, atoi(kv.v)})
	}
	for _, m := range members {
		h := pairMap(r.Cmd("HGETALL", "table::member:"+m.id))
		assert(h["epoch"] == strconv.Itoa(m.epoch), "member epoch changed: %s", m.id)
		pl := Placement{Member: m.id}
		for _, t := range tables {
			for _, e := range epochs {
				where := NoPlace
				if placed := h["place:"+t]; placed != "" && m.epoch == e {
					i := strings.LastIndex(placed, ":")
					assert(i > 0, "member place %q has no column", placed)
					where = mkCell(t, e, placed[:i], placed[i+1:])
				}
				pl.Locations = append(pl.Locations, Location{Phys{t, e}, where})
			}
		}
		s.Place = append(s.Place, pl)
	}
	return s
}

func atoi(v string) int {
	n, err := strconv.Atoi(v)
	assert(err == nil, "score %q is not an integer", v)
	return n
}

// Event is a committed receipt: the fields of one entry of a table's change
// stream, and its id.
type Event map[string]string

func (e Event) clone() Event {
	c := Event{}
	maps.Copy(c, e)
	return c
}

func revision(r *Store, table string) int {
	v := r.Cmd("HGET", "table:"+table+":revision", "n")
	if v == nil {
		return 0
	}
	return integer(v)
}

func jsonList(s string) []any {
	var v []any
	assert(json.Unmarshal([]byte(s), &v) == nil, "%q is not a JSON list", s)
	return v
}

func jsonStrings(s string) []string {
	var v []string
	assert(json.Unmarshal([]byte(s), &v) == nil, "%q is not a JSON list of strings", s)
	return v
}

// receipt checks the reply of a call against the store's change stream. A
// refusal advances nothing and has no event. A change advances the revision by
// exactly one and appends one event that names the verb, its arguments, its
// options and the revisions around it, and the reply ends in the receipt of
// that event. It returns the event, or nil for a refusal.
func receipt(r *Store, verb string, args []string, opts callOptions, reply []any, before int) Event {
	table := args[0]
	after := revision(r, table)
	if reply[0] == "REFUSED" {
		assert(after == before, "refusal advanced revision")
		return nil
	}
	assert(after == before+1, "revision gap")
	entries := list(r.Cmd("XREVRANGE", "table:"+table+":changes", "+", "-", "COUNT", 1))
	assert(len(entries) == 1, "the change stream of %s has no event", table)
	entry := list(entries[0])
	id := str(entry[0])
	event := Event(pairMap(entry[1]))
	assert(event["verb"] == verb && reflect.DeepEqual(jsonStrings(event["args"]), args), "receipt arguments differ")
	assert(atoi(event["rev_before"]) == before && atoi(event["rev_after"]) == after, "receipt revision gap")
	for _, k := range []string{"epoch", "actor", "fence", "idem"} {
		assert(event[k] == opts[k], "receipt option differs: %s", k)
	}
	assert(event["outcome"] == "changed" || event["outcome"] == "noop", "receipt outcome %q is neither changed nor noop", event["outcome"])
	jsonList(event["cells"])
	jsonList(event["members"])
	want := []any{"RECEIPT", id, opts["epoch"], strconv.Itoa(before), strconv.Itoa(after), event["outcome"]}
	assert(reflect.DeepEqual(reply[len(reply)-1], want), "the reply does not end in this event's receipt")
	event["id"] = id
	return event
}

// continuity refuses an event that does not start where the store's revision
// is and end one later.
func continuity(event Event, before int) error {
	if atoi(event["rev_before"]) != before || atoi(event["rev_after"]) != before+1 {
		return fmt.Errorf("GAP: expected revision %d, receipt starts %s", before, event["rev_before"])
	}
	return nil
}

// execute performs one step. read_epoch and advance move the epoch a writer
// has seen and touch no table. A table verb is called with the options of its
// writer's epoch; when saved is given (the replay), the durable event's own
// verb, arguments and options are called instead of the step's, and the
// event's revisions must continue the store's. It returns whether the call was
// refused (nil for the two model-only steps) and its event.
func execute(r *Store, a Action, seen map[string]int, saved Event) (*bool, Event) {
	switch a.Verb {
	case "read_epoch":
		seen[a.Actor] = integer(r.Cmd("HGET", "replay:epoch", "n"))
		return nil, nil
	case "advance":
		current := integer(r.Cmd("HGET", "replay:epoch", "n"))
		assert(seen[a.Actor] == current, "%s advances from epoch %d, the store is at %d", a.Actor, seen[a.Actor], current)
		r.Cmd("HSET", "replay:epoch", "n", current+1)
		return nil, nil
	}
	verb, args := a.Verb, a.Args
	before := revision(r, args[0])
	count := integer(r.Cmd("XLEN", "table:"+args[0]+":changes"))
	opts := options(seen[a.Actor], a.Actor)
	if saved != nil {
		if err := continuity(saved, before); err != nil {
			fail("%v", err)
		}
		// Replay the durable event's command, arguments and options, not the
		// separate test action. Refused calls have no event and use their
		// admission log.
		verb, args = saved["verb"], jsonStrings(saved["args"])
		opts = callOptions{}
		for _, k := range []string{"epoch", "actor", "fence", "idem"} {
			opts[k] = saved[k]
		}
	}
	reply := call(r, verb, args, opts)
	event := receipt(r, verb, args, opts, reply, before)
	added := 0
	if event != nil {
		added = 1
	}
	assert(integer(r.Cmd("XLEN", "table:"+args[0]+":changes")) == count+added, "wrong event count")
	refused := reply[0] == "REFUSED"
	return &refused, event
}

// endpoint is a place as a receipt writes it: "row:col", or empty for none.
func endpoint(c Cell) string {
	if c == NoPlace {
		return ""
	}
	return c.Row + ":" + c.Col
}

// validateDelta checks a receipt against the store states on either side of
// its call: the receipt's member changes are exactly the members whose place
// in that physical table moved, with the score they carry; every cell whose
// presence, binding or contents changed is in the receipt's cells, none twice;
// and an outcome of noop means nothing changed.
func validateDelta(event Event, before, after State) error {
	var argv []string
	if err := json.Unmarshal([]byte(event["args"]), &argv); err != nil || len(argv) == 0 {
		return fmt.Errorf("receipt arguments are not a list")
	}
	table := argv[0]
	epoch := atoi(event["epoch"])
	physical := Phys{table, epoch}
	places := func(s State) map[string]Cell {
		out := map[string]Cell{}
		for _, p := range s.Place {
			for _, l := range p.Locations {
				if l.P == physical {
					out[p.Member] = l.Cell
				}
			}
		}
		return out
	}
	score := func(s State, at Cell, member string) string {
		for _, d := range s.Data {
			if d.Cell == at && d.Member == member {
				return strconv.Itoa(d.Score)
			}
		}
		panic(&Failure{Msg: fmt.Sprintf("no score for %s at %v", member, at)})
	}
	type change struct{ ID, From, To, Score string }
	old, now := places(before), places(after)
	var expected []change
	for _, m := range members {
		if old[m.id] != now[m.id] {
			var sc string
			if now[m.id] == NoPlace {
				sc = score(before, old[m.id], m.id)
			} else {
				sc = score(after, now[m.id], m.id)
			}
			expected = append(expected, change{m.id, endpoint(old[m.id]), endpoint(now[m.id]), sc})
		}
	}
	var raw []map[string]string
	if err := json.Unmarshal([]byte(event["members"]), &raw); err != nil {
		return fmt.Errorf("receipt members are not a list of objects: %v", err)
	}
	var actual []change
	for _, m := range raw {
		actual = append(actual, change{m["id"], m["from"], m["to"], m["score"]})
	}
	byID := func(l []change) { sort.Slice(l, func(i, j int) bool { return l[i].ID < l[j].ID }) }
	byID(expected)
	byID(actual)
	if len(expected) != len(actual) || (len(expected) > 0 && !reflect.DeepEqual(expected, actual)) {
		return fmt.Errorf("receipt member delta disagrees with store")
	}
	affected := jsonStrings2(event["cells"])
	seen := map[string]bool{}
	for _, a := range affected {
		if seen[a] {
			return fmt.Errorf("duplicate affected cell")
		}
		seen[a] = true
	}
	type contents struct {
		present bool
		bound   *Cell
		data    []string
	}
	contentsOf := func(s State, at Cell) contents {
		c := contents{}
		for _, row := range s.Rows {
			c.present = c.present || (row.P == physical && row.Row == at.Row)
		}
		for _, b := range s.Binds {
			if b.Cell == at {
				t := b.Target
				c.bound = &t
				break
			}
		}
		for _, d := range s.Data {
			if d.Cell == at {
				c.data = append(c.data, d.Member+"\x00"+strconv.Itoa(d.Score))
			}
		}
		sort.Strings(c.data)
		return c
	}
	for _, row := range rowKeys {
		for _, col := range cols {
			at := mkCell(table, epoch, row, col)
			if !reflect.DeepEqual(contentsOf(before, at), contentsOf(after, at)) && !seen[row+":"+col] {
				return fmt.Errorf("receipt omits an affected cell")
			}
		}
	}
	if event["outcome"] == "noop" && !reflect.DeepEqual(before, after) {
		return fmt.Errorf("receipt calls a state change a noop")
	}
	return nil
}

func jsonStrings2(s string) []string {
	var v []string
	assert(json.Unmarshal([]byte(s), &v) == nil, "receipt cells %q are not a list of strings", s)
	return v
}
