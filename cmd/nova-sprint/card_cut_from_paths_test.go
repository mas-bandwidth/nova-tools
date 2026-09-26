package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws"
)

// pathsRows is a card file of one row per (id, stream, paths).
func pathsRows(rows ...[3]string) string {
	var b strings.Builder
	b.WriteString("id\ttitle\tstream\twho\tpaths\tdone-when\tbody\tdepends-on\troute\test\n")
	for i, r := range rows {
		fmt.Fprintf(&b, "%s\tCard %d does its thing\t%s\tany\t%s\tgo test ./x -run TestX%d passes\tWhy %d\tnone\tfriend\t30\n",
			r[0], i+1, r[1], r[2], i+1, i+1)
	}
	return b.String()
}

// TestCardCutFromStreamsPathsDisjoint is the invariant at push (#4322),
// with the fake store the push tests use: two open streams (work holds
// internal/nsprint/ws, ci holds cmd/nova-sprint/ci.go); a row overlapping
// work is refused with the exact receipt and nothing is filed or pushed;
// the same row with --join work is pushed onto work; a row naming a parent
// dir of ci's file is refused (the prefix rule); a disjoint row is pushed
// on its own stream; two rows of the file on two streams sharing a path
// refuse the second.
func TestCardCutFromStreamsPathsDisjoint(t *testing.T) {
	t.Parallel()
	open := func() *fakeCutStore {
		return &fakeCutStore{paths: ws.StreamPaths{"work": {"internal/nsprint/ws"}, "ci": {"cmd/nova-sprint/ci.go"}}}
	}

	forge, st := &fakeCutForge{}, open()
	code, out := runCutFrom(cutFromOpts{Text: []byte(pathsRows([3]string{"p1", "fleet", "internal/nsprint/ws/paths.go"}))}, cutDeps(forge, st))
	want := `REFUSED PATHS overlap stream=work paths=internal/nsprint/ws,internal/nsprint/ws/paths.go remedy="--join work" row=1 line=2 id=p1` + "\n"
	if code != 1 || !strings.HasPrefix(out, want) || len(forge.titles) != 0 || len(st.batches) != 0 {
		t.Fatalf("overlap: exit %d filed %d batches %d out %q; want exit 1, nothing filed or pushed, first line %q",
			code, len(forge.titles), len(st.batches), out, want)
	}

	forge, st = &fakeCutForge{}, open()
	code, out = runCutFrom(cutFromOpts{Join: "work", Text: []byte(pathsRows([3]string{"p1", "fleet", "internal/nsprint/ws/paths.go"}))}, cutDeps(forge, st))
	if code != 0 || len(st.batches) != 1 || st.batches[0][0].Stream != "work" || !strings.Contains(out, "CARD CUT row=1 id=p1 ref=mas-bandwidth/nova-tools#5000 stream=work to=waiting") {
		t.Fatalf("--join work: exit %d out %q; want the row pushed onto work", code, out)
	}

	forge, st = &fakeCutForge{}, open()
	code, out = runCutFrom(cutFromOpts{Text: []byte(pathsRows([3]string{"p2", "fleet", "cmd/nova-sprint/"}))}, cutDeps(forge, st))
	want = `REFUSED PATHS overlap stream=ci paths=cmd/nova-sprint,cmd/nova-sprint/ci.go remedy="--join ci" row=1 line=2 id=p2` + "\n"
	if code != 1 || !strings.HasPrefix(out, want) || len(st.batches) != 0 {
		t.Fatalf("parent dir: exit %d out %q; want first line %q", code, out, want)
	}

	forge, st = &fakeCutForge{}, open()
	code, out = runCutFrom(cutFromOpts{Text: []byte(pathsRows([3]string{"p3", "fleet", "internal/nsprint/card/push.go internal/nsprint/wsx"}))}, cutDeps(forge, st))
	if code != 0 || len(st.batches) != 1 || st.batches[0][0].Stream != "fleet" {
		t.Fatalf("disjoint: exit %d out %q; want pushed on fleet", code, out)
	}

	forge, st = &fakeCutForge{}, open()
	code, out = runCutFrom(cutFromOpts{Text: []byte(pathsRows(
		[3]string{"p4", "fleet", "internal/nsprint/deal"}, [3]string{"p5", "docs", "internal/nsprint/deal/deal.go"}))}, cutDeps(forge, st))
	want = `REFUSED PATHS overlap stream=fleet paths=internal/nsprint/deal,internal/nsprint/deal/deal.go remedy="--join fleet" row=2 line=3 id=p5` + "\n"
	if code != 1 || !strings.HasPrefix(out, want) || len(forge.titles) != 0 {
		t.Fatalf("in-file overlap: exit %d out %q; want first line %q", code, out, want)
	}
}

// TestCardCutFromDryRunSaysPathsUnchecked is the #4405 fix round's item 5
// (#4322): a --dry-run with no store checks no paths, and its receipt says
// so with the remedy, beside the rows and the summary.
func TestCardCutFromDryRunSaysPathsUnchecked(t *testing.T) {
	t.Parallel()
	d := cutDeps(&fakeCutForge{}, &fakeCutStore{})
	d.StreamPaths = nil
	code, out := runCutFrom(cutFromOpts{DryRun: true, Text: []byte(pathsRows([3]string{"u1", "fleet", "internal/nsprint/ws/paths.go"}))}, d)
	want := `CARD CUT DRY PATHS unchecked rows=1 why="no --redis: the paths gate reads the store" remedy="pass --redis <addr>"` + "\n"
	if code != 0 || !strings.Contains(out, want) || !strings.Contains(out, "CARD CUT DRY row=1 id=u1 ") {
		t.Fatalf("dry run with no store: exit %d out %q; want the line %q", code, out, want)
	}
	d = cutDeps(&fakeCutForge{}, &fakeCutStore{paths: ws.StreamPaths{}})
	if code, out := runCutFrom(cutFromOpts{DryRun: true, Text: []byte(pathsRows([3]string{"u1", "fleet", "internal/nsprint/ws/paths.go"}))}, d); code != 0 || strings.Contains(out, "unchecked") {
		t.Fatalf("dry run with a store: exit %d out %q; want no unchecked line", code, out)
	}
}
