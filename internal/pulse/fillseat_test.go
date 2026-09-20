package pulse

// fillseat_test.go is the red test for #2014: the seat a card runs under is the REGISTRY'S,
// not a name fill makes up.
//
// `fill` handed every launcher the seat `swarm-<bench>`. The Studio's seat in the secrets
// store is `studio` (studio.yaml, studio.key), so on the night of the 2026-09-20 load test
// every card dealt to the strongest bench in the fleet died at
// `SECRETS EXEC FAIL store file .../swarm-studio.yaml is absent` (exit 125) and bounced back
// into the queue, robbing the other benches of those cards on every tick. The MacBook Air's
// seat is `air`, for the same reason. The registry's seat column already said both.
//
// And the refusal is a refusal AT THE LOOP, not per card: a bench whose row names no seat is
// named once, before the first card is dealt, and dropped from the pool. Refusing per card
// is what turned one wrong argument into a queue-wide denial of service.

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// seatLauncher records `<bench> <seat> <card>` per launch.
type seatLauncher struct {
	mu    sync.Mutex
	calls []string
}

func (l *seatLauncher) Launch(bench, seat, card string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls = append(l.calls, bench+" "+seat+" "+filepath.Base(card))
	return nil
}

// seatMachines writes a registry whose rows carry the seats the test names: `name=seat`, and
// a bare name for a bench whose row carries no seat at all (`-`).
func seatMachines(t *testing.T, dir string, rows ...string) string {
	t.Helper()
	var b strings.Builder
	b.WriteString("# name\tssh\tos/arch\troles\tseat\tcores\tnotes\n")
	for _, row := range rows {
		name, seat, ok := strings.Cut(row, "=")
		if !ok {
			seat = "-"
		}
		b.WriteString(name + "\t" + name + "\tlinux/x64\tbench\t" + seat + "\t64\t-\n")
	}
	path := filepath.Join(dir, "machines.tsv")
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestFillPassesTheRegistrySeatNotSwarmBench: the Studio's row says `studio`, so the card
// goes out under `studio`; the MacBook's says `air`; a bench whose row happens to say
// `swarm-hulk` still gets `swarm-hulk`, because the row is the answer either way.
func TestFillPassesTheRegistrySeatNotSwarmBench(t *testing.T) {
	dir := t.TempDir()
	ready, launched := filepath.Join(dir, "ready"), filepath.Join(dir, "launched")
	for i := 1; i <= 3; i++ {
		writeCard(t, ready, fmt.Sprintf("card-%03d.md", i), "a card\n")
	}
	l := &seatLauncher{}
	var out, errb bytes.Buffer
	code := Fill(FillInput{
		Ready: ready, Launched: launched,
		Machines: seatMachines(t, dir, "studio=studio", "macbook=air", "hulk=swarm-hulk"),
		Benches:  []string{"studio", "macbook", "hulk"},
		Once:     true,
		Stdout:   &out, Stderr: &errb,
		Capacity: laneCap{"studio": 4, "macbook": 4, "hulk": 4}, Launcher: l,
	})
	if code != 0 {
		t.Fatalf("fill exit = %d, want 0; stderr=%q", code, errb.String())
	}
	seats := map[string]string{}
	for _, call := range l.calls {
		f := strings.Fields(call)
		seats[f[0]] = f[1]
	}
	for bench, want := range map[string]string{"studio": "studio", "macbook": "air", "hulk": "swarm-hulk"} {
		if seats[bench] != want {
			t.Errorf("%s ran under seat %q, want %q; calls=%v", bench, seats[bench], want, l.calls)
		}
	}
}

// TestFillRefusesASeatlessBenchOnceByNameAndFillsTheRest: a bench whose row names no seat is
// refused ONCE, by name, before any card is dealt. Its neighbours keep working -- the whole
// fleet is not stopped by one incomplete row -- and the refusal does not repeat per card.
func TestFillRefusesASeatlessBenchOnceByNameAndFillsTheRest(t *testing.T) {
	dir := t.TempDir()
	ready, launched := filepath.Join(dir, "ready"), filepath.Join(dir, "launched")
	for i := 1; i <= 6; i++ {
		writeCard(t, ready, fmt.Sprintf("card-%03d.md", i), "a card\n")
	}
	l := &seatLauncher{}
	var out, errb bytes.Buffer
	code := Fill(FillInput{
		Ready: ready, Launched: launched,
		Machines: seatMachines(t, dir, "seatless", "hulk=swarm-hulk"),
		Benches:  []string{"seatless", "hulk"},
		Once:     true,
		Stdout:   &out, Stderr: &errb,
		Capacity: laneCap{"seatless": 10, "hulk": 10}, Launcher: l,
	})
	if code != 0 {
		t.Fatalf("fill exit = %d, want 0; stderr=%q", code, errb.String())
	}
	if n := strings.Count(errb.String(), "bench=seatless"); n != 1 {
		t.Errorf("the seatless bench was named %d times, want exactly 1 (once at the loop, not once per card):\n%s", n, errb.String())
	}
	if !strings.Contains(errb.String(), "FILL REFUSED") {
		t.Errorf("no refusal line for the seatless bench:\n%s", errb.String())
	}
	for _, call := range l.calls {
		if strings.HasPrefix(call, "seatless ") {
			t.Fatalf("a card was launched on the seatless bench: %v", l.calls)
		}
	}
	if len(l.calls) != 6 {
		t.Errorf("the fleet launched %d of 6 cards; the bench beside the refused one must keep working: %v", len(l.calls), l.calls)
	}
}

// TestFillRefusesWhenNoNamedBenchHasASeat: with every named bench seatless there is nothing
// to fill, and that is a refusal that starts nothing -- exit 2, no card moved.
func TestFillRefusesWhenNoNamedBenchHasASeat(t *testing.T) {
	dir := t.TempDir()
	ready, launched := filepath.Join(dir, "ready"), filepath.Join(dir, "launched")
	writeCard(t, ready, "card-001.md", "a card\n")
	l := &seatLauncher{}
	var out, errb bytes.Buffer
	code := Fill(FillInput{
		Ready: ready, Launched: launched,
		Machines: seatMachines(t, dir, "seatless"),
		Benches:  []string{"seatless"},
		Once:     true,
		Stdout:   &out, Stderr: &errb,
		Capacity: laneCap{"seatless": 10}, Launcher: l,
	})
	if code != 2 {
		t.Fatalf("fill exit = %d, want 2; stderr=%q", code, errb.String())
	}
	if len(l.calls) != 0 {
		t.Fatalf("a card was launched with no seat to run it under: %v", l.calls)
	}
	if _, err := os.Stat(filepath.Join(ready, "card-001.md")); err != nil {
		t.Errorf("the card left --ready on a run that refused to start: %v", err)
	}
}
