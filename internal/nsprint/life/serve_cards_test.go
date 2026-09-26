package life_test

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/life"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
	"github.com/redis/go-redis/v9"
)

const cardStream = "nova-sprint + merge + bus"

// pushCard puts one task card in friend:emma:cards:ready.
func pushCard(t *testing.T, client *redis.Client, id, kind string, title ...string) {
	t.Helper()
	_, err := taskcard.Push(context.Background(), client, taskcard.PushRequest{
		ID: id, Stream: cardStream, Friend: "emma", Sprint: "s1", Kind: kind,
		Origin: "issue:nova-tools#4095",
		Title: "serve card " + id + " | PATHS: internal/nsprint/life/ | DONE-WHEN: `go test ./internal/nsprint/life/` exits 0" +
			strings.Join(title, ""),
		By: "rowan",
	})
	if err != nil {
		t.Fatalf("push card %s: %v", id, err)
	}
}

func receiptCount(t *testing.T, client *redis.Client, kind string) int {
	t.Helper()
	n := 0
	for _, k := range logKinds(t, client) {
		if k == kind {
			n++
		}
	}
	return n
}

// TestServeCardsWidthTwoFinishesFiveWithNoHandCall is the #4095 DONE-WHEN: a
// fixture seat with width 2 and five ready cards runs exactly two children at
// a time and finishes all five with no hand call. The loop's tick is a
// minute, so only a child's exit can take the third, fourth and fifth card:
// the exit is the trigger, not a timer. Each card's brief is rendered from
// its record by kind and each card ends with its child's typed line.
func TestServeCardsWidthTwoFinishesFiveWithNoHandCall(t *testing.T) {
	t.Parallel()
	// SLEEPS: this test waits on the wall clock (calls time.Sleep). Skipped 2026-09-25
	// by Glenn's rule ("unit tests must not have real sleeps or waits"): it becomes a
	// mocked-clock unit test or a functional program (nova-tools #4221).
	t.Skip("SLEEPS: needs a mocked clock or a functional test (nova-tools #4221)")

	st, client := seedSeat(t, 2)
	ctx := context.Background()
	for i := 1; i <= 5; i++ {
		pushCard(t, client, "c"+strconv.Itoa(i), "build")
	}
	if n := client.ZCard(ctx, "friend:emma:cards:ready").Val(); n != 5 {
		t.Fatalf("friend:emma:cards:ready holds %d, want 5", n)
	}
	cfg := serveConfig(t, "sess-cards", 2, "slot")
	cfg.Sprint = ""
	cfg.Tick = time.Minute
	cfg.Models = map[string]string{"build": "opus-5.5"}
	live, started := t.TempDir(), t.TempDir()
	seen := filepath.Join(t.TempDir(), "seen")
	cfg.Env = append(cfg.Env, "NOVA_SERVE_LIVE="+live, "NOVA_SERVE_STARTED="+started, "NOVA_SERVE_TOTAL=5",
		"NOVA_SERVE_SEEN="+seen)

	sctx, cancel := context.WithCancel(ctx)
	served := make(chan error, 1)
	go func() { served <- life.Serve(sctx, st, cfg) }()
	deadline := time.Now().Add(30 * time.Second)
	for client.ZCard(ctx, "friend:emma:cards:done").Val() < 5 {
		if time.Now().After(deadline) {
			cancel()
			<-served
			t.Fatalf("after 30s: ready=%d working=%d done=%d; a freed slot was not refilled without a tick",
				client.ZCard(ctx, "friend:emma:cards:ready").Val(), client.ZCard(ctx, "friend:emma:cards:working").Val(),
				client.ZCard(ctx, "friend:emma:cards:done").Val())
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	if err := <-served; err != nil {
		t.Fatalf("serve: %v", err)
	}

	raw, err := os.ReadFile(seen)
	if err != nil {
		t.Fatal(err)
	}
	counts := strings.Fields(string(raw))
	if len(counts) != 5 {
		t.Fatalf("%d children started, want 5: %q", len(counts), counts)
	}
	most := 0
	for _, c := range counts {
		n, _ := strconv.Atoi(c)
		if n > most {
			most = n
		}
	}
	if most != 2 {
		t.Fatalf("children saw at most %d live (%v), want exactly 2 at a time", most, counts)
	}
	for i := 1; i <= 5; i++ {
		id := "c" + strconv.Itoa(i)
		h := client.HGetAll(ctx, taskcard.Key(id)).Val()
		if h["where"] != "done" || h["where_ok"] != "ok" {
			t.Fatalf("card %s where=%s where_ok=%s, want done/ok", id, h["where"], h["where_ok"])
		}
		if want := "DONE built " + id + " model=opus-5.5"; h["evidence"] != want {
			t.Fatalf("card %s evidence %q, want the child's typed line %q", id, h["evidence"], want)
		}
	}
	for _, where := range []string{"ready", "working"} {
		if n := client.ZCard(ctx, "friend:emma:cards:"+where).Val(); n != 0 {
			t.Fatalf("friend:emma:cards:%s holds %d after the serve", where, n)
		}
	}
	// The seat's own receipts, in order: a start is +1 live, an exit -1;
	// the width is never exceeded and is reached.
	running, peak := 0, 0
	for _, k := range logKinds(t, client) {
		switch k {
		case "start":
			running++
			peak = max(peak, running)
		case "exit":
			running--
		}
	}
	if peak != 2 || running != 0 {
		t.Fatalf("friend:emma:log peaks at %d live and ends at %d, want 2 and 0", peak, running)
	}
	if n := receiptCount(t, client, "take"); n != 5 {
		t.Fatalf("%d take receipts on friend:emma:log, want 5", n)
	}
	briefs, _ := filepath.Glob(filepath.Join(cfg.Dir, life.CardsDirName, "c1-*", "brief.md"))
	if len(briefs) != 1 {
		t.Fatalf("card c1 briefs: %v", briefs)
	}
	b, _ := os.ReadFile(briefs[0])
	for _, want := range []string{"CARD: c1\n", "# Build brief", "PATHS: internal/nsprint/life/",
		"Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>", "ORIGIN: issue:nova-tools#4095"} {
		if !strings.Contains(string(b), want) {
			t.Fatalf("c1 brief lacks %q:\n%s", want, b)
		}
	}
}

// TestServeCardWithoutTypedLineFails: a card child that exits without a typed
// line fails its card (done/fail with the exit and its last line); a card
// whose kind has no template, a card with no model for its brief's co-author
// line, and a card whose kind has no model for a dispatch that says @model
// fail before any exec. Nothing is left working.
func TestServeCardWithoutTypedLineFails(t *testing.T) {
	t.Parallel()

	st, client := seedSeat(t, 3)
	ctx := context.Background()
	pushCard(t, client, "dies", "build")
	pushCard(t, client, "harvest", "harvest")
	pushCard(t, client, "nocoauthor", "fix")
	pushCard(t, client, "nomodel", "rebase", " | MODEL: fable-5.1")
	cfg := serveConfig(t, "sess-fail", 4, "die")
	cfg.Sprint = ""
	cfg.Models = map[string]string{"build": "opus-5.5"}
	cfg.Dispatch = append(cfg.Dispatch, "--", "@model")
	res, err := life.ServeOnce(ctx, st, cfg)
	if err != nil {
		t.Fatalf("serve once: %v", err)
	}
	if res.Taken != 4 || res.Closed != 4 || res.Live != 0 {
		t.Fatalf("pass = %+v, want taken 4 closed 4 live 0", res)
	}
	for id, want := range map[string]string{
		"dies":       "no typed line: boom: harness crashed before any line",
		"harvest":    "field=kind want=build|fix|read|rebase got=harvest",
		"nocoauthor": "field=model got=ABSENT",
		"nomodel":    "no model for kind rebase: pass --model rebase=<model>",
	} {
		h := client.HGetAll(ctx, taskcard.Key(id)).Val()
		if h["where"] != "done" || h["where_ok"] != "fail" || !strings.Contains(h["evidence"], want) {
			t.Fatalf("card %s where=%s where_ok=%s evidence=%q, want done/fail with %q", id, h["where"], h["where_ok"], h["evidence"], want)
		}
	}
	if n := client.ZCard(ctx, "friend:emma:cards:working").Val(); n != 0 {
		t.Fatalf("friend:emma:cards:working holds %d", n)
	}
	if n := receiptCount(t, client, "fail"); n != 4 {
		t.Fatalf("%d fail receipts, want 4", n)
	}
}

// TestServeStopGivesCardBack: a serve stopped while its card child lives kills
// the child and gives the card back to friend:emma:cards:ready (working ->
// ready, the one move), so no card sits in working without a child.
func TestServeStopGivesCardBack(t *testing.T) {
	t.Parallel()
	// SLEEPS: this test waits on the wall clock (calls time.Sleep). Skipped 2026-09-25
	// by Glenn's rule ("unit tests must not have real sleeps or waits"): it becomes a
	// mocked-clock unit test or a functional program (nova-tools #4221).
	t.Skip("SLEEPS: needs a mocked clock or a functional test (nova-tools #4221)")

	st, client := seedSeat(t, 1)
	ctx := context.Background()
	pushCard(t, client, "held", "build")
	cfg := serveConfig(t, "sess-stop", 1, "wait")
	cfg.Sprint = ""
	cfg.Models = map[string]string{"build": "opus-5.5"}
	sctx, cancel := context.WithCancel(ctx)
	served := make(chan error, 1)
	go func() { served <- life.Serve(sctx, st, cfg) }()
	deadline := time.Now().Add(20 * time.Second)
	for client.ZCard(ctx, "friend:emma:cards:working").Val() != 1 {
		if time.Now().After(deadline) {
			cancel()
			<-served
			t.Fatal("the card never reached working")
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	if err := <-served; err != nil {
		t.Fatalf("serve: %v", err)
	}
	h := client.HGetAll(ctx, taskcard.Key("held")).Val()
	if h["where"] != "ready" || client.ZCard(ctx, "friend:emma:cards:ready").Val() != 1 ||
		client.ZCard(ctx, "friend:emma:cards:working").Val() != 0 {
		t.Fatalf("after stop: where=%s ready=%d working=%d, want the card back in ready", h["where"],
			client.ZCard(ctx, "friend:emma:cards:ready").Val(), client.ZCard(ctx, "friend:emma:cards:working").Val())
	}
}
