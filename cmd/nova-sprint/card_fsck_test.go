package main

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/redis/go-redis/v9"
)

// TestCardFsckAndBenchReindexVerbs (#3692): bench reindex adopts a record
// that predates the card model, card fsck is then clean (exit 0), a lost
// bench link is drift (exit 1, remedy --repair), --repair restores it, card
// show prints the pointer, and card ls --unplaced prints the null cards.
func TestCardFsckAndBenchReindexVerbs(t *testing.T) {
	ctx := context.Background()
	addr := testutil.Start(t)
	client := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = client.Close() })
	if err := fn.Load(ctx, client); err != nil {
		t.Fatal(err)
	}
	const sprint = "verbs-3692"
	id := "s:" + sprint + ":card:old"
	client.HSet(ctx, id, "state", "running", "bench", "b1", "cut_at", "1700000000")
	client.SAdd(ctx, "s:"+sprint+":idx:card:running", "old")

	run := func(f func(context.Context, []string, io.Writer, io.Writer) int, args ...string) (int, string, string) {
		var out, errOut bytes.Buffer
		code := f(ctx, args, &out, &errOut)
		return code, out.String(), errOut.String()
	}
	if code, out, errOut := run(cmdCardFsck, "--sprint", sprint, "--redis", addr); code != 1 || !strings.Contains(errOut, "DRIFT unlisted "+id) || !strings.Contains(out, "cards=0") {
		t.Fatalf("fsck before reindex: %d %q %q", code, out, errOut)
	}
	if code, out, errOut := run(runBenchReindex, "--sprint", sprint, "--redis", addr); code != 0 || !strings.HasPrefix(out, "BENCH REINDEX sprint="+sprint+" cards=1 null=0 waiting=0 ready=0 working=1 ") {
		t.Fatalf("reindex: %d %q %q", code, out, errOut)
	}
	if code, out, errOut := run(cmdCardFsck, "--sprint", sprint, "--redis", addr); code != 0 || !strings.Contains(out, "drift=0") {
		t.Fatalf("fsck after reindex: %d %q %q", code, out, errOut)
	}
	if n := client.ZCard(ctx, "bench:b1:cards:working").Val(); n != 1 {
		t.Fatalf("bench:b1:cards:working = %d after reindex", n)
	}
	client.ZRem(ctx, "bench:b1:cards:working", id)
	if code, _, errOut := run(cmdCardFsck, "--sprint", sprint, "--redis", addr); code != 1 || !strings.Contains(errOut, "--repair") {
		t.Fatalf("fsck with a lost link: %d %q", code, errOut)
	}
	if code, out, errOut := run(cmdCardFsck, "--sprint", sprint, "--redis", addr, "--repair"); code != 0 || !strings.Contains(out, "drift=1 fixed=1") {
		t.Fatalf("fsck --repair: %d %q %q", code, out, errOut)
	}
	if code, out, _ := runSprint("card", "show", "--redis", addr, "--sprint", sprint, "--label", "old"); code != 0 ||
		!strings.Contains(out, "card.where working\n") || !strings.Contains(out, "card.where_ok -\n") {
		t.Fatalf("card show after reindex: %d %q", code, out)
	}
	if code, out, _ := run(cmdCardLs, "--unplaced", "--sprint", sprint, "--redis", addr); code != 0 || out != "CARD LS sprint="+sprint+" unplaced=0\n" {
		t.Fatalf("card ls --unplaced: %d %q", code, out)
	}
	if code, _, _ := run(cmdCardFsck, "--redis", addr); code != 2 {
		t.Fatalf("fsck without --sprint exits %d, want 2", code)
	}
}
