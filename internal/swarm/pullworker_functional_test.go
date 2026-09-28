//go:build functional

package swarm

import (
	"bytes"
	"context"
	"github.com/mas-bandwidth/nova-tools/internal/testbin"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// This test runs the container path through a fake nova-secrets program:
// exec of a whole program is the functional tier's (Glenn 2026-09-26,
// nova-tools#4328).

// --seat wraps the container command through nova-secrets exec: the production
// executeContainer path, not just the helper, must hand nova-secrets the wrapped
// command line.
func TestExecuteContainerRunsThroughNovaSecrets(t *testing.T) {
	log := filepath.Join(t.TempDir(), "args.log")
	script := "#!/bin/sh\nprintf '%s' \"$*\" > " + `"` + log + `"` + "\n"
	bin := writeFakeExec(t, "nova-secrets", script)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	workDir := t.TempDir()
	opts := PullWorkerOptions{
		Seat:      "hulk",
		Container: "podman",
		Image:     "nova-card:dev",
		Model:     "m",
		Stderr:    &bytes.Buffer{},
	}

	code, err := executeContainer(context.Background(), opts, "CARD-1", "card", workDir)
	if err != nil || code != 0 {
		t.Fatalf("executeContainer = %d, %v; want 0", code, err)
	}
	data, rerr := os.ReadFile(log)
	if rerr != nil {
		t.Fatalf("nova-secrets was never run: %v", rerr)
	}
	got := string(data)
	if !strings.Contains(got, "exec --as hulk -- podman") {
		t.Fatalf("nova-secrets was not handed the wrapped command; got %q", got)
	}
}

// writeFakeExec puts a shell script named <name> in dir/bin and returns the dir
// with that bin and nothing else on PATH, so a test can prove which binary a
// path ran and with what arguments, never the real one.
func writeFakeExec(t *testing.T, name, script string) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := testbin.WriteExecutable(filepath.Join(bin, name), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}
