package sprintfn

import (
	"context"
	"errors"
	"strconv"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/tset"
)

// TestXProbesOnEveryPart: the ten verifier probes of Layer 1's commit (L1 1.6) run
// against the write path with X active and every writer asked to write: each step
// is one X would write for if it applied (an index derived, an agenda key done, a
// card quarantined, the counter raised), and each is refused as the probe says with
// the whole image unchanged, and the agenda's key still there, so X's commands are
// shown not to be applied before the refusal. Most are refused by the static check
// of the request before any phase runs, as the store refuses them in S.open;
// REVISION is refused by the plan, after X.pre has passed. The probes that need a
// key of the wrong type (P2a to P2c) exist only in a store: Mem has no such state to
// be refused. X's own keys of the wrong type are refused after the plan, leaving the
// image equal, in TestXRefusalAfterPlanLeavesNothing (S15).
func TestXProbesOnEveryPart(t *testing.T) {
	t.Parallel()
	// writing is a step X writes for, added to each probe's own entries: a card
	// that enters an index, an agenda key finished, a quarantine with its note.
	writing := func(req *Request) *Request {
		req.Body.Entries = append([]tset.Entry{xCreate(sprint.Fleet, "m1:ready", "w0", "1", map[string]string{"due_untaken": "9000"})}, req.Body.Entries...)
		req.Body.Done = []string{"deal"}
		req.Body.Requeue = []string{"ask@5+10"}
		req.Body.Quarantine = []Quarantined{{ID: "p2", Stream: "s1", Code: "DRIFT", Rule: "resolve"}}
		req.Body.Notes = []NoteReq{{Op: "open", Type: "invariant", Cause: "drift", Subjects: []string{"p2"}}}
		req.Sprint = &SprintPart{Counter: &CounterChange{Read: map[string]string{"score": "1000"}, Set: map[string]string{"score": "1001"}}}
		return req
	}
	many := func(table, to, prefix string, from, n int) tset.Entry {
		e := tset.Entry{Kind: "create", Table: table, To: to}
		for i := from; i < from+n; i++ {
			id := prefix + strconv.Itoa(i)
			e.IDs, e.Scores, e.About = append(e.IDs, id), append(e.Scores, "1"), append(e.About, id)
		}
		return e
	}
	probes := []struct {
		name string // the test Layer 1 names it by
		code string
		skip string
		req  func() *Request
	}{
		{name: "TestCommitProbeRevisionSecondTable", code: "REVISION", req: func() *Request {
			return xTick(1,
				xCreate(sprint.Readers, "r1:asked", "a1", "1", nil), xCreate(sprint.Merge, "s1:queued", "b1", "1", nil), xCreate(sprint.Fleet, "m2:ready", "c1", "1", nil),
				tset.Entry{Kind: "move", Table: sprint.Work, From: "s1:waiting", To: "s1:ready", IDs: []string{"p1"}, Revs: []tset.Decimal{"99"}, About: []string{"p1"}})
		}},
		{name: "TestCommitProbeRecordWrongType", skip: "G0: needs the store: a record key of the wrong type exists only in a store"},
		{name: "TestCommitProbeDestinationWrongType", skip: "G0: needs the store: a destination cell key of the wrong type exists only in a store"},
		{name: "TestCommitProbeHistoryWrongType", skip: "G0: needs the store: a history key of the wrong type exists only in a store"},
		{name: "TestCommitProbeCandidateOverflow", code: "LIMIT", req: func() *Request {
			return xTick(1, many(sprint.Fleet, "m1:ready", "x", 0, 2000), many(sprint.Fleet, "m2:ready", "y", 0, 2000), many(sprint.Readers, "r1:asked", "z", 0, 1))
		}},
		{name: "TestCommitProbeUnset8000", code: "LIMIT", req: func() *Request {
			names := make([]string, 8000)
			for i := range names {
				names[i] = "f" + strconv.Itoa(i)
			}
			return xTick(1, tset.Entry{Kind: "move", Table: sprint.Work, From: "s1:waiting", IDs: []string{"p1"}, Unset: names, About: []string{"p1"}})
		}},
		{name: "TestCommitProbeRowsAcrossEntries", code: "LIMIT", req: func() *Request {
			entries := []tset.Entry{xCreate(sprint.Readers, "r1:asked", "a2", "1", nil)}
			for i := 0; i < 40; i++ {
				rows := tset.Entry{Kind: "rows", Table: sprint.Fleet}
				for j := 0; j < 100; j++ {
					rows.Add = append(rows.Add, "row"+strconv.Itoa(i*100+j))
				}
				entries = append(entries, rows)
			}
			return xTick(1, entries...)
		}},
		{name: "TestCommitProbeScoreLeadingSpace", code: "REQUEST", req: func() *Request {
			return xTick(1, tset.Entry{Kind: "move", Table: sprint.Fleet, From: "m1:ctl", IDs: []string{"ctl-m1"}, Scores: []string{" 1"}, About: []string{"ctl-m1"}})
		}},
		{name: "TestCommitProbeScoreTrailingSpace", code: "REQUEST", req: func() *Request {
			return xTick(1, tset.Entry{Kind: "move", Table: sprint.Fleet, From: "m1:ctl", IDs: []string{"ctl-m1"}, Scores: []string{"1 "}, About: []string{"ctl-m1"}})
		}},
		{name: "TestCommitProbeScoreUnderflow", code: "REQUEST", req: func() *Request {
			return xTick(1, tset.Entry{Kind: "move", Table: sprint.Fleet, From: "m1:ctl", IDs: []string{"ctl-m1"}, Scores: []string{"1e-400"}, About: []string{"ctl-m1"}})
		}},
	}
	if len(probes) != 10 {
		t.Fatalf("%d probes, want Layer 1's ten", len(probes))
	}
	for _, p := range probes {
		t.Run(p.name, func(t *testing.T) {
			t.Parallel()
			if p.skip != "" {
				t.Skip(p.skip)
			}
			h := newXHarness(t)
			h.fixture()
			h.write(xLease("1"), xRunning(0), Command("ZADD", xp+"agenda@0", kindZSet, "5", "deal"))
			before := h.img()
			req := writing(p.req())
			res, err := Step(context.Background(), h.tw, req)
			var code string
			var ie *ItemError
			switch {
			case errors.As(err, &ie):
				code = ie.Refusal.Code // refused before anything ran: a malformed item (L1 1.5)
				t.Logf("refused before the pipeline ran: %s", ie.Refusal)
			case err == nil && res.Refusal != nil:
				code = res.Refusal.Code
				t.Logf("refused in %s: %s", res.Refusal.Phase, res.Refusal)
			default:
				t.Fatalf("result %+v, err %v; want %s", res, err, p.code)
			}
			if code != p.code {
				t.Fatalf("refused %s, the probe wants %s", code, p.code)
			}
			if after := h.img(); after != before {
				t.Fatalf("a %s refusal changed the twin", p.code)
			}
			if _, ok := h.zset("agenda@0")["deal"]; !ok {
				t.Fatalf("the agenda's deal key was removed by a refused step")
			}
		})
	}
}
