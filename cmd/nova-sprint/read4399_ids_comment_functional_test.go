//go:build functional

package main

// #4399 round 5, the reader's regression at the CLI door: `scope park --ids
// @file` through the binary on a throwaway store, the file carrying a
// whole comment line, a trailing comment and a duplicate. Round 4's shared
// Parse expanded the file before ws.ReadIDs saw it, so the comment's words
// became ids and the joined list was cut at the first '#': a b / # comment
// / c parked a and b only. Through the one Parse every id parks, once.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws/wstest"
)

func TestRead4399IDsFileCommentThroughTheOneParse(t *testing.T) {
	t.Parallel()
	addr, c := wstest.Start(t)
	wstest.Fixture(t, c, 30, 3) // stream 1 holds t00001, t00004, t00007, ... (i%3 == 1), the first eight waiting
	dir := t.TempDir()
	idsFile := filepath.Join(dir, "ids")
	if err := os.WriteFile(idsFile, []byte("t00001 t00004\n# a whole comment line\nt00007,t00001  # trailing, and t00001 again\n\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cp := filepath.Join(dir, "cp.tsv")
	code, out, errOut := runSprint("scope", "park", "--redis", addr, "--stream", wstest.StreamName(1), "--ids", "@"+idsFile, "--checkpoint", cp)
	if code != 0 || !strings.HasPrefix(out, `PARKED stream="s1: work" parked=3 same=0 checkpoint=`+cp+" ") {
		t.Fatalf("scope park --ids @file with a comment line: exit %d stdout %q stderr %q; want parked=3", code, out, errOut)
	}
	ctx := context.Background()
	for _, id := range []string{"t00001", "t00004", "t00007"} {
		if where, err := c.HGet(ctx, "task:"+id, "state").Result(); err != nil || where != "parked" {
			t.Fatalf("task:%s is %q (%v) after the park, want parked", id, where, err)
		}
	}
	if where := c.HGet(ctx, "task:t00010", "state").Val(); where != "waiting" {
		t.Fatalf("task:t00010, not in the file, is %q", where)
	}
}
