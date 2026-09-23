package main

// TestIssue2381 reproduces nova-tools#2381 as its title states it: nova-pulse fill -- a
// launcher refusal (capacity, disk, unreachable; rc 2 before any process) is HANDED BACK
// to the queue, never raised as an UNKNOWN start.
//
// The break, on the 2026-09-20 load test: 191 cards were raised UNKNOWN in twenty-eight
// minutes, every one a launcher line like `LAUNCH REFUSED space card-...: CAPACITY
// cores=32 load=66 free=193G memfree=78G allowed=-22` followed by `LAUNCHER EXIT rc=2`.
// Nothing had started; the card was simply dropped and had to be requeued by hand.
//
// A refusal is slow on exactly the bench that causes it: the launcher learns "over
// capacity" or "unreachable" over an ssh that the overloaded bench answers late, so the
// rc 2 lands AFTER the launch grace. A fill that takes "still running at the grace" as
// "launched" counts the card launched with no job behind it -- the drop the loop then
// raised UNKNOWN. The fill owes the launcher's contract one refusal window: a child that
// exits inside it reports its code exactly as an in-grace exit (rc 2 is handed back);
// only a child still running when the window closes has launched.

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestIssue2381(t *testing.T) {
	specs := fakePATH(t)
	dir := t.TempDir()
	// The launcher is a bench at capacity: it answers its own ssh past the launch
	// grace, then refuses before any process -- rc 2, the contract's code.
	fakeTool(t, specs, "nova-swarm", fakeSpec{Default: fakeRule{
		SleepMS: 1400,
		Stderr:  "LAUNCH REFUSED bench-a card-001: CAPACITY cores=32 load=66 free=193G memfree=78G allowed=-22",
		Exit:    2,
	}})
	ready, launched := filepath.Join(dir, "ready"), filepath.Join(dir, "launched")
	writeMainFile(t, ready, "card-001.md", "a card\n")

	var out, errb bytes.Buffer
	_ = run([]string{"fill",
		"--ready", ready, "--launched", launched,
		"--machines", fillMachines(t, dir, "bench-a"),
		"--bench", "bench-a", "--capacity", "1", "--once",
		"--launch-grace", "1s",
		"--launcher", filepath.Join(fakeBins(t), "nova-swarm"+exeSuffix()),
	}, &out, &errb, time.Now().UTC())

	// HANDED BACK: the card is the queue's again, in ready, not dropped into launched
	// with nothing started behind it.
	if got := fillCount(t, ready); got != 1 {
		t.Errorf("a refused card is handed back to ready: ready holds %d cards, want 1 (stdout=%q stderr=%q)", got, out.String(), errb.String())
	}
	if got := fillCount(t, launched); got != 0 {
		t.Errorf("a refused card never counted as a start: launched holds %d cards, want 0 (stdout=%q stderr=%q)", got, out.String(), errb.String())
	}
	if strings.Contains(out.String(), "launched=1") {
		t.Errorf("the FILL line counted a refused card as launched: %q", out.String())
	}
	// A one-line reason in the loop log: the refusal names the card and carries the
	// launcher's own reason, so the next hand reads why.
	if !strings.Contains(errb.String(), "card-001.md") || !strings.Contains(errb.String(), "LAUNCH REFUSED") {
		t.Errorf("the loop log does not carry the refusal's one-line reason: %q", errb.String())
	}
}

// TestIssue2381RefusalAfterTwiceTheGrace is Stella's boundary on #2874: a refusal that lands
// after TWICE the launch grace is still a refusal. The first cut of #2381 waited the grace
// and then one more grace and took a launcher still running at that second deadline as
// launched, which only moved the boundary: the same bench, a little slower, and the card
// sat in launched/ with nothing behind it. A launch counts only on a positive record, so
// here the launcher refuses three graces late -- on STDOUT, where the shell launcher echoes
// its LAUNCH REFUSED lines -- and the card must still be handed back with the reason in the
// loop log. Nothing below asserts a clock: the fill waits for the launcher's answer, and
// the answer is a refusal whenever it comes.
func TestIssue2381RefusalAfterTwiceTheGrace(t *testing.T) {
	specs := fakePATH(t)
	dir := t.TempDir()
	fakeTool(t, specs, "nova-swarm", fakeSpec{Default: fakeRule{
		SleepMS: 1500,
		Stdout:  "LAUNCH REFUSED bench-a card-001: CAPACITY cores=32 load=90 free=193G memfree=78G allowed=-40",
		Exit:    2,
	}})
	ready, launched := filepath.Join(dir, "ready"), filepath.Join(dir, "launched")
	writeMainFile(t, ready, "card-001.md", "a card\n")

	var out, errb bytes.Buffer
	_ = run([]string{"fill",
		"--ready", ready, "--launched", launched,
		"--machines", fillMachines(t, dir, "bench-a"),
		"--bench", "bench-a", "--capacity", "1", "--once",
		"--launch-grace", "500ms",
		"--launcher", filepath.Join(fakeBins(t), "nova-swarm"+exeSuffix()),
	}, &out, &errb, time.Now().UTC())

	if got := fillCount(t, launched); got != 0 {
		t.Errorf("a refusal after twice the grace was counted as a start: launched holds %d cards, want 0 (stdout=%q stderr=%q)", got, out.String(), errb.String())
	}
	if got := fillCount(t, ready); got != 1 {
		t.Errorf("a refusal after twice the grace is handed back: ready holds %d cards, want 1 (stdout=%q stderr=%q)", got, out.String(), errb.String())
	}
	if strings.Contains(out.String(), "launched=1") {
		t.Errorf("the FILL line counted a late refusal as launched: %q", out.String())
	}
	if !strings.Contains(errb.String(), "LAUNCH REFUSED") {
		t.Errorf("the refusal's reason, printed on the launcher's stdout, is not in the loop log: %q", errb.String())
	}
}

// The launcher helper: this test binary, re-executed as the launcher, so a test can hold it
// between its acceptance and its exit, or keep it silent past the grace and then refuse. It
// is driven by the environment because flashLauncher's argv is the launcher contract's
// (bench, seat, card, label, deadline) and nothing else.
const (
	launchHelperEnv    = "NOVA_PULSE_LAUNCH_HELPER"
	launchHelperGo     = "NOVA_PULSE_LAUNCH_HELPER_GO"
	launchHelperRCEnv  = "NOVA_PULSE_LAUNCH_HELPER_RC"
	launchHelperStepMS = 10
)

func init() {
	mode := os.Getenv(launchHelperEnv)
	if mode == "" {
		return
	}
	// hold waits for the test's go-ahead file. It is a poll with a step, and its own bound
	// is a count of steps, so an abandoned helper exits rather than outliving the test.
	hold := func() {
		for i := 0; i < 6000; i++ {
			if _, err := os.Stat(os.Getenv(launchHelperGo)); err == nil {
				return
			}
			time.Sleep(launchHelperStepMS * time.Millisecond)
		}
		os.Exit(99)
	}
	rc, _ := strconv.Atoi(os.Getenv(launchHelperRCEnv))
	switch mode {
	case "accept":
		fmt.Println("LAUNCH ACCEPTED bench-a card-001")
		hold()
		fmt.Fprintln(os.Stderr, "the card failed on the bench, after it was accepted")
	case "silent-then-refuse":
		hold()
		fmt.Println("LAUNCH REFUSED bench-a card-001: CAPACITY cores=32 load=90 allowed=-40")
	case "hang":
		hold()
	}
	os.Exit(rc)
}

// launchHelper points flashLauncher at the helper in the given mode and answers the
// launcher path and the go-ahead that releases it.
func launchHelper(t *testing.T, mode string, rc int) (bin string, release func()) {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	gofile := filepath.Join(t.TempDir(), "go")
	t.Setenv(launchHelperEnv, mode)
	t.Setenv(launchHelperGo, gofile)
	t.Setenv(launchHelperRCEnv, strconv.Itoa(rc))
	var once sync.Once
	release = func() {
		once.Do(func() {
			if err := os.WriteFile(gofile, []byte("go\n"), 0o644); err != nil {
				t.Error(err)
			}
		})
	}
	t.Cleanup(release)
	return self, release
}

// clockAsk is one timer the launcher asked the fake clock for: how long, and the channel
// the test fires when it decides that much time has passed.
type clockAsk struct {
	d    time.Duration
	fire chan time.Time
}

// fakeAfter is flashLauncher's clock under test: every ask is handed to the test, and no
// time passes until the test fires it.
func fakeAfter() (func(time.Duration) <-chan time.Time, chan clockAsk) {
	asks := make(chan clockAsk, 8)
	return func(d time.Duration) <-chan time.Time {
		c := make(chan time.Time, 1)
		asks <- clockAsk{d: d, fire: c}
		return c
	}, asks
}

// launchTestWait is the bound on waiting for an EVENT the waits class test allows: thirty
// seconds, or NOVA_TEST_WAIT. It is never the thing asserted.
func launchTestWait() time.Duration {
	if v := os.Getenv("NOVA_TEST_WAIT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return 30 * time.Second
}

func launchAsync(l flashLauncher, card string) chan error {
	res := make(chan error, 1)
	go func() { res <- l.Launch("bench-a", card) }()
	return res
}

func nextAsk(t *testing.T, asks chan clockAsk, what string) clockAsk {
	t.Helper()
	select {
	case a := <-asks:
		return a
	case <-time.After(launchTestWait()):
		t.Fatalf("the launcher never asked the clock for %s", what)
	}
	return clockAsk{}
}

func launchResult(t *testing.T, res chan error) error {
	t.Helper()
	select {
	case err := <-res:
		return err
	case <-time.After(launchTestWait()):
		t.Fatal("Launch never answered")
	}
	return nil
}

// TestLaunchCountsOnlyOnAPositiveRecord is the handshake Stella asked for on #2874, over
// the launcher itself and a clock the test drives:
//   - a launcher silent at the grace and silent at a second grace that then refuses has
//     REFUSED (the 360cdd78 cut answered launched at the second grace);
//   - a launcher that prints LAUNCH ACCEPTED has launched, at once, while it still runs,
//     and whatever it exits with afterwards is the bench's;
//   - a launcher that neither accepts nor exits by the bound is killed and not launched.
func TestLaunchCountsOnlyOnAPositiveRecord(t *testing.T) {
	card := filepath.Join(t.TempDir(), "card-001.md")

	t.Run("a refusal after the grace, and after twice the grace, is a refusal", func(t *testing.T) {
		bin, release := launchHelper(t, "silent-then-refuse", 2)
		after, asks := fakeAfter()
		res := launchAsync(flashLauncher{bin: bin, grace: time.Second, after: after}, card)
		grace := nextAsk(t, asks, "the grace")
		grace.fire <- time.Now()
		// Twice the grace, and more: whatever the launcher waits on next, fire it too, so
		// the refusal lands after every window a grace-based fill would have used -- except
		// the bound, which is the hang case below.
		next := nextAsk(t, asks, "the next wait after the grace")
		if next.d <= 2*time.Second {
			next.fire <- time.Now()
		}
		release()
		err := launchResult(t, res)
		if err == nil {
			t.Fatal("a launcher that refused after twice the grace was counted as launched")
		}
		if !strings.Contains(err.Error(), "LAUNCH REFUSED") || !strings.Contains(err.Error(), "exit status 2") {
			t.Fatalf("the refusal does not carry rc 2 and the launcher's stdout reason: %v", err)
		}
	})

	t.Run("LAUNCH ACCEPTED is a launch while the launcher still runs", func(t *testing.T) {
		bin, release := launchHelper(t, "accept", 7)
		after, _ := fakeAfter()
		err := launchResult(t, launchAsync(flashLauncher{bin: bin, grace: time.Second, after: after}, card))
		if err != nil {
			t.Fatalf("an accepted launch answered an error: %v", err)
		}
		release()
	})

	t.Run("a launcher that never answers is killed at the bound and not launched", func(t *testing.T) {
		bin, _ := launchHelper(t, "hang", 0)
		after, asks := fakeAfter()
		var note bytes.Buffer
		res := launchAsync(flashLauncher{bin: bin, grace: time.Second, after: after, note: &note}, card)
		nextAsk(t, asks, "the grace").fire <- time.Now()
		bound := nextAsk(t, asks, "the bound")
		if want := defaultCardDeadline*time.Second + launchAnswerSlack; bound.d != want {
			t.Errorf("the bound is %s, want the card's deadline plus the slack, %s", bound.d, want)
		}
		bound.fire <- time.Now()
		err := launchResult(t, res)
		if err == nil || !strings.Contains(err.Error(), "LAUNCH UNANSWERED") {
			t.Fatalf("a launcher silent to the bound answered %v, want LAUNCH UNANSWERED", err)
		}
		if !strings.Contains(note.String(), "launch unanswered at the grace") {
			t.Errorf("the loop log does not say the launch was unanswered at the grace: %q", note.String())
		}
	})
}
