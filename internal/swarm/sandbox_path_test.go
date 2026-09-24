package swarm

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sandbox"
)

// Issue #3501 / SWARM GATE #3501: inside the card sandbox, verbs installed in ~/.local/bin
// (and entries on PATH) must resolve and execute by name, but their files cannot be
// inspected (file-read-metadata granted on PATH entries, file-read* denied).
func TestDarwinProfileGrantsMetadataOnInheritedPathEntries(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skipf("skipped on %s: the darwin SBPL profile is macOS-specific", runtime.GOOS)
	}

	// Create a test directory representing a toolchain bin on PATH (like ~/.local/bin)
	tempDir := t.TempDir()
	binDir := filepath.Join(tempDir, "user", "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	fakeTool := filepath.Join(binDir, "my-tool")
	if err := os.WriteFile(fakeTool, []byte("#!/bin/sh\necho ok\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	jobDir := t.TempDir()
	dataHome := filepath.Join(jobDir, "data")
	if err := os.MkdirAll(dataHome, 0o755); err != nil {
		t.Fatal(err)
	}

	// Policy built with LookAt containing binDir
	p, bad := sandbox.Build(sandbox.Input{
		Writes: []string{jobDir, dataHome},
		Home:   dataHome,
		LookAt: binDir,
		Argv:   []string{"/bin/sh", "-c", "echo test"},
	})
	if len(bad) > 0 {
		t.Fatalf("sandbox.Build failed: %v", bad)
	}

	text, _, err := sandbox.DarwinProfile(p)
	if err != nil {
		t.Fatalf("sandbox.DarwinProfile failed: %v", err)
	}

	// 1. Must grant file-read-metadata subpath on the PATH directory itself
	wantSubpath := `(allow file-read-metadata (subpath "` + binDir + `"))`
	if !strings.Contains(text, wantSubpath) {
		t.Errorf("DarwinProfile does not contain file-read-metadata subpath for PATH entry %s\nProfile:\n%s", binDir, text)
	}

	// 2. Must grant file-read-metadata literal on all proper ancestors
	for _, ancestor := range sandbox.Ancestors(binDir) {
		wantLiteral := `(allow file-read-metadata (literal "` + ancestor + `"))`
		if !strings.Contains(text, wantLiteral) {
			t.Errorf("DarwinProfile does not contain file-read-metadata literal for ancestor %s\nProfile:\n%s", ancestor, text)
		}
	}

	// 3. Must NOT grant file-read* (subpath binDir) — files cannot be inspected
	badRead := `(allow file-read* (subpath "` + binDir + `"))`
	if strings.Contains(text, badRead) {
		t.Errorf("DarwinProfile granted full file-read* on PATH entry %s; its files must not be inspectable", binDir)
	}
}
