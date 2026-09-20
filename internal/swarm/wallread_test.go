package swarm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// THE LOG THAT COST A CARD, in its own shape. `js-under-20-bytes` (2026-09-19, rc=-1,
// wall=1200.04s, no RESULT.md) carried ONE `Operation not permitted`, on line 5 of its
// harness capture -- the harness's own startup banner, printed before STEP 1 -- and then
// worked for sixteen more model steps before dying in a provider turn that never answered.
// The post-mortem scan reported `WALL task=js-under-20-bytes path=/var/db/xcode_select_link`
// and the shift went looking at the wall.
const bannerRefusalLog = "\n> build · deepseek-v4-pro\n" +
	"xcode-select: error: unable to read data link at '/var/db/xcode_select_link', expected symbolic link (Operation not permitted)\n" +
	"# Todos\nSTEP 1: Confirm workspace\n" +
	"$ cd repo && grep -n needHome internal/codegen/jstable/fixedmodule.go\n" +
	"5:// runtime and every type's write/decode helper land\n"

// TestWallStoppedIgnoresARefusalTheCardMovedPast: a refusal with another tool call after it
// is one the model routed around. RED WITHOUT THE FIX: WallRefused takes the first refusal
// in the file whatever the card did next, so the banner line was named as the death.
func TestWallStoppedIgnoresARefusalTheCardMovedPast(t *testing.T) {
	if _, ok := WallRefused([]byte(bannerRefusalLog)); !ok {
		t.Fatal("the fixture must hold a refusal WallRefused finds, or this test proves nothing")
	}
	if w, ok := WallStopped([]byte(bannerRefusalLog)); ok {
		t.Fatalf("a refusal the card made sixteen more steps past is not what stopped it, got %+v", w)
	}
}

// TestWallStoppedNamesARefusalWithNothingAfterIt: the other half. A refusal the card never
// got past is still a wall death, and the classification must survive the fix.
func TestWallStoppedNamesARefusalWithNothingAfterIt(t *testing.T) {
	log := "STEP 2: build\n$ cc -o probe probe.c\n" +
		"cc: error: unable to read data link at '/var/db/xcode_select_link' (Operation not permitted)\n"
	w, ok := WallStopped([]byte(log))
	if !ok {
		t.Fatal("a refusal with no tool call after it is what stopped the card")
	}
	if w.Path != "/var/db/xcode_select_link" || w.Step != "2" {
		t.Fatalf("the refusal names the path and the step it reached, got %+v", w)
	}
}

// TestPermissionDeniedOnAPathIsARefusal: measured inside the swarm's own wall on hulk
// (landlock abi 4, 2026-09-19) -- `sh: 1: cannot create /tmp/nova-wall-probe-swarm:
// Permission denied`. RED WITHOUT THE FIX: this package knew only `SANDBOX REFUSED` and
// `Operation not permitted`, so no linux wall refusal was classified at all.
func TestPermissionDeniedOnAPathIsARefusal(t *testing.T) {
	log := "STEP 1\n$ sh -c 'echo probe > /tmp/nova-wall-probe-swarm'\n" +
		"sh: 1: cannot create /tmp/nova-wall-probe-swarm: Permission denied\n"
	w, ok := WallStopped([]byte(log))
	if !ok {
		t.Fatal("landlock refuses with EACCES, which the C library spells `Permission denied`")
	}
	if w.Path != "/tmp/nova-wall-probe-swarm" {
		t.Fatalf("the refused path is named, got %q", w.Path)
	}
	if got := WallKind("sh: 1: cannot create /tmp/x: Permission denied"); got != "write" {
		t.Fatalf("a refused create is a write, got %q", got)
	}
	// A bare permission failure about something that is not a path is not the wall.
	if _, ok := WallStopped([]byte("kill: Operation not permitted\n")); ok {
		t.Fatal("a refusal that names no path is not the wall refusing a read or a write")
	}
}

// TestWallReaderAnnouncesTheRefusalAsItArrives: the point of the reader. RED WITHOUT THE
// FIX: nothing read the child's output until the child was gone, so the coordinator learnt
// of a refusal at the reap -- twenty minutes later on the card that died of one.
func TestWallReaderAnnouncesTheRefusalAsItArrives(t *testing.T) {
	var said []string
	r := NewWallReader("card-8311", func(line string) { said = append(said, line) })
	if _, err := r.Write([]byte("STEP 2: write the file\n")); err != nil {
		t.Fatal(err)
	}
	if len(said) != 0 {
		t.Fatalf("nothing is announced before a refusal, got %v", said)
	}
	if _, err := r.Write([]byte("sh: 1: cannot create /etc/hosts: Permission denied\n")); err != nil {
		t.Fatal(err)
	}
	want := "WALL REFUSED write /etc/hosts task=card-8311 step=2"
	if len(said) != 1 || said[0] != want {
		t.Fatalf("the typed line is announced once, as it arrives:\nwant %q\ngot  %v", want, said)
	}
	// A SECOND refusal is not a second announcement: one line per card, the first one.
	if _, err := r.Write([]byte("sh: 1: cannot create /etc/passwd: Permission denied\n")); err != nil {
		t.Fatal(err)
	}
	if len(said) != 1 {
		t.Fatalf("one announcement per card, got %v", said)
	}
}

// TestWallReaderHoldsAPartialLine: the child's stdout and stderr arrive in whatever pieces
// the pipe hands over, and a refusal split across two writes is still a refusal.
func TestWallReaderHoldsAPartialLine(t *testing.T) {
	var said []string
	r := NewWallReader("c", func(line string) { said = append(said, line) })
	_, _ = r.Write([]byte("sh: 1: cannot create /etc/ho"))
	if len(said) != 0 {
		t.Fatalf("a line with no newline yet is not a line, got %v", said)
	}
	_, _ = r.Write([]byte("sts: Permission denied\n"))
	if len(said) != 1 || !strings.Contains(said[0], "/etc/hosts") {
		t.Fatalf("the two halves are one refusal, got %v", said)
	}
}

// TestWallReaderForgetsARefusalTheCardMovedPast: the live half of WallStopped. A reader
// that saw a refusal and then a tool call reports no refusal at all, so a card ended for
// stillness is not reported as a wall death it had already worked past.
func TestWallReaderForgetsARefusalTheCardMovedPast(t *testing.T) {
	r := NewWallReader("c", nil)
	_, _ = r.Write([]byte("xcode-select: error: unable to read data link at '/var/db/xcode_select_link' (Operation not permitted)\n"))
	if _, _, ok := r.Stopped(); !ok {
		t.Fatal("a refusal with nothing after it is the refusal that stopped the card")
	}
	_, _ = r.Write([]byte("$ grep -n needHome internal/codegen/jstable/fixedmodule.go\n"))
	if w, _, ok := r.Stopped(); ok {
		t.Fatalf("a card that made another tool call moved past the refusal, got %+v", w)
	}
}

// TestWriteBlockedResultNeverOverwritesAPublishedReport: a card that published owns its
// report, and the machinery never writes over one.
func TestWriteBlockedResultNeverOverwritesAPublishedReport(t *testing.T) {
	job := t.TempDir()
	mine := filepath.Join(job, "RESULT.md")
	if err := os.WriteFile(mine, []byte("RESULT: mine\nfindings: 0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, wrote, err := WriteBlockedResult(job, "c", "write", "/etc/hosts", "2", "still"); err != nil || wrote {
		t.Fatalf("a published report is never overwritten: wrote=%v err=%v", wrote, err)
	}
	raw, err := os.ReadFile(mine)
	if err != nil || !strings.Contains(string(raw), "RESULT: mine") {
		t.Fatalf("the worker's own report is untouched: %q %v", raw, err)
	}
}

// TestWriteBlockedResultNamesTheBlockAndCannotBeGreen: the report a card that published
// none is given. It carries the typed line, it says who wrote it, and it has NO findings
// head -- so it parses `plan-only` and can never be folded as work a worker did.
func TestWriteBlockedResultNamesTheBlockAndCannotBeGreen(t *testing.T) {
	job := t.TempDir()
	path, wrote, err := WriteBlockedResult(job, "card-8311", "write", "/etc/hosts", "2", "the card wrote nothing for 300s")
	if err != nil || !wrote {
		t.Fatalf("a card with no report of its own is given one: %v %v", wrote, err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	for _, want := range []string{"RESULT: BLOCKED card-8311", "WALL REFUSED write /etc/hosts task=card-8311 step=2", "written-by: nova-swarm native"} {
		if !strings.Contains(body, want) {
			t.Fatalf("the blocked report carries %q:\n%s", want, body)
		}
	}
	if rep := ParseReport(raw); rep.Class != ClassPlanOnly {
		t.Fatalf("a report the machinery wrote must never be scored as a worker's work, got class %q", rep.Class)
	}
}
