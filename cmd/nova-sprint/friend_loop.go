package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// friend sync's own exit codes, its -h's line (verbhelp.go): one pass's, and the loop's.
func init() {
	verbExit["friend sync"] = "exit codes: 0 done, 1 refused (a friend row's name, or a working directory that cannot be read), 2 usage or a store that did not answer, 3 the config could not be read or holds no friend row; with --every: 0 interrupted (a failing pass is said and the loop goes on), 2 usage, 3 its binary was replaced (its supervisor starts the new one)"
}

// The friend sync loop (docs/FRIENDS.md, "The friend sync loop"; docs/SPEC-FRIEND.md,
// "The beat comes from the daemon"; the owner, 2026-10-04: "Golang nova-tools and
// nova-sprint verbs only"; "Make the ping loop mechanical!!!!"): friend sync --every
// is the loop, a nova-config loop row kept alive runs it, and no shell wraps it. Each
// pass reopens the store, so it runs at the sprint's epoch then, and acts as the
// sprint's coordinator seat then when no --actor is given, so a seat that moves takes
// the loop with it. A pass that changed something says its lines; a pass with nothing
// to do says nothing; a failing pass is said once on stderr, again only when what it
// says changes, and the pass that is ok after it says so once. An interrupt ends the
// loop with 0, and a binary replaced under it with exitReplaced, so its supervisor
// starts the new one.
func (a *app) friendSyncLoop(c common, pg, root string, every time.Duration, stdout, stderr io.Writer) int {
	ctx := context.Background()
	if a.notify != nil {
		var cancel context.CancelFunc
		ctx, cancel = a.notify(ctx)
		defer cancel()
	}
	after := a.after
	if after == nil {
		after = time.After
	}
	began := a.binaryStamp()
	failing, failed := "", 0
	var since time.Time // the first failing pass of this run of failures
	var recorded string // what the store holds of this failure, "" for nothing
	// a run before this one may have recorded a failure and ended (its binary replaced):
	// the first ok pass of this run clears whatever stands, so a judgment outlives no failure
	clearFirst := true
	for {
		if ctx.Err() != nil {
			fmt.Fprintln(stdout, "FRIEND-SYNC STOP interrupted")
			return 0
		}
		if now := a.binaryStamp(); began != "" && now != began {
			fmt.Fprintln(stdout, "FRIEND-SYNC STOP the binary this loop runs was replaced; its supervisor starts the new one")
			return exitReplaced
		}
		var out, errs bytes.Buffer
		code, changed := 1, false
		if pc, why := a.friendSyncActor(ctx, c); why != "" {
			fmt.Fprintf(&errs, "%s friend sync: %s\n", prog, why)
		} else {
			code, changed = a.friendSyncPass(pc, pg, root, &out, &errs)
		}
		now := a.now()
		stamp := now.Format(time.RFC3339)
		if code != 0 {
			failed++
			if since.IsZero() {
				since = now
			}
			if said := strings.ReplaceAll(strings.TrimSpace(errs.String()), "\n", "; "); said != failing {
				fmt.Fprintf(stderr, "%s FRIEND-SYNC FAILING exit=%d, said once until it changes or a pass is ok; the next try is in %s: %s\n", stamp, code, every, said)
				failing = said
			}
			// past the bound, the failure is recorded on the store, once per text: the
			// tick raises its one judgment on the record (sprint.NFriendSyncFailing)
			if now.Sub(since) >= sprint.FriendSyncJudgeAfter && recorded != failing {
				if why := a.friendSyncRecord(ctx, c, sprint.FriendSyncStateReq{Failing: true, Since: since, Failed: failed, Exit: code, Said: failing}); why != "" {
					fmt.Fprintf(stderr, "%s FRIEND-SYNC NOTE the failure could not be recorded for the coordinator's judgment: %s\n", stamp, why)
				} else {
					recorded = failing
					fmt.Fprintf(stdout, "%s FRIEND-SYNC JUDGMENT recorded after %d failing passes since %s: the tick raises it to the coordinator, and an ok pass clears it\n", stamp, failed, since.Format(time.RFC3339))
				}
			}
		} else {
			if changed {
				// ignored: the loop's log is its supervisor's file; a pass it cannot say is synced all the same
				_, _ = stdout.Write(out.Bytes())
			}
			if failing != "" {
				fmt.Fprintf(stdout, "%s FRIEND-SYNC OK again after %d failing passes\n", stamp, failed)
			}
			if recorded != "" || clearFirst {
				if why := a.friendSyncRecord(ctx, c, sprint.FriendSyncStateReq{Failing: false, Failed: failed}); why != "" {
					fmt.Fprintf(stderr, "%s FRIEND-SYNC NOTE the recovery could not be recorded, so the judgment stays open: %s\n", stamp, why)
				} else {
					recorded, clearFirst = "", false
				}
			}
			failing, failed, since = "", 0, time.Time{}
		}
		select {
		case <-ctx.Done():
		case <-after(every):
		}
	}
}

// friendSyncRecord writes the loop's state to the store (store.FriendSyncStateStep) as
// the seat the pass acts as; why is the reason it could not, "" when it did. The store is
// the sprint's, not the config's: a loop refused by the config store can still be told of.
func (a *app) friendSyncRecord(ctx context.Context, c common, r sprint.FriendSyncStateReq) string {
	pc, why := a.friendSyncActor(ctx, c)
	if why != "" {
		return why
	}
	st, err := a.storeCtx(ctx, pc)
	if err != nil {
		return err.Error()
	}
	r.Who = st.Actor
	res, err := st.Run(ctx, store.FriendSyncStateStep(r))
	if err != nil {
		return err.Error()
	}
	if len(res.Refused) > 0 {
		return res.Refused[0].Why
	}
	return ""
}

// friendSyncActor is c as a pass of the loop acts: c itself when it names an actor,
// else c acting as the sprint's coordinator seat as the store says it now; why is
// the reason it cannot, "" when it can.
func (a *app) friendSyncActor(ctx context.Context, c common) (common, string) {
	if c.actor != "" {
		return c, ""
	}
	read := c
	read.verb = "where" // a read: no actor wanted to read the seat
	st, err := a.storeCtx(ctx, read)
	if err != nil {
		return c, err.Error()
	}
	seat, err := st.B.Coordinator(ctx)
	if err != nil {
		return c, "the sprint's coordinator seat cannot be read: " + err.Error()
	}
	if seat == "" {
		return c, "the sprint has no coordinator seat to act as, and no --actor was given; run: nova-sprint init --coordinator <name>"
	}
	c.actor = seat
	return c, ""
}
