package reconcile_test

// The land duty (nova-tools #3898): a throwaway redis-server with the
// library loaded, a local bare remote standing in for GitHub's git side, a
// fake forge for its REST side, and a CI request seam that records. No host
// is touched.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/land/stream"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/read"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/reconcile"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
)

const (
	ldRepo   = "mas-bandwidth/nova-tools"
	ldAlpha  = "landing: alpha"
	ldBeta   = "landing: beta"
	ldSprint = "land-duty"
)

func ldGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	full := append([]string{"-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "-c", "commit.gpgsign=false", "-c", "init.defaultBranch=dev"}, args...)
	cmd := exec.Command("git", full...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "LC_ALL=C")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// ldRemote is dev with a.txt and one branch per member PR, published as
// refs/pull/<n>/head in a bare repo. files maps each PR to the file it
// writes and the body; two PRs that write a.txt conflict.
func ldRemote(t *testing.T, files map[int][2]string) (url string, heads map[int]string) {
	t.Helper()
	root := t.TempDir()
	src, bare := filepath.Join(root, "src"), filepath.Join(root, "remote.git")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	ldGit(t, src, "init", "-q", "-b", "dev")
	if err := os.WriteFile(filepath.Join(src, "a.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ldGit(t, src, "add", ".")
	ldGit(t, src, "commit", "-q", "-m", "base")
	heads = map[int]string{}
	for n, f := range files {
		ldGit(t, src, "checkout", "-q", "-b", fmt.Sprintf("m%d", n), "dev")
		if err := os.WriteFile(filepath.Join(src, f[0]), []byte(f[1]+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		ldGit(t, src, "add", ".")
		ldGit(t, src, "commit", "-q", "-m", fmt.Sprintf("member %d", n))
		heads[n] = ldGit(t, src, "rev-parse", "HEAD")
		ldGit(t, src, "checkout", "-q", "dev")
	}
	ldGit(t, root, "clone", "-q", "--bare", src, bare)
	for n, h := range heads {
		ldGit(t, bare, "update-ref", fmt.Sprintf("refs/pull/%d/head", n), h)
	}
	return "file://" + bare, heads
}

// ldForge is the forge's REST side: POST pulls opens #900, #901, ...; PUT
// merge answers merged at a fixed sha; comments, closes and body reads are
// accepted.
type ldForge struct {
	mu     sync.Mutex
	opened []map[string]string
	merged []string
	srv    *httptest.Server
}

func newLDForge(t *testing.T) *ldForge {
	g := &ldForge{}
	g.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &body)
		g.mu.Lock()
		defer g.mu.Unlock()
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/repos/"+ldRepo+"/pulls":
			g.opened = append(g.opened, body)
			w.WriteHeader(http.StatusCreated)
			fmt.Fprintf(w, `{"number":%d}`, 899+len(g.opened))
		case r.Method == http.MethodPut && strings.HasSuffix(r.URL.Path, "/merge"):
			g.merged = append(g.merged, r.URL.Path)
			_, _ = w.Write([]byte(`{"merged":true,"sha":"` + strings.Repeat("c", 40) + `"}`))
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/comments"):
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":1}`))
		case r.Method == http.MethodPatch:
			_, _ = w.Write([]byte(`{}`))
		case r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`{"body":""}`))
		default:
			http.Error(w, `{"message":"unexpected"}`, http.StatusNotFound)
		}
	}))
	t.Cleanup(g.srv.Close)
	return g
}

type ciCall struct {
	repo, sha string
	pr        int
}

type ldFixture struct {
	ctx   context.Context
	c     *redis.Client
	heads map[int]string
	forge *ldForge
	mu    sync.Mutex
	ci    []ciCall
	out   bytes.Buffer
	d     *reconcile.LandDuty
}

// seedMember puts task t<n> in ws:<s>:merging at age at, with a pr record at
// its head; lines are its typed lines, stored through `pr lines` (the
// record's reads field) or, with viaReadPost, through `read post` (the
// record's lines list).
func (fx *ldFixture) seedMember(t *testing.T, s string, n int, at float64, owner string, viaReadPost bool, lines ...string) {
	t.Helper()
	ctx, c := fx.ctx, fx.c
	id := fmt.Sprintf("t%d", n)
	c.ZAdd(ctx, "ws:"+s+":merging", redis.Z{Score: at, Member: id})
	c.HSet(ctx, "task:"+id, "stream", s, "state", "merging", "where", "merging", "pr", fmt.Sprintf("%s#%d", ldRepo, n),
		"created_at", fmt.Sprint(int64(at)), "owner", owner)
	if _, err := stream.Record(ctx, c, ldRepo, n, stream.RecordFields{Head: fx.heads[n], Base: "dev", Stream: s, Task: id}); err != nil {
		t.Fatal(err)
	}
	for _, l := range lines {
		if viaReadPost {
			var so, se bytes.Buffer
			if code := read.Post(ctx, c, ldRepo, fmt.Sprint(n), l, nil, &so, &se); code != 0 {
				t.Fatalf("read post #%d: %d %s", n, code, se.String())
			}
			continue
		}
		if _, err := stream.AddLine(ctx, c, ldRepo, n, l); err != nil {
			t.Fatal(err)
		}
	}
}

func ldScore(who, head string) string {
	return fmt.Sprintf("SCORE who=%s head=%s score=9/10 gates=ci:ok,base:ok,scope:ok", who, head)
}

// newLDFixture is stream alpha with five read members (#1-#5, their reads in
// the record's reads field) and one unread (#6), and stream beta with four
// read members (#11-#13 and #15, their reads posted by `read post` into the
// lines list) and one unread (#14, read at an old head); #13 and #15 both
// rewrite a.txt, so #15 (the younger) conflicts. emma built #15.
func newLDFixture(t *testing.T) *ldFixture {
	t.Helper()
	fx := &ldFixture{ctx: context.Background()}
	fx.c = redis.NewClient(&redis.Options{Addr: testutil.Start(t)})
	t.Cleanup(func() { _ = fx.c.Close() })
	if err := fn.Load(fx.ctx, fx.c); err != nil {
		t.Fatal(err)
	}
	files := map[int][2]string{13: {"a.txt", "thirteen"}, 15: {"a.txt", "fifteen"}}
	for _, n := range []int{1, 2, 3, 4, 5, 6, 11, 12, 14} {
		files[n] = [2]string{fmt.Sprintf("f%d.txt", n), fmt.Sprint(n)}
	}
	var url string
	url, fx.heads = ldRemote(t, files)
	ctx, c := fx.ctx, fx.c
	for i, s := range []string{ldAlpha, ldBeta} {
		c.ZAdd(ctx, "ws:order", redis.Z{Score: float64(i + 1), Member: s})
		c.SAdd(ctx, "ws:names", s)
	}
	for n := 1; n <= 5; n++ {
		fx.seedMember(t, ldAlpha, n, float64(1000+n), "rowan", false, ldScore("emma", fx.heads[n]))
	}
	fx.seedMember(t, ldAlpha, 6, 1006, "rowan", false)
	for _, n := range []int{11, 12, 13, 15} {
		fx.seedMember(t, ldBeta, n, float64(2000+n), "emma", true, ldScore("stella", fx.heads[n]))
	}
	fx.seedMember(t, ldBeta, 14, 2014, "emma", true, ldScore("stella", strings.Repeat("d", 40)))
	c.HSet(ctx, "cfg:land", "remote:"+ldRepo, url, "min_score", "8")
	c.Set(ctx, "cfg:land:test:"+ldRepo, "true", 0)
	// The sprint the rebase task goes to, and its author a live friend.
	c.HSet(ctx, "s:"+ldSprint, "status", "open")
	c.ZAdd(ctx, "sprint:order", redis.Z{Score: 1, Member: ldSprint})
	for _, f := range []string{"emma", "rowan", "stella"} {
		c.SAdd(ctx, "friends", f)
		c.HSet(ctx, "friend:"+f+":desired", "slots", 4, "paused", "0")
		c.HSet(ctx, "friend:"+f+":beat", "host", "fixture")
	}
	fx.forge = newLDForge(t)
	fx.d = &reconcile.LandDuty{
		Client: fx.c, Repos: []string{ldRepo}, Workroot: t.TempDir(), Out: &fx.out, Host: "fixture",
		GitHub: func() (*stream.GitHub, error) { return &stream.GitHub{API: fx.forge.srv.URL, Token: "test"}, nil },
		Request: func(_ context.Context, repo, sha string, pr int, _ string) (string, error) {
			fx.mu.Lock()
			defer fx.mu.Unlock()
			fx.ci = append(fx.ci, ciCall{repo, sha, pr})
			return "CREATED", nil
		},
	}
	return fx
}

func lineFor(t *testing.T, p reconcile.LandPass, s string) reconcile.LandLine {
	t.Helper()
	for _, l := range p.Lines {
		if l.Stream == s {
			return l
		}
	}
	t.Fatalf("no line for %q in %+v", s, p.Lines)
	return reconcile.LandLine{}
}

// TestLandDutyLandsEveryReadStreamInOneBatch is the DONE-WHEN control: two
// streams, 5 and 3 read members plus 2 unread; one duty pass opens exactly
// two stream PRs (5 and 3 members), requests CI for each head, and the
// receipt names the 2 unread; the member that conflicts is parked with a
// rebase task on its author's queue and its stream PR still opens with the
// rest. Beta's reads exist only in the lines list read post writes.
func TestLandDutyLandsEveryReadStreamInOneBatch(t *testing.T) {
	fx := newLDFixture(t)
	ctx, c := fx.ctx, fx.c
	p, err := fx.d.Pass(ctx, "fixture-1", ldRepo)
	if err != nil {
		t.Fatalf("pass: %v\n%+v", err, p)
	}
	if p.Status != "TAKEN" || len(p.Lines) != 2 {
		t.Fatalf("pass: status=%s lines=%+v", p.Status, p.Lines)
	}
	a, b := lineFor(t, p, ldAlpha), lineFor(t, p, ldBeta)
	if a.State != "waiting" || fmt.Sprint(a.Members) != "[1 2 3 4 5]" || fmt.Sprint(a.Unread) != "[6]" || a.PR != 900 {
		t.Errorf("alpha: %s", a)
	}
	if b.State != "waiting" || fmt.Sprint(b.Members) != "[11 12 13]" || fmt.Sprint(b.Unread) != "[14]" || b.PR != 901 {
		t.Errorf("beta: %s", b)
	}
	if len(fx.forge.opened) != 2 {
		t.Fatalf("stream PRs opened: %d, want 2", len(fx.forge.opened))
	}
	// CI requested once per stream PR, at the landing's head.
	if len(fx.ci) != 2 {
		t.Fatalf("ci requests: %+v", fx.ci)
	}
	for _, s := range []string{ldAlpha, ldBeta} {
		slug, _ := stream.Slug(s)
		l, ok, err := stream.LoadLanding(ctx, c, ldRepo, slug)
		if err != nil || !ok || l.State != "open" {
			t.Fatalf("landing %s: %+v %v %v", slug, l, ok, err)
		}
		found := false
		for _, r := range fx.ci {
			found = found || (r.sha == l.Head && r.pr == l.PR && r.repo == ldRepo)
		}
		if !found {
			t.Errorf("no ci request for %s at %s: %+v", slug, l.Head, fx.ci)
		}
	}
	// The receipt names both unread members.
	out := fx.out.String() + a.String() + "\n" + b.String()
	for _, want := range []string{"unread=#6", "unread=#14", "parked=#15:conflict:a.txt"} {
		if !strings.Contains(out, want) {
			t.Errorf("receipt lacks %q:\n%s", want, out)
		}
	}
	// #15 is parked back to working, its record parked, and emma has a
	// rebase task naming the stream branch, its head and the file.
	if len(b.Parked) != 1 || b.Parked[0].N != 15 || len(b.Rebase) != 1 {
		t.Fatalf("beta parked/rebase: %s", b)
	}
	if st, _ := c.HGet(ctx, stream.PRKey(ldRepo, 15), "state").Result(); st != "parked" {
		t.Errorf("#15 record state=%q, want parked", st)
	}
	if _, err := c.ZScore(ctx, "ws:"+ldBeta+":working", "t15").Result(); err != nil {
		t.Errorf("t15 not in working: %v", err)
	}
	if _, err := c.ZScore(ctx, "ws:"+ldBeta+":merging", "t15").Result(); err == nil {
		t.Errorf("t15 still in merging")
	}
	id := b.Rebase[0]
	if id != "rebase-15-"+fx.heads[15][:8] {
		t.Errorf("rebase id %q", id)
	}
	tk, _ := c.HGetAll(ctx, "s:"+ldSprint+":task:"+id).Result()
	l, _, _ := stream.LoadLanding(ctx, c, ldRepo, "landing-beta")
	title := tk["title"]
	if tk["kind"] != "rebase" || !strings.Contains(title, "stream/landing-beta at "+l.Head[:8]) || !strings.Contains(title, "a.txt") {
		t.Errorf("rebase task: %v", tk)
	}
	if _, err := c.ZScore(ctx, "s:"+ldSprint+":open:emma", id).Result(); err != nil || tk["dest"] != "emma" {
		t.Errorf("rebase task is not on emma's queue: %v %v", err, tk)
	}
	// One pass per tick: a second pass now waits and opens nothing.
	p2, err := fx.d.Pass(ctx, "fixture-1", ldRepo)
	if err != nil || p2.Status != "WAIT" || len(fx.forge.opened) != 2 {
		t.Fatalf("second pass: %+v %v opened=%d", p2, err, len(fx.forge.opened))
	}
	if n, _ := c.HGet(ctx, "proc:land:"+ldRepo, "opened").Result(); n != "2" {
		t.Errorf("proc:land opened=%q", n)
	}
}

// TestLandDutyOneWorkerPerRepoAndNextTickMerges: a held lease:land:<repo>
// keeps a second worker out; on the next tick the open landings resume at
// their CI wait and, once CI is green at the stream head, merge and move
// their members to landed.
func TestLandDutyOneWorkerPerRepoAndNextTickMerges(t *testing.T) {
	fx := newLDFixture(t)
	ctx, c := fx.ctx, fx.c
	if _, err := fx.d.Pass(ctx, "fixture-1", ldRepo); err != nil {
		t.Fatal(err)
	}
	c.HSet(ctx, "proc:land:"+ldRepo, "due_at", "0")
	c.HSet(ctx, "lease:land:"+ldRepo, "instance", "other", "token", "x")
	p, err := fx.d.Pass(ctx, "fixture-1", ldRepo)
	if err != nil || p.Status != "HELD" || p.Holder != "other" {
		t.Fatalf("held: %+v %v", p, err)
	}
	c.Del(ctx, "lease:land:"+ldRepo)
	l, _, _ := stream.LoadLanding(ctx, c, ldRepo, "landing-alpha")
	if _, err := stream.Record(ctx, c, ldRepo, l.PR, stream.RecordFields{CI: "green"}); err != nil {
		t.Fatal(err)
	}
	if _, err := fx.d.Run(ctx, nil); err != nil {
		t.Fatal(err)
	}
	fx.d.Wait()
	if !strings.Contains(fx.out.String(), "LAND-DUTY repo="+ldRepo+" stream=\"landing: alpha\" state=landed pr=#900 members=#1,#2,#3,#4,#5") {
		t.Fatalf("alpha did not land:\n%s", fx.out.String())
	}
	if !strings.Contains(fx.out.String(), "stream=\"landing: beta\" state=waiting pr=#901") {
		t.Errorf("beta not left waiting:\n%s", fx.out.String())
	}
	if len(fx.forge.opened) != 2 || len(fx.forge.merged) != 1 {
		t.Errorf("opened=%d merged=%d", len(fx.forge.opened), len(fx.forge.merged))
	}
	if n, _ := c.ZCard(ctx, "ws:"+ldAlpha+":merging").Result(); n != 1 {
		t.Errorf("alpha merging holds %d, want only the unread #6", n)
	}
	if held := fx.d.Stop(ctx); len(held) != 0 {
		t.Errorf("released after stop: %v", held)
	}
	if n, _ := c.Exists(ctx, "lease:land:"+ldRepo).Result(); n != 0 {
		t.Errorf("lease left held")
	}
}
