package update

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// watchAdoptNamed runs the coordinator's own adoption pass for the two-column
// checks file, a mechanical step of the upgrade cycle (rule 27). Each check the
// file names runs bounded by --timeout; an exit 0 with an answer line is ADOPT
// OK, everything else ADOPT REFUSED naming its remedy, and every REFUSED is
// handed to the duty tier as one ADOPT ESCALATE line. The receipt -- the OK and
// REFUSED lines with ADOPT DONE last -- is printed, and under --send posted to
// the bus as the coordinator's own through rule 24's prepared delivery.
func watchAdoptNamed(checks []check, o options, out, errs io.Writer, env Environment) int {
	started := env.Now()
	ctx, cancel := context.WithTimeout(context.Background(), o.budget)
	defer cancel()
	at := started.UTC().Format(time.RFC3339)
	var receipt bytes.Buffer
	fmt.Fprintf(&receipt, "ADOPT at=%s rebuild=%s file=%s checks=%d timeout=%s budget=%s\n", field(at), field(o.rebuild), field(o.adopt), len(checks), o.timeout, o.budget)
	ok, refused := 0, 0
	for _, c := range checks {
		child, cancel := context.WithTimeout(ctx, o.timeout)
		p := process(child, c.Argv, nil, ChildCap)
		cancel()
		answer := firstLine(p.Stdout)
		detail := answer
		if detail == "" {
			detail = p.Reason
		}
		if detail == "" {
			detail = "no answer"
		}
		if p.Reason == "" && answer != "" {
			ok++
			fmt.Fprintf(&receipt, "ADOPT OK %s %s\n", field(c.Name), oneline.Escape(detail))
		} else {
			refused++
			remedy := "run " + strings.Join(c.Argv, " ") + " and read its answer"
			fmt.Fprintf(&receipt, "ADOPT REFUSED %s %s (%s)\n", field(c.Name), oneline.Escape(detail), oneline.Escape(remedy))
			fmt.Fprintf(errs, "ADOPT ESCALATE %s adopt %s: %s\n", field(at), field(c.Name), oneline.Escape(detail))
		}
	}
	fmt.Fprintf(&receipt, "ADOPT DONE sha=%s ok=%d refused=%d took=%s\n", field(o.rebuild), ok, refused, time.Since(started).Round(time.Millisecond))
	code := 0
	if refused > 0 {
		code = 1
	}
	var body bytes.Buffer
	fmt.Fprintf(&body, "From: %s\nTo: %s\nSubject: adoption at %s\n\n", o.as, o.to, at)
	body.Write(receipt.Bytes())
	if o.draft {
		out.Write(body.Bytes())
		return code
	}
	out.Write(receipt.Bytes())
	if o.send {
		if _, err := deliver(ctx, o, emptySnapshot(), nil, body.Bytes(), out, env, "ADOPT"); err != nil {
			fmt.Fprintf(errs, "ADOPT NOTE %s\n", oneline.Err(err))
			code = 1
		}
	}
	return code
}
