package main

// fill-loop.sh's tick body as a verb (#1142): every tick, for each bench, read the
// bench's capacity, cap it, pop that many card-*.md from --ready in filename order,
// move each to --launched and launch it. The seams -- capacity and the per-card
// launcher -- are injected, so no test opens an ssh connection or spawns a process.

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/pulse"
)

// fillCapStub answers a fixed capacity per bench.
type fillCapStub map[string]int

func (f fillCapStub) Capacity(bench string) (int, error) { return f[bench], nil }

// fillLaunchStub records one line per launched card, bench then card.
type fillLaunchStub struct{ calls []string }

func (l *fillLaunchStub) Launch(bench, card string) error {
	l.calls = append(l.calls, bench+" "+card)
	return nil
}

// fillMachines writes a machines registry naming the test's benches as benches, so the
// fill's lock guard (runner hosts are CI-only) has something to resolve them through.
func fillMachines(t *testing.T, dir string, names ...string) string {
	t.Helper()
	var b strings.Builder
	for _, name := range names {
		b.WriteString(name + "\t" + name + "\tlinux/x64\tbench\tswarm-" + name + "\t64\t-\n")
	}
	path := filepath.Join(dir, "machines.tsv")
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func fillReady(t *testing.T, dir string, n int) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= n; i++ {
		writeMainFile(t, dir, fmt.Sprintf("card-%03d.md", i), dealtCard)
	}
}

func fillCount(t *testing.T, dir string) int {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(dir, "card-*.md"))
	if err != nil {
		t.Fatal(err)
	}
	return len(matches)
}

// TestFillLaunchesCapacityPerBench: bench-a takes 3, bench-b takes 2, the rest stay ready.
// The FILL line names every bench and the remaining ready count.
func TestFillLaunchesCapacityPerBench(t *testing.T) {
	dir := t.TempDir()
	ready, launched := filepath.Join(dir, "ready"), filepath.Join(dir, "launched")
	fillReady(t, ready, 8)
	cap := fillCapStub{"bench-a": 3, "bench-b": 2}
	l := &fillLaunchStub{}
	var out, errb bytes.Buffer
	code := pulse.Fill(pulse.FillInput{
		Ready: ready, Launched: launched,
		Machines: fillMachines(t, dir, "bench-a", "bench-b"),
		Benches:  []string{"bench-a", "bench-b"},
		Once:     true,
		Stdout:   &out,
		Stderr:   &errb,
		Capacity: cap,
		Launcher: l,
	})
	if code != 0 {
		t.Fatalf("fill exit = %d, want 0; stderr=%q", code, errb.String())
	}
	if len(l.calls) != 5 {
		t.Fatalf("launcher calls = %d, want 5: %q", len(l.calls), l.calls)
	}
	// Oldest first, bench-a before bench-b.
	wantFirst := "bench-a " + filepath.Join(launched, "card-001.md")
	if l.calls[0] != wantFirst {
		t.Fatalf("first launch = %q, want %q", l.calls[0], wantFirst)
	}
	if got := fillCount(t, launched); got != 5 {
		t.Fatalf("launched holds %d cards, want 5", got)
	}
	if got := fillCount(t, ready); got != 3 {
		t.Fatalf("ready holds %d cards, want 3", got)
	}
	line := strings.TrimSpace(out.String())
	want := "FILL tick=1 bench-a:launched=3,failed=0 bench-b:launched=2,failed=0 ready=3"
	if line != want {
		t.Fatalf("FILL line = %q, want %q", line, want)
	}
}

// TestFillCapsAtThirty: a bench whose formula allows one hundred cards never takes more
// than thirty in a tick -- the CI reserve fill-loop.sh holds back.
func TestFillCapsAtThirty(t *testing.T) {
	dir := t.TempDir()
	ready, launched := filepath.Join(dir, "ready"), filepath.Join(dir, "launched")
	fillReady(t, ready, 40)
	l := &fillLaunchStub{}
	var out, errb bytes.Buffer
	code := pulse.Fill(pulse.FillInput{
		Ready: ready, Launched: launched,
		Machines: fillMachines(t, dir, "bench-x"),
		Benches:  []string{"bench-x"},
		Once:     true,
		Stdout:   &out,
		Stderr:   &errb,
		Capacity: fillCapStub{"bench-x": 100},
		Launcher: l,
	})
	if code != 0 {
		t.Fatalf("fill exit = %d, want 0; stderr=%q", code, errb.String())
	}
	if len(l.calls) != 30 {
		t.Fatalf("launcher calls = %d, want 30 (the cap)", len(l.calls))
	}
	line := strings.TrimSpace(out.String())
	want := "FILL tick=1 bench-x:launched=30,failed=0 ready=10"
	if line != want {
		t.Fatalf("FILL line = %q, want %q", line, want)
	}
}

// TestFillNeverLaunchesACardTwice: the move out of ready is the claim; a second tick sees
// an empty ready and launches nothing.
func TestFillNeverLaunchesACardTwice(t *testing.T) {
	dir := t.TempDir()
	ready, launched := filepath.Join(dir, "ready"), filepath.Join(dir, "launched")
	fillReady(t, ready, 4)
	l := &fillLaunchStub{}
	in := pulse.FillInput{
		Ready: ready, Launched: launched,
		Machines: fillMachines(t, dir, "bench-a"),
		Benches:  []string{"bench-a"},
		Once:     true,
		Capacity: fillCapStub{"bench-a": 100},
		Launcher: l,
	}
	var out1, errb1 bytes.Buffer
	in.Stdout, in.Stderr = &out1, &errb1
	if code := pulse.Fill(in); code != 0 {
		t.Fatalf("first fill exit = %d, stderr=%q", code, errb1.String())
	}
	var out2, errb2 bytes.Buffer
	in.Stdout, in.Stderr = &out2, &errb2
	if code := pulse.Fill(in); code != 0 {
		t.Fatalf("second fill exit = %d, stderr=%q", code, errb2.String())
	}
	if len(l.calls) != 4 {
		t.Fatalf("launcher calls = %d, want 4 (no card launched twice): %q", len(l.calls), l.calls)
	}
	want := "FILL tick=1 bench-a:launched=0,failed=0 ready=0"
	if line := strings.TrimSpace(out2.String()); line != want {
		t.Fatalf("second FILL line = %q, want %q", line, want)
	}
}

// The dogfood edges of 2026-09-18 at the command's own edge: a capacity reader that threw
// the reason away, and a lane logic no hand could reach without a live bench.

// TestSSHCapacityCarriesTheChildsLastLine: `exit status 255` alone says nothing. The last
// line of the child's stderr is kept, bounded, and joined to the exit status.
func TestSSHCapacityCarriesTheChildsLastLine(t *testing.T) {
	specs := fakePATH(t)
	fakeTool(t, specs, "ssh", fakeSpec{Default: fakeRule{
		Stderr: "ssh: connect to host bench-x port 22: Connection refused",
		Exit:   255,
	}})
	_, err := sshCapacity{}.Capacity("bench-x")
	if err == nil {
		t.Fatal("capacity over a refused connection answered no error")
	}
	if !strings.Contains(err.Error(), "255") {
		t.Errorf("the error does not carry the exit status: %q", err)
	}
	if !strings.Contains(err.Error(), "Connection refused") {
		t.Errorf("the error does not carry the child's last line: %q", err)
	}
}

// TestTailKeepsTheLastLineBounded: a child that prints a megabyte costs the tail and no
// more, and the last non-empty line is what is kept.
func TestTailKeepsTheLastLineBounded(t *testing.T) {
	var said tail
	fmt.Fprint(&said, strings.Repeat("noise\n", 100000))
	fmt.Fprint(&said, "the reason\n\n")
	if len(said.buf) > tailBytes {
		t.Fatalf("the tail holds %d bytes, want at most %d", len(said.buf), tailBytes)
	}
	if got := said.lastLine(); got != "the reason" {
		t.Fatalf("lastLine = %q, want the last non-empty line", got)
	}
}

// dealtCard is a card as the dealer leaves it in a ready queue: its route picked and on it.
const dealtCard = "a card\nROUTE: test-route\nMODEL: test-model\n"

// TestFillNeverHoldsOnDependsOn (#3251): the verb, as the bench loops run it (--capacity,
// --launcher, --lanes), launches every ready card -- `DEPENDS-ON: -`, a DEPENDS-ON parent
// that never landed, a LEG no bench was said to carry, two cards on one lane -- and prints
// no HELD line. Readiness was the dealer's, before the cards entered ready. On dev at
// fdffd7968 the unlanded parent is `FILL HELD ... reason=dependency ... not merged into dev`
// and the second lane card is `FILL HELD ... lane=pulse`.
func TestFillNeverHoldsOnDependsOn(t *testing.T) {
	specs := fakePATH(t)
	dir := t.TempDir()
	log := filepath.Join(dir, "launcher.log")
	fakeTool(t, specs, "nova-swarm", fakeSpec{Log: log, Default: fakeRule{Exit: 0}})
	ready, launched := filepath.Join(dir, "ready"), filepath.Join(dir, "launched")
	writeMainFile(t, ready, "card-001.md", "DEPENDS-ON: -\n"+dealtCard)
	writeMainFile(t, ready, "card-002.md", "DEPENDS-ON: card-999-never-landed\n"+dealtCard)
	writeMainFile(t, ready, "card-003.md", "LEG: squirrel\n"+dealtCard)
	writeMainFile(t, ready, "card-004.md", "LANE: pulse\n"+dealtCard)
	writeMainFile(t, ready, "card-005.md", "LANE: pulse\n"+dealtCard)
	lanes := writeMainFile(t, dir, "lanes.tsv", "pulse\tinternal/pulse/\n")

	var out, errb bytes.Buffer
	code := run([]string{"fill",
		"--ready", ready, "--launched", launched, "--lanes", lanes,
		"--machines", fillMachines(t, dir, "bench-a"),
		"--bench", "bench-a", "--capacity", "10", "--once", "--launch-grace", "0",
		"--launcher", filepath.Join(fakeBins(t), "nova-swarm"+exeSuffix()),
	}, &out, &errb, time.Now().UTC())
	if code != 0 {
		t.Fatalf("fill exit = %d, want 0; stderr=%q", code, errb.String())
	}
	if strings.Contains(out.String(), "HELD") || strings.Contains(errb.String(), "REFUSED") {
		t.Fatalf("the fill held or refused a ready card: stdout=%q stderr=%q", out.String(), errb.String())
	}
	if !strings.Contains(out.String(), "bench-a:launched=5,failed=0 ready=0") {
		t.Fatalf("want all five launched and ready=0: stdout=%q", out.String())
	}
	if fillCount(t, launched) != 5 || fillCount(t, ready) != 0 {
		t.Fatalf("launched=%d ready=%d, want 5 and 0", fillCount(t, launched), fillCount(t, ready))
	}
}

// TestFillVerbRefusesACardTheDealerDidNotRoute: the verb always guards the route. A card
// with no ROUTE: stays in ready, refused by name; the fill never picks one for it.
func TestFillVerbRefusesACardTheDealerDidNotRoute(t *testing.T) {
	specs := fakePATH(t)
	dir := t.TempDir()
	fakeTool(t, specs, "nova-swarm", fakeSpec{Default: fakeRule{Exit: 0}})
	ready, launched := filepath.Join(dir, "ready"), filepath.Join(dir, "launched")
	writeMainFile(t, ready, "card-001.md", "MODEL: test-model\n")
	var out, errb bytes.Buffer
	code := run([]string{"fill",
		"--ready", ready, "--launched", launched,
		"--machines", fillMachines(t, dir, "bench-a"),
		"--bench", "bench-a", "--capacity", "10", "--once", "--launch-grace", "0",
		"--launcher", filepath.Join(fakeBins(t), "nova-swarm"+exeSuffix()),
	}, &out, &errb, time.Now().UTC())
	if code != 0 {
		t.Fatalf("fill exit = %d, want 0; stderr=%q", code, errb.String())
	}
	if !strings.Contains(errb.String(), "FILL REFUSED card=card-001.md missing=ROUTE") {
		t.Fatalf("no refusal naming the missing ROUTE: %q", errb.String())
	}
	if fillCount(t, ready) != 1 || fillCount(t, launched) != 0 {
		t.Fatalf("ready=%d launched=%d, want the card left in ready", fillCount(t, ready), fillCount(t, launched))
	}
}

// TestRouteEnvCarriesTheDealtRoute: the launcher is handed the dealer's pick, never a pick
// of its own.
func TestRouteEnvCarriesTheDealtRoute(t *testing.T) {
	card := writeMainFile(t, t.TempDir(), "card-001.md", "KIND: fix\nROUTE: pro\nMODEL: opus\n")
	got := strings.Join(routeEnv(card), " ")
	if got != "NOVA_CARD_ROUTE=pro NOVA_CARD_MODEL=opus" {
		t.Fatalf("routeEnv = %q", got)
	}
}

// TestFillRefusesWithoutReadyAndLaunched: both directories are flags, so neither is guessed.
func TestFillRefusesWithoutReadyAndLaunched(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"fill"}, &out, &errb, time.Now().UTC()); code != 2 {
		t.Fatalf("fill with no flags exit = %d, want 2; stderr=%q", code, errb.String())
	}
	if !strings.Contains(errb.String(), "--ready is required") {
		t.Fatalf("refusal does not name --ready: %q", errb.String())
	}
	if !strings.Contains(errb.String(), "--launched is required") {
		t.Fatalf("refusal does not name --launched: %q", errb.String())
	}
}

// TestLaunchDoesNotWaitForTheCardToRun: the launcher runs the card, not just the start of
// it, and one tick blocked nine minutes launching three cards one after another (dogfood,
// 2026-09-18). The property, asserted without a clock: a child that fails only after the
// grace is up is already counted as launched, while the same child with no grace is waited
// for and its failure is seen.
func TestLaunchDoesNotWaitForTheCardToRun(t *testing.T) {
	specs := fakePATH(t)
	fakeTool(t, specs, "nova-swarm", fakeSpec{Default: fakeRule{SleepMS: 1500, Stderr: "late failure", Exit: 7}})
	bin := filepath.Join(fakeBins(t), "nova-swarm"+exeSuffix())
	card := filepath.Join(t.TempDir(), "card-001.md")

	if err := (flashLauncher{bin: bin, grace: time.Millisecond}).Launch("bench-a", card); err != nil {
		t.Fatalf("a launcher still running at the grace answered an error: %v", err)
	}
	err := flashLauncher{bin: bin, grace: 0}.Launch("bench-a", card)
	if err == nil {
		t.Fatal("with no grace the launcher is waited for; its failure was not seen")
	}
	if !strings.Contains(err.Error(), "late failure") {
		t.Fatalf("the waited launch did not carry the child's last line: %v", err)
	}
}

// TestLaunchStillCatchesAFailureInTheGrace: a launcher that fails, fails at once, and the
// card's return to --ready depends on that being noticed.
func TestLaunchStillCatchesAFailureInTheGrace(t *testing.T) {
	specs := fakePATH(t)
	fakeTool(t, specs, "nova-swarm", fakeSpec{Default: fakeRule{Stderr: "no such bench", Exit: 7}})
	l := flashLauncher{bin: filepath.Join(fakeBins(t), "nova-swarm"+exeSuffix()), grace: 10 * time.Second}
	err := l.Launch("bench-a", filepath.Join(t.TempDir(), "card-001.md"))
	if err == nil {
		t.Fatal("an exit-7 launcher answered no error")
	}
	if !strings.Contains(err.Error(), "no such bench") {
		t.Fatalf("the error does not carry the launcher's last line: %v", err)
	}
}

// TestLaunchPassesTheDeadlineFlag: 2400 was a number hardcoded in the script; it is the
// caller's now, and it reaches the launcher as the fifth argument.
func TestLaunchPassesTheDeadlineFlag(t *testing.T) {
	specs := fakePATH(t)
	dir := t.TempDir()
	log := filepath.Join(dir, "argv.log")
	fakeTool(t, specs, "nova-swarm", fakeSpec{Log: log, Default: fakeRule{Exit: 0}})
	ready, launched := filepath.Join(dir, "ready"), filepath.Join(dir, "launched")
	writeMainFile(t, ready, "card-001.md", dealtCard)
	var out, errb bytes.Buffer
	code := run([]string{"fill", "--ready", ready, "--launched", launched,
		"--machines", fillMachines(t, dir, "bench-a"),
		"--bench", "bench-a", "--capacity", "1", "--once", "--deadline", "600",
		"--launch-grace", "0",
		"--launcher", filepath.Join(fakeBins(t), "nova-swarm"+exeSuffix()),
	}, &out, &errb, time.Now().UTC())
	if code != 0 {
		t.Fatalf("fill --deadline exit = %d; stderr=%q", code, errb.String())
	}
	raw, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), " 600") {
		t.Fatalf("the launcher was not given the deadline: %q", raw)
	}
	if strings.Contains(string(raw), " 2400") {
		t.Fatalf("the launcher was given the hardcoded deadline: %q", raw)
	}
}
