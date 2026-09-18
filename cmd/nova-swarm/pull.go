// pull takes one card from the bench queue, but only under a slot lease
// (docs/SPEC-JOBS.md section 3, "Pull workers with leases and heartbeats"). The
// worker pulls; no dispatcher tells it to start. A card is taken by rename into
// taken/, the lease it runs under is labelled with the card, and a dead worker's
// lease past until= returns its card to queue/ on the next pull.
//
// With a bench slot, pull also prefers the card whose repo the bench already holds
// in a kept worktree under <slot>/worktrees/<owner>/<name>, so the clone is reused
// and cache warmth is kept (docs/SPEC-JOBS.md section 4). Every path comes from a
// flag; there is no default slot, queue, store or mirror.
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// cmdPull is `nova-swarm pull`: reap, then take the next card under a lease. A
// pull that cannot take a lease refuses and starts nothing -- no launch without
// a lease. An empty queue is an idle pull, exit 0. Given a bench slot it takes
// the card warmth prefers and reports the kept worktree it lands in.
func cmdPull(args []string, stdout, stderr io.Writer, now time.Time) int {
	f := newFlags("pull")
	store := f.fs.String("store", "", "")
	owner := f.fs.String("owner", "", "")
	forDur := f.fs.String("for", "", "")
	slot := f.fs.String("slot", "", "")
	queue := f.fs.String("queue", "", "")
	mirror := f.fs.String("mirror", "", "")
	if !f.parse(args, stderr) {
		return 2
	}

	// A pull is either a card taken under a lease from the bench store (section 3)
	// or a card chosen for warmth from a bench slot's kept worktrees (section 4):
	// one set of flags, never both.
	lease := *store != "" || *owner != "" || *forDur != ""
	warm := *slot != "" || *queue != "" || *mirror != ""
	if lease && warm {
		f.add("pull takes a card either under a lease (--store --owner --for) or from a warm bench (--slot --queue --mirror); pass one set, not both")
		f.refused(stderr)
		return 2
	}
	if warm {
		f.want(*slot, "slot", "the bench slot whose kept worktrees live under <slot>/worktrees/<owner>/<name>")
		f.want(*queue, "queue", "the bench's queue directory of cards")
		f.want(*mirror, "mirror", "the bench mirror a cold clone fetches from")
		if f.refused(stderr) {
			return 2
		}

		cards, err := swarm.ReadCardDir(*queue)
		if err != nil {
			return refusePull(stderr, oneline.Err(err))
		}
		got, err := swarm.Prefer([]swarm.PullBench{{Name: "local", Slot: *slot, Mirror: *mirror, Queue: cards}})
		if err != nil {
			return refusePull(stderr, oneline.Err(err))
		}
		fmt.Fprintf(stdout, "PULL OK card=%s kind=%s repo=%s warm=%t worktree=%s",
			oneline.Field(got.Card.Name), oneline.Field(got.Card.Kind), oneline.Field(got.Card.Repo),
			got.Warm, oneline.Field(got.Path))
		if !got.Warm {
			fmt.Fprintf(stdout, " fetch=%s", oneline.Field(got.Fetch))
		}
		fmt.Fprintln(stdout)
		return 0
	}

	f.want(*store, "store", "the bench store holding shares.tsv, queue/ and slots/")
	f.want(*owner, "owner", "whose share the lease counts against")
	dur, err := time.ParseDuration(*forDur)
	if err != nil || dur <= 0 {
		f.add(fmt.Sprintf("--for wants a positive duration such as 30m, got %q", *forDur))
	}
	if f.refused(stderr) {
		return 2
	}

	res, err := swarm.PullCard(*store, *owner, dur, now, os.Getpid())
	switch {
	case errors.Is(err, swarm.ErrNoCard):
		fmt.Fprintf(stdout, "PULL IDLE owner=%s cards=0\n", oneline.Field(*owner))
		return 0
	case err != nil:
		var ref *swarm.LeaseRefusal
		if errors.As(err, &ref) {
			return refuse(stderr, " pull", fmt.Sprintf(
				"no lease for owner=%s card=%s held=%d share=%d free=%d holders=%s; a launch without a lease is refused",
				oneline.Field(*owner), oneline.Field(ref.Card), ref.Held, ref.Share, ref.Free, oneline.Escape(ref.Holders)))
		}
		return refuse(stderr, " pull", oneline.Err(err))
	}
	fmt.Fprintf(stdout, "PULL OK owner=%s card=%s lease=%s until=%s\n",
		oneline.Field(res.Owner), oneline.Field(res.Card), oneline.Field(res.Lease),
		oneline.Field(res.Until.UTC().Format(time.RFC3339)))
	return 0
}

// refusePull writes the one line a pull that could not run owes its caller.
func refusePull(w io.Writer, reason string) int {
	fmt.Fprintf(w, "PULL REFUSED: %s\n", oneline.Escape(reason))
	return 2
}
