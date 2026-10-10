package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// devSyncBefore is --dev-sync, before the first batch (docs/SPEC-SPRINT.md, "Dev sync
// every cycle"). Off, or a dry run, it does nothing: a dry run reads the store only.
// On, the round merges the development branch into the base in its clone through its
// own tree gate (LandCycleSync, RunDevSync) and records the facts (DevSynced) before
// any batch. A conflict stops every stream, so the batches after it are refused as a
// stopped stream's are. A red gate or a refused push pushes nothing and no batch starts.
func (l *lander) devSyncBefore(ctx context.Context, s *sprint.Snapshot) (*sprint.Snapshot, error) {
	if l == nil || !l.devSync || l.dry {
		return s, nil
	}
	base, repo := l.devSyncTarget(s)
	if base == "" {
		return s, errors.New("--dev-sync needs a base: pass --base <branch>, or queue a card whose brief names BASE:; nothing was fetched or pushed")
	}
	dir := l.repoDir
	if dir == "" {
		if repo == "" {
			return s, errors.New("--dev-sync needs the round's clone: pass --repo-dir <clone>, or queue a card whose brief names REPO:; nothing was fetched or pushed")
		}
		var why string
		dir, why = l.clone(ctx, repo)
		if why != "" {
			return s, errors.New(why)
		}
	}
	if why := l.hold(ctx, dir); why != "" {
		return s, errors.New(why)
	}
	var env []string
	if l.a != nil {
		env = l.a.gitEnv
	}
	req := sprint.DevSyncReq{
		RepoDir: dir,
		Base:    base,
		Env:     env,
		// the round's tree gate, the same one a batch's tree passes (treeGate)
		Check: func(ctx context.Context, d string) error {
			if why := l.treeGate(ctx, d, true); why != "" {
				return errors.New(why)
			}
			return nil
		},
	}
	facts, due, err := sprint.LandCycleSync(ctx, s, req)
	if err != nil {
		return s, err
	}
	if !due {
		return s, nil
	}
	epoch := l.epoch
	step := store.Step{
		Verb:  "dev-sync",
		Load:  []string{sprint.Work, sprint.Merge},
		Epoch: &epoch,
		Plan: func(snap *sprint.Snapshot) sprint.Plan {
			return sprint.DevSynced(snap, facts, nil)
		},
	}
	l.a.serial.Lock()
	res, err := l.st.Run(ctx, step)
	l.a.serial.Unlock()
	if err != nil {
		return s, err
	}
	if len(res.Refused) > 0 {
		return s, fmt.Errorf("dev sync was not recorded: %s", stepWhy(res, nil))
	}
	l.a.serial.Lock()
	next, err := l.st.Load(ctx, []string{sprint.Work, sprint.Merge, sprint.Fleet}, nil)
	l.a.serial.Unlock()
	if err != nil {
		return s, err
	}
	return next, nil
}

// devSyncTarget is the base --dev-sync merges into, and the repository whose clone
// it uses when --repo-dir was not given: --base, else the base the first queued
// card names, and that card's repository.
func (l *lander) devSyncTarget(s *sprint.Snapshot) (base, repo string) {
	base = l.base
	if s == nil || s.Work == nil {
		return base, ""
	}
	for _, name := range s.Streams() {
		for _, c := range landQueue(s, name) {
			pr := s.Work.Placed(c.ID)
			if pr == nil {
				continue
			}
			cb := swarm.ReadCardBase([]byte(pr.F("brief")))
			if base == "" {
				base = cb.Ref
			}
			if repo == "" {
				repo = cb.Repo
			}
			if base != "" && (repo != "" || l.repoDir != "") {
				return base, repo
			}
		}
	}
	return base, repo
}
