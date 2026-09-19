// slots are bench-wide leases with shares, reserve, expiry and live-pid
// fencing (docs/SPEC-SWARM.md, "Bench slot leases"): `slots init` makes a store,
// `slots take` grants, `slots release` frees, `slots list` prints one line per
// lease.
package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// cmdSlots dispatches the slots verb's subcommands: init, take, release, list.
func cmdSlots(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return refuse(stderr, " slots", "wants a subcommand: init (make a store), take (grant leases), release (free them) or list (print them)")
	}
	switch args[0] {
	case "init":
		return cmdSlotsInit(args[1:], stdout, stderr)
	case "take":
		return cmdSlotsTake(args[1:], stdout, stderr)
	case "release":
		return cmdSlotsRelease(args[1:], stdout, stderr)
	case "list":
		return cmdSlotsList(args[1:], stdout, stderr)
	}
	return refuse(stderr, " slots", fmt.Sprintf("unknown subcommand %q", args[0]))
}

// cmdSlotsInit makes a bench slot store: the directory, its `slots/` subdirectory and one
// shares.tsv holding a capacity, a reserve of 0 and one owner's share. It exists because
// `native` now REFUSES to launch without a store (nova-tools#1546), and a refusal whose
// remedy is "pass --slots-store <dir>" is no remedy at all on a bench that has never had
// one: the operator would be left to discover the file format by reading loadSlotShares.
//
// It creates and never updates. A store that already has a shares.tsv is refused, not
// rewritten, because the leases under it belong to other processes that are running right
// now and a capacity edited underneath them is a bench that overcommits silently. Changing
// a live store is a hand edit of shares.tsv, which is a two-column TSV a person can read.
func cmdSlotsInit(args []string, stdout, stderr io.Writer) int {
	f := newFlags("slots init")
	store := f.fs.String("store", "", "")
	owner := f.fs.String("owner", "", "")
	capacity := f.fs.Int("capacity", 0, "")
	share := f.fs.Int("share", 0, "")
	if !f.parse(args, stderr) {
		return 2
	}
	f.want(*store, "store", "the directory to create: it will hold shares.tsv and slots/")
	f.want(*owner, "owner", "the one owner this store starts with; further owners are rows added to shares.tsv by hand")
	f.wantCount(*capacity, "capacity", "how many leases this bench grants at once, at least 1")
	f.wantCount(*share, "share", "how many of those leases this owner may hold at once, at least 1")
	// `capacity` and `reserve` are the two RESERVED keys of shares.tsv, so an owner
	// spelled as either would be read back as a number and the owner would vanish.
	// A tab or a newline in an owner would split or end the row it is written on.
	if *owner == "capacity" || *owner == "reserve" {
		f.add(fmt.Sprintf("--owner %s is a reserved shares.tsv key; pick another name, or this owner's row would be read back as the bench's own capacity or reserve", oneline.Field(*owner)))
	}
	// The owner is written into shares.tsv and printed back on the OK line, so it must
	// survive both unchanged: a tab would split its own row, a newline would end it, an
	// `=` or a space would come back escaped and no longer be the name that was asked for.
	if *owner != "" && oneline.Field(*owner) != *owner {
		f.add("--owner wants a plain one-word name: shares.tsv is a two-column tab-separated file, and a space, a tab, a newline or an `=` in an owner would split its row or be read back as something else")
	}
	// A share above the capacity can never be met, and the store would refuse every
	// take with numbers that look like a bug in the tool rather than in the store.
	if *capacity > 0 && *share > *capacity {
		f.add(fmt.Sprintf("--share %d is more than --capacity %d: a share is this owner's slice of the bench, so it cannot be wider than the bench", *share, *capacity))
	}
	if f.refused(stderr) {
		return 2
	}
	path := filepath.Join(*store, "shares.tsv")
	if err := os.MkdirAll(slotsSubdir(*store), 0o755); err != nil {
		fmt.Fprintf(stderr, "nova-swarm slots init: %s\n", oneline.Err(err))
		return 2
	}
	// O_EXCL, not a Stat first: two inits racing on the same path must not both believe
	// they created the store, and the kernel is the only thing that can settle that.
	fh, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		if os.IsExist(err) {
			fmt.Fprintf(stderr, "SLOTS REFUSED reason=store_exists store=%s: %s is already there and init never overwrites a store, because the leases under it are other processes'; edit shares.tsv by hand to change a capacity, a reserve or a share\n",
				oneline.Field(*store), oneline.Field(path))
			return 2
		}
		fmt.Fprintf(stderr, "nova-swarm slots init: %s\n", oneline.Err(err))
		return 2
	}
	// reserve is 0: a reserve is a bench-wide holdback somebody decides on later, and a
	// store that invented one would be quietly narrower than the capacity it prints.
	if _, werr := fmt.Fprintf(fh, "capacity\t%d\nreserve\t0\n%s\t%d\n", *capacity, oneline.Field(*owner), *share); werr != nil {
		fh.Close()
		fmt.Fprintf(stderr, "nova-swarm slots init: %s\n", oneline.Err(werr))
		return 2
	}
	if cerr := fh.Close(); cerr != nil {
		fmt.Fprintf(stderr, "nova-swarm slots init: %s\n", oneline.Err(cerr))
		return 2
	}
	fmt.Fprintf(stdout, "SLOTS INIT OK store=%s owner=%s capacity=%d reserve=%d share=%d\n",
		oneline.Field(*store), oneline.Field(*owner), *capacity, 0, *share)
	return 0
}

// slotsSubdir is the leases directory inside a store. internal/swarm makes it on the first
// lease; init makes it up front so that a freshly initialised store LOOKS like a store to
// the person who just ran the command and goes to see what it made.
func slotsSubdir(store string) string { return filepath.Join(store, "slots") }

func cmdSlotsTake(args []string, stdout, stderr io.Writer) int {
	f := newFlags("slots take")
	store := f.fs.String("store", "", "")
	owner := f.fs.String("owner", "", "")
	n := f.fs.Int("n", 0, "")
	forDur := f.fs.String("for", "", "")
	label := f.fs.String("label", "", "")
	if !f.parse(args, stderr) {
		return 2
	}
	f.want(*store, "store", "the directory holding shares.tsv and slots/")
	f.want(*owner, "owner", "whose share the leases count against")
	f.wantCount(*n, "n", "how many leases to grant, at least 1")
	dur, err := time.ParseDuration(*forDur)
	if err != nil || dur <= 0 {
		f.add(fmt.Sprintf("--for wants a positive duration such as 30m, got %q", *forDur))
	}
	if f.refused(stderr) {
		return 2
	}
	ids, held, share, free, holders, ok, terr := swarm.TakeSlotLeases(
		*store, *owner, *n, dur, *label, time.Now().UTC(), os.Getpid())
	if terr != nil {
		fmt.Fprintf(stderr, "nova-swarm slots take: %s\n", oneline.Err(terr))
		return 2
	}
	if ok {
		fmt.Fprintf(stdout, "SLOTS OK owner=%s granted=%d held=%d share=%d free=%d\n",
			oneline.Field(*owner), len(ids), held, share, free)
		return 0
	}
	if holders == "" {
		holders = "-"
	}
	fmt.Fprintf(stderr, "SLOTS REFUSED owner=%s want=%d held=%d share=%d free=%d holders=%s\n",
		oneline.Field(*owner), *n, held, share, free, oneline.Escape(holders))
	return 2
}

func cmdSlotsRelease(args []string, stdout, stderr io.Writer) int {
	f := newFlags("slots release")
	store := f.fs.String("store", "", "")
	owner := f.fs.String("owner", "", "")
	label := f.fs.String("label", "", "")
	all := f.fs.Bool("all", false, "")
	if !f.parse(args, stderr) {
		return 2
	}
	f.want(*store, "store", "the directory holding shares.tsv and slots/")
	f.want(*owner, "owner", "whose leases are freed")
	if *all && *label != "" {
		f.add("--label and --all together: one frees the leases carrying a label and the other frees them all; pass at most one")
	}
	if !*all && *label == "" {
		f.add("--label or --all is required; it wants which of this owner's leases to free: the ones carrying a label, or all of them")
	}
	if f.refused(stderr) {
		return 2
	}
	released, held, err := swarm.ReleaseSlotLeases(*store, *owner, *label, *all)
	if err != nil {
		fmt.Fprintf(stderr, "nova-swarm slots release: %s\n", oneline.Err(err))
		return 2
	}
	fmt.Fprintf(stdout, "SLOTS RELEASED owner=%s released=%d held=%d\n",
		oneline.Field(*owner), released, held)
	return 0
}

func cmdSlotsList(args []string, stdout, stderr io.Writer) int {
	f := newFlags("slots list")
	store := f.fs.String("store", "", "")
	if !f.parse(args, stderr) {
		return 2
	}
	f.want(*store, "store", "the directory holding shares.tsv and slots/")
	if f.refused(stderr) {
		return 2
	}
	now := time.Now().UTC()
	leases, err := swarm.ListSlotLeases(*store, now)
	if err != nil {
		fmt.Fprintf(stderr, "nova-swarm slots list: %s\n", oneline.Err(err))
		return 2
	}
	for _, l := range leases {
		fmt.Fprintf(stdout, "SLOT %s owner=%s pid=%d label=%s until=%s state=%s\n",
			oneline.Field(l.ID), oneline.Field(l.Owner), l.Pid,
			oneline.Field(dash(l.Label)), oneline.Field(l.Until.UTC().Format(time.RFC3339)),
			oneline.Field(l.State(now)))
	}
	return 0
}
