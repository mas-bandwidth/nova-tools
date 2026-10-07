package main

// The record by place checks the base (docs/SPEC-SPRINT.md section 8,
// no-merge-step-prints-land-and-merge-refuses-a-head-off-the-base): `merge --stream <s>` with
// no fact and no --landed records the head of the queue landed, and a caller that did not
// push cannot know the cards are on the base. Before the step, the verb asks git, once per
// queued card that carries a head, whether that head is an ancestor of its base's tip, and
// gives the answers to the step as facts (sprint.MergeReq.Ancestry); the step refuses the
// record whole, naming each card off the base or not checked, and writes nothing. A bare
// merge --stream once recorded a card landed whose head was not on the base.

import (
	"context"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// mergeAncestry is the fact for each card queued in the stream that carries a head: with
// repo and baseRef, git merge-base --is-ancestor in that clone against the caller's fetched
// ref (as --landed runs it); else the card's base from its brief (REPO: and BASE:), fetched
// from origin in land's clone. A card it cannot check carries why; the step refuses it.
func (a *app) mergeAncestry(ctx context.Context, c common, s *sprint.Snapshot, stream, repo, baseRef string) ([]sprint.LandedPin, error) {
	var heads []headCheck
	for _, m := range s.Merge.Cell(stream, sprint.Queued) {
		pr := s.Work.Placed(m.ID)
		if pr == nil || !shaRE.MatchString(pr.F("head")) {
			continue // no commit to check: the step skips it too (sprint.MergeReq.CheckAncestry)
		}
		cb := swarm.ReadCardBase([]byte(pr.F("brief")))
		heads = append(heads, headCheck{ID: pr.ID, Stream: stream, Head: pr.F("head"), Repo: cb.Repo, Base: cb.Ref})
	}
	if len(heads) == 0 {
		return nil, nil
	}
	l := &lander{a: a, c: c}
	if repo != "" {
		// the caller named a clone and its fetched base ref: check every head there
		var pairs []string
		for _, h := range heads {
			pairs = append(pairs, h.ID+"@"+h.Head)
		}
		pins, err := landedPins(ctx, pairs, repo, baseRef)
		if err != nil {
			return nil, err
		}
		tip, _ := l.git(ctx, repo, "rev-parse", "--verify", baseRef+"^{commit}") // ignored: the tip is for the refusal's words only
		for i := range pins {
			pins[i].Tip = strings.TrimSpace(tip)
		}
		return pins, nil
	}
	for i := range heads {
		if heads[i].Base == "" {
			heads[i].Why = "its brief names no BASE: line; run: nova-sprint merge --stream " + stream + " --repo <clone> --base-ref origin/<base>"
		}
	}
	l.checkHeads(ctx, heads)
	pins := make([]sprint.LandedPin, 0, len(heads))
	for _, h := range heads {
		pins = append(pins, sprint.LandedPin{ID: h.ID, Head: h.Head, InBase: h.Why == "" && !h.Missing, Tip: h.Tip, Why: h.Why})
	}
	return pins, nil
}
