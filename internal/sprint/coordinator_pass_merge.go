package sprint

import (
	"fmt"
	"slices"
	"strings"
)

// Merge health, the pass's fourth condition (docs/SPEC-SPRINT.md section 8, "The
// coordinator's pass", merge health; the owner, 2026-10-05: "How can we ensure that you
// ALWAYS do the merging properly from now on, vs. drifting and forgetting?", and "We really
// need to stop this branch divergence"). One judgment about the sprint listing what is
// wrong of: the base red at its tip, dev behind (DevBehind), the promotion PR's state, and
// the branches not on the base. It is an episode as the pass's others are: raised once,
// rewritten in place with the latest lines, raised again every PassEvery of running time
// while any line holds, closed when none does (tla/CoordinatorPass.tla: one condition on
// one subject, its lines the facts it is rewritten with).
//
// Dev behind and a stream the base-gate rule stopped (NBaseRed) are the store's facts and
// read here; the base's tip, the promotion PR and the branches are the forge's and the
// repository's, read by the binding with the tick (TickReq.Merge) from the facts the drift
// alarms compute. The pass never reads git or the forge itself.

// NMergeHealth is the pass's merge-health judgment.
const NMergeHealth = "merge health: the base, dev and the branches are not stitched"

// The promotion PR's states that are a line of merge health: every state of an open PR.
const (
	PROpen       = "open"
	PRQueued     = "queued"
	PRFailing    = "failing"
	PRConflicted = "conflicted"
)

// MergeFacts is what the forge and the repository say of the merge, read by the binding
// with the tick; nil in the tick's request is none read.
type MergeFacts struct {
	Base    string // the base branch
	BaseSha string // its tip
	// BaseRed is why the base's tip fails its gate; "" while it is green or not known.
	BaseRed string
	// Promotion is the open promotion PR into dev; nil is none open.
	Promotion *PromotionPR
	// Branches are the branches named by an open card or by the running server, each with
	// its commits ahead of the base and behind it.
	Branches []BranchLag
}

// PromotionPR is the open promotion PR: its number and state (PROpen, PRQueued, PRFailing,
// PRConflicted).
type PromotionPR struct {
	Number int
	State  string
}

// BranchLag is one branch against the base: who names it (a card, the server), and its
// commits ahead and behind. A branch with commits ahead is not on the base.
type BranchLag struct {
	Branch string
	Named  string
	Ahead  int
	Behind int
}

// MergeHealthLines are the lines of merge health that hold now, in a fixed order: the base
// red, dev behind, the promotion PR, then the branches not on the base by name. None is
// merge health well.
func MergeHealthLines(s *Snapshot, m *MergeFacts) []string {
	var lines []string
	base := "the base"
	if m != nil && m.Base != "" {
		base = m.Base
	}
	if m != nil && m.BaseRed != "" {
		lines = append(lines, fmt.Sprintf("base %s is red at %s: %s", base, orDash(m.BaseSha), m.BaseRed))
	}
	var stopped []string
	seen := map[string]bool{}
	for _, o := range s.Open {
		if o.Note.Kind == Judgment && o.Note.Type == NBaseRed && !seen[o.Note.Stream] {
			seen[o.Note.Stream] = true
			stopped = append(stopped, o.Note.Stream)
		}
	}
	slices.Sort(stopped)
	for _, st := range stopped {
		lines = append(lines, fmt.Sprintf("stream %s stopped: its base fails its tree gate", st))
	}
	if d, ok := DevBehind(s); ok {
		lines = append(lines, d.Line())
	}
	if m != nil && m.Promotion != nil {
		lines = append(lines, fmt.Sprintf("promotion PR #%d is %s", m.Promotion.Number, orDash(m.Promotion.State)))
	}
	if m != nil {
		bs := slices.Clone(m.Branches)
		slices.SortFunc(bs, func(a, b BranchLag) int { return strings.Compare(a.Branch, b.Branch) })
		for _, b := range bs {
			if b.Ahead <= 0 {
				continue
			}
			lines = append(lines, fmt.Sprintf("branch %s (%s) is not on %s: %d ahead, %d behind", b.Branch, orDash(b.Named), base, b.Ahead, b.Behind))
		}
	}
	return lines
}

// mergeHealthConds is the one merge-health condition while any line holds.
func mergeHealthConds(s *Snapshot, r TickReq) []cond {
	lines := MergeHealthLines(s, r.Merge)
	if len(lines) == 0 {
		return nil
	}
	return []cond{{typ: NMergeHealth, streamLevel: true,
		what: fmt.Sprintf("merge health: %s; stitch them: merge the base into each branch or land it, fix the base, promote into dev (docs/SPEC-SPRINT.md section 7)",
			strings.Join(lines, "; ")),
		decisions: TickDecisions[NMergeHealth]}}
}
