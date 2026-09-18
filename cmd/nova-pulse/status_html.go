package main

import "github.com/mas-bandwidth/nova-tools/internal/pulse"

// The three seams `status --html` reaches the world through. Production leaves each nil and
// internal/pulse uses its own: the benches read over ssh (ps/pgrep liveness, never log
// age), the host read with ps and df, the page shipped over ssh. A test replaces them, so
// no test starts ssh, reads this machine's process table, or ships a file anywhere.
var (
	statusHTMLReader     pulse.FleetReader
	statusHTMLSelfReader pulse.SelfReader
	statusHTMLPublisher  pulse.Publisher
)

// repeatable is a flag that may be given more than once, kept in the order given. --loop is
// the only one here: a fleet runs as many named loops as it runs, and the tool never knows
// their names in advance.
type repeatable []string

func (r *repeatable) String() string { return "" }

func (r *repeatable) Set(v string) error {
	*r = append(*r, v)
	return nil
}
