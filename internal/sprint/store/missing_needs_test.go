package store

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/stretchr/testify/require"
)

func TestAddRefusesDependentsOfAnUnadmittedID(t *testing.T) {
	t.Parallel()
	for _, bad := range []string{"bad.id", "ctl-absent"} {
		t.Run(bad, func(t *testing.T) {
			h := newHarness(t)
			h.setup(0)
			r := h.run(AddStep(sprint.AddReq{Stream: "s1", IDs: []string{bad, "waiter"}, Needs: []string{bad}}))
			if len(r.Refused) != 2 || h.snap().Work.Card("waiter") != nil || h.snap().Work.Card(bad) != nil {
				t.Fatalf("dependent admitted: %+v", r)
			}
			h.clean("refused missing dependency")
		})
	}
	// All or nothing: an add naming several writes none of them when any is
	// refused, naming every one; an existing id is refused as well.
	h := newHarness(t)
	h.setup(1)
	r := h.run(AddStep(sprint.AddReq{Stream: "s2", IDs: []string{"bad.id", "good"}}))
	if len(r.Refused) != 2 || h.snap().Work.Card("good") != nil || !strings.Contains(fmt.Sprint(r.Refused), "all or none") {
		t.Fatalf("partial acceptance: %+v", r)
	}
	r = h.run(AddStep(sprint.AddReq{Stream: "s2", IDs: []string{"s1-1", "waiter"}, Needs: []string{"s1-1"}}))
	require.Len(t, r.Refused, 2, "an existing id with a new one: %+v", r)
	require.Nil(t, h.snap().Work.Card("waiter"), "an existing id with a new one: %+v", r)
	h.must(AddStep(sprint.AddReq{Stream: "s2", IDs: []string{"waiter"}, Needs: []string{"s1-1"}}))
	require.Equal(t, sprint.Waiting, h.state("waiter"), "waiter: %s", h.state("waiter"))
	h.clean("unrelated refusals")
}

// Recreate already-persisted data from the old admission bug. Production
// verbs no longer create it; recovery must still surface it to the coordinator.
func seedMissingNeeds(h *harness, id, needs string) {
	h.t.Helper()
	h.m.mu.Lock()
	defer h.m.mu.Unlock()
	table := h.m.tables["t-work"]
	c := table.members[id]
	c.fields["needs"] = needs
	c.rev++
	table.rev++
}

func TestStoredMissingNeedsHaveOneActionableJudgment(t *testing.T) {
	t.Parallel()
	const missing = "a primary is blocked on something missing"
	for _, sentinel := range []bool{false, true} {
		for _, live := range []bool{false, true} {
			t.Run(fmt.Sprintf("sentinel=%v/live=%v", sentinel, live), func(t *testing.T) {
				h := newHarness(t)
				h.setup(1)
				h.must(AddStep(sprint.AddReq{Stream: "s2", IDs: []string{"waiter"}, Needs: []string{"s1-1"}, Sentinel: sentinel}))
				needs := "bad.id"
				if live {
					needs += ",s1-1"
				}
				seedMissingNeeds(h, "waiter", needs)
				h.startMachine()
				for i := 0; i < 3; i++ {
					if i == 2 {
						h.tick(time.Minute + time.Second)
					}
					h.machine()
				}
				notes := h.nOpenOf(missing, "waiter")
				require.Len(t, notes, 1, "missing dependency is silent or repeated: %+v", notes)
				require.Len(t, h.nAllNotes(missing), 1, "missing dependency is silent or repeated: %+v", notes)
				n := notes[0].Note
				if strings.Join(n.Needs, ",") != "bad.id" || strings.Join(n.Decisions, ",") != "drop,ack" || !strings.Contains(n.What, "bad.id") {
					t.Fatalf("not actionable: %+v", n)
				}
				h.must(AckStep(sprint.AckReq{Notes: []string{n.ID}, Reason: "dependency not required", Who: "tester"}))
				c := h.snap().Work.Card("waiter")
				if c.F("waived") != "bad.id" || c.F("waived_by") != "tester" || c.F("waived_at") == "" {
					t.Fatalf("waiver not recorded: %+v", c)
				}
				switch {
				case live:
					require.Equal(t, sprint.Waiting, c.Col, "live dependency bypassed: %+v", c)
					require.Empty(t, c.F("reached"), "live dependency bypassed: %+v", c)
				case sentinel:
					require.Equal(t, sprint.Waiting, c.Col, "sentinel not reached: %+v", c)
					require.NotEmpty(t, c.F("reached"), "sentinel not reached: %+v", c)
				default:
					require.Equal(t, sprint.Ready, c.Col, "primary not ready: %+v", c)
				}
				h.machine()
				require.Empty(t, h.nOpenOf(missing, "waiter"), "acknowledged missing need repeated")
				require.Len(t, h.nAllNotes(missing), 1, "acknowledged missing need repeated")
				h.clean("missing need acknowledged")
			})
		}
	}
}

func TestMissingNeedWaiverDoesNotIncludeLaterMissingNeed(t *testing.T) {
	t.Parallel()
	const missing = "a primary is blocked on something missing"
	h := newHarness(t)
	h.setup(1)
	h.must(AddStep(sprint.AddReq{Stream: "s2", IDs: []string{"waiter"}, Needs: []string{"s1-1"}}))
	seedMissingNeeds(h, "waiter", "bad.id")
	h.startMachine()
	h.machine()
	notes := h.nOpenOf(missing, "waiter")
	require.Len(t, notes, 1, "missing judgment: %+v", notes)
	seedMissingNeeds(h, "waiter", "bad.id,later.bad")
	h.must(AckStep(sprint.AckReq{Notes: []string{notes[0].Note.ID}, Reason: "only the first", Who: "tester"}))
	c := h.snap().Work.Card("waiter")
	require.Equal(t, "bad.id", c.F("waived"), "waived unreviewed need: %+v", c)
	require.Equal(t, sprint.Waiting, c.Col, "waived unreviewed need: %+v", c)
	h.machine()
	notes = h.nOpenOf(missing, "waiter")
	require.Len(t, notes, 1, "new missing need not raised: %+v", notes)
	require.Equal(t, "later.bad", strings.Join(notes[0].Note.Needs, ","), "new missing need not raised: %+v", notes)
	h.clean("later missing need remains judged")
}

func TestRestoredMissingNeedIsNotWaivedAndItsJudgmentCloses(t *testing.T) {
	t.Parallel()
	for _, action := range []string{"ack", "resolve", "dropped"} {
		t.Run(action, func(t *testing.T) {
			h := newHarness(t)
			h.setup(1)
			h.must(AddStep(sprint.AddReq{Stream: "s2", IDs: []string{"waiter"}, Needs: []string{"s1-1"}}))
			seedMissingNeeds(h, "waiter", "later")
			h.must(ResolveStep(sprint.ResolveReq{}))
			notes := h.nOpenOf(sprint.NMissingNeed, "waiter")
			require.Len(t, notes, 1, "missing judgment: %+v", notes)
			h.must(AddStep(sprint.AddReq{Stream: "s3", IDs: []string{"later"}}))
			switch action {
			case "ack":
				h.must(AckStep(sprint.AckReq{Notes: []string{notes[0].Note.ID}, Reason: "it exists now"}))
			case "resolve":
				h.must(ResolveStep(sprint.ResolveReq{}))
			case "dropped":
				h.must(DropStep(sprint.DropReq{Sel: sprint.Sel{IDs: []string{"later"}}, Reason: "not needed"}))
				h.must(ResolveStep(sprint.ResolveReq{}))
				require.Len(t, h.nOpenOf(sprint.NBlocked, "waiter"), 1, "dropped need was hidden by former missing judgment")
			}
			c := h.snap().Work.Card("waiter")
			if c.Col != sprint.Waiting || c.F("waived") != "" || len(h.nOpenOf(sprint.NMissingNeed, "waiter")) != 0 {
				t.Fatalf("wrong recovery: card=%+v notes=%+v", c, h.nOpenOf(sprint.NMissingNeed, "waiter"))
			}
			h.clean("dependency exists again")
		})
	}
}

// Every missing-need judgment is written in the one tick that finds it: a
// tick has no bound on its notes but the step's (the owner's rule, never a
// row at a time), and none is written twice.
func TestMissingNeedJudgmentsAllInOneTick(t *testing.T) {
	t.Parallel()
	const n = 51
	h := newHarness(t)
	h.setup(1)
	h.must(AddStep(sprint.AddReq{Stream: "s2", Count: n, Needs: []string{"s1-1"}}))
	for i := 1; i <= n; i++ {
		seedMissingNeeds(h, fmt.Sprintf("s2-%d", i), "bad.id")
	}
	h.startMachine()
	h.machine()
	got := len(h.nOpenOf(sprint.NMissingNeed, ""))
	require.Equal(t, n, got, "first tick: %d", got)
	h.machine()
	h.machine()
	got = len(h.nAllNotes(sprint.NMissingNeed))
	require.Equal(t, n, got, "not written once each: %d", got)
	h.clean("missing judgments drained")
}
