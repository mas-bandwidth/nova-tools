//go:build functional

package main

import (
	"bytes"
	"context"
	"fmt"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/reconcile"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws"
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
	if n := client.ZCard(ctx, taskcard.StreamKeyAt(0, "swarm: cards", "waiting")).Val(); n != 101 { // 100 cards and the stream's sentinel (#4318)
		t.Fatalf("ws:swarm: cards:waiting holds %d, want 100 cards and the sentinel", n)
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
	if n := client.ZCard(ctx, taskcard.StreamKeyAt(0, "swarm: cards", "waiting")).Val(); n != 101 { // the 100 and the sentinel
		t.Fatalf("rerun left %d in waiting, want 100 cards and the sentinel", n)
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
	// a swarm card names its test or says why it has none (#4313)
	// one-invariant bodies (#4396) and a TEST cell per swarm row (#4313)
	rows := strings.ReplaceAll("id\ttitle\tstream\tpaths\tdone-when\tbody\tdepends-on\troute\ttest\n"+
		"base\tBase\tledger\tp1.go\tgo test passes\tI\tnone\tpro\tnone the ledger fixture\n"+
		"\tTwo\tledger\tp2.go\tgo test passes\tI\tbase\tflash\tnone the ledger fixture\n"+
		"\tThree\tledger\tp3.go\tgo test passes\tI\ttask:base\t\tnone the ledger fixture\n"+
		"\tFour\tledger\tp4.go\tgo test passes\tI\t#12\t\tnone the ledger fixture\n"+
		"\tFive\tledger\tp5.go\tgo test passes\tI\t-\t\tnone the ledger fixture\n", "\tI\t", "\t"+cutInv+"\t")
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
	if n, _ := ws.CardCount(ctx, client, "ledger", "waiting"); n != 5 { // the cards, the stream's stop aside (#4318)
		t.Fatalf("ws:ledger:waiting holds %d cards, want 5", n)
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

// TestCardCutFromLintRefusalWritesNothing (#4396) on a real store: a file
// with one good row and one row that is a list (a lower-case a./b. BUILD:
// list) exits 2, prints one REFUSED card-lint line on stderr, files no issue
// and leaves the store's key count unchanged.
func TestCardCutFromLintRefusalWritesNothing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	addr := testutil.Start(t)
	client := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = client.Close() })
	if err := fn.Load(ctx, client); err != nil {
		t.Fatal(err)
	}
	rows := "id\ttitle\tstream\tpaths\tdone-when\tdepends-on\tbody\n" +
		"good\tGood\tlint\tp1.go\tgo test passes\tnone\t" + cutInv + "\n" +
		"list\tList\tlint\tp2.go\tgo test passes\tnone\t" + cutInv + `\nBUILD:\na. the parser\nb. the linter` + "\n"
	keys := client.DBSize(ctx).Val()
	forge := &fakeCutForge{}
	code, out, errOut := runCutFromErr(cutFromOpts{Text: []byte(rows)}, cutDepsRedis(forge, client))
	want := `REFUSED card-lint rule=build-list line="BUILD:" remedy="cut as a parent with children: card cut --parent" row=2` + "\n"
	if code != 2 || errOut != want || len(forge.titles) != 0 || !strings.Contains(out, `CARD CUT REFUSED row=2 line=3 id=list why="card-lint build-list: not one invariant"`) {
		t.Fatalf("exit %d filed %d stdout\n%s\nstderr\n%s\nwant exit 2 and\n%s", code, len(forge.titles), out, errOut, want)
	}
	if n := client.DBSize(ctx).Val(); n != keys {
		t.Fatalf("a card-lint refusal changed the key count %d -> %d", keys, n)
	}
}

// TestCardCutFromNoneResolvesToReady is #4399 item 8 on a real store: the
// cold walk's ten rows, depends-on `-` (the TSV's empty marker) or `none`,
// land in waiting with blocked_on none, and one pass of the reconciler's
// waiting-resolve duty moves all ten to ready (the walk logged RESOLVE
// ready=0 still=10 when the cut wrote no blocked_on). A row whose depends-on
// cell is empty is refused naming none, and nothing is written.
func TestCardCutFromNoneResolvesToReady(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	addr := testutil.Start(t)
	client := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = client.Close() })
	if err := fn.Load(ctx, client); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	b.WriteString("id\ttitle\tstream\tpaths\tdone-when\tdepends-on\tbody\n")
	for i := 1; i <= 10; i++ {
		dep := "-"
		if i%2 == 0 {
			dep = "none"
		}
		fmt.Fprintf(&b, "p%d\tprobe %d\tprobe-a\tp%d.go\tgo test passes\t%s\t%s\n", i, i, i, dep, cutInv)
	}
	d := cutDepsRedis(&fakeCutForge{}, client)
	code, out := runCutFrom(cutFromOpts{Text: []byte(b.String()), Sprint: "probe", NoGitHub: true}, d)
	if code != 0 || strings.Count(out, " to=waiting depends=none\n") != 10 {
		t.Fatalf("cut: exit %d, want ten cards to waiting depends=none:\n%s", code, out)
	}
	for i := 1; i <= 10; i++ {
		if bo := client.HGet(ctx, taskcard.Key(fmt.Sprintf("p%d", i)), "blocked_on").Val(); bo != "none" {
			t.Fatalf("task:p%d blocked_on %q, want none (the resolver's met marker)", i, bo)
		}
	}
	lease, err := reconcile.Acquire(ctx, store.New(client), reconcile.AcquireOptions{Host: "test"})
	if err != nil {
		t.Fatal(err)
	}
	var log bytes.Buffer
	counts, err := (&reconcile.WaitingResolve{Client: client, Out: &log}).Run(ctx, lease)
	if err != nil || counts.Routed != 10 || !strings.Contains(log.String(), "RESOLVE stream=probe-a ready=10 still=0 ") {
		t.Fatalf("resolve: routed %d err %v, want 10:\n%s", counts.Routed, err, log.String())
	}
	if n := client.ZCard(ctx, taskcard.StreamKeyAt(0, "probe-a", "ready")).Val(); n != 10 {
		t.Fatalf("ws:probe-a:ready holds %d, want the ten", n)
	}

	// An empty cell: refused with its row, nothing filed, pushed or ledgered.
	empty := "id\ttitle\tstream\tpaths\tdone-when\tbody\tdepends-on\nq1\tq one\tprobe-b\tq1.go\tgo test passes\t" + cutInv + "\t\nq2\tq two\tprobe-b\tq2.go\tgo test passes\t" + cutInv + "\tnone\n"
	forge := &fakeCutForge{}
	code, out = runCutFrom(cutFromOpts{Text: []byte(empty), Sprint: "probe", NoGitHub: true}, cutDepsRedis(forge, client))
	if code != 1 || !strings.Contains(out, `CARD CUT REFUSED row=1 line=2 id=q1 why="depends-on is empty: write none or the ids"`) ||
		!strings.Contains(out, "rows=2 cut=0 already=0 refused=1 filed=0 ") {
		t.Fatalf("empty cell: exit %d, want 1 and the row refused naming none:\n%s", code, out)
	}
	if n := client.Exists(ctx, taskcard.Key("q1"), taskcard.Key("q2")).Val(); n != 0 || len(forge.titles) != 0 ||
		client.ZCard(ctx, taskcard.StreamKeyAt(0, "probe-b", "waiting")).Val() != 0 {
		t.Fatalf("an empty cell wrote something: %d records, %d issues", n, len(forge.titles))
	}
}
