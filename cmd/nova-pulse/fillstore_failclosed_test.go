package main

// FAIL CLOSED. Johnny's security read and Stella's plain-Go read of #1945 are the same
// class from two ends: a bench cannot make the coordinator execute anything, but it can
// make it BELIEVE a free count, and every soft edge in the probe read high. A missing
// `--slots-bin`, a swallowed `slots list` failure, a `held=` the parser never checked --
// each of them answered "this bench is empty" for a bench that was full.
//
// The rule these tests hold: **every probe or parse failure on a bench is Free=0 for that
// bench, said on a named FILL line with the reason, never a deal and never a silent
// fallback.** The one path to the old load formula is the positively detected "this store
// has no row for this owner", from a lease read that SUCCEEDED. Counting happens in Go, not
// in a shell pipeline whose status belongs to grep.
//
// Every test drives the real CLI entry and reads what reached the queue directories.

import (
	"bytes"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// slotLine is one row of `nova-swarm slots list`, exactly as internal/swarm writes it.
func slotLine(id, owner, state string) string {
	return "SLOT " + id + " owner=" + owner + " pid=10 label=- until=2026-09-20T00:00:00Z state=" + state
}

// leaseAnswer is what the probe prints for a readable store: the header, the `leases`
// marker, then the lease listing verbatim for Go to count.
func leaseAnswer(share, cores int, load string, leases ...string) string {
	head := "store share=" + strconv.Itoa(share) + " cores=" + strconv.Itoa(cores) + " load1=" + load
	return head + "\nleases\n" + strings.Join(leases, "\n") + "\n"
}

// TestEveryUnreadableBenchIsNamedOnItsOwnFillLineWithFreeZero: two benches, two different
// failures, and BOTH have to be named. The tick used to keep only the first error, so a
// fleet where every bench was unreadable said one reason and nothing about the rest.
func TestEveryUnreadableBenchIsNamedOnItsOwnFillLineWithFreeZero(t *testing.T) {
	specs := fakePATH(t)
	fakeTool(t, specs, "ssh", fakeSpec{Rules: []fakeRule{
		{Arg: 4, Equals: "bench-a", Stdout: "unreadable reason=slots-list-exit rc=127\n"},
	}, Default: fakeRule{Stdout: "store share=4 cores=64 load1=x\nleases\n"}})
	fakeTool(t, specs, "nova-bus", fakeSpec{Default: fakeRule{Exit: 0}})
	dir := t.TempDir()
	ready, launched := filepath.Join(dir, "ready"), filepath.Join(dir, "launched")
	fillReady(t, ready, 6)
	var out, errb bytes.Buffer
	code := run([]string{"fill", "--ready", ready, "--launched", launched,
		"--machines", fillMachines(t, dir, "bench-a", "bench-b"),
		"--bench", "bench-a", "--bench", "bench-b",
		"--launcher", filepath.Join(fakeBins(t), "nova-bus"+exeSuffix()),
		"--launch-grace", "0", "--once",
	}, &out, &errb, time.Now().UTC())

	if code == 0 {
		t.Fatalf("a tick where no bench could be read exited 0; stdout=%q stderr=%q", out.String(), errb.String())
	}
	if fillCount(t, launched) != 0 || fillCount(t, ready) != 6 {
		t.Fatalf("launched %d / ready %d, want 0 / 6: %q %q",
			fillCount(t, launched), fillCount(t, ready), out.String(), errb.String())
	}
	said := errb.String()
	for _, bench := range []string{"bench-a", "bench-b"} {
		want := "FILL UNREADABLE bench=" + bench + " free=0 reason="
		if !strings.Contains(said, want) {
			t.Fatalf("no named line for %s (want %q): %q", bench, want, said)
		}
	}
	if n := strings.Count(said, "FILL UNREADABLE"); n != 2 {
		t.Fatalf("%d unreadable lines for two unreadable benches, want 2: %q", n, said)
	}
}

// TestFillRefusesALeaseListItCannotParse: the counting is Go's now, and a row that is not a
// lease is a refusal. A shell `grep -c` counted matches, so a row it did not match was
// indistinguishable from a slot that is free -- it read high, which is the whole class.
func TestFillRefusesALeaseListItCannotParse(t *testing.T) {
	for name, listing := range map[string][]string{
		"a lease with no state":   {"SLOT 1 owner=swarm-bench-a pid=10 label=- until=2026-09-20T00:00:00Z state="},
		"a lease with no owner":   {"SLOT 2 pid=10 label=- until=2026-09-20T00:00:00Z state=live"},
		"a row that is not SLOT":  {"warning: the store is being migrated"},
		"a state nobody declared": {slotLine("3", "swarm-bench-a", "")},
	} {
		t.Run(name, func(t *testing.T) {
			code, out, errb, ready, launched := sshFill(t, leaseAnswer(4, 64, "1.0", listing...),
				"--slots-owner", "swarm-bench-a")
			if code == 0 {
				t.Fatalf("a lease listing nobody could parse exited 0; stdout=%q stderr=%q", out, errb)
			}
			if fillCount(t, launched) != 0 || fillCount(t, ready) != 4 {
				t.Fatalf("launched %d / ready %d, want 0 / 4", fillCount(t, launched), fillCount(t, ready))
			}
			if !strings.Contains(errb, "FILL UNREADABLE bench=bench-a free=0 reason=") {
				t.Fatalf("the bench was not named with free=0: %q", errb)
			}
		})
	}
}

// TestFillRefusesMoreLeasesHeldThanTheShareAllows: an owner cannot hold more than its
// share. A reading that says it did is a reading that went wrong, and the answer is Free=0
// with a reason, not a share-minus-held that some clamp catches later.
func TestFillRefusesMoreLeasesHeldThanTheShareAllows(t *testing.T) {
	leases := []string{
		slotLine("1", "swarm-bench-a", "live"),
		slotLine("2", "swarm-bench-a", "live"),
		slotLine("3", "swarm-bench-a", "live"),
	}
	code, out, errb, ready, launched := sshFill(t, leaseAnswer(2, 64, "1.0", leases...),
		"--slots-owner", "swarm-bench-a")
	if code == 0 {
		t.Fatalf("held above share exited 0; stdout=%q stderr=%q", out, errb)
	}
	if fillCount(t, launched) != 0 || fillCount(t, ready) != 4 {
		t.Fatalf("launched %d / ready %d, want 0 / 4", fillCount(t, launched), fillCount(t, ready))
	}
	if !strings.Contains(errb, "FILL UNREADABLE bench=bench-a free=0 reason=") {
		t.Fatalf("the bench was not named with free=0: %q", errb)
	}
}

// TestFillCountsADriftLeaseAsHeld: `nova-swarm native` counts a DRIFT lease -- expired by
// the clock, with its process still alive -- as held, and the probe's old `grep state=live`
// did not. The probe read HIGH against the seat that actually grants, so fill dealt cards
// the bench then refused. Held is every lease that is not expired.
func TestFillCountsADriftLeaseAsHeld(t *testing.T) {
	leases := []string{
		slotLine("1", "swarm-bench-a", "live"),
		slotLine("2", "swarm-bench-a", "DRIFT"),
		slotLine("3", "swarm-bench-a", "expired"),
		slotLine("4", "swarm-other", "live"),
	}
	code, out, errb, _, launched := sshFill(t, leaseAnswer(4, 64, "1.0", leases...),
		"--slots-owner", "swarm-bench-a")
	if code != 0 {
		t.Fatalf("exit = %d; stdout=%q stderr=%q", code, out, errb)
	}
	if got := fillCount(t, launched); got != 2 {
		t.Fatalf("launched %d cards, want 2 (share 4 less one live and one DRIFT lease): %q %q", got, out, errb)
	}
}

// TestTheFormulaFallbackIsNamedAndNeverFollowsAFailedLeaseRead: the ONE documented path
// back to the old load formula is "this store has no row for this owner", positively
// detected, from a lease read that succeeded -- and it says so on a named line rather than
// quietly becoming the number this whole change exists to stop using.
func TestTheFormulaFallbackIsNamedAndNeverFollowsAFailedLeaseRead(t *testing.T) {
	t.Run("named when the list succeeded", func(t *testing.T) {
		code, out, errb, _, _ := sshFill(t, "formula capacity=2 cores=64 load1=1.0 why=no-row-for-owner\n")
		if code != 0 {
			t.Fatalf("exit = %d; stdout=%q stderr=%q", code, out, errb)
		}
		if !strings.Contains(errb, "FILL FORMULA bench=bench-a") || !strings.Contains(errb, "why=no-row-for-owner") {
			t.Fatalf("the fallback was silent: %q", errb)
		}
	})
	t.Run("refused when the list failed", func(t *testing.T) {
		code, out, errb, _, launched := sshFill(t, "unreadable reason=slots-list-exit rc=2\n")
		if code == 0 {
			t.Fatalf("a failed lease read fell through to something; stdout=%q stderr=%q", out, errb)
		}
		if fillCount(t, launched) != 0 {
			t.Fatalf("%d cards launched after a failed lease read", fillCount(t, launched))
		}
		if strings.Contains(errb, "FILL FORMULA") {
			t.Fatalf("a failed lease read fell back to the formula: %q", errb)
		}
	})
}

// TestTheProbeAnswerIsBounded: the answer used to land in an unbounded buffer, so a bench
// that printed for ever cost the coordinator its memory. A bench is the least trusted thing
// on the wire; what it says is read up to a bound and refused past it.
func TestTheProbeAnswerIsBounded(t *testing.T) {
	huge := "store share=4 cores=64 load1=1.0\nleases\n" +
		strings.Repeat(slotLine("1", "swarm-other", "live")+"\n", 20000)
	code, out, errb, _, launched := sshFill(t, huge, "--slots-owner", "swarm-bench-a")
	if code == 0 {
		t.Fatalf("an unbounded answer exited 0; stdout=%q", out)
	}
	if fillCount(t, launched) != 0 {
		t.Fatalf("%d cards launched on an unbounded answer", fillCount(t, launched))
	}
	if !strings.Contains(errb, "FILL UNREADABLE bench=bench-a free=0 reason=") {
		t.Fatalf("the bench was not named with free=0: %q", errb)
	}
	if len(errb) > 8000 {
		t.Fatalf("the refusal itself is %d bytes; a bounded read must bound its own message", len(errb))
	}
}
