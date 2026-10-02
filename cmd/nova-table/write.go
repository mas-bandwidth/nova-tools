package main

import (
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
)

// A caller supplies the epoch it observed. No retry silently enters a newer one.
func (app *application) writeFlags(fs *flag.FlagSet) (*ntable.WriteOptions, *bool) {
	opts := &ntable.WriteOptions{Receipt: &ntable.Receipt{}}
	fs.Uint64Var(&opts.Epoch, "epoch", app.defaults.Epoch, "the epoch this write observed (default 0)")
	fs.StringVar(&opts.Actor, "actor", app.defaults.Actor, "actor recorded with the change")
	fs.StringVar(&opts.Fence, "fence", app.defaults.Fence, "coordinator fence recorded with the change")
	fs.StringVar(&opts.Idem, "idem", app.defaults.Idem, "attempt identifier recorded with the change; does not deduplicate")
	dryRunFlag(fs, app.dryRun)
	return opts, fs.Bool("receipt", app.receipts, "print the committed event ID, epoch and revision")
}

// dryRunFlag declares --dry-run on a verb that writes (planned answers it).
func dryRunFlag(fs *flag.FlagSet, value bool) {
	fs.Bool("dry-run", value, "check the arguments, print the call the verb would send and stop: nothing is dialled or written")
}

// planned answers --dry-run on a verb that writes, at the point the real run
// would dial: the arguments have been checked as the real run checks them, so
// one line names the call (the verb, its arguments, the flags given) and the
// store it would go to, and nothing is dialled or written. What only the store
// can check (the table, its epoch, its rows and columns, a bound cell) is left
// to the real run. It reports whether it answered, and the exit: 0, or 1 when
// the line did not reach stdout (a closed pipe), as a verb's own output does.
func (app *application) planned(stdout, stderr io.Writer, fs *flag.FlagSet, verb, addr string, args []string) (int, bool) {
	if code, refused := app.overridden(stderr, fs, verb); refused {
		return code, true
	}
	if f := fs.Lookup("dry-run"); f == nil || f.Value.String() != "true" {
		return 0, false
	}
	var b strings.Builder
	b.WriteString("TABLE DRY-RUN verb=" + strings.Join(strings.Fields(verb), "-"))
	for i, a := range args {
		fmt.Fprintf(&b, " arg%d=%s", i+1, field(a))
	}
	fs.Visit(func(f *flag.Flag) {
		if f.Name != "dry-run" && f.Name != "redis" {
			fmt.Fprintf(&b, " %s=%s", f.Name, field(f.Value.String()))
		}
	})
	if strings.TrimSpace(addr) == "" {
		addr = "-"
	}
	fmt.Fprintf(&b, " redis=%s dialled=0 written=0\n", field(addr))
	if _, err := io.WriteString(stdout, b.String()); err != nil {
		return 1, true
	}
	return 0, true
}

// overridden refuses a write line that turns the dry run off inside a shell
// entered with --dry-run. A dry shell is dry for every line, whatever the
// line says: refusing --dry-run=false (rather than planning the line anyway
// with a note) is the answer that cannot surprise, because a reader who
// wrote --dry-run=false meant to write, and a plan printed in place of the
// write would read like a write that happened; the refusal says the line did
// nothing and how to write for real.
func (app *application) overridden(stderr io.Writer, fs *flag.FlagSet, verb string) (int, bool) {
	if !app.dryRun {
		return 0, false
	}
	off := false
	fs.Visit(func(f *flag.Flag) { off = off || (f.Name == "dry-run" && f.Value.String() != "true") })
	if !off {
		return 0, false
	}
	return refuse(stderr, verb, "this shell was entered with --dry-run, so every write line is planned and none is written; "+
		"--dry-run=false does not turn that off; to write, leave the shell (quit) and run the verb on its own; run: nova-table help shell"), true
}

func printReceipt(out io.Writer, opts *ntable.WriteOptions, enabled bool) {
	if enabled {
		r := opts.Receipt
		fmt.Fprintf(out, "TABLE RECEIPT event=%s epoch=%d before=%d after=%d outcome=%s\n", r.ID, r.Epoch, r.Before, r.After, r.Outcome)
	}
}
