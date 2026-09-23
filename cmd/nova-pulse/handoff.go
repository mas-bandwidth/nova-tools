package main

// The handoff and takeover verbs: the duty shift ends by handoff and begins
// again by takeover (SPEC-PULSE, Handoff). Handoff releases queue/OWNER,
// writes the HANDOFF record and posts one bus note to the successor; takeover
// refuses a live owner and takes a stale lock with one NOTE line.

import (
	"io"

	"github.com/mas-bandwidth/nova-tools/internal/bounded"
	"github.com/mas-bandwidth/nova-tools/internal/pulse"
)

func cmdHandoff(args []string, stdout, stderr io.Writer) int {
	f := newFlags("handoff")
	queue := f.fs.String("queue", "", "")
	to := f.fs.String("to", "", "")
	bus := f.fs.String("bus", "", "")
	roots := f.fs.String("roots", "", "")
	as := f.fs.String("as", "", "")
	work := f.fs.String("work", "", "")
	max := f.fs.Int("max", bounded.Default, "")
	if !f.parse(args, stderr) {
		return 2
	}
	f.want(*queue, "queue", "the queue directory holding OWNER, pending, launched and the logs")
	f.want(*to, "to", "the successor the shift goes to")
	f.want(*bus, "bus", "the nova-bus clone carrying the one note to the successor")
	if *max < 0 {
		f.add("--max is 0 or more; 0 already means all")
	}
	if f.refused(stderr) {
		return 2
	}
	return pulse.Handoff(pulse.HandoffInput{
		Queue: *queue, To: *to, Bus: *bus, Roots: *roots, As: *as,
		Work: *work, Max: *max, Stdout: stdout, Stderr: stderr,
	})
}

func cmdTakeover(args []string, stdout, stderr io.Writer) int {
	f := newFlags("takeover")
	queue := f.fs.String("queue", "", "")
	as := f.fs.String("as", "", "")
	bus := f.fs.String("bus", "", "")
	roots := f.fs.String("roots", "", "")
	max := f.fs.Int("max", bounded.Default, "")
	if !f.parse(args, stderr) {
		return 2
	}
	f.want(*queue, "queue", "the queue directory holding OWNER and HANDOFF")
	f.want(*as, "as", "the name taking the shift")
	if *max < 0 {
		f.add("--max is 0 or more; 0 already means all")
	}
	if f.refused(stderr) {
		return 2
	}
	return pulse.Takeover(pulse.TakeoverInput{
		Queue: *queue, As: *as, Bus: *bus, Roots: *roots,
		Max: *max, Stdout: stdout, Stderr: stderr,
	})
}
