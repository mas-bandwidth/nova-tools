package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// The run loop's friend reconcile (docs/SPEC-SPRINT.md section 1,
// friend-reconcile-every-tick-r.w1; 2026-10-04: one friend's row read working=4 with
// nothing running, and it stayed so until the coordinator typed friend reconcile). After
// each tick of a RUNNING machine, run reconciles every friend of the friends table with
// the plan friend reconcile applies (reconcileFriend), as the machine, so each fix is the
// verb's own history line and nothing new decides. Her directory is <HOME>/<friend>-working,
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
	// stalled is the friends whose directory did not answer within FriendReadDeadline
	// since their last pass that got through: the coordinator is pushed one note an
	// episode
	stalled map[string]bool
}

func newFriendTick() *friendTick {
	return &friendTick{skipped: map[string]string{}, disagree: map[string]bool{}, stalled: map[string]bool{}}
}

// FriendReadDeadline bounds each read of a friend's directory by the run loop's reconcile
// (her directory's stat, her QUEUE.json, each card's outbox report): the reconcile runs in
// the tick's turn of the line (run.go), so a directory that does not answer (a stalled
// network mount) would hold every worker's step behind it. Past it the friend is passed
// over for this tick, said once and pushed to the coordinator once an episode, and the
// read is left to finish on its own: it only reads.
const FriendReadDeadline = 2 * time.Second

// errFriendDirStalled is a read of a friend's directory that did not answer within
// FriendReadDeadline.
var errFriendDirStalled = errors.New("her directory did not answer within " + FriendReadDeadline.String())

// friendRead runs read, a read of a friend's directory, and waits for what it read at
// most FriendReadDeadline by the app's clock (a.after): past it, errFriendDirStalled, and
// read goes on alone; what it reads then is sent to a channel no one reads, and shares
// nothing with the caller.
func friendRead[T any](a *app, dir string, read func() T) (T, error) {
	got := make(chan T, 1)
	go func() {
		if a.friendDirHook != nil {
			a.friendDirHook(dir)
		}
		got <- read()
	}()
	select {
	case v := <-got:
		return v, nil
	case <-a.after(FriendReadDeadline):
		select {
		case v := <-got: // it answered as the deadline came
			return v, nil
		default:
			var zero T
			return zero, errFriendDirStalled
		}
	}
}

// friendStalled passes a friend over for this tick: said once (skip), and one note pushed
// to the coordinator an episode (sprint.NFriendDirStalled), which ends at her next pass
// that gets through.
func (a *app) friendStalled(ctx context.Context, st *store.Store, ft *friendTick, friend, dir, what string, stdout io.Writer) {
	why := dir + " did not answer " + what + " within " + FriendReadDeadline.String() + ": she is passed over this tick"
	ft.skip(friend, why, stdout)
	if ft.stalled[friend] {
		return
	}
	ft.stalled[friend] = true
	_, err := st.Run(ctx, store.Step{Verb: "friend reconcile", Actor: sprint.MachineActor, Plan: func(s *sprint.Snapshot) sprint.Plan {
		return sprint.Plan{Notes: []sprint.Note{{Kind: sprint.Happened, Type: sprint.NFriendDirStalled, Who: sprint.MachineActor, At: s.Now, To: s.Coordinator,
			What: "friend " + friend + ": " + why, Hint: "look at the mount of " + dir + " on the server; nova-sprint friend reconcile " + friend + " --dry-run"}}}
	}})
	if err != nil {
		fmt.Fprintf(stdout, "FRIEND-RECONCILE FAILED friend=%s: the note of her stalled directory was not written: %s\n", friend, oneline.Escape(err.Error()))
	}
}

// skip says once that a friend's pass is skipped, and why; the same why again says nothing.
func (ft *friendTick) skip(friend, why string, stdout io.Writer) {
	if ft.skipped[friend] == why {
		return
	}
	ft.skipped[friend] = why
	fmt.Fprintf(stdout, "FRIEND-RECONCILE SKIPPED friend=%s: %s; the run loop tries again each tick and says this once\n", friend, oneline.Escape(why))
}

// reconcileFriendsTick is the run loop's pass over every friend, after a tick, in the tick's
// turn of the line (the caller holds it). A read of the store that fails is said and the pass
// ends: the next tick reads again.
func (a *app) reconcileFriendsTick(ctx context.Context, st *store.Store, ft *friendTick, stdout io.Writer) {
	names, err := st.FriendNames(ctx)
	if err != nil {
		fmt.Fprintf(stdout, "FRIEND-RECONCILE FAILED the friends table cannot be read: %s; the next tick reads it again; run: nova-sprint where --all\n", oneline.Escape(err.Error()))
		return
	}
	if len(names) == 0 {
		return
	}
	root := a.getenv("HOME")
	for _, friend := range names {
		if root == "" {
			ft.skip(friend, "HOME is not set on the server, so her working directory is not known", stdout)
			continue
		}
		if err := a.reconcileFriendTick(ctx, st, ft, friend, filepath.Join(root, friend+"-working"), stdout); err != nil {
			fmt.Fprintf(stdout, "FRIEND-RECONCILE FAILED friend=%s: %s; the next tick reads her again; run: nova-sprint friend reconcile %s --dry-run\n", friend, oneline.Escape(err.Error()), friend)
		}
	}
}

// reconcileFriendTick is one friend's pass, run by the run loop while it holds the server's
// line of control for the tick (run.go), as every step of the server holds it (serve.go):
// her directory and account read, each card working on her row settled, and her
// disagreement episode written when it changed.
func (a *app) reconcileFriendTick(ctx context.Context, st *store.Store, ft *friendTick, friend, dir string, stdout io.Writer) error {
	// her directory and her account, read within FriendReadDeadline
	type dirRead struct {
		fi      os.FileInfo
		statErr error
		account sprint.FriendAccount
		why     string
		err     error
	}
	d, stalled := friendRead(a, dir, func() (d dirRead) {
		if d.fi, d.statErr = os.Stat(dir); d.statErr == nil && d.fi.IsDir() {
			d.account, d.why, d.err = friendQueueRead(dir)
		}
		return d
	})
	if stalled != nil {
		a.friendStalled(ctx, st, ft, friend, dir, "its stat or her inbox/QUEUE.json read", stdout)
		return nil
	}
	switch {
	case d.statErr != nil:
		ft.skip(friend, dir+" is not reachable from the server: "+d.statErr.Error(), stdout)
		return nil
	case !d.fi.IsDir():
		ft.skip(friend, dir+" is not a directory", stdout)
		return nil
	}
	account, why, err := d.account, d.why, d.err
	if err == nil && why != "" {
		err = fmt.Errorf("%s", why)
	}
	if err != nil {
		ft.skip(friend, "her account cannot be read: "+err.Error(), stdout)
		return nil
	}
	// each card's report, read within FriendReadDeadline too
	type reportRead struct {
		report, why string
		at          time.Time
		err         error
	}
	readReport := func(dir, job string) (string, string, time.Time, error) {
		r, stalled := friendRead(a, dir, func() (r reportRead) {
			r.report, r.why, r.at, r.err = friendReadReport(dir, job)
			return r
		})
		if stalled != nil {
			return "", "", time.Time{}, stalled
		}
		return r.report, r.why, r.at, r.err
	}
	// the moves alone are said: a card kept, or a stray of her queue, is the same each tick
	say := func(l string) {
		if !strings.HasPrefix(l, "FRIEND-RECONCILE KEPT") && !strings.HasPrefix(l, "NOTE ") {
			fmt.Fprintln(stdout, l)
		}
	}
	if _, err := a.reconcileFriend(ctx, st, reconcileReq{friend: friend, dir: dir, account: account, who: sprint.MachineActor, say: say, push: true, readReport: readReport}); err != nil {
		if errors.Is(err, errFriendDirStalled) {
			// what the pass settled before the stall stands, each its own step
			a.friendStalled(ctx, st, ft, friend, dir, "a card's outbox report read", stdout)
			return nil
		}
		return err
	}
	delete(ft.skipped, friend)
	delete(ft.stalled, friend)
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
