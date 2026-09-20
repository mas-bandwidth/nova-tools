package pulse

// fillfair_test.go is the red test for #2008: the deal is a SHARE OF FREE CAPACITY, and it
// is settled before any launcher runs.
//
// The load test of 2026-09-20 (peak 501 live cards on seven machines) found the defect the
// hard way: N fill loops read ONE ready directory and the claim was the rename, so the bench
// whose launcher answered first took the cards. The MacBook Air, 93 ms away over the
// tailnet, sat at 0/14 for an hour with a full queue while the LAN benches ate. Glenn:
// "everybody should get busy, not just the low ping bastards."
//
// Two rules follow, and each one has a test here:
//
//  1. PROPORTION. A tick's cards are split across the benches with room in proportion to
//     each bench's FREE slots, with a floor of one card for every bench that has room, and
//     the floor is handed out smallest-bench-first so a big bench cannot drain the pool
//     before a small one has eaten. Round-robin (#1483) gave every bench an EQUAL share,
//     which starves a bench with ten free slots beside one with two.
//
//  2. THE DEAL DOES NOT WAIT ON A LAUNCHER. Every card the tick deals is claimed -- moved
//     out of --ready -- before the first launcher is called, so a bench whose launcher takes
//     a second to answer cannot lose its share to a bench whose launcher answers at once.
//     That is the property the latency starvation actually violated.

import (
	"bytes"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fairCap answers a fixed number of free slots per bench.
type fairCap map[string]int

func (c fairCap) Capacity(bench string) (int, error) { return c[bench], nil }

// fairLauncher counts what each bench took, and records how many cards were still sitting in
// --ready at the moment the FIRST launcher call was made: the receipt that the deal was
// settled before any bench answered. One named bench answers slowly, which is the whole
// simulation -- a high-latency bench, in the shape the fleet met.
type fairLauncher struct {
	mu        sync.Mutex
	ready     string        // the ready directory, read at the first launch
	slow      string        // the bench that answers slowly; "" for none
	delay     time.Duration // how slowly it answers
	took      map[string]int
	order     []string
	firstSeen bool
	readyLeft int // cards still in --ready when the first launch was called
}

func newFairLauncher(ready, slow string, delay time.Duration) *fairLauncher {
	return &fairLauncher{ready: ready, slow: slow, delay: delay, took: map[string]int{}}
}

func (l *fairLauncher) Launch(bench, seat, card string) error {
	l.mu.Lock()
	if !l.firstSeen {
		l.firstSeen = true
		l.readyLeft = len(readyCards(l.ready))
	}
	l.took[bench]++
	l.order = append(l.order, bench+" "+filepath.Base(card))
	slow, delay := l.slow, l.delay
	l.mu.Unlock()
	if bench == slow && delay > 0 {
		time.Sleep(delay)
	}
	return nil
}

// fairFill runs one tick over n cards with the given free-slot counts and returns what each
// bench took, plus the launcher, so a test can read its other receipts.
func fairFill(t *testing.T, n int, free map[string]int, slow string, delay time.Duration) *fairLauncher {
	t.Helper()
	dir := t.TempDir()
	ready, launched := filepath.Join(dir, "ready"), filepath.Join(dir, "launched")
	for i := 1; i <= n; i++ {
		writeCard(t, ready, fmt.Sprintf("card-%03d.md", i), "a card\n")
	}
	names := make([]string, 0, len(free))
	for name := range free {
		names = append(names, name)
	}
	// The registry's own order is not the deal's order; sorting keeps the fixture stable.
	sortStrings(names)
	l := newFairLauncher(ready, slow, delay)
	var out, errb bytes.Buffer
	code := Fill(FillInput{
		Ready: ready, Launched: launched,
		Machines: machinesFile(t, dir, names, nil),
		Benches:  names,
		Once:     true,
		Stdout:   &out,
		Stderr:   &errb,
		Capacity: fairCap(free),
		Launcher: l,
	})
	if code != 0 {
		t.Fatalf("fill exit = %d, want 0; stderr=%q", code, errb.String())
	}
	return l
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

// TestFillDealsInProportionToFreeSlots: three benches with 2, 3 and 10 free slots and a pool
// of exactly their sum each take their own free count -- not a third each. The same fixture
// with a pool smaller than the fleet's capacity still splits by proportion, and the two small
// benches keep their floor of one.
func TestFillDealsInProportionToFreeSlots(t *testing.T) {
	free := map[string]int{"small": 2, "mid": 3, "big": 10}

	t.Run("a pool the size of the fleet's free capacity", func(t *testing.T) {
		l := fairFill(t, 15, free, "", 0)
		for bench, want := range map[string]int{"small": 2, "mid": 3, "big": 10} {
			if l.took[bench] != want {
				t.Errorf("%s took %d cards, want %d (free slots: %v, pool 15); took=%v",
					bench, l.took[bench], want, free, l.took)
			}
		}
	})

	t.Run("a pool smaller than the fleet's free capacity", func(t *testing.T) {
		// 6 cards over 15 free slots: the exact proportions are 0.8, 1.2 and 4.0, so the
		// floor of one carries the two small benches and the big bench takes the rest.
		l := fairFill(t, 6, free, "", 0)
		for bench, want := range map[string]int{"small": 1, "mid": 1, "big": 4} {
			if l.took[bench] != want {
				t.Errorf("%s took %d cards, want %d (free slots: %v, pool 6); took=%v",
					bench, l.took[bench], want, free, l.took)
			}
		}
	})
}

// TestFillGivesEveryBenchWithRoomAtLeastOneCard: the floor. A bench with one free slot beside
// a bench with a hundred is not rounded to nothing -- #2008's first hand-built defect, where
// rounding in list order starved the benches at the end of the list.
func TestFillGivesEveryBenchWithRoomAtLeastOneCard(t *testing.T) {
	l := fairFill(t, 4, map[string]int{"tiny": 1, "huge": 100}, "", 0)
	if l.took["tiny"] != 1 {
		t.Errorf("the bench with one free slot took %d cards, want 1; took=%v", l.took["tiny"], l.took)
	}
	if l.took["huge"] != 3 {
		t.Errorf("the bench with a hundred free slots took %d cards, want 3; took=%v", l.took["huge"], l.took)
	}
}

// TestFillDealsToASlowBenchItsProportionalShare is the load test's own shape: two benches
// with the same free capacity, one of which answers its launcher slowly. The slow bench must
// take half the pool, and every card must already be claimed out of --ready when the first
// launcher call is made -- the deal cannot be a race the fast bench wins.
func TestFillDealsToASlowBenchItsProportionalShare(t *testing.T) {
	l := fairFill(t, 12, map[string]int{"fast": 6, "slow": 6}, "slow", 20*time.Millisecond)
	if l.took["slow"] != 6 || l.took["fast"] != 6 {
		t.Errorf("slow bench took %d cards and the fast bench %d, want 6 and 6; order=%s",
			l.took["slow"], l.took["fast"], strings.Join(l.order, " "))
	}
	if l.readyLeft != 0 {
		t.Errorf("%d cards were still in --ready when the first launcher was called, want 0: "+
			"the deal must be settled before any bench answers, or the fastest launcher takes the pool", l.readyLeft)
	}
}

// TestFairShares is the arithmetic on its own, including the two defects the hand-built
// dealer met on the night: rounding that starved the tail of the list, and a share bigger
// than the bench's own free count.
func TestFairShares(t *testing.T) {
	for _, tc := range []struct {
		name string
		free []int
		n    int
		want []int
	}{
		{"nothing to deal", []int{4, 4}, 0, []int{0, 0}},
		{"no room anywhere", []int{0, 0}, 5, []int{0, 0}},
		{"a bench with no room takes nothing", []int{0, 5}, 3, []int{0, 3}},
		{"exactly the free capacity", []int{2, 3, 10}, 15, []int{2, 3, 10}},
		{"more cards than capacity", []int{2, 3}, 100, []int{2, 3}},
		{"the floor of one", []int{1, 100}, 4, []int{1, 3}},
		{"one card, smallest bench first", []int{1, 100}, 1, []int{1, 0}},
		{"the tail of the list is not the one that pays", []int{3, 3, 3}, 4, []int{2, 1, 1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := fairShares(tc.free, tc.n)
			if len(got) != len(tc.want) {
				t.Fatalf("fairShares(%v, %d) = %v, want %v", tc.free, tc.n, got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("fairShares(%v, %d) = %v, want %v", tc.free, tc.n, got, tc.want)
				}
			}
			total := 0
			for i, k := range got {
				if k > tc.free[i] {
					t.Fatalf("bench %d was dealt %d cards with %d free slots: %v", i, k, tc.free[i], got)
				}
				total += k
			}
			if total > tc.n {
				t.Fatalf("dealt %d cards out of a pool of %d: %v", total, tc.n, got)
			}
		})
	}
}
