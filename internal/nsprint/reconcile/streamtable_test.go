package reconcile_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/life"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/reconcile"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/redis/go-redis/v9"
)

// TestHelperStreamChild is the fake bench's child (a friend:rowan copy's
// Opus child): when friend serve execs this test binary with
// NOVA_STREAM_CHILD set, it opens its PR from rowan/<copy> (it writes the PR
// record pr record writes, at the card's base) and ends on the typed line
// naming it. Without the variable it is an empty test.
func TestHelperStreamChild(t *testing.T) {
	if os.Getenv("NOVA_STREAM_CHILD") == "" {
		return
	}
	c := redis.NewClient(&redis.Options{Addr: os.Getenv("NOVA_STREAM_REDIS")})
	defer c.Close()
	repo, base, pr, head := os.Getenv("NOVA_STREAM_REPO"), os.Getenv("NOVA_STREAM_BASE"), os.Getenv("NOVA_STREAM_PR"),
		os.Getenv("NOVA_STREAM_HEAD")
	id := os.Getenv(life.ServeEnvCopy)
	c.HSet(context.Background(), "pr:"+repo+":"+pr, "repo", repo, "n", pr, "head", head, "base", base,
		"branch", os.Getenv(life.ServeEnvFriend)+"/"+id, "state", "open")
	fmt.Printf("DONE %s pr=%s#%s head=%s\n", id, repo, pr, head)
}

const (
	stStream  = "nova-sprint + merge + bus"
	stSprint  = "s-3999"
	stBaseSHA = "0123456789abcdef0123456789abcdef01234567"
	stHead    = "abcabcabcabcabcabcabcabcabcabcabcabcabca"
	stMerge   = "feedfeedfeedfeedfeedfeedfeedfeedfeedfeed"
)

// TestStreamTableTrueEndToEnd is #3999's DONE-WHEN control, once per repo
// (streams and sets are repo-agnostic: the ref and the PR carry the repo).
// One card, pushed to waiting, walks waiting -> working -> reading ->
// merging -> landed, each move made by a duty or a copy's end and by nothing
// else: the deal duty (DealPass: the cut and its fill), the fake bench
// (friend serve --as rowan running a fake Opus child whose typed line ends
// the copy through card end with its PR: the move to reading and the read
// copy's cut), fake CI (our own ci run's three calls, green at the PR head:
// no move), a fake reader (bench:reader's read copy ended 9/10) and the
// land duty's merge. After every event the five cells change by exactly the
// one move, the record's where names it, ws:log has one move entry naming
// the event, and task fsck and card fsck are clean.
func TestStreamTableTrueEndToEnd(t *testing.T) {
	for _, tc := range []struct{ repo, base string }{{"nova-tools", "dev"}, {"rowan-tools", "main"}} {
		t.Run(tc.repo, func(t *testing.T) { streamTable(t, tc.repo, tc.base) })
	}
}

func streamTable(t *testing.T, repo, base string) {
	ctx := context.Background()
	addr := testutil.Start(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = c.Close() })
	must(t, fn.Load(ctx, c))
	st := store.New(c)
	rowan, reader := taskcard.Consumer{Kind: "friend", Name: "rowan"}, taskcard.Consumer{Kind: "bench", Name: "reader"}
	c.SAdd(ctx, "friends", "rowan")
	c.SAdd(ctx, "benches", "reader", "ci")
	c.HSet(ctx, rowan.DesiredKey(), "slots", "1")
	c.HSet(ctx, reader.DesiredKey(), "slots", "1")
	c.HSet(ctx, "cfg:ci", "max_attempts", "1")
	for _, k := range []taskcard.Consumer{rowan, reader} {
		must(t, taskcard.Enroll(ctx, c, k, true))
	}
	benchBeat := func() { c.HSet(ctx, reader.BeatKey(), "at", strconv.FormatInt(time.Now().UnixMilli(), 10)) }
	benchBeat()
	step := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}

	id := repo + "-3999"
	ref := repo + "#3999"
	walk, err := reconcile.NewWalk(ctx, c, stStream, id, stSprint)
	step(err)

	// 1. the import: card push to waiting in ONE call, the whole record.
	_, err = taskcard.Push(ctx, c, taskcard.PushRequest{ID: id, Where: "waiting", Stream: stStream, Sprint: stSprint,
		Kind: "build", Ref: ref, Origin: "issue:mas-bandwidth/" + ref, Title: "the stream table is true end to end",
		Repo: "mas-bandwidth/" + repo, By: "import", Why: "import " + ref, Fields: []string{"base", base,
			"base_sha", stBaseSHA, "who", "only rowan", "paths", "internal/nsprint/reconcile/streamtable.go",
			"done_when", "`go test ./internal/nsprint/reconcile -run TestStreamTableTrueEndToEnd` passes"}})
	step(err)
	step(walk.Move(ctx, "import", "waiting"))

	// 2. the deal duty: rowan's serve beats (its seat is up), the pass cuts
	// the work copy on friend:rowan and fills it (waiting -> working).
	srv, err := life.NewServer(st, life.ServeConfig{Friend: "rowan", Session: "s-rowan", Host: "studio", Harness: "fake",
		Width: 1, Dispatch: []string{os.Args[0], "-test.run=^TestHelperStreamChild$"}, Dir: t.TempDir(), Out: logWriter{t},
		Env: []string{"NOVA_STREAM_CHILD=1", "NOVA_STREAM_REDIS=" + addr, "NOVA_STREAM_REPO=" + repo,
			"NOVA_STREAM_BASE=" + base, "NOVA_STREAM_PR=7999", "NOVA_STREAM_HEAD=" + stHead}})
	step(err)
	step(srv.Start(ctx))
	d, err := taskcard.DealPass(ctx, c, "reconciler", time.Now())
	if err != nil || d.Dealt != 1 || d.Worked != 1 {
		t.Fatalf("deal pass %+v %v", d, err)
	}
	step(walk.Move(ctx, "deal duty", "working"))

	// 3. the fake bench: friend serve --as rowan runs the copy the deal
	// duty filled and ends it through card end with the PR: the copy's ok
	// moves the primary to reading and cuts its read copy on the reader in
	// the same call (never the author).
	res, err := srv.Pass(ctx)
	if err != nil || res.Taken != 1 {
		t.Fatalf("serve pass %+v %v", res, err)
	}
	if n, err := srv.Wait(ctx); err != nil || n != 1 {
		t.Fatalf("serve wait %d %v", n, err)
	}
	step(srv.Release(ctx))
	cp := c.HGetAll(ctx, taskcard.Key(id+"~1")).Val()
	if cp["where"] != "ok" || cp["pr"] != "7999" || cp["head"] != stHead {
		t.Fatalf("work copy %v", cp)
	}
	step(walk.Move(ctx, "bench end", "reading"))
	rc := c.HGet(ctx, taskcard.Key(id), "copy").Val()
	if h := c.HGetAll(ctx, taskcard.Key(rc)).Val(); h["leg"] != "read" || h["consumer"] != reader.String() {
		t.Fatalf("read copy %s %v", rc, h)
	}

	// 4. fake CI: our own ci run at the PR head, green. A verdict moves no
	// card: it gates the read copy's end.
	ciGreen(t, c, repo, 7999, stHead)
	step(walk.Hold(ctx, "ci"))

	// 5. the deal duty fills the reader; the fake reader ends its copy 9/10.
	benchBeat()
	if _, err := taskcard.DealPass(ctx, c, "reconciler", time.Now()); err != nil {
		t.Fatal(err)
	}
	step(walk.Hold(ctx, "deal duty fills the read"))
	if _, err := taskcard.End(ctx, c, taskcard.EndRequest{IDs: []string{rc}, OK: true, Score: 9, Reader: "reader",
		Gates: "ci:green,base:ok,scope:ok", By: "reader"}); err != nil {
		t.Fatal(err)
	}
	step(walk.Move(ctx, "read end", "merging"))

	// 6. the land duty: the merge moves it to landed.
	l, err := taskcard.LandStream(ctx, c, stStream, stMerge, "land-duty", "")
	if err != nil || len(l.Landed) != 1 || l.Landed[0].ID != id {
		t.Fatalf("land %+v %v", l, err)
	}
	step(walk.Move(ctx, "land duty", "landed"))
	step(walk.Walked())

	events := map[string]string{"import": "import " + ref, "deal duty": "work copy to friend:rowan",
		"bench end": "ok pr " + repo + "#7999 head " + stHead[:12], "read end": "read 9/10",
		"land duty": "stream merged " + stMerge}
	for _, s := range walk.Steps {
		if !strings.HasPrefix(s.Event, events[s.Name]) {
			t.Errorf("step %s: ws:log event %q by %s, want %q", s.Name, s.Event, s.By, events[s.Name])
		}
		t.Logf("MOVE %-10s %s -> %s event=%q by=%s", s.Name, s.From, s.To, s.Event, s.By)
	}
	if len(walk.Steps) != 5 {
		t.Fatalf("%d moves, want 5", len(walk.Steps))
	}
}

// ciGreen is our own CI at one head through its three calls (request,
// claim, receipt) with rc 0.
func ciGreen(t *testing.T, c *redis.Client, repo string, pr int, sha string) {
	t.Helper()
	ctx := context.Background()
	if v, err := c.FCall(ctx, "ns_ci_request", nil, repo, sha, pr, "", "", "unit", "go test ./...").Result(); err != nil {
		t.Fatalf("ci request %v %v", v, err)
	}
	if v, err := c.FCall(ctx, "ns_ci_claim", nil, "ci", "tok-"+sha, 60000).Result(); err != nil || fmt.Sprint(v.([]any)[0]) != "CLAIMED" {
		t.Fatalf("ci claim %v %v", v, err)
	}
	if v, err := c.FCall(ctx, "ns_ci_receipt", nil, repo, sha, "unit", "tok-"+sha, "0", 10, "log", "ci", 60000, "").Result(); err != nil {
		t.Fatalf("ci receipt %v %v", v, err)
	}
}

type logWriter struct{ t *testing.T }

func (w logWriter) Write(p []byte) (int, error) {
	w.t.Log(strings.TrimRight(string(p), "\n"))
	return len(p), nil
}

// TestStreamWalkNamesTheStep: the control fails on anything but the one
// move, and the failure names the step (what the nightly control reopens
// #3999 with): a second card in the stream moves a cell the event did not.
func TestStreamWalkNamesTheStep(t *testing.T) {
	ctx := context.Background()
	c := redis.NewClient(&redis.Options{Addr: testutil.Start(t)})
	t.Cleanup(func() { _ = c.Close() })
	must(t, fn.Load(ctx, c))
	walk, err := reconcile.NewWalk(ctx, c, stStream, "a", stSprint)
	must(t, err)
	push := func(id string) {
		_, err := taskcard.Push(ctx, c, taskcard.PushRequest{ID: id, Where: "waiting", Stream: stStream, Sprint: stSprint,
			Kind: "build", Ref: "nova-tools#1", Title: id, By: "import"})
		must(t, err)
	}
	push("a")
	must(t, walk.Move(ctx, "import", "waiting"))
	push("b")
	err = walk.Hold(ctx, "bench end")
	var se *reconcile.StepError
	if !errors.As(err, &se) || se.Step != "bench end" || !strings.Contains(se.Why, "cell waiting went 1 -> 2") {
		t.Fatalf("hold with a foreign move: %v", err)
	}
}
