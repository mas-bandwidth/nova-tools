package sprintfn

import (
	"context"
	"fmt"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/tset"
)

// The ten probes of the tracer's checks (L1 1.6; TRACER-VERIFY: P1, P2a to
// P2c, P3a to P3c, P4a to P4c) run against every part (A4, the upper design's
// 0 row 21): a step that carries a part and is refused anywhere leaves the
// whole twin equal, the tables, the sprint's keys and the log, so a part never
// writes before the commit and never fails after it. Layer 1's own probes
// refuse the table side (REVISION, LIMIT, REQUEST); the part's own keys are
// what P2's wrong types are made of here, since the twin's records are not
// Redis keys: the last key the part writes (P2a), the first (P2b) and every
// key it touches (P2c) hold another type, and the part refuses WRONGTYPE from
// its pre, before the table plan.

// probeTable is a table of the four, with the row and the two cells a probe's
// base member moves between.
type probeTable struct{ table, row, from, to string }

var probeTables = []probeTable{
	{sprint.Work, "s1", "waiting", "ready"}, {sprint.Readers, "r1", "asked", "reading"},
	{sprint.Merge, "s1", "queued", "merged"}, {sprint.Fleet, "m1", "ready", "working"},
}

// probeSeed creates the rows and one base member in each of the four tables.
func probeSeed(t *testing.T, tw *Twin) {
	t.Helper()
	var entries []tset.Entry
	for _, p := range probeTables {
		entries = append(entries, tset.Entry{Kind: "rows", Table: p.table, Add: []string{p.row}})
	}
	for _, p := range probeTables {
		id := "base-" + p.table
		entries = append(entries, tset.Entry{Kind: "create", Table: p.table, To: p.row + ":" + p.from, IDs: []string{id},
			Scores: []string{"1"}, Set: map[string]string{"seed": "present"}, About: []string{id}})
	}
	mustStep(t, tw, &Request{Epoch: "0", Meta: Meta{Verb: "seed"}, Body: Body{Entries: entries}})
}

// probeMoves moves each table's base member, guarded by its revision.
func probeMoves() []tset.Entry {
	var entries []tset.Entry
	for _, p := range probeTables {
		id := "base-" + p.table
		entries = append(entries, tset.Entry{Kind: "move", Table: p.table, From: p.row + ":" + p.from, To: p.row + ":" + p.to,
			IDs: []string{id}, Revs: []tset.Decimal{"1"}, Set: map[string]string{"probe": "changed"}, About: []string{id}})
	}
	return entries
}

// A probe is a change to the four-table step that Layer 1 refuses, or a
// corruption of the part's own keys.
type probe struct {
	name string
	code string
	// table poisons the entries; keys corrupts the part's keys (and the
	// entries stay valid).
	table func(entries []tset.Entry) []tset.Entry
	keys  func(keys []typedKey) []typedKey
}

func probeCreates(counts [4]int) func([]tset.Entry) []tset.Entry {
	return func(entries []tset.Entry) []tset.Entry {
		var out []tset.Entry
		for i, p := range probeTables {
			ids, scores := make([]string, counts[i]), make([]string, counts[i])
			for j := range ids {
				ids[j], scores[j] = fmt.Sprintf("bulk-%s-%04d", p.table, j), "1"
			}
			out = append(out, tset.Entry{Kind: "create", Table: p.table, To: p.row + ":" + p.to, IDs: ids, Scores: scores, About: ids})
		}
		return out
	}
}

func probeBadScore(score string) func([]tset.Entry) []tset.Entry {
	return func(entries []tset.Entry) []tset.Entry {
		return append(entries[:3:3], tset.Entry{Kind: "create", Table: sprint.Readers, To: "r1:reading", IDs: []string{"bad-score"},
			Scores: []string{score}, About: []string{"bad-score"}})
	}
}

var probes = []probe{
	{name: "P1 second-table revision", code: "REVISION", table: func(e []tset.Entry) []tset.Entry {
		e[1].Revs = []tset.Decimal{"0"}
		return e
	}},
	{name: "P2a last key wrong type", code: CodeWrongType, keys: func(k []typedKey) []typedKey { return k[len(k)-1:] }},
	{name: "P2b first key wrong type", code: CodeWrongType, keys: func(k []typedKey) []typedKey { return k[:1] }},
	{name: "P2c every key wrong type", code: CodeWrongType, keys: func(k []typedKey) []typedKey { return k }},
	{name: "P3a 4,001 changed candidates", code: CodeLimit, table: probeCreates([4]int{1000, 1000, 1000, 1001})},
	{name: "P3b 8,000 unset names", code: CodeLimit, table: func(e []tset.Entry) []tset.Entry {
		unset := make([]string, 8000)
		for i := range unset {
			unset[i] = fmt.Sprintf("field-%04d", i)
		}
		e[3].Unset = unset
		return e
	}},
	{name: "P3c one changed reader plus 40 x 100 fleet rows", code: CodeLimit, table: func(e []tset.Entry) []tset.Entry {
		out := []tset.Entry{e[1]}
		for batch := 0; batch < 40; batch++ {
			rows := make([]string, 100)
			for i := range rows {
				rows[i] = fmt.Sprintf("fleet-%02d-%02d", batch, i)
			}
			out = append(out, tset.Entry{Kind: "rows", Table: sprint.Fleet, Add: rows})
		}
		return out
	}},
	{name: "P4a score leading space", code: CodeRequest, table: probeBadScore(" 1")},
	{name: "P4b score trailing space", code: CodeRequest, table: probeBadScore("1 ")},
	{name: "P4c score underflow 1e-400", code: CodeRequest, table: probeBadScore("1e-400")},
}

// partCase is one part with work that is valid by itself: the request that
// carries it, what the twin must hold before it, and the keys it touches in
// the order it writes them.
type partCase struct {
	name  string
	setup func(t *testing.T, tw *Twin, clk *stepClock)
	part  func(req *Request)
	keys  func(ks func(name string) string, e func(name string) string) []typedKey
}

// takeLease makes token "probe" the lease's holder at generation 1.
func takeLease(t *testing.T, tw *Twin, clk *stepClock) {
	t.Helper()
	mustStep(t, tw, leaseReq("probe", "probe", 600000, nil))
}

var partCases = []partCase{
	{name: PartLease,
		part: func(r *Request) {
			r.Lease = &LeasePart{Owner: "probe", Name: "probe", HoldMS: 5000, Heartbeat: map[string]string{"ticks": "1"}}
		},
		keys: func(sk, ek func(string) string) []typedKey {
			return []typedKey{{sk("lease"), kindHash}, {sk("heartbeat"), kindHash}}
		}},
	{name: PartPop,
		setup: func(t *testing.T, tw *Twin, clk *stepClock) {
			takeLease(t, tw, clk)
			dueFixture(tw, clk.ms())
		},
		part: func(r *Request) { r.Meta = Meta{Tick: true, Gen: 1}; r.Pop = &PopPart{Limit: 100} },
		keys: func(sk, ek func(string) string) []typedKey {
			return []typedKey{{ek("agenda"), kindZSet}, {ek("due"), kindZSet}, {ek("cut"), kindZSet}, {ek("tick"), kindHash}}
		}},
	{name: PartIngest,
		setup: func(t *testing.T, tw *Twin, clk *stepClock) { takeLease(t, tw, clk) },
		part: func(r *Request) {
			r.Meta = Meta{Tick: true, Gen: 1}
			r.Ingest = &IngestPart{From: "0", To: "9", Keys: []sprint.AgendaKey{{Key: "deal", Seq: 3}, {Key: "held:p1", Seq: 4}}}
		},
		keys: func(sk, ek func(string) string) []typedKey {
			return []typedKey{{ek("agenda"), kindZSet}, {ek("heldq"), kindZSet}, {ek("tick"), kindHash}}
		}},
	{name: PartBeat,
		part: func(r *Request) { r.Beat = &BeatPart{Members: []BeatMember{{Member: "m1", Load: "0.1"}}} },
		keys: func(sk, ek func(string) string) []typedKey {
			return []typedKey{{sk("beat:m1"), kindHash}, {ek("due"), kindZSet}, {sk("strangers"), kindHash}}
		}},
	{name: PartClock,
		setup: func(t *testing.T, tw *Twin, clk *stepClock) {
			seed(tw, Command("HSET", sk("clock"), kindHash, "stopped_ms", "0", "stopped_since_ms", ""))
		},
		part: func(r *Request) { r.Clock = &ClockPart{Verb: ClockStop} },
		keys: func(sk, ek func(string) string) []typedKey { return []typedKey{{sk("clock"), kindHash}} }},
	{name: PartSprint,
		setup: func(t *testing.T, tw *Twin, clk *stepClock) {
			seed(tw, Command("ZADD", ek("agenda"), kindZSet, "4", "deal"))
		},
		part: func(r *Request) {
			r.Sprint = &SprintPart{Counter: &CounterChange{Read: map[string]string{"score": ""}, Set: map[string]string{"score": "5"}},
				Dropping: map[string]string{"s1": "op-1"}, Park: []ParkedKey{{Key: "deal", Rule: "deal", Code: "LIMIT"}}, Coordinator: "boss",
				Quarantine: []Quarantined{{ID: "p1", Code: "DRIFT", Stream: "s1", Rule: "deal", Cells: []string{"s1:ready"}}}}
		},
		keys: func(sk, ek func(string) string) []typedKey {
			return []typedKey{{ek("next"), kindHash}, {ek("dropping"), kindHash}, {ek("parked"), kindHash}, {ek("agenda"), kindZSet},
				{ek("quarantine"), kindHash}, {sk("coordinator"), kindString}}
		}},
}

// corruptKey gives a key another type than its own, as a stray writer would.
func corruptKey(tw *Twin, k typedKey) {
	delete(tw.keys.vals, k.key)
	if k.kind == kindString {
		tw.keys.apply([]Cmd{Command("HSET", k.key, kindHash, "x", "y")})
		return
	}
	tw.keys.apply([]Cmd{Command("SET", k.key, kindString, "x")})
}

// probeTwin is a twin with the four tables seeded and the part case set up.
func probeTwin(t *testing.T, pc partCase) (*Twin, *tset.Mem, *LogStub, *stepClock) {
	t.Helper()
	tw, m, log, clk := partsTwin(t)
	probeSeed(t, tw)
	if pc.setup != nil {
		pc.setup(t, tw, clk)
	}
	return tw, m, log, clk
}

func probeRequest(pc partCase, entries []tset.Entry) *Request {
	r := &Request{Epoch: "0", Meta: Meta{Verb: "probe"}, Body: Body{Entries: entries}}
	pc.part(r)
	return r
}

// TestPartsProbes (A4; 8.1's IT16): the ten probes on every part, on the
// twin. Each of the 6 x 10 cases sends the four-table step that the probe
// spoils, with the part's own valid work beside it, and requires the probe's
// code and a whole twin (Mem state, sprint keys, log lines) byte-equal to what
// it was. Each part also has a control case: its valid work beside the valid
// four-table step applies, so no refusal is one the part would have made
// anyway, and its keys change.
func TestPartsProbes(t *testing.T) {
	t.Parallel()
	for _, pc := range partCases {
		t.Run(pc.name+"/control", func(t *testing.T) {
			t.Parallel()
			tw, _, _, _ := probeTwin(t, pc)
			before := tw.SprintKeys()
			res := mustStep(t, tw, probeRequest(pc, probeMoves()))
			if res.Reply.Changed != 4 {
				t.Fatalf("the control step changed %d members, want 4", res.Reply.Changed)
			}
			if len(changedKeys(before, tw.SprintKeys())) == 0 {
				t.Fatalf("the part's control step wrote no key of its own")
			}
		})
		keys := pc.keys(sk, ek)
		for _, pr := range probes {
			t.Run(pc.name+"/"+pr.name, func(t *testing.T) {
				t.Parallel()
				tw, m, log, _ := probeTwin(t, pc)
				entries := probeMoves()
				if pr.table != nil {
					entries = pr.table(entries)
				}
				if pr.keys != nil {
					for _, k := range pr.keys(keys) {
						corruptKey(tw, k)
					}
				}
				before := image(t, tw, m, log)
				req := probeRequest(pc, entries)
				ref := refusedStep(t, tw, req)
				if ref.Code != pr.code {
					t.Fatalf("refused %s, want %s", ref, pr.code)
				}
				if got := image(t, tw, m, log); string(got) != string(before) {
					t.Fatalf("a refusal of %s changed the twin", ref.Code)
				}
				if tw.broken != nil {
					t.Fatalf("the twin diverged: %v", tw.broken)
				}
				// The twin still serves: the same step, unspoiled, applies.
				if pr.keys != nil {
					return
				}
				clean := probeRequest(pc, probeMoves())
				if res, err := Step(context.Background(), tw, clean); err != nil || res.Step == nil {
					t.Fatalf("the unspoiled step after the refusal: %+v, %v", res, err)
				}
			})
		}
	}
}
