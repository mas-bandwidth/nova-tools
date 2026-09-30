//go:build functional

package tset

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/redis/go-redis/v9"
)

// Acceptance tests for lua/table_set_log.lua (Layer 2, the log), run on
// the composed profile only, each on its own server inside the container.
// Direct store writes below are fault injections and are marked as such.

func logDraftFixture(t *testing.T) (*tsetFixture, *RedisStore) {
	t.Helper()
	fx := newComposedTSetFixture(t)
	fx.Define(t, "work", "a", "b")
	fx.Define(t, "aux", "c")
	fx.Activate(t)
	return fx, newFixtureRedis(t, fx.Client)
}

func logNamed(step Step, op string) Step {
	intent := "log-draft/" + op
	step.Op, step.Intent = &op, &intent
	return step
}

func logRows(space string, epoch Decimal, table string, rows ...string) Step {
	return Step{Epoch: epoch, Space: space, Entries: []Entry{{Kind: "rows", Table: table, Add: rows}}}
}

func logNote(meta string, about ...string) Note {
	if about == nil {
		about = []string{}
	}
	return Note{Line: NoteLine{Kind: "note", Meta: json.RawMessage(meta)}, About: about}
}

func logRefused(t *testing.T, fx *tsetFixture, store *RedisStore, step Step, code string) *Refusal {
	t.Helper()
	before := commitProbeImage(t, fx.Client)
	reply, err := store.Step(context.Background(), step)
	var refusal *Refusal
	if !errors.As(err, &refusal) || refusal.Code != code {
		t.Fatalf("step reply=%+v err=%v, want refusal %s", reply, err, code)
	}
	if after := commitProbeImage(t, fx.Client); !reflect.DeepEqual(before, after) {
		t.Fatalf("%s refusal changed the Redis TYPE/DUMP image", code)
	}
	return refusal
}

type logDraftLine struct {
	Seq  string
	N    string
	D    string
	Body logDraftBody
}

type logDraftBody struct {
	K      string              `json:"k"`
	MS     string              `json:"ms"`
	Tbl    string              `json:"tbl"`
	From   *string             `json:"from"`
	To     *string             `json:"to"`
	IDs    []string            `json:"ids"`
	About  []string            `json:"about"`
	Score  [][2]*string        `json:"score"`
	Rev    [][2]*string        `json:"rev"`
	Shared map[string]string   `json:"shared"`
	Set    []map[string]string `json:"set"`
	Unset  [][]string          `json:"unset"`
	Meta   json.RawMessage     `json:"meta"`
	Add    []struct {
		Rank string `json:"rank"`
		Row  string `json:"row"`
	} `json:"add"`
	Del []string `json:"del"`
}

// logDraftStream reads the stored stream directly and asserts Gapless: the
// ids are exactly 1-0 .. N-0, each entry exactly the fields n and d.
func logDraftStream(t *testing.T, c *redis.Client, space, epoch string) []logDraftLine {
	t.Helper()
	msgs, err := c.XRange(context.Background(), fixtureLogKey(space, epoch), "-", "+").Result()
	if err != nil {
		t.Fatalf("XRANGE log@%s: %v", epoch, err)
	}
	out := make([]logDraftLine, 0, len(msgs))
	for i, m := range msgs {
		if want := fmt.Sprintf("%d-0", i+1); m.ID != want {
			t.Fatalf("log@%s entry %d id=%s, want %s", epoch, i, m.ID, want)
		}
		n, okN := m.Values["n"].(string)
		d, okD := m.Values["d"].(string)
		if !okN || !okD || len(m.Values) != 2 {
			t.Fatalf("log@%s entry %s fields=%v, want exactly n and d", epoch, m.ID, m.Values)
		}
		var body logDraftBody
		if err := json.Unmarshal([]byte(d), &body); err != nil {
			t.Fatalf("log@%s entry %s body %q: %v", epoch, m.ID, d, err)
		}
		out = append(out, logDraftLine{Seq: fmt.Sprint(i + 1), N: n, D: d, Body: body})
	}
	return out
}

func logDraftHistory(t *testing.T, c *redis.Client, space, epoch, about string) []string {
	t.Helper()
	got, err := c.LRange(context.Background(), fixtureHistoryKey(space, epoch, about), 0, -1).Result()
	if err != nil {
		t.Fatalf("LRANGE history %s@%s: %v", about, epoch, err)
	}
	return got
}

// logDraftRaw calls the registered read function with hand-built JSON, so
// cursor shapes the Go types do not encode can be sent unchanged.
func logDraftRaw(t *testing.T, fx *tsetFixture, plan string) map[string]json.RawMessage {
	t.Helper()
	wire, err := fx.Client.FCallRO(context.Background(), "ns_tset_read", []string{}, Version, plan).Result()
	if err != nil {
		t.Fatalf("Lua read returned Redis error: %v", err)
	}
	encoded, ok := wire.(string)
	if !ok {
		t.Fatalf("Lua read returned %T", wire)
	}
	var out map[string]json.RawMessage
	if err := json.Unmarshal([]byte(encoded), &out); err != nil {
		t.Fatalf("read reply %q: %v", encoded, err)
	}
	return out
}

func logDraftCode(t *testing.T, reply map[string]json.RawMessage) string {
	t.Helper()
	var status, code string
	_ = json.Unmarshal(reply["status"], &status)
	_ = json.Unmarshal(reply["code"], &code)
	if status != "refused" {
		return ""
	}
	for _, leaked := range []string{"answers", "items"} {
		if _, ok := reply[leaked]; ok {
			t.Fatalf("refusal leaked %s: %v", leaked, reply)
		}
	}
	return code
}

func TestLogOneLinePerEmittingEntry(t *testing.T) {
	t.Parallel()
	fx, store := logDraftFixture(t)
	composedWrite(t, store, logRows(fx.Space, "0", "work", "r"))
	composedWrite(t, store, Step{Epoch: "0", Space: fx.Space, Entries: []Entry{{Kind: "create",
		Table: "work", To: "r:a", IDs: []string{"x", "y", "z"}, Scores: []string{"1", "2", "3"},
		About: []string{"p", "q", "z"}, Set: map[string]string{"brief": "shared-brief"},
		Each: []map[string]string{{"own": "1"}, {"own": "2"}, {"own": "3"}}}}})
	step := logNamed(Step{Epoch: "0", Space: fx.Space, Entries: []Entry{
		{Kind: "rows", Table: "work", Add: []string{"s"}},
		{Kind: "move", Table: "work", From: "r:a", To: "s:b", IDs: []string{"x"},
			Set: map[string]string{"state": "moved"}, About: []string{"p"}},
		// A stay with no score or field change: no effective change, no line.
		{Kind: "move", Table: "work", From: "r:a", IDs: []string{"y"}, About: []string{"q"}},
		{Kind: "guard", Table: "work", From: "r:a", IDs: []string{"z"}},
	}, Notes: []Note{logNote(`{"why":"x"}`, "p", "p", "q"), logNote(`{}`)}}, "emit")
	reply := composedWrite(t, store, step)
	if reply.FirstSeq != "3" || reply.LastSeq != "6" || reply.Lines != 4 ||
		!reflect.DeepEqual(reply.ChangedPerEntry, []int{0, 1, 0, 0}) {
		t.Fatalf("emitting step reply=%+v, want seqs 3..6, four lines, changed [0 1 0 0]", reply)
	}
	lines := logDraftStream(t, fx.Client, fx.Space, "0")
	want := []struct{ n, d string }{
		{"0", `{"k":"w","ms":"MS","tbl":"work","add":[{"rank":"0","row":"r"}]}`},
		{"3", `{"k":"c","ms":"MS","tbl":"work","to":"r:a","ids":["x","y","z"],"about":["p","q","z"],` +
			`"score":[[null,"1"],[null,"2"],[null,"3"]],"rev":[[null,"1"],[null,"1"],[null,"1"]],` +
			`"shared":{"brief":"shared-brief"},"set":[{"own":"1"},{"own":"2"},{"own":"3"}]}`},
		{"0", `{"k":"w","ms":"MS","tbl":"work","add":[{"rank":"1","row":"s"}]}`},
		{"1", `{"k":"m","ms":"MS","tbl":"work","from":"r:a","to":"s:b","ids":["x"],"about":["p"],` +
			`"score":[["1","1"]],"rev":[["1","2"]],"shared":{"state":"moved"}}`},
		{"2", `{"k":"n","ms":"MS","about":["p","q"],"meta":{"why":"x"}}`},
		{"0", `{"k":"n","ms":"MS"}`},
	}
	if len(lines) != len(want) {
		t.Fatalf("log has %d lines, want %d", len(lines), len(want))
	}
	for i, line := range lines {
		canonical := strings.Replace(line.D, `"ms":"`+line.Body.MS+`"`, `"ms":"MS"`, 1)
		if line.N != want[i].n || canonical != want[i].d {
			t.Errorf("line %s: n=%s d=%s\nwant n=%s d=%s", line.Seq, line.N, canonical, want[i].n, want[i].d)
		}
	}
	// TestTimeFrozenAcrossLines, in part: one TIME sample for every line of a call.
	for _, line := range lines[2:] {
		if line.Body.MS != lines[2].Body.MS || line.Body.MS == "" {
			t.Errorf("line %s at_ms=%q, want the call's one sample %q", line.Seq, line.Body.MS, lines[2].Body.MS)
		}
	}
	for about, seqs := range map[string][]string{"p": {"2", "4", "5"}, "q": {"2", "5"}, "z": {"2"}} {
		if got := logDraftHistory(t, fx.Client, fx.Space, "0", about); !reflect.DeepEqual(got, seqs) {
			t.Errorf("history %s=%v, want %v", about, got, seqs)
		}
	}

	// TestNoLineForNoopOrGuard: a no-op stay, a guard and an already present
	// row add emit nothing and allocate nothing; without op nothing is written.
	before := commitProbeImage(t, fx.Client)
	noop := composedWrite(t, store, Step{Epoch: "0", Space: fx.Space, Entries: []Entry{
		{Kind: "move", Table: "work", From: "r:a", IDs: []string{"y"}, About: []string{"q"}},
		{Kind: "guard", Table: "work", From: "r:a", IDs: []string{"z"}},
		{Kind: "rows", Table: "work", Add: []string{"r"}},
	}})
	if noop.Lines != 0 || noop.FirstSeq != "0" || noop.LastSeq != "0" {
		t.Fatalf("no-op step reply=%+v, want no line and seqs 0", noop)
	}
	if after := commitProbeImage(t, fx.Client); !reflect.DeepEqual(before, after) {
		t.Fatal("no-op step changed the store")
	}
}

func TestLogSequenceNoGapAcrossRefusalAndAdvance(t *testing.T) {
	t.Parallel()
	fx, store := logDraftFixture(t)
	composedWrite(t, store, logRows(fx.Space, "0", "work", "r", "s"))
	composedWrite(t, store, Step{Epoch: "0", Space: fx.Space, Entries: []Entry{{Kind: "create",
		Table: "work", To: "r:a", IDs: []string{"x"}, Scores: []string{"1"}, About: []string{"p"}}}})
	// A Layer 1 refusal and a Layer 2 refusal: neither writes, neither leaves a gap.
	logRefused(t, fx, store, Step{Epoch: "0", Space: fx.Space, Entries: []Entry{{Kind: "move",
		Table: "work", From: "r:a", To: "s:a", IDs: []string{"x"}, Revs: []Decimal{"9"},
		About: []string{"p"}}}}, "REVISION")
	logRefused(t, fx, store, Step{Epoch: "0", Space: fx.Space, Entries: []Entry{{Kind: "move",
		Table: "work", From: "r:a", To: "s:a", IDs: []string{"x"}}}}, "REQUEST")
	if reply := composedWrite(t, store, Step{Epoch: "0", Space: fx.Space, Entries: []Entry{{Kind: "move",
		Table: "work", From: "r:a", To: "s:a", IDs: []string{"x"}, About: []string{"p"}}}}); reply.FirstSeq != "3" || reply.LastSeq != "3" {
		t.Fatalf("step after two refusals took seqs %s..%s, want 3", reply.FirstSeq, reply.LastSeq)
	}
	// A rows-only change allocates.
	if reply := composedWrite(t, store, logRows(fx.Space, "0", "work", "t")); reply.FirstSeq != "4" || reply.LastSeq != "4" {
		t.Fatalf("rows change took seqs %s..%s, want 4", reply.FirstSeq, reply.LastSeq)
	}
	oldLog, err := fx.Client.Dump(context.Background(), fixtureLogKey(fx.Space, "0")).Result()
	if err != nil {
		t.Fatal(err)
	}
	advance := logNamed(Step{Epoch: "0", Space: fx.Space, Entries: []Entry{
		{Kind: "advance", AdvanceFrom: "0"},
		{Kind: "rows", Table: "work", Add: []string{"r"}},
	}, Notes: []Note{logNote(`{}`, "p")}}, "advance")
	reply := composedWrite(t, store, advance)
	if reply.EpochAfter != "1" || reply.FirstSeq != "1" || reply.LastSeq != "3" || reply.Lines != 3 {
		t.Fatalf("advance reply=%+v, want epoch 1 and seqs 1..3 of the new log", reply)
	}
	if got, err := fx.Client.Dump(context.Background(), fixtureLogKey(fx.Space, "0")).Result(); err != nil || got != oldLog {
		t.Fatalf("advance appended to or changed the old epoch's log (err=%v)", err)
	}
	fresh := logDraftStream(t, fx.Client, fx.Space, "1")
	if len(fresh) != 3 || fresh[0].Body.K != "a" || fresh[0].Body.From == nil || *fresh[0].Body.From != "0" ||
		fresh[0].Body.To == nil || *fresh[0].Body.To != "1" || fresh[1].Body.K != "w" || fresh[2].Body.K != "n" {
		t.Fatalf("new epoch log=%+v, want advance line first, then the restored row, then the note", fresh)
	}
	if got := logDraftHistory(t, fx.Client, fx.Space, "1", "p"); !reflect.DeepEqual(got, []string{"3"}) {
		t.Fatalf("new epoch history p=%v, want [3]", got)
	}
	if got := logDraftHistory(t, fx.Client, fx.Space, "0", "p"); !reflect.DeepEqual(got, []string{"2", "3"}) {
		t.Fatalf("old epoch history p=%v, want [2 3]", got)
	}
	ctx := context.Background()
	if n, err := fx.Client.HLen(ctx, fixtureDoneKey(fx.Space, "0")).Result(); err != nil || n != 1 {
		t.Fatalf("receipt at request epoch: count=%d err=%v", n, err)
	}
	if n, err := fx.Client.Exists(ctx, fixtureDoneKey(fx.Space, "1")).Result(); err != nil || n != 0 {
		t.Fatalf("receipt written at the successor epoch: exists=%d err=%v", n, err)
	}
	if reply := composedWrite(t, store, Step{Epoch: "1", Space: fx.Space, Entries: []Entry{{Kind: "create",
		Table: "work", To: "r:a", IDs: []string{"y"}, Scores: []string{"1"}, About: []string{"p"}}}}); reply.FirstSeq != "4" || reply.LastSeq != "4" {
		t.Fatalf("first member step of epoch 1 took seqs %s..%s, want 4", reply.FirstSeq, reply.LastSeq)
	}
	logDraftStream(t, fx.Client, fx.Space, "1")
}

// TestAdvanceNewLogStartsAtOne: an advance whose new-epoch log key is already
// present refuses DRIFT before any write.
func TestAdvanceNewLogStartsAtOne(t *testing.T) {
	t.Parallel()
	fx, store := logDraftFixture(t)
	composedWrite(t, store, logRows(fx.Space, "0", "work", "r"))
	// Fault injection: a stream at the successor epoch's log key.
	if err := fx.Client.XAdd(context.Background(), &redis.XAddArgs{Stream: fixtureLogKey(fx.Space, "1"),
		ID: "1-0", Values: []any{"n", "0", "d", `{"k":"n","ms":"1"}`}}).Err(); err != nil {
		t.Fatal(err)
	}
	// An advance carries an op and an intent (revision 4), so it reaches the check.
	logRefused(t, fx, store, logNamed(Step{Epoch: "0", Space: fx.Space,
		Entries: []Entry{{Kind: "advance", AdvanceFrom: "0"}}}, "advance-drift"), "DRIFT")
}

// logDraftReplay folds lines into reconstructed table state, value by value.
type logDraftReplay struct {
	space, epoch string
	records      map[string]map[string]string
	cells        map[string]map[string]string
	rows         map[string]map[string]string
	history      map[string][]string
}

func logDraftCellKey(t *testing.T, space, table, epoch, ref string) string {
	t.Helper()
	i := strings.LastIndex(ref, ":")
	if i <= 0 {
		t.Fatalf("cell reference %q", ref)
	}
	return fixtureCellKey(space, table, epoch, ref[:i], ref[i+1:])
}

func (r *logDraftReplay) apply(t *testing.T, seq string, b logDraftBody) {
	t.Helper()
	switch b.K {
	case "w":
		key := fixtureRowsKey(r.space, b.Tbl, r.epoch)
		if r.rows[key] == nil {
			r.rows[key] = map[string]string{}
		}
		for _, add := range b.Add {
			r.rows[key][add.Row] = add.Rank
		}
		for _, row := range b.Del {
			delete(r.rows[key], row)
		}
	case "a":
	case "n":
	case "c", "m", "x":
		for j, id := range b.IDs {
			key := fixtureRecordKey(r.space, b.Tbl, id)
			rec := r.records[key]
			if b.K == "c" {
				rec = map[string]string{"epoch": r.epoch}
				r.records[key] = rec
			}
			if rec == nil {
				t.Fatalf("seq %s changes %s before any create", seq, key)
			}
			rec["revision"] = *b.Rev[j][1]
			for f, v := range b.Shared {
				rec[f] = v
			}
			if b.Set != nil {
				for f, v := range b.Set[j] {
					rec[f] = v
				}
			}
			if b.Unset != nil {
				for _, f := range b.Unset[j] {
					delete(rec, f)
				}
			}
			place := "place:" + b.Tbl
			if b.From != nil {
				delete(r.cells[logDraftCellKey(t, r.space, b.Tbl, r.epoch, *b.From)], id)
			}
			dest := ""
			switch {
			case b.K == "c":
				dest = *b.To
			case b.K == "m" && b.To != nil:
				dest = *b.To
			case b.K == "m":
				dest = *b.From
			}
			if dest == "" {
				delete(rec, place)
				continue
			}
			rec[place] = dest
			cell := logDraftCellKey(t, r.space, b.Tbl, r.epoch, dest)
			if r.cells[cell] == nil {
				r.cells[cell] = map[string]string{}
			}
			r.cells[cell][id] = *b.Score[j][1]
		}
	default:
		t.Fatalf("seq %s has unknown kind %q", seq, b.K)
	}
	seen := map[string]bool{}
	for _, about := range b.About {
		if !seen[about] {
			seen[about] = true
			key := fixtureHistoryKey(r.space, r.epoch, about)
			r.history[key] = append(r.history[key], seq)
		}
	}
}

func logDraftTableKeys(t *testing.T, c *redis.Client, space string) []string {
	t.Helper()
	keys, err := c.Keys(context.Background(), space+"*").Result()
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, k := range keys {
		switch {
		case strings.HasPrefix(k, space+"member:"),
			strings.HasPrefix(k, space+"sprint:cl:"),
			strings.HasPrefix(k, space+"table:") && (strings.HasSuffix(k, ":rows") || strings.Contains(k, ":cell:")):
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

func logDraftValue(t *testing.T, c *redis.Client, key string) any {
	t.Helper()
	ctx := context.Background()
	kind, err := c.Type(ctx, key).Result()
	if err != nil {
		t.Fatal(err)
	}
	switch kind {
	case "hash":
		return c.HGetAll(ctx, key).Val()
	case "zset":
		return c.ZRangeWithScores(ctx, key, 0, -1).Val()
	case "list":
		return c.LRange(ctx, key, 0, -1).Val()
	}
	t.Fatalf("key %s has type %s", key, kind)
	return nil
}

// A small epoch replayed from its log, through the public lines page, is
// written into a second private server and compared with the store value by
// value (value-equal, not DUMP-equal): records, cells, rows and histories.
func TestLogReplaySmallEpochEqualsTables(t *testing.T) {
	t.Parallel()
	fx, store := logDraftFixture(t)
	s := fx.Space
	composedWrite(t, store, Step{Epoch: "0", Space: s, Entries: []Entry{
		{Kind: "rows", Table: "work", Add: []string{"r1", "r2", "r3"}},
		{Kind: "rows", Table: "aux", Add: []string{"q1"}}}})
	composedWrite(t, store, Step{Epoch: "0", Space: s, Entries: []Entry{
		{Kind: "create", Table: "work", To: "r1:a", IDs: []string{"x1", "x2", "x3"}, Scores: []string{"1", "2", "3"},
			About: []string{"p1", "p2", "p3"}, Set: map[string]string{"brief": "B"},
			Each: []map[string]string{{"n": "1"}, {"n": "2"}, {"n": "3"}}},
		{Kind: "create", Table: "aux", To: "q1:c", IDs: []string{"y1"}, Scores: []string{"5"},
			About: []string{"p1"}, Set: map[string]string{"k": "v"}}}})
	composedWrite(t, store, Step{Epoch: "0", Space: s, Entries: []Entry{
		{Kind: "move", Table: "work", From: "r1:a", To: "r2:b", IDs: []string{"x1"}, Scores: []string{"10"},
			Set: map[string]string{"state": "s1"}, Unset: []string{"n"}, About: []string{"p1"}},
		{Kind: "move", Table: "work", From: "r1:a", IDs: []string{"x2"}, Scores: []string{"7"}, About: []string{"p2"}}}})
	composedWrite(t, store, Step{Epoch: "0", Space: s, Entries: []Entry{
		{Kind: "remove", Table: "work", From: "r1:a", IDs: []string{"x3"}, Set: map[string]string{"retired": "yes"},
			About: []string{"p3"}}}})
	composedWrite(t, store, logNamed(Step{Epoch: "0", Space: s, Entries: []Entry{
		{Kind: "rows", Table: "work", Del: []string{"r3"}}}, Notes: []Note{logNote(`{"n":"1"}`, "p1", "p2")}}, "notes"))
	composedWrite(t, store, Step{Epoch: "0", Space: s, Entries: []Entry{
		{Kind: "move", Table: "work", From: "r1:a", To: "r2:a", IDs: []string{"x2"}, Set: map[string]string{"n": "22"},
			About: []string{"p2"}},
		{Kind: "rows", Table: "work", Del: []string{"r1"}}}})
	// Two ids of one primary on one line: its history takes the seq once.
	composedWrite(t, store, Step{Epoch: "0", Space: s, Entries: []Entry{
		{Kind: "create", Table: "work", To: "r2:a", IDs: []string{"x4", "x5"}, Scores: []string{"4", "5"},
			About: []string{"p4", "p4"}}}})

	type item struct {
		Seq string `json:"seq"`
		N   string `json:"n"`
		D   string `json:"d"`
	}
	var items []item
	after := Decimal("0")
	var through *Decimal
	for turn := 0; ; turn++ {
		if turn > 50 {
			t.Fatal("lines pages did not reach their high-water")
		}
		page := composedPage(t, store, ReadPlan{Epoch: "0", Space: s, Mode: "page",
			Queries: []ReadQuery{{Kind: "lines", AfterSeq: after, ThroughSeq: through, Limit: 2}}})
		for _, raw := range page.Items {
			var it item
			if err := json.Unmarshal(raw, &it); err != nil {
				t.Fatalf("lines item %s: %v", raw, err)
			}
			items = append(items, it)
		}
		var high Decimal
		if err := json.Unmarshal(page.Through, &high); err != nil {
			t.Fatalf("page through %s: %v", page.Through, err)
		}
		through = &high
		if page.Exhausted {
			break
		}
		after = Decimal(items[len(items)-1].Seq)
	}
	stored := logDraftStream(t, fx.Client, s, "0")
	if len(items) != len(stored) {
		t.Fatalf("paged %d lines, stream holds %d", len(items), len(stored))
	}
	replay := &logDraftReplay{space: s, epoch: "0", records: map[string]map[string]string{},
		cells: map[string]map[string]string{}, rows: map[string]map[string]string{}, history: map[string][]string{}}
	for i, it := range items {
		if it.Seq != stored[i].Seq || it.N != stored[i].N || it.D != stored[i].D {
			t.Fatalf("lines item %d=%+v is not the stored line %s verbatim", i, it, stored[i].Seq)
		}
		replay.apply(t, it.Seq, stored[i].Body)
	}

	scratch := redis.NewClient(&redis.Options{Addr: testutil.Start(t), MaxRetries: -1})
	t.Cleanup(func() { _ = scratch.Close() })
	ctx := context.Background()
	for key, rec := range replay.records {
		args := []any{}
		for f, v := range rec {
			args = append(args, f, v)
		}
		if err := scratch.HSet(ctx, key, args...).Err(); err != nil {
			t.Fatal(err)
		}
	}
	for key, members := range replay.cells {
		for id, score := range members {
			if err := scratch.Do(ctx, "ZADD", key, score, id).Err(); err != nil {
				t.Fatal(err)
			}
		}
	}
	for key, rows := range replay.rows {
		for row, rank := range rows {
			if err := scratch.Do(ctx, "ZADD", key, rank, row).Err(); err != nil {
				t.Fatal(err)
			}
		}
	}
	for key, seqs := range replay.history {
		args := make([]any, len(seqs))
		for i, seq := range seqs {
			args[i] = seq
		}
		if err := scratch.RPush(ctx, key, args...).Err(); err != nil {
			t.Fatal(err)
		}
	}
	storeKeys := logDraftTableKeys(t, fx.Client, s)
	replayKeys := logDraftTableKeys(t, scratch, s)
	if !reflect.DeepEqual(storeKeys, replayKeys) {
		t.Fatalf("replayed keys differ:\nstore  %v\nreplay %v", storeKeys, replayKeys)
	}
	for _, key := range storeKeys {
		if a, b := logDraftValue(t, fx.Client, key), logDraftValue(t, scratch, key); !reflect.DeepEqual(a, b) {
			t.Errorf("key %s: store=%v replay=%v", key, a, b)
		}
	}
	if len(storeKeys) < 8 {
		t.Fatalf("replay compared only %d keys: %v", len(storeKeys), storeKeys)
	}
}

func logDraftCardSetup(t *testing.T) (*tsetFixture, *RedisStore) {
	t.Helper()
	fx, store := logDraftFixture(t)
	composedWrite(t, store, logRows(fx.Space, "0", "work", "r"))
	composedWrite(t, store, Step{Epoch: "0", Space: fx.Space, Entries: []Entry{{Kind: "create",
		Table: "work", To: "r:a", IDs: []string{"m1", "m2", "m3", "m4"}, Scores: []string{"1", "2", "3", "4"},
		About: []string{"p", "p", "p", "q"},
		Each:  []map[string]string{{"brief": "b1"}, {"brief": "b2"}, {"brief": "b3"}, {"brief": "foreign"}}}}})
	composedWrite(t, store, logNamed(Step{Epoch: "0", Space: fx.Space, Entries: []Entry{},
		Notes: []Note{logNote(`{}`, "p")}}, "card-note"))
	composedWrite(t, store, Step{Epoch: "0", Space: fx.Space, Entries: []Entry{{Kind: "move",
		Table: "work", From: "r:a", To: "r:b", IDs: []string{"m1", "m2"}, About: []string{"p", "p"}}}})
	return fx, store
}

type logDraftCardItem struct {
	Seq  string            `json:"seq"`
	Kind string            `json:"kind"`
	ID   string            `json:"id"`
	Set  map[string]string `json:"set"`
}

// The pair cursor (list index, item index within the line) neither skips nor
// repeats a (line, member id) projection, and never shows a foreign id.
func TestCardLinesPairCursorNeitherSkipsNorRepeats(t *testing.T) {
	t.Parallel()
	want := []string{"2/create/m1/b1", "2/create/m2/b2", "2/create/m3/b3", "3/note//", "4/move/m1/", "4/move/m2/"}
	t.Run("pages ending on line boundaries", func(t *testing.T) {
		t.Parallel()
		fx, store := logDraftCardSetup(t)
		query := ReadQuery{Kind: "cardlines", Abouts: []string{"p"}, Fields: []string{"brief"}, Limit: 3}
		var got []string
		for turn := 0; turn < 5; turn++ {
			page := composedPage(t, store, ReadPlan{Epoch: "0", Space: fx.Space, Mode: "page", Queries: []ReadQuery{query}})
			if len(page.Items) != 1 {
				t.Fatalf("page slots=%d, want one per about", len(page.Items))
			}
			if strings.Contains(string(page.Items[0]), "foreign") || strings.Contains(string(page.Items[0]), "m4") {
				t.Fatalf("foreign primary leaked into p's projection: %s", page.Items[0])
			}
			var slot struct {
				About string             `json:"about"`
				Lines []logDraftCardItem `json:"lines"`
			}
			if err := json.Unmarshal(page.Items[0], &slot); err != nil || slot.About != "p" {
				t.Fatalf("slot %s err=%v", page.Items[0], err)
			}
			for _, it := range slot.Lines {
				got = append(got, it.Seq+"/"+it.Kind+"/"+it.ID+"/"+it.Set["brief"])
			}
			if page.Exhausted {
				break
			}
			cursor := composedCursor(t, page.Next)
			query.Cursor = &cursor
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("paged projections=%v, want %v", got, want)
		}
	})
	t.Run("a page ending inside a line resumes at the next item", func(t *testing.T) {
		t.Parallel()
		fx, _ := logDraftCardSetup(t)
		first := logDraftRaw(t, fx, fmt.Sprintf(`{"epoch":"0","space":%q,"mode":"page","queries":[`+
			`{"kind":"cardlines","abouts":["p"],"fields":["brief"],"limit":2}]}`, fx.Space))
		if code := logDraftCode(t, first); code != "" {
			t.Fatalf("first page refused %s", code)
		}
		var next struct {
			Positions []map[string]any `json:"positions"`
		}
		if err := json.Unmarshal(first["next"], &next); err != nil || len(next.Positions) != 1 ||
			next.Positions[0]["next_index"] != float64(0) || next.Positions[0]["next_item"] != float64(2) {
			t.Fatalf("first page cursor=%s err=%v, want the pair (0, 2)", first["next"], err)
		}
		second := logDraftRaw(t, fx, fmt.Sprintf(`{"epoch":"0","space":%q,"mode":"page","queries":[`+
			`{"kind":"cardlines","abouts":["p"],"fields":["brief"],"limit":2,"cursor":%s}]}`, fx.Space, first["next"]))
		if code := logDraftCode(t, second); code != "" {
			t.Fatalf("resuming inside a line refused %s: the pair cursor's item index was not accepted back", code)
		}
		if !strings.Contains(string(second["items"]), `"id":"m3"`) || strings.Contains(string(second["items"]), `"id":"m1"`) {
			t.Fatalf("second page items=%s, want m3 then the note, no repeat of m1", second["items"])
		}
	})
}

func TestLogBudgetNoPartialAnswer(t *testing.T) {
	t.Parallel()
	t.Run("atomic lines answers a bounded prefix, BUDGET only when no line fits", func(t *testing.T) {
		t.Parallel()
		fx, store := logDraftFixture(t)
		composedWrite(t, store, logRows(fx.Space, "0", "work", "r"))
		composedWrite(t, store, Step{Epoch: "0", Space: fx.Space, Entries: []Entry{{Kind: "create",
			Table: "work", To: "r:a", IDs: []string{"x", "y"}, Scores: []string{"1", "2"}, About: []string{"p", "q"}}}})
		atomic := func(after string, idsLimit int) map[string]json.RawMessage {
			raw := fmt.Sprintf(`{"epoch":"0","space":%q,"queries":[{"kind":"lines","after_seq":%q,"limit":10,"ids_limit":%d}]}`,
				fx.Space, after, idsLimit)
			return logDraftRaw(t, fx, raw)
		}
		prefix := atomic("0", 1)
		var answers []struct {
			Lines []struct {
				Seq string `json:"seq"`
			} `json:"lines"`
			Next    string `json:"next"`
			Through string `json:"through"`
		}
		if err := json.Unmarshal(prefix["answers"], &answers); err != nil || len(answers) != 1 ||
			len(answers[0].Lines) != 1 || answers[0].Lines[0].Seq != "1" || answers[0].Next != "2" || answers[0].Through != "1" {
			t.Fatalf("bounded prefix answer=%s err=%v, want line 1 with next 2 and through 1", prefix["answers"], err)
		}
		if code := logDraftCode(t, atomic("1", 1)); code != "BUDGET" {
			t.Fatalf("a first line over ids_limit gave %q, want BUDGET with no answer", code)
		}
		empty := atomic("2", 10)
		answers = nil
		// Open for revision 2 of the Layer 2 contract: the empty answer's next
		// and through.
		if err := json.Unmarshal(empty["answers"], &answers); err != nil || len(answers) != 1 ||
			len(answers[0].Lines) != 0 || answers[0].Next != "3" || answers[0].Through != "0" {
			t.Fatalf("empty answer=%s err=%v", empty["answers"], err)
		}
	})
	t.Run("a cardlines item over 512 KiB is BUDGET, uncut", func(t *testing.T) {
		t.Parallel()
		fx, store := logDraftFixture(t)
		composedWrite(t, store, logRows(fx.Space, "0", "work", "r"))
		composedWrite(t, store, logNamed(Step{Epoch: "0", Space: fx.Space, Entries: []Entry{},
			Notes: []Note{logNote(`{}`, "p")}}, "budget-note"))
		wide := strings.Repeat("\x01", 65536) // six JSON bytes a character
		composedWrite(t, store, Step{Epoch: "0", Space: fx.Space, Entries: []Entry{{Kind: "create",
			Table: "work", To: "r:a", IDs: []string{"big"}, Scores: []string{"1"}, About: []string{"p"},
			Set: map[string]string{"f1": wide, "f2": wide}}}})
		plan := func(fields, cursor string) string {
			extra := ""
			if cursor != "" {
				extra = `,"cursor":` + cursor
			}
			return fmt.Sprintf(`{"epoch":"0","space":%q,"mode":"page","queries":[{"kind":"cardlines","abouts":["p"],`+
				`"fields":%s,"limit":10%s}]}`, fx.Space, fields, extra)
		}
		first := logDraftRaw(t, fx, plan(`["f1","f2"]`, ""))
		if code := logDraftCode(t, first); code != "" {
			t.Fatalf("first page refused %s, want the fitting note first", code)
		}
		if strings.Contains(string(first["items"]), `"f1"`) || !strings.Contains(string(first["items"]), `"kind":"note"`) {
			t.Fatalf("first page items=%.200s, want only the note", first["items"])
		}
		if code := logDraftCode(t, logDraftRaw(t, fx, plan(`["f1","f2"]`, string(first["next"])))); code != "BUDGET" {
			t.Fatalf("the oversized item gave %q, want BUDGET", code)
		}
		atomic := fmt.Sprintf(`{"epoch":"0","space":%q,"queries":[{"kind":"cardlines","abouts":["p"],"fields":["f1","f2"],"limit":10}]}`, fx.Space)
		if code := logDraftCode(t, logDraftRaw(t, fx, atomic)); code != "BUDGET" {
			t.Fatalf("atomic cardlines with the oversized item gave %q, want BUDGET", code)
		}
		narrow := logDraftRaw(t, fx, plan(`["f1"]`, ""))
		var exhausted bool
		if code := logDraftCode(t, narrow); code != "" || json.Unmarshal(narrow["exhausted"], &exhausted) != nil || !exhausted ||
			!strings.Contains(string(narrow["items"]), `"f1"`) {
			t.Fatalf("a narrower projection is not readable: code=%q exhausted=%s", code, narrow["exhausted"])
		}
	})
}

func TestLogReadRefusesGap(t *testing.T) {
	t.Parallel()
	setup := func(t *testing.T) (*tsetFixture, *RedisStore) {
		fx, store := logDraftFixture(t)
		composedWrite(t, store, logRows(fx.Space, "0", "work", "r"))
		composedWrite(t, store, Step{Epoch: "0", Space: fx.Space, Entries: []Entry{{Kind: "create",
			Table: "work", To: "r:a", IDs: []string{"x"}, Scores: []string{"1"}, About: []string{"p"}}}})
		composedWrite(t, store, logNamed(Step{Epoch: "0", Space: fx.Space, Entries: []Entry{},
			Notes: []Note{logNote(`{}`, "p")}}, "gap-note"))
		return fx, store
	}
	t.Run("a deleted entry is LOGID on every read and on the next write", func(t *testing.T) {
		t.Parallel()
		fx, store := setup(t)
		// Fault injection: XDEL inside the epoch, outside the supported writer.
		if err := fx.Client.XDel(context.Background(), fixtureLogKey(fx.Space, "0"), "2-0").Err(); err != nil {
			t.Fatal(err)
		}
		for name, raw := range map[string]string{
			"lines":     `{"kind":"lines","after_seq":"0","limit":10}`,
			"last":      `{"kind":"last"}`,
			"cardlines": `{"kind":"cardlines","abouts":["p"],"limit":10}`,
		} {
			reply := logDraftRaw(t, fx, fmt.Sprintf(`{"epoch":"0","space":%q,"queries":[%s]}`, fx.Space, raw))
			if code := logDraftCode(t, reply); code != "LOGID" {
				t.Errorf("%s over a hole gave %q, want LOGID", name, code)
			}
		}
		logRefused(t, fx, store, Step{Epoch: "0", Space: fx.Space, Entries: []Entry{{Kind: "move",
			Table: "work", From: "r:a", To: "r:b", IDs: []string{"x"}, About: []string{"p"}}}}, "LOGID")
	})
	t.Run("a deleted log key under live histories is DRIFT", func(t *testing.T) {
		t.Parallel()
		fx, store := setup(t)
		// Fault injection: DEL of the log key, outside the supported writer.
		if err := fx.Client.Del(context.Background(), fixtureLogKey(fx.Space, "0")).Err(); err != nil {
			t.Fatal(err)
		}
		logRefused(t, fx, store, Step{Epoch: "0", Space: fx.Space, Entries: []Entry{{Kind: "move",
			Table: "work", From: "r:a", To: "r:b", IDs: []string{"x"}, About: []string{"p"}}}}, "DRIFT")
	})
	t.Run("n disagreeing with the body is DRIFT", func(t *testing.T) {
		t.Parallel()
		fx, _ := setup(t)
		ctx := context.Background()
		// Fault injection: a gapless line whose n does not match its about set.
		if err := fx.Client.XAdd(ctx, &redis.XAddArgs{Stream: fixtureLogKey(fx.Space, "0"), ID: "4-0",
			Values: []any{"n", "5", "d", `{"k":"n","ms":"1","about":["p"]}`}}).Err(); err != nil {
			t.Fatal(err)
		}
		if err := fx.Client.RPush(ctx, fixtureHistoryKey(fx.Space, "0", "p"), "4").Err(); err != nil {
			t.Fatal(err)
		}
		reply := logDraftRaw(t, fx, fmt.Sprintf(`{"epoch":"0","space":%q,"queries":[{"kind":"cardlines","abouts":["p"],"limit":10}]}`, fx.Space))
		if code := logDraftCode(t, reply); code != "DRIFT" {
			t.Fatalf("cardlines over a bad n gave %q, want DRIFT", code)
		}
	})
}

// TestLineBytesRefuseBeforeWrite and TestSharedWordsStoredOnce.
func TestLogLineBytesAndSharedWords(t *testing.T) {
	t.Parallel()
	create := func(space string, distinct bool) Step {
		ids, scores, about := make([]string, 2000), make([]string, 2000), make([]string, 2000)
		each := make([]map[string]string, 2000)
		for j := range ids {
			ids[j] = fmt.Sprintf("c%04d", j)
			scores[j], about[j] = "1", ids[j]
			each[j] = map[string]string{"brief": fmt.Sprintf("%04d", j) + strings.Repeat("b", 1020)}
		}
		entry := Entry{Kind: "create", Table: "work", To: "r:a", IDs: ids, Scores: scores, About: about}
		if distinct {
			entry.Each = each
		} else {
			entry.Set = map[string]string{"brief": strings.Repeat("s", 1024)}
		}
		return Step{Epoch: "0", Space: space, Entries: []Entry{entry}}
	}
	fx, store := logDraftFixture(t)
	composedWrite(t, store, logRows(fx.Space, "0", "work", "r"))
	refusal := logRefused(t, fx, store, create(fx.Space, true), "LIMIT")
	if refusal.Detail.Budget != "log_line_bytes" || refusal.Detail.Actual == nil || *refusal.Detail.Actual <= 1048576 {
		t.Fatalf("2 MB line refusal detail=%+v, want log_line_bytes over 1 MiB", refusal.Detail)
	}
	reply := composedWrite(t, store, create(fx.Space, false))
	if reply.Lines != 1 || reply.FirstSeq != "2" {
		t.Fatalf("shared brief step reply=%+v, want one line at seq 2", reply)
	}
	lines := logDraftStream(t, fx.Client, fx.Space, "0")
	if len(lines) != 2 || lines[1].N != "2000" || strings.Count(lines[1].D, strings.Repeat("s", 1024)) != 1 ||
		lines[1].Body.Set != nil || len(lines[1].D) >= 1048576 {
		t.Fatalf("shared line n=%s bytes=%d set=%v, want the brief stored once", lines[1].N, len(lines[1].D), lines[1].Body.Set != nil)
	}
}

const logDraftPlanProbeLua = `
redis.register_function('ns_tset_log_plan_probe', function(keys,args)
  local S=NS.tset
  if #keys~=0 or #args~=3 then return S.json.encode(S.refuse('ARGS')) end
  local ctx,err=S.open(args[1],args[2])
  if err then return S.json.encode(err) end
  if ctx.replay then return S.json.encode(S.refuse('REQUEST')) end
  local table_plan;table_plan,err=S.plan(ctx)
  if err then return S.json.encode(err) end
  -- Fault injection of the shared budget: leave less than the XINFO reserve.
  if args[3]=='xinfo_full' then ctx.budget.fetched_bytes=8388608-1048576 end
  local fetched_before=ctx.budget.fetched_bytes
  local log_plan;log_plan,err=NS.tlog.plan(ctx,table_plan)
  if err then return S.json.encode(err) end
  local argv=S.array()
  for i,d in ipairs(log_plan.commands) do
    local a=S.array()
    for j,v in ipairs(d.argv) do a[j]=v end
    argv[i]=a
  end
  return S.json.encode({status='planned',argv=argv,first_seq=log_plan.first_seq,
    last_seq=log_plan.last_seq,line_count=log_plan.line_count,
    about_appends=log_plan.about_appends,note_seqs=log_plan.note_seqs,
    fetched=ctx.budget.fetched_bytes-fetched_before})
end)
`

// TestLogKeyNeverDeletedInEpoch and the descriptor shape: the log plan holds
// only XADD with explicit ascending ids and one RPUSH per history key with
// ascending seqs; note_seqs align with the notes; planning writes nothing.
func TestLogPlanDescriptors(t *testing.T) {
	t.Parallel()
	fx := newComposedTSetFixture(t)
	fx.Define(t, "work", "a", "b")
	fx.ActivateWithLua(t, logDraftPlanProbeLua)
	store := newFixtureRedis(t, fx.Client)
	composedWrite(t, store, logRows(fx.Space, "0", "work", "r"))
	composedWrite(t, store, Step{Epoch: "0", Space: fx.Space, Entries: []Entry{{Kind: "create",
		Table: "work", To: "r:a", IDs: []string{"x", "y"}, Scores: []string{"1", "2"}, About: []string{"p", "q"}}}})
	step := logNamed(Step{Epoch: "0", Space: fx.Space, Entries: []Entry{
		{Kind: "move", Table: "work", From: "r:a", To: "r:b", IDs: []string{"x"}, About: []string{"p"}},
		{Kind: "move", Table: "work", From: "r:a", To: "r:b", IDs: []string{"y"}, About: []string{"q"}},
		{Kind: "create", Table: "work", To: "r:a", IDs: []string{"z"}, Scores: []string{"3"}, About: []string{"p"}},
	}, Notes: []Note{logNote(`{}`, "q", "p"), logNote(`{}`, "p")}}, "probe")
	raw, err := EncodeStep(step)
	if err != nil {
		t.Fatal(err)
	}
	before := commitProbeImage(t, fx.Client)
	probe := func(mode string) string {
		value, err := fx.Client.FCall(context.Background(), "ns_tset_log_plan_probe", []string{}, Version, string(raw), mode).Result()
		if err != nil {
			t.Fatalf("probe %s: %v", mode, err)
		}
		if after := commitProbeImage(t, fx.Client); !reflect.DeepEqual(before, after) {
			t.Fatalf("planning the log (%s) changed the store", mode)
		}
		return value.(string)
	}
	// TestXInfoPayloadCharged: the XINFO reservation (two maximum lines plus
	// metadata) must fit the shared fetched budget; on a write it is LIMIT.
	var refused Refusal
	if err := json.Unmarshal([]byte(probe("xinfo_full")), &refused); err != nil ||
		refused.Code != "LIMIT" || refused.Detail.Budget != "fetched_bytes" {
		t.Fatalf("XINFO over the remaining fetched budget: %+v err=%v, want LIMIT fetched_bytes", refused, err)
	}
	var plan struct {
		Status       string     `json:"status"`
		Argv         [][]string `json:"argv"`
		FirstSeq     string     `json:"first_seq"`
		LastSeq      string     `json:"last_seq"`
		LineCount    int        `json:"line_count"`
		AboutAppends int        `json:"about_appends"`
		NoteSeqs     []string   `json:"note_seqs"`
		Fetched      int        `json:"fetched"`
	}
	value := probe("plan")
	if err := json.Unmarshal([]byte(value), &plan); err != nil || plan.Status != "planned" {
		t.Fatalf("probe reply %v err=%v", value, err)
	}
	if plan.Fetched <= 0 {
		t.Fatalf("XINFO's actual bytes were not charged: fetched=%d", plan.Fetched)
	}
	logKey := fixtureLogKey(fx.Space, "0")
	var want [][]string
	for seq := 3; seq <= 7; seq++ {
		want = append(want, []string{"XADD", logKey, fmt.Sprintf("%d-0", seq), "n"})
	}
	want = append(want, []string{"RPUSH", fixtureHistoryKey(fx.Space, "0", "p"), "3", "5", "6", "7"},
		[]string{"RPUSH", fixtureHistoryKey(fx.Space, "0", "q"), "4", "6"})
	if len(plan.Argv) != len(want) {
		t.Fatalf("log plan has %d commands, want %d: %v", len(plan.Argv), len(want), plan.Argv)
	}
	for i, argv := range plan.Argv {
		switch argv[0] {
		case "XTRIM", "XDEL", "DEL", "UNLINK", "EXPIRE", "PEXPIRE", "EXPIREAT":
			t.Fatalf("log plan deletes or expires: %v", argv)
		}
		if argv[0] == "XADD" {
			if len(argv) != 7 || !reflect.DeepEqual(argv[:4], want[i]) || argv[5] != "d" {
				t.Errorf("command %d=%v, want XADD with explicit id %s", i, argv[:4], want[i][2])
			}
			continue
		}
		if !reflect.DeepEqual(argv, want[i]) {
			t.Errorf("command %d=%v, want %v", i, argv, want[i])
		}
	}
	if plan.FirstSeq != "3" || plan.LastSeq != "7" || plan.LineCount != 5 || plan.AboutAppends != 6 ||
		!reflect.DeepEqual(plan.NoteSeqs, []string{"6", "7"}) {
		t.Fatalf("log plan=%+v", plan)
	}
}

// This read callback mirrors the Sprint seam: S.read with Layer 2's reader
// and one enclosing kind whose read uses L.read_line_at for a line by seq.
const logDraftLineAtProbeLua = `
redis.register_function('ns_tset_lineat_probe', function(keys,args)
  local S=NS.tset
  if #keys~=0 or #args~=2 then return S.json.encode(S.refuse('ARGS')) end
  local extension={kinds={'lineat'},
    validate=function(q,index)
      -- A number passes through, so the fragment's own refusal of one is seen.
      if type(q.seq)~='string' and type(q.seq)~='number' then return nil,S.refuse('REQUEST',{query_index=index}) end
      return true,nil
    end,
    read=function(ctx,q,index)
      local line,err=NS.tlog.read_line_at(ctx,q.seq,index)
      if err then return nil,err end
      return {kind='lineat',seq=line.seq,line_kind=line.kind,ids=line.ids,about=line.about,
        meta=line.meta or cjson.null,log_id=ctx.budget.log_id,fetched=ctx.budget.fetched_bytes},nil
    end}
  return S.read(args[1],args[2],NS.tlog.read,extension)
end)
`

func TestLogReadLineAt(t *testing.T) {
	t.Parallel()
	fx := newComposedTSetFixture(t)
	fx.Define(t, "work", "a", "b")
	fx.ActivateWithLua(t, logDraftLineAtProbeLua)
	store := newFixtureRedis(t, fx.Client)
	composedWrite(t, store, logRows(fx.Space, "0", "work", "r"))
	composedWrite(t, store, Step{Epoch: "0", Space: fx.Space, Entries: []Entry{{Kind: "create",
		Table: "work", To: "r:a", IDs: []string{"x", "y"}, Scores: []string{"1", "2"}, About: []string{"p", "q"},
		Meta: json.RawMessage(`{"m":"v"}`)}}})
	composedWrite(t, store, logNamed(Step{Epoch: "0", Space: fx.Space, Entries: []Entry{},
		Notes: []Note{logNote(`{}`, "p")}}, "lineat-note"))
	call := func(queries string) map[string]json.RawMessage {
		value, err := fx.Client.FCall(context.Background(), "ns_tset_lineat_probe", []string{}, Version,
			fmt.Sprintf(`{"epoch":"0","space":%q,"queries":[%s]}`, fx.Space, queries)).Result()
		if err != nil {
			t.Fatalf("line-at probe: %v", err)
		}
		var out map[string]json.RawMessage
		if err := json.Unmarshal([]byte(value.(string)), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	reply := call(`{"kind":"lineat","seq":"2"},{"kind":"lineat","seq":"2"},{"kind":"lineat","seq":"3"}`)
	var answers []struct {
		Seq      string            `json:"seq"`
		LineKind string            `json:"line_kind"`
		IDs      []string          `json:"ids"`
		About    []string          `json:"about"`
		Meta     map[string]string `json:"meta"`
		LogID    int               `json:"log_id"`
		Fetched  int               `json:"fetched"`
	}
	if err := json.Unmarshal(reply["answers"], &answers); err != nil || len(answers) != 3 {
		t.Fatalf("line-at answers=%s err=%v", reply["answers"], err)
	}
	a := answers[0]
	if a.Seq != "2" || a.LineKind != "create" || !reflect.DeepEqual(a.IDs, []string{"x", "y"}) ||
		!reflect.DeepEqual(a.About, []string{"p", "q"}) || a.Meta["m"] != "v" {
		t.Fatalf("decoded line 2=%+v", a)
	}
	// The second read of seq 2 is a cache hit: no fetch, but its ids are charged.
	if answers[1].Fetched != a.Fetched || answers[1].LogID != a.LogID+2 {
		t.Fatalf("repeat of seq 2 fetched=%d->%d log_id=%d->%d, want no fetch and two more ids",
			a.Fetched, answers[1].Fetched, a.LogID, answers[1].LogID)
	}
	if answers[2].LineKind != "note" || !reflect.DeepEqual(answers[2].About, []string{"p"}) || answers[2].LogID != a.LogID+3 {
		t.Fatalf("decoded note line 3=%+v", answers[2])
	}
	for seq, code := range map[string]string{"0": "REQUEST", "01": "REQUEST", "4": "LOGID", "9": "LOGID"} {
		if got := logDraftCode(t, call(fmt.Sprintf(`{"kind":"lineat","seq":%q}`, seq))); got != code {
			t.Errorf("line at %q gave %q, want %s", seq, got, code)
		}
	}
	// A seq is a canonical decimal string: a Lua number, even of a line that
	// is there, is REQUEST (the sprint's callers pass strings; read 4812 S2).
	for _, seq := range []string{"2", "9"} {
		if got := logDraftCode(t, call(`{"kind":"lineat","seq":`+seq+`}`)); got != "REQUEST" {
			t.Errorf("line at the number %s gave %q, want REQUEST", seq, got)
		}
	}
}

// TestSeqExactMetadataCeiling, draft subset: the live ceiling 2^53-1 applies
// to the stored head (LOGID) and to the lines a step would add (OVERFLOW).
func TestSeqExactMetadataCeiling(t *testing.T) {
	t.Parallel()
	setup := func(t *testing.T, last string) (*tsetFixture, *RedisStore) {
		fx, store := logDraftFixture(t)
		composedWrite(t, store, logRows(fx.Space, "0", "work", "r"))
		// Fault injection: move the stream's metadata to the ceiling's edge.
		if err := fx.Client.Do(context.Background(), "XSETID", fixtureLogKey(fx.Space, "0"), last+"-0",
			"ENTRIESADDED", last).Err(); err != nil {
			t.Fatalf("XSETID: %v", err)
		}
		return fx, store
	}
	t.Run("a step whose second line would pass the ceiling is OVERFLOW", func(t *testing.T) {
		t.Parallel()
		fx, store := setup(t, "9007199254740990")
		logRefused(t, fx, store, Step{Epoch: "0", Space: fx.Space, Entries: []Entry{
			{Kind: "rows", Table: "work", Add: []string{"s"}},
			{Kind: "rows", Table: "aux", Add: []string{"q"}}}}, "OVERFLOW")
	})
	t.Run("a stored head beyond the ceiling is LOGID", func(t *testing.T) {
		t.Parallel()
		fx, store := setup(t, "9007199254740992")
		logRefused(t, fx, store, logRows(fx.Space, "0", "work", "s"), "LOGID")
	})
}

// logRawStep sends one raw tset/1 step to the store and returns its decoded
// reply, so a request the Go encoder would change reaches the Lua as written.
func logRawStep(t *testing.T, fx *tsetFixture, body string) map[string]json.RawMessage {
	t.Helper()
	wire, err := fx.Client.FCall(context.Background(), "ns_tset_step", []string{}, Version, body).Result()
	if err != nil {
		t.Fatalf("Lua step returned Redis error: %v", err)
	}
	var out map[string]json.RawMessage
	if err := json.Unmarshal([]byte(wire.(string)), &out); err != nil {
		t.Fatalf("step reply %q: %v", wire, err)
	}
	return out
}

// logRawRefused sends a raw step, wants the refusal code and entry index,
// and wants the store's TYPE/DUMP image unchanged.
func logRawRefused(t *testing.T, fx *tsetFixture, body, code string, entry int) {
	t.Helper()
	before := commitProbeImage(t, fx.Client)
	reply := logRawStep(t, fx, body)
	var status, got string
	var detail struct {
		EntryIndex *int `json:"entry_index"`
	}
	_ = json.Unmarshal(reply["status"], &status)
	_ = json.Unmarshal(reply["code"], &got)
	_ = json.Unmarshal(reply["detail"], &detail)
	if status != "refused" || got != code || (entry >= 0 && (detail.EntryIndex == nil || *detail.EntryIndex != entry)) {
		t.Fatalf("step %s: reply %v, want %s at entry %d", body, reply, code, entry)
	}
	if after := commitProbeImage(t, fx.Client); !reflect.DeepEqual(before, after) {
		t.Fatalf("a %s refusal changed the Redis TYPE/DUMP image", code)
	}
}

// TestLogLastOverAutoIDHead (read 4812 M1; the model's WShape: the XINFO
// equality, not the <n>-0 shape): a raw XADD * leaves entries-added and
// length agreeing but the last id not <entries-added>-0, and last refuses
// LOGID with the budget last_generated_id. With the equality disabled it
// would answer last_seq "2".
func TestLogLastOverAutoIDHead(t *testing.T) {
	t.Parallel()
	fx, store := logDraftFixture(t)
	composedWrite(t, store, logRows(fx.Space, "0", "work", "r"))
	if err := fx.Client.XAdd(context.Background(), &redis.XAddArgs{Stream: fixtureLogKey(fx.Space, "0"),
		Values: []any{"n", "0", "d", `{"k":"n","ms":"1"}`}}).Err(); err != nil {
		t.Fatal(err)
	}
	raw := logDraftRaw(t, fx, fmt.Sprintf(`{"epoch":"0","space":%q,"queries":[{"kind":"last"}]}`, fx.Space))
	var detail struct {
		Budget string `json:"budget"`
	}
	_ = json.Unmarshal(raw["detail"], &detail)
	if code := logDraftCode(t, raw); code != "LOGID" || detail.Budget != "last_generated_id" {
		t.Fatalf("last over an auto-id head: %q %q, want LOGID last_generated_id", code, detail.Budget)
	}
}

// TestLogMetaHoldsNoNumber (read 4812 choice 5; L2 1.1: a body d holds no
// JSON number): a number anywhere in an entry's or a note's meta is REQUEST,
// before any guard (L1 8: REQUEST's static phase precedes PLACE) and before
// any write, so no value is stored rounded (12345678901234567 would be
// 1.2345678901235e+16). A string of digits is kept.
func TestLogMetaHoldsNoNumber(t *testing.T) {
	t.Parallel()
	fx, store := logDraftFixture(t)
	composedWrite(t, store, logRows(fx.Space, "0", "work", "r", "s"))
	composedWrite(t, store, Step{Epoch: "0", Space: fx.Space, Entries: []Entry{{Kind: "create",
		Table: "work", To: "r:a", IDs: []string{"x"}, Scores: []string{"1"}, About: []string{"p"}}}})
	move := func(from, meta string) string {
		return fmt.Sprintf(`{"epoch":"0","space":%q,"entries":[{"kind":"move","t":"work","from":%q,"to":"s:a","ids":["x"],"about":["p"],"meta":%s}]}`,
			fx.Space, from, meta)
	}
	for _, meta := range []string{`{"n":12345678901234567}`, `{"n":1e300}`, `{"a":{"b":[true,null,"s",0]}}`, `{"z":-1}`} {
		logRawRefused(t, fx, move("r:a", meta), "REQUEST", 0)
	}
	// The single fault alone is PLACE; with a number in meta beside it,
	// REQUEST comes first.
	logRawRefused(t, fx, move("s:a", `{"n":"1"}`), "PLACE", 0)
	logRawRefused(t, fx, move("s:a", `{"n":12345678901234567}`), "REQUEST", 0)
	note := func(meta string) string {
		return fmt.Sprintf(`{"epoch":"0","space":%q,"op":"meta-note","intent":"i","entries":[],"notes":[{"line":{"kind":"note","meta":%s},"about":["p"]}]}`,
			fx.Space, meta)
	}
	logRawRefused(t, fx, note(`{"v":1e300}`), "REQUEST", -1)
	if got := logRawStep(t, fx, note(`{"v":"12345678901234567"}`)); string(got["status"]) != `"ok"` {
		t.Fatalf("a note with digits in a string: %v", got)
	}
	lines := logDraftStream(t, fx.Client, fx.Space, "0")
	if last := lines[len(lines)-1]; string(last.Body.Meta) != `{"v":"12345678901234567"}` {
		t.Fatalf("the stored meta %s", last.Body.Meta)
	}
}

// TestLogAboutBeforeGuards (read 4812 choice 6; L1 8's fixed precedence): a
// member-changing entry with no about is REQUEST in the static phase, so a
// wrong from beside it does not turn it into PLACE.
func TestLogAboutBeforeGuards(t *testing.T) {
	t.Parallel()
	fx, store := logDraftFixture(t)
	composedWrite(t, store, logRows(fx.Space, "0", "work", "r", "s"))
	composedWrite(t, store, Step{Epoch: "0", Space: fx.Space, Entries: []Entry{{Kind: "create",
		Table: "work", To: "r:a", IDs: []string{"x"}, Scores: []string{"1"}, About: []string{"p"}}}})
	logRefused(t, fx, store, Step{Epoch: "0", Space: fx.Space, Entries: []Entry{{Kind: "move",
		Table: "work", From: "s:a", To: "r:b", IDs: []string{"x"}, About: []string{"p"}}}}, "PLACE")
	ref := logRefused(t, fx, store, Step{Epoch: "0", Space: fx.Space, Entries: []Entry{
		{Kind: "rows", Table: "work", Add: []string{"t"}},
		{Kind: "move", Table: "work", From: "s:a", To: "r:b", IDs: []string{"x"}}}}, "REQUEST")
	if ref.Detail.EntryIndex == nil || *ref.Detail.EntryIndex != 1 || ref.Detail.Table != "work" {
		t.Fatalf("the refusal's detail %+v, want entry 1 of work", ref.Detail)
	}
}

// TestLogLinesBytesLimit (read 4812 choice 9; decision 6: lines takes an
// optional bytes_limit, bounded by the 8 MiB reply, the envelope reserved
// first): a bytes_limit stops an atomic answer at a whole line, the store
// and the twin return the same prefix, and a bytes_limit past 8 MiB is LIMIT,
// 0 is REQUEST, and one too small for a line is BUDGET.
func TestLogLinesBytesLimit(t *testing.T) {
	t.Parallel()
	fx, store := logDraftFixture(t)
	twin := NewMemLog()
	mem := NewMem()
	for _, table := range []string{"work", "aux"} {
		cols := map[string][]string{"work": {"a", "b"}, "aux": {"c"}}[table]
		if err := mem.DefineTable(fx.Space, table, TableDefinition{Columns: cols,
			MemberPrefix: fx.Space + "member:" + table + ":", EpochKey: fx.Space + "sprint:epoch", EpochField: "n"}); err != nil {
			t.Fatal(err)
		}
	}
	w := &logWorld{t: t, fx: fx, store: store, mem: mem, log: twin}
	w.apply(logRows(fx.Space, "0", "work", "r"))
	for i := 0; i < 6; i++ {
		id := fmt.Sprintf("m%d", i)
		w.apply(Step{Epoch: "0", Entries: []Entry{{Kind: "create", Table: "work", To: "r:a", IDs: []string{id},
			Scores: []string{"1"}, About: []string{"p"}, Set: map[string]string{"v": strings.Repeat("x", 400)}}}})
	}
	both := func(q ReadQuery) (ReadReply, ReadReply, string, string) {
		plan := ReadPlan{Epoch: "0", Space: fx.Space, Queries: []ReadQuery{q}}
		got, err := store.Read(context.Background(), plan)
		want, ref := twin.Read(fx.Space, plan)
		var refusal *Refusal
		storeCode, twinCode := "", ""
		if errors.As(err, &refusal) {
			storeCode = refusal.Code
		} else if err != nil {
			t.Fatal(err)
		}
		if ref != nil {
			twinCode = ref.Code
		}
		return got, want, storeCode, twinCode
	}
	got, want, sc, tc := both(ReadQuery{Kind: "lines", AfterSeq: "0", Limit: 50, BytesLimit: 4096 + 1200})
	if sc != "" || tc != "" || len(got.Answers) != 1 || len(want.Answers) != 1 {
		t.Fatalf("bytes_limit: store %q twin %q", sc, tc)
	}
	if n := len(got.Answers[0].Lines); n < 1 || n >= 7 {
		t.Fatalf("a 1,200-byte room returned %d of 7 lines", n)
	}
	if !reflect.DeepEqual(logSemantic(t, got.Answers[0]), logSemantic(t, want.Answers[0])) {
		t.Fatalf("store %s\ntwin %s", logSemantic(t, got.Answers[0]), logSemantic(t, want.Answers[0]))
	}
	// A room of the envelope alone fits no line: BUDGET on both.
	if _, _, sc, tc := both(ReadQuery{Kind: "lines", AfterSeq: "0", Limit: 50, BytesLimit: 4096}); sc != "BUDGET" || tc != "BUDGET" {
		t.Errorf("bytes_limit 4096: store %q twin %q, want BUDGET", sc, tc)
	}
	// The Go encoder and the Lua validator bound it alike.
	if _, _, sc, _ := both(ReadQuery{Kind: "lines", AfterSeq: "0", Limit: 50, BytesLimit: 8388609}); sc != "LIMIT" {
		t.Errorf("bytes_limit 8388609 through the Go encoder: %q, want LIMIT", sc)
	}
	for limit, code := range map[string]string{"8388609": "LIMIT", "0": "REQUEST", "-1": "REQUEST", "1.5": "REQUEST", `"9"`: "REQUEST"} {
		raw := logDraftRaw(t, fx, fmt.Sprintf(`{"epoch":"0","space":%q,"queries":[{"kind":"lines","after_seq":"0","limit":5,"bytes_limit":%s}]}`, fx.Space, limit))
		if got := logDraftCode(t, raw); got != code {
			t.Errorf("raw bytes_limit %s: %q, want %s", limit, got, code)
		}
	}
}

// logSemantic is a value as decoded JSON, for comparing answers whose object
// keys the store and the twin order differently.
func logSemantic(t *testing.T, v any) any {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.UseNumber()
	var out any
	if err := dec.Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out
}
