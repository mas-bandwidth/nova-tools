package main

// Controls of nova-tools #3092 rev 7 on the Functions harness: a throwaway
// redis-server with the real nova_sprint library (fn.Load), driven through
// the real verbs via run(...). Records are keyed by the #3139 unit contract;
// each PR is seeded as a unit through the real writer ns_unit_head.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/disposition"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/land"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/task"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
)

const holdRepo = "nova-tools"

var (
	headA = strings.Repeat("a", 40)
	headB = strings.Repeat("b", 40)
)

type holdEnv struct {
	t    *testing.T
	c    *redis.Client
	addr string
	S    string
}

func newHoldEnv(t *testing.T, sprint string) *holdEnv {
	t.Helper()
	addr := testutil.Start(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = c.Close() })
	if err := fn.Load(context.Background(), c); err != nil {
		t.Fatalf("load nova_sprint: %v", err)
	}
	return &holdEnv{t: t, c: c, addr: addr, S: sprint}
}

func (e *holdEnv) run(args ...string) (int, string, string) {
	e.t.Helper()
	if len(args) > 1 && args[0] == "friend" && args[1] == "hello" {
		// #2929 rev 4: hello's --as must equal the seat (NOVA_FRIEND).
		for i := 2; i+1 < len(args); i++ {
			if args[i] == "--as" {
				e.t.Setenv(seatEnv, args[i+1])
			}
		}
	}
	var out, errb bytes.Buffer
	code := run(append(args, "--redis", e.addr), &out, &errb)
	return code, out.String(), errb.String()
}

// register writes a friend's desired capacity through the one width writer,
// `capacity friend` (#2934); friend hello never registers or sets slots.
func (e *holdEnv) register(f string, slots int) {
	e.t.Helper()
	if err := e.c.HSetNX(context.Background(), "machine:ctl:ceiling", "slots", 64).Err(); err != nil {
		e.t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if code := run([]string{"capacity", "friend", "--redis", e.addr, "--as", "config", "--machine", "ctl", f, strconv.Itoa(slots)}, &out, &errb); code != 0 {
		e.t.Fatalf("capacity friend %s %d: exit %d %s", f, slots, code, errb.String())
	}
}

// friends registers each friend through the real writers: capacity friend,
// then friend hello.
func (e *holdEnv) friends(names ...string) {
	e.t.Helper()
	ctx := context.Background()
	if err := e.c.HSet(ctx, "machine:ctl:ceiling", "slots", 64).Err(); err != nil {
		e.t.Fatal(err)
	}
	for _, f := range names {
		e.register(f, 0)
		if code, _, errOut := e.run("friend", "hello", "--as", f, "--once", "--host", "ctl", "--session", "ctl-"+f); code != 0 {
			e.t.Fatalf("hello %s: exit %d %s", f, code, errOut)
		}
	}
}

func (e *holdEnv) policy(fixTo, reader string) {
	e.t.Helper()
	if err := e.c.HSet(context.Background(), "s:"+e.S+":policy", "fix_to", fixTo, "release_reader", reader).Err(); err != nil {
		e.t.Fatal(err)
	}
}

func unitOf(pr int) string { return "u-" + strconv.Itoa(pr) }

// unit seeds (or moves) a unit through the real ns_unit_head.
func (e *holdEnv) unit(pr int, head, author string) {
	e.t.Helper()
	if _, err := land.CallUnitHead(context.Background(), e.c, land.UnitHeadParams{
		Sprint: e.S, Unit: unitOf(pr), Repo: holdRepo, Base: "dev", Branch: "b-" + strconv.Itoa(pr),
		Head: head, BaseSHA: headA, PR: strconv.Itoa(pr), Author: author,
	}); err != nil {
		e.t.Fatalf("ns_unit_head: %v", err)
	}
}

var bodySeq int

func (e *holdEnv) ingest(pr int, body string) (int, string, string) {
	e.t.Helper()
	bodySeq++
	path := filepath.Join(e.t.TempDir(), "body.md")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		e.t.Fatal(err)
	}
	url := "mas-bandwidth/nova-tools/pull/" + strconv.Itoa(pr) + "#issuecomment-" + strconv.Itoa(9000+bodySeq)
	return e.run("hold", "ingest", "--as", "rowan", "--sprint", e.S, "--repo", "mas-bandwidth/nova-tools",
		"--pr", strconv.Itoa(pr), "--url", url, "--body-file", path)
}

func (e *holdEnv) mustIngest(pr int, body, want string) string {
	e.t.Helper()
	code, out, errOut := e.ingest(pr, body)
	if code != 0 || !strings.HasPrefix(out, want) {
		e.t.Fatalf("ingest #%d: exit %d out %q err %q, want prefix %q", pr, code, out, errOut, want)
	}
	return out
}

func (e *holdEnv) route(extra ...string) string {
	e.t.Helper()
	code, out, errOut := e.run(append([]string{"hold", "route", "--once", "--sprint", e.S}, extra...)...)
	if code != 0 {
		e.t.Fatalf("hold route: exit %d %s", code, errOut)
	}
	return out
}

func (e *holdEnv) queue(f string) []string {
	e.t.Helper()
	ids, err := e.c.ZRange(context.Background(), "s:"+e.S+":open:"+f, 0, -1).Result()
	if err != nil {
		e.t.Fatal(err)
	}
	return ids
}

func (e *holdEnv) zcard(f string) int64 {
	return int64(len(e.queue(f)))
}

func (e *holdEnv) hget(key, field string) string {
	v, err := e.c.HGet(context.Background(), key, field).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		e.t.Fatal(err)
	}
	return v
}

func (e *holdEnv) holdKey(pr int, holder string) string {
	return "s:" + e.S + ":hold:" + unitOf(pr) + ":" + holder
}

func (e *holdEnv) parks() map[string]disposition.Park {
	e.t.Helper()
	raw, err := e.c.HGetAll(context.Background(), disposition.ParkKey(e.S)).Result()
	if err != nil {
		e.t.Fatal(err)
	}
	out := map[string]disposition.Park{}
	for k, v := range raw {
		var p disposition.Park
		if err := json.Unmarshal([]byte(v), &p); err != nil {
			e.t.Fatal(err)
		}
		out[k] = p
	}
	return out
}

func (e *holdEnv) fixIDs() []string {
	e.t.Helper()
	var ids []string
	keys, err := e.c.Keys(context.Background(), "task:fix-*").Result()
	if err != nil {
		e.t.Fatal(err)
	}
	for _, k := range keys {
		ids = append(ids, strings.TrimPrefix(k, "task:"))
	}
	sort.Strings(ids)
	return ids
}

// replay re-delivers an already handled hold event to ns_hold_route.
func (e *holdEnv) replay(eventID string) string {
	e.t.Helper()
	r, err := e.c.FCall(context.Background(), disposition.FunctionRoute, nil,
		e.S, "event", eventID, "", "1").StringSlice()
	if err != nil {
		e.t.Fatal(err)
	}
	return strings.Join(r, " ")
}

func (e *holdEnv) lastEvent() string {
	e.t.Helper()
	ms, err := e.c.XRevRangeN(context.Background(), disposition.EventsKey(e.S), "+", "-", 1).Result()
	if err != nil || len(ms) == 0 {
		e.t.Fatalf("no hold event: %v", err)
	}
	return ms[0].ID
}

func (e *holdEnv) setDown(f string, down bool) {
	ctx := context.Background()
	if down {
		e.c.Set(ctx, "friend:"+f+":down", "1", 0)
	} else {
		e.c.Del(ctx, "friend:"+f+":down")
	}
}

func typed(who, head, verdict string, score int, tail string) string {
	s := "DISPOSITION who=" + who + " head=" + head + " verdict=" + verdict + " score=" + strconv.Itoa(score)
	if tail != "" {
		s += ": " + tail
	}
	return s + "\n"
}

func repairLine(who, head string) string {
	return "REPAIR who=" + who + " head=" + head + " ready=true\n\nfixed.\n"
}

const substance = "the parser accepts any cert at cmd/nova-pulse/accept.go:424; add the check"

func fixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "internal", "nsprint", "disposition", "testdata", "typed", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func holdWait() time.Duration {
	if v := os.Getenv("NOVA_TEST_WAIT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	return 30 * time.Second
}

func TestIngestDeployedLines(t *testing.T) {
	ctx := context.Background()
	e := newHoldEnv(t, "s-ingest")
	if n := e.c.Exists(ctx, "friends", "friends:login").Val(); n != 0 {
		t.Fatalf("friends or friends:login present at start (%d)", n)
	}
	e.friends("stella", "johnny")
	e.register("rowan", 32)
	if code, _, errOut := e.run("friend", "hello", "--as", "rowan", "--login", "rowan-claude",
		"--once", "--host", "ctl", "--session", "ctl-rowan"); code != 0 {
		t.Fatalf("hello rowan --login: %d %s", code, errOut)
	}
	members := e.c.SMembers(ctx, "friends").Val()
	sort.Strings(members)
	if strings.Join(members, ",") != "johnny,rowan,stella" {
		t.Fatalf("friends = %v", members)
	}
	logins := e.c.HGetAll(ctx, "friends:login").Val()
	if len(logins) != 1 || logins["rowan-claude"] != "rowan" {
		t.Fatalf("friends:login = %v", logins)
	}
	countLogin := func() int {
		n := 0
		for _, m := range e.c.XRange(ctx, "cap:log", "-", "+").Val() {
			if m.Values["kind"] == "friend-login" {
				n++
			}
		}
		return n
	}
	if n := countLogin(); n != 1 {
		t.Fatalf("cap:log friend-login receipts = %d, want 1", n)
	}
	for _, tc := range []struct{ as, alias, want string }{
		{"stella", "rowan-claude", "LOGIN-TAKEN rowan-claude rowan"},
		{"johnny", "stella", "LOGIN-IS-FRIEND stella"},
	} {
		code, _, errOut := e.run("friend", "hello", "--as", tc.as, "--login", tc.alias, "--once", "--host", "ctl", "--session", "ctl-"+tc.as)
		if code != 2 || !strings.Contains(errOut, tc.want) {
			t.Fatalf("hello %s --login %s: exit %d %q, want 2 %q", tc.as, tc.alias, code, errOut, tc.want)
		}
		if got := e.c.HGetAll(ctx, "friends:login").Val(); len(got) != 1 || got["rowan-claude"] != "rowan" {
			t.Fatalf("friends:login changed by a refused hello: %v", got)
		}
	}

	type want struct {
		id, head, out string
		pr            int
		check         func(e *holdEnv)
	}
	cases := []want{
		{"5802800953", "b2d830d36bef4d7d907b461e7a176c6009996d9d", "RECORD hold", 3286, func(e *holdEnv) {
			k := e.holdKey(3286, "stella")
			if e.hget(k, "kind") != "substance" || e.hget(k, "score") != "7" || !strings.Contains(e.hget(k, "reason"), "cmd/nova-pulse/accept.go:424") {
				t.Fatalf("5802800953 hold = %v", e.c.HGetAll(ctx, k).Val())
			}
		}},
		{"5802709513", "b3f9f7e15e73fd98e8c0385ee7c15d3e08c12816", "RECORD hold", 2913, func(e *holdEnv) {
			if k := e.holdKey(2913, "stella"); e.hget(k, "kind") != "substance" {
				t.Fatalf("5802709513 hold = %v", e.c.HGetAll(ctx, k).Val())
			}
		}},
		{"5802733875", "5c22281227787713632a1cd01f90ba9a2be61277", "RECORD read", 2804, func(e *holdEnv) {
			r := e.c.HGetAll(ctx, "s:"+e.S+":read:"+unitOf(2804)+":stella").Val()
			if r["verdict"] != "APPROVE" || r["score"] != "10" || r["head"] != "5c22281227787713632a1cd01f90ba9a2be61277" || !strings.HasSuffix(r["url"], r["comment_id"]) {
				t.Fatalf("5802733875 read = %v", r)
			}
			u := e.c.HGetAll(ctx, "s:"+e.S+":u:"+unitOf(2804)).Val()
			if u["last_read_at"] == "" || u["approve_head"] != r["head"] || u["approve_seq"] != r["seq"] {
				t.Fatalf("5802733875 reap fields = %v", u)
			}
		}},
		{"5802690236", "911f065c54d7abd33bcf54d56a668dcffbe594ae", "RECORD read", 2764, func(e *holdEnv) {
			r := e.c.HGetAll(ctx, "s:"+e.S+":read:"+unitOf(2764)+":stella").Val()
			if r["verdict"] != "APPROVE" || r["score"] != "8" {
				t.Fatalf("5802690236 read = %v", r)
			}
		}},
		{"5802626981", "b2d830d36bef4d7d907b461e7a176c6009996d9d", "RECORD repair", 3286, func(e *holdEnv) {
			if v := e.hget(disposition.RepairKey(e.S, unitOf(3286)), "b2d830d36bef4d7d907b461e7a176c6009996d9d"); !strings.HasPrefix(v, "rowan ") {
				t.Fatalf("5802626981 repair = %q", v)
			}
		}},
		{"5802664097", "27bb138d129ddf683993644c3d9c6b100c06362b", "RECORD repair", 2749, func(e *holdEnv) {
			if v := e.hget(disposition.RepairKey(e.S, unitOf(2749)), "27bb138d129ddf683993644c3d9c6b100c06362b"); !strings.HasPrefix(v, "rowan ") {
				t.Fatalf("5802664097 repair = %q", v)
			}
		}},
		{"5802939314", "93b903042a200394a9a13596e7aaa47e79df5534", "NORECORD ready=false", 2911, func(e *holdEnv) {
			if n := e.c.Exists(ctx, disposition.RepairKey(e.S, unitOf(2911)), disposition.EventsKey(e.S)).Val(); n != 0 {
				t.Fatalf("5802939314 wrote %d keys", n)
			}
		}},
	}
	for _, tc := range cases {
		// One sprint per fixture: 5802626981's REPAIR is at the head of
		// 5802800953's HOLD on #3286, so on one sprint it would be refused.
		e.S = "s-" + tc.id
		e.policy("rowan", "stella")
		e.unit(tc.pr, tc.head, "johnny")
		e.mustIngest(tc.pr, fixture(t, tc.id+".md"), tc.out)
		tc.check(e)
	}

	e.S = "s-refuse"
	e.policy("rowan", "stella")
	e.unit(3286, "b2d830d36bef4d7d907b461e7a176c6009996d9d", "johnny")
	body := fixture(t, "5802800953.md")
	noHead := strings.Replace(body, " head=b2d830d36bef4d7d907b461e7a176c6009996d9d", "", 1)
	nobody := strings.Replace(body, "who=stella", "who=nobody", 1)
	for name, b := range map[string]string{"obsolete": fixture(t, "obsolete-rev4.txt"), "no-head": noHead, "nobody": nobody} {
		before := e.c.DBSize(ctx).Val()
		code, out, _ := e.ingest(3286, b)
		if code != 2 || !strings.HasPrefix(out, "REFUSED") {
			t.Fatalf("%s: exit %d %q, want REFUSED exit 2", name, code, out)
		}
		if after := e.c.DBSize(ctx).Val(); after != before {
			t.Fatalf("%s wrote keys: %d -> %d", name, before, after)
		}
	}
	// who=rowan-claude and who=rowan write the same rowan@<head> key.
	asRowan := strings.Replace(body, "who=stella", "who=rowan", 1)
	e.mustIngest(3286, asRowan, "RECORD hold")
	seq1 := e.hget("s:"+e.S+":read:"+unitOf(3286)+":rowan", "seq")
	e.mustIngest(3286, strings.Replace(body, "who=stella", "who=rowan-claude", 1), "RECORD hold")
	if seq2 := e.hget("s:"+e.S+":read:"+unitOf(3286)+":rowan", "seq"); seq2 == seq1 || e.c.Exists(ctx, "s:"+e.S+":read:"+unitOf(3286)+":rowan-claude").Val() != 0 {
		t.Fatalf("rowan-claude did not write the rowan key (seq %s -> %s)", seq1, seq2)
	}

	// Deployed-config contrast: hello without --login leaves rowan-claude unknown.
	e2 := newHoldEnv(t, "s-contrast")
	e2.policy("rowan", "stella")
	e2.c.HSet(ctx, "machine:ctl:ceiling", "slots", 64)
	e2.register("rowan", 32)
	hello := []string{"friend", "hello", "--as", "rowan", "--once", "--host", "ctl", "--session", "ctl-rowan"}
	if code, _, errOut := e2.run(hello...); code != 0 {
		t.Fatalf("contrast hello: %s", errOut)
	}
	e2.unit(3286, "b2d830d36bef4d7d907b461e7a176c6009996d9d", "johnny")
	claude := strings.Replace(body, "who=stella", "who=rowan-claude", 1)
	before := e2.c.DBSize(ctx).Val()
	if code, out, _ := e2.ingest(3286, claude); code != 2 || !strings.HasPrefix(out, "REFUSED unknown-who") {
		t.Fatalf("contrast ingest: exit %d %q, want REFUSED unknown-who", code, out)
	}
	if after := e2.c.DBSize(ctx).Val(); after != before {
		t.Fatalf("refused ingest wrote keys")
	}
	if code, _, errOut := e2.run(append(hello, "--login", "rowan-claude")...); code != 0 {
		t.Fatalf("contrast hello --login: %s", errOut)
	}
	e2.mustIngest(3286, claude, "RECORD hold")
	if e2.c.Exists(ctx, "s:"+e2.S+":read:"+unitOf(3286)+":rowan").Val() != 1 {
		t.Fatalf("contrast ingest did not write rowan@<head>")
	}

	// Hold 5 on #3473 at 90527217: NAME-IS-LOGIN on every hello, aliases or
	// not; a mapped login never registers as a friend.
	if code, _, errOut := e.run("friend", "hello", "--as", "rowan-claude", "--once", "--host", "ctl", "--session", "ctl-rc"); code != 2 || !strings.Contains(errOut, "NAME-IS-LOGIN rowan-claude") {
		t.Fatalf("hello --as a mapped login, no --login: exit %d %q, want 2 NAME-IS-LOGIN", code, errOut)
	}
	if e.c.SIsMember(ctx, "friends", "rowan-claude").Val() {
		t.Fatalf("a mapped login registered as a friend")
	}
	// A duplicate alias in one hello is one mapping and one receipt.
	receipts := countLogin()
	if code, _, errOut := e.run("friend", "hello", "--as", "johnny", "--login", "jz-bot", "--login", "jz-bot", "--once", "--host", "ctl", "--session", "ctl-johnny"); code != 0 {
		t.Fatalf("hello johnny --login jz-bot twice: %d %s", code, errOut)
	}
	if n := countLogin(); n != receipts+1 {
		t.Fatalf("duplicate alias receipts = %d, want %d", n, receipts+1)
	}
	if got := e.c.HGet(ctx, "friends:login", "jz-bot").Val(); got != "johnny" {
		t.Fatalf("friends:login jz-bot = %q, want johnny", got)
	}
}

func TestControl40(t *testing.T) {
	ctx := context.Background()
	e := newHoldEnv(t, "s40")
	e.friends("stella", "johnny")
	e.register("rowan", 0)
	if code, _, errOut := e.run("friend", "hello", "--as", "rowan", "--login", "rowan-claude", "--once", "--host", "ctl", "--session", "ctl-rowan"); code != 0 {
		t.Fatal(errOut)
	}
	e.policy("rowan", "stella")
	e.unit(40, headA, "johnny")
	e.mustIngest(40, typed("rowan-claude", headA, "APPROVE", 9, ""), "RECORD read")
	e.mustIngest(40, typed("rowan", headA, "APPROVE", 9, ""), "RECORD read")
	keys := e.c.Keys(ctx, "s:"+e.S+":read:*").Val()
	if len(keys) != 1 || keys[0] != "s:"+e.S+":read:"+unitOf(40)+":rowan" {
		t.Fatalf("read keys = %v, want one rowan key", keys)
	}
	out := e.mustIngest(40, typed("stella", headA, "HOLD", 5, "CI is red on the shard 1/4 leg. Pending a rerun."), "RECORD note")
	if !strings.Contains(out, " ci ") {
		t.Fatalf("CI-only HOLD: %q, want note kind=ci", out)
	}
	e.unit(41, headA, "johnny")
	out = e.mustIngest(41, typed("stella", headA, "HOLD", 5, "CI is red because internal/pulse/kinds.go:88 drops the third field."), "RECORD hold")
	if !strings.Contains(out, "substance") {
		t.Fatalf("file:line HOLD with CI words: %q", out)
	}
}

func TestControl53(t *testing.T) {
	ctx := context.Background()
	e := newHoldEnv(t, "s53")
	e.friends("stella", "johnny", "rowan")
	e.policy("rowan", "stella")
	// A hold-shaped comment with no record: nothing blocks the unit.
	e.unit(53, headA, "johnny")
	e.mustIngest(53, "stella: HOLD, "+substance+"\n", "NORECORD prose")
	if v := e.hget("s:"+e.S+":u:"+unitOf(53), "holds_open"); v != "" && v != "0" {
		t.Fatalf("no-record PR holds_open = %q", v)
	}
	if _, err := land.CallUnitEval(ctx, e.c, e.S, unitOf(53), holdRepo, "dev", 0); err != nil {
		t.Fatal(err)
	}
	if e.c.ZScore(ctx, land.LandableKey(e.S, holdRepo, "dev"), unitOf(53)).Err() != nil {
		t.Fatalf("the no-record unit is not landable")
	}
	// A record with no comment: the gate's holds_open counts it.
	e.unit(54, headA, "johnny")
	e.mustIngest(54, typed("stella", headA, "HOLD", 5, substance), "RECORD hold")
	if v := e.hget("s:"+e.S+":u:"+unitOf(54), "holds_open"); v != "1" {
		t.Fatalf("recorded hold holds_open = %q, want 1", v)
	}
}

func TestHoldRouteOneOwner(t *testing.T) {
	ctx := context.Background()
	e := newHoldEnv(t, "s-owner")
	e.friends("stella", "johnny", "rowan", "emma")
	e.policy("rowan", "stella")
	st := store.New(e.c)

	e.unit(1, headA, "johnny")
	e.mustIngest(1, typed("stella", headA, "HOLD", 5, substance), "RECORD hold")
	e.route()
	e.route()
	if q := e.queue("rowan"); len(q) != 1 || q[0] != disposition.FixID("1", "stella", headA) {
		t.Fatalf("one fix to fix_to: %v", q)
	}

	// An open fix-<n>-* task means no push.
	e.unit(2, headA, "johnny")
	if got, err := task.Push(ctx, st, task.PushRequest{Sprint: e.S, ID: "fix-2-manual", Kind: task.KindFix, Title: "x",
		Effects: task.EffectsNone, To: "rowan"}); err != nil || got != task.PushCreated {
		t.Fatalf("seed fix: %v %v", got, err)
	}
	e.mustIngest(2, typed("stella", headA, "HOLD", 5, substance), "RECORD hold")
	if out := e.route(); !strings.Contains(out, "OPEN-FIX fix-2-manual") {
		t.Fatalf("open fix: %q", out)
	}
	if n := e.zcard("rowan"); n != 2 {
		t.Fatalf("rowan queue = %d, want 2", n)
	}

	// A later REPAIR means no push.
	e.unit(3, headA, "johnny")
	e.mustIngest(3, typed("stella", headA, "HOLD", 5, substance), "RECORD hold")
	e.unit(3, headB, "johnny")
	e.mustIngest(3, repairLine("rowan", headB), "RECORD repair")
	e.route()
	if e.c.Exists(ctx, "task:"+disposition.FixID("3", "stella", headA)).Val() != 0 {
		t.Fatalf("a superseded hold got a fix task")
	}
	if q := e.queue("stella"); len(q) != 1 || q[0] != task.ReviewID(holdRepo, 3, headB, "stella") {
		t.Fatalf("re-read: %v", q)
	}

	// Hold 3 on #3473 at 90527217: a fix taken by fix_to (claimed, out of
	// idx:task:open) still holds the at-most-one guard for a second holder.
	e.unit(6, headA, "johnny")
	e.mustIngest(6, typed("stella", headA, "HOLD", 5, substance), "RECORD hold")
	e.route()
	fix6 := disposition.FixID("6", "stella", headA)
	if err := e.c.HSet(ctx, "s:"+e.S, "status", "open").Err(); err != nil {
		t.Fatal(err)
	}
	e.register("rowan", 4)
	if code, _, errOut := e.run("friend", "hello", "--as", "rowan", "--once", "--host", "ctl", "--session", "ctl-rowan"); code != 0 {
		t.Fatalf("hello rowan (4 slots): %s", errOut)
	}
	claims, err := task.TakeAvailable(ctx, st, "rowan", e.S, fix6, 1, "rowan", "take-fix6")
	if err != nil || len(claims) != 1 {
		t.Fatalf("take %s: %v %v", fix6, claims, err)
	}
	if s := e.hget("task:"+fix6, "state"); s != "claimed" {
		t.Fatalf("%s state = %q, want claimed", fix6, s)
	}
	e.mustIngest(6, typed("emma", headA, "HOLD", 5, substance), "RECORD hold")
	if out := e.route(); !strings.Contains(out, "OPEN-FIX "+fix6) {
		t.Fatalf("second holder with a claimed fix: %q, want OPEN-FIX %s", out, fix6)
	}
	if e.c.Exists(ctx, "task:"+disposition.FixID("6", "emma", headA)).Val() != 0 {
		t.Fatalf("a second fix task was created while the first was claimed")
	}

	// A jev HOLD line makes no hold and no task.
	e.unit(4, headA, "johnny")
	if code, out, _ := e.ingest(4, typed("jev", headA, "HOLD", 3, substance)); code != 2 || !strings.HasPrefix(out, "REFUSED unknown-who") {
		t.Fatalf("jev: %d %q", code, out)
	}
	// A REPAIR at the hold's own head is refused.
	e.unit(5, headA, "johnny")
	e.mustIngest(5, typed("stella", headA, "HOLD", 5, substance), "RECORD hold")
	if code, out, _ := e.ingest(5, repairLine("rowan", headA)); code != 2 || !strings.HasPrefix(out, "REFUSED same-head") {
		t.Fatalf("same-head repair: %d %q", code, out)
	}
}

func downHolderCase(t *testing.T, pr int, markDown func(e *holdEnv), wantTo string, e *holdEnv) {
	t.Helper()
	ctx := context.Background()
	e.unit(pr, headA, "johnny")
	e.mustIngest(pr, typed("emma", headA, "HOLD", 5, substance), "RECORD hold")
	e.route()
	fix := disposition.FixID(strconv.Itoa(pr), "emma", headA)
	if e.c.ZScore(ctx, "s:"+e.S+":open:rowan", fix).Err() != nil {
		t.Fatalf("fix task not queued")
	}
	emmaBefore := e.zcard("emma")
	markDown(e)
	e.unit(pr, headB, "johnny")
	e.mustIngest(pr, repairLine("rowan", headB), "RECORD repair")
	e.route()
	if s := e.hget("task:"+fix, "state"); s != "closed" || e.hget("task:"+fix, "evidence") != "repair at "+headB[:12] {
		t.Fatalf("fix task not closed: %s", s)
	}
	if e.c.ZScore(ctx, "s:"+e.S+":open:rowan", fix).Err() == nil {
		t.Fatalf("closed fix task still queued")
	}
	id := task.ReviewID(holdRepo, pr, headB, "emma")
	if e.c.ZScore(ctx, "s:"+e.S+":open:"+wantTo, id).Err() != nil {
		t.Fatalf("re-read %s not in open:%s", id, wantTo)
	}
	title := e.hget("task:"+id, "title")
	if wantTo != "emma" {
		if !strings.Contains(title, "release_for=emma") {
			t.Fatalf("title %q lacks release_for=emma", title)
		}
		if n := e.zcard("emma"); n != emmaBefore {
			t.Fatalf("down holder's queue grew: %d -> %d", emmaBefore, n)
		}
	} else if strings.Contains(title, "release_for") {
		t.Fatalf("up holder's re-read carries release_for: %q", title)
	}
}

func TestHoldRouteDownHolder(t *testing.T) {
	e := newHoldEnv(t, "s-down")
	e.friends("stella", "johnny", "rowan", "emma")
	e.policy("rowan", "stella")
	downHolderCase(t, 10, func(e *holdEnv) { e.setDown("emma", true) }, "stella", e)
	e.setDown("emma", false)
	downHolderCase(t, 11, func(e *holdEnv) {
		e.c.HSet(context.Background(), "friend:emma:state", "state", "out-of-credits")
	}, "stella", e)
	e.c.Del(context.Background(), "friend:emma:state")
	downHolderCase(t, 12, func(*holdEnv) {}, "emma", e)
}

func TestHoldRouteNoFixSelfOrCI(t *testing.T) {
	ctx := context.Background()
	e := newHoldEnv(t, "s-self")
	e.friends("stella", "johnny", "rowan")
	e.policy("johnny", "stella")
	// A rowan HOLD on rowan's PR: note kind=self, no fix, one read to a non-author.
	e.unit(20, headA, "rowan")
	if out := e.mustIngest(20, typed("rowan", headA, "HOLD", 5, substance), "RECORD note"); !strings.Contains(out, " self ") {
		t.Fatalf("self: %q", out)
	}
	e.route()
	if ids := e.fixIDs(); len(ids) != 0 {
		t.Fatalf("self hold cut fix tasks %v", ids)
	}
	if q := e.queue("stella"); len(q) != 1 || q[0] != task.ReviewID(holdRepo, 20, headA, "stella") {
		t.Fatalf("self read: %v", q)
	}
	// A stella CI-only HOLD: note kind=ci; a read, or one update-* when CONFLICTING.
	e.unit(21, headA, "rowan")
	e.mustIngest(21, typed("stella", headA, "HOLD", 5, "CI is red on shard 2."), "RECORD note")
	e.route()
	if len(e.fixIDs()) != 0 || e.c.ZScore(ctx, "s:"+e.S+":open:stella", task.ReviewID(holdRepo, 21, headA, "stella")).Err() != nil {
		t.Fatalf("ci note: fix %v, stella %v", e.fixIDs(), e.queue("stella"))
	}
	e.unit(22, headA, "rowan")
	if _, err := land.CallUnitMergeable(ctx, e.c, e.S, unitOf(22), headA, land.MergeableConflict); err != nil {
		t.Fatalf("mergeable #22: %v", err)
	}
	e.mustIngest(22, typed("stella", headA, "HOLD", 5, "CI is red on shard 2."), "RECORD note")
	e.route()
	if q := e.queue("johnny"); len(q) != 1 || q[0] != disposition.UpdateID("22", headA) {
		t.Fatalf("CONFLICTING ci note: %v", q)
	}
	if len(e.fixIDs()) != 0 {
		t.Fatalf("ci note cut a fix")
	}
	// A file:line HOLD: exactly one fix-*.
	e.unit(23, headA, "rowan")
	e.mustIngest(23, typed("stella", headA, "HOLD", 5, substance), "RECORD hold")
	e.route()
	e.route()
	if ids := e.fixIDs(); len(ids) != 1 || ids[0] != disposition.FixID("23", "stella", headA) {
		t.Fatalf("file:line hold fixes: %v", ids)
	}
}

func TestHoldRouteUnpark(t *testing.T) {
	ctx := context.Background()
	start := func(t *testing.T, name string, pr int) *holdEnv {
		e := newHoldEnv(t, name)
		e.friends("stella", "johnny", "rowan", "emma")
		e.policy("rowan", "stella")
		e.unit(pr, headA, "johnny")
		e.mustIngest(pr, typed("emma", headA, "HOLD", 5, substance), "RECORD hold")
		e.route()
		e.setDown("emma", true)
		e.setDown("stella", true)
		e.unit(pr, headB, "johnny")
		e.mustIngest(pr, repairLine("rowan", headB), "RECORD repair")
		e.route()
		if p := e.parks(); len(p) != 1 {
			t.Fatalf("repair re-read not parked: %v", p)
		}
		if e.zcard("stella") != 0 || e.zcard("emma") != 0 || e.zcard("johnny") != 0 {
			t.Fatalf("a queue grew while parked")
		}
		return e
	}
	id := task.ReviewID(holdRepo, 30, headB, "emma")
	// (a) release_reader comes up first.
	a := start(t, "s-unpark-a", 30)
	a.setDown("stella", false)
	a.route()
	a.route()
	if q := a.queue("stella"); len(q) != 1 || q[0] != id || !strings.Contains(a.hget("task:"+id, "title"), "release_for=emma") {
		t.Fatalf("(a) stella queue %v", q)
	}
	if len(a.parks()) != 0 || a.zcard("emma") != 0 || a.zcard("johnny") != 0 {
		t.Fatalf("(a) park left or queue grew")
	}
	// (b) the holder comes up first.
	b := start(t, "s-unpark-b", 30)
	b.setDown("emma", false)
	b.route()
	b.route()
	if q := b.queue("emma"); len(q) != 1 || q[0] != id || strings.Contains(b.hget("task:"+id, "title"), "release_for") {
		t.Fatalf("(b) emma queue %v", q)
	}
	if len(b.parks()) != 0 || b.zcard("stella") != 0 {
		t.Fatalf("(b) park left or stella grew")
	}
	// (c) a note read whose release_reader is the PR author parks; a policy
	// change to an up non-author unparks exactly one read.
	c := newHoldEnv(t, "s-unpark-c")
	c.friends("stella", "johnny", "rowan", "emma")
	c.policy("rowan", "stella")
	c.unit(31, headA, "stella")
	c.mustIngest(31, typed("emma", headA, "HOLD", 5, "CI is red on shard 3."), "RECORD note")
	c.route()
	if p := c.parks(); len(p) != 1 || p[holdRepo+":31:n1"].Reason != "author" {
		t.Fatalf("(c) park %v", p)
	}
	c.c.HSet(ctx, "s:"+c.S+":policy", "release_reader", "johnny")
	c.route()
	c.route()
	if q := c.queue("johnny"); len(q) != 1 || q[0] != task.ReviewID(holdRepo, 31, headA, "johnny") {
		t.Fatalf("(c) johnny queue %v", q)
	}
	if len(c.parks()) != 0 || c.zcard("stella") != 0 {
		t.Fatalf("(c) park left or the author's queue grew")
	}
}

func TestHoldRouteUnparkFix(t *testing.T) {
	ctx := context.Background()
	setup := func(t *testing.T, name string) *holdEnv {
		e := newHoldEnv(t, name)
		e.friends("stella", "johnny", "rowan", "emma")
		e.policy("rowan", "stella")
		return e
	}
	// (a) down to up.
	a := setup(t, "s-fix-a")
	a.setDown("rowan", true)
	a.unit(40, headA, "johnny")
	a.mustIngest(40, typed("stella", headA, "HOLD", 5, substance), "RECORD hold")
	holdEvent := a.lastEvent()
	a.route()
	p := a.parks()
	if len(p) != 1 || p[holdRepo+":40:h:stella:"+headA].Role != "fix_to" || p[holdRepo+":40:h:stella:"+headA].Reason != "down" || a.zcard("rowan") != 0 {
		t.Fatalf("(a) park %v", p)
	}
	a.setDown("rowan", false)
	a.route()
	a.route()
	fix := disposition.FixID("40", "stella", headA)
	if q := a.queue("rowan"); len(q) != 1 || q[0] != fix || len(a.parks()) != 0 {
		t.Fatalf("(a) rowan %v parks %v", q, a.parks())
	}
	// (e) replay after (a).
	if r := a.replay(holdEvent); !strings.HasPrefix(r, "DUP") {
		t.Fatalf("(e) replay: %q", r)
	}
	a.route()
	if a.zcard("rowan") != 1 {
		t.Fatalf("(e) replay pushed again")
	}

	// (b) author to eligible.
	b := setup(t, "s-fix-b")
	b.unit(41, headA, "rowan")
	b.mustIngest(41, typed("stella", headA, "HOLD", 5, substance), "RECORD hold")
	for i := 0; i < 10; i++ {
		b.route()
	}
	field := holdRepo + ":41:h:stella:" + headA
	if p := b.parks(); p[field].Reason != "author" || b.zcard("rowan") != 0 {
		t.Fatalf("(b) park %v rowan %d", p, b.zcard("rowan"))
	}
	b.setDown("emma", true)
	b.c.HSet(ctx, "s:"+b.S+":policy", "fix_to", "emma")
	b.route()
	if p := b.parks(); p[field].Reason != "down" || b.zcard("emma") != 0 {
		t.Fatalf("(b) after fix_to=emma down: %v", p)
	}
	b.setDown("emma", false)
	b.route()
	if q := b.queue("emma"); len(q) != 1 || q[0] != disposition.FixID("41", "stella", headA) || len(b.parks()) != 0 {
		t.Fatalf("(b) emma %v", q)
	}
	if b.zcard("rowan") != 0 {
		t.Fatalf("(b) the author's queue grew")
	}

	// (c) a CONFLICTING CI note with fix_to down parks one update-*.
	c := setup(t, "s-fix-c")
	c.setDown("rowan", true)
	c.unit(42, headA, "johnny")
	if _, err := land.CallUnitMergeable(ctx, c.c, c.S, unitOf(42), headA, land.MergeableConflict); err != nil {
		t.Fatalf("(c) mergeable #42: %v", err)
	}
	c.mustIngest(42, typed("stella", headA, "HOLD", 5, "CI is red on shard 1."), "RECORD note")
	noteEvent := c.lastEvent()
	c.route()
	if p := c.parks(); len(p) != 1 || p[holdRepo+":42:n1"].ID != disposition.UpdateID("42", headA) {
		t.Fatalf("(c) park %v", p)
	}
	c.setDown("rowan", false)
	c.route()
	c.route()
	if q := c.queue("rowan"); len(q) != 1 || q[0] != disposition.UpdateID("42", headA) {
		t.Fatalf("(c) rowan %v", q)
	}
	if r := c.replay(noteEvent); !strings.HasPrefix(r, "DUP") || c.zcard("rowan") != 1 {
		t.Fatalf("(e) replay after (c): %q", r)
	}

	// (d) the hold is released while parked: dropped with unpark-drop, no push.
	d := setup(t, "s-fix-d")
	d.setDown("rowan", true)
	d.unit(43, headA, "johnny")
	d.mustIngest(43, typed("stella", headA, "HOLD", 5, substance), "RECORD hold")
	d.route()
	d.unit(43, headB, "johnny")
	if out := d.mustIngest(43, typed("stella", headB, "APPROVE", 9, ""), "RECORD read"); !strings.Contains(out, "released") {
		t.Fatalf("(d) approve did not release: %q", out)
	}
	d.setDown("rowan", false)
	d.route()
	if len(d.parks()) != 0 || d.zcard("rowan") != 0 {
		t.Fatalf("(d) parks %v rowan %d", d.parks(), d.zcard("rowan"))
	}
	drop := false
	for _, m := range d.c.XRange(ctx, "s:"+d.S+":log", "-", "+").Val() {
		if m.Values["kind"] == "unpark-drop" {
			drop = true
		}
	}
	if !drop {
		t.Fatalf("(d) no unpark-drop receipt")
	}
}

func TestHoldRouteCrashReplay(t *testing.T) {
	ctx := context.Background()
	setup := func(t *testing.T, name, consumer string) (*holdEnv, string) {
		e := newHoldEnv(t, name)
		e.friends("stella", "johnny", "rowan")
		e.policy("rowan", "stella")
		e.unit(50, headA, "johnny")
		e.mustIngest(50, typed("stella", headA, "HOLD", 5, substance), "RECORD hold")
		ev := disposition.EventsKey(e.S)
		if err := e.c.XGroupCreateMkStream(ctx, ev, disposition.Group, "0").Err(); err != nil {
			t.Fatal(err)
		}
		ms, err := e.c.XReadGroup(ctx, &redis.XReadGroupArgs{Group: disposition.Group, Consumer: consumer, Streams: []string{ev, ">"}, Count: 10, Block: -1}).Result()
		if err != nil || len(ms) != 1 || len(ms[0].Messages) != 1 {
			t.Fatalf("leave pending: %v %v", ms, err)
		}
		return e, ms[0].Messages[0].ID
	}
	fix := disposition.FixID("50", "stella", headA)
	after := func(t *testing.T, e *holdEnv, eid string) {
		if r := e.replay(eid); !strings.HasPrefix(r, "DUP") {
			t.Fatalf("replay: %q", r)
		}
		if n := e.c.XPending(ctx, disposition.EventsKey(e.S), disposition.Group).Val().Count; n != 0 {
			t.Fatalf("event still pending (%d)", n)
		}
		if q := e.queue("rowan"); len(q) != 1 || q[0] != fix {
			t.Fatalf("queue %v", q)
		}
	}
	// (a) owner field set, no task hash: one tick pushes the recorded id.
	a, eid := setup(t, "s-crash-a", "hold-route")
	a.c.HSet(ctx, disposition.OwnerKey(a.S, unitOf(50)), "h:stella:"+headA, fix+" 1 rowan")
	a.route()
	after(t, a, eid)

	// (b) task hash set, no owner field: one tick writes the owner, pushes nothing.
	b, eid := setup(t, "s-crash-b", "hold-route")
	if got, err := task.Push(ctx, store.New(b.c), task.PushRequest{Sprint: b.S, ID: fix, Kind: task.KindFix, Title: "fix",
		Effects: task.EffectsNone, To: "rowan", Front: true, Priority: 1}); err != nil || got != task.PushCreated {
		t.Fatalf("seed: %v %v", got, err)
	}
	b.route()
	if v := b.hget(disposition.OwnerKey(b.S, unitOf(50)), "h:stella:"+headA); !strings.HasPrefix(v, fix+" ") {
		t.Fatalf("(b) owner = %q", v)
	}
	after(t, b, eid)

	// (c) read by a crashed consumer, no FCALL: reclaimed only once idle.
	c, eid := setup(t, "s-crash-c", "crashed")
	c.route("--reclaim-idle", "3600000")
	if c.zcard("rowan") != 0 {
		t.Fatalf("(c) pushed before idle")
	}
	pend := c.c.XPendingExt(ctx, &redis.XPendingExtArgs{Stream: disposition.EventsKey(c.S), Group: disposition.Group, Start: "-", End: "+", Count: 10}).Val()
	if len(pend) != 1 || pend[0].Consumer != "crashed" {
		t.Fatalf("(c) pending %v", pend)
	}
	deadline := time.Now().Add(holdWait())
	for {
		// XPENDING <stream> hold-route IDLE 50 - + 10: the event, once idle
		// past the reclaim threshold the next tick is given.
		idle, err := c.c.Do(ctx, "XPENDING", disposition.EventsKey(c.S), disposition.Group, "IDLE", 50, "-", "+", 10).Slice()
		if err != nil {
			t.Fatal(err)
		}
		if len(idle) == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("(c) event never idle")
		}
		time.Sleep(10 * time.Millisecond)
	}
	c.route("--reclaim-idle", "50")
	after(t, c, eid)

	// Hold 4 on #3473 at 90527217: lease:hold-route renew and release are
	// token-checked in one script. An owner whose lease expired can neither
	// extend nor delete its successor's lease.
	ctx2 := context.Background()
	lc := c.c
	lc.Del(ctx2, disposition.LeaseKey)
	if ok, err := holdLease(ctx2, lc, "stale"); err != nil || !ok {
		t.Fatalf("first take: %v %v", ok, err)
	}
	lc.Del(ctx2, disposition.LeaseKey) // the stale owner's lease expires
	if ok, err := holdLease(ctx2, lc, "next"); err != nil || !ok {
		t.Fatalf("successor take: %v %v", ok, err)
	}
	lc.PExpire(ctx2, disposition.LeaseKey, 2*time.Second)
	if ok, err := holdLease(ctx2, lc, "stale"); err != nil || ok {
		t.Fatalf("stale renew: %v %v, want false", ok, err)
	}
	if ttl := lc.PTTL(ctx2, disposition.LeaseKey).Val(); ttl > 2*time.Second {
		t.Fatalf("stale renew extended the successor's lease: pttl %v", ttl)
	}
	if dropLease(ctx2, lc, "stale") {
		t.Fatalf("stale release reported a delete")
	}
	if v := lc.Get(ctx2, disposition.LeaseKey).Val(); v != "next" {
		t.Fatalf("stale release touched the successor's lease: %q", v)
	}
	if ok, err := holdLease(ctx2, lc, "next"); err != nil || !ok {
		t.Fatalf("owner renew: %v %v", ok, err)
	}
	if !dropLease(ctx2, lc, "next") || lc.Exists(ctx2, disposition.LeaseKey).Val() != 0 {
		t.Fatalf("owner release did not delete")
	}
}

func TestHoldReleaseHolderOnly(t *testing.T) {
	e := newHoldEnv(t, "s-release")
	e.friends("stella", "johnny", "rowan", "emma")
	e.policy("rowan", "stella")
	e.unit(60, headA, "emma")
	e.mustIngest(60, typed("johnny", headA, "HOLD", 4, substance), "RECORD hold")
	e.unit(60, headB, "emma")
	e.mustIngest(60, typed("stella", headB, "APPROVE", 10, ""), "RECORD read")
	if e.hget(e.holdKey(60, "johnny"), "released_by") != "" {
		t.Fatalf("stella's APPROVE released johnny's hold")
	}
	// A stale head is refused, for the typed line and for hold release.
	if code, out, _ := e.ingest(60, typed("johnny", headA, "APPROVE", 9, "")); code != 2 || !strings.HasPrefix(out, "REFUSED stale-head") {
		t.Fatalf("stale approve: %d %q", code, out)
	}
	rel := func(as, head string) (int, string) {
		code, out, _ := e.run("hold", "release", "--as", as, "--sprint", e.S, "nova-tools#60", "johnny", "--head", head, "--evidence", "evidence-1")
		return code, out
	}
	if code, out := rel("johnny", headA); code != 2 || !strings.HasPrefix(out, "REFUSED stale-head") {
		t.Fatalf("stale release: %d %q", code, out)
	}
	if code, out := rel("stella", headB); code != 2 || !strings.HasPrefix(out, "REFUSED holder-up") {
		t.Fatalf("reader release of an up holder: %d %q", code, out)
	}
	if out := e.mustIngest(60, typed("johnny", headB, "APPROVE", 9, ""), "RECORD read"); !strings.Contains(out, "released") {
		t.Fatalf("johnny's APPROVE did not release: %q", out)
	}
	if e.hget(e.holdKey(60, "johnny"), "released_by") != "johnny" || e.hget("s:"+e.S+":u:"+unitOf(60), "holds_open") != "0" {
		t.Fatalf("hold not released by johnny")
	}
	// hold release by the holder at the current head.
	e.unit(61, headA, "emma")
	e.mustIngest(61, typed("johnny", headA, "HOLD", 4, substance), "RECORD hold")
	e.unit(61, headB, "emma")
	if code, out, _ := e.run("hold", "release", "--as", "johnny", "--sprint", e.S, "nova-tools#61", "johnny", "--head", headB, "--evidence", "evidence-2"); code != 0 || !strings.HasPrefix(out, "RELEASED") {
		t.Fatalf("holder release: %d %q", code, out)
	}
}

func TestHoldRouteReplay(t *testing.T) {
	e := newHoldEnv(t, "s-replay")
	e.friends("stella", "johnny", "rowan")
	e.policy("rowan", "stella")
	e.unit(70, headA, "johnny")
	e.mustIngest(70, typed("stella", headA, "HOLD", 5, substance), "RECORD hold")
	eid := e.lastEvent()
	e.route()
	if r := e.replay(eid); !strings.HasPrefix(r, "DUP") {
		t.Fatalf("replay: %q", r)
	}
	e.route()
	if q := e.queue("rowan"); len(q) != 1 {
		t.Fatalf("same event twice: %v", q)
	}
}

func TestHoldRouteZeroREST(t *testing.T) {
	stub := testutil.StartGitHubStub(t)
	e := newHoldEnv(t, "s-rest")
	e.friends("stella", "johnny", "rowan")
	e.policy("rowan", "stella")
	e.unit(80, headA, "johnny")
	e.mustIngest(80, typed("stella", headA, "HOLD", 5, substance), "RECORD hold")
	e.route()
	e.unit(80, headB, "johnny")
	e.mustIngest(80, repairLine("rowan", headB), "RECORD repair")
	e.route()
	if code, _, errOut := e.run("hold", "show", "--sprint", e.S, "nova-tools#80"); code != 0 {
		t.Fatalf("hold show: %s", errOut)
	}
	if code, _, _ := e.run("hold", "release", "--as", "stella", "--sprint", e.S, "nova-tools#80", "stella", "--head", headB, "--evidence", "evidence-3"); code != 0 {
		t.Fatalf("hold release")
	}
	if n := stub.Calls(); n != 0 {
		t.Fatalf("GitHub was called %d times", n)
	}
}

// reviewTask pushes one review task for pr at head to f and claims it; it
// returns the task id and its token.
func (e *holdEnv) reviewTask(pr int, head, f string) (string, string) {
	e.t.Helper()
	ctx := context.Background()
	st := store.New(e.c)
	id := task.ReviewID(holdRepo, pr, head, f)
	// ns_task_push still fences a review push on the PR record's head
	// (task_claim.lua), a key outside this unit.
	if err := e.c.HSet(ctx, "s:"+e.S+":pr:"+holdRepo+":"+strconv.Itoa(pr), "head", head).Err(); err != nil {
		e.t.Fatal(err)
	}
	if got, err := task.Push(ctx, st, task.PushRequest{Sprint: e.S, ID: id, Kind: task.KindReview,
		Title: "read", Effects: task.EffectsNone, Repo: holdRepo, PR: pr, Head: head, To: f}); err != nil || got != task.PushCreated {
		e.t.Fatalf("push %s: %v %v", id, got, err)
	}
	if err := e.c.HSet(ctx, "s:"+e.S, "status", "open").Err(); err != nil {
		e.t.Fatal(err)
	}
	e.register(f, 8)
	if code, _, errOut := e.run("friend", "hello", "--as", f, "--once", "--host", "ctl", "--session", "ctl-"+f); code != 0 {
		e.t.Fatalf("hello %s (8 slots): %s", f, errOut)
	}
	claims, err := task.TakeAvailable(ctx, st, f, e.S, id, 1, f, "take-"+id)
	if err != nil || len(claims) != 1 {
		e.t.Fatalf("take %s: %v %v", id, claims, err)
	}
	return id, claims[0].Token
}

// done runs `task done` on a review task with a body file.
func (e *holdEnv) done(id, token, url, body string, extra ...string) (int, string, string) {
	e.t.Helper()
	path := filepath.Join(e.t.TempDir(), "done.md")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		e.t.Fatal(err)
	}
	args := []string{"task", "done", "--sprint", e.S, "--id", id, "--token", token,
		"--evidence", url, "--url", url, "--body-file", path}
	return e.run(append(args, extra...)...)
}

// TestTaskDoneReviewIngest: `task done --body-file` on a review task goes
// through the shared parser and ns_ingest_disposition in the same atomic
// call as the close (#3092 rev 7, hold item 1 on #3473): APPROVE and HOLD
// make their records and close; a malformed or prose body, a typed head
// that is not the task head, and a who that is not the task owner refuse
// with the task still claimed and no record. The legacy flag path the
// deployed friend-harness uses keeps closing.
func TestTaskDoneReviewIngest(t *testing.T) {
	ctx := context.Background()
	e := newHoldEnv(t, "s-done")
	e.friends("stella", "emma", "rowan", "johnny")
	e.policy("rowan", "emma")
	e.unit(81, headA, "johnny")
	id, token := e.reviewTask(81, headA, "stella")
	url := "mas-bandwidth/nova-tools/pull/81#issuecomment-5900000081"
	taskKey := "task:" + id
	readKey := "s:" + e.S + ":read:" + unitOf(81) + ":stella"
	claimed := func(what string) {
		t.Helper()
		if s := e.hget(taskKey, "state"); s != "claimed" {
			t.Fatalf("%s: task state %q, want claimed", what, s)
		}
		if e.c.Exists(ctx, readKey).Val() != 0 {
			t.Fatalf("%s: a read record was written", what)
		}
	}

	// Malformed (the obsolete rev 4 form): REFUSED, exit 2, nothing written.
	if code, out, _ := e.done(id, token, url, "DISPOSITION who=stella HOLD #81 at aaaaaaaa score=6 kind=substance reason=x\n"); code != 2 || !strings.Contains(out, "REFUSED") {
		t.Fatalf("malformed: exit %d %q", code, out)
	}
	claimed("malformed")
	// Prose: a review close needs a record.
	if code, out, _ := e.done(id, token, url, "**stella HOLD 4:** fixed\n"); code != 2 || !strings.Contains(out, "REFUSED") {
		t.Fatalf("prose: exit %d %q", code, out)
	}
	claimed("prose")
	// Head mismatch: the task-head fence.
	if code, out, _ := e.done(id, token, url, typed("stella", headB, "APPROVE", 9, "")); code != 2 || !strings.Contains(out, "INVALID") {
		t.Fatalf("head mismatch: exit %d %q", code, out)
	}
	claimed("head mismatch")
	// A line typed by someone other than the task owner.
	if code, out, _ := e.done(id, token, url, typed("emma", headA, "APPROVE", 9, "")); code != 2 || !strings.Contains(out, "REFUSED who-not-owner") {
		t.Fatalf("who not owner: exit %d %q", code, out)
	}
	claimed("who not owner")
	// Flags and a body file together are refused before any call.
	if code, _, _ := e.done(id, token, url, typed("stella", headA, "APPROVE", 9, ""), "--verdict", "APPROVE"); code != 2 {
		t.Fatalf("flags and body: exit %d", code)
	}
	claimed("flags and body")

	// APPROVE: closed, read record at head, the disp field in the doc.go shape.
	if code, out, errOut := e.done(id, token, url, typed("stella", headA, "APPROVE", 9, "looks right")); code != 0 || !strings.HasPrefix(out, "DONE DONE id="+id) || !strings.Contains(out, "RECORD read") {
		t.Fatalf("approve: exit %d %q %q", code, out, errOut)
	}
	if s := e.hget(taskKey, "state"); s != "closed" {
		t.Fatalf("approve: task state %q", s)
	}
	if v, s := e.hget(taskKey, "verdict"), e.hget(taskKey, "score"); v != "APPROVE" || s != "9" {
		t.Fatalf("approve: task verdict %q score %q", v, s)
	}
	if h, v, u := e.hget(readKey, "head"), e.hget(readKey, "verdict"), e.hget(readKey, "url"); h != headA || v != "APPROVE" || u != url {
		t.Fatalf("approve read record: head %q verdict %q url %q", h, v, u)
	}
	if d := e.hget("s:"+e.S+":disp:"+holdRepo+":81", "stella@"+headA); d != "APPROVE 9 "+url+" 5900000081" {
		t.Fatalf("approve disp = %q", d)
	}
	// A repeated identical done is CLOSED and writes nothing new.
	if code, out, _ := e.done(id, token, url, typed("stella", headA, "APPROVE", 9, "looks right")); code != 0 || !strings.HasPrefix(out, "DONE CLOSED") {
		t.Fatalf("repeat: exit %d %q", code, out)
	}

	// HOLD: a hold record with its event, and the close.
	e.unit(82, headA, "johnny")
	hid, htoken := e.reviewTask(82, headA, "emma")
	hurl := "mas-bandwidth/nova-tools/pull/82#issuecomment-5900000082"
	if code, out, errOut := e.done(hid, htoken, hurl, typed("emma", headA, "HOLD", 4, substance)); code != 0 || !strings.Contains(out, "RECORD hold") {
		t.Fatalf("hold: exit %d %q %q", code, out, errOut)
	}
	hk := e.holdKey(82, "emma")
	if h, k := e.hget(hk, "head"), e.hget(hk, "kind"); h != headA || k != "substance" {
		t.Fatalf("hold record: head %q kind %q", h, k)
	}
	if n := e.c.XLen(ctx, disposition.EventsKey(e.S)).Val(); n != 1 {
		t.Fatalf("hold events = %d, want 1", n)
	}
	if s := e.hget("task:"+hid, "state"); s != "closed" {
		t.Fatalf("hold: task state %q", s)
	}
	if d := e.hget("s:"+e.S+":disp:"+holdRepo+":82", "emma@"+headA); !strings.HasPrefix(d, "HOLD 4 "+hurl) {
		t.Fatalf("hold disp = %q", d)
	}

	// No unit for the PR: refused, the task stays claimed.
	nid, ntoken := e.reviewTask(83, headA, "stella")
	if code, out, _ := e.done(nid, ntoken, url, typed("stella", headA, "APPROVE", 9, "")); code != 2 || !strings.Contains(out, "REFUSED no-unit") {
		t.Fatalf("no unit: exit %d %q", code, out)
	}
	if s := e.hget("task:"+nid, "state"); s != "claimed" {
		t.Fatalf("no unit: task state %q", s)
	}

	// The legacy flag path (rowan-tools bin/friend-harness) still closes and
	// keeps the base disp value for the readers #3491 has not moved.
	e.unit(84, headA, "johnny")
	lid, ltoken := e.reviewTask(84, headA, "stella")
	if code, out, errOut := e.run("task", "done", "--sprint", e.S, "--id", lid, "--token", ltoken,
		"--evidence", "legacy evidence", "--verdict", "APPROVE", "--score", "8", "--head", headA); code != 0 || out != "DONE DONE id="+lid+"\n" {
		t.Fatalf("legacy: exit %d %q %q", code, out, errOut)
	}
	if d := e.hget("s:"+e.S+":disp:"+holdRepo+":84", "stella@"+headA); d != "APPROVE 8 legacy evidence" {
		t.Fatalf("legacy disp = %q", d)
	}
}

// TestUnitMergeableWriter: ns_unit_mergeable is the production writer of a
// unit's mergeable word (#3092 rev 7 hold item 6 on #3473), fenced on the
// unit head; a head move resets the word to UNKNOWN; a CONFLICTING word
// written by it routes a CI note to one update-<n>-<sha8> for fix_to.
func TestUnitMergeableWriter(t *testing.T) {
	ctx := context.Background()
	e := newHoldEnv(t, "s-merg")
	e.friends("stella", "johnny", "rowan")
	e.policy("johnny", "rowan")
	ukey := "s:" + e.S + ":u:" + unitOf(71)

	if _, err := land.CallUnitMergeable(ctx, e.c, e.S, unitOf(71), headA, "CONFLICTING"); !errors.Is(err, land.ErrNoUnit) {
		t.Fatalf("no unit: %v, want ErrNoUnit", err)
	}
	e.unit(71, headA, "stella")
	if _, err := land.CallUnitMergeable(ctx, e.c, e.S, unitOf(71), headA, "BEHIND"); err == nil {
		t.Fatalf("BEHIND accepted")
	}
	if _, err := land.CallUnitMergeable(ctx, e.c, e.S, unitOf(71), headB, "CONFLICTING"); !errors.Is(err, land.ErrStaleHead) {
		t.Fatalf("stale head: %v, want ErrStaleHead", err)
	}
	if m := e.hget(ukey, "mergeable"); m != "" {
		t.Fatalf("a refused write left mergeable=%q", m)
	}
	if _, err := land.CallUnitMergeable(ctx, e.c, e.S, unitOf(71), headA, "CONFLICTING"); err != nil {
		t.Fatalf("write: %v", err)
	}
	if m, h := e.hget(ukey, "mergeable"), e.hget(ukey, "mergeable_head"); m != "CONFLICTING" || h != headA {
		t.Fatalf("unit mergeable %q at %q", m, h)
	}
	e.mustIngest(71, typed("rowan", headA, "HOLD", 5, "CI is red on shard 2."), "RECORD note")
	e.route()
	if q := e.queue("johnny"); len(q) != 1 || q[0] != disposition.UpdateID("71", headA) {
		t.Fatalf("CONFLICTING from the writer: johnny %v", q)
	}

	// A new head: the old word is not carried; the CI note is a read.
	e.unit(71, headB, "stella")
	if m := e.hget(ukey, "mergeable"); m != "UNKNOWN" {
		t.Fatalf("after a head move mergeable = %q, want UNKNOWN", m)
	}
	if _, err := land.CallUnitMergeable(ctx, e.c, e.S, unitOf(71), headB, "MERGEABLE"); err != nil {
		t.Fatalf("write at headB: %v", err)
	}
	e.mustIngest(71, typed("rowan", headB, "HOLD", 5, "CI is red on shard 2."), "RECORD note")
	e.route()
	if q := e.queue("johnny"); len(q) != 1 {
		t.Fatalf("MERGEABLE cut another update: johnny %v", q)
	}
	if q := e.queue("rowan"); len(q) != 1 || q[0] != task.ReviewID(holdRepo, 71, headB, "rowan") {
		t.Fatalf("MERGEABLE ci note read: rowan %v", q)
	}
}

// TestHoldRouteNoPolicyRefusal (#3814): hold route --once on a sprint without
// fix_to/release_reader exits 1 with one line naming the remedy.
func TestHoldRouteNoPolicyRefusal(t *testing.T) {
	e := newHoldEnv(t, "hold-3814")
	e.friends("rowan", "stella")

	// Neither key in policy.
	code, out, errOut := e.run("hold", "route", "--once", "--sprint", e.S)
	if code != 1 {
		t.Fatalf("exit code %d, want 1; out=%q errOut=%q", code, out, errOut)
	}
	if out != "" {
		t.Fatalf("stdout not empty: %q", out)
	}
	const wantRemedy = "nova-sprint plan apply with policy fix_to and policy release_reader"
	if !strings.Contains(errOut, wantRemedy) {
		t.Fatalf("errOut %q does not contain remedy %q", errOut, wantRemedy)
	}
	if !strings.Contains(errOut, "fix_to and release_reader") {
		t.Fatalf("errOut %q does not name missing keys", errOut)
	}
	if strings.Count(strings.TrimSpace(errOut), "\n") != 0 {
		t.Fatalf("errOut has %d lines, want exactly 1: %q", strings.Count(strings.TrimSpace(errOut), "\n")+1, errOut)
	}

	// Only fix_to in policy: still refused exit 1 with the remedy.
	if err := e.c.HSet(context.Background(), "s:"+e.S+":policy", "fix_to", "rowan").Err(); err != nil {
		t.Fatal(err)
	}
	code, out, errOut = e.run("hold", "route", "--once", "--sprint", e.S)
	if code != 1 || !strings.Contains(errOut, wantRemedy) {
		t.Fatalf("only fix_to: code=%d out=%q errOut=%q", code, out, errOut)
	}

	// Only release_reader in policy: still refused exit 1 with the remedy.
	e.c.Del(context.Background(), "s:"+e.S+":policy")
	if err := e.c.HSet(context.Background(), "s:"+e.S+":policy", "release_reader", "stella").Err(); err != nil {
		t.Fatal(err)
	}
	code, out, errOut = e.run("hold", "route", "--once", "--sprint", e.S)
	if code != 1 || !strings.Contains(errOut, wantRemedy) {
		t.Fatalf("only release_reader: code=%d out=%q errOut=%q", code, out, errOut)
	}

	// Both present: route passes (no refusal).
	if err := e.c.HSet(context.Background(), "s:"+e.S+":policy", "fix_to", "rowan", "release_reader", "stella").Err(); err != nil {
		t.Fatal(err)
	}
	code, _, errOut = e.run("hold", "route", "--once", "--sprint", e.S)
	if code != 0 {
		t.Fatalf("both keys present: code=%d errOut=%q", code, errOut)
	}
}
