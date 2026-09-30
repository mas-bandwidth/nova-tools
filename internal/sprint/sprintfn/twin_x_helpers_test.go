package sprintfn

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/tset"
)

// The harness of X's tests: a twin whose X is the real X (XPre, XCmds and the
// asks of xBefore), over the four tables of helpers_test.go.
//
// X's tests need the sprint's own keys to hold what the tick's parts write (the
// lease, the clock, the counter, the marks, the quarantine), and those parts are
// IT16's and IT03's, not yet merged. The stand-ins below write exactly the keys
// the design says each part writes, and no more: they are these tests' own, they
// stand for the parts until those merge, and what a test checks about X never
// depends on them being right beyond that.

// xp is the prefix of the sprint's own keys in every test.
const xp = testPrefix + "sprint:"

// xh is one test's twin and the commands it wants written by the next step.
type xh struct {
	t     *testing.T
	tw    *Twin
	mem   *tset.Mem
	log   *LogStub
	extra []Cmd // sprint keys the next step's stand-in part writes, then cleared
	last  []Cmd // the commands X.plan returned for the last step that reached it

	mirror *xLuaMirror // the Lua half of X, run on every call beside the twin's; nil when off
}

// newXHarness is a twin with X in its phases and the stand-in parts registered,
// and the Lua half of X run beside it on every call (twin_x_lua_test.go).
func newXHarness(t *testing.T) *xh { return newXHarnessMirrored(t, true) }

// newXHarnessMirrored is newXHarness with the Lua half of X on or off.
func newXHarnessMirrored(t *testing.T, mirror bool) *xh {
	t.Helper()
	h := &xh{t: t}
	if mirror {
		h.mirror = newXLuaMirror(t)
	}
	phases := Phases{Before: xBefore, JDecide: xStandInJDecide, JCmds: xStandInJCmds,
		XPre: func(st *State, req *Request, obs *Before) *Refusal {
			ref := XPre(st, req, obs)
			if h.mirror != nil {
				h.mirror.checkPre(st, req, obs, ref)
			}
			return ref
		},
		XCmds: func(st *State, tp TablePlan, lp LogPlan) []Cmd {
			cmds := XCmds(st, tp, lp)
			if h.mirror != nil {
				h.mirror.checkCmds(st, tp, lp, cmds)
			}
			h.last = cmds
			return cmds
		}}
	h.tw, h.mem, h.log = newTestTwin(t, phases)
	idle := PartFuncs{PreFunc: func(*State, *Request, *Before) (any, *Refusal) { return xPlanCmds{}, nil }, CmdsFunc: xHandOver}
	for name, p := range map[string]Part{PartSprint: h.xSprintPart(), PartClock: h.xClockPart(), PartLease: h.xLeasePart(),
		PartPop: idle, PartIngest: idle, PartBeat: idle} {
		if err := h.tw.parts.Register(name, p); err != nil {
			t.Fatal(err)
		}
	}
	return h
}

// xStandInJDecide stands in for J (IT15): one note a request, about its subjects.
func xStandInJDecide(st *State, in []NoteReq, obs *Before) ([]tset.Note, JPlan, *Refusal) {
	var notes []tset.Note
	var jp JPlan
	for i, n := range in {
		meta, _ := json.Marshal(map[string]string{"op": n.Op, "type": n.Type, "cause": n.Cause})
		notes = append(notes, tset.Note{Line: tset.NoteLine{Kind: "note", Meta: meta}, About: n.Subjects})
		jp.Notes = append(jp.Notes, JNote{Index: i, Req: n})
	}
	return notes, jp, nil
}

// xStandInJCmds stands in for J's writes (IT15): the note's id in jopen of each
// subject, under the type and cause.
func xStandInJCmds(st *State, jp JPlan, lp LogPlan) []Cmd {
	var out []Cmd
	for _, n := range jp.Notes {
		for _, subject := range n.Req.Subjects {
			out = append(out, Command("HSET", xp+"jopen:"+subject+"@"+string(st.Epoch), kindHash,
				n.Req.Type+"|"+n.Req.Cause, "n"+string(lp.NoteSeqs[n.Index])))
		}
	}
	return out
}

func xSortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// xPlanCmds is a stand-in part's plan: the commands its Cmds hands over.
type xPlanCmds struct{ Cmds []Cmd }

func xHandOver(st *State, plan any, lp LogPlan) ([]Cmd, *Refusal) { return plan.(xPlanCmds).Cmds, nil }

// xSprintPart stands in for the sprint part (IT16): the counter's Set, the
// dropping marks, the coordinator and the quarantine, and the commands the test
// handed over.
func (h *xh) xSprintPart() Part {
	return PartFuncs{
		PreFunc: func(st *State, req *Request, obs *Before) (any, *Refusal) {
			var cmds []Cmd
			e := "@" + string(st.Epoch)
			if s := req.Sprint; s != nil {
				if c := s.Counter; c != nil && len(c.Set) != 0 {
					args := []string{}
					for _, f := range xSortedKeys(c.Set) {
						args = append(args, f, c.Set[f])
					}
					cmds = append(cmds, Command("HSET", xp+"next"+e, kindHash, args...))
				}
				for _, stream := range xSortedKeys(s.Dropping) {
					if op := s.Dropping[stream]; op == "" {
						cmds = append(cmds, Command("HDEL", xp+"dropping"+e, kindHash, stream))
					} else {
						cmds = append(cmds, Command("HSET", xp+"dropping"+e, kindHash, stream, op))
					}
				}
				if s.Coordinator != "" {
					cmds = append(cmds, Command("SET", xp+"coordinator", kindString, s.Coordinator))
				}
			}
			for _, q := range req.Body.Quarantine {
				cmds = append(cmds, Command("HSET", xp+"quarantine"+e, kindHash, q.ID, q.Code))
			}
			cmds = append(cmds, h.extra...)
			h.extra = nil
			return xPlanCmds{Cmds: cmds}, nil
		},
		CmdsFunc: xHandOver,
	}
}

// xClockPart stands in for the clock's part (IT03): init, start, stop and clear on
// {p}clock's fields (1.2).
func (h *xh) xClockPart() Part {
	return PartFuncs{
		PreFunc: func(st *State, req *Request, obs *Before) (any, *Refusal) {
			key := xp + "clock"
			now, _ := strconv.ParseInt(string(st.NowMS), 10, 64)
			since, _ := st.Keys.HGet(key, "stopped_since_ms")
			past, _ := st.Keys.HGet(key, "stopped_ms")
			var cmds []Cmd
			switch req.Clock.Verb {
			case ClockInit:
				cmds = append(cmds, Command("HSET", key, kindHash, "stopped_ms", "0", "stopped_since_ms", string(st.NowMS)))
			case ClockStop:
				cmds = append(cmds, Command("HSET", key, kindHash, "stopped_since_ms", string(st.NowMS)))
			case ClockStart:
				s, _ := strconv.ParseInt(since, 10, 64)
				p, _ := strconv.ParseInt(past, 10, 64)
				cmds = append(cmds, Command("HSET", key, kindHash, "stopped_ms", strconv.FormatInt(p+now-s, 10), "stopped_since_ms", ""))
			}
			return xPlanCmds{Cmds: cmds}, nil
		},
		CmdsFunc: xHandOver,
	}
}

// xLeasePart stands in for the lease part (IT16): it takes the lease for the
// request's owner at the next generation.
func (h *xh) xLeasePart() Part {
	return PartFuncs{
		PreFunc: func(st *State, req *Request, obs *Before) (any, *Refusal) {
			gen, _ := st.Keys.HGet(xp+"lease", "gen")
			n, _ := strconv.ParseUint(gen, 10, 64)
			return xPlanCmds{Cmds: []Cmd{Command("HSET", xp+"lease", kindHash, "owner", req.Lease.Owner, "name", req.Lease.Name,
				"gen", strconv.FormatUint(n+1, 10))}}, nil
		},
		CmdsFunc: xHandOver,
	}
}

// Request builders. Every entry that changes a member carries its about ids
// (the composed profile's rule, L1 3).

func xCreate(table, to, id, score string, set map[string]string) tset.Entry {
	return tset.Entry{Kind: "create", Table: table, To: to, IDs: []string{id}, Scores: []string{score}, Set: set, About: []string{id}}
}

func xMove(table, from, to, id string, set map[string]string) tset.Entry {
	return tset.Entry{Kind: "move", Table: table, From: from, To: to, IDs: []string{id}, Set: set, About: []string{id}}
}

func xRemove(table, from, id string) tset.Entry {
	return tset.Entry{Kind: "remove", Table: table, From: from, IDs: []string{id}, About: []string{id}}
}

// verb is a request of a verb's step, which applies in either state.
func xVerb(name string, entries ...tset.Entry) *Request {
	return &Request{Epoch: "0", Meta: Meta{Verb: name, Actor: "coordinator"}, Body: Body{Entries: entries}}
}

// tick is a request of a tick step at lease generation gen.
func xTick(gen uint64, entries ...tset.Entry) *Request {
	return &Request{Epoch: "0", Meta: Meta{Rule: "resolve", Tick: true, Gen: gen}, Body: Body{Entries: entries}}
}

// do sends a request and fails unless it applied.
func (h *xh) do(req *Request) *StepReply {
	h.t.Helper()
	return mustStep(h.t, h.tw, req)
}

// refused sends a request and returns its refusal, failing if it applied or
// came back as an error.
func (h *xh) refused(req *Request) *Refusal {
	h.t.Helper()
	res, err := Step(context.Background(), h.tw, req)
	var ie *ItemError
	if errors.As(err, &ie) {
		return ie.Refusal
	}
	if err != nil || res.Refusal == nil {
		h.t.Fatalf("result %+v, err %v; want a refusal", res, err)
	}
	return res.Refusal
}

// write runs a step whose only effect is the stand-in sprint part writing cmds:
// the keys a test wants the machine to have.
func (h *xh) write(cmds ...Cmd) {
	h.t.Helper()
	h.extra = cmds
	h.do(&Request{Epoch: "0", Meta: Meta{Verb: "seed"}, Sprint: &SprintPart{}})
}

// keys is the twin's sprint keys, read.
func (h *xh) keys() map[string]KeyValue { return h.tw.SprintKeys() }

// zset is the members of a sorted set of the twin's sprint keys, with scores.
func (h *xh) zset(name string) map[string]float64 {
	h.t.Helper()
	return h.keys()[xp+name].ZSet
}

// img is the whole image of the twin.
func (h *xh) img() string { return string(image(h.t, h.tw, h.mem, h.log)) }

// fixture seeds the counter, the rows and these cards: p1 and p2 waiting in s1
// (free to go), p3 ready in s1 (never dealt), a sentinel g1 waiting in s1, and
// the members' control cards, m1 up and m2 down.
func (h *xh) fixture() {
	h.t.Helper()
	h.do(&Request{Epoch: "0", Meta: Meta{Verb: "seed"}, Sprint: &SprintPart{Counter: &CounterChange{Read: map[string]string{}, Set: map[string]string{"score": "1000", "streams": "1"}}}})
	h.do(xVerb("seed", tset.Entry{Kind: "rows", Table: sprint.Work, Add: []string{"s1", "s2"}},
		tset.Entry{Kind: "rows", Table: sprint.Fleet, Add: []string{"m1", "m2"}},
		tset.Entry{Kind: "rows", Table: sprint.Readers, Add: []string{"r1"}},
		tset.Entry{Kind: "rows", Table: sprint.Merge, Add: []string{"s1", "s2"}}))
	h.do(xVerb("seed",
		xCreate(sprint.Work, "s1:waiting", "p1", "1", map[string]string{"kind": "primary", "open": "0"}),
		xCreate(sprint.Work, "s1:waiting", "p2", "2", map[string]string{"kind": "primary", "open": "0"}),
		xCreate(sprint.Work, "s1:ready", "p3", "3", map[string]string{"kind": "primary", "attempt": "0"}),
		xCreate(sprint.Work, "s1:waiting", "g1", "4", map[string]string{"kind": "sentinel"}),
		xCreate(sprint.Fleet, "m1:ctl", "ctl-m1", "0", map[string]string{"status": "up"}),
		xCreate(sprint.Fleet, "m2:ctl", "ctl-m2", "0", map[string]string{"status": "down"})))
}
