package main

// #2013 at the command line: --markers names where the failure markers go, and with no
// --markers they go beside the queue rather than into it. A ready directory holds cards.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func fillOnceWithAFailingLauncher(t *testing.T, dir string, extra ...string) (int, string) {
	t.Helper()
	specs := fakePATH(t)
	fakeTool(t, specs, "nova-swarm", fakeSpec{Default: fakeRule{Stderr: "exit 125", Exit: 125}})
	ready, launched := filepath.Join(dir, "ready"), filepath.Join(dir, "launched")
	writeMainFile(t, ready, "card-001.md", "a card\n")
	args := append([]string{"fill", "--ready", ready, "--launched", launched,
		"--machines", seatRegistry(t, dir, "bench-a=swarm-bench-a"),
		"--bench", "bench-a", "--capacity", "1", "--once", "--launch-grace", "0",
		"--launcher", filepath.Join(fakeBins(t), "nova-swarm"+exeSuffix()),
	}, extra...)
	var out, errb bytes.Buffer
	code := run(args, &out, &errb, time.Now().UTC())
	return code, errb.String()
}

func namesInDir(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

// TestFillPutsMarkersBesideTheQueueByDefault: no --markers, so the failure marker lands in
// <ready>-markers and --ready holds the card and nothing else.
func TestFillPutsMarkersBesideTheQueueByDefault(t *testing.T) {
	dir := t.TempDir()
	if code, errs := fillOnceWithAFailingLauncher(t, dir); code != 1 {
		t.Fatalf("fill exit = %d, want 1 (the one bench filled nothing); stderr=%q", code, errs)
	}
	if got := namesInDir(t, filepath.Join(dir, "ready")); len(got) != 1 || got[0] != "card-001.md" {
		t.Fatalf("ready holds %v, want exactly [card-001.md]", got)
	}
	got := namesInDir(t, filepath.Join(dir, "ready-markers"))
	if len(got) != 1 || !strings.HasPrefix(got[0], "card-001.md.failed-") {
		t.Fatalf("ready-markers holds %v, want one card-001.md.failed-<n>", got)
	}
}

// TestFillMarkersFlagNamesTheDirectory: --markers is the caller's answer, and it is the only
// place a marker is written.
func TestFillMarkersFlagNamesTheDirectory(t *testing.T) {
	dir := t.TempDir()
	mine := filepath.Join(dir, "elsewhere", "markers")
	if code, errs := fillOnceWithAFailingLauncher(t, dir, "--markers", mine); code != 1 {
		t.Fatalf("fill exit = %d, want 1; stderr=%q", code, errs)
	}
	got := namesInDir(t, mine)
	if len(got) != 1 || !strings.HasPrefix(got[0], "card-001.md.failed-") {
		t.Fatalf("--markers %s holds %v, want one card-001.md.failed-<n>", mine, got)
	}
	if n := len(namesInDir(t, filepath.Join(dir, "ready-markers"))); n != 0 {
		t.Fatalf("a marker was written to the default directory although --markers named another: %d", n)
	}
}
