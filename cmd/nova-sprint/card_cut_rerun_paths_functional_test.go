//go:build functional

package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws/wstest"
)

// TestCardCutParentRerunChangedPathsConflicts is probe (d) of the #4405
// fix round (#4322): card cut --parent rerun with a child's PATHS changed
// printed to=already and kept the old paths. Now that row is a CONFLICT
// naming the record's PATHS and the row's, exit 1, the record (and its
// stream's paths) as they were; the unchanged child is still to=already.
func TestCardCutParentRerunChangedPathsConflicts(t *testing.T) {
	t.Parallel()
	addr, c := wstest.Start(t)
	ctx := context.Background()
	run := func(args ...string) (int, string, string) {
		return runSprint(append(args, "--redis", addr)...)
	}
	if _, err := taskcard.Push(ctx, c, taskcard.PushRequest{ID: "rr", Where: "waiting", Stream: "rerun", Kind: "build",
		Title: "rerun plan", Repo: "mas-bandwidth/nova-tools", By: "rowan",
		Fields: []string{"base", "dev", "base_sha", cutFromSHA, "paths", "internal/nsprint/taskcard", "done_when", "the plan holds"}}); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	tsv := filepath.Join(dir, "children.tsv")
	write := func(verbPaths string) {
		rows := "id\ttitle\tpaths\tdone-when\tdepends-on\troute\n" +
			"rr-model\tthe model\tinternal/nsprint/taskcard/hierarchy.go\tPlan.State is derived\tnone\tpro\n" +
			"rr-verb\tthe verb\t" + verbPaths + "\tcard cut --parent\trr-model\tfriend\n"
		if err := os.WriteFile(tsv, []byte(rows), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("cmd/nova-sprint/card_cut_from.go")
	if code, out, errOut := run("card", "cut", "--parent", "rr", "--from", tsv, "--no-github", "--actor", "rowan"); code != 0 {
		t.Fatalf("first cut: %d\n%s%s", code, out, errOut)
	}
	before := c.HGet(ctx, ws.PathsKey, "rerun").Val()

	write("cmd/nova-sprint/card_cut_other.go")
	code, out, _ := run("card", "cut", "--parent", "rr", "--from", tsv, "--no-github", "--actor", "rowan")
	want := `CARD CUT REFUSED row=2 line=3 id=rr-verb why="CONFLICT task:rr-verb paths=cmd/nova-sprint/card_cut_from.go new=cmd/nova-sprint/card_cut_other.go: a card's PATHS are fixed at its push; cut the change as another card (its own id), or cancel task:rr-verb and rerun"` + "\n"
	if code != 1 || !strings.Contains(out, want) || !strings.Contains(out, "CARD CUT row=1 id=rr-model ref=- stream=rerun to=already ") ||
		strings.Contains(out, "id=rr-verb ref=- stream=rerun to=already") {
		t.Fatalf("rerun with changed PATHS: exit %d\n%s\nwant the line %q", code, out, want)
	}
	if p := c.HGet(ctx, taskcard.Key("rr-verb"), "paths").Val(); p != "cmd/nova-sprint/card_cut_from.go" {
		t.Fatalf("rr-verb paths = %q after the conflict", p)
	}
	if after := c.HGet(ctx, ws.PathsKey, "rerun").Val(); after != before {
		t.Fatalf("ws:paths rerun %q -> %q", before, after)
	}
}
