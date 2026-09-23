package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Issue #2045: launch: ONE launch verb replaces 27 launcher scripts (10 linux,
// 7 darwin twins, 5 Studio, rr/rrpro/rr-run): provider from a registry, any
// OS, local or ssh, output kept, no card lost.
//
// Johnny's hold on PR #2885: the first cut hashed the label and printed LAUNCH
// OK without running anything. These tests pin that the verb RUNS the card on
// the selected row (locally through `native`, or over ssh), that the output is
// kept under the results root, and that LAUNCH OK is printed only after a run
// that exited 0. Stella's hold: a malformed registry row never selects.

// TestIssue2045 runs a real card locally on the selected row: the fake harness
// actually starts, native publishes its report under the results root, and
// LAUNCH OK follows the run.
func TestIssue2045(t *testing.T) {
	windowsIsNotABench(t)
	bin := nativeHarness(t)
	root, slot := aSlot(t)
	resultsRoot := filepath.Join(t.TempDir(), "results")
	registry := filepath.Join(t.TempDir(), "providers.tsv")
	write(t, registry, "fake\tfake/fake-model\t"+bin+"\n")
	card := filepath.Join(root, "card.md")
	write(t, card, "FAKE-SAY launch-ran-the-card\nRESULT: c2045 launch verb\n")

	exit, stdout, stderr := runSwarm(t, "launch",
		"--providers", registry, "--label", "l2045", "--card", card, "--",
		"--tokens", "unmetered", "--slots-store", nativeStore(t), "--owner", "fake-1",
		"--slot", slot, "--root", root, "--deadline", "30s", "--idle", "0", "--no-wall",
		"--results-root", resultsRoot)
	if exit == 2 && strings.Contains(stderr, "unknown subcommand") {
		t.Fatalf("nova-swarm launch not recognized (issue #2045); stderr: %s", stderr)
	}
	if exit != 0 {
		t.Fatalf("launch must exit 0, got %d:\nstdout: %s\nstderr: %s", exit, stdout, stderr)
	}
	if !strings.Contains(stdout, "NATIVE OK ") {
		t.Fatalf("launch must run the card through native:\nstdout: %s\nstderr: %s", stdout, stderr)
	}
	if strings.Index(stdout, "LAUNCH OK") < strings.Index(stdout, "NATIVE OK ") {
		t.Fatalf("LAUNCH OK must follow the run, not precede it:\n%s", stdout)
	}
	for _, want := range []string{"provider=fake", "model=fake/fake-model", "where=local", "ran=yes", "rc=0"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("LAUNCH OK must carry %s:\n%s", want, stdout)
		}
	}
	attempt := oneRunAttempt(t, resultsRoot, "l2045")
	report, err := os.ReadFile(filepath.Join(attempt, "report"))
	if err != nil {
		t.Fatalf("the output must be kept under the results root: %v", err)
	}
	if !strings.Contains(string(report), "launch-ran-the-card") {
		t.Fatalf("the kept report does not carry what the harness said:\n%s", report)
	}
}

// A run that fails is LAUNCH FAILED with the native exit code, never LAUNCH OK.
func TestIssue2045FailedRunIsNotOK(t *testing.T) {
	windowsIsNotABench(t)
	root, slot := aSlot(t)
	registry := filepath.Join(t.TempDir(), "providers.tsv")
	write(t, registry, "fake\tfake/fake-model\t"+filepath.Join(root, "no-such-harness")+"\n")
	card := filepath.Join(root, "card.md")
	write(t, card, "RESULT: c2045 failed\n")
	exit, stdout, stderr := runSwarm(t, "launch",
		"--providers", registry, "--label", "l2045f", "--card", card, "--",
		"--tokens", "unmetered", "--slots-store", nativeStore(t), "--owner", "fake-1",
		"--slot", slot, "--root", root, "--deadline", "30s", "--no-wall")
	if exit == 0 || strings.Contains(stdout, "LAUNCH OK") {
		t.Fatalf("a run that did not start must not be LAUNCH OK: exit=%d\n%s\n%s", exit, stdout, stderr)
	}
	if !strings.Contains(stdout, "LAUNCH FAILED") {
		t.Fatalf("a failed run prints LAUNCH FAILED:\n%s\n%s", stdout, stderr)
	}
}

// A host column runs the same native verb on that host over ssh, with the card
// on standard input and the row's harness and model on the remote argv.
func TestIssue2045SSHRow(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake ssh is a POSIX shell script")
	}
	dir := t.TempDir()
	argvLog := filepath.Join(dir, "argv")
	stdinLog := filepath.Join(dir, "stdin")
	fakeSSH := filepath.Join(dir, "ssh")
	write(t, fakeSSH, "#!/bin/sh\nprintf '%s\\n' \"$@\" > "+argvLog+"\ncat > "+stdinLog+"\necho 'NATIVE OK remote'\n")
	if err := os.Chmod(fakeSSH, 0o755); err != nil {
		t.Fatal(err)
	}
	registry := filepath.Join(dir, "providers.tsv")
	write(t, registry, "pro\topenai/gpt-4o\t/opt/bin/opencode\tbench@hulk\n")
	card := filepath.Join(dir, "card.md")
	write(t, card, "RESULT: c2045 over ssh\n")

	exit, stdout, stderr := runSwarm(t, "launch", "--ssh", fakeSSH,
		"--providers", registry, "--label", "remote", "--card", card, "--",
		"--deadline", "5m")
	if exit != 0 || !strings.Contains(stdout, "LAUNCH OK") || !strings.Contains(stdout, "where=ssh:bench@hulk") {
		t.Fatalf("ssh row must run and print LAUNCH OK: exit=%d\n%s\n%s", exit, stdout, stderr)
	}
	argv, _ := os.ReadFile(argvLog)
	for _, want := range []string{"bench@hulk", "'nova-swarm' 'native'", "'--harness' '/opt/bin/opencode'", "'--model' 'openai/gpt-4o'", "'--card' '/dev/stdin'", "'--deadline' '5m'"} {
		if !strings.Contains(string(argv), want) {
			t.Errorf("remote argv must carry %s:\n%s", want, argv)
		}
	}
	got, _ := os.ReadFile(stdinLog)
	if string(got) != "RESULT: c2045 over ssh\n" {
		t.Errorf("the card must reach the remote on stdin, got %q", got)
	}
}

// Determinism: the same label must always select the same provider row.
// --dry-run selects without running and never says LAUNCH OK.
func TestIssue2045Deterministic(t *testing.T) {
	dir := t.TempDir()
	registry := filepath.Join(dir, "providers.tsv")
	write(t, registry, strings.Join([]string{
		"flash\tanthropic/claude-sonnet-4-20250514\topencode",
		"pro\topenai/gpt-4o\topencode",
		"ds\tdeepseek/deepseek-r1\topencode",
	}, "\n")+"\n")
	card := filepath.Join(dir, "card.md")
	write(t, card, "RESULT: c2045 deterministic\nKIND: fix\n")

	_, stdout1, _ := runSwarm(t, "launch", "--dry-run", "--providers", registry, "--label", "same-label", "--card", card)
	_, stdout2, _ := runSwarm(t, "launch", "--dry-run", "--providers", registry, "--label", "same-label", "--card", card)
	p1 := extractField(stdout1, "provider=")
	p2 := extractField(stdout2, "provider=")
	if p1 == "" || p2 == "" {
		t.Fatalf("both runs must carry provider=; first:\n%s\nsecond:\n%s", stdout1, stdout2)
	}
	if p1 != p2 {
		t.Errorf("same label must select same provider: first=%q second=%q", p1, p2)
	}
	if strings.Contains(stdout1, "LAUNCH OK") || !strings.Contains(stdout1, "LAUNCH DRY") {
		t.Errorf("a dry run is LAUNCH DRY, never LAUNCH OK:\n%s", stdout1)
	}
}

// Stella's hold: two-column rows and rows with an empty field are refused and
// cannot produce LAUNCH OK.
func TestIssue2045MalformedRowsRefused(t *testing.T) {
	dir := t.TempDir()
	card := filepath.Join(dir, "card.md")
	write(t, card, "RESULT: c2045 malformed\n")
	for name, body := range map[string]string{
		"two-columns":   "flash\tanthropic/claude-sonnet-4-20250514\n",
		"empty-harness": "flash\tanthropic/claude-sonnet-4-20250514\t\n",
		"empty-model":   "flash\t\topencode\n",
		"five-columns":  "flash\tm/x\topencode\thost\textra\n",
	} {
		registry := filepath.Join(dir, name+".tsv")
		write(t, registry, body)
		exit, stdout, stderr := runSwarm(t, "launch", "--dry-run", "--providers", registry, "--label", "x", "--card", card)
		if exit != 2 || strings.Contains(stdout, "LAUNCH") {
			t.Errorf("%s: a malformed row must be refused (exit 2, no LAUNCH line): exit=%d\n%s\n%s", name, exit, stdout, stderr)
		}
		if !strings.Contains(stderr, "line 1") {
			t.Errorf("%s: the refusal must name the line:\n%s", name, stderr)
		}
	}
}

// extractField pulls the value of a field from a whitespace-separated line.
func extractField(line, prefix string) string {
	for _, f := range strings.Fields(line) {
		if strings.HasPrefix(f, prefix) {
			return strings.TrimPrefix(f, prefix)
		}
	}
	return ""
}
