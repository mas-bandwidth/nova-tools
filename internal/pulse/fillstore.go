package pulse

// The store is the capacity (#1914); the load is not the fill's to judge (#3251).
//
// `fill`'s capacity used to be a load formula -- `cores*3/2 - load1 - cores/8`, the min of
// that and two headroom terms -- and a bench earns load precisely by running the cards it
// was dealt. Measured on 2026-09-19 at 21:00Z: space answered **-1**, clamped to zero and
// dealt nothing, with **52 free slots in its own store**; hulk answered 12 against a share
// of 40. The harder the fleet worked the less it was fed, which is the stall itself.
//
// The bench's own slot store is its answer to "how many more cards may I take": the owner's
// row in `<store>/shares.tsv` minus the live leases that owner holds, which is exactly what
// `nova-swarm native --slots-store --owner` enforces at launch. That number is the capacity,
// and nothing on the bench shrinks it. The load brake that sat on top of it
// (`--max-load-per-core`) is gone (Glenn 2026-09-23: "Load checks. Load decisions can be made
// centrally."): the bench reports load1/ncpu on its row (bench-row), and the dealer
// (internal/pulse/dealer, Policy.MaxLoadPerCore) decides from that row how many cards to
// deal a bench BEFORE they enter its ready queue.
//
// The probe is a seam. The command runs one line of shell on the bench (over ssh, or on this
// machine for a local bench) and hands back what it printed; a test hands back the same line
// and opens nothing.

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// CapacityAnswer is one bench's capacity probe, parsed. FromStore says which of the two
// numbers the bench could give: its store's own free count, or the legacy load formula for a
// bench that has no store yet.
type CapacityAnswer struct {
	FromStore bool
	Share     int    // the owner's row in shares.tsv, when FromStore
	Held      int    // the live leases that owner holds, when FromStore
	Formula   int    // the legacy load formula's number, when not FromStore
	Why       string // why the bench answered the formula rather than its store
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
// EVERY COUNT IS VALIDATED (Stella, #1945). A malformed reading used to be taken as a valid
// one: `held=-10` made a share of 64 answer 74. Counts are whole numbers and never negative,
// and a field said twice is a refusal -- a line nobody can read one way is not a line to act
// on. `cores=` and `load1=` are still allowed on the line (the probe measures them for the
// legacy formula) and read by nothing here: the load is the dealer's (#3251).
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
		// The reason TOKEN leads, so `FILL UNREADABLE ... reason=<token>` reads as one
		// word a person can grep for and a runbook can name.
		token := seen["reason"]
		if token == "" {
			token = "unreadable"
		}
		return CapacityAnswer{}, fmt.Errorf(
			"%s: the bench could not read its slot store (%s); refusing a capacity nobody read, rather than calling a full bench empty",
			oneline.Field(token), oneLineAnswer(strings.Join(rest, " ")))
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
// line and answers the store's free count. Probe is the seam -- the command
// gives it an ssh (or this machine's own shell for a local bench) and a test gives it a
// canned line.
type StoreCapacity struct {
	Probe  func(bench string) (string, error)
	Owner  string // the store's owner row, whose leases are counted
	Stderr io.Writer
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
	return a.Free(), nil
}
