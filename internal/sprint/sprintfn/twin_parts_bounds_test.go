package sprintfn

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	lua "github.com/yuin/gopher-lua"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/tset"
)

// The bounds of the parts, at their edges (the read of IT16, findings 2, 3, 10
// and the probes 3, 6 and 7): every bound a part refuses at is tested at N and
// at N + 1 on the twin, the Lua half's constants are held equal to the Go
// constants, and the differential runs the two halves on the same edge requests
// so that a constant wrong in one half only is found. The reads and the shares
// of Layer 1 are kept by the stub, so a cost understated in either half fails
// here, and the number of reads a part makes is counted.

// stepOutcome is "applied", or the code a step was refused with.
func stepOutcome(t *testing.T, tw *Twin, req *Request) string {
	t.Helper()
	res, err := Step(context.Background(), tw, req)
	var ref *Refusal
	switch {
	case err != nil:
		if !errors.As(err, &ref) {
			t.Fatalf("step error %v, want a refusal", err)
		}
		return ref.Code
	case res.Err != nil:
		t.Fatalf("step error %v", res.Err)
	case res.Refusal != nil:
		return res.Refusal.Code
	}
	return "applied"
}

func rep(n int, s string) string { return strings.Repeat(s, n) }

// names returns n distinct stream or key names with a prefix.
func names(prefix string, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = prefix + strconv.Itoa(i)
	}
	return out
}

func opMap(keys []string, op string) map[string]string {
	m := make(map[string]string, len(keys))
	for _, k := range keys {
		m[k] = op
	}
	return m
}

func parkedN(n int) []ParkedKey {
	var out []ParkedKey
	for _, k := range names("k", n) {
		out = append(out, ParkedKey{Key: k, Code: "LIMIT"})
	}
	return out
}

func quarantinedN(n int) []Quarantined {
	var out []Quarantined
	for _, id := range names("p", n) {
		out = append(out, Quarantined{ID: id, Code: "DRIFT"})
	}
	return out
}

// quarantineOfSize is a card whose record is exactly n bytes.
func quarantineOfSize(id string, n int) Quarantined {
	q := Quarantined{ID: id, Code: "C"}
	// The record is "C", a tab, the empty rule, a tab, the empty stream: 3
	// bytes; each cell adds a tab and its bytes (1 to 256).
	for remaining := n - 3; remaining > 0; {
		chunk := min(remaining, tset.MaxIdentifierBytes+1)
		if remaining-chunk == 1 {
			chunk--
		}
		q.Cells = append(q.Cells, rep(chunk-1, "x"))
		remaining -= chunk
	}
	if got := len(quarantineValue(q)); got != n {
		panic("a record of " + strconv.Itoa(n) + " bytes was built as " + strconv.Itoa(got))
	}
	return q
}

// counterNames are the n fields of {p}next@e a counter change can name, in the
// order of their streams: score, streams, then id:<s> and gate:<s> for each.
func counterNames(n int) []string {
	out := []string{"score", "streams"}
	for i := 0; len(out) < n; i++ {
		out = append(out, "id:s"+strconv.Itoa(i))
		if len(out) < n {
			out = append(out, "gate:s"+strconv.Itoa(i))
		}
	}
	return out
}

func counterOf(read, set int) *CounterChange {
	c := &CounterChange{Read: map[string]string{}, Set: map[string]string{}}
	for i, f := range counterNames(read) {
		c.Read[f] = ""
		if i < set {
			c.Set[f] = "1"
		}
	}
	return c
}

// leasedTwin is a twin whose lease the token "tok" holds at generation 1.
func leasedTwin(t *testing.T) (*Twin, *stepClock) {
	t.Helper()
	tw, _, _, clk := partsTwin(t)
	mustStep(t, tw, leaseReq("tok", "run", 600000, nil))
	return tw, clk
}

// edge is one bound: a request at the bound, which applies, and one past it,
// which is refused with code.
type edge struct {
	name    string
	setup   func(t *testing.T, tw *Twin, clk *stepClock)
	ok, bad func() *Request
	code    string // REQUEST unless given
}

func setClock(clk *stepClock, ms int64) {
	clk.mu.Lock()
	defer clk.mu.Unlock()
	clk.t = time.UnixMilli(ms)
}

func edges() []edge {
	leased := func(t *testing.T, tw *Twin, clk *stepClock) { mustStep(t, tw, leaseReq("tok", "run", 600000, nil)) }
	beatOf := func(n int) func() *Request { return func() *Request { return beatReq(names("m", n)...) } }
	loadOf := func(n int) func() *Request {
		return func() *Request {
			r := beatReq("m1")
			r.Beat.Members[0].Load = rep(n, "l")
			return r
		}
	}
	sp := func(p *SprintPart) func() *Request { return func() *Request { return sprintReq(p) } }
	lease := func(owner, name string, hold int64, hb map[string]string) func() *Request {
		return func() *Request { return leaseReq(owner, name, hold, hb) }
	}
	// the clock at the top of the exact range, where until_ms must stay exact
	topOfTime := func(t *testing.T, tw *Twin, clk *stepClock) { setClock(clk, maxExactMS-5) }
	bounds := []edge{
		{name: "lease hold at the cap", ok: lease("t", "n", LeaseHoldMaxMS, nil), bad: lease("t", "n", LeaseHoldMaxMS+1, nil)},
		{name: "lease hold at one ms", ok: lease("t", "n", 1, nil), bad: lease("t", "n", 0, nil)},
		{name: "lease hold that stays exact", setup: topOfTime, ok: lease("t", "n", 5, nil), bad: lease("t", "n", 6, nil)},
		{name: "lease owner bytes", ok: lease(rep(256, "o"), "n", 5000, nil), bad: lease(rep(257, "o"), "n", 5000, nil)},
		{name: "lease name bytes", ok: lease("t", rep(256, "n"), 5000, nil), bad: lease("t", rep(257, "n"), 5000, nil)},
		{name: "lease name is given", ok: lease("t", "n", 5000, nil), bad: lease("t", "", 5000, nil)},
		{name: "heartbeat value bytes", ok: lease("t", "n", 5000, map[string]string{"error": rep(HeartbeatValueBytesMax, "e")}),
			bad: lease("t", "n", 5000, map[string]string{"error": rep(HeartbeatValueBytesMax+1, "e")})},
		{name: "beat members", ok: beatOf(SprintMembersMax), bad: beatOf(SprintMembersMax + 1)},
		{name: "beat member id bytes", ok: func() *Request { return beatReq(rep(128, "a")) }, bad: func() *Request { return beatReq(rep(129, "a")) }},
		{name: "beat load bytes", ok: loadOf(BeatLoadBytesMax), bad: loadOf(BeatLoadBytesMax + 1)},
		{name: "pop limit", setup: leased, ok: func() *Request { return popReq(1, PopMax) }, bad: func() *Request { return popReq(1, PopMax+1) }},
		{name: "pop limit of one", setup: leased, ok: func() *Request { return popReq(1, 1) }, bad: func() *Request { return popReq(1, 0) }},
		{name: "ingest cursor at the top of the exact range", setup: leased,
			ok:  func() *Request { return ingestReq(1, "0", strconv.FormatUint(maxExactSeq, 10)) },
			bad: func() *Request { return ingestReq(1, "0", strconv.FormatUint(maxExactSeq+1, 10)) }},
		{name: "ingest key bytes", setup: leased,
			ok:  func() *Request { return ingestReq(1, "0", "1", sprint.AgendaKey{Key: rep(256, "k"), Seq: 1}) },
			bad: func() *Request { return ingestReq(1, "0", "1", sprint.AgendaKey{Key: rep(257, "k"), Seq: 1}) }},
		{name: "dropping streams", ok: sp(&SprintPart{Dropping: opMap(names("s", SprintMembersMax), "op")}),
			bad: sp(&SprintPart{Dropping: opMap(names("s", SprintMembersMax+1), "op")})},
		{name: "undropping streams", ok: sp(&SprintPart{Undrop: opMap(names("s", SprintMembersMax), "op")}),
			bad: sp(&SprintPart{Undrop: opMap(names("s", SprintMembersMax+1), "op")})},
		{name: "streams marked and unmarked together", ok: sp(&SprintPart{Dropping: opMap(names("a", 125), "op"), Undrop: opMap(names("b", 125), "op")}),
			bad: sp(&SprintPart{Dropping: opMap(names("a", 125), "op"), Undrop: opMap(names("b", 126), "op")})},
		{name: "stream id bytes", ok: sp(&SprintPart{Dropping: map[string]string{rep(128, "s"): "op"}}),
			bad: sp(&SprintPart{Dropping: map[string]string{rep(129, "s"): "op"}})},
		{name: "dropping op bytes", ok: sp(&SprintPart{Dropping: map[string]string{"s": rep(256, "o")}}),
			bad: sp(&SprintPart{Dropping: map[string]string{"s": rep(257, "o")}})},
		{name: "parked keys", ok: sp(&SprintPart{Park: parkedN(SprintKeysMax)}), bad: sp(&SprintPart{Park: parkedN(SprintKeysMax + 1)})},
		{name: "unparked keys", ok: sp(&SprintPart{Unpark: names("k", SprintKeysMax)}), bad: sp(&SprintPart{Unpark: names("k", SprintKeysMax+1)})},
		{name: "parked key bytes", ok: sp(&SprintPart{Park: []ParkedKey{{Key: rep(256, "k"), Code: "C"}}}),
			bad: sp(&SprintPart{Park: []ParkedKey{{Key: rep(257, "k"), Code: "C"}}})},
		{name: "parked code bytes", ok: sp(&SprintPart{Park: []ParkedKey{{Key: "k", Code: rep(256, "c")}}}),
			bad: sp(&SprintPart{Park: []ParkedKey{{Key: "k", Code: rep(257, "c")}}})},
		{name: "parked size is a decimal", ok: sp(&SprintPart{Park: []ParkedKey{{Key: "k", Code: "C", Actual: "18446744073709551615"}}}),
			bad: sp(&SprintPart{Park: []ParkedKey{{Key: "k", Code: "C", Actual: "18446744073709551616"}}})},
		{name: "parked limit is a decimal", ok: sp(&SprintPart{Park: []ParkedKey{{Key: "k", Code: "C", Limit: "18446744073709551615"}}}),
			bad: sp(&SprintPart{Park: []ParkedKey{{Key: "k", Code: "C", Limit: "18446744073709551616"}}})},
		{name: "parked size has no leading zero", ok: sp(&SprintPart{Park: []ParkedKey{{Key: "k", Code: "C", Actual: "10"}}}),
			bad: sp(&SprintPart{Park: []ParkedKey{{Key: "k", Code: "C", Actual: "010"}}})},
		{name: "parked limit has no leading zero", ok: sp(&SprintPart{Park: []ParkedKey{{Key: "k", Code: "C", Limit: "10"}}}),
			bad: sp(&SprintPart{Park: []ParkedKey{{Key: "k", Code: "C", Limit: "010"}}})},
		// The parked key's rule and budget: each a name, at most one name long (the
		// recheck of IT16, finding 1; the mutations that removed either check
		// passed the suite). A record is at most ParkedValueBytesMax because each of
		// its text fields is bounded here, so a check that goes makes every later
		// pop and ingest read of {p}parked@e refuse DRIFT.
		{name: "parked rule bytes", ok: sp(&SprintPart{Park: []ParkedKey{{Key: "k", Code: "C", Rule: rep(256, "r")}}}),
			bad: sp(&SprintPart{Park: []ParkedKey{{Key: "k", Code: "C", Rule: rep(257, "r")}}})},
		{name: "parked budget bytes", ok: sp(&SprintPart{Park: []ParkedKey{{Key: "k", Code: "C", Budget: rep(256, "b")}}}),
			bad: sp(&SprintPart{Park: []ParkedKey{{Key: "k", Code: "C", Budget: rep(257, "b")}}})},
		{name: "unparked key bytes", ok: sp(&SprintPart{Unpark: []string{rep(256, "k")}}), bad: sp(&SprintPart{Unpark: []string{rep(257, "k")}})},
		{name: "quarantined cards", ok: sp(&SprintPart{Quarantine: quarantinedN(QuarantineMax)}),
			bad: sp(&SprintPart{Quarantine: quarantinedN(QuarantineMax + 1)})},
		{name: "quarantine record bytes", ok: func() *Request {
			return sprintReq(&SprintPart{Quarantine: []Quarantined{quarantineOfSize("p1", QuarantineValueBytesMax)}})
		}, bad: func() *Request {
			return sprintReq(&SprintPart{Quarantine: []Quarantined{quarantineOfSize("p1", QuarantineValueBytesMax+1)}})
		}},
		{name: "quarantine id bytes", ok: sp(&SprintPart{Quarantine: []Quarantined{{ID: rep(256, "p"), Code: "D"}}}),
			bad: sp(&SprintPart{Quarantine: []Quarantined{{ID: rep(257, "p"), Code: "D"}}})},
		{name: "quarantine code bytes", ok: sp(&SprintPart{Quarantine: []Quarantined{{ID: "p1", Code: rep(256, "c")}}}),
			bad: sp(&SprintPart{Quarantine: []Quarantined{{ID: "p1", Code: rep(257, "c")}}})},
		{name: "quarantine rule bytes", ok: sp(&SprintPart{Quarantine: []Quarantined{{ID: "p1", Code: "D", Rule: rep(256, "r")}}}),
			bad: sp(&SprintPart{Quarantine: []Quarantined{{ID: "p1", Code: "D", Rule: rep(257, "r")}}})},
		{name: "quarantine stream bytes", ok: sp(&SprintPart{Quarantine: []Quarantined{{ID: "p1", Code: "D", Stream: rep(256, "s")}}}),
			bad: sp(&SprintPart{Quarantine: []Quarantined{{ID: "p1", Code: "D", Stream: rep(257, "s")}}})},
		{name: "quarantine cell bytes", ok: sp(&SprintPart{Quarantine: []Quarantined{{ID: "p1", Code: "D", Cells: []string{rep(256, "c")}}}}),
			bad: sp(&SprintPart{Quarantine: []Quarantined{{ID: "p1", Code: "D", Cells: []string{rep(257, "c")}}}})},
		{name: "counter fields read", ok: sp(&SprintPart{Counter: counterOf(CounterFieldsMax, 1)}),
			bad: sp(&SprintPart{Counter: counterOf(CounterFieldsMax+1, 1)})},
		{name: "counter fields set", ok: sp(&SprintPart{Counter: counterOf(CounterFieldsMax, CounterFieldsMax)}),
			bad: sp(&SprintPart{Counter: counterOf(CounterFieldsMax+1, CounterFieldsMax+1)})},
		{name: "coordinator bytes", ok: sp(&SprintPart{Coordinator: rep(256, "c")}), bad: sp(&SprintPart{Coordinator: rep(257, "c")})},
		{name: "time due entries", ok: func() *Request { return timeReq(&SprintTime{Due: dueN(SprintKeysMax)}) },
			bad: func() *Request { return timeReq(&SprintTime{Due: dueN(SprintKeysMax + 1)}) }},
		{name: "time due key bytes", ok: func() *Request { return timeReq(&SprintTime{Due: []DueAt{{Key: rep(256, "k"), At: "1"}}}) },
			bad: func() *Request { return timeReq(&SprintTime{Due: []DueAt{{Key: rep(257, "k"), At: "1"}}}) }},
		{name: "time due at the top of the exact range", ok: func() *Request { return timeReq(&SprintTime{Due: []DueAt{{Key: "k", At: "9007199254740991"}}}) },
			bad: func() *Request { return timeReq(&SprintTime{Due: []DueAt{{Key: "k", At: "9007199254740992"}}}) }},
		{name: "time goal claims", ok: func() *Request { return timeReq(&SprintTime{Goals: claimsN(SprintMembersMax)}) },
			bad: func() *Request { return timeReq(&SprintTime{Goals: claimsN(SprintMembersMax + 1)}) }},
		{name: "time goal person bytes", ok: func() *Request { return timeReq(&SprintTime{Goals: []GoalClaim{{Person: rep(128, "p"), R: "1"}}}) },
			bad: func() *Request { return timeReq(&SprintTime{Goals: []GoalClaim{{Person: rep(129, "p"), R: "1"}}}) }},
		{name: "tick end backlog", ok: sp(&SprintPart{TickEnd: &TickEnd{Backlog: "18446744073709551615"}}),
			bad: sp(&SprintPart{TickEnd: &TickEnd{Backlog: "18446744073709551616"}})},
	}
	return append(bounds, textEdges()...)
}

// textField is one place a part stores text: the Go part holds it to partText
// and the Lua part to valid_text, which is one name long, valid UTF-8 and free
// of control characters, so that the delimiters of a stored record (a tab
// between the fields, a line between records) can never be part of a value.
// req builds the request of a single part whose field holds v.
type textField struct {
	name  string
	setup func(t *testing.T, tw *Twin, clk *stepClock)
	req   func(v string) *Request
}

// textFields is every place the Lua reads with valid_text or optional_text,
// each with its Go twin: one entry per call site, so a check dropped at one of
// them is found at that one.
func textFields() []textField {
	leased := func(t *testing.T, tw *Twin, clk *stepClock) { mustStep(t, tw, leaseReq("tok", "run", 600000, nil)) }
	park := func(k ParkedKey) *Request { return sprintReq(&SprintPart{Park: []ParkedKey{k}}) }
	quarantine := func(q Quarantined) *Request { return sprintReq(&SprintPart{Quarantine: []Quarantined{q}}) }
	return []textField{
		{name: "lease owner", req: func(v string) *Request { return leaseReq(v, "n", 5000, nil) }},
		{name: "lease name", req: func(v string) *Request { return leaseReq("t", v, 5000, nil) }},
		{name: "ingest key", setup: leased, req: func(v string) *Request {
			return ingestReq(1, "0", "1", sprint.AgendaKey{Key: v, Seq: 1})
		}},
		{name: "quarantine id", req: func(v string) *Request { return quarantine(Quarantined{ID: v, Code: "D"}) }},
		{name: "quarantine code", req: func(v string) *Request { return quarantine(Quarantined{ID: "p1", Code: v}) }},
		{name: "quarantine rule", req: func(v string) *Request { return quarantine(Quarantined{ID: "p1", Code: "D", Rule: v}) }},
		{name: "quarantine stream", req: func(v string) *Request { return quarantine(Quarantined{ID: "p1", Code: "D", Stream: v}) }},
		{name: "quarantine cell", req: func(v string) *Request {
			return quarantine(Quarantined{ID: "p1", Code: "D", Cells: []string{"c1", v}})
		}},
		{name: "parked key", req: func(v string) *Request { return park(ParkedKey{Key: v, Code: "C"}) }},
		{name: "parked code", req: func(v string) *Request { return park(ParkedKey{Key: "k", Code: v}) }},
		{name: "parked rule", req: func(v string) *Request { return park(ParkedKey{Key: "k", Code: "C", Rule: v}) }},
		{name: "parked budget", req: func(v string) *Request { return park(ParkedKey{Key: "k", Code: "C", Budget: v}) }},
		{name: "unparked key", req: func(v string) *Request { return sprintReq(&SprintPart{Unpark: []string{v}}) }},
		{name: "dropping op", req: func(v string) *Request { return sprintReq(&SprintPart{Dropping: map[string]string{"s": v}}) }},
		{name: "undropping op", req: func(v string) *Request { return sprintReq(&SprintPart{Undrop: map[string]string{"s": v}}) }},
		{name: "coordinator", req: func(v string) *Request { return sprintReq(&SprintPart{Coordinator: v}) }},
	}
}

// controlCases are the bytes a stored text refuses, each with the nearest byte
// it accepts: the record's delimiters (tab, newline), the carriage return and
// NUL, and the two edges of the control range, 0x1f (the last under the space,
// which is accepted) and 0x7f (DEL, the one past the last printable byte 0x7e,
// which is accepted). The byte stands in the middle of the value.
var controlCases = []struct{ name, bad, ok string }{
	{"a tab", "\t", " "},
	{"a newline", "\n", " "},
	{"a carriage return", "\r", " "},
	{"a NUL", "\x00", " "},
	{"byte 0x1f", "\x1f", " "},
	{"byte 0x7f", "\x7f", "~"},
}

// textEdges is an edge for each text field and each control case, and a tab at
// the start and at the end of each field (a check that reads all but the first or
// the last byte passes the middle one). Both halves are run on them.
func textEdges() []edge {
	var out []edge
	for _, f := range textFields() {
		f := f
		add := func(what, ok, bad string) {
			out = append(out, edge{name: f.name + " with " + what, setup: f.setup,
				ok: func() *Request { return f.req(ok) }, bad: func() *Request { return f.req(bad) }})
		}
		for _, c := range controlCases {
			add(c.name, "a"+c.ok+"b", "a"+c.bad+"b")
		}
		add("a tab first", " ab", "\tab")
		add("a tab last", "ab ", "ab\t")
	}
	return out
}

func (e edge) twin(t *testing.T) (*Twin, *stepClock) {
	t.Helper()
	tw, _, _, clk := partsTwin(t)
	fleetSeed(t, tw)
	if e.setup != nil {
		e.setup(t, tw, clk)
	}
	return tw, clk
}

// TestPartBoundsAtTheirEdges (finding 2; probe 3): every bound a part refuses
// at is tested at N, which applies, and at N + 1, which is REQUEST and leaves
// the twin as it was: an off-by-one in a check, which no random walk reaches,
// fails here.
func TestPartBoundsAtTheirEdges(t *testing.T) {
	t.Parallel()
	for _, e := range edges() {
		t.Run(e.name, func(t *testing.T) {
			t.Parallel()
			tw, _ := e.twin(t)
			if got := stepOutcome(t, tw, e.ok()); got != "applied" {
				t.Fatalf("at the bound: %s, want the step applied", got)
			}
			tw, _ = e.twin(t)
			before := tw.SprintKeys()
			want := e.code
			if want == "" {
				want = CodeRequest
			}
			if got := stepOutcome(t, tw, e.bad()); got != want {
				t.Fatalf("past the bound: %s, want %s", got, want)
			}
			if len(changedKeys(before, tw.SprintKeys())) != 0 {
				t.Fatal("a refused step wrote")
			}
		})
	}
}

// TestPartsLuaMatchesTwinAtTheEdges (finding 2; probe 7): the two halves, run
// on the requests of every edge above and on the state the twin has, give the
// same reply, the same commands and the same refusal, so a constant that is
// wrong in the Lua half only is found even where the walk never goes. The
// request at the bound is planned, and the one past it is refused with the
// code of the edge by the two halves: the Lua half is seen to refuse, not only
// to do what the Go half does.
func TestPartsLuaMatchesTwinAtTheEdges(t *testing.T) {
	t.Parallel()
	for _, e := range edges() {
		t.Run(e.name, func(t *testing.T) {
			t.Parallel()
			h := newLuaParts(t)
			want := e.code
			if want == "" {
				want = CodeRequest
			}
			tw, clk := e.twin(t)
			out, _ := diffParts(t, h, tw, clk, e.ok(), nil)
			if ref := firstRefusal(out); ref != nil {
				t.Fatalf("at the bound the parts refused %s %q", ref.Code, ref.Message)
			}
			tw, clk = e.twin(t)
			out, _ = diffParts(t, h, tw, clk, e.bad(), nil)
			if ref := firstRefusal(out); ref == nil || ref.Code != want {
				t.Fatalf("past the bound the parts gave %v, want a refusal %s", ref, want)
			}
		})
	}
}

// firstRefusal is the refusal of the first part, in part order, that refused.
func firstRefusal(out map[string]luaResult) *Refusal {
	for _, name := range PartOrder {
		if r, ok := out[name]; ok && r.refusal != nil {
			return r.refusal
		}
	}
	return nil
}

// diffParts runs every part the request carries through the Go part and the Lua
// part on the twin's present state and requires the same result of the two, one
// State and one ctx for the parts of the request together, as a step has them.
// It returns the Go results by part and the Lua ctx.
func diffParts(t *testing.T, h *luaParts, tw *Twin, clk *stepClock, req *Request, shares *partShares) (map[string]luaResult, *lua.LTable) {
	t.Helper()
	st := tw.partState(clk, req.Epoch)
	st.shares = shares
	obs, ref := tw.before(context.Background(), st, req)
	if ref != nil {
		t.Fatalf("before refused %v", ref)
	}
	ctx := h.newCtx(tw, clk, req)
	if shares != nil {
		withShares(h, ctx, shares.commands, shares.argvBytes)
	}
	out := map[string]luaResult{}
	for _, name := range PartOrder {
		if !requestPart(req, name) {
			continue
		}
		g := goResultAt(tw, st, name, req, obs)
		l := h.run(name, req, tw, clk, obs, ctx)
		if diff := sameResult(g, l); diff != "" {
			t.Fatalf("part %s: %s", name, diff)
		}
		out[name] = g
	}
	return out, ctx
}

// luaConstant reads the `local NAME = 123` lines of the parts file.
var luaConstant = regexp.MustCompile(`(?m)^\s*local ([A-Z][A-Z0-9_]*)\s*=\s*([0-9]+)\b`)

// TestPartsLuaConstantsMatchGo (finding 2; probes 6 and 7): every number the
// Lua file names is the Go constant of the same bound, and a number the Lua file
// names that no Go constant mirrors is a failure, so a constant cannot be added
// to one half only. The differential reaches the bounds a request can reach; the
// shares of the planned commands and argv bytes, the pieces of a command and the
// read reservation are reached through these numbers.
func TestPartsLuaConstantsMatchGo(t *testing.T) {
	t.Parallel()
	f, err := fn.SprintPartsFragment()
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]int64{
		"BEAT_FRESH_MS":      BeatFreshMS,
		"BEHIND_SPAN_MS":     BehindSpanMS,
		"MEMBERS_MAX":        SprintMembersMax,
		"HELD_QUEUE_CAP":     HeldQueueCap,
		"HELD_DROP_MAX":      HeldDropMax,
		"POP_MAX":            PopMax,
		"LEASE_HOLD_MAX":     LeaseHoldMaxMS,
		"MAX_EXACT":          maxExactMS,
		"NAME_BYTES":         tset.MaxIdentifierBytes,
		"FIELD_VALUE_BYTES":  HeartbeatValueBytesMax,
		"RESULT_BYTES":       QuarantineValueBytesMax,
		"MAX_PIECES":         maxPieces,
		"SPRINT_KEYS_MAX":    SprintKeysMax,
		"QUARANTINE_MAX":     QuarantineMax,
		"COUNTER_FIELDS_MAX": CounterFieldsMax,
		"PARKED_VALUE_BYTES": ParkedValueBytesMax,
		"QUARANTINE_CHUNK":   quarantineChunk,
		"COMMANDS_SHARE":     partCommandsShare,
		"ARGV_SHARE":         partArgvShare,
		"RANGE_RESERVE":      RangeReserveBytes,
	}
	got := map[string]int64{}
	for _, m := range luaConstant.FindAllStringSubmatch(f.Source, -1) {
		n, err := strconv.ParseInt(m[2], 10, 64)
		if err != nil {
			t.Fatalf("constant %s: %v", m[1], err)
		}
		if _, dup := got[m[1]]; dup {
			t.Errorf("the Lua file declares %s twice", m[1])
		}
		got[m[1]] = n
	}
	for name, w := range want {
		g, ok := got[name]
		switch {
		case !ok:
			t.Errorf("the Lua file has no constant %s (Go: %d)", name, w)
		case g != w:
			t.Errorf("Lua %s = %d, Go = %d", name, g, w)
		}
	}
	var extra []string
	for name := range got {
		if _, ok := want[name]; !ok {
			extra = append(extra, name)
		}
	}
	sort.Strings(extra)
	if len(extra) != 0 {
		t.Errorf("the Lua file names numbers no Go constant mirrors: %v", extra)
	}
}

// TestPartShareAtItsEdges (finding 3; probe 6): the parts' share of L1 6's
// planned commands and argv bytes is held at N and N + 1, and the running total
// of the parts of one step, not each part's own, is what is held (finding 10).
func TestPartShareAtItsEdges(t *testing.T) {
	t.Parallel()
	tw, _, _, clk := partsTwin(t)
	cmdsOf := func(n int) []Cmd {
		out := make([]Cmd, n)
		for i := range out {
			out[i] = Command("HSET", sk("heartbeat"), kindHash, "f", "v")
		}
		return out
	}
	budget := func(ref *Refusal) string {
		if ref == nil {
			return ""
		}
		return ref.Code + " " + ref.Detail.Budget
	}
	state := func() *State { return tw.partState(clk, "0") }

	if got := budget(checkCommands(state(), cmdsOf(partCommandsShare))); got != "" {
		t.Fatalf("commands at the share: %s", got)
	}
	if got := budget(checkCommands(state(), cmdsOf(partCommandsShare+1))); got != "LIMIT planned_commands" {
		t.Fatalf("commands past the share: %q", got)
	}
	// One command whose argv is exactly the share, and one byte more.
	fat := func(n int) []Cmd {
		base := len("HSET") + len(sk("heartbeat")) + len("f")
		return []Cmd{Command("HSET", sk("heartbeat"), kindHash, "f", rep(n-base, "v"))}
	}
	if got := budget(checkCommands(state(), fat(partArgvShare))); got != "" {
		t.Fatalf("argv bytes at the share: %s", got)
	}
	if got := budget(checkCommands(state(), fat(partArgvShare+1))); got != "LIMIT planned_argv_bytes" {
		t.Fatalf("argv bytes past the share: %q", got)
	}
	// The parts of one step add up: two lists that each fit, and together do not.
	st := state()
	if got := budget(checkCommands(st, cmdsOf(partCommandsShare/2))); got != "" {
		t.Fatalf("the first half: %s", got)
	}
	if got := budget(checkCommands(st, cmdsOf(partCommandsShare/2))); got != "" {
		t.Fatalf("the second half: %s", got)
	}
	if got := budget(checkCommands(st, cmdsOf(1))); got != "LIMIT planned_commands" {
		t.Fatalf("one command past the total: %q", got)
	}
	st = state()
	if got := budget(checkCommands(st, fat(partArgvShare/2))); got != "" {
		t.Fatalf("the first half of the argv bytes: %s", got)
	}
	if got := budget(checkCommands(st, fat(partArgvShare/2+1))); got != "LIMIT planned_argv_bytes" {
		t.Fatalf("the second half and a byte: %q", got)
	}
	// A refused list adds nothing to the total.
	st = state()
	checkCommands(st, cmdsOf(partCommandsShare+1))
	if st.partCmds != 0 || st.partArgv != 0 {
		t.Fatalf("a refused list was counted: %d commands, %d bytes", st.partCmds, st.partArgv)
	}
}

// TestPartsShareOneBudgetInBothHalves (findings 3 and 10): an ingest (2
// commands) and a beat (4) in one step, each within a share that the two
// together exceed, are refused LIMIT at the part that crosses the total (the
// beat, which runs after the ingest), in Go and in Lua, at N and at N + 1, for
// commands and for argv bytes. The reversed witness, a share held for each part
// alone, accepts both.
func TestPartsShareOneBudgetInBothHalves(t *testing.T) {
	t.Parallel()
	h := newLuaParts(t)
	tw, clk := leasedTwin(t)
	fleetSeed(t, tw)
	req := func() *Request {
		r := beatReq("m1", "m2", "m3")
		r.Meta = Meta{Tick: true, Gen: 1}
		r.Ingest = &IngestPart{From: "0", To: "3", Keys: []sprint.AgendaKey{{Key: "deal", Seq: 3}}}
		return r
	}
	bytesOf := func(cmds [][]string) int {
		n := 0
		for _, c := range cmds {
			for _, a := range c {
				n += len(a)
			}
		}
		return n
	}
	free, _ := diffParts(t, h, tw, clk, req(), nil)
	ingest, beat := free[PartIngest], free[PartBeat]
	if len(ingest.cmds) != 2 || len(beat.cmds) != 4 {
		t.Fatalf("the fixture's commands: ingest %d, beat %d", len(ingest.cmds), len(beat.cmds))
	}
	total, totalBytes := len(ingest.cmds)+len(beat.cmds), bytesOf(ingest.cmds)+bytesOf(beat.cmds)

	for _, c := range []struct {
		name         string
		shares       partShares
		ingestOK     bool
		beatRefusedW string // the budget the beat is refused at, "" when it applies
	}{
		{"both fit exactly", partShares{total, totalBytes}, true, ""},
		{"a command short for the total", partShares{total - 1, totalBytes}, true, "planned_commands"},
		{"a byte short for the total", partShares{total, totalBytes - 1}, true, "planned_argv_bytes"},
		{"room for the ingest alone", partShares{len(ingest.cmds), totalBytes}, true, "planned_commands"},
		{"not room for the ingest", partShares{len(ingest.cmds) - 1, totalBytes}, false, ""},
	} {
		c := c
		t.Run(c.name, func(t *testing.T) {
			sh := c.shares
			st := tw.partState(clk, "0")
			st.shares = &sh
			r := req()
			obs, ref := tw.before(context.Background(), st, r)
			if ref != nil {
				t.Fatal(ref)
			}
			ctx := h.newCtx(tw, clk, r)
			withShares(h, ctx, sh.commands, sh.argvBytes)
			results := map[string]luaResult{}
			for _, name := range PartOrder {
				if !requestPart(r, name) {
					continue
				}
				g := goResultAt(tw, st, name, r, obs)
				l := h.run(name, r, tw, clk, obs, ctx)
				if diff := sameResult(g, l); diff != "" {
					t.Fatalf("part %s: %s", name, diff)
				}
				results[name] = g
			}
			if refused := results[PartIngest].refusal != nil; refused == c.ingestOK {
				t.Fatalf("the ingest: refused %v, want %v", refused, !c.ingestOK)
			}
			bref := results[PartBeat].refusal
			switch {
			case c.beatRefusedW == "" && c.ingestOK && bref != nil:
				t.Fatalf("the beat was refused: %v", bref)
			case c.beatRefusedW != "" && (bref == nil || bref.Code != CodeLimit || bref.Detail.Budget != c.beatRefusedW):
				t.Fatalf("the beat: %v, want LIMIT %s", bref, c.beatRefusedW)
			}
		})
	}
}

// TestLuaStubKeepsLayerOnesAccounts: the stub's reads compute the payload as
// table_set.lua's S.readcmd does and refuse DRIFT past the reservation, the
// cells and range ids are charged against Layer 1's limits, and the commands
// count against its planned commands and argv bytes. A member of a scored set
// longer than its reservation is DRIFT on the store and here (the Go twin has no
// reservation, so this is the Lua half alone); one within it is read.
func TestLuaStubKeepsLayerOnesAccounts(t *testing.T) {
	t.Parallel()
	read := func(member string) *Refusal {
		h := newLuaParts(t)
		tw, clk := leasedTwin(t)
		seed(tw, Command("ZADD", ek("due"), kindZSet, strconv.FormatInt(clk.ms()-5, 10), member))
		req := popReq(1, 1)
		st := tw.partState(clk, "0")
		obs, _ := tw.before(context.Background(), st, req)
		return h.run(PartPop, req, tw, clk, obs, h.newCtx(tw, clk, req)).refusal
	}
	// A reply of one member reserves RangeReserveBytes + 16 (limit 1).
	if ref := read("overdue:" + rep(RangeReserveBytes+16-len("overdue:"), "x")); ref != nil {
		t.Fatalf("a member at the reservation: %v", ref)
	}
	ref := read("overdue:" + rep(RangeReserveBytes+17-len("overdue:"), "x"))
	if ref == nil || ref.Code != "DRIFT" {
		t.Fatalf("a member past the reservation: %v, want DRIFT", ref)
	}
	// The longest key the parts write (one name) and the keys of X are inside it.
	if ref := read("overdue:" + rep(tset.MaxIdentifierBytes-len("overdue:"), "x")); ref != nil {
		t.Fatalf("a key of one name: %v", ref)
	}
	// Ten members, each of the reservation: the reply is within the ten reservations.
	h := newLuaParts(t)
	tw, clk := leasedTwin(t)
	for i := 0; i < 10; i++ {
		seed(tw, Command("ZADD", ek("due"), kindZSet, strconv.FormatInt(clk.ms()-5, 10), "overdue:"+rep(RangeReserveBytes-len("overdue:")-1, "x")+strconv.Itoa(i)))
	}
	diffParts(t, h, tw, clk, popReq(1, 10), nil)

	// The charge and the limits are Layer 1's: a call that reads past a limit is refused LIMIT.
	ctx := h.newCtx(tw, clk, popReq(1, 1))
	S := h.L.GetGlobal("NS").(*lua.LTable).RawGetString("tset").(*lua.LTable)
	charge := S.RawGetString("charge")
	call := func(unit string, n int) lua.LValue {
		if err := h.L.CallByParam(lua.P{Fn: charge, NRet: 2, Protect: true}, ctx, lua.LString(unit), lua.LNumber(n)); err != nil {
			t.Fatal(err)
		}
		second := h.L.Get(-1)
		h.L.Pop(2)
		return second
	}
	if call("cell", 20000) != lua.LNil {
		t.Fatal("20,000 cells refused: that is Layer 1's limit")
	}
	if r, ok := call("cell", 1).(*lua.LTable); !ok || r.RawGetString("code").String() != "LIMIT" {
		t.Fatalf("a cell past the limit was not refused LIMIT")
	}
}

// TestLuaReadsDoNotGrowWithTheInput (finding 3, 8.1's cost rules; T4 batch
// always): the number of reads a part makes of the store is counted by the stub,
// and is the same for one key as for a thousand: each further thousand keys (two
// hundred and fifty cards of a quarantine) add one command to each batched read,
// and nothing else adds any. A read a key at a time, which the store would pay
// for in round trips of the function's own commands and in cells charged against
// Layer 1's limit of 20,000, fails here.
func TestLuaReadsDoNotGrowWithTheInput(t *testing.T) {
	t.Parallel()
	type scenario struct {
		name  string
		chunk int // the keys one batched command reads, 0 when the reads do not depend on the input
		// perKey is a command that is read once for each key of the input, because
		// each key is a key of its own: a beat writes one record a member, and
		// Layer 1 has the type of every key a step writes read in the pre stage.
		perKey string
		reads  func(t *testing.T, n int) (*Twin, *stepClock, *Request)
		sizes  []int
	}
	parkedReq := func(n int) *Request { return sprintReq(&SprintPart{Park: parkedN(n)}) }
	scenarios := []scenario{
		{name: "ingest", chunk: 1000, sizes: []int{1, 1000, 1001, 4500}, reads: func(t *testing.T, n int) (*Twin, *stepClock, *Request) {
			tw, clk := leasedTwin(t)
			var keys []sprint.AgendaKey
			for i, k := range names("k", n) {
				keys = append(keys, sprint.AgendaKey{Key: k, Seq: uint64(i + 1)})
			}
			return tw, clk, ingestReq(1, "0", strconv.Itoa(n), keys...)
		}},
		{name: "ingest of held keys", chunk: 1000, sizes: []int{1, 1001, 3000}, reads: func(t *testing.T, n int) (*Twin, *stepClock, *Request) {
			tw, clk := leasedTwin(t)
			var keys []sprint.AgendaKey
			for i, k := range names("held:c", n) {
				keys = append(keys, sprint.AgendaKey{Key: k, Seq: uint64(i + 1)})
			}
			return tw, clk, ingestReq(1, "0", strconv.Itoa(n), keys...)
		}},
		{name: "pop", sizes: []int{1, 10, 1000}, reads: func(t *testing.T, n int) (*Twin, *stepClock, *Request) {
			tw, clk := leasedTwin(t)
			for _, k := range names("overdue:n", n) {
				seed(tw, Command("ZADD", ek("due"), kindZSet, strconv.FormatInt(clk.ms()-5, 10), k))
			}
			return tw, clk, popReq(1, PopMax)
		}},
		{name: "beat", perKey: "TYPE", sizes: []int{1, 50, SprintMembersMax}, reads: func(t *testing.T, n int) (*Twin, *stepClock, *Request) {
			tw, clk := leasedTwin(t)
			return tw, clk, beatReq(names("m", n)...)
		}},
		{name: "park", sizes: []int{1, 10, SprintKeysMax}, reads: func(t *testing.T, n int) (*Twin, *stepClock, *Request) {
			tw, clk := leasedTwin(t)
			return tw, clk, parkedReq(n)
		}},
		{name: "park of keys in the agenda", sizes: []int{1, SprintKeysMax}, reads: func(t *testing.T, n int) (*Twin, *stepClock, *Request) {
			tw, clk := leasedTwin(t)
			for _, k := range names("k", n) {
				seed(tw, Command("ZADD", ek("agenda"), kindZSet, "4", k))
			}
			return tw, clk, parkedReq(n)
		}},
		{name: "unpark", sizes: []int{1, SprintKeysMax}, reads: func(t *testing.T, n int) (*Twin, *stepClock, *Request) {
			tw, clk := leasedTwin(t)
			return tw, clk, sprintReq(&SprintPart{Unpark: names("k", n)})
		}},
		{name: "quarantine", chunk: 250, sizes: []int{1, 250, 251, 2000}, reads: func(t *testing.T, n int) (*Twin, *stepClock, *Request) {
			tw, clk := leasedTwin(t)
			return tw, clk, sprintReq(&SprintPart{Quarantine: quarantinedN(n)})
		}},
		{name: "dropping", sizes: []int{1, SprintMembersMax}, reads: func(t *testing.T, n int) (*Twin, *stepClock, *Request) {
			tw, clk := leasedTwin(t)
			return tw, clk, sprintReq(&SprintPart{Dropping: opMap(names("s", n), "op")})
		}},
		{name: "counter", sizes: []int{1, CounterFieldsMax}, reads: func(t *testing.T, n int) (*Twin, *stepClock, *Request) {
			tw, clk := leasedTwin(t)
			return tw, clk, sprintReq(&SprintPart{Counter: counterOf(n, n)})
		}},
	}
	for _, sc := range scenarios {
		sc := sc
		t.Run(sc.name, func(t *testing.T) {
			t.Parallel()
			var base int
			for i, n := range sc.sizes {
				tw, clk, req := sc.reads(t, n)
				h := newLuaParts(t)
				_, ctx := diffParts(t, h, tw, clk, req, nil)
				total := 0
				for _, c := range readsOf(ctx) {
					total += c
				}
				if i == 0 {
					base = total
				}
				allowed := base
				if sc.perKey != "" {
					allowed += n - 1 // one read of the key's type for each key
				}
				if sc.chunk != 0 {
					// the keys go through one HMGET and one ZMSCORE (ingest), or one HMGET (quarantine)
					per := 2
					if sc.name == "quarantine" {
						per = 1
					}
					allowed += per * ((n+sc.chunk-1)/sc.chunk - 1)
				}
				if total > allowed {
					t.Fatalf("%d keys made %d reads, %d for one key: want at most %d (%v)", n, total, base, allowed, readsOf(ctx))
				}
				if sc.perKey != "" && readsOf(ctx)[sc.perKey] > n+4 {
					t.Fatalf("%d keys: %d %s reads, want one a key and a few for the step", n, readsOf(ctx)[sc.perKey], sc.perKey)
				}
			}
		})
	}
}

// TestHeldQueueCapAtItsEdges (finding 2; probe 7): the held queue holds at most
// HeldQueueCap keys and an ingest drops the oldest past it, at most HeldDropMax
// a call. Each edge is run on the twin and on both halves: one key short of the
// cap drops nothing, at the cap one key drops one (the oldest), and the drop is
// at most HeldDropMax, tested at one under, at and one over. A Lua constant one
// off here is found, which no walk reaches.
func TestHeldQueueCapAtItsEdges(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name                 string
		size, added, dropped int
	}{
		{"one short of the cap", HeldQueueCap - 1, 1, 0},
		{"at the cap", HeldQueueCap, 1, 1},
		{"one short of the cap, two keys", HeldQueueCap - 1, 2, 1},
		{"one under the drop limit", HeldQueueCap, HeldDropMax - 1, HeldDropMax - 1},
		{"at the drop limit", HeldQueueCap, HeldDropMax, HeldDropMax},
		{"one past the drop limit", HeldQueueCap, HeldDropMax + 1, HeldDropMax},
	} {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			h := newLuaParts(t)
			tw, clk := leasedTwin(t)
			var fill flatPairs
			for i := 1; i <= c.size; i++ {
				fill = append(fill, strconv.Itoa(i), "held:c"+strconv.Itoa(i))
			}
			seed(tw, zaddCommands(ek("heldq"), fill)...)
			var keys []sprint.AgendaKey
			for i := 0; i < c.added; i++ {
				keys = append(keys, sprint.AgendaKey{Key: "held:n" + strconv.Itoa(i), Seq: uint64(c.size + 1 + i)})
			}
			req := ingestReq(1, "0", strconv.Itoa(c.size+c.added), keys...)
			out, _ := diffParts(t, h, tw, clk, req, nil)
			if got := planOf(out[PartIngest]); got["added"] != float64(c.added) || got["dropped"] != float64(c.dropped) {
				t.Fatalf("%v, want %d added and %d dropped", got, c.added, c.dropped)
			}
			mustStep(t, tw, req)
			q := tw.SprintKeys()[ek("heldq")].ZSet
			if want := c.size + c.added - c.dropped; len(q) != want {
				t.Fatalf("the queue holds %d keys, want %d", len(q), want)
			}
			for i := 1; i <= c.dropped; i++ { // the oldest go, and only they
				if _, in := q["held:c"+strconv.Itoa(i)]; in {
					t.Fatalf("the oldest key held:c%d was not dropped", i)
				}
			}
			if _, in := q["held:c"+strconv.Itoa(c.dropped+1)]; !in {
				t.Fatalf("held:c%d was dropped", c.dropped+1)
			}
		})
	}
}

// TestQuarantineCarriedLuaMatchesGo (finding 5): the Lua core's static check of
// a quarantine no sprint part carries (NS.SP.quarantine_carried) says what the
// Go client's does (quarantineCarried) for each shape of request.
func TestQuarantineCarriedLuaMatchesGo(t *testing.T) {
	t.Parallel()
	h := newLuaParts(t)
	card := func(id string) []Quarantined { return []Quarantined{{ID: id, Code: "DRIFT"}} }
	with := func(body []Quarantined, part *SprintPart) *Request {
		r := &Request{Epoch: "0", Meta: Meta{Verb: "tick"}, Sprint: part}
		r.Body.Quarantine = body
		return r
	}
	for name, req := range map[string]*Request{
		"nothing":                    with(nil, nil),
		"a sprint part alone":        with(nil, &SprintPart{Coordinator: "c"}),
		"a part's quarantine alone":  with(nil, &SprintPart{Quarantine: card("p1")}),
		"the body alone":             with(card("p1"), nil),
		"the body and an empty part": with(card("p1"), &SprintPart{}),
		"another card in the part":   with(card("p1"), &SprintPart{Quarantine: card("p2")}),
		"the same card":              with(card("p1"), &SprintPart{Quarantine: card("p1")}),
		"two in the body, one in the part": with([]Quarantined{{ID: "p1", Code: "D"}, {ID: "p2", Code: "D"}},
			&SprintPart{Quarantine: card("p1")}),
		"two in the body, both in the part": with([]Quarantined{{ID: "p1", Code: "D"}, {ID: "p2", Code: "D"}},
			&SprintPart{Quarantine: []Quarantined{{ID: "p2", Code: "D"}, {ID: "p1", Code: "D"}, {ID: "p3", Code: "D"}}}),
		"the same two, in another order": with([]Quarantined{{ID: "p1", Code: "D"}, {ID: "p2", Code: "D"}},
			&SprintPart{Quarantine: []Quarantined{{ID: "p2", Code: "D"}, {ID: "p1", Code: "D"}}}),
		"a card named twice in the part": with(card("p1"), &SprintPart{Quarantine: []Quarantined{{ID: "p1", Code: "D"}, {ID: "p1", Code: "D"}}}),
	} {
		raw, err := json.Marshal(sprintWireOf(req))
		if err != nil {
			t.Fatal(err)
		}
		var decoded map[string]any
		if err := json.Unmarshal(raw, &decoded); err != nil {
			t.Fatal(err)
		}
		SP := h.L.GetGlobal("NS").(*lua.LTable).RawGetString("SP").(*lua.LTable)
		if err := h.L.CallByParam(lua.P{Fn: SP.RawGetString("quarantine_carried"), NRet: 1, Protect: true}, luaValueOf(h.L, decoded)); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		got := h.L.Get(-1) == lua.LTrue
		h.L.Pop(1)
		if want := quarantineCarried(req); got != want {
			t.Errorf("%s: Lua says %v, Go says %v", name, got, want)
		}
	}
}

// TestQuarantineCarriedBothDirections (the cold read of PR 4788, L1): the body
// and the sprint part quarantine the same cards. A part that quarantines a card
// the body does not name is refused as a body that names one no part carries:
// X acts on the body's quarantine alone, so the card would stay in the indexes
// while its mark is written.
func TestQuarantineCarriedBothDirections(t *testing.T) {
	t.Parallel()
	card := func(ids ...string) []Quarantined {
		var out []Quarantined
		for _, id := range ids {
			out = append(out, Quarantined{ID: id, Code: "DRIFT"})
		}
		return out
	}
	for _, c := range []struct {
		name       string
		body, part []Quarantined
		want       bool
	}{
		{"neither", nil, nil, true},
		{"the same", card("p1", "p2"), card("p2", "p1"), true},
		{"the body alone", card("p1"), nil, false},
		{"the part alone", nil, card("p1"), false},
		{"the part has one more", card("p1"), card("p1", "p2"), false},
		{"the body has one more", card("p1", "p2"), card("p1"), false},
	} {
		req := &Request{Epoch: "0", Meta: Meta{Verb: "tick"}}
		req.Body.Quarantine = c.body
		if c.part != nil {
			req.Sprint = &SprintPart{Quarantine: c.part}
		}
		if got := quarantineCarried(req); got != c.want {
			t.Errorf("%s: carried %v, want %v", c.name, got, c.want)
		}
	}
}

// TestQuarantineCarriedLuaTakesAnyShape (the recheck of IT16, probe N1): the
// Lua core's static check runs in decode_sprint before any validation, on
// whatever JSON a caller sent, so it answers REQUEST (false) to a shape the Go
// client cannot send and never raises a script error. The Go client always
// sends an id, which is why quarantineCarried, typed, has no such cases: these
// are hand-made. A raise here would reach the caller as a script error.
func TestQuarantineCarriedLuaTakesAnyShape(t *testing.T) {
	t.Parallel()
	h := newLuaParts(t)
	SP := h.L.GetGlobal("NS").(*lua.LTable).RawGetString("SP").(*lua.LTable)
	for _, c := range []struct {
		name, sprint string
		want         bool
	}{
		{"no quarantine", `{"meta":{}}`, true},
		{"an empty body list", `{"meta":{},"quarantine":[]}`, true},
		{"the same card", `{"quarantine":[{"id":"p1"}],"sprint":{"quarantine":[{"id":"p1"}]}}`, true},
		{"the part's card alone", `{"sprint":{"quarantine":[{"id":"p1"}]}}`, false},
		{"the part's card beside an empty body", `{"quarantine":[],"sprint":{"quarantine":[{"id":"p1"}]}}`, false},
		{"the part's list is a string and the body empty", `{"quarantine":[],"sprint":{"quarantine":"p1"}}`, true},
		{"the part has a card more", `{"quarantine":[{"id":"p1"}],"sprint":{"quarantine":[{"id":"p1"},{"id":"p2"}]}}`, false},

		{"a card of the part with no id", `{"quarantine":[{"id":"p1"}],"sprint":{"quarantine":[{"code":"DRIFT"}]}}`, false},
		{"a card of the part with no id beside the body's card", `{"quarantine":[{"id":"p1"}],"sprint":{"quarantine":[{"id":"p1"},{"code":"DRIFT"}]}}`, false},
		{"a card of the part whose id is a number", `{"quarantine":[{"id":"p1"}],"sprint":{"quarantine":[{"id":7}]}}`, false},
		{"a card of the part whose id is a list", `{"quarantine":[{"id":"p1"}],"sprint":{"quarantine":[{"id":["p1"]}]}}`, false},
		{"a card of the part that is a string", `{"quarantine":[{"id":"p1"}],"sprint":{"quarantine":["p1"]}}`, false},
		{"a card of the body with no id", `{"quarantine":[{"code":"DRIFT"}],"sprint":{"quarantine":[{"id":"p1"}]}}`, false},
		{"a card of the body whose id is a number", `{"quarantine":[{"id":7}],"sprint":{"quarantine":[{"id":"p1"}]}}`, false},
		{"a card of the body that is a string", `{"quarantine":["p1"],"sprint":{"quarantine":[{"id":"p1"}]}}`, false},
		{"the part's list is an object", `{"quarantine":[{"id":"p1"}],"sprint":{"quarantine":{"p1":{"id":"p1"}}}}`, false},
		{"the part's list is a string", `{"quarantine":[{"id":"p1"}],"sprint":{"quarantine":"p1"}}`, false},
		{"the part is a string", `{"quarantine":[{"id":"p1"}],"sprint":"p1"}`, false},
		{"the part is a list", `{"quarantine":[{"id":"p1"}],"sprint":[]}`, false},
		{"the body's list is a string", `{"quarantine":"p1","sprint":{"quarantine":[{"id":"p1"}]}}`, false},
		{"the body's list is a number", `{"quarantine":7,"sprint":{"quarantine":[{"id":"p1"}]}}`, false},
		{"the body's list is true", `{"quarantine":true,"sprint":{"quarantine":[{"id":"p1"}]}}`, false},
	} {
		var decoded map[string]any
		if err := json.Unmarshal([]byte(c.sprint), &decoded); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if err := h.L.CallByParam(lua.P{Fn: SP.RawGetString("quarantine_carried"), NRet: 1, Protect: true}, luaValueOf(h.L, decoded)); err != nil {
			t.Errorf("%s: the check raised %v, want an answer", c.name, err)
			continue
		}
		got := h.L.Get(-1) == lua.LTrue
		h.L.Pop(1)
		if got != c.want {
			t.Errorf("%s: carried %v, want %v", c.name, got, c.want)
		}
	}
}

// TestParkedRecordAtItsLongest: the longest record a parked key can have (a
// code, a rule and a bound of one name each, and a size and a limit of twenty
// digits) is ParkedValueBytesMax, which the Lua reads reserve: it is written and
// read back without DRIFT, in both halves, by the pop and the ingest that look
// for parked keys.
func TestParkedRecordAtItsLongest(t *testing.T) {
	t.Parallel()
	longest := ParkedKey{Key: "deal", Rule: rep(256, "r"), Code: rep(256, "c"), Budget: rep(256, "b"), Actual: "18446744073709551615", Limit: "18446744073709551615"}
	if got := len(parkedValue(longest)); got != ParkedValueBytesMax {
		t.Fatalf("the longest record is %d bytes, ParkedValueBytesMax is %d", got, ParkedValueBytesMax)
	}
	h := newLuaParts(t)
	tw, clk := leasedTwin(t)
	mustStep(t, tw, sprintReq(&SprintPart{Park: []ParkedKey{longest}}))
	seed(tw, Command("ZADD", ek("due"), kindZSet, strconv.FormatInt(clk.ms()-5, 10), "deal"))
	out, _ := diffParts(t, h, tw, clk, popReq(1, 10), nil)
	if got := planOf(out[PartPop]); got["parked"] != float64(1) {
		t.Fatalf("pop %v", got)
	}
	out, _ = diffParts(t, h, tw, clk, ingestReq(1, "0", "1", sprint.AgendaKey{Key: "deal", Seq: 1}), nil)
	if got := planOf(out[PartIngest]); got["parked"] != float64(1) {
		t.Fatalf("ingest %v", got)
	}
}

func planOf(r luaResult) map[string]any {
	m, _ := r.plan.(map[string]any)
	return m
}

// TestParkedKeysAreNotQueued (finding 7): a key that is parked waits for its
// judgment and is not planned (1.1), so neither the pop nor the ingest queues
// it: the due entry leaves and the line is consumed, and E3 holds because the
// key is named. A step that parks a key carries no pop and no ingest, since
// their queuing would leave the key both parked and in the agenda. Both halves
// agree on each.
func TestParkedKeysAreNotQueued(t *testing.T) {
	t.Parallel()
	h := newLuaParts(t)
	tw, clk := leasedTwin(t)
	seed(tw, Command("HSET", ek("parked"), kindHash, "overdue:n5", "LIMIT\t\t\t\t", "deal", "LIMIT\t\t\t\t", "held:c1", "LIMIT\t\t\t\t"),
		Command("ZADD", ek("due"), kindZSet, strconv.FormatInt(clk.ms()-5, 10), "overdue:n5", strconv.FormatInt(clk.ms()-4, 10), "hold:n7",
			strconv.FormatInt(clk.ms()-3, 10), "beat:m1"))

	out, _ := diffParts(t, h, tw, clk, popReq(1, 10), nil)
	if got := planOf(out[PartPop]); got["popped"] != float64(3) || got["parked"] != float64(1) {
		t.Fatalf("pop %v", got)
	}
	mustStep(t, tw, popReq(1, 10))
	k := tw.SprintKeys()
	_, parkedQueued := k[ek("agenda")].ZSet["overdue:n5"]
	_, holdQueued := k[ek("agenda")].ZSet["hold:n7"]
	_, downQueued := k[ek("agenda")].ZSet["down:m1"]
	if parkedQueued || !holdQueued || !downQueued {
		t.Fatalf("agenda %v: the parked key was queued, or another was not", k[ek("agenda")].ZSet)
	}
	if len(k[ek("due")].ZSet) != 0 {
		t.Fatalf("due %v: the parked key's entry stayed and would be popped every tick", k[ek("due")].ZSet)
	}

	keys := []sprint.AgendaKey{{Key: "deal", Seq: 1}, {Key: "ask:p1", Seq: 2}, {Key: "held:c1", Seq: 3}, {Key: "held:c2", Seq: 4}}
	out, _ = diffParts(t, h, tw, clk, ingestReq(1, "0", "4", keys...), nil)
	if got := planOf(out[PartIngest]); got["added"] != float64(2) || got["parked"] != float64(2) {
		t.Fatalf("ingest %v", got)
	}
	mustStep(t, tw, ingestReq(1, "0", "4", keys...))
	k = tw.SprintKeys()
	if _, in := k[ek("agenda")].ZSet["deal"]; in || k[ek("agenda")].ZSet["ask:p1"] != 2 {
		t.Fatalf("agenda %v", k[ek("agenda")].ZSet)
	}
	if _, in := k[ek("heldq")].ZSet["held:c1"]; in || k[ek("heldq")].ZSet["held:c2"] != 4 {
		t.Fatalf("held queue %v", k[ek("heldq")].ZSet)
	}
	if k[ek("tick")].Hash["cur"] != "4" {
		t.Fatalf("the cursor %q: the page was not consumed", k[ek("tick")].Hash["cur"])
	}

	// The error step of one key beside a pop or an ingest is REQUEST: both would queue it.
	park := &SprintPart{Park: []ParkedKey{{Key: "overdue:n9", Code: "LIMIT"}}}
	before := tw.SprintKeys()
	for name, r := range map[string]*Request{
		"pop":    {Epoch: "0", Meta: Meta{Tick: true, Gen: 1}, Pop: &PopPart{Limit: 10}, Sprint: park},
		"ingest": {Epoch: "0", Meta: Meta{Tick: true, Gen: 1}, Ingest: &IngestPart{From: "4", To: "5", Keys: []sprint.AgendaKey{{Key: "overdue:n9", Seq: 5}}}, Sprint: park},
	} {
		out, _ := diffParts(t, h, tw, clk, r, nil) // the Lua half refuses it as the Go half does
		if ref := out[PartSprint].refusal; ref == nil || ref.Code != CodeRequest {
			t.Errorf("the error step beside a %s: %v, want REQUEST in both halves", name, ref)
		}
		if ref := refusedStep(t, tw, r); ref.Code != CodeRequest {
			t.Errorf("the error step beside a %s: %v, want REQUEST", name, ref)
		}
	}
	if len(changedKeys(before, tw.SprintKeys())) != 0 {
		t.Fatal("a refused step wrote")
	}
	// An unpark may ride with them: nothing it does is queued by them.
	un := &Request{Epoch: "0", Meta: Meta{Tick: true, Gen: 1}, Pop: &PopPart{Limit: 10}, Sprint: &SprintPart{Unpark: []string{"deal"}}}
	diffParts(t, h, tw, clk, un, nil)
	mustStep(t, tw, un)
	if _, in := tw.SprintKeys()[ek("parked")].Hash["deal"]; in {
		t.Fatal("the unpark did not remove the key")
	}
}

// TestBeatKeepsTheBeatRecordsOtherFields (errata 3, H12): a beat writes the
// member's record by field, at_ms and load, and never replaces the record, so
// the field R1 keeps on it (stable_since) and any other survive every beat.
func TestBeatKeepsTheBeatRecordsOtherFields(t *testing.T) {
	t.Parallel()
	tw, _, _, clk := partsTwin(t)
	fleetSeed(t, tw)
	seed(tw, Command("HSET", sk("beat:m1"), kindHash, "stable_since", "77", "at_ms", "1", "load", "old"))
	clk.advance(time.Second)
	mustStep(t, tw, beatReq("m1"))
	rec := tw.SprintKeys()[sk("beat:m1")].Hash
	if rec["stable_since"] != "77" || rec["at_ms"] != strconv.FormatInt(clk.ms(), 10) || rec["load"] != "0.25 0.5" {
		t.Fatalf("m1's beat record %v", rec)
	}
}

// TestLeaseHoldIsBounded (finding 1, the reader's probe TestReaderHoldOverflow):
// a hold is at most an hour and now plus the hold stays exact, in both halves.
// With HoldMS at 2^53 - 1 Go wrote until_ms ...991 and Lua ...992, and a
// renewal a second later applied in Go and was CONFIG in Lua for good.
func TestLeaseHoldIsBounded(t *testing.T) {
	t.Parallel()
	h := newLuaParts(t)
	tw, _, _, clk := partsTwin(t)
	for _, hold := range []int64{maxExactMS, maxExactMS - 1, LeaseHoldMaxMS + 1, 0, -1} {
		req := leaseReq("tok", "run", hold, nil)
		out, _ := diffParts(t, h, tw, clk, req, nil)
		if ref := out[PartLease].refusal; ref == nil || ref.Code != CodeRequest {
			t.Fatalf("a hold of %d ms: %v, want REQUEST", hold, ref)
		}
		if ref := refusedStep(t, tw, req); ref.Code != CodeRequest {
			t.Fatalf("a hold of %d ms on the twin: %v", hold, ref)
		}
	}
	// At the cap it applies, and a renewal a second later still does, in both halves.
	mustStep(t, tw, leaseReq("tok", "run", LeaseHoldMaxMS, nil))
	clk.advance(time.Second)
	out, _ := diffParts(t, h, tw, clk, leaseReq("tok", "run", 5000, nil), nil)
	if ref := out[PartLease].refusal; ref != nil {
		t.Fatalf("a renewal after a hold at the cap: %v", ref)
	}
	if h := tw.SprintKeys()[sk("lease")].Hash; h["until_ms"] != strconv.FormatInt(clk.ms()-1000+LeaseHoldMaxMS, 10) {
		t.Fatalf("lease %v", h)
	}
	// Near the top of the exact range a sum that would leave it is refused, and one that stays is not.
	setClock(clk, maxExactMS-5)
	fresh, _, _, fclk := partsTwin(t)
	setClock(fclk, maxExactMS-5)
	for hold, want := range map[int64]string{5: "", 6: CodeRequest} {
		out, _ := diffParts(t, h, fresh, fclk, leaseReq("tok", "run", hold, nil), nil)
		ref := out[PartLease].refusal
		switch {
		case want == "" && ref != nil, want != "" && (ref == nil || ref.Code != want):
			t.Fatalf("a hold of %d ms at the top of time: %v, want %q", hold, ref, want)
		}
	}
}

// TestPartsRunTwiceWriteNothing (idempotence; E7): a part run a second time on
// the state the first run left, at the same call time, writes nothing: the pop,
// the sprint part's counter, marks, park, quarantine, coordinator and tick end
// give an empty list of commands, the ingest is refused INGESTAT, the clock's
// stop is MACHINESTATE, and clear changes nothing. The lease and the beat are
// refreshes of a time: the same call time writes the values already there, and
// the twin is byte-equal after the second run.
func TestPartsRunTwiceWriteNothing(t *testing.T) {
	t.Parallel()
	type run struct {
		name   string
		part   string
		req    func() *Request
		second string // "nothing", "same", or the code of the refusal
	}
	marks := func() *Request {
		return sprintReq(&SprintPart{Dropping: map[string]string{"s1": "op"}, Coordinator: "boss", TickEnd: &TickEnd{Backlog: "7"},
			Park:       []ParkedKey{{Key: "deal", Code: "LIMIT"}},
			Quarantine: []Quarantined{{ID: "p1", Code: "DRIFT"}}})
	}
	runs := []run{
		{"lease", PartLease, func() *Request { return leaseReq("tok", "run", 5000, map[string]string{"ticks": "1"}) }, "same"},
		{"pop", PartPop, func() *Request { return popReq(1, 100) }, "nothing"},
		{"ingest", PartIngest, func() *Request { return ingestReq(1, "12", "15", sprint.AgendaKey{Key: "ask:p9", Seq: 13}) }, CodeIngestAt},
		{"ingest of nothing", PartIngest, func() *Request { return ingestReq(1, "12", "12") }, "nothing"},
		{"beat", PartBeat, func() *Request { return beatReq("m1", "m2", "m4") }, "same"},
		{"clock stop", PartClock, func() *Request { return &Request{Epoch: "0", Clock: &ClockPart{Verb: ClockStop}} }, CodeMachineState},
		{"clock clear", PartClock, func() *Request { return &Request{Epoch: "0", Clock: &ClockPart{Verb: ClockClear}} }, "nothing"},
		{"sprint part", PartSprint, marks, "nothing"},
		{"counter", PartSprint, func() *Request {
			return sprintReq(&SprintPart{Counter: &CounterChange{Read: map[string]string{"score": ""}, Set: map[string]string{"score": "9"}}})
		}, CodeCounter}, // the guard is on the value read: a second run finds it moved
		{"counter that changes nothing", PartSprint, func() *Request {
			return sprintReq(&SprintPart{Counter: &CounterChange{Read: map[string]string{"score": "9"}, Set: map[string]string{"score": "9"}}})
		}, "nothing"},
		{"unmark", PartSprint, func() *Request {
			return sprintReq(&SprintPart{Undrop: map[string]string{"s1": "op"}, Unpark: []string{"deal"}})
		}, "nothing"},
	}
	for _, r := range runs {
		r := r
		t.Run(r.name, func(t *testing.T) {
			t.Parallel()
			tw, _, _, clk := partsTwin(t)
			fleetSeed(t, tw)
			mustStep(t, tw, leaseReq("tok", "run", 600000, nil))
			dueFixture(tw, clk.ms())
			seed(tw, Command("ZADD", ek("agenda"), kindZSet, "4", "deal"))
			if r.name == "counter that changes nothing" {
				seed(tw, Command("HSET", ek("next"), kindHash, "score", "9"))
			}
			if r.name == "unmark" {
				mustStep(t, tw, sprintReq(&SprintPart{Dropping: map[string]string{"s1": "op"}, Park: []ParkedKey{{Key: "deal", Code: "LIMIT"}}}))
			}
			if r.name == "clock clear" {
				mustStep(t, tw, &Request{Epoch: "0", Clock: &ClockPart{Verb: ClockStop}})
			}
			mustStep(t, tw, r.req())
			after := tw.SprintKeys()
			st := tw.partState(clk, "0")
			p, _ := tw.parts.Lookup(r.part)
			req := r.req()
			obs, ref := tw.before(context.Background(), st, req)
			if ref != nil {
				t.Fatal(ref)
			}
			plan, ref := p.Pre(st, req, obs)
			switch r.second {
			case "nothing":
				if ref != nil {
					t.Fatalf("the second run was refused: %v", ref)
				}
				cmds, _ := p.Cmds(st, plan, LogPlan{})
				if len(cmds) != 0 {
					t.Fatalf("the second run writes %v", cmds)
				}
			case "same":
				if ref != nil {
					t.Fatalf("the second run was refused: %v", ref)
				}
				mustStep(t, tw, req)
				if changed := changedKeys(after, tw.SprintKeys()); len(changed) != 0 {
					t.Fatalf("the second run changed %v", changed)
				}
			default:
				if ref == nil || ref.Code != r.second {
					t.Fatalf("the second run: %v, want %s", ref, r.second)
				}
			}
		})
	}
}

// dueN is n due writes of distinct keys; claimsN n claims of distinct people.
func dueN(n int) []DueAt {
	out := make([]DueAt, n)
	for i, k := range names("k", n) {
		out[i] = DueAt{Key: k, At: "1"}
	}
	return out
}

func claimsN(n int) []GoalClaim {
	out := make([]GoalClaim, n)
	for i, p := range names("p", n) {
		out[i] = GoalClaim{Person: p, R: "1"}
	}
	return out
}
