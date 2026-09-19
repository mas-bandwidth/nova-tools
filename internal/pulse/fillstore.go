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

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// CapacityAnswer is one bench's capacity probe, parsed. FromStore says which of the two
// numbers the bench could give: its store's own free count, or the legacy load formula for a
// bench that has no store yet.
type CapacityAnswer struct {
	FromStore bool
	Share     int     // the owner's row in shares.tsv, when FromStore
	Held      int     // the live leases that owner holds, when FromStore
	Formula   int     // the legacy load formula's number, when not FromStore
	Cores     int     // the bench's cores, for the brake; 0 when CoresRead is false
	CoresRead bool    // whether the bench could measure its cores at all
	Load1     float64 // the bench's one-minute load, for the brake; 0 when LoadRead is false
	LoadRead  bool    // whether the bench could measure its load at all
	Why       string  // why the bench answered the formula rather than its store
}

// UnreadableMeasurement is what a bench says instead of a number when every reader for that
// measurement failed. It is never 0: a zero load passes every brake, so a load nobody could
// read used to turn the configured brake off without saying a word (Stella, R2 of #1945).
const UnreadableMeasurement = "unreadable"

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
	if a.Cores <= 0 || !a.CoresRead || !a.LoadRead {
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
func ParseCapacityAnswer(out, owner string) (CapacityAnswer, error) {
	// One header line, then an optional `leases` marker and the listing itself. Anything
	// between the header and the marker is a probe saying more than its contract allows.
	lines := strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n")
	head := ""
	var listing []string
	hasListing := false
	for i, line := range lines {
		if i == 0 {
			head = line
			continue
		}
		if !hasListing {
			if strings.TrimSpace(line) == "leases" {
				hasListing = true
				continue
			}
			if strings.TrimSpace(line) != "" {
				return CapacityAnswer{}, fmt.Errorf(
					"the capacity probe said %q before its `leases` marker; a probe answers one header line and nothing else",
					oneLineAnswer(line))
			}
			continue
		}
		listing = append(listing, line)
	}
	fields := strings.Fields(strings.TrimSpace(head))
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
	// A HEADER FIELD THE PARSER DOES NOT KNOW IS A REFUSAL. `held=` used to be one of
	// them, read straight off the bench and never checked; the leases are counted here
	// now, and a bench that still says `held=` is a bench answering a contract that is
	// gone. An unknown field is not a field to step over.
	allowed := map[string]map[string]bool{
		"store":   {"share": true, "cores": true, "load1": true},
		"formula": {"capacity": true, "cores": true, "load1": true, "why": true},
	}[kind]
	if allowed != nil {
		for k := range seen {
			if !allowed[k] {
				return CapacityAnswer{}, fmt.Errorf(
					"the capacity probe answered %s=%s, which is not a field of a %s answer; refusing a reading nobody wrote down",
					oneline.Field(k), oneline.Field(seen[k]), kind)
			}
		}
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
		// THE LEASES ARE COUNTED HERE, IN GO. They used to be counted by a shell
		// `grep -c` whose exit status belonged to grep, so a lease read that failed
		// counted zero and a full bench answered its whole share; and the pattern was
		// `state=live`, which misses a DRIFT lease that `nova-swarm native` counts as
		// held, so the probe read high against the seat that actually grants.
		if strings.TrimSpace(owner) == "" {
			return CapacityAnswer{}, fmt.Errorf(
				"the store's owner is empty, so its leases cannot be told from anybody else's; name it with --slots-owner")
		}
		if !hasListing {
			return CapacityAnswer{}, fmt.Errorf(
				"the capacity probe answered %q with no `leases` listing after it; refusing a share with no lease count",
				oneLineAnswer(head))
		}
		if a.Held, err = countLeases(strings.Join(listing, "\n"), owner); err != nil {
			return CapacityAnswer{}, err
		}
		if a.Held > a.Share {
			return CapacityAnswer{}, fmt.Errorf(
				"the store says %s holds %d leases against a share of %d; an owner cannot hold more than its share, so this reading went wrong",
				oneline.Field(owner), a.Held, a.Share)
		}
	case "formula":
		if a.Formula, err = count("capacity"); err != nil {
			return CapacityAnswer{}, err
		}
		a.Why = seen["why"]
	default:
		return CapacityAnswer{}, fmt.Errorf(
			"the capacity probe answered %q; wanted `store share=<n> held=<n> cores=<n> load1=<f>`, `formula capacity=<n> cores=<n> load1=<f>` or `unreadable reason=<why>`",
			oneLineAnswer(out))
	}
	// Cores and load are the brake's readings, and they are required: a bench that cannot
	// say what its load is cannot be braked, and running it unbraked is the brake turning
	// itself off. Whether that is fatal is the caller's -- a caller with the brake off
	// (--max-load-per-core 0) does not need them -- but they must at least be READABLE.
	if seen["cores"] == UnreadableMeasurement {
		a.Cores, a.CoresRead = 0, false
	} else if a.Cores, err = count("cores"); err != nil {
		return CapacityAnswer{}, err
	} else {
		a.CoresRead = true
	}
	raw, ok := seen["load1"]
	if !ok {
		return CapacityAnswer{}, fmt.Errorf("the capacity probe answered %q, with no load1=", oneLineAnswer(out))
	}
	if raw == UnreadableMeasurement {
		// Kept as itself all the way to the brake. Whether it is fatal is the brake's
		// to say, and it is the ONE thing this must not decide by substituting a number.
		a.Load1, a.LoadRead = 0, false
		return a, nil
	}
	a.LoadRead = true
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

// leaseStateExpired is the one `nova-swarm slots list` state that is NOT held: the lease's
// clock ran out AND its process is gone. `live` is held, and so is `DRIFT` -- expired by the
// clock with the process still running -- because that is what `nova-swarm native` counts
// when it grants, and a probe that counts fewer deals cards the bench then refuses.
const leaseStateExpired = "expired"

// leaseStates is the vocabulary internal/swarm writes. A state outside it is a refusal
// rather than a guess about which side of held it falls on.
var leaseStates = map[string]bool{"live": true, "DRIFT": true, leaseStateExpired: true}

// countLeases reads `nova-swarm slots list`'s own output -- one
// `SLOT <id> owner=<o> pid=<n> label=<l> until=<t> state=<s>` per lease -- and answers how
// many of them this owner holds. Every non-empty row must BE a lease: a row that is not is
// a refusal, because a row nobody matched used to be indistinguishable from a free slot.
func countLeases(listing, owner string) (int, error) {
	held := 0
	for _, line := range strings.Split(listing, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, "SLOT ") {
			return 0, fmt.Errorf(
				"the lease listing carries a row that is not a lease: %q", oneLineAnswer(line))
		}
		var rowOwner, state string
		for _, f := range strings.Fields(line) {
			switch k, v, _ := strings.Cut(f, "="); k {
			case "owner":
				rowOwner = v
			case "state":
				state = v
			}
		}
		if rowOwner == "" || state == "" {
			return 0, fmt.Errorf(
				"the lease listing carries a row with no owner= or no state=: %q", oneLineAnswer(line))
		}
		if !leaseStates[state] {
			return 0, fmt.Errorf(
				"the lease listing carries state=%s, which is not one this fill knows (live, DRIFT, expired); refusing to guess whether it is held",
				oneline.Field(state))
		}
		if rowOwner == owner && state != leaseStateExpired {
			held++
		}
	}
	return held, nil
}

// unreadOr prints a measurement, or the word a bench uses when nobody could take it.
func unreadOr(read bool, s string) string {
	if !read {
		return UnreadableMeasurement
	}
	return s
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
	Owner          string // the store's owner row, whose leases are counted
	MaxLoadPerCore float64
	Stderr         io.Writer
}

// ownerFor is the store row whose leases are this bench's: the one named, or the seat the
// launcher already hands the bench.
func (c StoreCapacity) ownerFor(bench string) string {
	if o := strings.TrimSpace(c.Owner); o != "" {
		return o
	}
	return "swarm-" + bench
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
	a, err := ParseCapacityAnswer(out, c.ownerFor(bench))
	if err != nil {
		return 0, err
	}
	// THE ONE FALLBACK IS NEVER SILENT. A bench answering the old load formula is a bench
	// with no row of its own in a store the probe could read, and it says which.
	if !a.FromStore && c.Stderr != nil {
		why := a.Why
		if why == "" {
			why = "no-store-row"
		}
		fmt.Fprintf(c.Stderr,
			"FILL FORMULA bench=%s why=%s capacity=%d note=%q\n",
			field(bench), oneline.Field(why), a.Free(),
			"this bench has no row of its own in the slot store, so its capacity is the old load formula; give it a share to size it by its store")
	}
	// THE BRAKE IS ON AND A MEASUREMENT IT NEEDS WAS NEVER TAKEN. A bench that runs
	// unbraked because its own reading failed is the brake turning itself off, which is
	// the silence this guard exists to break. `--max-load-per-core 0` says there is no
	// brake, and then there is nothing for an unread measurement to hold the bench with --
	// but it is still said out loud, because an unread measurement is worth saying either
	// way.
	if c.MaxLoadPerCore > 0 {
		if !a.LoadRead {
			return 0, fmt.Errorf(
				"load-unreadable: the brake is on (%.2f per core) and no reader on the bench could measure its load; refusing to fill a bench nobody can brake (fix the load reader, or say --max-load-per-core 0 to run it unbraked on purpose)",
				c.MaxLoadPerCore)
		}
		if !a.CoresRead {
			return 0, fmt.Errorf(
				"cores-unreadable: the brake is on (%.2f per core) and no reader on the bench could measure its cores; refusing to fill a bench nobody can brake (fix the core reader, or say --max-load-per-core 0 to run it unbraked on purpose)",
				c.MaxLoadPerCore)
		}
		if a.Cores <= 0 {
			return 0, fmt.Errorf(
				"cores-unreadable: the load brake is on (%.2f per core) and the bench answered cores=%d; refusing to fill a bench nobody can brake (measure its cores, or say --max-load-per-core 0 to run it unbraked on purpose)",
				c.MaxLoadPerCore, a.Cores)
		}
	} else if (!a.LoadRead || !a.CoresRead) && c.Stderr != nil {
		fmt.Fprintf(c.Stderr,
			"FILL UNMEASURED bench=%s cores=%s load1=%s note=%q\n",
			field(bench), unreadOr(a.CoresRead, strconv.Itoa(a.Cores)), unreadOr(a.LoadRead, "-"),
			"the brake is off (--max-load-per-core 0), so a measurement nobody took does not hold this bench")
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
