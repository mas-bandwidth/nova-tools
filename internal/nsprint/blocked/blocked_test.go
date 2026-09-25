package blocked_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/blocked"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws/wstest"
)

// task writes one ws-shaped task: the hash and the one set its state names,
// scored by created_at.
func task(t *testing.T, c *redis.Client, stream, id, state string, created int64, fields ...any) {
	t.Helper()
	ctx := context.Background()
	pipe := c.Pipeline()
	pipe.SAdd(ctx, "ws:names", stream)
	pipe.ZAddNX(ctx, "ws:order", redis.Z{Score: 1, Member: stream})
	args := append([]any{"stream", stream, "state", state, "created_at", fmt.Sprint(created)}, fields...)
	pipe.HSet(ctx, "task:"+id, args...)
	if state != ws.Closed {
		pipe.ZAdd(ctx, ws.Key(stream, state), redis.Z{Score: float64(created), Member: id})
	}
	if _, err := pipe.Exec(ctx); err != nil {
		t.Fatal(err)
	}
}

func zrange(t *testing.T, c *redis.Client, key string) []string {
	t.Helper()
	zs, err := c.ZRangeWithScores(context.Background(), key, 0, -1).Result()
	if err != nil {
		t.Fatal(err)
	}
	out := make([]string, len(zs))
	for i, z := range zs {
		out[i] = fmt.Sprintf("%v@%.0f", z.Member, z.Score)
	}
	return out
}

// logTail is the last n ws:log entries, oldest first, as "id from->to why".
func logTail(t *testing.T, c *redis.Client, n int64) []string {
	t.Helper()
	xs, err := c.XRevRangeN(context.Background(), "ws:log", "+", "-", n).Result()
	if err != nil {
		t.Fatal(err)
	}
	out := make([]string, len(xs))
	for i, x := range xs {
		out[len(xs)-1-i] = fmt.Sprintf("%v %v->%v %v", x.Values["id"], x.Values["from"], x.Values["to"], x.Values["why"])
	}
	return out
}

// mirror makes <root>/<repo>.git with a dev branch in t.TempDir(): a local
// repository, no remote.
type mirror struct {
	t    *testing.T
	root string
	dir  string
}

func newMirror(t *testing.T, repo string) *mirror {
	t.Helper()
	root := t.TempDir()
	m := &mirror{t: t, root: root, dir: filepath.Join(root, repo+".git")}
	m.run("init", "-q", "--initial-branch=dev", m.dir)
	m.commit("base")
	return m
}

func (m *mirror) run(args ...string) string {
	m.t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t",
		"GIT_COMMITTER_EMAIL=t@t", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		m.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// commit adds an empty commit with message msg on the checked-out branch
// and returns its sha.
func (m *mirror) commit(msg string) string {
	m.t.Helper()
	m.run("-C", m.dir, "commit", "-q", "--allow-empty", "-m", msg)
	return m.run("-C", m.dir, "rev-parse", "HEAD")
}

func resolver(c *redis.Client, m *mirror) *blocked.Resolver {
	r := &blocked.Resolver{Client: c, By: "blocked-resolve"}
	if m != nil {
		r.Git = blocked.Mirror{Root: m.root}
	}
	return r
}

func pass(t *testing.T, r *blocked.Resolver) blocked.Result {
	t.Helper()
	res, err := r.Pass(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Refused) > 0 || len(res.Unknown) > 0 {
		t.Fatalf("refused %v unknown %v", res.Refused, res.Unknown)
	}
	return res
}

// TestResolveWalksOldestFirstAndReleasesInPosition: c3, c1 and c2 (created
// 300, 100, 200, blocked in that order) wait on nova-tools#11; the lander's
// landed move marks pr:nova-tools:11 landed. One pass releases c1, then c2,
// then c3 through the move primitive (ws:log in that order), and ready holds
// them at their created_at scores 100, 200, 300, never "now". A second pass
// moves nothing.
func TestResolveWalksOldestFirstAndReleasesInPosition(t *testing.T) {
	_, c := wstest.Start(t)
	ctx := context.Background()
	for _, x := range []struct {
		id string
		at int64
	}{{"c3", 300}, {"c1", 100}, {"c2", 200}} {
		task(t, c, "s1", x.id, "waiting", x.at, "blocked_on", "mas-bandwidth/nova-tools#11", "owner", "stella")
	}
	r := resolver(c, nil)
	if res := pass(t, r); len(res.Released) != 0 || res.Waiting != 3 {
		t.Fatalf("before landing: %+v", res)
	}
	c.HSet(ctx, "pr:nova-tools:11", "state", "landed", "head", "0123456789abcdef")
	res := pass(t, r)
	var got []string
	for _, x := range res.Released {
		got = append(got, x.ID+" "+x.Parent+" "+x.SHA)
	}
	want := []string{"c1 mas-bandwidth/nova-tools#11 01234567", "c2 mas-bandwidth/nova-tools#11 01234567", "c3 mas-bandwidth/nova-tools#11 01234567"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("released %v, want %v", got, want)
	}
	if got := zrange(t, c, ws.Key("s1", "ready")); !reflect.DeepEqual(got, []string{"c1@100", "c2@200", "c3@300"}) {
		t.Fatalf("ready %v", got)
	}
	if got := zrange(t, c, ws.Key("s1", "waiting")); len(got) != 0 {
		t.Fatalf("waiting %v", got)
	}
	why := "depends-on landed: mas-bandwidth/nova-tools#11 at 01234567"
	if got := logTail(t, c, 3); !reflect.DeepEqual(got, []string{"c1 waiting->ready " + why, "c2 waiting->ready " + why, "c3 waiting->ready " + why}) {
		t.Fatalf("ws:log %v", got)
	}
	if err := ws.Check(ctx, c, []string{"c1", "c2", "c3"}); err != nil {
		t.Fatal(err)
	}
	if res := pass(t, r); len(res.Released)+len(res.Parked) != 0 {
		t.Fatalf("second pass moved %+v", res)
	}
}

// TestResolveParksOnClosedParent: a parent PR closed and not on the base,
// and a parent task cancelled, park their children no-parent:<cond>; they
// are never left waiting.
func TestResolveParksOnClosedParent(t *testing.T) {
	_, c := wstest.Start(t)
	ctx := context.Background()
	m := newMirror(t, "nova-tools")
	m.commit("part of #12, not a landing")
	c.HSet(ctx, "pr:nova-tools:12", "state", "closed", "head", "feedfacefeedface", "base", "dev")
	task(t, c, "s1", "c1", "waiting", 100, "blocked_on", "nova-tools#12")
	task(t, c, "s1", "p9", ws.Closed, 50, "cancelled", "1")
	task(t, c, "s1", "c2", "waiting", 200, "blocked_on", "task:p9")
	res := pass(t, resolver(c, m))
	if len(res.Parked) != 2 || res.Parked[0] != (blocked.Park{Stream: "s1", ID: "c1", Parent: "nova-tools#12"}) ||
		res.Parked[1] != (blocked.Park{Stream: "s1", ID: "c2", Parent: "task:p9"}) {
		t.Fatalf("parked %+v", res.Parked)
	}
	if got := zrange(t, c, ws.Key("s1", "parked")); !reflect.DeepEqual(got, []string{"c1@100", "c2@200"}) {
		t.Fatalf("parked set %v", got)
	}
	if got := logTail(t, c, 2); !reflect.DeepEqual(got, []string{"c1 waiting->parked no-parent:nova-tools#12", "c2 waiting->parked no-parent:task:p9"}) {
		t.Fatalf("ws:log %v", got)
	}
	if err := ws.Check(ctx, c, []string{"c1", "c2", "p9"}); err != nil {
		t.Fatal(err)
	}
}

// TestResolveReadsTheBaseAndLeavesTheRestWaiting: a parent Redis does not
// show landed is read from the mirror's base (a landing commit, or the PR
// head an ancestor); an open parent, a second unmet parent, a condition
// that is not this resolver's, and a waiting task with no DEPENDS-ON all
// stay waiting; when the base tip moves the cached miss is read again.
func TestResolveReadsTheBaseAndLeavesTheRestWaiting(t *testing.T) {
	_, c := wstest.Start(t)
	ctx := context.Background()
	m := newMirror(t, "nova-tools")
	head := m.commit("feature for 14")
	c.HSet(ctx, "pr:nova-tools:14", "state", "open", "head", head, "base", "dev")
	task(t, c, "s1", "a", "waiting", 100, "blocked_on", "nova-tools#13")
	task(t, c, "s1", "b", "waiting", 200, "blocked_on", "nova-tools#14")
	task(t, c, "s1", "d", "waiting", 300, "blocked_on", "nova-tools#14;nova-tools#15")
	task(t, c, "s2", "e", "waiting", 400, "blocked_on", "stream/other")
	task(t, c, "s2", "f", "waiting", 500, "blocked_reason", "sweep: pitstop")
	task(t, c, "s2", "g", "waiting", 600, "blocked_on", "task:p1")
	task(t, c, "s2", "p1", "landed", 10, "head", "abcdef0123")
	r := resolver(c, m)
	res := pass(t, r)
	if len(res.Released) != 2 || res.Released[0].ID != "b" || res.Released[0].SHA != head[:8] ||
		res.Released[1].ID != "g" || res.Released[1].SHA != "abcdef01" || res.Waiting != 4 {
		t.Fatalf("first pass %+v", res)
	}
	merge := m.commit("Merge nova-tools#13 into dev")
	res = pass(t, r)
	if len(res.Released) != 1 || res.Released[0].ID != "a" || res.Released[0].SHA != merge[:8] || res.Waiting != 3 {
		t.Fatalf("after the base moved %+v", res)
	}
	m.commit("feature 15 (#15)")
	res = pass(t, r)
	if len(res.Released) != 1 || res.Released[0].ID != "d" || res.Waiting != 2 {
		t.Fatalf("after #15 %+v", res)
	}
	if got := zrange(t, c, ws.Key("s2", "waiting")); !reflect.DeepEqual(got, []string{"e@400", "f@500"}) {
		t.Fatalf("s2 waiting %v", got)
	}
}

// TestResolveWithoutMirrorWaits: no mirror for the repo is no evidence, never
// a release or a park; the pass says which parent it could not read.
func TestResolveWithoutMirrorWaits(t *testing.T) {
	_, c := wstest.Start(t)
	c.HSet(context.Background(), "pr:rowan-tools:7", "state", "closed")
	task(t, c, "s1", "a", "waiting", 100, "blocked_on", "rowan-tools#7")
	r := &blocked.Resolver{Client: c, By: "t", Git: blocked.Mirror{Root: t.TempDir()}}
	res, err := r.Pass(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Released)+len(res.Parked) != 0 || res.Waiting != 1 || len(res.Unknown) != 1 {
		t.Fatalf("%+v", res)
	}
}

func TestMirrorLandingPattern(t *testing.T) {
	m := newMirror(t, "nova-tools")
	g := blocked.Mirror{Root: m.root}
	ctx := context.Background()
	shas := map[int]string{}
	for n, msg := range map[int]string{
		21: "stage from card header (#21)",
		23: "nova-sprint task batch verbs\n\nCloses mas-bandwidth/nova-tools#23",
		25: "Merge #25 stream/quack: two probe cards",
		26: "Merge pull request #26 from x/y",
	} {
		shas[n] = m.commit(msg)
	}
	m.commit("part of #22; amends #27; Closes rowan-tools#24; see #210")
	for _, tc := range []struct {
		n    int
		want string
	}{{21, shas[21]}, {23, shas[23]}, {25, shas[25]}, {26, shas[26]}, {22, ""}, {24, ""}, {27, ""}, {2, ""}} {
		got, err := g.Landed(ctx, "nova-tools", "dev", tc.n, "")
		if err != nil || got != tc.want {
			t.Errorf("#%d: %q %v, want %q", tc.n, got, err, tc.want)
		}
	}
	base, tip, err := g.Tip(ctx, "nova-tools", "")
	if err != nil || base != "dev" || tip == "" {
		t.Fatalf("tip %q %q %v", base, tip, err)
	}
	if _, _, err := g.Tip(ctx, "nova-tools", "main"); err == nil {
		t.Fatal("no main branch, want an error")
	}
	if _, _, err := g.Tip(ctx, "../x", ""); err == nil {
		t.Fatal("a path is not a repo name")
	}
}

func TestParse(t *testing.T) {
	conds, err := blocked.Parse("mas-bandwidth/nova-tools#11; task:t1,card:c2 ,stream/s1, nova-tools#11")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, c := range conds {
		got = append(got, fmt.Sprintf("%s:%s%s%d", c.Kind, c.ID, c.Repo, c.N))
	}
	want := []string{"pr:nova-tools11", "task:t10", "task:c20", "other:0", "pr:nova-tools11"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%v, want %v", got, want)
	}
	for _, none := range []string{"", "none", " "} {
		if c, err := blocked.Parse(none); err != nil || c != nil {
			t.Fatalf("%q: %v %v", none, c, err)
		}
	}
	for _, reason := range []string{"spec not ready", "hold: stella"} {
		if _, err := blocked.Parse(reason); err == nil {
			t.Fatalf("%q parsed as a condition", reason)
		}
	}
}

// TestListOldestFirstPerStream: streams in ws:order rank, each oldest first,
// with blocked_on.
func TestListOldestFirstPerStream(t *testing.T) {
	_, c := wstest.Start(t)
	ctx := context.Background()
	task(t, c, "b", "b2", "waiting", 20, "blocked_on", "nova-tools#1")
	task(t, c, "b", "b1", "waiting", 10)
	task(t, c, "a", "a1", "waiting", 30, "blocked_on", "task:x")
	task(t, c, "a", "a0", "ready", 5)
	c.ZAdd(ctx, "ws:order", redis.Z{Score: 1, Member: "a"}, redis.Z{Score: 2, Member: "b"})
	rows, err := blocked.List(ctx, c, "")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, r := range rows {
		got = append(got, fmt.Sprintf("%s/%s@%d:%s", r.Stream, r.ID, r.Created, r.On))
	}
	want := []string{"a/a1@30:task:x", "b/b1@10:", "b/b2@20:nova-tools#1"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%v, want %v", got, want)
	}
	if rows, _ := blocked.List(ctx, c, "b"); len(rows) != 2 {
		t.Fatalf("one stream: %v", rows)
	}
}

// TestResolveAThousandWaiting: 1,000 waiting tasks over 10 streams on one
// landed PR resolve in one pass with no command a second on the server
// (SLOWLOG armed at one second), and the ws invariants hold.
func TestResolveAThousandWaiting(t *testing.T) {
	_, c := wstest.Start(t)
	ctx := context.Background()
	pipe := c.Pipeline()
	var ids []string
	for i := 0; i < 1000; i++ {
		id, s := fmt.Sprintf("w%04d", i), wstest.StreamName(i%10)
		ids = append(ids, id)
		pipe.SAdd(ctx, "ws:names", s)
		pipe.ZAdd(ctx, "ws:order", redis.Z{Score: float64(i%10 + 1), Member: s})
		pipe.HSet(ctx, "task:"+id, "stream", s, "state", "waiting", "created_at", fmt.Sprint(1000+i), "blocked_on", "nova-tools#99")
		pipe.ZAdd(ctx, ws.Key(s, "waiting"), redis.Z{Score: float64(1000 + i), Member: id})
	}
	pipe.HSet(ctx, "pr:nova-tools:99", "state", "merged")
	if _, err := pipe.Exec(ctx); err != nil {
		t.Fatal(err)
	}
	c.ConfigSet(ctx, "slowlog-log-slower-than", "1000000")
	c.SlowLogReset(ctx)
	res := pass(t, resolver(c, nil))
	if len(res.Released) != 1000 || res.Waiting != 0 {
		t.Fatalf("released %d waiting %d", len(res.Released), res.Waiting)
	}
	if err := ws.Check(ctx, c, ids); err != nil {
		t.Fatal(err)
	}
	logs, _ := c.SlowLogGet(ctx, 16).Result()
	for _, l := range logs {
		t.Errorf("over one second on the server: %v took %v", l.Args, l.Duration)
	}
}
