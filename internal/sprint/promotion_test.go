package sprint

import (
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"strings"
	"testing"
	"time"
)

// The fixture explicitly models a verified external head; it never makes the
// old landed column itself proof. Git ancestry belongs to the CLI probes.
func promotionWorld(t *testing.T) (*world, PinnedCard) {
	t.Helper()
	w := setup(t, 1)
	accepted(w, "s1-1")
	c := w.s.Work.Card("s1-1")
	c.Fields["head"] = strings.Repeat("a", 40)
	return w, PinnedCard{ID: c.ID, Head: c.F("head"), Attempt: c.F("attempt")}
}

func TestPromotionStageDoesNotLandOrReleaseDependencies(t *testing.T) {
	t.Parallel()
	w, pin := promotionWorld(t)
	w.must(Add(w.s, AddReq{IDs: []string{"dependent"}, Stream: "s2", Needs: []string{pin.ID}, Brief: proBrief}))
	stage := &StageReceipt{Epoch: w.s.Epoch, Repo: "owner/repo", Base: "work", Tip: strings.Repeat("b", 40), CITip: strings.Repeat("b", 40), CIReceipt: "exact check passed", Entries: []PinnedCard{pin}}
	w.must(MergeStep(w.s, MergeReq{Stream: "s1", Stage: stage}))
	assert.Equal(t, Merging, w.state(pin.ID))
	assert.Equal(t, Queued, w.s.Merge.Card(pin.ID).Col)
	assert.Equal(t, Waiting, w.state("dependent"))
	assert.False(t, Delivered(w.s, pin.ID))
	at := w.s.Work.Card(pin.ID).F("staged_at")
	w.tick(time.Minute)
	w.must(MergeStep(w.s, MergeReq{Stream: "s1", Stage: stage}))
	assert.Equal(t, at, w.s.Work.Card(pin.ID).F("staged_at"), "replay preserves staging time")
	assert.Equal(t, Waiting, w.state("dependent"))
}

func TestPromotionBareFactsAndMismatchedProofCannotLand(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"bare", "stale epoch", "wrong head", "wrong attempt", "wrong CI tip", "wrong target", "no review"} {
		t.Run(name, func(t *testing.T) {
			w, pin := promotionWorld(t)
			tip := strings.Repeat("b", 40)
			receipt := &DevReceipt{Epoch: w.s.Epoch, Repo: "owner/repo", Branch: "dev", Tip: tip, CandidateTip: tip, CITip: tip, CIReceipt: "exact check", Review: "approved exact diff", BatchID: "batch", VerifiedAt: w.s.Now, Entries: []PinnedCard{pin}}
			switch name {
			case "bare":
				receipt = nil
			case "stale epoch":
				receipt.Epoch++
			case "wrong head":
				receipt.Entries[0].Head = strings.Repeat("c", 40)
			case "wrong attempt":
				receipt.Entries[0].Attempt = "999"
			case "wrong CI tip":
				receipt.CITip = strings.Repeat("c", 40)
			case "wrong target":
				receipt.Branch = "work"
			case "no review":
				receipt.Review = ""
			}
			p := MergeStep(w.s, MergeReq{Stream: "s1", Dev: receipt})
			require.NotEmpty(t, p.Refused)
			assert.Empty(t, p.Units)
			assert.Equal(t, Merging, w.state(pin.ID))
		})
	}
}

func TestPromotionDevProofLandsAndReleasesExactlyItsNeed(t *testing.T) {
	t.Parallel()
	w, pin := promotionWorld(t)
	w.must(Add(w.s, AddReq{IDs: []string{"dependent"}, Stream: "s2", Needs: []string{pin.ID}, Brief: proBrief}))
	tip := strings.Repeat("b", 40)
	receipt := &DevReceipt{Epoch: w.s.Epoch, Repo: "owner/repo", Branch: "dev", Tip: tip, CandidateTip: strings.Repeat("c", 40), CITip: strings.Repeat("c", 40), CIReceipt: "exact candidate check", Review: "queue review", BatchID: "batch", VerifiedAt: w.s.Now, Entries: []PinnedCard{pin}}
	w.must(MergeStep(w.s, MergeReq{Stream: "s1", Dev: receipt}))
	assert.True(t, Delivered(w.s, pin.ID))
	assert.Equal(t, Landed, w.state(pin.ID))
	assert.Equal(t, Ready, w.state("dependent"))
	delete(w.s.Work.Card(pin.ID).Fields, "dev_review")
	assert.False(t, Delivered(w.s, pin.ID), "legacy landed label without receipt is unknown")
}
