package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// syncDevBeforeLanding runs the opt-in cycle sync on the land invocation's
// explicit clone, base, and tree gate. Any failure or conflict holds every
// landing batch in this invocation.
func (l *lander) syncDevBeforeLanding(ctx context.Context, snapshot *sprint.Snapshot, streams []string) string {
	if l.twin {
		return "--dev-sync cannot run on an in-memory twin"
	}
	// Apply the same placement/authorization and merge-window/forge-queue
	// policy as a batch before the sync can push its base.
	var targets []landCard
	for _, name := range streams {
		queue := landQueue(snapshot, name)
		cards := l.landCards(snapshot, name, queue)
		for len(cards) > 0 {
			n := 1
			for n < len(cards) && cards[n].repo == cards[0].repo && cards[n].base == cards[0].base {
				n++
			}
			group := cards[:n]
			if group[0].base != l.base {
				return "--dev-sync base " + l.base + " does not match queued card " + group[0].id + " base " + group[0].base
			}
			if why, _ := l.placeWhy(name, group); why != "" {
				return why
			}
			if why := l.pause(ctx, snapshot, group[0].repo, group[0].base); why != "" {
				return why
			}
			targets = append(targets, group[0])
			cards = cards[n:]
		}
	}
	if len(targets) == 0 {
		return ""
	}
	req := sprint.DevSyncReq{
		RepoDir: l.repoDir,
		Base:    l.base,
		Env:     l.a.gitEnv,
		Check: func(ctx context.Context, dir string) error {
			why, _ := l.runCheck(ctx, dir)
			if why != "" {
				return errors.New(why)
			}
			return nil
		},
		BeforePush: func(ctx context.Context) error {
			l.a.serial.Lock()
			fresh, err := l.st.Load(ctx, []string{sprint.Work, sprint.Merge}, nil)
			l.a.serial.Unlock()
			if err != nil {
				return fmt.Errorf("could not reload landing policy: %w", err)
			}
			for _, target := range targets {
				if why := l.pause(ctx, fresh, target.repo, target.base); why != "" {
					return errors.New(why)
				}
			}
			return nil
		},
	}
	facts, due, err := sprint.LandCycleSync(ctx, snapshot, req)
	if err != nil {
		return err.Error()
	}
	if !due {
		return ""
	}
	step := store.Step{
		Verb:    "dev sync",
		Load:    []string{sprint.Merge, sprint.Work},
		Args:    store.ArgsOf(facts),
		Actor:   l.c.actor,
		Named:   true,
		Mirrors: true,
		Plan: func(s *sprint.Snapshot) sprint.Plan {
			return sprint.DevSynced(s, facts, nil)
		},
	}
	epoch := l.epoch
	step.Epoch = &epoch
	if l.c.op != "" {
		step.CallerOp = l.c.op + ".dev-sync." + step.Args
	}
	l.a.serial.Lock()
	result, err := l.st.Run(ctx, step)
	l.a.serial.Unlock()
	if err != nil {
		return "dev sync facts were not recorded: " + oneline.Err(err)
	}
	if len(result.Refused) > 0 {
		return "dev sync facts were not recorded: " + stepWhy(result, nil)
	}
	if facts.Conflict {
		return fmt.Sprintf("dev sync conflict merging %s into %s in %v; the judgment was recorded and no landing batch was started", facts.Dev, facts.Base, facts.Files)
	}
	return ""
}
