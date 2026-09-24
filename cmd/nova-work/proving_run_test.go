package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/workreconcile"
)

func TestCmdProvingRunCLI(t *testing.T) {
	tempDir := t.TempDir()

	// 1. Test missing flag
	var stdout, stderr bytes.Buffer
	code := cmdProvingRun([]string{}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("expected exit 2 on missing --nova-tools, got %d", code)
	}
	if !strings.Contains(stderr.String(), "nova-work proving-run: --nova-tools <path> is required") {
		t.Fatalf("expected refusal on stderr, got: %s", stderr.String())
	}

	// 2. Prepare test manifest
	ntManifest := workreconcile.CaptureManifest{
		Provider:    "github",
		Repo:        "mas-bandwidth/nova-tools",
		FetchedAt:   "2026-09-20T12:00:00Z",
		TotalIssues: 2,
		Issues: []workreconcile.CapturedIssue{
			{
				Provider:     "github",
				Repo:         "mas-bandwidth/nova-tools",
				Number:       1945,
				Title:        "fill: fail-closed load, capacity from the slot store",
				State:        "closed",
				Revision:     "rev-1945",
				CommentCount: 3,
				Labels:       []string{"fill", "landed"},
				URL:          "https://github.com/mas-bandwidth/nova-tools/issues/1945",
			},
			{
				Provider:     "github",
				Repo:         "mas-bandwidth/nova-tools",
				Number:       2029,
				Title:        "fill: one dealer at a time",
				State:        "open",
				Revision:     "rev-2029",
				CommentCount: 1,
				Labels:       []string{"dealer"},
				URL:          "https://github.com/mas-bandwidth/nova-tools/issues/2029",
			},
		},
	}
	ntRaw, _ := json.Marshal(ntManifest)
	ntFile := filepath.Join(tempDir, "nt_capture.json")
	if err := os.WriteFile(ntFile, ntRaw, 0644); err != nil {
		t.Fatal(err)
	}

	// Sprint file
	sprintContent := `
 1. #1945  fill: fail-closed load
 2. #2029  fill: one dealer at a time
`
	sprintFile := filepath.Join(tempDir, "sprint.txt")
	if err := os.WriteFile(sprintFile, []byte(sprintContent), 0644); err != nil {
		t.Fatal(err)
	}

	// Run proving-run
	stdout.Reset()
	stderr.Reset()
	code = cmdProvingRun([]string{
		"--nova-tools", ntFile,
		"--sprint", sprintFile,
		"--batch-size", "1",
		"--interrupt=false",
	}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("expected exit 0, got %d; stderr=%s", code, stderr.String())
	}

	out := stdout.String()
	if !strings.Contains(out, "RECONCILE repo=mas-bandwidth/nova-tools captured=2 imported=2 discrepancies=0 result=PASS") {
		t.Errorf("missing reconcile pass in stdout:\n%s", out)
	}
	if !strings.Contains(out, "SPRINT rows_completed=1 rows_total=2 percent=50.0") {
		t.Errorf("missing sprint completion metrics in stdout:\n%s", out)
	}
	if !strings.Contains(out, "PROVING RUN result=PASS") {
		t.Errorf("missing PROVING RUN result=PASS in stdout:\n%s", out)
	}
}
