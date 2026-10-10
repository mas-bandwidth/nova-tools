package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// The run loop's friend reconcile (docs/SPEC-SPRINT.md section 1,
// friend-reconcile-every-tick-r.w1; 2026-10-04: one friend's row read working=4 with
// nothing running, and it stayed so until the coordinator typed friend reconcile). After
// each tick of a RUNNING machine, run reconciles every friend of the friends table with
// the plan friend reconcile applies (reconcileFriend), as the machine, so each fix is the
// verb's own history line and nothing new decides. Her directory is her row's dir, else <HOME>/<friend>-working,
// HOME the server's, as friend sync and friend reconcile take it when given no --root. It
// is bounded: one stat walk of each friend's directory a tick (her directory, her
// QUEUE.json, and each card working on her row's outbox/<job>/REPORT.md), and a friend
// whose directory or account cannot be read from the server is skipped, one record line
// an episode and never an error. A card it moves pushes the coordinator one note naming
// it and why (sprint.FriendReturnReq.Push, sprint.FriendCollected); a friend whose row
// still disagrees with her QUEUE.json after the pass is one note an episode
// (sprint.FriendDisagree). The cards the tick has just dealt are not yet delivered (friend
// sync delivers them): her account was written before their deal, so the verb's rule keeps
// them (sprint.FriendReconcileOf).

// friendTick is what the run loop's reconcile keeps from one tick to the next: the why of
// each friend skipped, said once until she is reachable again, and each friend's
// disagreement as last written, so the episode's step runs only when it changes.
type friendTick struct {
	skipped  map[string]string
	disagree map[string]bool
}

func newFriendTick() *friendTick {
	return &friendTick{skipped: map[string]string{}, disagree: map[string]bool{}}
}

// skip says once that a friend's pass is skipped, and why; the same why again says nothing.
func (ft *friendTick) skip(friend, why string, stdout io.Writer) {
	if ft.skipped[friend] == why {
		return
	}
	ft.skipped[friend] = why
	fmt.Fprintf(stdout, "FRIEND-RECONCILE SKIPPED friend=%s: %s; the run loop tries again each tick and says this once\n", friend, oneline.Escape(why))
}

// reconcileFriendsTick is the run loop's pass over every friend, after a tick. A read of the
// store that fails is said and the pass ends: the next tick reads again.
func (a *app) reconcileFriendsTick(ctx context.Context, st *store.Store, ft *friendTick, stdout io.Writer) {
	names, err := st.FriendNames(ctx)
	if err != nil {
		fmt.Fprintf(stdout, "FRIEND-RECONCILE FAILED the friends table cannot be read: %s; the next tick reads it again; run: nova-sprint where --all\n", oneline.Escape(err.Error()))
		return
	}
	if len(names) == 0 {
		return
	}
	dirs, err := st.FriendDirs(ctx)
	if err != nil {
		fmt.Fprintf(stdout, "FRIEND-RECONCILE FAILED the friends table cannot be read: %s; the next tick reads it again; run: nova-sprint where --all\n", oneline.Escape(err.Error()))
		return
	}
	root := a.getenv("HOME")
	for _, friend := range names {
		if root == "" && dirs[friend] == "" {
			ft.skip(friend, "HOME is not set on the server and her nova-config row has no dir, so her working directory is not known", stdout)
			continue
		}
		if err := a.reconcileFriendTick(ctx, st, ft, friend, a.friendDir(friend, dirs[friend], root, stdout), stdout); err != nil {
			fmt.Fprintf(stdout, "FRIEND-RECONCILE FAILED friend=%s: %s; the next tick reads her again; run: nova-sprint friend reconcile %s --dry-run\n", friend, oneline.Escape(err.Error()), friend)
		}
	}
}

// reconcileFriendTick is one friend's pass, holding the server's line of control as every
// step of the server does (serve.go): her directory and account read, each card working on
// her row settled, and her disagreement episode written when it changed.
func (a *app) reconcileFriendTick(ctx context.Context, st *store.Store, ft *friendTick, friend, dir string, stdout io.Writer) error {
	fi, err := os.Stat(dir)
	switch {
	case err != nil:
		ft.skip(friend, dir+" is not reachable from the server: "+err.Error(), stdout)
		return nil
	case !fi.IsDir():
		ft.skip(friend, dir+" is not a directory", stdout)
		return nil
	}
	account, why, err := friendQueueRead(dir)
	if err == nil && why != "" {
		err = fmt.Errorf("%s", why)
	}
	if err != nil {
		ft.skip(friend, "her account cannot be read: "+err.Error(), stdout)
		return nil
	}
	delete(ft.skipped, friend)
	a.serial.Lock()
	defer a.serial.Unlock()
	// the moves alone are said: a card kept, or a stray of her queue, is the same each tick
	say := func(l string) {
		if !strings.HasPrefix(l, "FRIEND-RECONCILE KEPT") && !strings.HasPrefix(l, "NOTE ") {
			fmt.Fprintln(stdout, l)
		}
	}
	if _, err := a.reconcileFriend(ctx, st, reconcileReq{friend: friend, dir: dir, account: account, who: sprint.MachineActor, say: say, push: true}); err != nil {
		return err
	}
	cards, err := st.ReadCells(ctx, sprint.Fleet, sprint.FriendRow(friend), sprint.Working)
	if err != nil {
		return err
	}
	r := sprint.FriendDisagreeReq{Friend: friend, Who: sprint.MachineActor, Row: len(cards), Queue: sprint.FriendQueueWorking(account)}
	disagree := r.Row != r.Queue
	if was, ok := ft.disagree[friend]; ok && was == disagree {
		return nil
	}
	res, err := st.Run(ctx, store.Step{Verb: "friend reconcile", Args: store.ArgsOf(r), Load: []string{sprint.Fleet},
		Plan: func(s *sprint.Snapshot) sprint.Plan { return sprint.FriendDisagree(s, r) }})
	if err != nil {
		return err
	}
	for _, m := range res.Moved {
		fmt.Fprintf(stdout, "FRIEND-RECONCILE %s\n", oneline.Escape(m))
	}
	ft.disagree[friend] = disagree
	return nil
}

// pushCollected pushes the coordinator the one note of a card the run loop's reconcile
// collected (sprint.FriendCollected).
func (a *app) pushCollected(ctx context.Context, st *store.Store, r sprint.FriendCollectedReq) error {
	_, err := st.Run(ctx, store.Step{Verb: "friend reconcile", Args: store.ArgsOf(r),
		Plan: func(s *sprint.Snapshot) sprint.Plan { return sprint.FriendCollected(s, r) }})
	return err
}
