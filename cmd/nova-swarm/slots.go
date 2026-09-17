// slots are bench-wide leases with shares, reserve, expiry and live-pid
// fencing (docs/SPEC-SWARM.md, "Bench slot leases"): `slots take` grants,
// `slots release` frees, `slots list` prints one line per lease.
package main

import (
	"fmt"
	"io"
	"os"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// cmdSlots dispatches the slots verb's subcommands: take, release, list.
func cmdSlots(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return refuse(stderr, " slots", "wants a subcommand: take (grant leases), release (free them) or list (print them)")
	}
	switch args[0] {
	case "take":
		return cmdSlotsTake(args[1:], stdout, stderr)
	case "release":
		return cmdSlotsRelease(args[1:], stdout, stderr)
	case "list":
		return cmdSlotsList(args[1:], stdout, stderr)
	}
	return refuse(stderr, " slots", fmt.Sprintf("unknown subcommand %q", args[0]))
}

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
	granted, held, share, free, holders, ok, terr := swarm.TakeSlotLeases(
		*store, *owner, *n, dur, *label, time.Now().UTC(), os.Getpid())
	if terr != nil {
		fmt.Fprintf(stderr, "nova-swarm slots take: %s\n", oneline.Err(terr))
		return 2
	}
	if ok {
		fmt.Fprintf(stdout, "SLOTS OK owner=%s granted=%d held=%d share=%d free=%d\n",
			oneline.Field(*owner), granted, held, share, free)
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
