// status.go holds the status verb: its flags, its run and the helpers only it uses.

package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"

	"github.com/mas-bandwidth/nova-tools/internal/fuse"
	"github.com/mas-bandwidth/nova-tools/pkg/bounded"
	"github.com/mas-bandwidth/nova-tools/pkg/oneline"
	"github.com/mas-bandwidth/nova-tools/pkg/tool"
)

// maxRemedy is the second half of the one MORE line this binary prints. A cap with no
// remedy is censorship; a cap with one is an index.
const maxRemedy = "--max <n> raises the ceiling, --max 0 lists every quarantine"

// cmdStatus reports. It exits 0 whenever the box was readable, blown or not, because
// answering the question is the job, and 2 when it could not read, because then it did
// not answer at all. Never gate on the exit code of status; check is the gate.
func cmdStatus(rest []string, stdout, stderr io.Writer, inv invocation) int {
	var max int
	var asJSON bool
	var parseErr bytes.Buffer
	box, positional, ok, parsed, jsonRequested := parseBoxWith("status", rest, &parseErr, inv.getenv, func(fs *flag.FlagSet) {
		fs.IntVar(&max, "max", bounded.Default, "quarantine lines to list before one MORE line stands for the rest; 0 lists all")
		fs.BoolVar(&asJSON, "json", false, "write the standard JSON receipt")
	})
	asJSON = asJSON || jsonRequested
	if !parsed {
		return parseFailure("status", asJSON, parseErr.String(), stdout, stderr)
	}
	var problems []string
	if !ok {
		problems = append(problems, parsedRefusalReasons("status", parseErr.String())...)
		if !asJSON {
			_, _ = io.Copy(stderr, &parseErr)
		}
	}
	if len(positional) > 0 {
		problem := fmt.Sprintf("unexpected argument %q", positional[0])
		problems = append(problems, problem)
		if asJSON {
			ok = false
		} else {
			refuse(stderr, " status", problem)
			ok = false
		}
	}
	if max < 0 {
		problem := fmt.Sprintf("--max must be a line ceiling of zero or more (got %d); 0 lists them all", max)
		problems = append(problems, problem)
		if asJSON {
			ok = false
		} else {
			fmt.Fprintf(stderr, "nova-fuse status REFUSED: %s; run: nova-fuse help\n", oneline.Escape(problem))
			ok = false
		}
	}
	if !ok {
		if asJSON {
			return writeFuseJSON(stdout, fuseRefusals("status", problems, "nova-fuse help status"))
		}
		return 2
	}

	b, err := fuse.ReadBox(boxFile(inv.wd, box))
	if err != nil {
		if asJSON {
			out := fuseRefusal("status", oneline.Err(err), remedy(err, box))
			out.Fact("lockdown", "unknown")
			out.Reasons = []string{fuseBoxReasonCode(err)}
			return writeFuseJSON(stdout, out)
		}
		fmt.Fprintf(stderr, "nova-fuse status REFUSED: %s -- a box that cannot be read is treated as BLOWN, never as clear; %s; run: nova-fuse help\n", oneline.Err(err), oneline.Escape(remedy(err, box)))
		return 2
	}

	names := b.Surfaces()
	if asJSON {
		out := tool.Done().Fact("lockdown", fuseState(b.Lockdown != nil)).Fact("quarantines", len(names))
		out.Verb = "status"
		if b.Lockdown != nil {
			out.Item("lockdown", "at", b.Lockdown.At, "reason", tool.Text(b.Lockdown.Reason))
		}
		for _, n := range names {
			f := b.Quarantine[n]
			out.Item("quarantine", "surface", n, "at", f.At, "reason", tool.Text(f.Reason))
		}
		out.Cap(max)
		return writeFuseJSON(stdout, out)
	}
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
