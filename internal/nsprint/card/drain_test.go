package card_test

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card"
	"github.com/redis/go-redis/v9"
)

// TestDrainReleasesAndImportsOnce is the #3035 control. One drain puts parked
// work back: three waiting cards whose parent landed are released, two cards
// parked on a paused bench are resumed, and four card files left in a retired
// fillloop queue dir are imported create-only with one receipt each. A second
// drain finds nothing to do, writes no receipt, and exits 0. A queue-dir card
// that fails lint is REFUSED and stays where it is.
func TestDrainReleasesAndImportsOnce(t *testing.T) {
	ctx := context.Background()
	client := newRedis(t)
	srv := repoServer(t)
	repo := srv.URL + "/acme/public.git"
	const bench = "bench:studio-3035"

	// Three waiting cards whose parent lands.
	parent := validCard(repo)
	parent.label = "parent-3035"
	mustPush(t, ctx, client, parent.render(), "pool")
	var released []string
	for _, l := range []string{"child-a-3035", "child-b-3035", "child-c-3035"} {
		c := validCard(repo)
		c.label = l
		c.depends = "parent-3035"
		mustPush(t, ctx, client, c.render(), "waiting")
		released = append(released, l)
	}
	mustLand(t, ctx, client, "parent-3035", strings.Repeat("d", 40))

	// Two cards parked on a paused bench: out of the pool, in the bench's
	// parked set, and the bench in s:<S>:paused.
	var parked []string
	for _, l := range []string{"parked-a-3035", "parked-b-3035"} {
		c := validCard(repo)
		c.label = l
		mustPush(t, ctx, client, c.render(), "pool")
		if err := client.ZRem(ctx, keyPool(), l).Err(); err != nil {
			t.Fatal(err)
		}
		if err := client.SAdd(ctx, keyParked(bench), l).Err(); err != nil {
			t.Fatal(err)
		}
		parked = append(parked, l)
	}
	if err := client.SAdd(ctx, keyPaused(), bench).Err(); err != nil {
		t.Fatal(err)
	}
	if n := client.ZCard(ctx, keyPool()).Val(); n != 0 {
		t.Fatalf("fixture pool has %d cards before drain, want 0", n)
	}

	// Four card files in a retired queue dir. They depend on a card that has
	// not landed, so they import into waiting and the pool holds exactly the
	// five released and resumed cards.
	later := validCard(repo)
	later.label = "later-3035"
	mustPush(t, ctx, client, later.render(), "pool")
	if err := client.ZRem(ctx, keyPool(), later.label).Err(); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	var imported []string
	for _, l := range []string{"queued-a-3035", "queued-b-3035", "queued-c-3035", "queued-d-3035"} {
		c := validCard(repo)
		c.label = l
		c.depends = "later-3035"
		writeCard(t, dir, l+".card", c.render())
		imported = append(imported, l)
	}

	opts := card.DrainOptions{Resume: []string{bench}, QueueDirs: []string{dir}}
	logBefore := client.XLen(ctx, keyLog()).Val()
	res := card.Drain(ctx, client, sprint, opts)
	if res.Code != 0 || res.Stderr != "" {
		t.Fatalf("first drain: exit %d stdout %q stderr %q", res.Code, res.Stdout, res.Stderr)
	}
	for _, want := range []string{
		"DRAIN OK sprint=" + sprint + " item=resume member=" + bench + " moved=2 was_paused=1\n",
		"DRAIN OK sprint=" + sprint + " item=release moved=3 waiting=4\n",
		"DRAIN DONE sprint=" + sprint + " released=3 resumed=2 imported=4 refused=0 receipts=4\n",
	} {
		if !strings.Contains(res.Stdout, want) {
			t.Fatalf("first drain stdout %q lacks %q", res.Stdout, want)
		}
	}
	// waiting=4 pins the order resume, import, release: release saw the four
	// imported cards (their parent has not landed) and moved only the three.
	for _, l := range imported {
		line := "DRAIN OK sprint=" + sprint + " item=import file=" + l + ".card label=" + l + " place=waiting receipt=1\n"
		if !strings.Contains(res.Stdout, line) {
			t.Fatalf("first drain stdout %q lacks %q", res.Stdout, line)
		}
	}

	pool := client.ZRange(ctx, keyPool(), 0, -1).Val()
	sort.Strings(pool)
	wantPool := append(append([]string{}, released...), parked...)
	sort.Strings(wantPool)
	if strings.Join(pool, ",") != strings.Join(wantPool, ",") {
		t.Fatalf("pool = %v, want %v", pool, wantPool)
	}
	for _, l := range imported {
		assertPlace(t, ctx, client, l, false, true)
	}
	if n := client.SCard(ctx, keyParked(bench)).Val(); n != 0 {
		t.Fatalf("bench parked set still holds %d cards", n)
	}
	if client.SIsMember(ctx, keyPaused(), bench).Val() {
		t.Fatalf("%s is still paused after drain resumed it", bench)
	}
	receipts := drainReceipts(t, ctx, client)
	if len(receipts) != 4 {
		t.Fatalf("receipts = %v, want one per imported card", receipts)
	}
	for _, l := range imported {
		if receipts[l] != 1 {
			t.Fatalf("card %s has %d receipts, want 1 (all: %v)", l, receipts[l], receipts)
		}
	}
	if left := dirEntries(t, dir); len(left) != 0 {
		t.Fatalf("queue dir not empty after drain: %v", left)
	}
	if client.XLen(ctx, keyLog()).Val() <= logBefore {
		t.Fatalf("first drain wrote nothing to the sprint log")
	}

	// Second drain: nothing parked, nothing waiting on a landed parent, an
	// empty queue dir. It writes no receipt and no log entry, and exits 0.
	logAfter := client.XLen(ctx, keyLog()).Val()
	res = card.Drain(ctx, client, sprint, opts)
	if res.Code != 0 || res.Stderr != "" {
		t.Fatalf("second drain: exit %d stdout %q stderr %q", res.Code, res.Stdout, res.Stderr)
	}
	if want := "DRAIN DONE sprint=" + sprint + " released=0 resumed=0 imported=0 refused=0 receipts=0\n"; !strings.Contains(res.Stdout, want) {
		t.Fatalf("second drain stdout %q lacks %q", res.Stdout, want)
	}
	if n := client.XLen(ctx, keyLog()).Val(); n != logAfter {
		t.Fatalf("second drain wrote %d log entries, want 0", n-logAfter)
	}
	if n := len(drainReceipts(t, ctx, client)); n != 4 {
		t.Fatalf("second drain changed the receipt count to %d", n)
	}

	// A queue-dir card that fails lint is REFUSED, left in place, and writes
	// no receipt. The rest of the drain still runs and the exit is 2.
	bad := validCard(repo)
	bad.label = "bad-3035"
	bad.omit = map[string]bool{"DONE-WHEN": true}
	writeCard(t, dir, "bad-3035.card", bad.render())
	res = card.Drain(ctx, client, sprint, opts)
	if res.Code != 2 {
		t.Fatalf("lint-failing card: exit %d stdout %q stderr %q, want exit 2", res.Code, res.Stdout, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "DRAIN REFUSED sprint="+sprint+" item=import file=bad-3035.card reason=missing\\x20DONE-WHEN\n") {
		t.Fatalf("lint-failing card: stdout %q lacks the REFUSED line", res.Stdout)
	}
	if left := dirEntries(t, dir); len(left) != 1 || left[0] != "bad-3035.card" {
		t.Fatalf("refused card not left in place: %v", left)
	}
	assertAbsent(t, ctx, client, "bad-3035")
	if n := len(drainReceipts(t, ctx, client)); n != 4 {
		t.Fatalf("refused card wrote a receipt: %d receipts", n)
	}
	if n := client.XLen(ctx, keyLog()).Val(); n != logAfter {
		t.Fatalf("refused drain wrote %d log entries, want 0", n-logAfter)
	}
	// #3419: resume and the import receipt are library Functions (FCALL);
	// the server counts no EVAL, EVALSHA or SCRIPT from any client, over the
	// pushes, the land and all three drains.
	if hits := adHocScriptCalls(t, ctx, client); len(hits) != 0 {
		t.Fatalf("drain sent ad-hoc scripting commands: %v", hits)
	}
}

// adHocScriptCalls reads the server's command counters for EVAL, EVALSHA
// (and their _RO forms) and SCRIPT: an ad-hoc script from any client on any
// path is counted, refused or not. FCALL and FUNCTION are not in the list
// (drain runs as the owner, which loads the library).
func adHocScriptCalls(t *testing.T, ctx context.Context, client *redis.Client) []string {
	t.Helper()
	info, err := client.Info(ctx, "commandstats").Result()
	if err != nil {
		t.Fatal(err)
	}
	var hits []string
	for _, line := range strings.Split(info, "\n") {
		name, _, _ := strings.Cut(strings.TrimSpace(line), ":")
		name = strings.TrimPrefix(name, "cmdstat_")
		if strings.HasPrefix(name, "eval") || strings.HasPrefix(name, "script") {
			hits = append(hits, strings.TrimSpace(line))
		}
	}
	return hits
}

// TestDrainRefusesBadInput is the negative control for the arguments: a bad
// sprint, a member that is neither bench: nor friend:, and a missing queue dir.
func TestDrainRefusesBadInput(t *testing.T) {
	ctx := context.Background()
	client := newRedis(t)
	if res := card.Drain(ctx, client, "Bad Sprint", card.DrainOptions{}); res.Code != 2 {
		t.Fatalf("bad sprint: exit %d", res.Code)
	}
	if res := card.Drain(ctx, client, sprint, card.DrainOptions{Resume: []string{"studio"}}); res.Code != 2 || !strings.Contains(res.Stderr, "bench:<b> or friend:<f>") {
		t.Fatalf("bare member: exit %d stderr %q", res.Code, res.Stderr)
	}
	missing := filepath.Join(t.TempDir(), "gone")
	res := card.Drain(ctx, client, sprint, card.DrainOptions{QueueDirs: []string{missing}})
	if res.Code != 2 || !strings.Contains(res.Stdout, "DRAIN REFUSED sprint="+sprint+" item=queue-dir") {
		t.Fatalf("missing queue dir: exit %d stdout %q stderr %q", res.Code, res.Stdout, res.Stderr)
	}
}

func keyParked(member string) string { return "s:" + sprint + ":parked:" + member }
func keyPaused() string              { return "s:" + sprint + ":paused" }
func keyLog() string                 { return "s:" + sprint + ":log" }

func writeCard(t *testing.T, dir, name string, body []byte) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), body, 0o644); err != nil {
		t.Fatal(err)
	}
}

func dirEntries(t *testing.T, dir string) []string {
	t.Helper()
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range ents {
		names = append(names, e.Name())
	}
	return names
}

// drainReceipts counts the drain-import entries in the sprint log per card.
func drainReceipts(t *testing.T, ctx context.Context, client *redis.Client) map[string]int {
	t.Helper()
	msgs, err := client.XRange(ctx, keyLog(), "-", "+").Result()
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]int{}
	for _, m := range msgs {
		if m.Values["actor"] == "drain-import" {
			id, _ := m.Values["id"].(string)
			out[id]++
		}
	}
	return out
}

func TestDrainControl(t *testing.T) {
	ctx := context.Background()
	client := newRedis(t)
	const ctrl = "control-test-3442"

	keysBefore, err := client.Keys(ctx, "*").Result()
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(keysBefore)

	if err := client.HSet(ctx, "s:"+ctrl, "status", "open").Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.HSet(ctx, "bench:"+ctrl+"-studio", "host", "h1", "at", "1").Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.SAdd(ctx, "benches", ctrl+"-studio").Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.HSet(ctx, "machine:"+ctrl+"-host:ceiling", "slots", 1).Err(); err != nil {
		t.Fatal(err)
	}
	if err := card.RegisterKey(ctx, client, ctrl, "extra-control-key"); err != nil {
		t.Fatal(err)
	}
	if err := client.Set(ctx, "extra-control-key", "val", 0).Err(); err != nil {
		t.Fatal(err)
	}

	res := card.Drain(ctx, client, "", card.DrainOptions{Control: ctrl})
	if res.Code != 0 || res.Stderr != "" {
		t.Fatalf("drain control: exit %d stdout %q stderr %q", res.Code, res.Stdout, res.Stderr)
	}

	keysAfter, err := client.Keys(ctx, "*").Result()
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(keysAfter)

	if strings.Join(keysAfter, ",") != strings.Join(keysBefore, ",") {
		t.Fatalf("keyspace after teardown != before:\nbefore: %v\nafter:  %v", keysBefore, keysAfter)
	}
}

