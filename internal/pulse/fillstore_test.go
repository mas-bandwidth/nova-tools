package pulse

// The store leads, the load only brakes (#1914). A bench's own slot store says how many
// more cards its owner may take -- the owner's share in shares.tsv minus the live leases
// that owner holds -- and THAT is the fill's capacity. The load formula that used to BE the
// capacity answered -1 on a bench with 52 free slots, because a bench earns load precisely
// by running the cards it was dealt: the harder the fleet worked the less it was fed.
//
// No test here opens an ssh connection. The probe is the seam: it answers the one line the
// bench's own shell would have printed.

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// storeProbe answers a canned line per bench, the way the bench's shell would, and counts
// how often it was asked.
type storeProbe struct {
	lines map[string]string
	calls map[string]int
}

func (p *storeProbe) probe(bench string) (string, error) {
	if p.calls == nil {
		p.calls = map[string]int{}
	}
	p.calls[bench]++
	line, ok := p.lines[bench]
	if !ok {
		return "", fmt.Errorf("no answer for %s", bench)
	}
	return line, nil
}

// storeLine is what a bench's capacity probe prints when it could read the store: the
// header, the `leases` marker, and the lease listing itself -- which is what Go counts.
func storeLine(share, held, cores int, load1 float64) string {
	b := fmt.Sprintf("store share=%d cores=%d load1=%.2f\nleases\n", share, cores, load1)
	for i := 0; i < held; i++ {
		b += fmt.Sprintf("SLOT %d owner=%s pid=%d label=- until=2026-09-20T00:00:00Z state=live\n",
			i+1, testOwner, 100+i)
	}
	return b
}

// testOwner is the store row every probe in this file answers for.
const testOwner = "swarm-bench-a"

// TestFillCapacityIsTheStoresFreeCountNotTheLoadFormula is #1914 itself: space at load1 45
// on 32 cores answered -1 from the formula -- clamped to 0, dealt nothing -- while its own
// store held 52 free slots. Here the bench is busy enough that the old formula would refuse
// it, and the store says two slots are free: exactly two cards launch.
func TestFillCapacityIsTheStoresFreeCountNotTheLoadFormula(t *testing.T) {
	dir := t.TempDir()
	ready, launched := filepath.Join(dir, "ready"), filepath.Join(dir, "launched")
	for i := 1; i <= 5; i++ {
		writeCard(t, ready, fmt.Sprintf("card-%03d.md", i), "a card\n")
	}
	// cores*3/2 - load1 - cores/8 = 48 - 45 - 4 = -1: the number that closed the fleet.
	p := &storeProbe{lines: map[string]string{"bench-a": storeLine(64, 62, 32, 45)}}
	l := &laneLauncher{}
	var out, errb bytes.Buffer
	code := Fill(FillInput{
		Ready: ready, Launched: launched,
		Machines: machinesFile(t, dir, []string{"bench-a"}, nil),
		Benches:  []string{"bench-a"},
		Once:     true,
		Stdout:   &out,
		Stderr:   &errb,
		Capacity: StoreCapacity{Probe: p.probe, Owner: testOwner, Stderr: &errb},
		Launcher: l,
	})
	if code != 0 {
		t.Fatalf("fill exit = %d, want 0; stderr=%q", code, errb.String())
	}
	if len(l.calls) != 2 {
		t.Fatalf("launcher calls = %d, want 2 (share 64 - held 62): %q", len(l.calls), l.calls)
	}
	if got := len(readyCards(ready)); got != 3 {
		t.Fatalf("ready holds %d cards, want 3", got)
	}
	want := "FILL tick=1 bench-a:launched=2,failed=0 ready=3"
	if line := strings.TrimSpace(out.String()); line != want {
		t.Fatalf("FILL line = %q, want %q", line, want)
	}
	if p.calls["bench-a"] != 1 {
		t.Fatalf("the store was probed %d times in one tick, want 1", p.calls["bench-a"])
	}
}

// TestFillTakesUpARaisedShareOnTheNextTick: a share raised while the loop is resident is
// picked up by the very next tick, because the capacity is read from the store every tick
// and never cached. This is the whole of "raised shares do not fill themselves": the Studio
// went from 128 to 192 between two ticks and the fill has to notice.
func TestFillTakesUpARaisedShareOnTheNextTick(t *testing.T) {
	dir := t.TempDir()
	ready, launched := filepath.Join(dir, "ready"), filepath.Join(dir, "launched")
	for i := 1; i <= 5; i++ {
		writeCard(t, ready, fmt.Sprintf("card-%03d.md", i), "a card\n")
	}
	stop := filepath.Join(dir, "STOP")
	p := &storeProbe{lines: map[string]string{"bench-a": storeLine(1, 0, 64, 1)}}
	l := &laneLauncher{}
	var out, errb bytes.Buffer
	slept := 0
	code := Fill(FillInput{
		Ready: ready, Launched: launched,
		Machines: machinesFile(t, dir, []string{"bench-a"}, nil),
		Benches:  []string{"bench-a"},
		Stop:     stop,
		Stdout:   &out,
		Stderr:   &errb,
		Capacity: StoreCapacity{Probe: p.probe, Owner: testOwner, Stderr: &errb},
		Launcher: l,
		Sleep: func(d time.Duration) {
			slept++
			switch slept {
			case 1:
				// The owner's share is raised from under the loop: 1 -> 4, with the
				// one card of tick 1 still leased.
				p.lines["bench-a"] = storeLine(4, 1, 64, 1)
			default:
				if err := os.WriteFile(stop, nil, 0o644); err != nil {
					t.Error(err)
				}
			}
		},
	})
	if code != 0 {
		t.Fatalf("fill exit = %d, want 0; stderr=%q", code, errb.String())
	}
	if len(l.calls) != 4 {
		t.Fatalf("launcher calls = %d, want 4 (one on tick 1, three more once the share rose): %q",
			len(l.calls), l.calls)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) < 2 {
		t.Fatalf("stdout has %d lines, want a line per tick: %q", len(lines), out.String())
	}
	if want := "FILL tick=1 bench-a:launched=1,failed=0 ready=4"; lines[0] != want {
		t.Fatalf("tick 1 = %q, want %q", lines[0], want)
	}
	if want := "FILL tick=2 bench-a:launched=3,failed=0 ready=1"; lines[1] != want {
		t.Fatalf("tick 2 did not take up the raised share: %q, want %q", lines[1], want)
	}
	if p.calls["bench-a"] < 2 {
		t.Fatalf("the store was probed %d times over two ticks, want one read per tick",
			p.calls["bench-a"])
	}
}

// TestFillLoadBrakeDealsNothingAndNeverShrinksBelowTheLiveLeases: the load is a brake, not
// the size. A bench over the per-core guard is dealt nothing this tick and says so by name,
// with the free count it did not fill on the line -- and the cards already live on it are
// not touched, not counted against it and not taken back. A brake that "shrinks" a bench
// below its live leases would be asking the bench to drop work it is already doing.
func TestFillLoadBrakeDealsNothingAndNeverShrinksBelowTheLiveLeases(t *testing.T) {
	dir := t.TempDir()
	ready, launched := filepath.Join(dir, "ready"), filepath.Join(dir, "launched")
	for i := 1; i <= 3; i++ {
		writeCard(t, ready, fmt.Sprintf("card-%03d.md", i), "a card\n")
	}
	// Ten live cards already on the bench: they are what made the load.
	for i := 1; i <= 10; i++ {
		writeCard(t, launched, fmt.Sprintf("card-live-%03d.md", i), "a live card\n")
	}
	// 64 cores, load1 200 = 3.12 per core, over the 1.5 guard, with 54 free slots.
	p := &storeProbe{lines: map[string]string{"bench-a": storeLine(64, 10, 64, 200)}}
	l := &laneLauncher{}
	var out, errb bytes.Buffer
	code := Fill(FillInput{
		Ready: ready, Launched: launched,
		Machines: machinesFile(t, dir, []string{"bench-a"}, nil),
		Benches:  []string{"bench-a"},
		Once:     true,
		Stdout:   &out,
		Stderr:   &errb,
		Capacity: StoreCapacity{Probe: p.probe, Owner: testOwner, MaxLoadPerCore: 1.5, Stderr: &errb},
		Launcher: l,
	})
	if code != 0 {
		t.Fatalf("fill exit = %d, want 0 (a braked bench is not a failed bench); stderr=%q", code, errb.String())
	}
	if len(l.calls) != 0 {
		t.Fatalf("launcher calls = %d, want 0 under the brake: %q", len(l.calls), l.calls)
	}
	if got := len(readyCards(ready)); got != 3 {
		t.Fatalf("ready holds %d cards, want 3 (nothing was claimed)", got)
	}
	if got := len(readyCards(launched)); got != 10 {
		t.Fatalf("launched holds %d cards, want the 10 live ones, untouched", got)
	}
	said := errb.String()
	for _, want := range []string{"FILL BRAKE", "bench=bench-a", "per-core=3.12", "max=1.50", "free=54", "held=10"} {
		if !strings.Contains(said, want) {
			t.Fatalf("the brake line does not carry %q: %q", want, said)
		}
	}
	if strings.Contains(out.String(), "launched=1") {
		t.Fatalf("a braked bench launched a card: %q", out.String())
	}
}

// TestFillBrakeIsOffWhenTheGuardIsZero: --max-load-per-core 0 is no brake at all, which is
// what a bench whose load is made by something other than its leases needs (the Studio ran
// at 3.2 per core with four leases held).
func TestFillBrakeIsOffWhenTheGuardIsZero(t *testing.T) {
	p := &storeProbe{lines: map[string]string{"bench-a": storeLine(64, 10, 64, 200)}}
	var errb bytes.Buffer
	n, err := StoreCapacity{Probe: p.probe, Owner: testOwner, MaxLoadPerCore: 0, Stderr: &errb}.Capacity("bench-a")
	if err != nil {
		t.Fatalf("capacity: %v", err)
	}
	if n != 54 {
		t.Fatalf("capacity = %d, want 54 (the store's free count, no brake)", n)
	}
	if strings.Contains(errb.String(), "FILL BRAKE") {
		t.Fatalf("a zero guard still braked: %q", errb.String())
	}
}

// TestStoreCapacityFallsBackToTheFormulaWhereThereIsNoStore: a bench with no slot store --
// one that has not been given one yet -- still answers the number it always answered, so
// adopting the store is not a flag day.
func TestStoreCapacityFallsBackToTheFormulaWhereThereIsNoStore(t *testing.T) {
	p := &storeProbe{lines: map[string]string{"bench-a": "formula capacity=12 cores=64 load1=76.00"}}
	n, err := StoreCapacity{Probe: p.probe, Owner: testOwner, MaxLoadPerCore: 1.5}.Capacity("bench-a")
	if err != nil {
		t.Fatalf("capacity: %v", err)
	}
	if n != 12 {
		t.Fatalf("capacity = %d, want the formula's 12", n)
	}
}

// TestStoreCapacityRefusesAnAnswerItCannotRead: a probe that answers something else is a
// refusal naming what it said, never a guessed capacity. A guessed free count puts a card
// on a full machine.
func TestStoreCapacityRefusesAnAnswerItCannotRead(t *testing.T) {
	for _, line := range []string{"", "12",
		"store share=x cores=8 load1=1\nleases\n",
		"store cores=8 load1=1\nleases\n",
		"store share=4 cores=8 load1=1",
		"store share=4 cores=8 load1=NaN\nleases\n",
		"store share=4 cores=8 load1=1\nleases\nwhat is this row\n",
		"unreadable reason=slots-list-exit rc=127",
	} {
		p := &storeProbe{lines: map[string]string{"bench-a": line}}
		n, err := StoreCapacity{Probe: p.probe, Owner: testOwner}.Capacity("bench-a")
		if err == nil {
			t.Fatalf("the probe answered %q and the capacity was %d, want a refusal", line, n)
		}
		if n != 0 {
			t.Fatalf("a refused read answered capacity %d, want 0", n)
		}
	}
}

// TestStoreCapacityRefusesMoreHeldThanTheShareAllows: an owner cannot hold more leases than
// its share. A reading that says it did is a reading that went wrong, and the answer is a
// refusal with a reason -- fail closed and loudly -- never a share-minus-held for a later
// clamp to catch. Free() still floors at zero for anything that reaches it.
func TestStoreCapacityRefusesMoreHeldThanTheShareAllows(t *testing.T) {
	p := &storeProbe{lines: map[string]string{"bench-a": storeLine(8, 20, 64, 1)}}
	n, err := StoreCapacity{Probe: p.probe, Owner: testOwner}.Capacity("bench-a")
	if err == nil {
		t.Fatalf("held above share answered capacity %d, want a refusal", n)
	}
	if n != 0 {
		t.Fatalf("a refused read answered capacity %d, want 0", n)
	}
	if got := (CapacityAnswer{FromStore: true, Share: 8, Held: 20}).Free(); got != 0 {
		t.Fatalf("Free() = %d, want 0 (never negative)", got)
	}
}
