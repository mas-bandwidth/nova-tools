package consume

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/redis/go-redis/v9"
)

// PRReadSprints is pr-to-read over every open sprint. `nova-sprint route
// --sprint <S>` serves the one sprint it was started with, so a sprint
// opened after it (quack-0925d, opened after the 05:53Z start) never had
// the group pr-to-read on its log and none of its `pr head` events queued a
// first read. Every Pass reads the open sprints from the `sprints` set
// again and runs one PRRead.OnceN for each: the sprint's lease:route:<S>,
// its group joined on first sight (PRRead.join, from 0, one PRREAD JOINED
// line), then one pass. A sprint whose lease another router holds is served
// by that router and skipped.
type PRReadSprints struct {
	Store    *store.Store
	Instance string // lease instance and stream consumer for every sprint
	Actor    string
	Remote   Remote
	Out      io.Writer

	reads map[string]*PRRead
}

// OpenSprints is every member of `sprints` whose s:<S> status is open, in
// name order, read in one pipeline.
func OpenSprints(ctx context.Context, st *store.Store) ([]string, error) {
	c := st.Client()
	names, err := c.SMembers(ctx, "sprints").Result()
	if err != nil {
		return nil, fmt.Errorf("sprints: %w", err)
	}
	sort.Strings(names)
	pipe := c.Pipeline()
	cmds := make([]*redis.StringCmd, len(names))
	for i, s := range names {
		cmds[i] = pipe.HGet(ctx, "s:"+s, "status")
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, fmt.Errorf("sprint status: %w", err)
	}
	var open []string
	for i, s := range names {
		if cmds[i].Val() == "open" {
			open = append(open, s)
		}
	}
	return open, nil
}

// Pass runs pr-to-read once over every open sprint and returns the moves
// made. A sprint held by another router is skipped; any other failure is
// joined into the error and the remaining sprints still run.
func (a *PRReadSprints) Pass(ctx context.Context) (int, error) {
	if a == nil || a.Store == nil || a.Instance == "" {
		return 0, errors.New("pr-to-read: store and instance are required")
	}
	sprints, err := OpenSprints(ctx, a.Store)
	if err != nil {
		return 0, fmt.Errorf("pr-to-read: %w", err)
	}
	if a.reads == nil {
		a.reads = map[string]*PRRead{}
	}
	n := 0
	var errs []string
	for _, s := range sprints {
		r := a.reads[s]
		if r == nil {
			r = &PRRead{Store: a.Store, Sprint: s, Consumer: a.Instance, Instance: a.Instance,
				Actor: a.Actor, Remote: a.Remote, Out: a.Out}
			a.reads[s] = r
		}
		got, err := r.OnceN(ctx)
		n += got
		var held *LeaseHeldError
		switch {
		case err == nil, errors.As(err, &held):
		default:
			errs = append(errs, fmt.Sprintf("%s: %v", s, err))
		}
	}
	// A sprint no longer open keeps nothing here.
	open := make(map[string]bool, len(sprints))
	for _, s := range sprints {
		open[s] = true
	}
	for s := range a.reads {
		if !open[s] {
			delete(a.reads, s)
		}
	}
	if len(errs) > 0 {
		return n, fmt.Errorf("pr-to-read: %s", strings.Join(errs, "; "))
	}
	return n, nil
}
