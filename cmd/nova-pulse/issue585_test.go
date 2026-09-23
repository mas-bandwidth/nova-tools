package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestIssue585 reproduces nova-tools#585: "a learned admission checklist in nova-pulse
// cut from the abstain history; preservation tests for handoff records..."
//
// The probe checklist is cut from the abstain history (--history). Without the history,
// the probe has nothing to learn and would refuse every card on a failed file read.
// The cmd cut tier must refuse --probe without --history up front, exit 2 with a line
// that names the missing flag, rather than admit a batch and abort inside pulse.Cut
// where the caller cannot see the class.
func TestIssue585(t *testing.T) {
	dir := t.TempDir()

	pool := writeMainFile(t, dir, "pool.tsv", "example.com/test\ttest1\tfix\tTest fix\tfix\n")

	templates := filepath.Join(dir, "templates")
	if err := os.MkdirAll(templates, 0o755); err != nil {
		t.Fatal(err)
	}
	writeMainFile(t, templates, "models.tsv", "flash opencode/flash\npro opencode/pro\n")
	writeMainFile(t, templates, "fix.md", `RESULT <label> sha=<sha12>
You are a worker. The deadline is the machinery's.
STEP 1. mkdir -p scratch && git clone -q https://github.com/<source>.git . && git checkout -b <branch>
   check: git rev-parse HEAD prints a head.
STEP 2. Make the fix; report the red line and then the green line, one row per item.
STEP last. Write RESULT.md with line 1 equal to this card's line 1.`)

	root := filepath.Join(dir, "root")
	out := filepath.Join(dir, "cards")

	var outb, errb bytes.Buffer
	code := run([]string{"cut", "--probe", "--pool", pool, "--templates", templates, "--out", out, "--root", root}, &outb, &errb, time.Now().UTC())

	if code != 2 {
		t.Errorf("cut --probe without --history: exit = %d, want 2 (the checklist is cut from the abstain history; the tool must refuse --probe without --history rather than probe against nothing)", code)
	}
	if !strings.Contains(errb.String(), "--history") {
		t.Errorf("refusal must name the missing --history flag: %q", errb.String())
	}
}
