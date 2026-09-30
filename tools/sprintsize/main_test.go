package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// fakeBin is a nova-sprint that exits with code.
func fakeBin(t *testing.T, code string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "nova-sprint")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit "+code+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSprintExistsReadsWhere(t *testing.T) {
	t.Parallel()
	if !sprintExists(fakeBin(t, "0"), nil) {
		t.Fatal("where succeeded: a sprint exists")
	}
	if sprintExists(fakeBin(t, "1"), nil) {
		t.Fatal("where refused: no sprint")
	}
}

func TestLocalStoreAcceptsOnlyThisMachine(t *testing.T) {
	t.Parallel()
	for addr, want := range map[string]bool{"127.0.0.1:6401": true, "localhost:6401": true, "[::1]:6401": true, "10.0.0.5:6401": false, "127.0.0.1": false} {
		if got := localStore(addr); got != want {
			t.Errorf("localStore(%q) = %v, want %v", addr, got, want)
		}
	}
}

// runMain runs main() in a child of the test binary with args, and returns its
// exit code and stderr.
func runMain(t *testing.T, args ...string) (int, string) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestMainChild$")
	cmd.Env = append(os.Environ(), "SPRINTSIZE_CHILD="+strings.Join(args, "\x1f"))
	var errb bytes.Buffer
	cmd.Stderr = &errb
	err := cmd.Run()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return code, errb.String()
}

// TestMainChild is the child of runMain: it is main() with the args it is given.
func TestMainChild(t *testing.T) {
	args := os.Getenv("SPRINTSIZE_CHILD")
	if args == "" {
		t.Skip("the child of runMain")
	}
	os.Args = append([]string{"sprintsize"}, strings.Split(args, "\x1f")...)
	main()
}

func TestMainRefusesAnExistingSprintUnlessReplace(t *testing.T) {
	t.Parallel()
	bin := fakeBin(t, "0")
	code, errs := runMain(t, "--bin", bin, "--redis", "127.0.0.1:1")
	if code != 2 || !strings.Contains(errs, "a sprint already exists on 127.0.0.1:1") || !strings.Contains(errs, "--replace") {
		t.Fatalf("an existing sprint: exit %d, %s", code, errs)
	}
	if code, errs := runMain(t, "--bin", bin, "--redis", "127.0.0.1:1", "--replace"); code == 2 {
		t.Fatalf("--replace was refused: %s", errs)
	}
	if code, errs := runMain(t, "--bin", fakeBin(t, "1"), "--redis", "127.0.0.1:1"); code == 2 {
		t.Fatalf("no sprint was refused: %s", errs)
	}
}
