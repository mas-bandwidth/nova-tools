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

// ISSUE #644, THE TOP FAILURE CLASS OF 2026-09-16: the harness's own permission fence
// auto-rejected paths the CARD was told to use -- its own `../scratch` beside `repo/`, a
// read-only /sys path on a bench with no wall -- the model stopped, and the batch scored
// eight of thirty cards `no-result`, the token for a model that chose to publish nothing.
//
// Two things are proved here, and each is red without its half of the fix: the config the
// job runs under NAMES THE JOB'S OWN DIRECTORIES, and a rejection that still happens is
// REPORTED on the NATIVE OK line instead of vanishing into an absent result.

// TestNativeConfigNamesTheJobDirectory: every native run writes a harness config, whether or
// not --config named one, and its permission block makes the job's own directories internal
// to the fence, and nothing above them. RED WITHOUT THE CHANGE: before it, a run with no
// --config wrote no config at all and a run with one wrote the provider's bytes with no
// permission block, so what the fence called external was left entirely to the harness.
func TestNativeConfigNamesTheJobDirectory(t *testing.T) {
	windowsIsNotABench(t)
	bin := nativeHarness(t)
	root, slot := aSlot(t)

	var errOut bytes.Buffer
	res, code := nativeRun(nativeRunConfig{
		binary: bin, model: "fake/fake-model", label: "lbl",
		card:     []byte("FAKE-NORESULT\n"),
		slotDir:  slot,
		root:     root,
		deadline: 30 * time.Second, noWall: true,
	}, &errOut)
	if code != 0 {
		t.Fatalf("the run exits 0, got %d:\n%s", code, errOut.String())
	}
	if res.configSHA == "" || res.configSHA == "-" {
		t.Errorf("the NATIVE OK line names the sha8 of the config the child saw, got %q", res.configSHA)
	}

	external := externalDirectoryRules(t, slot)
	job := res.job
	for _, want := range []string{job + "/*", job + "/**"} {
		if external[want] != "allow" {
			t.Errorf("the permission block allows %s (the job's own directory); it holds %v", want, external)
		}
	}
	// AND NOTHING ABOVE THE JOB. The harness resolves a card's `../scratch` after its own
	// `cd repo`, so the job's `jobs` parent buys nothing -- and naming it would hand one
	// card every sibling job in the slot on a bench with no wall.
	jobs := filepath.Dir(job)
	for _, never := range []string{jobs + "/*", jobs + "/**"} {
		if _, named := external[never]; named {
			t.Errorf("%s is above the job and is never named; it holds %v", never, external)
		}
	}
	if external["*"] != "deny" {
		t.Errorf("everything else is denied without prompting (a deny is a tool error the model routes around; an ask auto-rejects and ends the run); it holds %v", external)
	}
}

// TestNativeConfigDeniesExternalPaths: the generated config the child reads DENIES a path
// outside the job, and denies webfetch, so no permission is left to prompt. An `ask` in a
// non-interactive `run` is auto-rejected and the model stops -- the run ends and the card's
// commits are stranded. RED WITHOUT THE CHANGE: the block said `ask` (issue #918).
func TestNativeConfigDeniesExternalPaths(t *testing.T) {
	windowsIsNotABench(t)
	bin := nativeHarness(t)
	root, slot := aSlot(t)

	var errOut bytes.Buffer
	if _, code := nativeRun(nativeRunConfig{
		binary: bin, model: "fake/fake-model", label: "lbl",
		card:     []byte("FAKE-NORESULT\n"),
		slotDir:  slot,
		root:     root,
		deadline: 30 * time.Second, noWall: true,
	}, &errOut); code != 0 {
		t.Fatalf("the run exits 0, got %d:\n%s", code, errOut.String())
	}
	raw, err := os.ReadFile(filepath.Join(slot, "data", ".config", "opencode", "opencode.json"))
	if err != nil {
		t.Fatalf("every native run writes the harness config the job's fence reads: %v", err)
	}
	var cfg struct {
		Permission struct {
			External map[string]string `json:"external_directory"`
			Webfetch string            `json:"webfetch"`
		} `json:"permission"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("the config the child saw is not readable JSON: %v\n%s", err, raw)
	}
	if cfg.Permission.External["*"] != "deny" {
		t.Errorf("a path outside the job is denied, never asked about; it holds %v", cfg.Permission.External)
	}
	if cfg.Permission.Webfetch != "deny" {
		t.Errorf("webfetch is denied too, so no permission is left to prompt; it holds %q", cfg.Permission.Webfetch)
	}
}

// TestNativeConfigNamesTheCardsReadPaths: on a bench with NO OS WALL, a card that names a
// read-only system path on its `READ:` line runs with that path open to the fence -- there
// is no wall to open it, and the second case of #644 was `cat /sys/kernel/security/lsm`
// rejected twice on Space.
func TestNativeConfigNamesTheCardsReadPaths(t *testing.T) {
	windowsIsNotABench(t)
	bin := nativeHarness(t)
	root, slot := aSlot(t)

	var errOut bytes.Buffer
	_, code := nativeRun(nativeRunConfig{
		binary: bin, model: "fake/fake-model", label: "lbl",
		card:     []byte("card line 1\nREAD: /sys/kernel/security/lsm\nFAKE-NORESULT\n"),
		slotDir:  slot,
		root:     root,
		deadline: 30 * time.Second, noWall: true,
	}, &errOut)
	if code != 0 {
		t.Fatalf("the run exits 0, got %d:\n%s", code, errOut.String())
	}
	external := externalDirectoryRules(t, slot)
	// The harness asks about the PARENT of a file it is told to read, with a `/*` on it.
	for _, want := range []string{"/sys/kernel/security/lsm", "/sys/kernel/security/*"} {
		if external[want] != "allow" {
			t.Errorf("the card named READ: /sys/kernel/security/lsm, so %s is allowed; it holds %v", want, external)
		}
	}
}

// TestNativeReportsAFenceRejection: a harness that prints its own rejection line and exits 0
// is reported `fence=rejected path=<p>` on the NATIVE OK line. RED WITHOUT THE CLASSIFIER:
// before it the line said `harness=ok rc=0` and nothing else, and the card was scored
// `no-result` -- a coordinator went and read the model for a fence the machinery built.
func TestNativeReportsAFenceRejection(t *testing.T) {
	windowsIsNotABench(t)
	bin := nativeHarness(t)
	root, slot := aSlot(t)
	cardPath := filepath.Join(root, "card.md")
	if err := os.WriteFile(cardPath, []byte("FAKE-FENCE-REJECT /Users/glenn/rowan-working/swarm-root/1/jobs/*\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	args := []string{"native", "--harness", bin, "--model", "fake/fake-model",
		"--label", "lbl", "--card", cardPath, "--slot", slot, "--root", root,
		"--deadline", "30s", "--no-wall"}
	var stdout, stderr bytes.Buffer
	if rc := run(args, strings.NewReader(""), &stdout, &stderr, time.Now()); rc != 0 {
		t.Fatalf("the run exits 0, got %d:\n%s", rc, stderr.String())
	}
	want := " fence=rejected path=/Users/glenn/rowan-working/swarm-root/1/jobs/*"
	if !strings.Contains(stdout.String(), want) {
		t.Fatalf("the NATIVE OK line carries%s:\n%s", want, stdout.String())
	}
}

// externalDirectoryRules reads the external_directory rules out of the config the child saw.
func externalDirectoryRules(t *testing.T, slot string) map[string]string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(slot, "data", ".config", "opencode", "opencode.json"))
	if err != nil {
		t.Fatalf("every native run writes the harness config the job's fence reads: %v", err)
	}
	var cfg struct {
		Permission struct {
			External map[string]string `json:"external_directory"`
		} `json:"permission"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("the config the child saw is not readable JSON: %v\n%s", err, raw)
	}
	return cfg.Permission.External
}
