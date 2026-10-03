// names: the roster echoed, so a To line can be spelled the way the tool accepts it.

package main

import (
	"fmt"
	"io"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

func cmdNames(args []string, stdout, stderr io.Writer) int {
	f := newFlags("names")
	busDir := f.fs.String("bus", "", "the bus's repository root (required)")
	if !f.parse(args, stderr, map[string]*string{"bus": busDir}) {
		return 2
	}
	c, err := bus.LoadConfig(*busDir)
	if err != nil {
		fmt.Fprintf(stderr, "NAMES REFUSED: %s\n", oneline.WithRemedy(oneline.Err(err), "nova-bus names -h"))
		return 2
	}
	// THE NAMES ARE QUOTED, NOT FIELD-ESCAPED, and this verb exists for exactly the reason
	// that matters. `names` is what a person runs to find out how to spell a To line this
	// tool will accept -- and under oneline.Field, which escapes every blank so that a
	// key=value field is one token, "Ada Vale" printed as `Ada\x20Claude`. Paste that
	// into a To line and `send` refuses it. The verb whose whole job is to tell you the
	// spelling was telling you one the tool does not take.
	//
	// oneline.Quote keeps the one-line guarantee by another route (see its comment): the
	// quotes delimit the value, so a blank inside one is not the end of a field, and every
	// character that could break or reorder a line is still escaped. A list is each name
	// quoted and joined by the ";" a To line separates on, so `aliases="Ada Vale";"the
	// keeper"` is two names a person can lift straight out. The lane is a slug and stays a
	// field: it holds no blank by construction and is not something anybody pastes.
	for _, p := range c.Participants {
		lane := p.Lane
		if lane == "" {
			lane = "-"
		}
		fmt.Fprintf(stdout, "NAMES NAME name=%s lane=%s aliases=%s\n",
			oneline.Quote(p.Name), oneline.Field(lane), quoteList(p.Aliases))
	}
	for _, g := range c.Groups {
		fmt.Fprintf(stdout, "NAMES GROUP name=%s members=%s\n", oneline.Quote(g.Name), quoteList(g.Members))
	}
	fmt.Fprintf(stdout, "NAMES OK participants=%d groups=%d senders=%d\n", len(c.Participants), len(c.Groups), len(c.Senders()))
	return 0
}
