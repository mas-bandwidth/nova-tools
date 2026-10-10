package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// friend reconcile <friend> (docs/SPEC-SPRINT.md section 1, friend reconcile; the owner,
// 2026-10-04: "trust but VERIFY"; "Are they actually doing the work that is shown in the
// friend table? Really?"): a friend said every card was done while the table showed one
// working. The verb reads her own account, <root>/<friend>-working/inbox/QUEUE.json, and her
// outbox, and settles each card working on her row (sprint.FriendReconcileOf): collected
// from outbox/<job>/REPORT.md as friend sync collects it (friendCollect), kept while her
// queue holds it queued or working, and returned to ready (sprint.FriendReturn, one step,
// all or none) when no report is there and her queue says done, or does not hold it
// though it was written after the card's deal. An id of her queue that is no card working
// on her row is named, never acted on. --dry-run says each card's settlement and writes
// nothing. --op gives each collect and the return its own operation id under it
// (friendCollect; op.return.<its args>), so a retry with the same --op replays.

// friendQueueReadCap bounds the QUEUE.json reconcile reads in one ReadFile: a task is a
// line of tens of bytes, and a friend works a width of cards.
const friendQueueReadCap = 1 << 20

// friendQueueRead reads a friend's inbox/QUEUE.json under her working directory dir, only
// when it is a regular file no larger than friendQueueReadCap (a link would read a file
// outside her directory); why names what is wrong with it, with the path.
func friendQueueRead(dir string) (account sprint.FriendAccount, why string, err error) {
	path := filepath.Join(dir, "inbox", "QUEUE.json")
	fi, err := os.Lstat(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return account, path + " is not there: her own account of her cards is what reconcile compares the store with", nil
	case err != nil:
		return account, "", err
	case !fi.Mode().IsRegular():
		return account, path + " is a symlink or a non-regular file", nil
	case fi.Size() > friendQueueReadCap:
		return account, path + " is larger than " + strconv.Itoa(friendQueueReadCap) + " bytes", nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return account, "", err
	}
	if account.Tasks, err = sprint.ReadFriendQueue(b); err != nil {
		return account, path + ": " + err.Error() + `; it wants {"tasks":[{"id":"<card>","state":"queued|working|done"}]}`, nil
	}
	account.Written = fi.ModTime()
	return account, "", nil
}

func (a *app) cmdFriendReconcile(args []string, stdout, stderr io.Writer) int {
	const name = "friend reconcile"
	fs, c := a.verbSetup(name)
	root := fs.String("root", "", "the directory the friends' working directories are under, <root>/<friend>-working (else HOME); her inbox/QUEUE.json and outbox are read there, and nothing is written there")
	dry := fs.Bool("dry-run", false, "say what each card working on her row would get (collect, keep, return) and write nothing; the reads of the store and her directory are made")
	friend, code := oneFriend(name, fs, args, stderr)
	if code != 0 {
		return code
	}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, name, err.Error())
	}
	ctx := context.Background()
	names, err := st.FriendNames(ctx)
	if err != nil {
		return a.readFailed(name, err, stderr)
	}
	if !slices.Contains(names, friend) {
		fmt.Fprintf(stderr, "%s %s: no friend %s on the friends table (friends: %s): its row is nova-config's friend row; run: nova-sprint friend sync; nothing was changed\n", prog, name, friend, orDashStr(strings.Join(names, ","), "none"))
		return 1
	}
	if *root == "" {
		*root = a.getenv("HOME")
	}
	if *root == "" {
		return refuse(stderr, name, "wants --root <dir>, the directory the friends' working directories are under (HOME is not set)")
	}
	dir := filepath.Join(*root, friend+"-working")
	account, why, err := friendQueueRead(dir)
	switch {
	case err != nil:
		fmt.Fprintf(stderr, "%s %s: her account cannot be read: %s; nothing was changed\n", prog, name, oneline.WithRemedy(err.Error(), "ls -la "+filepath.Join(dir, "inbox")))
		return 1
	case why != "":
		fmt.Fprintf(stderr, "%s %s: %s; run: ls -la %s; nothing was changed\n", prog, name, oneline.Escape(why), filepath.Join(dir, "inbox"))
		return 1
	}
	var said []string
	say := func(l string) {
		said = append(said, l)
		if !c.json {
			fmt.Fprintln(stdout, l)
		}
	}
	dryWord := ""
	if *dry {
		dryWord = " (dry run: nothing written)"
	}
	t, err := a.reconcileFriend(ctx, st, reconcileReq{friend: friend, dir: dir, account: account, dry: *dry, op: c.op, who: c.actor, say: say})
	if err != nil {
		return a.readFailed(name, err, stderr)
	}
	collected, kept, returned, refused, strays, packets := t.collected, t.kept, t.returned, t.refused, t.strays, t.cards
	line := fmt.Sprintf("collected=%d kept=%d returned=%d refused=%d strays=%d", collected, kept, returned, refused, len(strays))
	facts := map[string]any{"friend": friend, "collected": collected, "kept": kept, "returned": returned, "refused": refused, "strays": orEmpty(strays), "dry_run": *dry, "cards": orEmpty(said)}
	if refused > 0 {
		// the store and her account still disagree: the exit says so (docs/STANDARD.md, exit codes)
		if c.json {
			facts["verb"], facts["status"], facts["exit"] = name, "failed", 1
			// ignored: a map of strings, numbers, booleans and lists of strings always encodes
			b, _ := json.Marshal(facts)
			fmt.Fprintln(stdout, string(b))
		} else {
			fmt.Fprintf(stdout, "FRIEND-RECONCILE FAILED friend=%s %s: %d cards were not settled, each on its line; run: nova-sprint friend reconcile %s\n", friend, line, refused, friend)
		}
		return 1
	}
	if packets+len(strays) == 0 {
		line += ": nothing to do, no card is working on her row and her QUEUE.json names none"
	}
	sayOK(stdout, c.json, name, "FRIEND-RECONCILE OK friend="+friend+" "+line+dryWord, facts)
	return 0
}

// reconcileReq is one friend's reconcile: her name, her working directory, her account
// as read there (friendQueueRead), and how it runs: --dry-run and --op, who acts, the
// line each card says, and push, the run loop's, which addresses each card moved to the
// coordinator (friendreconcile_tick.go).
type reconcileReq struct {
	friend, dir string
	account     sprint.FriendAccount
	dry         bool
	op, who     string
	say         func(string)
	push        bool
	// readReport reads a card's outbox report (friendReadReport when nil): the run loop's
	// pass gives one bounded by FriendReadDeadline (friendreconcile_tick.go)
	readReport func(dir, job string) (report, why string, at time.Time, err error)
}

// reconcileTally is what one reconcile did: the cards working on her row it read, each
// one's settlement counted, and her queue's strays.
type reconcileTally struct {
	cards, collected, kept, returned, refused int
	strays                                    []string
}

// reconcileFriend settles each card working on a friend's row against her account (docs/
// SPEC-SPRINT.md section 1, friend reconcile): the plan friend reconcile and the run
// loop's pass (friendreconcile_tick.go) both apply, so nothing but this decides. It
// writes nothing in her directory; an error is a read or a write of the store.
func (a *app) reconcileFriend(ctx context.Context, st *store.Store, r reconcileReq) (reconcileTally, error) {
	var t reconcileTally
	friend, dir, account, say := r.friend, r.dir, r.account, r.say
	dryWord := ""
	if r.dry {
		dryWord = " (dry run: nothing written)"
	}
	cards, err := st.ReadCells(ctx, sprint.Fleet, sprint.FriendRow(friend), sprint.Working)
	if err != nil {
		return t, err
	}
	packets, err := st.Packets(ctx, cards)
	if err != nil {
		return t, err
	}
	byID := make(map[string]*sprint.Card, len(cards))
	for _, wc := range cards {
		byID[wc.ID] = wc
	}
	var held []string
	var back []sprint.FriendReturnCard
	collected, kept, refused := 0, 0, 0
	for _, p := range packets {
		job := friendJobOf(p)
		held = append(held, p.Card, job)
		readReport := r.readReport
		if readReport == nil {
			readReport = friendReadReport
		}
		report, bad, at, err := readReport(dir, job)
		if err != nil {
			return t, err
		}
		if bad != "" {
			kept++
			say(fmt.Sprintf("FRIEND-RECONCILE KEPT friend=%s card=%s job=%s: %s; left working", friend, p.Card, oneline.Field(job), oneline.Escape(bad)))
			continue
		}
		if p.Kind == "read" && report != "" {
			// a read is closed by friend sync from its report (sprint.FriendReadClose), never
			// collected as work
			kept++
			say(fmt.Sprintf("FRIEND-RECONCILE KEPT friend=%s card=%s job=%s: a read with its report: friend sync closes it; left working", friend, p.Card, oneline.Field(job)))
			continue
		}
		wc := byID[p.Card]
		if wc == nil {
			kept++
			say(fmt.Sprintf("FRIEND-RECONCILE KEPT friend=%s card=%s job=%s: its card was not read with its packet; left working", friend, p.Card, oneline.Field(job)))
			continue
		}
		action, why := sprint.FriendReconcileOf(wc, job, report != "", account)
		switch {
		case action == sprint.ReconcileKeep:
			kept++
			say(fmt.Sprintf("FRIEND-RECONCILE KEPT friend=%s card=%s job=%s: %s", friend, p.Card, oneline.Field(job), oneline.Escape(why)))
		case action == sprint.ReconcileReturn:
			back = append(back, sprint.FriendReturnCard{ID: p.Card, Gen: p.Gen, Why: why})
		case r.dry:
			collected++
			say(fmt.Sprintf("FRIEND-RECONCILE COLLECT friend=%s card=%s job=%s: %s%s", friend, p.Card, oneline.Field(job), oneline.Escape(why), dryWord))
		default:
			done, err := a.friendCollect(ctx, st, friend, p, report, r.op, at, say)
			if err != nil {
				return t, err
			}
			if done {
				collected++
				if r.push {
					if err := a.pushCollected(ctx, st, sprint.FriendCollectedReq{Friend: friend, Card: p.Card, Primary: p.Primary, Stream: p.Stream, Who: r.who, Why: why}); err != nil {
						return t, err
					}
				}
			} else {
				refused++
			}
		}
	}
	returned := len(back)
	if len(back) > 0 && !r.dry {
		step := store.FriendReturnStep(sprint.FriendReturnReq{Friend: friend, Who: r.who, Cards: back, Push: r.push})
		if r.op != "" {
			// the return's own operation id, apart from each collect's (friendCollect), so --op
			// replays the step rather than being accepted and ignored
			step.CallerOp = r.op + ".return." + step.Args
		}
		res, err := st.Run(ctx, step)
		if err != nil {
			return t, err
		}
		for _, r := range res.Refused {
			say(fmt.Sprintf("FRIEND-RECONCILE REFUSED friend=%s card=%s: %s; nothing was returned, and the next reconcile reads it again", friend, oneline.Field(r.Key), oneline.Escape(r.Why)))
		}
		if len(res.Refused) > 0 {
			refused += len(back)
			returned = 0
		}
	}
	if returned > 0 {
		for _, b := range back {
			say(fmt.Sprintf("FRIEND-RECONCILE RETURNED friend=%s card=%s: %s%s", friend, b.ID, oneline.Escape(b.Why), dryWord))
		}
	}
	strays := sprint.FriendQueueStrays(account.Tasks, held)
	for _, id := range strays {
		say(fmt.Sprintf("NOTE friend=%s: her QUEUE.json says %s for %s, which is no card working on her row; nothing was done", friend, oneline.Field(account.Tasks[id]), oneline.Field(id)))
	}
	t.cards, t.collected, t.kept, t.returned, t.refused, t.strays = len(packets), collected, kept, returned, refused, strays
	return t, nil
}
