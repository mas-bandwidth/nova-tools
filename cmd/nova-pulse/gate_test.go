package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// gate-verb-reads-a-source-file: the whole verb, end to end, with --source in place of gh --
// one GATE line, a STOP whose line 2 is the admission name, and no network anywhere.
func TestGateVerbWritesStopFromASourceFile(t *testing.T) {
	dir := t.TempDir()
	queue := filepath.Join(dir, "queue")
	if err := os.MkdirAll(queue, 0o755); err != nil {
		t.Fatal(err)
	}
	source := writeMainFile(t, dir, "runs.json", `{"runs":[{"databaseId":35120376309,"status":"completed","conclusion":"failure","headSha":"8f3714d1c0de0000","workflowName":"ci","event":"push"}],
	 "job":{"name":"studio-fast","log":"--- FAIL: TestOne\nfix #828 first\n"}}`)

	var out, errb bytes.Buffer
	code := run([]string{"gate", "--repo", "mas-bandwidth/nova-tools", "--branch", "main", "--queue", queue, "--source", source}, &out, &errb, time.Now().UTC())
	if code != 1 {
		t.Fatalf("gate on a red exit = %d, want 1; out=%q err=%q", code, out.String(), errb.String())
	}
	raw, err := os.ReadFile(filepath.Join(queue, "STOP"))
	if err != nil {
		t.Fatalf("no STOP written: %v", err)
	}
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	if len(lines) != 2 || !strings.HasPrefix(lines[0], "MAIN-RED 8f3714d1c0de run=35120376309 ") || lines[1] != "#828" {
		t.Fatalf("STOP = %q", lines)
	}
	if n := len(strings.Fields(strings.TrimSpace(out.String() + errb.String()))); n == 0 {
		t.Fatal("the gate printed nothing; one GATE line is always printed")
	}
	if c := strings.Count(strings.TrimSpace(out.String()+errb.String()), "\n"); c != 0 {
		t.Fatalf("the gate printed more than one line: %q%q", out.String(), errb.String())
	}
}

// gate-refuses-a-guess: every flag the gate needs is required, and the refusal names it.
func TestGateVerbRefusesMissingFlags(t *testing.T) {
	dir := t.TempDir()
	for _, tc := range []struct {
		name, want string
		args       []string
	}{
		{"repo", "--repo", []string{"gate", "--branch", "main", "--queue", dir}},
		{"branch", "--branch", []string{"gate", "--repo", "o/r", "--queue", dir}},
		{"queue", "--queue", []string{"gate", "--repo", "o/r", "--branch", "main"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out, errb bytes.Buffer
			if code := run(tc.args, &out, &errb, time.Now().UTC()); code != 2 {
				t.Fatalf("exit = %d, want 2", code)
			}
			if !strings.Contains(errb.String(), tc.want+" is required") {
				t.Fatalf("stderr = %q, want %s named", errb.String(), tc.want)
			}
		})
	}
}

// help-lists-the-gate: the verb table and the help are one fact, so a shipped verb the help
// does not list is red (bug F: every coordinator action has a verb).
func TestHelpListsGate(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"help"}, &out, &errb, time.Now().UTC()); code != 0 {
		t.Fatalf("help exit = %d", code)
	}
	found := false
	for _, line := range strings.Split(out.String(), "\n") {
		f := strings.Fields(line)
		if len(f) >= 2 && f[0] == "nova-pulse" && f[1] == "gate" {
			found = true
			if strings.Contains(line, "not yet implemented") {
				t.Errorf("gate is shipped and help marks it unshipped: %q", line)
			}
		}
	}
	if !found {
		t.Fatalf("help does not list the gate verb: %q", out.String())
	}
}
