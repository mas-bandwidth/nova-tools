package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// nova-tools #625, Stella's dogfood: `nova-board add --dir <not-yet-created>`
// was refused with "directory missing". A board is an append-only log and an
// empty directory is a valid empty ledger, so the first `add` into a directory
// that is not there MAKES it, exactly as the first `quickstart` does, and the
// card lands. The red test the issue asks for: add into a nonexistent dir
// succeeds and list prints the one card.
func TestAddMakesTheDirectoryOnFirstUse(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "board")
	var out, errb bytes.Buffer
	exit := run([]string{"add", "--dir", missing, "--as", "stella", "--text", "the board dir was not there",
		"--by", "4h", "--default", "the filer files it as a known gap"}, &out, &errb, time.Now().UTC(), &seq{})
	if exit != 0 {
		t.Fatalf("add into a directory that is not there exits %d, want 0: an empty dir is a valid empty ledger; stderr: %s\nstdout: %s",
			exit, errb.String(), out.String())
	}
	if info, err := os.Stat(missing); err != nil || !info.IsDir() {
		t.Fatalf("add returned 0 and %s is not a directory (%v); a first run has nowhere to write", missing, err)
	}
	out.Reset()
	errb.Reset()
	if exit := run([]string{"list", "--dir", missing, "--stale", "10m", "--list"}, &out, &errb, time.Now().UTC(), &seq{}); exit != 0 {
		t.Fatalf("list after the first add exits %d, want 0; stderr: %s", exit, errb.String())
	}
	if !strings.Contains(out.String(), "BOARD CARD") || !strings.Contains(out.String(), "the board dir was not there") {
		t.Errorf("list did not print the one card the first add filed:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "BOARD OK cards=1 open=1") {
		t.Errorf("list did not count the one card the first add filed:\n%s", out.String())
	}
}
