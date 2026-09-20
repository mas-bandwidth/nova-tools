package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/capture"
)

func TestCaptureCLIVerb(t *testing.T) {
	// Fake script simulating gh
	tmpDir := t.TempDir()
	fakeGHScript := filepath.Join(tmpDir, "fakegh.sh")
	scriptContent := `#!/bin/sh
if echo "$*" | grep -q "issues?state=all"; then
  echo '[{"number":2080,"node_id":"I_kw2080","id":2080,"title":"Test Issue","html_url":"https://github.com/mas-bandwidth/nova-tools/issues/2080","state":"open","body":"Issue body","created_at":"2026-09-20T10:00:00Z","updated_at":"2026-09-20T10:00:00Z","user":{"login":"rowan"},"labels":[]}]'
elif echo "$*" | grep -q "comments"; then
  echo '[]'
elif echo "$*" | grep -q "timeline"; then
  echo '[]'
else
  echo '[]'
fi
`
	if err := os.WriteFile(fakeGHScript, []byte(scriptContent), 0o755); err != nil {
		t.Fatalf("writing fake gh: %v", err)
	}

	restore := capture.SetGHBinary(fakeGHScript)
	defer restore()

	stagingDir := filepath.Join(tmpDir, "staging")

	// Missing flags
	var stdout, stderr bytes.Buffer
	rc := run([]string{"capture"}, &stdout, &stderr)
	if rc != 2 {
		t.Fatalf("run capture with no flags rc = %d; want 2", rc)
	}

	// Missing repo
	stdout.Reset()
	stderr.Reset()
	rc = run([]string{"capture", "--into", stagingDir}, &stdout, &stderr)
	if rc != 2 || !strings.Contains(stderr.String(), "--repo") {
		t.Fatalf("run capture with missing repo rc = %d, err = %s", rc, stderr.String())
	}

	// Missing into
	stdout.Reset()
	stderr.Reset()
	rc = run([]string{"capture", "--repo", "mas-bandwidth/nova-tools"}, &stdout, &stderr)
	if rc != 2 || !strings.Contains(stderr.String(), "--into") {
		t.Fatalf("run capture with missing into rc = %d, err = %s", rc, stderr.String())
	}

	// Successful capture
	stdout.Reset()
	stderr.Reset()
	rc = run([]string{"capture", "--repo", "mas-bandwidth/nova-tools", "--into", stagingDir}, &stdout, &stderr)
	if rc != 0 {
		t.Fatalf("run capture rc = %d; stderr = %s", rc, stderr.String())
	}
	outStr := stdout.String()
	if !strings.Contains(outStr, "CAPTURE OK") || !strings.Contains(outStr, "repo=mas-bandwidth/nova-tools") {
		t.Fatalf("unexpected stdout = %q", outStr)
	}

	// Verify files created
	manifestPath := filepath.Join(stagingDir, "manifest.json")
	if _, err := os.Stat(manifestPath); err != nil {
		t.Fatalf("manifest not found: %v", err)
	}
	issuePath := filepath.Join(stagingDir, "issues", "issue-2080.json")
	if _, err := os.Stat(issuePath); err != nil {
		t.Fatalf("issue file not found: %v", err)
	}
}
