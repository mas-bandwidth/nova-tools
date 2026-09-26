package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ci"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/redis/go-redis/v9"
)

// TestCardFsckAndBenchReindexVerbs (#3692): bench reindex adopts a record
// that predates the card model, card fsck is then clean (exit 0), a lost
// bench link is drift (exit 1, remedy --repair), --repair restores it, card
// show prints the pointer, and card ls --unplaced prints the null cards.
func TestCardFsckAndBenchReindexVerbs(t *testing.T) {
	t.Parallel()

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

// TestCardFsckReportsNoMirror (#3804): card fsck prints one NOMIRROR line per
// registered bench whose ci:nomirror:<bench> set is non-empty and counts the
// benches in its receipt; a bench defect is not card drift, so the exit is 0.
func TestCardFsckReportsNoMirror(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	addr := testutil.Start(t)
	client := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = client.Close() })
	if err := fn.Load(ctx, client); err != nil {
		t.Fatal(err)
	}
	const sprint = "nomirror-3804"
	var out, errOut bytes.Buffer
	if code := cmdCardFsck(ctx, []string{"--sprint", sprint, "--redis", addr}, &out, &errOut); code != 0 ||
		strings.Contains(out.String(), "NOMIRROR") || !strings.HasSuffix(out.String(), " nomirror=0\n") {
		t.Fatalf("fsck with no marks: %d %q %q", code, out.String(), errOut.String())
	}
	client.SAdd(ctx, "benches", "hulk", "space", "studio")
	client.SAdd(ctx, ci.NoMirrorKey("space"), "rowan-tools", "nova-tools")
	client.SAdd(ctx, ci.NoMirrorKey("hulk"), "nova-tools")
	out.Reset()
	errOut.Reset()
	code := cmdCardFsck(ctx, []string{"--sprint", sprint, "--redis", addr}, &out, &errOut)
	want := "NOMIRROR bench=hulk repos=nova-tools\nNOMIRROR bench=space repos=nova-tools,rowan-tools\nCARD FSCK sprint=" + sprint
	if code != 0 || !strings.HasPrefix(out.String(), want) || !strings.HasSuffix(out.String(), " drift=0 fixed=0 registered=0 notacard=0 removed=0 orphansets=0 nomirror=2\n") {
		t.Fatalf("fsck with marks: %d %q %q", code, out.String(), errOut.String())
	}
}

// TestCardFsckMemberNotACard (#4054) is the DONE-WHEN: a friend set with one
// member that is not the id of any record (the import-pipe shape found in
// a friend's ready set) fails card fsck as MEMBER-NOT-A-CARD with the set
// and the member, --repair removes it with a receipt on ws:log and keeps
// every member that is a record, and the move functions refuse to add it
// back: the card move (BADID) and the friend deal, whose adds are the one
// checked add.
func TestCardFsckMemberNotACard(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	addr := testutil.Start(t)
	client := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = client.Close() })
	if err := fn.Load(ctx, client); err != nil {
		t.Fatal(err)
	}
	const (
		sprint = "member-4054"
		stream = "swarm: cards"
		ready  = "friend:emma:cards:ready"
		wsRdy  = "ws:" + stream + ":ready"
		good   = "build-4054-good"
		bad    = "/private/tmp/nova/import-ab.pipe:task:build-3155-ghost"
	)
	for _, cmd := range [][]any{
		{"SADD", "friends", "emma"},
		{"ZADD", "ws:order", 1, stream},
		{"SADD", "ws:names", stream},
		{"HSET", "task:" + good, "stream", stream, "state", "ready", "owner", "emma", "created_at", 1000},
		{"ZADD", wsRdy, 1000, good},
		{"ZADD", ready, 1000, good},
		{"ZADD", ready, 2000, bad},
	} {
		if err := client.Do(ctx, cmd...).Err(); err != nil {
			t.Fatalf("%v: %v", cmd, err)
		}
	}
	run := func(args ...string) (int, string, string) {
		var out, errOut bytes.Buffer
		code := cmdCardFsck(ctx, args, &out, &errOut)
		return code, out.String(), errOut.String()
	}
	violation := "DRIFT MEMBER-NOT-A-CARD " + ready + " " + bad
	if code, out, errOut := run("--sprint", sprint, "--redis", addr); code != 1 ||
		!strings.Contains(errOut, violation) || !strings.Contains(out, " notacard=1 removed=0 ") || !strings.Contains(errOut, "--repair") {
		t.Fatalf("fsck with a member that is not a card: %d %q %q", code, out, errOut)
	}
	if !zMember(t, ctx, client, ready, bad) {
		t.Fatalf("a read-only fsck removed %q", bad)
	}
	if code, out, errOut := run("--sprint", sprint, "--redis", addr, "--repair"); code != 0 || !strings.Contains(out, " notacard=1 removed=1 ") {
		t.Fatalf("fsck --repair: %d %q %q", code, out, errOut)
	}
	if zMember(t, ctx, client, ready, bad) || !zMember(t, ctx, client, ready, good) || !zMember(t, ctx, client, wsRdy, good) {
		t.Fatalf("after repair %s = %v, %s = %v: want only %q", ready, client.ZRange(ctx, ready, 0, -1).Val(), wsRdy, client.ZRange(ctx, wsRdy, 0, -1).Val(), good)
	}
	log := client.XRange(ctx, "ws:log", "-", "+").Val()
	if len(log) != 1 || log[0].Values["id"] != bad || log[0].Values["set"] != ready || log[0].Values["why"] != "MEMBER-NOT-A-CARD" ||
		log[0].Values["from"] != "ready" || log[0].Values["by"] != "card fsck" {
		t.Fatalf("ws:log after repair = %v, want one MEMBER-NOT-A-CARD receipt for %q", log, bad)
	}
	if code, out, errOut := run("--sprint", sprint, "--redis", addr); code != 0 || !strings.Contains(out, " notacard=0 removed=0 ") {
		t.Fatalf("fsck after repair: %d %q %q", code, out, errOut)
	}

	// The move functions refuse to add it back, and write nothing.
	if got := client.FCall(ctx, "ns_card_move", nil, bad, "ready").Val(); got != "REFUSED BADID "+bad {
		t.Fatalf("ns_card_move of %q = %v, want REFUSED BADID", bad, got)
	}
	ghost := "s:" + sprint + ":card:ghost"
	if got := client.FCall(ctx, "ns_card_move", nil, ghost, "ready").Val(); got != "REFUSED NOCARD "+ghost {
		t.Fatalf("ns_card_move of %q = %v, want REFUSED NOCARD", ghost, got)
	}
	client.HSet(ctx, "lease:reconciler", "token", "tok-4054")
	client.Set(ctx, "friend:emma:slots", "5", 0)
	reply, err := client.FCall(ctx, "ns_deal_friend", nil, "tok-4054", "emma", "test", "4054", bad, good).Slice()
	dealt := fmt.Sprint(reply)
	if err != nil || !strings.HasPrefix(dealt, "[DEALT 1 5 1 "+bad+" no task ") {
		t.Fatalf("ns_deal_friend = %s, %v: want the good task dealt and %q refused", dealt, err, bad)
	}
	if zMember(t, ctx, client, ready, bad) || zMember(t, ctx, client, "friend:emma:cards:working", bad) ||
		!zMember(t, ctx, client, "friend:emma:cards:working", good) || !zMember(t, ctx, client, "ws:"+stream+":working", good) {
		t.Fatalf("after the deal: friend working %v, ws working %v", client.ZRange(ctx, "friend:emma:cards:working", 0, -1).Val(), client.ZRange(ctx, "ws:"+stream+":working", 0, -1).Val())
	}
	if code, out, errOut := run("--sprint", sprint, "--redis", addr); code != 0 || !strings.Contains(out, " notacard=0 ") {
		t.Fatalf("fsck after the refused adds: %d %q %q", code, out, errOut)
	}
}

func zMember(t *testing.T, ctx context.Context, client *redis.Client, key, member string) bool {
	t.Helper()
	_, err := client.ZScore(ctx, key, member).Result()
	if err == redis.Nil {
		return false
	}
	if err != nil {
		t.Fatal(err)
	}
	return true
}
