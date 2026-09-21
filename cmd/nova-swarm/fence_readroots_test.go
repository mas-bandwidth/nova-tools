package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// ISSUE #1463: THE HARNESS FENCE IGNORED THE WORKER DESCRIPTION'S `read_roots`, so a staged
// read the desk had granted was auto-rejected while the OS wall allowed it -- and the run
// still printed `NATIVE OK ... rc=0 harness=ok`. The card came back `not done` with the whole
// row owed and the spend already made.
//
// `read_roots` is the worker description's own declaration of what every job of this worker
// may READ: a bench-local mirror, a corpus, a toolchain under a user directory. It is
// validated at load (internal/swarm/worker.go:227-236 refuses a relative entry, an empty one,
// one that does not exist and one that is not a directory) and `worker check` answers
// `WORKER OK`, so a coordinator has every reason to believe the path is usable.
//
// `native` read it nowhere. At dev 31e35195 `grep -n ReadRoots cmd/nova-swarm/native.go` is
// EMPTY: neither the wall's argv nor the harness's fence was ever told.
//
// THE WALL IS STILL THE REAL BOUNDARY (SPEC-SANDBOX rule 1). The harness's fence is a second,
// weaker one, and a second fence that denies what the first one grants can only cost cards.
// So what the fence is handed here is EXACTLY what the wall is handed, and never more.

// aReadRootWorker is a description whose only interesting field is `read_roots`. The model is
// the fake one every native seam test uses, so the run's pinned-model check passes.
func aReadRootWorker(roots ...string) *swarm.Worker {
	return &swarm.Worker{Name: "read-root-worker", Provider: "fake", Model: "fake-model", ReadRoots: roots}
}

// TestNativeConfigNamesTheWorkerReadRootsOnAWalledRun is the issue, at the fence. RED
// WITHOUT THE FIX: the walled run's permission block held the job's own directories and
// nothing else, so every read of the staged root was auto-rejected.
func TestNativeConfigNamesTheWorkerReadRootsOnAWalledRun(t *testing.T) {
	windowsIsNotABench(t)
	bin := nativeHarness(t)
	root, slot := aSlot(t)
	stage := t.TempDir()

	var errOut bytes.Buffer
	_, code := nativeRun(nativeRunConfig{
		binary: bin, model: "fake/fake-model", label: "lbl",
		card:     []byte("FAKE-NORESULT\n"),
		slotDir:  slot,
		root:     root,
		deadline: 30 * time.Second,
		sandbox:  nativeSandbox(t),
		worker:   aReadRootWorker(stage),
	}, &errOut)
	if code != 0 {
		t.Fatalf("the run exits 0, got %d:\n%s", code, errOut.String())
	}
	external := externalDirectoryRules(t, slot)
	// The path itself, and both wildcard spellings, because the harness asks about a
	// directory under either -- FenceReadPatterns is the one place that decides the shapes.
	for _, want := range []string{stage, stage + "/*", stage + "/**"} {
		if external[want] != "allow" {
			t.Errorf("read_roots named %s, so the fence allows %s on a WALLED run; it holds %v", stage, want, external)
		}
	}
}

// TestNativeWallReadsTheWorkerReadRoots is the same declaration at the OTHER fence. A root
// the fence allows and the wall does not is the same dead card with the denial one layer
// down, so both are told or neither is. RED WITHOUT THE FIX: the wall's argv named the slot,
// the harness's own directory, /opt/homebrew and the toolchain roots, and no root of the
// description's.
func TestNativeWallReadsTheWorkerReadRoots(t *testing.T) {
	windowsIsNotABench(t)
	bin := nativeHarness(t)
	_, slot := aSlot(t)
	stage := t.TempDir()
	cfg := nativeRunConfig{slotDir: slot, benchHome: t.TempDir(), benchOS: "linux", worker: aReadRootWorker(stage)}
	argv := nativeSandboxArgv(bin, cfg, slot+"/data", slot+"/jobs/lbl", slot+"/tmp/lbl")

	found := false
	for i := 0; i+1 < len(argv); i++ {
		if argv[i] == "--read" && argv[i+1] == stage {
			found = true
		}
		// AND NEVER A WRITE. A root the desk names is a reference, not a workspace.
		if (argv[i] == "--write" || argv[i] == "--read-noexec") && argv[i+1] == stage {
			t.Errorf("a read root reaches the wall as --read and on no other flag; %s is on %s:\n%s", stage, argv[i], strings.Join(argv, " "))
		}
	}
	if !found {
		t.Errorf("read_roots named %s, so the wall is handed --read %s; the argv reads:\n%s", stage, stage, strings.Join(argv, " "))
	}
}

// TestNativeConfigStillWithholdsTheCardsReadPathsOnAWalledRun is the line this fix does NOT
// cross, pinned so a later widening is a red test rather than a security review nobody asks
// for. A card is the MODEL'S text; a description is a PERSON'S. A fence rule a card can
// widen for itself is no fence, so the card's `READ:` paths stay what they were -- a
// --no-wall affair, where there is no OS wall to open them and the fence is all there is
// (issue #644, TestNativeConfigNamesTheCardsReadPaths).
func TestNativeConfigStillWithholdsTheCardsReadPathsOnAWalledRun(t *testing.T) {
	windowsIsNotABench(t)
	bin := nativeHarness(t)
	root, slot := aSlot(t)

	var errOut bytes.Buffer
	_, code := nativeRun(nativeRunConfig{
		binary: bin, model: "fake/fake-model", label: "lbl",
		card:     []byte("card line 1\nREAD: /sys/kernel/security/lsm\nFAKE-NORESULT\n"),
		slotDir:  slot,
		root:     root,
		deadline: 30 * time.Second,
		sandbox:  nativeSandbox(t),
	}, &errOut)
	if code != 0 {
		t.Fatalf("the run exits 0, got %d:\n%s", code, errOut.String())
	}
	external := externalDirectoryRules(t, slot)
	for _, never := range []string{"/sys/kernel/security/lsm", "/sys/kernel/security/*"} {
		if _, named := external[never]; named {
			t.Errorf("a WALLED run takes no path from the card's own text: %s is named; it holds %v", never, external)
		}
	}
}
