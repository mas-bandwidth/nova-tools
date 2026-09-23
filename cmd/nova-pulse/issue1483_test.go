package main

// issue1483_test.go: the red test for nova-tools#1483. Two behaviour differences between
// the live loop (fill-loop2.sh) and pulse.Fill:
//
// 1. Dealing order: the loop deals one card per bench in turn (round-robin), while pulse.Fill
//    used to drain each bench to its capacity in list order. The round-robin is already in
//    place (internal/pulse/fill.go fillTick round-robin loop, tested by fillrobin_test.go).
//
// 2. Cap per tick: pulse.FillCap was 30; the loop uses 60. The fix makes FillCap a flag with
//    a default of 60, so the verb and the loop agree on the number.

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/pulse"
)

// TestIssue1483 asserts both halves of #1483:
//
// (a) deal one card per bench in turn, never drain a bench in list order -- three benches,
//
//	each with capacity 10 and nine cards ready, the placement sequence must be
//	b1,b2,b3, b1,b2,b3, b1,b2,b3, not b1×3,b2×3,b3×3.
//
// (b) cap per tick is 60 in the live loop -- a bench whose capacity is 100 and sixty cards
//
//	ready must launch all sixty (the verb's default cap matches the loop's 60, not the
//	old hardcoded 30).
func TestIssue1483(t *testing.T) {
	t.Run("deals one per bench in turn", func(t *testing.T) {
		dir := t.TempDir()
		ready, launched := filepath.Join(dir, "ready"), filepath.Join(dir, "launched")
		if err := os.MkdirAll(ready, 0o755); err != nil {
			t.Fatal(err)
		}
		for i := 1; i <= 9; i++ {
			p := filepath.Join(ready, fmt.Sprintf("card-%03d.md", i))
			if err := os.WriteFile(p, []byte("a card\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		machines := writeMainFile(t, dir, "machines.tsv",
			"b1\tb1\tlinux/x64\tbench\tswarm-b1\t64\t-\n"+
				"b2\tb2\tlinux/x64\tbench\tswarm-b2\t64\t-\n"+
				"b3\tb3\tlinux/x64\tbench\tswarm-b3\t64\t-\n")

		stub := &fillLaunchStub{}
		var out, errb bytes.Buffer
		code := pulse.Fill(pulse.FillInput{
			Ready:    ready,
			Launched: launched,
			Machines: machines,
			Benches:  []string{"b1", "b2", "b3"},
			Once:     true,
			Stdout:   &out,
			Stderr:   &errb,
			Capacity: fillCapStub{"b1": 10, "b2": 10, "b3": 10},
			Launcher: stub,
		})
		if code != 0 {
			t.Fatalf("fill exit = %d, want 0; stderr=%q", code, errb.String())
		}
		wantSeq := []string{
			"b1 card-001.md", "b2 card-002.md", "b3 card-003.md",
			"b1 card-004.md", "b2 card-005.md", "b3 card-006.md",
			"b1 card-007.md", "b2 card-008.md", "b3 card-009.md",
		}
		if len(stub.calls) != len(wantSeq) {
			t.Fatalf("launcher calls = %d, want %d: %q", len(stub.calls), len(wantSeq), stub.calls)
		}
		for i, want := range wantSeq {
			if i >= len(stub.calls) {
				t.Fatalf("call %d missing; got %d calls total", i, len(stub.calls))
			}
			got := stub.calls[i]
			bench, cardPath, _ := strings.Cut(got, " ")
			gotShort := bench + " " + filepath.Base(cardPath)
			if gotShort != want {
				t.Fatalf("call %d = %q, want %q", i, gotShort, want)
			}
		}
	})

	t.Run("cap per tick is 60 not 30", func(t *testing.T) {
		dir := t.TempDir()
		ready, launched := filepath.Join(dir, "ready"), filepath.Join(dir, "launched")
		if err := os.MkdirAll(ready, 0o755); err != nil {
			t.Fatal(err)
		}
		for i := 1; i <= 60; i++ {
			p := filepath.Join(ready, fmt.Sprintf("card-%03d.md", i))
			if err := os.WriteFile(p, []byte("a card\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		machines := writeMainFile(t, dir, "machines.tsv",
			"bench-x\tbench-x\tlinux/x64\tbench\tswarm-bench-x\t64\t-\n")

		stub := &fillLaunchStub{}
		var out, errb bytes.Buffer
		code := pulse.Fill(pulse.FillInput{
			Ready:    ready,
			Launched: launched,
			Machines: machines,
			Benches:  []string{"bench-x"},
			Once:     true,
			Stdout:   &out,
			Stderr:   &errb,
			Capacity: fillCapStub{"bench-x": 100},
			Launcher: stub,
			FillCap:  60,
		})
		if code != 0 {
			t.Fatalf("fill exit = %d, want 0; stderr=%q", code, errb.String())
		}
		if len(stub.calls) != 60 {
			t.Fatalf("launcher calls = %d, want 60 (cap 60 with 100 capacity and 60 cards): %q", len(stub.calls), stub.calls)
		}
		line := strings.TrimSpace(out.String())
		want := "FILL tick=1 bench-x:launched=60,failed=0 ready=0"
		if line != want {
			t.Fatalf("FILL line = %q, want %q", line, want)
		}
	})
}

// TestIssue1483CapDefault60: the fill verb's default cap (when no --fill-cap flag is given)
// is 60, matching the live loop. A bench with capacity 100 and 60 cards ready launches all
// 60 through the verb.
func TestIssue1483CapDefault60(t *testing.T) {
	specs := fakePATH(t)
	dir := t.TempDir()
	ready, launched := filepath.Join(dir, "ready"), filepath.Join(dir, "launched")
	if err := os.MkdirAll(ready, 0o755); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 60; i++ {
		p := filepath.Join(ready, fmt.Sprintf("card-%03d.md", i))
		if err := os.WriteFile(p, []byte("a card\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	log := filepath.Join(dir, "launcher.log")
	fakeTool(t, specs, "nova-swarm", fakeSpec{Log: log, Default: fakeRule{Exit: 0}})
	machines := writeMainFile(t, dir, "machines.tsv",
		"bench-x\tbench-x\tlinux/x64\tbench\tswarm-bench-x\t64\t-\n")

	var out, errb bytes.Buffer
	code := run([]string{"fill",
		"--ready", ready, "--launched", launched,
		"--machines", machines,
		"--bench", "bench-x",
		"--capacity", "100",
		"--once",
		"--launcher", filepath.Join(fakeBins(t), "nova-swarm"+exeSuffix()),
	}, &out, &errb, time.Now().UTC())
	if code != 0 {
		t.Fatalf("fill exit = %d, want 0; stderr=%q", code, errb.String())
	}
	line := strings.TrimSpace(out.String())
	want := "FILL tick=1 bench-x:launched=60,failed=0 ready=0"
	if line != want {
		t.Fatalf("FILL line = %q, want %q", line, want)
	}
}
