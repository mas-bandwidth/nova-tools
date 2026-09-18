package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A GATE THAT DID NOT EXECUTE ITS COMMAND CANNOT RETURN OK (issue #1465).
//
// The run this closes: a `native` Go card was handed GOMODCACHE, GOCACHE and
// GOTOOLCHAIN=local, wrote its test, and could not compile it --
//
//	/usr/bin/bash: line 1: /home/glenn/go/bin/go: Permission denied
//
// -- because the toolchain those three names are FOR is under no root the wall admits. The
// card said so in its own RESULT.md, and the tool said
//
//	NATIVE OK label=go-writer-bound-count ... rc=0 wall=743s sandbox=landlock harness=ok
//
// usd 0.0993, and a commit nobody had compiled read as green. The card was honest; every
// machine-readable field lied. A coordinator reading dispositions and not prose ships it.
//
// The class is made impossible here rather than papered over: a run whose capture holds the
// wall refusing the card's own shell a path it tried to RUN is a HARD REFUSAL. There is no
// NATIVE OK line at all, the exit is non-zero, and the one line names the path, the step and
// the roots to open.
func TestNativeGateThatCouldNotRunIsNeverOK(t *testing.T) {
	windowsIsNotABench(t)
	bin := nativeHarness(t)
	root, slot := aSlot(t)
	const refused = "/opt/sdk/go1.26.5/bin/go"
	cardPath := filepath.Join(root, "card.md")
	// The card publishes a RESULT.md the way the real one did, so this is the SILENT shape
	// and not a wall death: the harness spoke, the result exists, the child exited 0.
	if err := os.WriteFile(cardPath, []byte("FAKE-EXEC-REFUSED "+refused+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	args := []string{"native", "--harness", bin, "--model", "fake/fake-model",
		"--label", "go-card", "--card", cardPath, "--slot", slot, "--root", root,
		"--deadline", "30s", "--no-wall"}
	var stdout, stderr bytes.Buffer
	rc := run(args, strings.NewReader(""), &stdout, &stderr, time.Now())

	if strings.Contains(stdout.String(), "NATIVE OK") {
		t.Errorf("the gate never executed its command and the run still reported OK:\n%s", stdout.String())
	}
	if rc == 0 {
		t.Errorf("a run whose gate could not execute exits non-zero, got %d:\n%s%s", rc, stdout.String(), stderr.String())
	}
	line := stderr.String()
	if !strings.Contains(line, "NATIVE REFUSED") {
		t.Fatalf("the run owes one refusal line naming the class:\n%s", line)
	}
	for _, want := range []string{refused, "read_roots", "step=3"} {
		if !strings.Contains(line, want) {
			t.Errorf("the refusal names %q; it reads:\n%s", want, line)
		}
	}
	// THE WORK IS NOT THROWN AWAY. The child ran and was paid for: the job directory, its
	// result and its usage row are all still named, so a coordinator can harvest what the
	// card did manage before the gate died.
	if !strings.Contains(line, filepath.Join(slot, "jobs", "go-card")) {
		t.Errorf("the refusal names the job directory so the spend is harvestable; it reads:\n%s", line)
	}
	if _, err := os.Stat(filepath.Join(slot, "jobs", "go-card", "RESULT.md")); err != nil {
		t.Errorf("the card's own result is left where it was published: %v", err)
	}
}

// TestNativeOrdinaryRunIsStillOK: the guard above fires on the class and on nothing else. A
// card that merely prints the words -- a refused READ it routed around, its own prose -- is
// a finished run and still says OK.
func TestNativeOrdinaryRunIsStillOK(t *testing.T) {
	windowsIsNotABench(t)
	bin := nativeHarness(t)
	root, slot := aSlot(t)
	cardPath := filepath.Join(root, "card.md")
	if err := os.WriteFile(cardPath, []byte("FAKE-REFUSE 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	args := []string{"native", "--harness", bin, "--model", "fake/fake-model",
		"--label", "read-card", "--card", cardPath, "--slot", slot, "--root", root,
		"--deadline", "30s", "--no-wall"}
	var stdout, stderr bytes.Buffer
	if rc := run(args, strings.NewReader(""), &stdout, &stderr, time.Now()); rc != 0 {
		t.Fatalf("a card that was refused a READ and carried on is a finished run, got %d:\n%s%s", rc, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "NATIVE OK ") {
		t.Fatalf("the NATIVE OK line is printed for a run whose gate did execute:\n%s", stdout.String())
	}
}

// THE READ ROOTS THE DESK NAMED REACH BOTH FENCES (issue #1463).
//
// A worker description's `read_roots` is the desk's own declaration of what a job may READ:
// a bench-local mirror, a corpus, a toolchain. `worker check` accepted it, and then neither
// fence was told. The OS wall's read set was built without it, and the HARNESS's own
// permission block -- which took the card's `READ:` paths on a `--no-wall` run only -- was
// built without it too, so every read of the staged path was auto-rejected and a card came
// back with the whole row owed after 148 seconds.
//
// Both fences now hear it, on a walled run as much as an unwalled one. The wall is still the
// real boundary (SPEC-SANDBOX rule 1); the harness's fence is a second, weaker one, and a
// second fence that denies what the first one grants can only cost cards.
func TestNativeWalledRunOpensTheWorkersReadRoots(t *testing.T) {
	windowsIsNotABench(t)
	bin := nativeHarness(t)
	t.Setenv("NOVA_FAKE_SANDBOX", "pass")
	t.Setenv("FAKE_KEY", fakeKey)
	root, slot := aSlot(t)
	stage, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	worker := workerWithReadRoots(t, stage)
	cardPath := filepath.Join(root, "card.md")
	if err := os.WriteFile(cardPath, []byte("a card line 1\nline 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	args := []string{"native", "--harness", bin, "--worker", worker,
		"--label", "staged", "--card", cardPath, "--slot", slot, "--root", root,
		"--deadline", "30s", "--sandbox", nativeSandbox(t)}
	var stdout, stderr bytes.Buffer
	if rc := run(args, strings.NewReader(""), &stdout, &stderr, time.Now()); rc != 0 {
		t.Fatalf("the walled run exits 0, got %d:\n%s%s", rc, stdout.String(), stderr.String())
	}
	// THE OS WALL. The read root is a --read, beside the slot directory and the harness's own.
	argv := sandboxArgv(t, filepath.Join(slot, "jobs", "staged"))
	if !strings.Contains(argv, "--read "+stage) {
		t.Errorf("the wall argv does not read the description's read_roots entry %s:\n%s", stage, argv)
	}
	// THE HARNESS'S OWN FENCE. The same root, in the permission block the child reads, on a
	// WALLED run -- which is the whole of #1463.
	external := externalDirectoryRules(t, slot)
	for _, want := range []string{stage, stage + "/*"} {
		if external[want] != "allow" {
			t.Errorf("the harness fence does not admit the read root %s on a walled run; it holds %v", want, external)
		}
	}
}

// workerWithReadRoots writes a worker description whose read_roots names one directory: the
// shape a coordinator hands a card a staging mirror, a corpus or a toolchain with.
func workerWithReadRoots(t *testing.T, roots ...string) string {
	t.Helper()
	home := t.TempDir()
	desc := map[string]any{
		"name": "fake-1", "provider": "fake", "model": "fake-model",
		"env_var": "FAKE_KEY", "secret": "FAKE_KEY", "usage": "opencode",
		"harness": "fake-harness", "worker_dir": home, "deadline": "30s",
		"harness_args": []string{"run", "--model", "{model}", "--", "{prompt}"},
		"read_roots":   roots,
	}
	raw, err := json.MarshalIndent(desc, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "worker.json")
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}
