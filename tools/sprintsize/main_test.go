package main

import (
	"os"
	"path/filepath"
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
