//go:build functional

package main

import (
	"context"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/redis/go-redis/v9"
)

// TestCardCutFromLandsHundredInWaiting is nova-tools#4340's DONE-WHEN on a
// real store: 100 cards land in ws:<stream>:waiting from one file with 100
// receipts, each a task card whose record carries its issue, spec and
// blocked_on; a second run files nothing (the cut ledger holds every row),
// finds every card already cut and writes nothing more.
func TestCardCutFromLandsHundredInWaiting(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	addr := testutil.Start(t)
	client := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = client.Close() })
	if err := fn.Load(ctx, client); err != nil {
		t.Fatal(err)
	}
	forge := &fakeCutForge{}
	d := cutDepsRedis(forge, client)
	code, out := runCutFrom(cutFromOpts{Text: []byte(hundredRows()), Sprint: "cut-from"}, d)
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	if n := strings.Count("\n"+out, "\nCARD CUT row="); n != 100 {
		t.Fatalf("%d receipts, want 100:\n%s", n, out)
	}
	if n := client.ZCard(ctx, taskcard.StreamKey("swarm: cards", "waiting")).Val(); n != 100 {
		t.Fatalf("ws:swarm: cards:waiting holds %d, want 100", n)
	}
	rec, err := client.HGetAll(ctx, taskcard.Key("nova-tools-5006")).Result() // row 2, filed seventh
	if err != nil {
		t.Fatal(err)
	}
	for k, want := range map[string]string{"where": "waiting", "blocked_on": "c7", "ref": "mas-bandwidth/nova-tools#5006",
		"route": "pro", "base_sha": cutFromSHA, "paths": "cmd/nova-sprint/c2.go", "depends_on": "mas-bandwidth/nova-tools#5005"} {
		if rec[k] != want {
			t.Errorf("record %s = %q, want %q", k, rec[k], want)
		}
	}
	forge2 := &fakeCutForge{}
	d.File = forge2.file
	code, out = runCutFrom(cutFromOpts{Text: []byte(hundredRows()), Sprint: "cut-from"}, d)
	if code != 0 || len(forge2.titles) != 0 || strings.Count(out, " to=already ") != 100 ||
		!strings.Contains(out, "rows=100 cut=0 already=100 refused=0 filed=0 reused=100 ") {
		t.Fatalf("rerun: exit %d filed %d, want 0, nothing filed and 100 already:\n%s", code, len(forge2.titles), out)
	}
	if n := client.ZCard(ctx, taskcard.StreamKey("swarm: cards", "waiting")).Val(); n != 100 {
		t.Fatalf("rerun left %d in waiting, want 100", n)
	}
}

// cutDepsRedis are the verb's seams on a real store: the push and the cut
// ledger through taskcard, the forge a fake.
func cutDepsRedis(forge *fakeCutForge, client *redis.Client) cutFromDeps {
	d := cutDeps(forge, nil)
	d.Push = func(ctx context.Context, reqs []taskcard.PushRequest) ([]taskcard.PushOutcome, error) {
		return taskcard.PushMany(ctx, client, reqs)
	}
	d.LedgerRead = func(ctx context.Context, key string) (taskcard.CutLedger, error) {
		return taskcard.ReadCutLedger(ctx, client, key)
	}
	d.LedgerWrite = func(ctx context.Context, key, repo string, row, issue int) error {
		return taskcard.WriteCutLedger(ctx, client, key, repo, row, issue)
	}
	return d
}

// TestCardCutFromLedgerFilesNothingTwice is the cut ledger on a real store
// (the cold read of #4358). The fake forge numbers monotonically, so an
// issue filed twice is a second number. The first run stops at the third
// filing; cut:<sha256 of the file> holds rows 1 and 2; the rerun files rows
// 3 to 5 alone and finds rows 1 and 2 already cut; a third run files
// nothing. Every row is filed once, the ledger names each row's issue, and
// each record carries that issue.
func TestCardCutFromLedgerFilesNothingTwice(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	addr := testutil.Start(t)
	client := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = client.Close() })
	if err := fn.Load(ctx, client); err != nil {
		t.Fatal(err)
	}
	rows := "id\ttitle\tstream\tpaths\tdone-when\tdepends-on\troute\n" +
		"base\tBase\tledger\tp1.go\tgo test passes\t\tpro\n" +
		"\tTwo\tledger\tp2.go\tgo test passes\tbase\tflash\n" +
		"\tThree\tledger\tp3.go\tgo test passes\ttask:base\t\n" +
		"\tFour\tledger\tp4.go\tgo test passes\t#12\t\n" +
		"\tFive\tledger\tp5.go\tgo test passes\t\t\n"
	forge := &fakeCutForge{failAt: 3}
	d := cutDepsRedis(forge, client)
	code, out := runCutFrom(cutFromOpts{Text: []byte(rows)}, d)
	if code != 1 || !strings.Contains(out, "rows=5 cut=2 already=0 refused=3 filed=2 reused=0 ") {
		t.Fatalf("first run: exit %d:\n%s", code, out)
	}
	key := taskcard.CutLedgerKey([]byte(rows))
	forge.failAt = 0
	for run := 2; run <= 3; run++ {
		code, out = runCutFrom(cutFromOpts{Text: []byte(rows)}, d)
		want := "rows=5 cut=3 already=2 refused=0 filed=3 reused=2 "
		if run == 3 {
			want = "rows=5 cut=0 already=5 refused=0 filed=0 reused=5 "
		}
		if code != 0 || !strings.Contains(out, want) {
			t.Fatalf("run %d: exit %d, want %q:\n%s", run, code, want, out)
		}
	}
	if got, want := strings.Join(forge.titles, ","), "Base,Two,Three,Four,Five"; got != want {
		t.Fatalf("filed %s across three runs, want each row once: %s", got, want)
	}
	ledger, err := client.HGetAll(ctx, key).Result()
	if err != nil {
		t.Fatal(err)
	}
	wantLedger := map[string]string{"repo": "mas-bandwidth/nova-tools", "1": "5000", "2": "5001", "3": "5002", "4": "5003", "5": "5004"}
	if len(ledger) != len(wantLedger) {
		t.Fatalf("%s = %v, want %v", key, ledger, wantLedger)
	}
	for k, v := range wantLedger {
		if ledger[k] != v {
			t.Fatalf("%s = %v, want %v", key, ledger, wantLedger)
		}
	}
	if ttl := client.TTL(ctx, key).Val(); ttl != -1 {
		t.Fatalf("%s has a TTL %v; keys do not expire", key, ttl)
	}
	if n := client.ZCard(ctx, taskcard.StreamKey("ledger", "waiting")).Val(); n != 5 {
		t.Fatalf("ws:ledger:waiting holds %d, want 5", n)
	}
	for id, ref := range map[string]string{"base": "#5000", "nova-tools-5001": "#5001", "nova-tools-5004": "#5004"} {
		if got := client.HGet(ctx, taskcard.Key(id), "ref").Val(); got != "mas-bandwidth/nova-tools"+ref {
			t.Fatalf("task:%s ref %q, want mas-bandwidth/nova-tools%s", id, got, ref)
		}
	}
	if got := client.HGet(ctx, taskcard.Key("nova-tools-5002"), "blocked_on").Val(); got != "base" {
		t.Fatalf("row 3 blocked_on %q, want base", got)
	}
}
