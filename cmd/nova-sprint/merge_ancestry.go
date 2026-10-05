package main

// The record by place checks the base (docs/SPEC-SPRINT.md section 8,
// no-merge-step-prints-land-and-merge-refuses-a-head-off-the-base): `merge --stream <s>` with
// no fact and no --landed records the head of the queue landed, and a caller that did not
// push cannot know the cards are on the base. Before the step, the verb asks git, once per
// queued card that carries a head, whether that head is an ancestor of its base's tip, and
// gives the answers to the step as facts (sprint.MergeReq.Ancestry); the step refuses the
// record whole, naming each card off the base or not checked, and writes nothing. On
// 2026-10-05 a bare merge recorded a card landed whose head was not on the base.

import (
	"context"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// mergeAncestry is the fact for each card queued in the stream that carries a head: with
// repo and baseRef, git merge-base --is-ancestor in that clone against the caller's fetched
// ref (as --landed runs it); else the card's base from its brief (REPO: and BASE:), fetched
// from origin in land's clone, as verify-landed checks a landed card. A card it cannot check
// carries why; the step refuses it.
func (a *app) mergeAncestry(ctx context.Context, c common, s *sprint.Snapshot, stream, repo, baseRef string) ([]sprint.LandedPin, error) {
	var checks []landedCheck
	for _, m := range s.Merge.Cell(stream, sprint.Queued) {
		pr := s.Work.Placed(m.ID)
		if pr == nil || !shaRE.MatchString(pr.F("head")) {
			continue // no commit to check: the step skips it too (sprint.MergeReq.CheckAncestry)
		}
		cb := swarm.ReadCardBase([]byte(pr.F("brief")))
		ch := landedCheck{ID: pr.ID, Stream: stream, Head: pr.F("head"), Repo: cb.Repo, Base: cb.Ref}
		if repo == "" && ch.Base == "" {
			ch.Why = "its brief names no BASE: line; run: nova-sprint merge --stream " + stream + " --repo <clone> --base-ref origin/<base>"
		}
		checks = append(checks, ch)
	}
	if len(checks) == 0 {
		return nil, nil
	}
	if repo != "" {
		var pairs []string
		for _, ch := range checks {
			if ch.Why == "" {
				pairs = append(pairs, ch.ID+"@"+ch.Head)
			}
		}
		pins, err := landedPins(ctx, pairs, repo, baseRef)
		if err != nil {
			return nil, err
		}
		tip, _ := (&lander{a: a, c: c}).git(ctx, repo, "rev-parse", "--verify", baseRef+"^{commit}") // ignored: the tip is for the refusal's words only
		for i := range pins {
			pins[i].Tip = strings.TrimSpace(tip)
		}
		for _, ch := range checks {
			if ch.Why != "" {
				pins = append(pins, sprint.LandedPin{ID: ch.ID, Head: ch.Head, Why: ch.Why})
			}
		}
		return pins, nil
	}
	(&lander{a: a, c: c}).verify(ctx, checks)
	pins := make([]sprint.LandedPin, 0, len(checks))
	for _, ch := range checks {
		pins = append(pins, sprint.LandedPin{ID: ch.ID, Head: ch.Head, InBase: ch.Why == "" && !ch.Missing, Tip: ch.Tip, Why: ch.Why})
	}
	return pins, nil
}
