package life_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/life"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
	"github.com/redis/go-redis/v9"
)

// TestServeCopyEndsFromTheChildsExit (#3998): a copy dealt to friend:emma is
// taken by her seat with card work, dispatched with its rendered card as the
// brief, and returned with card end at the child's exit: a child that dies
// with no typed line fails the copy with the exit reason and the last line
// it wrote, its primary goes back to waiting, the friend's working set is
// empty, and the copy's lease was renewed while it ran (the start-ack beat).
func TestServeCopyEndsFromTheChildsExit(t *testing.T) {
	st, client := seedSeat(t, 2)
	ctx := context.Background()
	k := taskcard.Consumer{Kind: "friend", Name: "emma"}
	if err := taskcard.Enroll(ctx, client, k, true); err != nil {
		t.Fatal(err)
	}
	if _, err := taskcard.Push(ctx, client, taskcard.PushRequest{ID: "p1", Where: "waiting", Stream: "swarm: cards",
		Kind: "build", Title: "primary p1", Repo: "mas-bandwidth/nova-tools", By: "rowan",
		Fields: []string{"base", "dev", "base_sha", serveHead, "paths", "internal/x.go", "done_when", "go test ./internal/x passes"}}); err != nil {
		t.Fatal(err)
	}
	d, err := taskcard.Deal(ctx, client, taskcard.DealRequest{To: k, IDs: []string{"p1"}, By: "rowan"})
	if err != nil || len(d) != 1 {
		t.Fatalf("deal %v %v", d, err)
	}
	cfg := serveConfig(t, "sess-1", 2, "die")
	cfg.Sprint = ""
	res, err := life.ServeOnce(ctx, st, cfg)
	if err != nil || res.Taken != 1 || res.Closed != 1 {
		t.Fatalf("serve %+v %v", res, err)
	}
	cp := client.HGetAll(ctx, taskcard.Key(d[0].Copy)).Val()
	if cp["where"] != "fail" || !strings.Contains(cp["why"], "exit=3") || !strings.Contains(cp["why"], "boom") {
		t.Fatalf("copy after a dying child: where=%s why=%q", cp["where"], cp["why"])
	}
	if cp["beat_at"] == "" {
		t.Fatalf("the copy was never beaten: %v", cp)
	}
	if w := client.HGet(ctx, taskcard.Key("p1"), "where").Val(); w != "waiting" {
		t.Fatalf("primary where=%s, want waiting after its copy failed", w)
	}
	if n := client.ZCard(ctx, k.Key("working")).Val(); n != 0 {
		t.Fatalf("friend:emma:cards:working holds %d after the close", n)
	}
	if kinds := strings.Join(logKinds(t, client), " "); !strings.Contains(kinds, "start") || !strings.Contains(kinds, "done") {
		t.Fatalf("friend:emma:log: %s", kinds)
	}
}

// TestHelperCopyChild is the fake Opus child of a friend:rowan copy (#3999):
// serve execs this test binary with NOVA_SERVE_COPY set. "pr" opens a PR
// (it writes the PR record pr record would, at the card's base, from its
// branch rowan/<copy>) and ends on the typed line naming it; "self" ends its
// copy itself through card end (the brief's one call) with its token.
// Without the variable it is an empty test.
func TestHelperCopyChild(t *testing.T) {
	mode := os.Getenv("NOVA_SERVE_COPY")
	if mode == "" {
		return
	}
	id := os.Getenv(life.ServeEnvCopy)
	brief, err := os.ReadFile(os.Getenv(life.ServeEnvBrief))
	if err != nil || id == "" || !strings.Contains(string(brief), id) {
		fmt.Printf("BLOCKED brief %v does not name copy %q\n", err, id)
		return
	}
	ctx := context.Background()
	c := redis.NewClient(&redis.Options{Addr: os.Getenv("NOVA_SERVE_REDIS")})
	defer c.Close()
	repo, base, pr := os.Getenv("NOVA_SERVE_REPO"), os.Getenv("NOVA_SERVE_BASE"), os.Getenv("NOVA_SERVE_PR")
	branch := os.Getenv(life.ServeEnvFriend) + "/" + id
	c.HSet(ctx, "pr:"+repo+":"+pr, "repo", repo, "n", pr, "head", copyHead, "base", base, "branch", branch, "state", "open")
	fmt.Printf("child %s on %s\n", id, branch)
	switch mode {
	case "pr":
		fmt.Printf("DONE opened pr=%s#%s head=%s\n", repo, pr, copyHead)
	case "self":
		if _, err := taskcard.End(ctx, c, taskcard.EndRequest{IDs: []string{id}, OK: true, Repo: repo, PR: pr, Head: copyHead,
			Token: os.Getenv(life.ServeEnvToken), By: os.Getenv(life.ServeEnvFriend),
			Fields: []string{"branch", branch}}); err != nil {
			fmt.Printf("BLOCKED card end: %v\n", err)
			os.Exit(5)
		}
		fmt.Printf("DONE ended %s myself\n", id)
	}
}

const (
	copyHead    = "c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0"
	copyBaseSHA = "0123456789abcdef0123456789abcdef01234567"
	copyStream  = "nova-sprint + merge + bus"
)

// TestServeRowanCopyEndsThroughCardEnd is #3999's second DONE-WHEN on
// #3998's serve: `friend serve --as rowan` takes the friend:rowan copy (card
// work), starts the seat's dispatch with the copy's card as the brief and
// the copy in its environment, and the copy ends through card end with the
// PR named on it, from the child's typed line or by the child itself. The
// copy's ok is the move: the primary goes working -> reading (no reader is
// enrolled, so its read copy waits for the deal). Both repos, the same sets.
func TestServeRowanCopyEndsThroughCardEnd(t *testing.T) {
	for _, tc := range []struct{ repo, base, mode string }{
		{"nova-tools", "dev", "pr"},
		{"rowan-tools", "main", "pr"},
		{"nova-tools", "dev", "self"},
	} {
		t.Run(tc.repo+"-"+tc.mode, func(t *testing.T) {
			ctx := context.Background()
			_, c, addr := controlRedis(t)
			k := taskcard.Consumer{Kind: "friend", Name: "rowan"}
			c.SAdd(ctx, "friends", "rowan")
			c.HSet(ctx, k.DesiredKey(), "slots", "1")
			if err := taskcard.Enroll(ctx, c, k, true); err != nil {
				t.Fatal(err)
			}
			id := "build-3999-" + tc.repo
			if _, err := taskcard.Push(ctx, c, taskcard.PushRequest{ID: id, Where: "waiting", Stream: copyStream, Kind: "build",
				Ref: tc.repo + "#3999", Origin: "issue:" + tc.repo + "#3999", Title: "the stream table",
				Repo: "mas-bandwidth/" + tc.repo, By: "import", Fields: []string{"base", tc.base, "base_sha", copyBaseSHA,
					"paths", "internal/x.go", "done_when", "go test ./internal/x passes", "who", "only rowan"}}); err != nil {
				t.Fatal(err)
			}
			d, err := taskcard.Deal(ctx, c, taskcard.DealRequest{To: k, IDs: []string{id}, By: "reconciler"})
			if err != nil || len(d) != 1 {
				t.Fatalf("deal %v %v", d, err)
			}
			cp := d[0].Copy
			cfg := life.ServeConfig{Friend: "rowan", Session: "s-rowan", Host: "studio", Harness: "fake", Width: 1,
				Dispatch: []string{os.Args[0], "-test.run=^TestHelperCopyChild$"}, Dir: t.TempDir(), Out: testWriter{t},
				Env: []string{"NOVA_SERVE_REDIS=" + addr, "NOVA_SERVE_REPO=" + tc.repo, "NOVA_SERVE_BASE=" + tc.base,
					"NOVA_SERVE_PR=7001", "NOVA_SERVE_COPY=" + tc.mode}}
			res, err := life.ServeOnce(ctx, store.New(c), cfg)
			if err != nil || res.Taken != 1 || res.Closed != 1 {
				t.Fatalf("serve once %+v %v", res, err)
			}
			got := c.HGetAll(ctx, taskcard.Key(cp)).Val()
			if got["where"] != "ok" || got["pr"] != "7001" || got["head"] != copyHead {
				t.Fatalf("copy %v", got)
			}
			if tc.mode == "pr" && (got["repo"] != tc.repo || !strings.HasPrefix(got["evidence"], "DONE opened pr=")) {
				t.Fatalf("copy end from the typed line %v", got)
			}
			p := c.HGetAll(ctx, taskcard.Key(id)).Val()
			if p["where"] != "reading" || p["copy"] != "" || p["pr"] != "7001" || p["author"] != k.String() {
				t.Fatalf("primary %v", p)
			}
			out, _ := os.ReadFile(filepath.Join(cfg.Dir, card.CopySprint, card.CopyCardLabel(cp), "out.log"))
			if !strings.Contains(string(out), "child "+cp+" on rowan/"+cp) {
				t.Fatalf("child env: %s", out)
			}
			var log []string
			for _, e := range c.XRange(ctx, life.LogKey("rowan"), "-", "+").Val() {
				log = append(log, fmt.Sprint(e.Values["kind"])+" "+fmt.Sprint(e.Values["detail"]))
			}
			want := "done copy=" + cp + " primary=" + id + " to=reading"
			if tc.mode == "self" {
				want = "done copy=" + cp + " ended by the child: ok"
			}
			if !strings.Contains(strings.Join(log, " | "), want) {
				t.Fatalf("serve log %v, want %q", log, want)
			}
			if r, err := taskcard.FsckMoves(ctx, c, false); err != nil || r.Drift != 0 {
				t.Fatalf("card fsck %+v %v", r, err)
			}
		})
	}
}
