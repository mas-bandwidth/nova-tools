//go:build functional

package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws/wstest"
	"github.com/redis/go-redis/v9"
)

// The #4322 fix round 3 probes (the cold read of 9aad08b5f, 7/10): the
// DEPENDS-ON cycle check runs inside the FCALL that writes, on the live edge
// set at that instant (cm_order_cycle in fn/lua/02_card_move.lua), so no
// separate read precedes the write; only a cycle through the pushed card
// refuses it; and a seat without the reorder grant is refused before it
// writes. Every verb takes the store on its flags or an injected client: no
// environment, so each test runs beside every other.

// cycleCard writes a card file onto stream s and returns its path.
func cycleCard(t *testing.T, dir, s, name, origin, depends string) string {
	t.Helper()
	path := filepath.Join(dir, name+".md")
	body := "LABEL: " + name + "\nREPO: mas-bandwidth/nova-tools\nBASE: dev\nbase-sha: " + strings.Repeat("ab", 20) +
		"\nPATHS: internal/" + name + "\nDEPENDS-ON: " + depends + "\nDONE-WHEN: go test passes\nSTREAM: " + s +
		"\nORIGIN: mas-bandwidth/nova-tools#" + origin + "\n\nwhat and why\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// cutFile writes a card cut --from file of rows (id, depends-on) onto s.
func cutFile(t *testing.T, dir, name, s string, rows ...[2]string) string {
	t.Helper()
	var b strings.Builder
	b.WriteString("id\ttitle\tstream\twho\tpaths\tdone-when\tbody\tdepends-on\troute\test\n")
	for _, r := range rows {
		fmt.Fprintf(&b, "%s\tcard %s\t%s\tany\tinternal/%s.go\tgo test passes\twhy\t%s\tfriend\t30\n", r[0], r[0], s, r[0], r[1])
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestOrderSeededCycleTakesUnrelatedPushes is probe (a) and item (1): a
// stream that already holds a DEPENDS-ON cycle (seeded by hand: x on y and
// z, y on x) takes every push the cycle does not run through, exit 0 and
// written, through each door (task push --actor, card push, card cut
// --from); a push that joins the cycle is refused naming it, and writes
// nothing (keys, ws:log and every ZSET unchanged). card cut --parent's bind
// (a DEPENDS-ON edit of the parent) closing a cycle is refused the same way.
func TestOrderSeededCycleTakesUnrelatedPushes(t *testing.T) {
	t.Parallel()
	start := time.Now()
	addr, c := wstest.Start(t)
	ctx := context.Background()
	run := func(args ...string) (int, string, string) { // verb subverb --redis <addr> flags... files...
		return runSprint(append([]string{args[0], args[1], "--redis", addr}, args[2:]...)...)
	}
	const s = "order: seeded"
	push := func(id, on string) (int, string, string) {
		args := []string{"task", "push", "--actor", "rowan", "--id", id, "--stream", s, "--kind", "build",
			"--title", id, "--ref", "mas-bandwidth/nova-tools#1" + fmt.Sprint(len(id))}
		if on != "" {
			args = append(args, "--on", on)
		}
		return run(args...)
	}
	for _, id := range []string{"x", "y"} {
		if code, out, errOut := push(id, ""); code != 0 {
			t.Fatalf("push %s: %d %q %q", id, code, out, errOut)
		}
	}
	// the seeded cycle: a hand edit, as older data or a race before this check left one
	c.HSet(ctx, "task:x", "blocked_on", "y,z,k")
	c.HSet(ctx, "task:y", "blocked_on", "x")
	if so, _ := ws.ReadOrder(ctx, c, s); so.Err == nil {
		t.Fatalf("the seed holds no cycle")
	}

	// task push --actor: u on x is outside the cycle; z on x joins it.
	code, out, errOut := push("u", "x")
	if code != 0 || c.HGet(ctx, "task:u", "where").Val() != "waiting" ||
		!strings.Contains(out, "ORDER CYCLE stream=\"order: seeded\" DEPENDS-ON cycle x -> y -> x\nTASK push id=u from=- to=waiting order=cycle ") {
		t.Fatalf("unrelated push onto a stream holding a cycle: %d %q %q", code, out, errOut)
	}
	t.Logf("task push, unrelated: exit %d, %s", code, strings.ReplaceAll(strings.TrimSpace(out), "\n", " | "))
	before := storeSnapshot(t, c)
	code, out, _ = push("z", "x")
	if code != 1 || !regexp.MustCompile(`^TASK push REFUSED id=z why="ORDER CYCLE stream=\\"order: seeded\\" DEPENDS-ON cycle z -> x -> z" ms=\d+\n$`).MatchString(out) {
		t.Fatalf("push joining the cycle: %d %q", code, out)
	}
	if after := storeSnapshot(t, c); after != before {
		t.Fatalf("the refused push wrote:\n%s---\n%s", before, after)
	}
	t.Logf("task push, joining: exit %d, %s", code, strings.TrimSpace(out))

	// card push: c1 on c2 and c2 on c1 and c3 (a hand edit); c4 on c1 is
	// outside the cycle, c3 on c1 joins it.
	dir := t.TempDir()
	for i, name := range []string{"c1", "c2"} {
		if code, out, errOut := run("card", "push", "--sprint", "sd", cycleCard(t, dir, s, name, fmt.Sprint(20+i), "none")); code != 0 {
			t.Fatalf("card push %s: %d %q %q", name, code, out, errOut)
		}
	}
	c.HSet(ctx, "s:sd:card:c1", "depends_on", "c2")
	c.HSet(ctx, "s:sd:card:c2", "depends_on", "c1,c3")
	code, out, errOut = run("card", "push", "--sprint", "sd", cycleCard(t, dir, s, "c4", "24", "c1"))
	if code != 0 || !c.HExists(ctx, "s:sd:card:c4", "where").Val() {
		t.Fatalf("unrelated card push: %d %q %q", code, out, errOut)
	}
	t.Logf("card push, unrelated: exit %d, %s", code, strings.ReplaceAll(strings.TrimSpace(out), "\n", " | "))
	before = storeSnapshot(t, c)
	code, out, errOut = run("card", "push", "--sprint", "sd", cycleCard(t, dir, s, "c3", "23", "c1"))
	if code == 0 || !strings.Contains(errOut, `ns_card_push: ORDER CYCLE stream="order: seeded" DEPENDS-ON cycle s:sd:card:c3 -> s:sd:card:c1 -> s:sd:card:c2 -> s:sd:card:c3`) {
		t.Fatalf("card push joining the cycle: %d %q %q", code, out, errOut)
	}
	if after := storeSnapshot(t, c); after != before {
		t.Fatalf("the refused card push wrote:\n%s---\n%s", before, after)
	}
	t.Logf("card push, joining: exit %d, %s", code, strings.TrimSpace(errOut))

	// card cut --from (--no-github): m on x is outside the cycle, k on x
	// joins it (x names k).
	code, out, errOut = run("card", "cut", "--from", cutFile(t, dir, "m.tsv", s, [2]string{"m", "task:x"}), "--no-github", "--repo", "mas-bandwidth/nova-tools", "--actor", "rowan")
	if code != 0 || !strings.Contains(out, "CARD CUT row=1 id=m ref=- stream=\"order: seeded\" to=waiting depends=task:x\n") {
		t.Fatalf("unrelated cut: %d %q %q", code, out, errOut)
	}
	t.Logf("card cut --from, unrelated: exit %d, %s", code, strings.ReplaceAll(strings.TrimSpace(out), "\n", " | "))
	before = storeSnapshot(t, c)
	code, out, errOut = run("card", "cut", "--from", cutFile(t, dir, "k.tsv", s, [2]string{"k", "task:x"}), "--no-github", "--repo", "mas-bandwidth/nova-tools", "--actor", "rowan")
	if code != 1 || !strings.Contains(out, `push refused: ORDER CYCLE stream=\"order: seeded\" DEPENDS-ON cycle k -> x -> k`) {
		t.Fatalf("cut joining the cycle: %d %q %q", code, out, errOut)
	}
	if after := storeSnapshot(t, c); after != before {
		t.Fatalf("the refused cut wrote:\n%s---\n%s", before, after)
	}
	t.Logf("card cut --from, joining: exit %d, %s", code, strings.ReplaceAll(strings.TrimSpace(out), "\n", " | "))

	// card cut --parent: a child on its own parent; the children and the
	// stitch are cut, the bind (the parent on the stitch) would close
	// p -> p-stitch -> pc -> p and is refused, the parent unchanged.
	if _, err := taskcard.Push(ctx, c, taskcard.PushRequest{ID: "p", Where: "waiting", Stream: s, Kind: "build",
		Ref: "mas-bandwidth/nova-tools#4322", Title: "the parent", Repo: "mas-bandwidth/nova-tools", By: "rowan",
		Fields: []string{"base", "dev", "base_sha", cutFromSHA, "paths", "internal/p", "done_when", "the plan holds"}}); err != nil {
		t.Fatal(err)
	}
	parent := c.HGetAll(ctx, "task:p").Val()
	tsv := filepath.Join(dir, "children.tsv")
	if err := os.WriteFile(tsv, []byte("id\ttitle\tpaths\tdone-when\tdepends-on\troute\npc\tthe child\tinternal/pc.go\tgo test passes\ttask:p\tfriend\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, errOut = run("card", "cut", "--parent", "p", "--from", tsv, "--no-github", "--repo", "mas-bandwidth/nova-tools", "--actor", "rowan")
	if code != 1 || !strings.Contains(out, `CARD CUT REFUSED parent=p why="bind: ORDER CYCLE stream=\"order: seeded\" DEPENDS-ON cycle p -> p-stitch -> pc -> p"`) {
		t.Fatalf("cut --parent closing a cycle through the parent: %d %q %q", code, out, errOut)
	}
	if got := c.HGetAll(ctx, "task:p").Val(); fmt.Sprint(got) != fmt.Sprint(parent) {
		t.Fatalf("the refused bind wrote the parent:\n%v\n%v", parent, got)
	}
	t.Logf("card cut --parent, bind refused: exit %d, %s", code, strings.ReplaceAll(strings.TrimSpace(out), "\n", " | "))
	t.Logf("wall %s", time.Since(start).Round(time.Millisecond))
}

// TestOrderBarrierPairsStoreNoCycle is probe (b) and item (2): 10 pairs of
// mutually dependent pushes through the door (task push --actor, x_i on y_i
// and y_i on x_i), each pair released together by a barrier. The check runs
// inside each push's FCALL, so the second of each pair sees the first: in
// every pair exactly one push is written and the other is refused naming
// the cycle, and the stream stores no cycle. (The cold read's reader stored
// a cycle in 1 of 10 pairs in 3 of 3 runs against 9aad08b5f.)
func TestOrderBarrierPairsStoreNoCycle(t *testing.T) {
	t.Parallel()
	start := time.Now()
	addr, c := wstest.Start(t)
	ctx := context.Background()
	const s = "order: barrier"
	refused := regexp.MustCompile(`^TASK push REFUSED id=(\S+) why="ORDER CYCLE stream=\\"order: barrier\\" DEPENDS-ON cycle (\S+) -> (\S+) -> (\S+)" ms=\d+\n$`)
	for i := 0; i < 10; i++ {
		ids := [2]string{fmt.Sprintf("x%d", i), fmt.Sprintf("y%d", i)}
		barrier := make(chan struct{})
		var wg sync.WaitGroup
		codes, outs := [2]int{}, [2]string{}
		for k := 0; k < 2; k++ {
			wg.Add(1)
			go func(k int) {
				defer wg.Done()
				<-barrier
				codes[k], outs[k], _ = runSprint("task", "push", "--redis", addr, "--actor", "rowan", "--id", ids[k],
					"--stream", s, "--kind", "build", "--title", ids[k], "--on", ids[1-k])
			}(k)
		}
		close(barrier)
		wg.Wait()
		written := 0
		for k := 0; k < 2; k++ {
			switch m := refused.FindStringSubmatch(outs[k]); {
			case codes[k] == 0 && c.Exists(ctx, "task:"+ids[k]).Val() == 1:
				written++
			case codes[k] == 1 && m != nil && m[1] == ids[k] && m[2] == ids[k] && m[3] == ids[1-k] && m[4] == ids[k] &&
				c.Exists(ctx, "task:"+ids[k]).Val() == 0:
			default:
				t.Fatalf("pair %d push %s: exit %d %q", i, ids[k], codes[k], outs[k])
			}
		}
		if written != 1 {
			t.Fatalf("pair %d: %d written, want 1: %q %q", i, written, outs[0], outs[1])
		}
	}
	so, err := ws.ReadOrder(ctx, c, s)
	if err != nil || so.Err != nil || len(so.Order) != 11 || so.Stale() {
		t.Fatalf("after 10 pairs: %d ranked, cycle %v, stale %v, %v", len(so.Order), so.Err, so.Stale(), err)
	}
	t.Logf("10 barrier pairs: 10 written, 10 refused ORDER CYCLE, 0 cycles stored (%d ranked with the sentinel, not stale); wall %s",
		len(so.Order), time.Since(start).Round(time.Millisecond))
}

// TestOrderPushWithoutReorderGrantIsRefused is probe (c): a seat whose ACL
// holds the push functions but not +fcall|ns_ws_reorder pushing a card onto
// a stream is REFUSED before any write (ORDER GRANT, nothing written: keys,
// ws:log and every ZSET unchanged), through task push --actor, card push
// and card cut --from; the same seat with the grant pushes and writes the
// order (order=<n>, never order=error). Decided: refuse, because the push's
// own FCALL cannot write the order (ws.Order is Go; a Lua copy would drift)
// and a written card with no order is the broken state the read found.
func TestOrderPushWithoutReorderGrantIsRefused(t *testing.T) {
	t.Parallel()
	start := time.Now()
	const pushes = "~* &* +@all -fcall +fcall|ns_tcard_push +fcall|ns_card_push +fcall|ns_card_header +fcall|ns_ping"
	var extra []string
	for name, rule := range map[string]string{"bare": pushes, "granted": pushes + " +fcall|" + ws.FnReorder} {
		extra = append(extra, "--user", name, "on", ">"+name+"-pass")
		extra = append(extra, strings.Split(rule, " ")...)
	}
	addr := testutil.Start(t, extra...)
	ctx := context.Background()
	admin := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = admin.Close() })
	if err := fn.Load(ctx, admin); err != nil {
		t.Fatal(err)
	}
	as := func(name string) *redis.Client {
		cl := redis.NewClient(&redis.Options{Addr: addr, Username: name, Password: name + "-pass"})
		t.Cleanup(func() { _ = cl.Close() })
		return cl
	}
	const s = "order: grant"
	taskPush := func(cl *redis.Client, id string) (int, string, string) {
		var out, errOut bytes.Buffer
		cmd, code, done := parseTaskCard("push", []string{"--actor", "rowan", "--id", id, "--stream", s, "--kind", "build",
			"--title", id}, &out, &errOut, func(string) string { return "" })
		if !done {
			code = cmd.run(ctx, store.New(cl), "push", &out, &errOut)
		}
		return code, out.String(), errOut.String()
	}
	dir := t.TempDir()
	cardPush := func(cl *redis.Client, name string) (int, string, string) {
		var out, errOut bytes.Buffer
		body, err := os.ReadFile(cycleCard(t, dir, s, name, "31", "none"))
		if err != nil {
			t.Fatal(err)
		}
		code := cardPushOn(ctx, cl, "sg", []card.CardFile{{Name: name + ".md", Body: body}}, card.PushOptions{}, &out, &errOut)
		return code, out.String(), errOut.String()
	}
	cut := func(cl *redis.Client, id string) (int, string) {
		d := cutDepsRedis(&fakeCutForge{}, cl)
		text, err := os.ReadFile(cutFile(t, dir, id+".tsv", s, [2]string{id, "none"}))
		if err != nil {
			t.Fatal(err)
		}
		return runCutFrom(cutFromOpts{Text: text, NoGitHub: true}, d)
	}
	grant := `ORDER GRANT ns_ws_reorder: NOPERM User bare has no permissions to run the 'fcall' command`

	bare := as("bare")
	before := storeSnapshot(t, admin)
	code, out, _ := taskPush(bare, "t1")
	if code != 1 || !strings.HasPrefix(out, `TASK push REFUSED id=t1 why="`+grant) {
		t.Fatalf("task push without the grant: %d %q", code, out)
	}
	t.Logf("task push, no grant: exit %d, %s", code, strings.TrimSpace(out))
	code, _, errOut := cardPush(bare, "g1")
	if code != 2 || !strings.Contains(errOut, "push refused, nothing written: "+grant) {
		t.Fatalf("card push without the grant: %d %q", code, errOut)
	}
	t.Logf("card push, no grant: exit %d, %s", code, strings.TrimSpace(errOut))
	code, out = cut(bare, "k1")
	if code != 1 || !strings.Contains(out, "CARD CUT REFUSED row=1 ") || !strings.Contains(out, grant) {
		t.Fatalf("card cut without the grant: %d %q", code, out)
	}
	t.Logf("card cut --from, no grant: exit %d, %s", code, strings.ReplaceAll(strings.TrimSpace(out), "\n", " | "))
	if after := storeSnapshot(t, admin); after != before {
		t.Fatalf("a push without the grant wrote:\n%s---\n%s", before, after)
	}

	granted := as("granted")
	code, out, errOut = taskPush(granted, "t2")
	if code != 0 || !regexp.MustCompile(`^TASK push id=t2 from=- to=ready order=2 order_rt=\d+ order_ms=`).MatchString(out) {
		t.Fatalf("task push with the grant: %d %q %q", code, out, errOut)
	}
	t.Logf("task push, granted: exit %d, %s", code, strings.TrimSpace(out))
	code, out, errOut = cardPush(granted, "g2")
	if code != 0 || !strings.Contains(out, "ORDER stream=\"order: grant\" order=3 ") {
		t.Fatalf("card push with the grant: %d %q %q", code, out, errOut)
	}
	code, out = cut(granted, "k2")
	if code != 0 || !strings.Contains(out, "ORDER stream=\"order: grant\" order=4 ") {
		t.Fatalf("card cut with the grant: %d %q", code, out)
	}
	if so, err := ws.ReadOrder(ctx, admin, s); err != nil || so.Stale() || len(so.Order) != 4 {
		t.Fatalf("order after the granted pushes: %d ranked, stale %v, %v", len(so.Order), so.Stale(), err)
	}
	t.Logf("granted: task, card and cut pushes written with their order, not stale; wall %s", time.Since(start).Round(time.Millisecond))
}

// TestRenameMovesAStreamHoldingACycleWhole is the #4410 fix round 4 item
// (1): a stream that already holds a DEPENDS-ON cycle (rx on ry, ry on rx,
// seeded by hand; rz unrelated) renames whole, exit 0. A rename moves every
// card with its edges, so it adds no edge; TK.move skips the cycle check on
// a rename move (o.rename), and ns_ws_rename, which moves one task at a
// time, never stops partway. With that skip removed this test is red the
// way the cold read saw it: REFUSED ORDER CYCLE after rx moved.
func TestRenameMovesAStreamHoldingACycleWhole(t *testing.T) {
	t.Parallel()
	start := time.Now()
	addr, c := wstest.Start(t)
	ctx := context.Background()
	const a, b = "rn: a", "rn: b"
	for _, id := range []string{"rx", "ry", "rz"} {
		if code, out, errOut := runSprint("task", "push", "--redis", addr, "--actor", "rowan", "--id", id, "--stream", a,
			"--kind", "build", "--title", id); code != 0 {
			t.Fatalf("push %s: %d %q %q", id, code, out, errOut)
		}
	}
	c.HSet(ctx, "task:rx", "blocked_on", "ry")
	c.HSet(ctx, "task:ry", "blocked_on", "rx")
	if so, _ := ws.ReadOrder(ctx, c, a); so.Err == nil {
		t.Fatalf("the seed holds no cycle")
	}
	code, out, errOut := runSprint("stream", "rename", "--redis", addr, a, b)
	if code != 0 || !strings.HasPrefix(out, `RENAMED from="rn: a" to="rn: b" members=3`) {
		t.Fatalf("rename of a stream holding a cycle: %d %q %q", code, out, errOut)
	}
	for _, id := range []string{"rx", "ry", "rz"} {
		if got := c.HGet(ctx, "task:"+id, "stream").Val(); got != b {
			t.Fatalf("task:%s stream %q after the rename, want %q", id, got, b)
		}
	}
	live := append(c.ZRange(ctx, ws.Key(b, "waiting"), 0, -1).Val(), c.ZRange(ctx, ws.Key(b, "ready"), 0, -1).Val()...)
	done := c.ZRange(ctx, ws.Key(b, "done"), 0, -1).Val()
	sort.Strings(live)
	if strings.Join(live, " ") != "rn-b:sentinel rx ry rz" || strings.Join(done, " ") != "rn-a:sentinel" ||
		c.HGet(ctx, "task:rn-a:sentinel", "where_ok").Val() != "fail" {
		t.Fatalf("after the rename: waiting and ready %v, done %v (ok=%s)", live, done, c.HGet(ctx, "task:rn-a:sentinel", "where_ok").Val())
	}
	for _, w := range []string{"waiting", "ready", "working", "review", "merging", "parked", "landed", "done"} {
		if n := c.ZCard(ctx, ws.Key(a, w)).Val(); n != 0 {
			t.Fatalf("%s still holds %d", ws.Key(a, w), n)
		}
	}
	if c.SIsMember(ctx, "ws:names", a).Val() || !c.SIsMember(ctx, "ws:names", b).Val() {
		t.Fatalf("ws:names %v", c.SMembers(ctx, "ws:names").Val())
	}
	t.Logf("%s; %s waiting and ready %v, done %v (ok=fail); ws:names %v; wall %s", strings.TrimSpace(out), b, live, done,
		c.SMembers(ctx, "ws:names").Val(), time.Since(start).Round(time.Millisecond))
}

// TestQuackCutProbesTheReorderGrant is the #4410 fix round 4 item (2):
// quack cut pushes its cards into the stream's waiting set, so it probes the
// reorder grant as the four other push doors do. A seat without the grant
// is refused before the stop or any card is written (the store unchanged);
// with the grant the cards are written and the stream's order with them.
func TestQuackCutProbesTheReorderGrant(t *testing.T) {
	t.Parallel()
	start := time.Now()
	addr := testutil.Start(t, "--user", "bare", "on", ">bare-pass", "~*", "&*", "+@all", "-fcall",
		"+fcall|ns_tcard_push", "+fcall|ns_ping", "--user", "granted", "on", ">granted-pass", "~*", "&*", "+@all")
	ctx := context.Background()
	admin := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = admin.Close() })
	if err := fn.Load(ctx, admin); err != nil {
		t.Fatal(err)
	}
	const S, s = "quack-grant", "quack: grant"
	admin.HSet(ctx, "s:"+S, "status", "open")
	as := func(name string) *redis.Client {
		cl := redis.NewClient(&redis.Options{Addr: addr, Username: name, Password: name + "-pass"})
		t.Cleanup(func() { _ = cl.Close() })
		return cl
	}
	tiers := []string{"flash", "pro"}
	cuts, err := quackCuts(3, S, s, "mas-bandwidth/quack", "dev", quackBase, tiers)
	if err != nil {
		t.Fatal(err)
	}
	plan := quackPlan{name: S, stream: s, repo: "mas-bandwidth/quack", who: "rowan", baseSHA: quackBase, tiers: tiers, cuts: cuts}

	before := storeSnapshot(t, admin)
	var out, errOut bytes.Buffer
	code := quackCutOn(ctx, as("bare"), plan, &out, &errOut)
	grant := `ORDER GRANT ns_ws_reorder: NOPERM User bare has no permissions to run the 'fcall' command`
	if code != 1 || !strings.HasPrefix(out.String(), `CUT REFUSED sprint=quack-grant stream=quack:\x20grant why="`+grant) {
		t.Fatalf("quack cut without the grant: %d %q %q", code, out.String(), errOut.String())
	}
	if after := storeSnapshot(t, admin); after != before {
		t.Fatalf("quack cut without the grant wrote:\n%s---\n%s", before, after)
	}
	t.Logf("quack cut, no grant: exit %d, %s", code, strings.TrimSpace(out.String()))

	out.Reset()
	errOut.Reset()
	code = quackCutOn(ctx, as("granted"), plan, &out, &errOut)
	if code != 0 || !regexp.MustCompile(`^CUT n=3 stream=quack:\\x20grant sprint=quack-grant .* pushed=3 skipped=0 refused=0 base-sha=\w+ pitstop=set order=4 order_rt=\d+ order_ms=\S+ lift=`).MatchString(out.String()) {
		t.Fatalf("quack cut with the grant: %d %q %q", code, out.String(), errOut.String())
	}
	if so, err := ws.ReadOrder(ctx, admin, s); err != nil || so.Stale() || len(so.Order) != 4 {
		t.Fatalf("order after the granted cut: %d ranked, stale %v, %v", len(so.Order), so.Stale(), err)
	}
	t.Logf("quack cut, granted: exit %d, %s; wall %s", code, strings.ReplaceAll(strings.TrimSpace(out.String()), "\n", " | "),
		time.Since(start).Round(time.Millisecond))
}
