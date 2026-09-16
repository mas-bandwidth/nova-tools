package pulse

// The loop's counters are ONE FILE beside the configuration. Pit stop 3, class A, bug 3:
// "the loop must be restarted to take any change and loses its counters (gt) each time".
// A counter in a running shell script is lost at every restart; a counter here survives one,
// and the next card number carries on rather than colliding with a launched card (bug 1,
// where hand-numbered cards 8130-8134 overwrote five launched cards of the same numbers).

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

// StateFile is the loop's counters, in the queue directory beside ConfigFile.
const StateFile = "pulse.state"

// FirstCard is the number a fresh queue's first card takes. It is 1 and not 0 so a card
// number is never the empty value of anything.
const FirstCard = 1

// State is what a restart must not lose: the next card number (the one cutter's counter),
// the tick count, the tick the last refill ran on, and how many times the gate has run.
type State struct {
	NextCard   int
	Tick       int
	RefillTick int
	Gate       int
}

// stateKeys is the file's shape, in the order it is written.
var stateKeys = []string{"next_card", "tick", "refill_tick", "gate"}

// LoadState reads <dir>/pulse.state. A file that is not there is a fresh queue: the first
// card number and zero counters. A file that is malformed is a REFUSAL and never a zero
// state -- a zeroed next_card reissues numbers that are live.
func LoadState(dir string) (State, error) {
	path := filepath.Join(dir, StateFile)
	kv, err := readKV(path)
	if err != nil {
		return State{}, fmt.Errorf("%s (%s is next_card=<n>, tick=<n>, refill_tick=<n>, gate=<n>)", err, StateFile)
	}
	s := State{NextCard: FirstCard}
	for k, v := range kv {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return State{}, fmt.Errorf("%s: %s wants a whole number, got %q (%s is next_card=<n>, tick=<n>, refill_tick=<n>, gate=<n>)", path, k, v, StateFile)
		}
		switch k {
		case "next_card":
			s.NextCard = n
		case "tick":
			s.Tick = n
		case "refill_tick":
			s.RefillTick = n
		case "gate":
			s.Gate = n
		default:
			return State{}, fmt.Errorf("%s: unknown counter %q; the loop never expands its state (%s is next_card=<n>, tick=<n>, refill_tick=<n>, gate=<n>)", path, k, StateFile)
		}
	}
	if s.NextCard < FirstCard {
		s.NextCard = FirstCard
	}
	return s, nil
}

// Save writes the counters whole, through a temp file and a rename, so a tick killed
// mid-write leaves the last good state rather than half of this one.
func (s State) Save(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return writeKV(filepath.Join(dir, StateFile), stateKeys, map[string]string{
		"next_card":   strconv.Itoa(s.NextCard),
		"tick":        strconv.Itoa(s.Tick),
		"refill_tick": strconv.Itoa(s.RefillTick),
		"gate":        strconv.Itoa(s.Gate),
	})
}
