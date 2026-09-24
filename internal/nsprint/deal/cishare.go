// Package deal is the reconciler's deal pass (nova-sprint #2756 section 5.3).
//
// This file is the ci share of the deal pass (issue #3039; #2756 10.4,
// control 30). A ci card (label ci-<pr>-<sha8>, cut by `nova-sprint ci cut`,
// #2936) takes one slot inside the machine width like any card, and on every
// bench, every pass, it is dealt under two policy lines at once:
//
//   - the slot share: ci cards hold at most ci_share (interim 0.5) of the
//     bench's desired slots, counting the ci cards already live there, so the
//     rest stays for model cards;
//   - the core cap: the sum of ci_procs (interim 8, the GOMAXPROCS every ci
//     card runs under) over the bench's live ci cards plus the new one stays
//     within ci_cores (interim half the measured cores of machine:<m>:ceiling).
//
// ci cards are dealt BEFORE bulk on every bench with a free slot, and
// backpressure never holds one: DealCIFirst does not take a backpressure
// input for ci cards at all, so no policy can hold them. A ci card that is not
// dealt (bench paused, down, full, or over the share or the core cap) is left
// exactly as it was, queued, which the ci line prints as `cut`; the pass has
// no cancel path, so a pause can never produce cancelled, FAIL or MISSING,
// and the first pass after resume deals it.
//
// THE HURT (#2795): 9 unbounded test shards outside the dealer took 39 cores
// on vision (load 97), and 35 of 53 heads were cancelled mid-job.
//
// THE SEAM. This file is pure: it plans, it never writes. The pass of #2743
// (PR #3061) reserves what it returns: ci cards first, the bench's Leased
// grown by the ci cards dealt, then its bulk Plan over what is left.
package deal

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Policy lines of s:<S>:policy read by the ci share (#2756 10.4 item 2).
const (
	PolicyCIShare = "ci_share"
	PolicyCIProcs = "ci_procs"
	PolicyCICores = "ci_cores"
	PolicyCIClock = "ci_clock_s"
)

// Interim policy values until the first fold measures them (#2756 10.4).
const (
	DefaultCIShare = 0.5
	DefaultCIProcs = 8
	// DefaultCIClock is the ci clock of section 1 as the deal pass holds it:
	// a ci card waiting in the pool longer than this is a red line.
	DefaultCIClock = 10 * time.Minute
)

// CIStateCut is what the ci line prints for a ci card that is queued and not
// yet dealt (ci status counts card state queued as cut). It is the only state
// the ci share ever leaves an undealt ci card in.
const CIStateCut = "cut"

// Why a ci card was not dealt on a bench this pass.
const (
	CIWhyNoBench = "no bench up"
	CIWhyPaused  = "bench paused"
	CIWhyFull    = "no free slot"
	CIWhyShare   = "ci_share"
	CIWhyCores   = "ci_cores"
)

// CIPolicy is the ci share of one sprint's policy.
type CIPolicy struct {
	Share float64       // fraction of desired slots ci cards may hold
	Procs int           // GOMAXPROCS of one ci card
	Cores int           // ci cores per bench; 0: half the bench's measured cores
	Clock time.Duration // a ci card waiting longer is a red line
}

// InterimCIPolicy is the policy #2756 10.4 names until the first fold.
func InterimCIPolicy() CIPolicy {
	return CIPolicy{Share: DefaultCIShare, Procs: DefaultCIProcs, Clock: DefaultCIClock}
}

// maxClockSeconds is the largest ci_clock_s a time.Duration holds.
const maxClockSeconds = math.MaxInt64 / int64(time.Second)

// ReadCIPolicy reads the ci lines of an s:<S>:policy hash. A missing line
// takes the interim value; a line present but unreadable or out of range is
// refused, never guessed.
func ReadCIPolicy(policy map[string]string) (CIPolicy, error) {
	p := InterimCIPolicy()
	if v, ok := policy[PolicyCIShare]; ok {
		f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		if err != nil || math.IsNaN(f) || f < 0 || f > 1 {
			return CIPolicy{}, fmt.Errorf("policy %s=%q: want a fraction in [0,1]", PolicyCIShare, v)
		}
		p.Share = f
	}
	if v, ok := policy[PolicyCIProcs]; ok {
		n, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil || n < 1 {
			return CIPolicy{}, fmt.Errorf("policy %s=%q: want a positive integer", PolicyCIProcs, v)
		}
		p.Procs = n
	}
	if v, ok := policy[PolicyCICores]; ok {
		n, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil || n < 0 {
			return CIPolicy{}, fmt.Errorf("policy %s=%q: want a non-negative integer (0: half the measured cores)", PolicyCICores, v)
		}
		p.Cores = n
	}
	if v, ok := policy[PolicyCIClock]; ok {
		// Bound before multiplying: past maxClockSeconds the Duration overflows
		// to a nonpositive clock, which would read as "use the default".
		n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
		if err != nil || n < 1 || n > maxClockSeconds {
			return CIPolicy{}, fmt.Errorf("policy %s=%q: want a positive number of seconds up to %d", PolicyCIClock, v, maxClockSeconds)
		}
		p.Clock = time.Duration(n) * time.Second
	}
	return p, nil
}

// ShareSlots is how many of desired slots ci cards may hold on one bench.
func (p CIPolicy) ShareSlots(desired int) int {
	if desired <= 0 || p.Share <= 0 {
		return 0
	}
	return int(math.Floor(p.Share*float64(desired) + 1e-9))
}

// CoreCap is ci_cores for a bench with the given measured cores.
func (p CIPolicy) CoreCap(measured int) int {
	if p.Cores > 0 {
		return p.Cores
	}
	return measured / 2
}

func (p CIPolicy) procs(c QueuedCard) int {
	if c.Procs > 0 {
		return c.Procs
	}
	if p.Procs > 0 {
		return p.Procs
	}
	return DefaultCIProcs
}

// CIBench is one bench as the ci share sees it.
type CIBench struct {
	Name    string
	Up      bool
	Paused  bool
	Slots   int // bench:<b>:desired slots
	Leased  int // starting + living over every sprint, ci cards included
	Cores   int // measured cores of the bench's machine (machine:<m>:ceiling)
	CILive  int // ci cards starting or living on the bench
	CIProcs int // sum of ci_procs over those
}

func (b CIBench) free() int {
	if f := b.Slots - b.Leased; f > 0 {
		return f
	}
	return 0
}

// QueuedCard is one queued card of a sprint's pool, ci or model.
type QueuedCard struct {
	Sprint   string
	Label    string
	Priority float64 // pool score; lower deals first
	Tier     string  // model cards: "priority" flows under backpressure
	Bench    string  // pinned bench; empty: any
	Procs    int     // ci cards: GOMAXPROCS; 0 is the policy's ci_procs
	CutAt    time.Time
}

// IsCI reports whether the card is a ci card (#2756 10.2 item 1: label
// ci-<pr>-<sha8>).
func (c QueuedCard) IsCI() bool { return strings.HasPrefix(c.Label, "ci-") }

// CIWait is a ci card the pass did not deal. Its state is always CIStateCut.
type CIWait struct {
	Card  QueuedCard
	State string
	Why   string
	Age   time.Duration
	Red   bool // waiting past the ci clock
}

// BenchDeal is one bench's batch, in deal order: CI first, then Bulk.
type BenchDeal struct {
	Bench string
	CI    []QueuedCard
	Bulk  []QueuedCard
}

// Cards is the batch in the order the pass reserves and launches it.
func (d BenchDeal) Cards() []QueuedCard {
	return append(append([]QueuedCard(nil), d.CI...), d.Bulk...)
}

// CIPass is one pass of the ci share over the fleet.
type CIPass struct {
	Benches []BenchDeal
	Waiting []CIWait
}

// Bench returns the batch of one bench (empty when it got nothing).
func (p CIPass) Bench(name string) BenchDeal {
	for _, b := range p.Benches {
		if b.Bench == name {
			return b
		}
	}
	return BenchDeal{Bench: name}
}

// Red is the ci cards waiting past the ci clock.
func (p CIPass) Red() []CIWait {
	var out []CIWait
	for _, w := range p.Waiting {
		if w.Red {
			out = append(out, w)
		}
	}
	return out
}

// DealCIFirst plans one pass: on every UP, unpaused bench with a free slot it
// deals ci cards first, while the bench's ci cards stay within the slot share
// and the core cap, then fills the slots that remain with model cards. Held
// reports whether backpressure holds a MODEL card; it is never consulted for
// a ci card. A ci card that is not dealt is returned in Waiting as `cut` with
// the reason of the last bench that could not take it. Nothing is written.
func DealCIFirst(now time.Time, benches []CIBench, queue []QueuedCard, pol CIPolicy, held func(QueuedCard) bool) CIPass {
	var ci, model []QueuedCard
	for _, c := range queue {
		if c.IsCI() {
			ci = append(ci, c)
		} else {
			model = append(model, c)
		}
	}
	order := func(cs []QueuedCard) {
		sort.SliceStable(cs, func(i, j int) bool {
			if cs[i].Priority != cs[j].Priority {
				return cs[i].Priority < cs[j].Priority
			}
			return cs[i].Sprint+"/"+cs[i].Label < cs[j].Sprint+"/"+cs[j].Label
		})
	}
	order(ci)
	order(model)
	bs := append([]CIBench(nil), benches...)
	sort.Slice(bs, func(i, j int) bool { return bs[i].Name < bs[j].Name })

	taken := map[string]bool{}
	why := map[string]string{}
	var out CIPass
	for _, b := range bs {
		if reason := ciBenchWhy(b); reason != "" {
			for _, c := range ci {
				if k := c.Sprint + "/" + c.Label; !taken[k] && (c.Bench == "" || c.Bench == b.Name) {
					setWhy(why, k, reason)
				}
			}
			continue
		}
		free := b.free()
		share := pol.ShareSlots(b.Slots) - b.CILive
		cores := pol.CoreCap(b.Cores) - b.CIProcs
		d := BenchDeal{Bench: b.Name}
		for _, c := range ci {
			k := c.Sprint + "/" + c.Label
			if taken[k] || (c.Bench != "" && c.Bench != b.Name) {
				continue
			}
			n := pol.procs(c)
			switch {
			case free == 0:
				setWhy(why, k, CIWhyFull)
				continue
			case share <= 0:
				setWhy(why, k, CIWhyShare)
				continue
			case n > cores:
				setWhy(why, k, CIWhyCores)
				continue
			}
			d.CI = append(d.CI, c)
			taken[k] = true
			free--
			share--
			cores -= n
		}
		for _, c := range model {
			if free == 0 {
				break
			}
			k := c.Sprint + "/" + c.Label
			if taken[k] || (c.Bench != "" && c.Bench != b.Name) {
				continue
			}
			if held != nil && held(c) {
				continue
			}
			d.Bulk = append(d.Bulk, c)
			taken[k] = true
			free--
		}
		if len(d.CI)+len(d.Bulk) > 0 {
			out.Benches = append(out.Benches, d)
		}
	}
	clock := pol.Clock
	if clock <= 0 {
		clock = DefaultCIClock
	}
	for _, c := range ci {
		k := c.Sprint + "/" + c.Label
		if taken[k] {
			continue
		}
		w := CIWait{Card: c, State: CIStateCut, Why: why[k]}
		if w.Why == "" {
			w.Why = CIWhyNoBench
		}
		if !c.CutAt.IsZero() && now.After(c.CutAt) {
			w.Age = now.Sub(c.CutAt)
			w.Red = w.Age > clock
		}
		out.Waiting = append(out.Waiting, w)
	}
	return out
}

// ciBenchWhy is why a bench takes no card this pass, or "" when it can.
func ciBenchWhy(b CIBench) string {
	switch {
	case !b.Up:
		return CIWhyNoBench
	case b.Paused:
		return CIWhyPaused
	case b.free() == 0:
		return CIWhyFull
	}
	return ""
}

// ciWhyRank orders the reasons: a bench that could have taken the card but
// hit a cap outranks one that was full, paused or down.
var ciWhyRank = map[string]int{CIWhyNoBench: 0, CIWhyPaused: 1, CIWhyFull: 2, CIWhyShare: 3, CIWhyCores: 3}

// setWhy keeps the most telling reason for a card not dealt.
func setWhy(why map[string]string, k, reason string) {
	if cur, ok := why[k]; !ok || ciWhyRank[reason] >= ciWhyRank[cur] {
		why[k] = reason
	}
}
