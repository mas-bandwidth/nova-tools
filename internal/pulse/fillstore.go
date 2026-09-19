package pulse

// The store leads, the load only brakes (#1914).
//
// `fill`'s capacity used to be a load formula -- `cores*3/2 - load1 - cores/8`, the min of
// that and two headroom terms -- and a bench earns load precisely by running the cards it
// was dealt. Measured on 2026-09-19 at 21:00Z: space answered **-1**, clamped to zero and
// dealt nothing, with **52 free slots in its own store**; hulk answered 12 against a share
// of 40. The harder the fleet worked the less it was fed, which is the stall itself.
//
// The bench's own slot store is its answer to "how many more cards may I take": the owner's
// row in `<store>/shares.tsv` minus the live leases that owner holds, which is exactly what
// `nova-swarm native --slots-store --owner` enforces at launch. That number is the capacity.
// The load is a BRAKE on top of it: a bench over `--max-load-per-core` is dealt nothing this
// tick and says so by name, with the free count it did not fill on the line. A brake never
// shrinks a bench below the leases it already holds -- the live cards are running, and the
// one thing a capacity number may never do is ask for them back.
//
// The probe is a seam. The command runs one line of shell on the bench (over ssh, or on this
// machine for a local bench) and hands back what it printed; a test hands back the same line
// and opens nothing.

import (
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
)

// CapacityAnswer is one bench's capacity probe, parsed. FromStore says which of the two
// numbers the bench could give: its store's own free count, or the legacy load formula for a
// bench that has no store yet.
type CapacityAnswer struct {
	FromStore bool
	Share     int     // the owner's row in shares.tsv, when FromStore
	Held      int     // the live leases that owner holds, when FromStore
	Formula   int     // the legacy load formula's number, when not FromStore
	Cores     int     // the bench's cores, for the brake
	Load1     float64 // the bench's one-minute load, for the brake
}

// Free is the capacity: the store's free count under the owner's share, never below zero.
// An owner holding more leases than its share -- a share LOWERED under live work -- is a
// bench with nothing free, not a negative for some later clamp to catch.
func (a CapacityAnswer) Free() int {
	n := a.Formula
	if a.FromStore {
		n = a.Share - a.Held
	}
	if n < 0 {
		return 0
	}
	return n
}

// PerCore is the load the brake is read against; a bench answering no cores is not braked,
// because a brake on a number nobody measured is a guess.
func (a CapacityAnswer) PerCore() float64 {
	if a.Cores <= 0 {
		return 0
	}
	return a.Load1 / float64(a.Cores)
}

// Braked says whether the load guard holds this bench for this tick. A guard of zero or less
// is no guard at all: the Studio runs at 3.2 load per core with four leases held, because
// something other than its leases makes the load, and a guard there would idle a bench with
// 190 free slots.
func (a CapacityAnswer) Braked(maxPerCore float64) bool {
	if maxPerCore <= 0 || !a.FromStore {
		return false
	}
	return a.PerCore() > maxPerCore
}

// ParseCapacityAnswer reads the one line a capacity probe prints. Three shapes, and nothing
// else is guessed:
//
//	store share=<n> held=<n> cores=<n> load1=<f>
//	formula capacity=<n> cores=<n> load1=<f>
//	unreadable reason=<why> ...
//
// A line in none of those shapes is a refusal naming what the bench said. A guessed free
// count puts a card on a full machine.
//
// EVERY FIELD IS VALIDATED (Stella, #1945). A malformed reading used to be taken as a valid
// one: `held=-10` made a share of 64 answer 74; a `cores=` that would not parse became 0,
// which is a bench that can never be braked; and a `load1=NaN` compared false against every
// threshold, so the brake the caller asked for was silently off. Counts are whole numbers
// and never negative, the load is finite and never negative, cores and load are REQUIRED
// rather than optional, and a field said twice is a refusal -- a line nobody can read one
// way is not a line to act on.
func ParseCapacityAnswer(out string) (CapacityAnswer, error) {
	fields := strings.Fields(strings.TrimSpace(out))
	if len(fields) == 0 {
		return CapacityAnswer{}, fmt.Errorf("the capacity probe said nothing")
	}
	kind, rest := fields[0], fields[1:]
	seen := map[string]string{}
	for _, f := range rest {
		k, v, ok := strings.Cut(f, "=")
		if !ok {
			continue
		}
		if _, twice := seen[k]; twice {
			return CapacityAnswer{}, fmt.Errorf(
				"the capacity probe answered %q, which says %s= twice; refusing a reading nobody can read one way",
				oneLineAnswer(out), k)
		}
		seen[k] = v
	}
	a := CapacityAnswer{}
	// count reads a required whole number that may never be negative: a share, a lease
	// count and a capacity are all counts of things, and a negative count is a reading
	// that went wrong, not a smaller number.
	count := func(key string) (int, error) {
		raw, ok := seen[key]
		if !ok {
			return 0, fmt.Errorf("the capacity probe answered %q, with no %s=", oneLineAnswer(out), key)
		}
		n, err := strconv.Atoi(raw)
		if err != nil {
			return 0, fmt.Errorf("the capacity probe answered %s=%q, which is not a whole number", key, raw)
		}
		if n < 0 {
			return 0, fmt.Errorf("the capacity probe answered %s=%d, and a count is never negative", key, n)
		}
		return n, nil
	}
	var err error
	switch kind {
	case "unreadable":
		return CapacityAnswer{}, fmt.Errorf(
			"the bench could not read its slot store (%s); refusing a capacity nobody read, rather than calling a full bench empty",
			oneLineAnswer(strings.Join(rest, " ")))
	case "store":
		a.FromStore = true
		if a.Share, err = count("share"); err != nil {
			return CapacityAnswer{}, err
		}
		if a.Held, err = count("held"); err != nil {
			return CapacityAnswer{}, err
		}
	case "formula":
		if a.Formula, err = count("capacity"); err != nil {
			return CapacityAnswer{}, err
		}
	default:
		return CapacityAnswer{}, fmt.Errorf(
			"the capacity probe answered %q; wanted `store share=<n> held=<n> cores=<n> load1=<f>`, `formula capacity=<n> cores=<n> load1=<f>` or `unreadable reason=<why>`",
			oneLineAnswer(out))
	}
	// Cores and load are the brake's readings, and they are required: a bench that cannot
	// say what its load is cannot be braked, and running it unbraked is the brake turning
	// itself off. Whether that is fatal is the caller's -- a caller with the brake off
	// (--max-load-per-core 0) does not need them -- but they must at least be READABLE.
	if a.Cores, err = count("cores"); err != nil {
		return CapacityAnswer{}, err
	}
	raw, ok := seen["load1"]
	if !ok {
		return CapacityAnswer{}, fmt.Errorf("the capacity probe answered %q, with no load1=", oneLineAnswer(out))
	}
	load, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return CapacityAnswer{}, fmt.Errorf("the capacity probe answered load1=%q, which is not a number", raw)
	}
	if math.IsNaN(load) || math.IsInf(load, 0) {
		return CapacityAnswer{}, fmt.Errorf(
			"the capacity probe answered load1=%q, which is not a finite number; a load that compares false against every threshold is a brake that is silently off", raw)
	}
	if load < 0 {
		return CapacityAnswer{}, fmt.Errorf("the capacity probe answered load1=%q, and a load is never negative", raw)
	}
	a.Load1 = load
	return a, nil
}

// oneLineAnswer bounds what a refusal quotes back, so a bench that printed a megabyte of
// shell noise still costs one readable line.
func oneLineAnswer(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}

// StoreCapacity is the pulse.Capacity the fleet runs on: it asks the bench, parses the one
// line, brakes on load and answers the store's free count. Probe is the seam -- the command
// gives it an ssh (or this machine's own shell for a local bench) and a test gives it a
// canned line.
type StoreCapacity struct {
	Probe          func(bench string) (string, error)
	MaxLoadPerCore float64
	Stderr         io.Writer
}

func (c StoreCapacity) Capacity(bench string) (int, error) {
	if c.Probe == nil {
		return 0, fmt.Errorf("no capacity probe; refusing to guess a free count")
	}
	// The threshold is checked here as well as at the flag, because this type is the
	// library seam and a NaN threshold compares false against every bench: the brake would
	// be off and nothing would say so.
	if math.IsNaN(c.MaxLoadPerCore) || math.IsInf(c.MaxLoadPerCore, 0) || c.MaxLoadPerCore < 0 {
		return 0, fmt.Errorf(
			"the load brake is %v, which is not a finite number of load units per core, 0 or more; a threshold nothing can exceed is a brake that is silently off",
			c.MaxLoadPerCore)
	}
	out, err := c.Probe(bench)
	if err != nil {
		return 0, err
	}
	a, err := ParseCapacityAnswer(out)
	if err != nil {
		return 0, err
	}
	// The brake is ON and the bench could not count its cores: it cannot be braked at all,
	// and a bench that runs unbraked because its own reading failed is exactly the silence
	// this guard exists to break.
	if c.MaxLoadPerCore > 0 && a.Cores <= 0 {
		return 0, fmt.Errorf(
			"the load brake is on (%.2f per core) and the bench answered cores=%d; refusing to fill a bench nobody can brake (measure its cores, or say --max-load-per-core 0 to run it unbraked on purpose)",
			c.MaxLoadPerCore, a.Cores)
	}
	if a.Braked(c.MaxLoadPerCore) {
		// A braked bench is NOT a failed bench: it answers zero and the tick carries on.
		// The line is the whole point -- a bench with free slots that took no card has to
		// say why, or the next hand widens something that was never the size.
		if c.Stderr != nil {
			fmt.Fprintf(c.Stderr,
				"FILL BRAKE bench=%s load1=%.2f cores=%d per-core=%.2f max=%.2f free=%d held=%d remedy=%q\n",
				field(bench), a.Load1, a.Cores, a.PerCore(), c.MaxLoadPerCore, a.Free(), a.Held,
				"the load brake holds new launches; the live leases are untouched -- report the load, or raise --max-load-per-core for a bench whose load is not its own leases")
		}
		return 0, nil
	}
	return a.Free(), nil
}
