package main

// #2908 (Johnny's hold at 4fd8646c): the library cap and the flag default were two numbers
// (pulse.FillCap = 30, --fill-cap default 60), so a FillInput with FillCap unset still
// capped at 30. One number: pulse.FillCap is 60, the flag default reads it, and the unset
// path uses it.

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/pulse"
)

// TestIssue2908UnsetFillCapIsTheOneNumber: FillCap left at zero caps at the same 60 the
// --fill-cap flag defaults to (TestIssue1483CapDefault60 covers the flag path).
func TestIssue2908UnsetFillCapIsTheOneNumber(t *testing.T) {
	if pulse.FillCap != 60 {
		t.Fatalf("pulse.FillCap = %d, want 60 (the one number the flag default reads)", pulse.FillCap)
	}
	dir := t.TempDir()
	ready, launched := filepath.Join(dir, "ready"), filepath.Join(dir, "launched")
	fillReady(t, ready, 70)
	l := &fillLaunchStub{}
	var out, errb bytes.Buffer
	code := pulse.Fill(pulse.FillInput{
		Ready: ready, Launched: launched,
		Machines: fillMachines(t, dir, "bench-x"),
		Benches:  []string{"bench-x"},
		Once:     true,
		Stdout:   &out,
		Stderr:   &errb,
		Capacity: fillCapStub{"bench-x": 100},
		Launcher: l,
	})
	if code != 0 {
		t.Fatalf("fill exit = %d, want 0; stderr=%q", code, errb.String())
	}
	if len(l.calls) != 60 {
		t.Fatalf("launcher calls = %d, want 60 (unset FillCap takes pulse.FillCap)", len(l.calls))
	}
	want := "FILL tick=1 bench-x:launched=60,failed=0 ready=10"
	if line := strings.TrimSpace(out.String()); line != want {
		t.Fatalf("FILL line = %q, want %q", line, want)
	}
}
