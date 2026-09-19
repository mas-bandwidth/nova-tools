package main

// Stella's HOLD on #1945 at c9f4c253, repaired here. Both defects are at the COMMAND's
// boundary, not the helper's, so every test in this file drives the real CLI entry --
// `run([]string{"fill", ...})` -- and asserts what reached the queue directories.
//
// P1: an unreadable lease store became free capacity. The probe piped `slots list` through
// `grep -c` and kept the PIPELINE's status, so a missing or failing nova-swarm printed 0,
// the script succeeded, and a bench with every slot leased answered `held=0` -- its whole
// share dealt to a machine that had nothing free. A read that failed must refuse that
// bench, and a lease list that is legitimately EMPTY must still fill it.
//
// P2: malformed readings inflated capacity or quietly turned the brake off. `held=-10`
// made a share of 64 answer 74; `cores=x` became 0, which is a bench that can never be
// braked; `load1=NaN` compared false against every threshold; and `--max-load-per-core NaN`
// passed the flag's only check, which was `< 0`. Every one of those is a refusal now, and
// the documented `--max-load-per-core 0` opt-out is untouched.

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// fillStore writes a slot store with one owner row, the shape the probe reads.
func fillStore(t *testing.T, dir, owner string, share int) string {
	t.Helper()
	store := filepath.Join(dir, "slots")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	writeMainFile(t, store, "shares.tsv", owner+"\t"+strconv.Itoa(share)+"\n")
	return store
}

// localFill runs one real `fill` tick against a LOCAL bench, so the probe that runs is the
// actual shell script this command ships -- the boundary Stella's witness reproduced -- and
// no ssh is involved at all.
func localFill(t *testing.T, dir, store, slotsBin string, extra ...string) (int, string, string, string, string) {
	t.Helper()
	ready, launched := filepath.Join(dir, "ready"), filepath.Join(dir, "launched")
	args := []string{"fill", "--ready", ready, "--launched", launched,
		"--machines", fillMachines(t, dir, "bench-a"),
		"--bench", "bench-a", "--local-bench", "bench-a",
		"--slots-store", store, "--slots-owner", "swarm-bench-a",
		"--slots-bin", slotsBin,
		"--launcher", filepath.Join(fakeBins(t), "nova-bus"+exeSuffix()),
		"--launch-grace", "0", "--once",
	}
	args = append(args, extra...)
	var out, errb bytes.Buffer
	code := run(args, &out, &errb, time.Now().UTC())
	return code, out.String(), errb.String(), ready, launched
}

// TestFillRefusesABenchWhoseSlotsBinaryIsMissing is P1 itself: `slots list` cannot run, the
// pipeline printed 0, and a full bench answered its whole share. The read failed, so the
// bench is refused by name and takes no card.
func TestFillRefusesABenchWhoseSlotsBinaryIsMissing(t *testing.T) {
	specs := fakePATH(t)
	// The launcher WORKS. Without that, a fall-through to the whole share would fail at
	// the launcher and the exit code would look like a refusal for the wrong reason.
	fakeTool(t, specs, "nova-bus", fakeSpec{Default: fakeRule{Exit: 0}})
	dir := t.TempDir()
	fillReady(t, filepath.Join(dir, "ready"), 4)
	store := fillStore(t, dir, "swarm-bench-a", 4)

	code, out, errb, ready, launched := localFill(t, dir, store, filepath.Join(dir, "no-such-nova-swarm"))
	if code == 0 {
		t.Fatalf("a bench whose lease store could not be read answered exit 0; stdout=%q stderr=%q", out, errb)
	}
	if fillCount(t, ready) != 4 {
		t.Fatalf("ready holds %d cards, want 4 (nothing may be claimed on a capacity nobody read)", fillCount(t, ready))
	}
	if fillCount(t, launched) != 0 {
		t.Fatalf("launched holds %d cards, want 0", fillCount(t, launched))
	}
	if !strings.Contains(out, "bench-a:launched=0,failed=0") {
		t.Fatalf("a card was claimed on a capacity nobody read: %q", out)
	}
	if !strings.Contains(errb, "FILL NOTE") {
		t.Fatalf("nothing said why the bench was refused: %q", errb)
	}
	for _, want := range []string{"slots", "bench-a"} {
		if !strings.Contains(errb, want) {
			t.Fatalf("the refusal does not name %q: %q", want, errb)
		}
	}
}

// TestFillRefusesABenchWhoseSlotsListExitsNonZero: the binary is there and the read failed
// anyway -- a corrupt store, a store another owner holds open. Same answer: no capacity.
func TestFillRefusesABenchWhoseSlotsListExitsNonZero(t *testing.T) {
	specs := fakePATH(t)
	fakeTool(t, specs, "nova-swarm", fakeSpec{Default: fakeRule{Stderr: "nova-swarm slots list: bad store", Exit: 3}})
	fakeTool(t, specs, "nova-bus", fakeSpec{Default: fakeRule{Exit: 0}})
	dir := t.TempDir()
	fillReady(t, filepath.Join(dir, "ready"), 3)
	store := fillStore(t, dir, "swarm-bench-a", 3)

	code, out, errb, ready, launched := localFill(t, dir, store, filepath.Join(fakeBins(t), "nova-swarm"+exeSuffix()))
	if code == 0 {
		t.Fatalf("a failing slots list answered exit 0; stdout=%q stderr=%q", out, errb)
	}
	if fillCount(t, ready) != 3 || fillCount(t, launched) != 0 {
		t.Fatalf("ready holds %d and launched %d, want 3 and 0: %q %q",
			fillCount(t, ready), fillCount(t, launched), out, errb)
	}
	if !strings.Contains(out, "bench-a:launched=0,failed=0") {
		t.Fatalf("a card was claimed on a capacity nobody read: %q", out)
	}
	if !strings.Contains(errb, "FILL NOTE") {
		t.Fatalf("nothing said why: %q", errb)
	}
}

// TestFillRefusesABenchWhoseSlotsListPrintsSomethingElse: exit 0 and output that is not
// leases. `grep -c` counts no match there either, and zero matches out of noise is not the
// same fact as zero leases out of an empty store.
func TestFillRefusesABenchWhoseSlotsListPrintsSomethingElse(t *testing.T) {
	specs := fakePATH(t)
	fakeTool(t, specs, "nova-swarm", fakeSpec{Default: fakeRule{
		Stdout: "usage: nova-swarm slots list --store <dir>\nsomething else entirely\n",
	}})
	fakeTool(t, specs, "nova-bus", fakeSpec{Default: fakeRule{Exit: 0}})
	dir := t.TempDir()
	fillReady(t, filepath.Join(dir, "ready"), 3)
	store := fillStore(t, dir, "swarm-bench-a", 3)

	code, out, errb, ready, launched := localFill(t, dir, store, filepath.Join(fakeBins(t), "nova-swarm"+exeSuffix()))
	if code == 0 {
		t.Fatalf("a malformed slots list answered exit 0; stdout=%q stderr=%q", out, errb)
	}
	if fillCount(t, ready) != 3 || fillCount(t, launched) != 0 {
		t.Fatalf("ready holds %d and launched %d, want 3 and 0: %q %q",
			fillCount(t, ready), fillCount(t, launched), out, errb)
	}
	if !strings.Contains(out, "bench-a:launched=0,failed=0") {
		t.Fatalf("a card was claimed out of noise read as an empty store: %q", out)
	}
	if !strings.Contains(errb, "FILL NOTE") {
		t.Fatalf("nothing said why: %q", errb)
	}
}

// TestFillFillsABenchWhoseSlotsListIsEmpty is the control that keeps the refusals honest: a
// store with no leases at all is a bench with its WHOLE share free, and it must still fill.
// A guard that cannot tell an empty list from a failed read is a guard that stops the fleet.
func TestFillFillsABenchWhoseSlotsListIsEmpty(t *testing.T) {
	specs := fakePATH(t)
	fakeTool(t, specs, "nova-swarm", fakeSpec{Default: fakeRule{Stdout: ""}})
	fakeTool(t, specs, "nova-bus", fakeSpec{Default: fakeRule{Exit: 0}})
	dir := t.TempDir()
	fillReady(t, filepath.Join(dir, "ready"), 5)
	store := fillStore(t, dir, "swarm-bench-a", 2)

	code, out, errb, ready, launched := localFill(t, dir, store, filepath.Join(fakeBins(t), "nova-swarm"+exeSuffix()))
	if code != 0 {
		t.Fatalf("an empty lease list is a free bench; exit = %d, stdout=%q stderr=%q", code, out, errb)
	}
	if fillCount(t, launched) != 2 {
		t.Fatalf("launched holds %d cards, want 2 (the whole share): %q %q", fillCount(t, launched), out, errb)
	}
	if fillCount(t, ready) != 3 {
		t.Fatalf("ready holds %d cards, want 3", fillCount(t, ready))
	}
}

// TestFillCountsTheOwnersLiveLeasesAndNobodyElses: the free count is the owner's share less
// the owner's OWN live leases -- not another owner's, and not an expired one.
func TestFillCountsTheOwnersLiveLeasesAndNobodyElses(t *testing.T) {
	specs := fakePATH(t)
	fakeTool(t, specs, "nova-swarm", fakeSpec{Default: fakeRule{Stdout: strings.Join([]string{
		"SLOT 1 owner=swarm-bench-a pid=10 label=a until=2026-09-20T00:00:00Z state=live",
		"SLOT 2 owner=swarm-bench-a pid=11 label=b until=2026-09-20T00:00:00Z state=live",
		"SLOT 3 owner=swarm-other pid=12 label=c until=2026-09-20T00:00:00Z state=live",
		"SLOT 4 owner=swarm-bench-a pid=13 label=d until=2026-09-19T00:00:00Z state=expired",
		"",
	}, "\n")}})
	fakeTool(t, specs, "nova-bus", fakeSpec{Default: fakeRule{Exit: 0}})
	dir := t.TempDir()
	fillReady(t, filepath.Join(dir, "ready"), 6)
	store := fillStore(t, dir, "swarm-bench-a", 5)

	code, out, errb, _, launched := localFill(t, dir, store, filepath.Join(fakeBins(t), "nova-swarm"+exeSuffix()))
	if code != 0 {
		t.Fatalf("exit = %d; stdout=%q stderr=%q", code, out, errb)
	}
	if got := fillCount(t, launched); got != 3 {
		t.Fatalf("launched holds %d cards, want 3 (share 5 - the owner's 2 live leases): %q %q", got, out, errb)
	}
}

// TestFillStillFallsBackToTheFormulaWithNoRowForTheOwner keeps the ONE documented
// fallthrough documented: a store that holds no row for this owner is the no-store case,
// and the bench answers the load formula exactly as it always did. Everything else refuses.
func TestFillStillFallsBackToTheFormulaWithNoRowForTheOwner(t *testing.T) {
	specs := fakePATH(t)
	fakeTool(t, specs, "nova-bus", fakeSpec{Default: fakeRule{Exit: 0}})
	dir := t.TempDir()
	fillReady(t, filepath.Join(dir, "ready"), 2)
	store := fillStore(t, dir, "somebody-else", 9)

	code, out, errb, _, _ := localFill(t, dir, store, filepath.Join(dir, "no-such-nova-swarm"))
	if code == 2 {
		t.Fatalf("a store with no row for the owner was a refusal, not the documented fallback; stderr=%q", errb)
	}
	if strings.Contains(errb, "slots-list") {
		t.Fatalf("the no-row fallback went through the lease read: %q", errb)
	}
	if !strings.Contains(out, "FILL tick=1") {
		t.Fatalf("no tick ran: %q %q", out, errb)
	}
}

// sshFill runs one real `fill` tick against a REMOTE bench whose probe answers exactly what
// the test hands it, so the parsing and the brake are driven through the command.
func sshFill(t *testing.T, answer string, extra ...string) (int, string, string, string, string) {
	t.Helper()
	specs := fakePATH(t)
	fakeTool(t, specs, "ssh", fakeSpec{Default: fakeRule{Stdout: answer}})
	fakeTool(t, specs, "nova-bus", fakeSpec{Default: fakeRule{Exit: 0}})
	dir := t.TempDir()
	ready, launched := filepath.Join(dir, "ready"), filepath.Join(dir, "launched")
	fillReady(t, ready, 4)
	args := []string{"fill", "--ready", ready, "--launched", launched,
		"--machines", fillMachines(t, dir, "bench-a"),
		"--bench", "bench-a",
		"--launcher", filepath.Join(fakeBins(t), "nova-bus"+exeSuffix()),
		"--launch-grace", "0", "--once",
	}
	args = append(args, extra...)
	var out, errb bytes.Buffer
	code := run(args, &out, &errb, time.Now().UTC())
	return code, out.String(), errb.String(), ready, launched
}

// TestFillRefusesAMalformedCapacityAnswer is P2: every one of these readings was taken as
// a valid capacity, and the first of them handed a bench MORE than its whole share.
func TestFillRefusesAMalformedCapacityAnswer(t *testing.T) {
	for name, answer := range map[string]string{
		"negative held inflates the share": "store share=64 held=-10 cores=64 load1=1.0\n",
		"negative share":                   "store share=-4 held=0 cores=64 load1=1.0\n",
		"cores that is not a number":       "store share=4 held=0 cores=x load1=1.0\n",
		"load that is not a number":        "store share=4 held=0 cores=64 load1=NaN\n",
		"infinite load":                    "store share=4 held=0 cores=64 load1=+Inf\n",
		"missing load":                     "store share=4 held=0 cores=64\n",
		"missing cores":                    "store share=4 held=0 load1=1.0\n",
		"duplicate share":                  "store share=4 share=400 held=0 cores=64 load1=1.0\n",
		"negative formula capacity":        "formula capacity=-3 cores=64 load1=1.0\n",
		"share out of range":               "store share=99999999999999999999 held=0 cores=64 load1=1.0\n",
		"the bench could not read it":      "unreadable reason=slots-list-exit rc=127\n",
	} {
		t.Run(name, func(t *testing.T) {
			code, out, errb, ready, launched := sshFill(t, answer)
			if code == 0 {
				t.Fatalf("the probe answered %q and the tick exited 0; stdout=%q stderr=%q", answer, out, errb)
			}
			if fillCount(t, launched) != 0 {
				t.Fatalf("the probe answered %q and %d cards launched", answer, fillCount(t, launched))
			}
			if fillCount(t, ready) != 4 {
				t.Fatalf("the probe answered %q and ready holds %d cards, want 4", answer, fillCount(t, ready))
			}
			if !strings.Contains(errb, "FILL NOTE") {
				t.Fatalf("the probe answered %q and nothing said why: %q", answer, errb)
			}
		})
	}
}

// TestFillRefusesToRunUnbrakedWhenTheBenchCannotCountItsCores: with the brake on, a bench
// answering no cores cannot be braked at all. Running it unbraked is the brake quietly
// turning itself off, which is the shape of the whole bug.
func TestFillRefusesToRunUnbrakedWhenTheBenchCannotCountItsCores(t *testing.T) {
	code, out, errb, _, launched := sshFill(t, "store share=4 held=0 cores=0 load1=8.0\n", "--max-load-per-core", "1.5")
	if code == 0 {
		t.Fatalf("a bench that could not be braked ran anyway; stdout=%q stderr=%q", out, errb)
	}
	if fillCount(t, launched) != 0 {
		t.Fatalf("%d cards launched on a bench nobody could brake", fillCount(t, launched))
	}
}

// TestTheZeroBrakeOptOutStillFillsABenchWithNoCores: `--max-load-per-core 0` is the
// documented "no brake" and it must stay exactly that, cores or no cores. The repair may
// not take the opt-out with it.
func TestTheZeroBrakeOptOutStillFillsABenchWithNoCores(t *testing.T) {
	code, out, errb, _, launched := sshFill(t, "store share=2 held=0 cores=0 load1=8.0\n", "--max-load-per-core", "0")
	if code != 0 {
		t.Fatalf("the zero-brake opt-out refused; exit = %d stdout=%q stderr=%q", code, out, errb)
	}
	if fillCount(t, launched) != 2 {
		t.Fatalf("launched holds %d cards, want 2: %q %q", fillCount(t, launched), out, errb)
	}
}

// TestFillRefusesABrakeThatIsNotAFiniteNumber: the flag's only check was `< 0`, and NaN is
// not less than zero -- so `--max-load-per-core NaN` parsed, compared false against every
// bench and silently disabled the brake the caller had asked for. Infinity is the same
// silence with a different spelling.
func TestFillRefusesABrakeThatIsNotAFiniteNumber(t *testing.T) {
	for _, bad := range []string{"NaN", "nan", "+Inf", "-Inf", "-1", "-0.5"} {
		t.Run(bad, func(t *testing.T) {
			dir := t.TempDir()
			var out, errb bytes.Buffer
			code := run([]string{"fill",
				"--ready", filepath.Join(dir, "ready"), "--launched", filepath.Join(dir, "launched"),
				"--machines", fillMachines(t, dir, "bench-a"),
				"--bench", "bench-a", "--capacity", "0", "--once",
				"--max-load-per-core", bad,
			}, &out, &errb, time.Now().UTC())
			if code != 2 {
				t.Fatalf("--max-load-per-core %s exit = %d, want 2; stderr=%q", bad, code, errb.String())
			}
			if !strings.Contains(errb.String(), "--max-load-per-core") {
				t.Fatalf("the refusal does not name the flag: %q", errb.String())
			}
		})
	}
}
