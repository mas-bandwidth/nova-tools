package sprint

import (
	"crypto/sha256"
	"fmt"
 "testing"
 "github.com/stretchr/testify/require"
)

// devMergeFixture gives state-machine fixtures explicit external Git evidence.
// These tests have no Git backend: the deterministic commit identifies the
// accepted fixture content, while CLI promotion tests verify real ancestry.
// Error facts and explicit proof tests retain their original request unchanged.
func devMergeFixture(s *Snapshot, r MergeReq) Plan {
	if r.Stage != nil || r.Dev != nil || r.Red || r.Rejected || r.Conflict != "" || r.Cross != "" {
		return MergeStep(s, r)
	}
	queued := s.Merge.Cell(r.Stream, Queued)
	n := r.Batch
	if n <= 0 || n > len(queued) {
		n = len(queued)
	}
	batch := queued[:n]
	if len(r.Cards) > 0 {
		batch = nil
		for _, c := range queued {
			if contains(r.Cards, c.ID) {
				batch = append(batch, c)
			}
		}
	}
	v := &DevReceipt{Epoch: s.Epoch, Repo: "fixture/repository", Branch: "dev", Tip: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", CandidateTip: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", CITip: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", CIReceipt: "fixture exact candidate CI", Review: "fixture independent review", BatchID: "fixture-" + r.Stream, VerifiedAt: s.Now}
	for _, c := range batch {
		pr := s.Work.Card(c.ID)
		if pr == nil {
			continue
		}
		if !validPinnedHead(PinnedCard{Head: pr.F("head")}) {
			// Establish the fixture's external head before submitting its proof.
			old := pr.F("head")
			sum := sha256.Sum256([]byte(pr.ID + "@" + pr.F("attempt") + ":" + old))
			pr.Fields["head"] = fmt.Sprintf("%x", sum[:20])
			// Existing reads refer to that same synthetic fixture content.
			for _, read := range s.Readers.Of(pr.ID) {
				if read.F("head") == old {
					read.Fields["head"] = pr.F("head")
				}
			}
		}
		v.Entries = append(v.Entries, PinnedCard{ID: pr.ID, Attempt: pr.F("attempt"), Head: pr.F("head")})
	}
	r.Dev = v
	return MergeStep(s, r)
}

// A valid ancestry receipt cannot bypass an earlier unresolved stream card,
// and its pin order is the order of the certified queue prefix.
func TestDevelopmentReceiptKeepsTheStreamPrefixOrder(t *testing.T) {
 t.Parallel()
 for _, disposition := range []string{"skip earlier", "reverse pins"} {
  t.Run(disposition,func(t *testing.T) {
   t.Parallel()
   w:=setup(t,2)
   accepted(w,"s1-1","s1-2")
   head:="aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
   var entries []PinnedCard
   for _,id:=range []string{"s1-1","s1-2"} {
    c:=w.s.Work.Card(id)
    c.Fields["head"]=head
    entries=append(entries,PinnedCard{ID:id,Attempt:c.F("attempt"),Head:head})
   }
   r:=MergeReq{Stream:"s1",Batch:2}
   if disposition=="skip earlier" {
    r.Cards=[]string{"s1-2"}
    entries=entries[1:]
   } else {
    entries[0],entries[1]=entries[1],entries[0]
   }
   r.Dev=&DevReceipt{Epoch:w.s.Epoch,Repo:"fixture/repository",Branch:"dev",Tip:head,CandidateTip:head,CITip:head,CIReceipt:"fixture exact check",Review:"fixture review",BatchID:"fixture-order",VerifiedAt:w.s.Now,Entries:entries}
   p:=MergeStep(w.s,r)
   require.Empty(t,p.Units)
   require.Len(t,p.Refused,1)
   require.Contains(t,p.Refused[0].Why,"prefix")
   require.Equal(t,Merging,w.state("s1-1"))
   require.Equal(t,Merging,w.state("s1-2"))
  })
 }
}
