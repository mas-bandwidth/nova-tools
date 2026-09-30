package main

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"
)

type jsonOutput struct {
	Result struct {
		Verb   string `json:"verb"`
		Status string `json:"status"`
		Exit   int    `json:"exit"`
		Remedy string `json:"remedy,omitempty"`
	} `json:"result"`
	Facts map[string]string `json:"facts"`
	Items []struct {
		Kind   string            `json:"kind"`
		Fields map[string]string `json:"fields,omitempty"`
		Text   string            `json:"text,omitempty"`
	} `json:"items,omitempty"`
	More []struct {
		Kind   string `json:"kind"`
		Shown  int    `json:"shown"`
		Total  int    `json:"total"`
		Remedy string `json:"remedy,omitempty"`
	} `json:"more,omitempty"`
	Notes []string `json:"notes,omitempty"`
}

func parseJSONOutput(t *testing.T, raw string) jsonOutput {
	t.Helper()
	var out jsonOutput
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("failed to parse JSON %v:\nraw output: %s", err, raw)
	}
	return out
}

func runFuseWithNow(now time.Time, args ...string) (exit int, stdout, stderr string) {
	var out, errb bytes.Buffer
	exit = run(args, &out, &errb, now)
	return exit, out.String(), errb.String()
}

func TestCheckJSON(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	box := filepath.Join(dir, "box.json")
	now := time.Date(2026, 9, 9, 18, 27, 40, 0, time.UTC)

	// Init box
	exit, stdout, stderr := runFuseWithNow(now, "init", "--box", box, "--json")
	if exit != 0 {
		t.Fatalf("init --json failed exit=%d stderr=%s", exit, stderr)
	}
	initOut := parseJSONOutput(t, stdout)
	if initOut.Result.Verb != "init" || initOut.Result.Status != "ok" || initOut.Result.Exit != 0 {
		t.Errorf("unexpected init output: %+v", initOut.Result)
	}
	if initOut.Facts["box"] != box {
		t.Errorf("init facts.box = %q, want %q", initOut.Facts["box"], box)
	}

	// Check clear without surface
	exit, stdout, stderr = runFuseWithNow(now, "check", "--box", box, "--json")
	if exit != 0 {
		t.Fatalf("check --json failed exit=%d stderr=%s", exit, stderr)
	}
	chk := parseJSONOutput(t, stdout)
	if chk.Result.Verb != "check" || chk.Result.Status != "ok" || chk.Result.Exit != 0 {
		t.Errorf("unexpected check output: %+v", chk.Result)
	}
	if chk.Facts["box"] != box || chk.Facts["lockdown"] != "clear" {
		t.Errorf("unexpected facts: %+v", chk.Facts)
	}

	// Check clear with surface
	exit, stdout, stderr = runFuseWithNow(now, "check", "--box", box, "--json", "a-surface")
	if exit != 0 {
		t.Fatalf("check with surface --json failed exit=%d stderr=%s", exit, stderr)
	}
	chk = parseJSONOutput(t, stdout)
	if chk.Result.Verb != "check" || chk.Result.Status != "ok" || chk.Result.Exit != 0 {
		t.Errorf("unexpected check output: %+v", chk.Result)
	}
	if chk.Facts["surface"] != "a-surface" || chk.Facts["box"] != box || chk.Facts["lockdown"] != "clear" || chk.Facts["quarantine"] != "clear" {
		t.Errorf("unexpected facts: %+v", chk.Facts)
	}

	// Quarantine surface
	exit, stdout, stderr = runFuseWithNow(now, "quarantine", "--box", box, "--json", "a-surface", "test reason")
	if exit != 0 {
		t.Fatalf("quarantine --json failed exit=%d stderr=%s", exit, stderr)
	}
	qOut := parseJSONOutput(t, stdout)
	if qOut.Result.Verb != "quarantine" || qOut.Result.Status != "ok" || qOut.Result.Exit != 0 {
		t.Errorf("unexpected quarantine output: %+v", qOut.Result)
	}
	if qOut.Facts["surface"] != "a-surface" || qOut.Facts["reason"] != "test reason" {
		t.Errorf("unexpected quarantine facts: %+v", qOut.Facts)
	}

	// Check quarantined surface -> exit 1, failed
	exit, stdout, stderr = runFuseWithNow(now, "check", "--box", box, "--json", "a-surface")
	if exit != 1 {
		t.Fatalf("check quarantined surface want exit=1, got %d; stderr=%s", exit, stderr)
	}
	chk = parseJSONOutput(t, stdout)
	if chk.Result.Verb != "check" || chk.Result.Status != "failed" || chk.Result.Exit != 1 {
		t.Errorf("unexpected check quarantined result: %+v", chk.Result)
	}
	if chk.Facts["surface"] != "a-surface" || chk.Facts["box"] != box || chk.Facts["quarantine"] != "blown" {
		t.Errorf("unexpected check quarantined facts: %+v", chk.Facts)
	}

	// Lift quarantine
	exit, stdout, stderr = runFuseWithNow(now, "lift", "quarantine", "--box", box, "--json", "a-surface")
	if exit != 0 {
		t.Fatalf("lift quarantine --json failed exit=%d stderr=%s", exit, stderr)
	}
	lOut := parseJSONOutput(t, stdout)
	if lOut.Result.Verb != "lift" || lOut.Result.Status != "ok" || lOut.Result.Exit != 0 {
		t.Errorf("unexpected lift output: %+v", lOut.Result)
	}
	if lOut.Facts["surface"] != "a-surface" || lOut.Facts["box"] != box {
		t.Errorf("unexpected lift facts: %+v", lOut.Facts)
	}

	// Lockdown
	exit, stdout, stderr = runFuseWithNow(now, "lockdown", "--box", box, "--json", "lockdown reason")
	if exit != 0 {
		t.Fatalf("lockdown --json failed exit=%d stderr=%s", exit, stderr)
	}
	ldOut := parseJSONOutput(t, stdout)
	if ldOut.Result.Verb != "lockdown" || ldOut.Result.Status != "ok" || ldOut.Result.Exit != 0 {
		t.Errorf("unexpected lockdown output: %+v", ldOut.Result)
	}
	if ldOut.Facts["lockdown"] != "blown" || ldOut.Facts["reason"] != "lockdown reason" {
		t.Errorf("unexpected lockdown facts: %+v", ldOut.Facts)
	}

	// Check under lockdown -> exit 1, failed
	exit, stdout, stderr = runFuseWithNow(now, "check", "--box", box, "--json", "any-surface")
	if exit != 1 {
		t.Fatalf("check under lockdown want exit=1, got %d; stderr=%s", exit, stderr)
	}
	chk = parseJSONOutput(t, stdout)
	if chk.Result.Verb != "check" || chk.Result.Status != "failed" || chk.Result.Exit != 1 {
		t.Errorf("unexpected check under lockdown result: %+v", chk.Result)
	}
	if chk.Facts["lockdown"] != "blown" || chk.Facts["surface"] != "any-surface" {
		t.Errorf("unexpected check under lockdown facts: %+v", chk.Facts)
	}

	// Missing --box check --json -> exit 2, refused
	exit, stdout, stderr = runFuseWithNow(now, "check", "--json")
	if exit != 2 {
		t.Fatalf("check --json without box want exit=2, got %d; stderr=%s", exit, stderr)
	}
	chk = parseJSONOutput(t, stdout)
	if chk.Result.Verb != "check" || chk.Result.Status != "refused" || chk.Result.Exit != 2 {
		t.Errorf("unexpected check missing box result: %+v", chk.Result)
	}
	if chk.Result.Remedy != "nova-fuse help" {
		t.Errorf("remedy = %q, want 'nova-fuse help'", chk.Result.Remedy)
	}
}

func TestStatusJSON(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	box := filepath.Join(dir, "box.json")
	now := time.Date(2026, 9, 9, 18, 27, 40, 0, time.UTC)

	// Init
	exit, _, stderr := runFuseWithNow(now, "init", "--box", box)
	if exit != 0 {
		t.Fatalf("init failed: %s", stderr)
	}

	// Add 3 quarantines
	for _, s := range []string{"surf1", "surf2", "surf3"} {
		exit, _, stderr = runFuseWithNow(now, "quarantine", "--box", box, s, "reason "+s)
		if exit != 0 {
			t.Fatalf("quarantine %s failed: %s", s, stderr)
		}
	}

	// Status with max 2
	exit, stdout, stderr := runFuseWithNow(now, "status", "--box", box, "--max", "2", "--json")
	if exit != 0 {
		t.Fatalf("status --json failed exit=%d stderr=%s", exit, stderr)
	}
	st := parseJSONOutput(t, stdout)
	if st.Result.Verb != "status" || st.Result.Status != "ok" || st.Result.Exit != 0 {
		t.Errorf("unexpected status result: %+v", st.Result)
	}
	if st.Facts["lockdown"] != "clear" || st.Facts["quarantines"] != "3" {
		t.Errorf("unexpected status facts: %+v", st.Facts)
	}
	if len(st.Items) != 2 {
		t.Errorf("expected 2 items, got %d", len(st.Items))
	}
	if len(st.More) != 1 || st.More[0].Shown != 2 || st.More[0].Total != 3 {
		t.Errorf("unexpected more: %+v", st.More)
	}

	// Status with max 0 (all)
	exit, stdout, stderr = runFuseWithNow(now, "status", "--box", box, "--max", "0", "--json")
	if exit != 0 {
		t.Fatalf("status --max 0 failed: %s", stderr)
	}
	st = parseJSONOutput(t, stdout)
	if len(st.Items) != 3 {
		t.Errorf("expected 3 items with --max 0, got %d", len(st.Items))
	}
	if len(st.More) != 0 {
		t.Errorf("expected 0 more with --max 0, got %d", len(st.More))
	}
}

func TestLiftLockdownJSON(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 9, 18, 27, 40, 0, time.UTC)

	exit, stdout, _ := runFuseWithNow(now, "lift", "lockdown", "--json")
	if exit != 2 {
		t.Fatalf("lift lockdown --json exit = %d, want 2", exit)
	}
	res := parseJSONOutput(t, stdout)
	if res.Result.Verb != "lift" || res.Result.Status != "refused" || res.Result.Exit != 2 {
		t.Errorf("unexpected lift lockdown result: %+v", res.Result)
	}
	if res.Facts["power"] != "lockdown" {
		t.Errorf("expected power=lockdown, got %v", res.Facts)
	}
}

func TestPathJSON(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	box := filepath.Join(dir, "box.json")
	now := time.Date(2026, 9, 9, 18, 27, 40, 0, time.UTC)

	exit, stdout, stderr := runFuseWithNow(now, "path", "--box", box, "--json")
	if exit != 0 {
		t.Fatalf("path --json failed exit=%d stderr=%s", exit, stderr)
	}
	res := parseJSONOutput(t, stdout)
	if res.Result.Verb != "path" || res.Result.Status != "ok" || res.Result.Exit != 0 {
		t.Errorf("unexpected path result: %+v", res.Result)
	}
	if res.Facts["path"] != box || res.Facts["box"] != box {
		t.Errorf("unexpected path facts: %+v", res.Facts)
	}
}
