// status.go holds the status verb: its flags, its run and the helpers only it uses.

package main

import (
	"flag"
	"fmt"
	"io"

	"github.com/mas-bandwidth/nova-tools/internal/fuse"
	"github.com/mas-bandwidth/nova-tools/pkg/bounded"
	"github.com/mas-bandwidth/nova-tools/pkg/oneline"
)

// maxRemedy is the second half of the one MORE line this binary prints. A cap with no
// remedy is censorship; a cap with one is an index.
const maxRemedy = "--max <n> raises the ceiling, --max 0 lists every quarantine"

// cmdStatus reports. It exits 0 whenever the box was readable, blown or not, because
// answering the question is the job, and 2 when it could not read, because then it did
// not answer at all. Never gate on the exit code of status; check is the gate.
func cmdStatus(rest []string, stdout, stderr io.Writer, inv invocation) int {
	var max int
	box, positional, ok, parsed := parseBoxWith("status", rest, stderr, inv.getenv, func(fs *flag.FlagSet) {
		fs.IntVar(&max, "max", bounded.Default, "quarantine lines to list before one MORE line stands for the rest; 0 lists all")
	})
	if !parsed {
		return 2
	}
	if len(positional) > 0 {
		refuse(stderr, " status", fmt.Sprintf("unexpected argument %q", positional[0]))
		ok = false
	}
	if max < 0 {
		// Zero already means "all", so a negative ceiling is a typo with two readings.
		fmt.Fprintf(stderr, "nova-fuse status REFUSED: --max must be a line ceiling of zero or more (got %d); 0 lists them all; run: nova-fuse help\n", max)
		ok = false
	}
	if !ok {
		return 2
	}

	b, err := fuse.ReadBox(boxFile(inv.wd, box))
	if err != nil {
		fmt.Fprintf(stderr, "nova-fuse status REFUSED: %s -- a box that cannot be read is treated as BLOWN, never as clear; %s; run: nova-fuse help\n", oneline.Err(err), oneline.Escape(remedy(err, box)))
		return 2
	}

	names := b.Surfaces()
	if b.Lockdown != nil {
		fmt.Fprintf(stdout, "STATUS OK lockdown=blown since=%s quarantines=%d: %s\n",
			since(*b.Lockdown), len(names), why(*b.Lockdown))
	} else {
		fmt.Fprintf(stdout, "STATUS OK lockdown=clear quarantines=%d\n", len(names))
	}
	// The count is never capped and the listing always is. quarantines= above is the truth
	// about the box; the lines below are a sample of it in the box's own order, and the
	// MORE line says how big the sample was, on a verb whose job is to be glanced at.
	list := bounded.Capped(stdout, max, "STATUS", "quarantine", maxRemedy)
	for _, n := range names {
		f := b.Quarantine[n]
		list.Line(fmt.Sprintf("STATUS OK quarantine=%s since=%s: %s", oneline.Field(n), since(f), why(f)))
	}
	list.More()
	return 0
}
