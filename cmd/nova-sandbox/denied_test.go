package main

import (
	"bytes"
	"strings"
	"testing"
)

// The seatbelt violation lines this parser reads are the OS's own, copied from
// `log show --predicate 'subsystem == "com.apple.sandbox.reporting"'` on the Studio
// (macOS 26, arm64, 2026-09-18) and NOT invented: the two shapes below — the plain
// violation and the deduplicated "N duplicate reports for" one — are what the kernel
// actually wrote there while these tests were being written.
const fakeDenialLog = `Timestamp                       (process)[PID]
2026-09-18 11:11:11.806663-0400  localhost kernel[0]: (Sandbox) [com.apple.sandbox.reporting:violation] 3 duplicate reports for Sandbox: maild(2087) deny(1) mach-lookup com.apple.contactsd.persistence
2026-09-18 11:11:11.806671-0400  localhost kernel[0]: (Sandbox) [com.apple.sandbox.reporting:violation] Sandbox: go(4210) deny(1) file-read-metadata /opt
2026-09-18 11:11:11.906671-0400  localhost kernel[0]: (Sandbox) [com.apple.sandbox.reporting:violation] Sandbox: go(4211) deny(1) file-read-data /Users/me/go/pkg/mod/github.com/x@v1.2.3/a.go
2026-09-18 11:11:12.006671-0400  localhost kernel[0]: (Sandbox) [com.apple.sandbox.reporting:violation] Sandbox: go(4211) deny(1) file-read-data /Users/me/go/pkg/mod/github.com/x@v1.2.3/a.go
2026-09-18 11:11:12.106671-0400  localhost kernel[0]: (Sandbox) [com.apple.sandbox.reporting:violation] Sandbox: sh(4212) deny(1) file-write-create /Users/me/notes/out.txt
2026-09-18 11:11:12.206671-0400  localhost kernel[0]: (Sandbox) [com.apple.sandbox.reporting:violation] Sandbox: sh(4099) deny(1) file-read-data /Users/someone-else/thing
2026-09-18 11:11:12.306671-0400  localhost kernel[0]: (Sandbox) [com.apple.sandbox.reporting:violation] Sandbox: sh(4212) deny(1) file-read-data /Volumes/nova-j1/work/inside.txt
`

// The parser reads the OS's lines and nothing else: the operation, the path and the pid.
func TestDenialsAreReadOffTheOSsOwnViolationLines(t *testing.T) {
	got := parseDenials(fakeDenialLog, 0)
	want := []deniedPath{
		{Path: "/opt", Op: "read", PID: 4210},
		{Path: "/Users/me/go/pkg/mod/github.com/x@v1.2.3/a.go", Op: "read", PID: 4211},
		{Path: "/Users/me/notes/out.txt", Op: "write", PID: 4212},
		{Path: "/Users/someone-else/thing", Op: "read", PID: 4099},
		{Path: "/Volumes/nova-j1/work/inside.txt", Op: "read", PID: 4212},
	}
	if len(got) != len(want) {
		t.Fatalf("parsed %d denials, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("denial %d is %+v, want %+v", i, got[i], want[i])
		}
	}
	// mach-lookup is not a path and has no --read that answers it, so it is not a denial
	// this line can carry a remedy for.
	for _, d := range got {
		if strings.Contains(d.Path, "com.apple") {
			t.Errorf("a mach-lookup denial was read as a path: %+v", d)
		}
	}
}

// A run is told about ITS OWN denials. The machine this tool runs on has eight CI runners
// on it, so a window of the log holds other people's violations too; the pid floor is the
// narrowing that keeps a card from being handed a neighbour's problem.
func TestDenialsBelowThePidFloorAreNotThisRuns(t *testing.T) {
	got := parseDenials(fakeDenialLog, 4210)
	for _, d := range got {
		if d.PID < 4210 {
			t.Errorf("a denial from pid %d was kept although this run's leader is 4210: %+v", d.PID, d)
		}
	}
	if len(got) != 4 {
		t.Fatalf("the pid floor kept %d denials, want 4: %v", len(got), got)
	}
}

// A denial INSIDE the wall's own allowed set is not a missing --read: it is some other
// operation on a path the caller already named, and printing a remedy that is already in
// the argv would send a reader to fix what is not broken.
func TestDenialsInsideTheAllowedSetAreNotReported(t *testing.T) {
	got := outsideTheWall(parseDenials(fakeDenialLog, 0), []string{"/Volumes/nova-j1"})
	for _, d := range got {
		if strings.HasPrefix(d.Path, "/Volumes/nova-j1") {
			t.Errorf("a denial inside the write set was reported as one to fix: %+v", d)
		}
	}
	if len(got) != 4 {
		t.Fatalf("%d denials outside the allowed set, want 4: %v", len(got), got)
	}
}

// The line is the contract, and the remedy on it is a line to RUN, not a thing to work out.
func TestTheDeniedLineNamesThePathTheOpAndTheRemedy(t *testing.T) {
	var errb bytes.Buffer
	printDenied(&errb, []deniedPath{
		{Path: "/opt", Op: "read", PID: 10},
		{Path: "/Users/me/notes/out.txt", Op: "write", PID: 11},
	}, 10)
	out := errb.String()
	if !strings.Contains(out, `SANDBOX DENIED path=/opt op=read remedy="--read /opt"`) {
		t.Errorf("the denied line for a directory does not name the directory as the remedy:\n%s", out)
	}
	// A FILE's remedy names the directory to pass, because --read takes a directory.
	if !strings.Contains(out, `SANDBOX DENIED path=/Users/me/notes/out.txt op=write remedy="--write /Users/me/notes"`) {
		t.Errorf("the denied line for a file does not name its directory as the remedy:\n%s", out)
	}
}

// Rule 16's shape for a list: a cap, and one line standing for the rest. A command that
// died early can trip hundreds of denials and a wall of them is not a remedy.
func TestTheDeniedLinesAreCapped(t *testing.T) {
	var many []deniedPath
	for i := 0; i < 25; i++ {
		many = append(many, deniedPath{Path: "/x/" + string(rune('a'+i)), Op: "read", PID: 1})
	}
	var errb bytes.Buffer
	printDenied(&errb, many, 3)
	out := errb.String()
	if got := strings.Count(out, "SANDBOX DENIED"); got != 3 {
		t.Errorf("the cap printed %d denied lines, want 3:\n%s", got, out)
	}
	if !strings.Contains(out, "22 more") {
		t.Errorf("the cap does not say how many denials it stood for:\n%s", out)
	}
}

// Nothing denied is nothing printed: a clean run says nothing about denials at all.
func TestNoDenialsPrintsNothing(t *testing.T) {
	var errb bytes.Buffer
	printDenied(&errb, nil, 10)
	if errb.Len() != 0 {
		t.Errorf("a run with no denials printed %q", errb.String())
	}
}
