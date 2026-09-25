package deal

import (
	"fmt"
	"reflect"
	"testing"
	"time"
)

// The fixture of #3039's DONE-WHEN: one bench, 8 desired slots, 64 measured
// cores, 10 ci cards and 10 model cards queued, interim policy (ci_share 0.5,
// ci_procs 8, ci_cores half the measured cores = 32).
const fxBench = "fx"

var fxNow = time.Date(2026, 9, 23, 13, 0, 0, 0, time.UTC)

func fxBenchRow() CIBench {
	return CIBench{Name: fxBench, Up: true, Slots: 8, Cores: 64}
}

func fxQueue() []QueuedCard {
	var q []QueuedCard
	for i := 0; i < 10; i++ {
		q = append(q, QueuedCard{
			Sprint: "s1", Label: fmt.Sprintf("ci-%d-%08x", 3000+i, i),
			Priority: -1, Tier: "front", CutAt: fxNow.Add(-time.Minute),
		})
		q = append(q, QueuedCard{
			Sprint: "s1", Label: fmt.Sprintf("card-%02d", i),
			Priority: float64(i + 1), Tier: fxTierBulk,
		})
	}
	return q
}

// fxTierBulk is the fixture's model-card tier; backpressure ON holds it.
const fxTierBulk = "bulk"

func ciCount(cs []QueuedCard) (ci, model int) {
	for _, c := range cs {
		if c.IsCI() {
			ci++
		} else {
			model++
		}
	}
	return
}

func TestCIShareAndCoreCap(t *testing.T) {
	t.Parallel()

	pol := InterimCIPolicy()
	if got, want := pol.ShareSlots(8), 4; got != want {
		t.Fatalf("ShareSlots(8) = %d, want %d (0.5 x 8)", got, want)
	}
	if got, want := pol.CoreCap(64)/pol.Procs, 4; got != want {
		t.Fatalf("ci_cores/ci_procs = %d, want %d (32/8)", got, want)
	}

	t.Run("one pass deals 4 ci cards first then 4 model cards", func(t *testing.T) {
		p := DealCIFirst(fxNow, []CIBench{fxBenchRow()}, fxQueue(), pol, nil)
		d := p.Bench(fxBench)
		if len(d.CI) != 4 || len(d.Bulk) != 4 {
			t.Fatalf("dealt ci=%d model=%d, want ci=4 model=4", len(d.CI), len(d.Bulk))
		}
		batch := d.Cards()
		for i, c := range batch {
			if want := i < 4; c.IsCI() != want {
				t.Fatalf("batch[%d] = %s: ci cards must come first, then bulk (%v)", i, c.Label, ciLabels(batch))
			}
		}
		if len(p.Waiting) != 6 {
			t.Fatalf("waiting = %d ci cards, want 6", len(p.Waiting))
		}
		for _, w := range p.Waiting {
			if w.State != CIStateCut || (w.Why != CIWhyShare && w.Why != CIWhyCores) {
				t.Fatalf("waiting %s: state=%q why=%q, want cut under ci_share or ci_cores", w.Card.Label, w.State, w.Why)
			}
		}
	})

	t.Run("the core cap binds on its own", func(t *testing.T) {
		tight := pol
		tight.Cores = 16 // two ci cards of 8 procs
		d := DealCIFirst(fxNow, []CIBench{fxBenchRow()}, fxQueue(), tight, nil).Bench(fxBench)
		if len(d.CI) != 2 || len(d.Bulk) != 6 {
			t.Fatalf("ci_cores 16: dealt ci=%d model=%d, want ci=2 model=6", len(d.CI), len(d.Bulk))
		}
	})

	t.Run("the slot share binds on its own", func(t *testing.T) {
		narrow := pol
		narrow.Share = 0.25
		d := DealCIFirst(fxNow, []CIBench{fxBenchRow()}, fxQueue(), narrow, nil).Bench(fxBench)
		if len(d.CI) != 2 || len(d.Bulk) != 6 {
			t.Fatalf("ci_share 0.25: dealt ci=%d model=%d, want ci=2 model=6", len(d.CI), len(d.Bulk))
		}
	})

	t.Run("live ci cards count against both caps", func(t *testing.T) {
		b := fxBenchRow()
		b.Leased, b.CILive, b.CIProcs = 2, 2, 16
		d := DealCIFirst(fxNow, []CIBench{b}, fxQueue(), pol, nil).Bench(fxBench)
		if len(d.CI) != 2 || len(d.Bulk) != 4 {
			t.Fatalf("2 ci live: dealt ci=%d model=%d, want ci=2 model=4", len(d.CI), len(d.Bulk))
		}
	})

	t.Run("a heavy ci card is refused by cores, a light one still fits", func(t *testing.T) {
		q := []QueuedCard{
			{Sprint: "s1", Label: "ci-1-aaaaaaaa", Priority: -2, Procs: 40},
			{Sprint: "s1", Label: "ci-2-bbbbbbbb", Priority: -1, Procs: 8},
		}
		p := DealCIFirst(fxNow, []CIBench{fxBenchRow()}, q, pol, nil)
		d := p.Bench(fxBench)
		if len(d.CI) != 1 || d.CI[0].Label != "ci-2-bbbbbbbb" {
			t.Fatalf("dealt %v, want only ci-2-bbbbbbbb (40 procs > ci_cores 32)", ciLabels(d.CI))
		}
		if len(p.Waiting) != 1 || p.Waiting[0].Why != CIWhyCores {
			t.Fatalf("waiting %+v, want ci-1 under ci_cores", p.Waiting)
		}
	})

	t.Run("backpressure ON still deals the 4 ci cards", func(t *testing.T) {
		held := func(c QueuedCard) bool {
			if c.IsCI() {
				t.Fatalf("backpressure was consulted for ci card %s; it never holds one", c.Label)
			}
			return c.Tier != "priority"
		}
		d := DealCIFirst(fxNow, []CIBench{fxBenchRow()}, fxQueue(), pol, held).Bench(fxBench)
		if len(d.CI) != 4 || len(d.Bulk) != 0 {
			t.Fatalf("backpressure ON: dealt ci=%d model=%d, want ci=4 model=0", len(d.CI), len(d.Bulk))
		}
	})
}

func TestControl30NeverDealtIsNeverCancelled(t *testing.T) {
	t.Parallel()

	pol := InterimCIPolicy()
	queue := fxQueue()
	before := append([]QueuedCard(nil), queue...)

	paused := fxBenchRow()
	paused.Paused = true
	p := DealCIFirst(fxNow, []CIBench{paused}, queue, pol, nil)
	if len(p.Benches) != 0 {
		t.Fatalf("paused bench dealt %v, want nothing", p.Benches)
	}
	if !reflect.DeepEqual(queue, before) {
		t.Fatalf("the pass changed the queue it was given")
	}
	ci, _ := ciCount(queue)
	if len(p.Waiting) != ci {
		t.Fatalf("ci line shows %d waiting, want all %d ci cards", len(p.Waiting), ci)
	}
	for _, w := range p.Waiting {
		if w.State != CIStateCut {
			t.Fatalf("%s shows %q on the ci line while its bench is paused, want %q (never cancelled, FAIL or MISSING)", w.Card.Label, w.State, CIStateCut)
		}
		if w.Why != CIWhyPaused {
			t.Fatalf("%s why=%q, want %q", w.Card.Label, w.Why, CIWhyPaused)
		}
		if w.Red {
			t.Fatalf("%s is red after 1 minute; the ci clock is %s", w.Card.Label, pol.Clock)
		}
	}
	first := p.Waiting[0].Card

	// Resume: the first pass deals the cut card, ci first.
	p = DealCIFirst(fxNow, []CIBench{fxBenchRow()}, queue, pol, nil)
	d := p.Bench(fxBench)
	if len(d.CI) != 4 || len(d.Bulk) != 4 {
		t.Fatalf("after resume: dealt ci=%d model=%d, want ci=4 model=4", len(d.CI), len(d.Bulk))
	}
	if d.CI[0] != first {
		t.Fatalf("after resume the first ci card dealt is %s, want the cut card %s", d.CI[0].Label, first.Label)
	}

	// A ci card waiting past the ci clock is a red line, still cut.
	late := fxNow.Add(pol.Clock + time.Minute)
	p = DealCIFirst(late, []CIBench{paused}, queue, pol, nil)
	if got := len(p.Red()); got != ci {
		t.Fatalf("red ci lines past the clock = %d, want %d", got, ci)
	}
	for _, w := range p.Red() {
		if w.State != CIStateCut {
			t.Fatalf("a red ci card shows %q, want %q", w.State, CIStateCut)
		}
	}
}

func TestReadCIPolicyRefusesUnreadableLines(t *testing.T) {
	t.Parallel()

	p, err := ReadCIPolicy(map[string]string{})
	if err != nil || p != InterimCIPolicy() {
		t.Fatalf("empty policy = %+v, %v; want the interim policy", p, err)
	}
	p, err = ReadCIPolicy(map[string]string{PolicyCIShare: "0.25", PolicyCIProcs: "4", PolicyCICores: "24", PolicyCIClock: "300"})
	if err != nil {
		t.Fatal(err)
	}
	if want := (CIPolicy{Share: 0.25, Procs: 4, Cores: 24, Clock: 300 * time.Second}); p != want {
		t.Fatalf("policy = %+v, want %+v", p, want)
	}
	for _, bad := range []map[string]string{
		{PolicyCIShare: "half"}, {PolicyCIShare: "1.5"}, {PolicyCIShare: "NaN"},
		{PolicyCIProcs: "0"}, {PolicyCICores: "-1"}, {PolicyCIClock: "soon"},
		// one past math.MaxInt64 / int64(time.Second): the Duration would overflow
		{PolicyCIClock: "9223372037"}, {PolicyCIClock: "99999999999999999999"},
	} {
		if _, err := ReadCIPolicy(bad); err == nil {
			t.Fatalf("ReadCIPolicy(%v) accepted an unreadable line", bad)
		}
	}
	// the largest clock a time.Duration holds in whole seconds is accepted exactly
	p, err = ReadCIPolicy(map[string]string{PolicyCIClock: "9223372036"})
	if err != nil {
		t.Fatalf("ci_clock_s at the Duration bound refused: %v", err)
	}
	if want := time.Duration(9223372036) * time.Second; p.Clock != want || p.Clock <= 0 {
		t.Fatalf("ci_clock_s at the Duration bound = %v, want %v", p.Clock, want)
	}
}

func ciLabels(cs []QueuedCard) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.Label
	}
	return out
}
