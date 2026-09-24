package main

// The command's end of #1914 and #1915: the flags that let a resident `nova-pulse fill`
// do what the scratch loop of 2026-09-19 had to do outside the verb -- read each bench's
// OWN slot store for the capacity, brake on load rather than size by it, tick in seconds
// rather than in five minutes, and stop on a file without killing anything.
//
// No test here opens an ssh connection: `ssh` is the fake on PATH, and what the test reads
// is the argv it recorded.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestSlotsStoreProbeAsksTheOwnersShareAndItsLiveLeases: the probe the bench runs names the
// store and the owner it was given, and asks the store -- not a load formula -- for the
// number. The answer it parses is the store's free count.
func TestSlotsStoreProbeAsksTheOwnersShareAndItsLiveLeases(t *testing.T) {
	specs := fakePATH(t)
	dir := t.TempDir()
	log := filepath.Join(dir, "ssh.log")
	fakeTool(t, specs, "ssh", fakeSpec{Log: log, Default: fakeRule{
		Stdout: "store share=64 cores=64 load1=76.00\nleases\n" +
			strings.Repeat("SLOT 1 owner=swarm-bench-a pid=9 label=- until=2026-09-20T00:00:00Z state=live\n", 12),
	}})
	n, err := storeProbeCapacity(storeProbeConfig{
		Store: "$HOME/nova-bench/slots", Owner: "swarm-bench-a",
		SlotsBin: "$HOME/.local/bin/nova-swarm", Shares: fixedShare(64),
	}).Capacity("bench-a")
	if err != nil {
		t.Fatalf("the store probe answered an error: %v", err)
	}
	if n != 52 {
		t.Fatalf("capacity = %d, want 52 (share 64 - held 12), not a load formula's number", n)
	}
	raw, err := os.ReadFile(log)
	if err != nil {
		t.Fatalf("ssh was never run: %v", err)
	}
	argv := string(raw)
	for _, want := range []string{"bench-a", "$HOME/nova-bench/slots", "store share=64", "slots list"} {
		if !strings.Contains(argv, want) {
			t.Fatalf("the probe does not ask for %q: %q", want, argv)
		}
	}
}

// TestSlotsStoreOwnerDefaultsToTheBenchsSeat: the lease owner is `swarm-<bench>`, the same
// name the launcher already hands the bench, so the common case needs no flag: its leases
// are the ones counted against the share, and another owner's are not.
func TestSlotsStoreOwnerDefaultsToTheBenchsSeat(t *testing.T) {
	specs := fakePATH(t)
	fakeTool(t, specs, "ssh", fakeSpec{Default: fakeRule{
		Stdout: "store share=8 cores=64 load1=1.00\nleases\n" +
			"SLOT 1 owner=swarm-vision pid=9 label=- until=2026-09-20T00:00:00Z state=live\n" +
			"SLOT 2 owner=swarm-vision pid=10 label=- until=2026-09-20T00:00:00Z state=live\n" +
			"SLOT 3 owner=swarm-other pid=11 label=- until=2026-09-20T00:00:00Z state=live\n",
	}})
	n, err := storeProbeCapacity(storeProbeConfig{Store: "/s", Shares: fixedShare(8)}).Capacity("vision")
	if err != nil {
		t.Fatalf("capacity: %v", err)
	}
	if n != 6 {
		t.Fatalf("capacity = %d, want 6 (share 8 - swarm-vision's 2 leases; swarm-other's are not counted)", n)
	}
}

// fixedShare is a share source that answers one share for every bench.
func fixedShare(n int) func(string) (int, bool, error) {
	return func(string) (int, bool, error) { return n, true, nil }
}

// TestALocalBenchIsProbedWithoutSSH: the Studio is a bench and there is no ssh from the
// Studio to the Studio. A bench named by --local-bench is read by this machine's own shell,
// and `ssh` is never started at all.
func TestALocalBenchIsProbedWithoutSSH(t *testing.T) {
	specs := fakePATH(t)
	dir := t.TempDir()
	log := filepath.Join(dir, "ssh.log")
	fakeTool(t, specs, "ssh", fakeSpec{Log: log, Default: fakeRule{Stdout: "store share=99 cores=8 load1=0\nleases\n"}})
	// A REAL lease read that answers an empty list: the store is readable, the owner holds
	// nothing, and the whole share is free. A slots binary that cannot run is a refusal
	// (Stella, #1945) and is covered by TestFillRefusesABenchWhoseSlotsBinaryIsMissing.
	fakeTool(t, specs, "nova-swarm", fakeSpec{Default: fakeRule{Stdout: ""}})
	store := filepath.Join(dir, "slots")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	n, err := storeProbeCapacity(storeProbeConfig{
		Store: store, Owner: "rowan", Local: map[string]bool{"studio": true}, Shares: fixedShare(7),
		SlotsBin: filepath.Join(fakeBins(t), "nova-swarm"+exeSuffix()),
	}).Capacity("studio")
	if err != nil {
		t.Fatalf("the local probe answered an error: %v", err)
	}
	if n != 7 {
		t.Fatalf("capacity = %d, want 7 (the configured share, no leases held)", n)
	}
	if _, err := os.Stat(log); err == nil {
		t.Fatal("a local bench was probed over ssh")
	}
}

// TestFillIntervalFlagRefusesADurationItCannotRead: a resident fill ticked every 300s
// because there was no flag (#1915). There is one now, and a value it cannot read is a
// refusal that names it rather than a silent five minutes.
func TestFillIntervalFlagRefusesADurationItCannotRead(t *testing.T) {
	dir := t.TempDir()
	ready, launched := filepath.Join(dir, "ready"), filepath.Join(dir, "launched")
	var out, errb bytes.Buffer
	code := run([]string{"fill", "--ready", ready, "--launched", launched,
		"--machines", fillMachines(t, dir, "bench-a"),
		"--bench", "bench-a", "--capacity", "0", "--once", "--interval", "ten seconds",
	}, &out, &errb, time.Now().UTC())
	if code != 2 {
		t.Fatalf("fill --interval 'ten seconds' exit = %d, want 2; stderr=%q", code, errb.String())
	}
	if !strings.Contains(errb.String(), "--interval") {
		t.Fatalf("the refusal does not name --interval: %q", errb.String())
	}
}

// TestFillStopFlagStopsTheTickWithoutKillingAnything: the flag reaches the loop, and a stop
// file that is already there means a fill that claims nothing.
func TestFillStopFlagStopsTheTickWithoutKillingAnything(t *testing.T) {
	specs := fakePATH(t)
	dir := t.TempDir()
	fakeTool(t, specs, "nova-swarm", fakeSpec{Default: fakeRule{Exit: 0}})
	ready, launched := filepath.Join(dir, "ready"), filepath.Join(dir, "launched")
	writeMainFile(t, ready, "card-001.md", "a card\n")
	stop := writeMainFile(t, dir, "STOP", "")
	var out, errb bytes.Buffer
	code := run([]string{"fill", "--ready", ready, "--launched", launched,
		"--machines", fillMachines(t, dir, "bench-a"),
		"--bench", "bench-a", "--capacity", "10", "--once", "--stop", stop,
		"--launcher", filepath.Join(fakeBins(t), "nova-swarm"+exeSuffix()),
	}, &out, &errb, time.Now().UTC())
	if code != 0 {
		t.Fatalf("fill --stop exit = %d, want 0; stderr=%q", code, errb.String())
	}
	if !strings.Contains(out.String(), "FILL STOP") {
		t.Fatalf("the stop file did not stop the tick: %q", out.String())
	}
	if fillCount(t, ready) != 1 {
		t.Fatalf("ready holds %d cards, want 1 (nothing was claimed)", fillCount(t, ready))
	}
}

// TestMaxLoadPerCoreFlagRefusesANegativeGuard: the brake is a number of load units per core,
// 0 for no brake at all. A negative is a typo, and a typo that silently brakes every bench
// is a fleet that stops.
func TestMaxLoadPerCoreFlagRefusesANegativeGuard(t *testing.T) {
	dir := t.TempDir()
	ready, launched := filepath.Join(dir, "ready"), filepath.Join(dir, "launched")
	var out, errb bytes.Buffer
	code := run([]string{"fill", "--ready", ready, "--launched", launched,
		"--machines", fillMachines(t, dir, "bench-a"),
		"--bench", "bench-a", "--capacity", "0", "--once", "--max-load-per-core", "-1",
	}, &out, &errb, time.Now().UTC())
	if code != 2 {
		t.Fatalf("fill --max-load-per-core -1 exit = %d, want 2; stderr=%q", code, errb.String())
	}
	if !strings.Contains(errb.String(), "--max-load-per-core") {
		t.Fatalf("the refusal does not name --max-load-per-core: %q", errb.String())
	}
}

// TestFillCapComesFromTheRegistryNotSharesTSV (nx-e06, capacity is config): the store holds a
// shares.tsv that says 7 and the machines registry says share=3. The fill deals 3 -- the
// registry is the capacity -- and the script the bench runs never names shares.tsv or
// ramp.tsv at all.
func TestFillCapComesFromTheRegistryNotSharesTSV(t *testing.T) {
	specs := fakePATH(t)
	fakeTool(t, specs, "nova-swarm", fakeSpec{Default: fakeRule{Stdout: ""}})
	fakeTool(t, specs, "nova-bus", fakeSpec{Default: fakeRule{Exit: 0}})
	dir := t.TempDir()
	fillReady(t, filepath.Join(dir, "ready"), 6)
	store := fillStore(t, dir, "swarm-bench-a", 3)
	writeMainFile(t, store, "shares.tsv", "swarm-bench-a\t7\n")

	code, out, errb, _, launched := localFill(t, dir, store,
		filepath.Join(fakeBins(t), "nova-swarm"+exeSuffix()), "--max-load-per-core", "0")
	if code != 0 {
		t.Fatalf("exit = %d; stdout=%q stderr=%q", code, out, errb)
	}
	if got := fillCount(t, launched); got != 3 {
		t.Fatalf("launched %d cards, want 3 (the registry's share, not shares.tsv's 7): %q %q", got, out, errb)
	}
	for _, share := range []int{0, 5} {
		script := storeScript(store, share, share > 0, "$HOME/.local/bin/nova-swarm", "$HOME/rowan-swarm-root")
		for _, file := range []string{"shares.tsv", "ramp.tsv"} {
			if strings.Contains(script, file) {
				t.Fatalf("the probe script still reads %s: %q", file, script)
			}
		}
	}
}
