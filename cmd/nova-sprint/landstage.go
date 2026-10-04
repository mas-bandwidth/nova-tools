package main

import (
	"context"
	"fmt"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"time"
)

// staged reports work-branch integration without landing or cleanup; every
// accepted head is checked against the pushed immutable tip (Sprint §7).
func (l *lander) staged(ctx context.Context, b landBatch, stream string, pins []landCard) bool {
	b.IDs = make([]string, len(pins))
	for i, pin := range pins {
		b.IDs[i] = pin.id
	}
	b.Cards = len(pins)
	repo := b.Repo
	if repo == "" {
		var err error
		repo, err = l.git(ctx, b.Dir, "remote", "get-url", "origin")
		if err != nil {
			b.Status, b.Reason = "failed", firstLine("", err)
			l.out = append(l.out, b)
			return false
		}
	}
	receipt := &sprint.StageReceipt{Epoch: l.epoch, Repo: repo, Base: b.Base, Tip: b.Tip, CITip: b.Tip, CIReceipt: "local check: " + l.check}
	for _, c := range pins {
		if _, err := l.git(ctx, b.Dir, "merge-base", "--is-ancestor", c.head, b.Tip); err != nil {
			b.Status, b.Reason = "failed", "pushed staging tip does not contain "+c.id+": "+firstLine("", err)
			l.out = append(l.out, b)
			return false
		}
		resolved, e := l.git(ctx, b.Dir, "rev-parse", "--verify", c.head+"^{commit}")
		if e != nil {
			b.Status, b.Reason = "failed", "cannot resolve exact accepted head: "+firstLine("", e)
			l.out = append(l.out, b)
			return false
		}
		receipt.Entries = append(receipt.Entries, sprint.PinnedCard{ID: c.id, Head: c.head, Attempt: c.attempt, ResolvedHead: resolved})
	}
	start := time.Now()
	res, err := l.step(sprint.MergeReq{Stream: stream, Stage: receipt, Who: l.c.actor}, pins)
	if b.Times != nil {
		since(&b.Times.Report, start)
	}
	if stepExit(res, err) != 0 || len(res.Moved) != len(pins) {
		b.Status, b.Reason = "failed", fmt.Sprintf("staging pushed to %s at %s but receipt not recorded (%s); %s", b.Base, b.Tip, stepWhy(res, err), againRemedy(stream))
		l.out = append(l.out, b)
		return false
	}
	b.Status, b.Delivery = "ok", "staged"
	b.Also = append(b.Also, "work stays merging and dependencies stay held; run: nova-sprint promote --dry-run")
	l.out = append(l.out, b)
	return true
}
