package store

import (
	"crypto/sha256"
	"fmt"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/stretchr/testify/require"
)

// devFixtureMergeStep supplies explicit external Git facts for legacy lifecycle
// fixtures; CLI tests separately verify ancestry. Explicit proofs and error
// requests pass through unchanged (SPEC-SPRINT section 7).
func devFixtureMergeStep(r sprint.MergeReq) Step {
	step := MergeStep(r)
	if r.Stage != nil || r.Dev != nil || r.Red || r.Rejected || r.Conflict != "" || r.Cross != "" {
		return step
	}
	step.Load = append(step.Load, sprint.Readers)
	step.Plan = func(s *sprint.Snapshot) sprint.Plan {
		queued := s.Merge.Cell(r.Stream, sprint.Queued)
		n := r.Batch
		if n <= 0 || n > len(queued) {
			n = len(queued)
		}
		batch := queued[:n]
		if len(r.Cards) > 0 {
			batch = nil
			for _, c := range queued {
				for _, id := range r.Cards {
					if c.ID == id {
						batch = append(batch, c)
						break
					}
				}
			}
		}
		req := r
		v := &sprint.DevReceipt{Epoch: s.Epoch, Repo: "fixture/repository", Branch: "dev", Tip: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", CandidateTip: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", CITip: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", CIReceipt: "fixture exact candidate CI", Review: "fixture independent review", BatchID: "fixture-" + r.Stream, VerifiedAt: s.Now}
		setup := map[string]string{}
		originalHeads := map[string]string{}
		for _, c := range batch {
			pr := s.Work.Card(c.ID)
			if pr == nil {
				continue
			}
			head := pr.F("head")
			if len(head) != 40 {
				sum := sha256.Sum256([]byte(pr.ID + "@" + pr.F("attempt") + ":" + head))
				head = fmt.Sprintf("%x", sum[:20])
				originalHeads[pr.ID] = pr.F("head")
				setup[pr.ID] = head
				pr.Fields["head"] = head
			}
			v.Entries = append(v.Entries, sprint.PinnedCard{ID: pr.ID, Attempt: pr.F("attempt"), Head: head})
		}
		req.Dev = v
		plan := sprint.MergeStep(s, req)
		// Persist the fixture's accepted full head in the same guarded work entry;
		// snapshot-only edits must never leave the landed row's pin inconsistent.
		for i := range plan.Units {
			if head := setup[plan.Units[i].Key]; head != "" {
				for _, rc := range s.Readers.Of(plan.Units[i].Key) {
					if rc.Col == sprint.OK {
						plan.Units[i].Changes = append(plan.Units[i].Changes, fixtureLabeledHeadChange(sprint.Readers, rc, head, originalHeads[plan.Units[i].Key]))
					}
				}
			}
			for j := range plan.Units[i].Changes {
				change := &plan.Units[i].Changes[j]
				if head := setup[change.Entry.ID]; change.Table == sprint.Work && head != "" {
					if change.Entry.Set == nil {
						change.Entry.Set = map[string]string{}
					}
					change.Entry.Set["head"] = head
					change.Entry.Set["fixture_head_label"] = originalHeads[change.Entry.ID]
				}
			}
		}
		for id, head := range originalHeads {
			pr := s.Work.Card(id)
			if head == "" {
				delete(pr.Fields, "head")
			} else {
				pr.Fields["head"] = head
			}
		}
		return plan
	}
	return step
}

// TestStorePromotionReceipts distinguishes staging from the development proof
// at the persistence boundary (SPEC-SPRINT section 7).
func TestStorePromotionReceipts(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"bare", "stage", "dev"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t)
			h.setup(1)
			h.through("s1-1")
			h.must(AddStep(sprint.AddReq{Stream: "s2", IDs: []string{"dependent"}, Needs: []string{"s1-1"}}))
			switch mode {
			case "bare":
				res := h.run(MergeStep(sprint.MergeReq{Stream: "s1"}))
				require.Len(t, res.Refused, 1)
				require.Contains(t, res.Refused[0].Why, "no verified development receipt")
			case "stage":
				const head = "cccccccccccccccccccccccccccccccccccccccc"
				// Establish the accepted external head as a persisted fixture fact.
				h.must(Step{Verb: "fixture-head", Load: tables(sprint.Work, sprint.Readers), Plan: func(s *sprint.Snapshot) sprint.Plan {
					changes := []sprint.Change{fixtureHeadChange(sprint.Work, s.Work.Card("s1-1"), head)}
					for _, rc := range s.Readers.Of("s1-1") {
						if rc.Col == sprint.OK {
							changes = append(changes, fixtureHeadChange(sprint.Readers, rc, head))
						}
					}
					return sprint.Plan{Units: []sprint.Unit{{Key: "s1-1", Changes: changes}}}
				}})
				s := h.snap()
				receipt := &sprint.StageReceipt{Epoch: s.Epoch, Repo: "fixture/repository", Base: "work", Tip: head, CITip: head, CIReceipt: "fixture stage CI", Entries: []sprint.PinnedCard{{ID: "s1-1", Attempt: s.Work.Card("s1-1").F("attempt"), Head: head}}}
				h.must(MergeStep(sprint.MergeReq{Stream: "s1", Stage: receipt}))
				require.Equal(t, head, h.snap().Work.Card("s1-1").F("staged_tip"))
				require.Empty(t, h.snap().Work.Card("s1-1").F("dev_tip"))
			case "dev":
				h.must(devFixtureMergeStep(sprint.MergeReq{Stream: "s1"}))
				c := h.snap().Work.Card("s1-1")
				require.Equal(t, sprint.Landed, h.state("s1-1"))
				require.Len(t, c.F("head"), 40)
				require.Equal(t, c.F("head"), c.F("dev_head_pin"))
				require.Equal(t, c.F("dev_candidate_tip"), c.F("dev_ci_tip"))
				require.NotEmpty(t, c.F("dev_review"))
			}
			if mode != "dev" {
				require.Equal(t, sprint.Merging, h.state("s1-1"))
				require.Equal(t, sprint.Waiting, h.state("dependent"))
				require.Equal(t, 1, h.snap().Merge.Count("s1", sprint.Queued))
			}
			h.clean(mode)
		})
	}
}

// fixtureHeadChange names the same reviewed fixture content by its external full
// Git identity and preserves membership/revision guards at the store boundary.
func fixtureHeadChange(table string, c *sprint.Card, head string) sprint.Change {
	return sprint.Change{Table: table, Entry: ntable.BatchMemberEntry{ID: c.ID, Expect: &ntable.MemberExpect{Revision: fmt.Sprint(c.Rev), Place: &ntable.PlaceExpect{Row: c.Row, Col: c.Col}}, Set: map[string]string{"head": head}}}
}

func fixtureLabeledHeadChange(table string, c *sprint.Card, head, label string) sprint.Change {
	change := fixtureHeadChange(table, c, head)
	change.Entry.Set["fixture_head_label"] = label
	return change
}

// fixtureAcceptedHeadsStep records fixture Git identities before fault injection,
// so the crash test cuts only real promotion writes, not fixture setup.
func fixtureAcceptedHeadsStep(stream string) Step {
	return Step{Verb: "fixture-heads", Load: tables(sprint.Work, sprint.Merge, sprint.Readers), Plan: func(s *sprint.Snapshot) sprint.Plan {
		var plan sprint.Plan
		for _, c := range s.Merge.Cell(stream, sprint.Queued) {
			pr := s.Work.Card(c.ID)
			label := pr.F("head")
			sum := sha256.Sum256([]byte(pr.ID + "@" + pr.F("attempt") + ":" + label))
			head := fmt.Sprintf("%x", sum[:20])
			u := sprint.Unit{Key: c.ID, Changes: []sprint.Change{fixtureLabeledHeadChange(sprint.Work, pr, head, label)}}
			for _, rc := range s.Readers.Of(c.ID) {
				if rc.Col == sprint.OK {
					u.Changes = append(u.Changes, fixtureLabeledHeadChange(sprint.Readers, rc, head, label))
				}
			}
			plan.Units = append(plan.Units, u)
		}
		return plan
	}}
}
