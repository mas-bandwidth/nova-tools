package main

import (
	"flag"
	"fmt"
	"io"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
)

// A caller supplies the epoch it observed. No retry silently enters a newer one.
func writeFlags(fs *flag.FlagSet) (*ntable.WriteOptions, *bool) {
	opts := &ntable.WriteOptions{Receipt: &ntable.Receipt{}}
	fs.Uint64Var(&opts.Epoch, "epoch", 0, "the epoch this write observed (default 0)")
	fs.StringVar(&opts.Actor, "actor", "", "actor recorded with the change")
	fs.StringVar(&opts.Fence, "fence", "", "coordinator fence recorded with the change")
	fs.StringVar(&opts.Idem, "idem", "", "attempt identifier recorded with the change; does not deduplicate")
	return opts, fs.Bool("receipt", false, "print the committed event ID, epoch and revision")
}

func printReceipt(out io.Writer, opts *ntable.WriteOptions, enabled bool) {
	if enabled {
		r := opts.Receipt
		fmt.Fprintf(out, "TABLE RECEIPT event=%s epoch=%d before=%d after=%d outcome=%s\n", r.ID, r.Epoch, r.Before, r.After, r.Outcome)
	}
}
