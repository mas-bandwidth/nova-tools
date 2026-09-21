package pulse

// fillrobin_test.go holds the red test for #1483: nova-pulse fill must deal one card per
// bench in turn, the shape the live hand loop (fill-loop2.sh) has -- vision, hulk, space,
// round again -- instead of draining each bench to its capacity in list order. The drain
// shape is what put 34 cards on hulk while vision sat at load 0.4.

import (
	"bytes"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

// robinCap answers a fixed capacity per bench.
type robinCap map[string]int

func (c robinCap) Capacity(bench string) (int, error) { return c[bench], nil }

// robinLauncher records one "bench card" line per launch, in launch order.
type robinLauncher struct{ calls []string }

func (l *robinLauncher) Launch(bench, card string) error {
	l.calls = append(l.calls, bench+" "+filepath.Base(card))
	return nil
}

// robinReady writes n ready cards named card-001.md .. card-00n.md, which sort in that
// known order.
func robinReady(t *testing.T, dir string, n int) {
	t.Helper()
	for i := 1; i <= n; i++ {
		writeCard(t, dir, fmt.Sprintf("card-%03d.md", i), "a card\n")
	}
}

// TestFillDealsOneCardPerBenchInTurn: with three benches and a pool deeper than any one
// bench's capacity, fill must place one card per bench per pass -- b1, b2, b3, then again
// -- never draining one bench before its neighbour is offered a card. The whole observed
// sequence is in the failure, so a reader sees the drain shape and not just the first
// mismatch.
func TestFillDealsOneCardPerBenchInTurn(t *testing.T) {
	t.Run("three benches nine cards", func(t *testing.T) {
		dir := t.TempDir()
		ready, launched := filepath.Join(dir, "ready"), filepath.Join(dir, "launched")
		robinReady(t, ready, 9)
		l := &robinLauncher{}
		var out, errb bytes.Buffer
		code := Fill(FillInput{
			Ready: ready, Launched: launched,
			Machines: machinesFile(t, dir, []string{"b1", "b2", "b3"}, nil),
			Benches:  []string{"b1", "b2", "b3"},
			Once:     true,
			Stdout:   &out,
			Stderr:   &errb,
			Capacity: robinCap{"b1": 10, "b2": 10, "b3": 10},
			Launcher: l,
		})
		if code != 0 {
			t.Fatalf("fill exit = %d, want 0; stderr=%q", code, errb.String())
		}
		want := strings.Join([]string{
			"b1 card-001.md", "b2 card-002.md", "b3 card-003.md",
			"b1 card-004.md", "b2 card-005.md", "b3 card-006.md",
			"b1 card-007.md", "b2 card-008.md", "b3 card-009.md",
		}, " ")
		if got := strings.Join(l.calls, " "); got != want {
			t.Fatalf("deal sequence:\n got: %s\nwant: %s", got, want)
		}
	})

	// Three benches at capacities 1, 2 and 5 with four cards: every bench gets its first
	// card in pass one, the bench at capacity one takes no more, and the fourth card goes
	// to the next bench with room in pass two. Both the sequence and the per-bench totals
	// must hold.
	t.Run("capacities one two five", func(t *testing.T) {
		dir := t.TempDir()
		ready, launched := filepath.Join(dir, "ready"), filepath.Join(dir, "launched")
		robinReady(t, ready, 4)
		l := &robinLauncher{}
		var out, errb bytes.Buffer
		code := Fill(FillInput{
			Ready: ready, Launched: launched,
			Machines: machinesFile(t, dir, []string{"b1", "b2", "b3"}, nil),
			Benches:  []string{"b1", "b2", "b3"},
			Once:     true,
			Stdout:   &out,
			Stderr:   &errb,
			Capacity: robinCap{"b1": 1, "b2": 2, "b3": 5},
			Launcher: l,
		})
		if code != 0 {
			t.Fatalf("fill exit = %d, want 0; stderr=%q", code, errb.String())
		}
		want := "b1 card-001.md b2 card-002.md b3 card-003.md b2 card-004.md"
		if got := strings.Join(l.calls, " "); got != want {
			t.Fatalf("deal sequence:\n got: %s\nwant: %s", got, want)
		}
		totals := map[string]int{}
		for _, call := range l.calls {
			bench, _, _ := strings.Cut(call, " ")
			totals[bench]++
		}
		for bench, n := range map[string]int{"b1": 1, "b2": 2, "b3": 1} {
			if totals[bench] != n {
				t.Fatalf("bench %s took %d cards, want %d; calls=%v", bench, totals[bench], n, l.calls)
			}
		}
	})
}
