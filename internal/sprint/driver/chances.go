package driver

import (
	"fmt"
	"math"
	"strings"
	"time"
)

// Chances are the six chances the seeded facts draw against, each a
// probability from 0 to 1. Broken and Fail are per report, drawn each time a
// card is reported, so a card reworked and reported again draws again (a
// reader finds the work broken; work comes back not ok from the fleet), Stuck
// and Cross are per merge batch (a merge needs help from the coordinator
// within its stream; across streams), Down and Up are per member and second
// (a machine that is up goes down; one that is down comes back).
type Chances struct {
	Broken, Fail, Stuck, Cross, Down, Up float64
}

// Chance is one row of the table of chances: the flag that sets it, its value
// when a run is not a simulation, its locked value under --simulation, what
// the flag says, and where the chance is kept.
type Chance struct {
	Flag   string
	Plain  float64
	Locked float64
	Usage  string
	At     func(*Chances) *float64
}

// ChanceRows are the six chances, one row each, in the order the simulation
// names them. The locked column is the owner's: a change to the simulation is
// a change to a row here, and the flags, the help and the tests follow it.
var ChanceRows = []Chance{
	{"broken", 0.05, 0.10, "the chance a read finds the work broken", func(c *Chances) *float64 { return &c.Broken }},
	{"fail", 0.10, 0.10, "the chance a work card comes back failed", func(c *Chances) *float64 { return &c.Fail }},
	{"stuck", 0.10, 0.10, "the chance a batch has a card that does not merge", func(c *Chances) *float64 { return &c.Stuck }},
	{"cross", 0.01, 0.01, "the chance a batch has a card that needs a card of another stream first", func(c *Chances) *float64 { return &c.Cross }},
	{"down", 0, 0.01, "the chance, per member and second, that a member's machine that is up goes down (stops beating)", func(c *Chances) *float64 { return &c.Down }},
	{"up", 0, 0.10, "the chance, per member and second, that a member's machine that is down comes back (beats again)", func(c *Chances) *float64 { return &c.Up }},
}

// Aliases are flags that set several chances at once, unless the chance is
// named itself: --flap is --down and --up with the one chance.
var Aliases = map[string][]string{"flap": {"down", "up"}}

// Set is the chances of a run: each row at its plain value, or at its locked
// value when simulating, then each flag the person gave, whatever it is (a
// chance of 0 given is 0). given holds the chances named on the command line
// by flag name; a chance outside 0 to 1 is refused, naming its flag.
func Set(simulation bool, given map[string]float64) (Chances, error) {
	named := map[string]float64{}
	for k, v := range given {
		named[k] = v
	}
	for alias, flags := range Aliases {
		v, ok := given[alias]
		if !ok {
			continue
		}
		if err := Valid(alias, v); err != nil {
			return Chances{}, err
		}
		for _, f := range flags {
			if _, set := named[f]; !set {
				named[f] = v
			}
		}
	}
	var c Chances
	for _, r := range ChanceRows {
		v := r.Plain
		if simulation {
			v = r.Locked
		}
		if g, ok := named[r.Flag]; ok {
			v = g
		}
		if err := Valid(r.Flag, v); err != nil {
			return Chances{}, err
		}
		*r.At(&c) = v
	}
	return c, nil
}

// Valid is nil for a chance from 0 to 1, and a refusal naming the flag for
// anything else (a number outside it, or not a number).
func Valid(flag string, p float64) error {
	if p >= 0 && p <= 1 {
		return nil
	}
	return fmt.Errorf("--%s wants a chance from 0 to 1, found %v", flag, p)
}

// String is the chances as flags, in the order of the table, each to six
// significant digits (a chance made from a second's chance by PerTick does not
// print its last digits of rounding).
func (c Chances) String() string {
	var out []string
	for _, r := range ChanceRows {
		out = append(out, fmt.Sprintf("%s=%.6g", r.Flag, *r.At(&c)))
	}
	return strings.Join(out, " ")
}

// PerTick is the chance a tick of every draws against, given a chance p each
// second: the chance of at least one such event in that much time, and p
// itself at one second, and for a tick of no time. The length of a tick is the
// configured every, not the time that passes between two draws.
func PerTick(p float64, every time.Duration) float64 {
	if every <= 0 || every == time.Second {
		return p
	}
	return 1 - math.Pow(1-p, every.Seconds())
}
