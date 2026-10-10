package main

import (
	"context"
	"slices"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/mas-bandwidth/nova-tools/pkg/gitrun"
	"github.com/mas-bandwidth/nova-tools/pkg/swarm"
	"github.com/mas-bandwidth/nova-tools/pkg/typedrec"
)

// headsFn is origin's branches of a repository matching a pattern (git ls-remote --heads),
// each with its tip; an error is a listing that could not be read.
type headsFn func(ctx context.Context, repo, pattern string) (map[string]string, error)

// branchHeads is the real headsFn: one git ls-remote of the pattern, bounded as branchTip is.
func (a *app) branchHeads(ctx context.Context, repo, pattern string) (map[string]string, error) {
	out, err := gitrun.Output(ctx, gitrun.Options{Env: a.gitEnv, Timeout: friendTipBudget}, "ls-remote", "--heads", "--", repo, pattern)
	if err != nil {
		return nil, err
	}
	heads := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		if f := strings.Fields(line); len(f) == 2 {
			heads[strings.TrimPrefix(f[1], "refs/heads/")] = f[0]
		}
	}
	return heads, nil
}

// readMissing is the server's check of broken reads at their close (docs/SPEC-SPRINT.md
// section 6, a read on a branch origin does not hold): for each read whose packet names a
// branch origin does not hold, the branch named and the branch of the same work card
// (sprint/<prefix><card>.g*) origin holds the read's head on, "" when none does. A read
// whose brief names no REPO:, whose tip cannot be read, or whose branch origin holds is
// not named: its verdict is the reader's. nil when the app checks nothing.
func (a *app) readMissing(ctx context.Context, packets []sprint.Packet) map[string]sprint.MissingBranch {
	if a.readTip == nil {
		return nil
	}
	var out map[string]sprint.MissingBranch
	for _, p := range packets {
		repo := swarm.ReadCardBase([]byte(p.Brief)).Repo
		if p.Kind != "read" || repo == "" || p.WorkBranch == "" {
			continue
		}
		if tip, err := a.readTip(ctx, repo, p.WorkBranch); err != nil || tip != "" {
			continue
		}
		m := sprint.MissingBranch{Named: p.WorkBranch}
		if i := strings.LastIndex(p.WorkBranch, ".g"); i > 0 && a.readHeads != nil && typedrec.IsFullSha(p.Head) {
			if heads, err := a.readHeads(ctx, repo, "refs/heads/"+p.WorkBranch[:i]+".g*"); err == nil {
				for _, b := range slices.Sorted(func(yield func(string) bool) {
					for b := range heads {
						if !yield(b) {
							return
						}
					}
				}) {
					if b != p.WorkBranch && strings.EqualFold(heads[b], p.Head) {
						m.Holds = b
						break
					}
				}
			}
		}
		if out == nil {
			out = map[string]sprint.MissingBranch{}
		}
		out[p.Card] = m
	}
	return out
}

// readMissingOf is readMissing over the read cards named, read from the readers table and
// the fleet table (a friend's or a member's read card).
func (a *app) readMissingOf(ctx context.Context, st *store.Store, ids []string) map[string]sprint.MissingBranch {
	if a.readTip == nil || len(ids) == 0 {
		return nil
	}
	var cards []*sprint.Card
	for _, table := range []string{sprint.Readers, sprint.Fleet} {
		cs, err := st.Records(ctx, table, ids)
		if err != nil {
			return nil
		}
		for _, c := range cs {
			if c != nil && c.Placed() && c.F("kind") == "read" {
				cards = append(cards, c)
			}
		}
	}
	if len(cards) == 0 {
		return nil
	}
	packets, err := st.Packets(ctx, cards)
	if err != nil {
		return nil
	}
	return a.readMissing(ctx, packets)
}
