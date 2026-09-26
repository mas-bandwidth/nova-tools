package main

import (
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
)

// TestNoteVerbPostsListsAndDrops (nova-tools #4324): a MERGE-NOTE is one
// typed line on the stream's list; ls prints it; the copy renderer reads
// the same list (card.RenderCopy, TestRenderCopyCarriesMergeNotes); drop
// empties it. Every usage error is a refusal on stderr, exit 2.
func TestNoteVerbPostsListsAndDrops(t *testing.T) {
	t.Parallel()
	mr := miniredis.RunT(t)
	const s = "swarm: cards"
	for _, args := range [][]string{
		{"note", "post", "--redis", mr.Addr(), "--stream", s, "x"},          // no --by
		{"note", "post", "--redis", mr.Addr(), "--stream", s, "--by", "m1"}, // no text
		{"note", "post", "--redis", mr.Addr(), "--by", "m1", "x"},           // no key
		{"note", "post", "--redis", mr.Addr(), "--stream", s, "--sprint", "sp", "--by", "m1", "x"},
		{"note", "ls", "--redis", mr.Addr(), "--stream", s, "stray"},
	} {
		if code, _, errOut := runSprint(args...); code != 2 || !strings.Contains(errOut, "nova-sprint note ") {
			t.Fatalf("%v: exit %d %q", args, code, errOut)
		}
	}
	code, out, errOut := runSprint("note", "post", "--redis", mr.Addr(), "--stream", s, "--by", "merge-swarm-cards-1",
		"conflict in taskcard/moves.go:", "keep", "both Opts.Fields")
	if code != 0 || !strings.Contains(out, "NOTE POSTED key=ws:swarm:\\x20cards:notes n=1 by=merge-swarm-cards-1") {
		t.Fatalf("post: %d %q %q", code, out, errOut)
	}
	if code, out, _ := runSprint("note", "post", "--redis", mr.Addr(), "--sprint", "sp", "--by", "rowan", "the moves API changed"); code != 0 || !strings.Contains(out, "n=1") {
		t.Fatalf("sprint post: %d %q", code, out)
	}
	code, out, _ = runSprint("note", "ls", "--redis", mr.Addr(), "--stream", s)
	if code != 0 || !strings.Contains(out, "NOTE MERGE-NOTE by=merge-swarm-cards-1 at=") || !strings.Contains(out, " conflict in taskcard/moves.go: keep both Opts.Fields\n") || !strings.Contains(out, "n=1") {
		t.Fatalf("ls: %d %q", code, out)
	}
	code, out, _ = runSprint("note", "drop", "--redis", mr.Addr(), "--stream", s)
	if code != 0 || !strings.Contains(out, "NOTE DROPPED key=ws:swarm:\\x20cards:notes n=1") {
		t.Fatalf("drop: %d %q", code, out)
	}
	if code, out, _ := runSprint("note", "ls", "--redis", mr.Addr(), "--stream", s); code != 0 || !strings.Contains(out, "n=0") {
		t.Fatalf("ls after drop: %d %q", code, out)
	}
}
