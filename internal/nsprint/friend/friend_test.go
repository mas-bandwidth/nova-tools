package friend_test

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/friend"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/life"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/task"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/redis/go-redis/v9"
)

// fsRedis starts a throwaway redis-server on 127.0.0.1 with the nova_sprint
// library loaded (the shape of life's rdRedis; no real host is reached).
func fsRedis(t *testing.T) (*store.Store, *redis.Client) {
	t.Helper()
	addr := testutil.Start(t)
	client := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = client.Close() })
	ctx := context.Background()
	if err := fn.Load(ctx, client); err != nil {
		t.Fatalf("load nova_sprint library: %v", err)
	}
	return store.New(client), client
}

const (
	fsSprint = "control-3101"
	fsRepo   = "nova-tools"
)

var fsRoster = life.Roster{
	MayHold:     []string{"stella", "johnny"},
	Builders:    []string{"johnny"},
	Coordinator: "rowan",
}

func fsHead(c byte) string { return strings.Repeat(string(c), 40) }

func fsMust(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// fsFriends registers the sprint and the friends: each is UP with 8 slots.
func fsFriends(t *testing.T, client *redis.Client, names ...string) {
	t.Helper()
	ctx := context.Background()
	fsMust(t, client.SAdd(ctx, "sprints", fsSprint).Err())
	fsMust(t, client.ZAdd(ctx, "sprint:order", redis.Z{Score: 1, Member: fsSprint}).Err())
	fsMust(t, client.HSet(ctx, "s:"+fsSprint, "status", "open").Err())
	for _, name := range names {
		fsMust(t, client.SAdd(ctx, "friends", name).Err())
		fsMust(t, client.HSet(ctx, "friend:"+name+":desired", "slots", "8", "machine", "studio", "paused", "0").Err())
		fsMust(t, client.HSet(ctx, "friend:"+name+":beat", "host", "studio", "session", name+"-1", "at", "1").Err())
		var roles []string
		for _, reader := range fsRoster.MayHold {
			if reader == name {
				roles = append(roles, "may-hold")
			}
		}
		for _, builder := range fsRoster.Builders {
			if builder == name {
				roles = append(roles, "builder")
			}
		}
		if fsRoster.Coordinator == name {
			roles = append(roles, "coordinator")
		}
		fsMust(t, client.HSet(ctx, "friend:"+name+":roles", "roles", strings.Join(roles, ",")).Err())
	}
}

// fsPush puts one task on f's queue; pr 0 is a build or fix with no PR.
func fsPush(t *testing.T, st *store.Store, f, id string, kind task.Kind, pr int, head string) {
	t.Helper()
	req := task.PushRequest{Sprint: fsSprint, ID: id, Kind: kind, Title: id + " title", To: f, Actor: "test"}
	if pr != 0 {
		req.Repo, req.PR, req.Head = fsRepo, pr, head
		req.Ref = "https://example.test/mas-bandwidth/nova-tools/pull/" + strconv.Itoa(pr)
	}
	status, err := task.Push(context.Background(), st, req)
	fsMust(t, err)
	if status != task.PushCreated {
		t.Fatalf("push %s: %s", id, status)
	}
}

func fsOpen(client *redis.Client, f string) int64 {
	return client.ZCard(context.Background(), "s:"+fsSprint+":open:"+f).Val()
}

func fsLeased(client *redis.Client, f string) int64 {
	ctx := context.Background()
	return client.ZCard(ctx, "friend:"+f+":starting").Val() + client.ZCard(ctx, "friend:"+f+":living").Val()
}

func fsQueueOf(client *redis.Client, id string, names ...string) string {
	for _, name := range names {
		if _, err := client.ZScore(context.Background(), "s:"+fsSprint+":open:"+name, id).Result(); err == nil {
			return name
		}
	}
	return ""
}

// fsOutbox counts friend:outbox entries of one kind for one friend.
func fsOutbox(t *testing.T, client *redis.Client, f, kind string) []map[string]any {
	t.Helper()
	entries, err := client.XRange(context.Background(), friend.OutboxKey, "-", "+").Result()
	if err != nil {
		t.Fatal(err)
	}
	var out []map[string]any
	for _, e := range entries {
		if e.Values["friend"] == f && e.Values["kind"] == kind {
			out = append(out, e.Values)
		}
	}
	return out
}

func fsState(client *redis.Client, f string) map[string]string {
	return client.HGetAll(context.Background(), "friend:"+f+":state").Val()
}

// TestControl43 (#2756 v6 control 43 through #3101's verbs): a fixture friend
// with 5 open tasks and 2 leases reports out-of-credits with `friend report`;
// one sweep later it has 0 open and 0 leased, reads sit on non-author UP
// readers with free width, builds on another builder, every title carries
// the marker and the carried-hold re-read is one release task.
func TestControl43(t *testing.T) {
	t.Parallel()

	st, client := fsRedis(t)
	ctx := context.Background()
	fsFriends(t, client, "emma", "stella", "johnny", "rowan")
	prs := map[int][2]string{101: {fsHead('a'), "johnny"}, 102: {fsHead('b'), "stella"}, 103: {fsHead('d'), "rowan"}}
	for n, pr := range prs {
		fsMust(t, client.HSet(ctx, "s:"+fsSprint+":pr:"+fsRepo+":"+strconv.Itoa(n), "head", pr[0], "author", pr[1]).Err())
	}
	fsMust(t, client.HSet(ctx, "s:"+fsSprint+":disp:"+fsRepo+":103", "emma@"+fsHead('c'), "HOLD 4 hold at c").Err())
	fsPush(t, st, "emma", "read-a", task.KindRead, 101, prs[101][0])
	fsPush(t, st, "emma", "read-b", task.KindRead, 102, prs[102][0])
	fsPush(t, st, "emma", "build-1", task.KindWork, 0, "")
	fsPush(t, st, "emma", "fix-1", task.KindFix, 0, "")
	fsPush(t, st, "emma", "reread-c", task.KindRead, 103, prs[103][0])
	fsPush(t, st, "emma", "build-2", task.KindWork, 0, "")
	fsPush(t, st, "emma", "build-3", task.KindWork, 0, "")
	for _, id := range []string{"build-2", "build-3"} {
		if _, ok, err := task.Take(ctx, st, task.TakeRequest{Sprint: fsSprint, ID: id, As: "emma", Actor: "emma"}); err != nil || !ok {
			t.Fatalf("take %s: %v %v", id, ok, err)
		}
	}
	if fsOpen(client, "emma") != 5 || fsLeased(client, "emma") != 2 {
		t.Fatalf("fixture: open %d leased %d", fsOpen(client, "emma"), fsLeased(client, "emma"))
	}

	until := time.Now().Add(3 * time.Hour).Truncate(time.Millisecond)
	fsMust(t, friend.Report(ctx, st, friend.ReportRequest{Friend: "emma", State: friend.StateOutOfCredits,
		Until: until, Reason: "usage limit", Actor: "emma-keeper"}))
	state := fsState(client, "emma")
	if state["state"] != friend.StateOutOfCredits || state["until"] != strconv.FormatInt(until.UnixMilli(), 10) || state["rung"] != "0" {
		t.Fatalf("state after report: %v", state)
	}

	ladder := &friend.Ladder{Store: st, Roster: fsRoster, Actor: "reconciler"}
	res, err := ladder.Sweep(ctx)
	fsMust(t, err)
	var move *life.Move
	for i := range res.Moves {
		if res.Moves[i].Friend == "emma" {
			move = &res.Moves[i]
		}
	}
	if move == nil || move.Moved != 6 || move.Leases != 2 || move.Released != 1 || move.Unrouted != 0 {
		t.Fatalf("one sweep moved %+v", res.Moves)
	}
	if fsOpen(client, "emma") != 0 || fsLeased(client, "emma") != 0 {
		t.Fatalf("after one sweep: open %d leased %d", fsOpen(client, "emma"), fsLeased(client, "emma"))
	}
	marker := "[moved from emma: out of credits]"
	authors := map[string]string{"read-a": "johnny", "read-b": "stella"}
	for id, author := range authors {
		owner := fsQueueOf(client, id, "stella", "johnny", "rowan")
		if owner != "stella" && owner != "johnny" || owner == author {
			t.Fatalf("%s on %q (author %s)", id, owner, author)
		}
	}
	for _, id := range []string{"build-1", "fix-1", "build-2", "build-3"} {
		if owner := fsQueueOf(client, id, "stella", "johnny", "rowan"); owner != "johnny" {
			t.Fatalf("%s on %q, want the other builder johnny", id, owner)
		}
	}
	for _, id := range []string{"read-a", "read-b", "build-1", "fix-1", "build-2", "build-3"} {
		if title := client.HGet(ctx, "task:"+id, "title").Val(); !strings.Contains(title, marker) {
			t.Fatalf("%s title %q lacks %q", id, title, marker)
		}
	}
	if got := client.HGet(ctx, "task:reread-c", "state").Val(); got != "cancelled" {
		t.Fatalf("reread-c %q, want cancelled", got)
	}
	if keys := client.Keys(ctx, "task:release-*").Val(); len(keys) != 1 {
		t.Fatalf("release tasks %v, want exactly one", keys)
	}

	// friend show prints the state, the reset and the counts from the store.
	rows, err := friend.Show(ctx, st, "emma")
	fsMust(t, err)
	if len(rows) != 1 {
		t.Fatalf("show rows %v", rows)
	}
	line := rows[0].Line()
	for _, want := range []string{"friend=emma", "state=out-of-credits", "until=", "open=0", "living=0", "wake=none"} {
		if !strings.Contains(line, want) {
			t.Fatalf("show line %q lacks %q", line, want)
		}
	}
	t.Log(line)
}

// TestControl43OneWriter: each friend key has one writer. friend:<f>:state
// is written only in friend.lua (ns_friend_state and its helpers),
// friend:<f>:wakemode only in friend.lua (ns_friend_wakemode) and
// friend:<f>:events only in presence.lua (ns_friend_event); no Go source
// writes any of the three; every XADD to the events stream is capped.
func TestControl43OneWriter(t *testing.T) {
	t.Parallel()

	source, err := fn.Source()
	fsMust(t, err)
	sections := strings.Split(source, "\n-- lua/")
	t.Run("lua", func(t *testing.T) {
		writes := regexp.MustCompile(`redis\.call\('(HSET|HSETNX|HDEL|DEL|UNLINK|HINCRBY|HINCRBYFLOAT|EXPIRE|PEXPIRE|SET|RENAME|HMSET|XADD|XTRIM|XDEL)',\s*([^,)]+)`)
		owner := map[string]string{"state": "friend.lua", "wakemode": "friend.lua", "events": "presence.lua"}
		binds := regexp.MustCompile(`local\s+(\w+)\s*=\s*(?:'friend:'\s*\.\.\s*\w+\s*\.\.\s*':(state|wakemode|events)'|(fs_key)\()`)
		checked := map[string]int{}
		for _, sec := range sections[1:] {
			name := sec[:strings.Index(sec, "\n")]
			// A local bound to a key is tracked within its top-level function.
			vars := map[string]string{}
			for i, line := range strings.Split(sec, "\n") {
				if strings.HasPrefix(line, "local function ") || strings.HasPrefix(line, "function ") {
					vars = map[string]string{}
				}
				if m := binds.FindStringSubmatch(line); m != nil {
					if m[3] != "" {
						vars[m[1]] = "state"
					} else {
						vars[m[1]] = m[2]
					}
				}
				m := writes.FindStringSubmatch(line)
				if m == nil {
					continue
				}
				target := strings.TrimSpace(m[2])
				key := vars[target]
				for k := range owner {
					if strings.Contains(target, "'friend:'") && strings.Contains(target, "':"+k+"'") {
						key = k
					}
				}
				if key == "" {
					continue
				}
				checked[key]++
				if name != owner[key] {
					t.Errorf("lua/%s line %d writes friend:<f>:%s (%s) outside %s", name, i, key, strings.TrimSpace(line), owner[key])
				}
			}
		}
		for k := range owner {
			if checked[k] == 0 {
				t.Errorf("found no write to friend:<f>:%s at all; the rule is reading the wrong source", k)
			}
		}
	})
	t.Run("go_sources", func(t *testing.T) {
		root := fsRepoRoot(t)
		call := regexp.MustCompile(`\.(HSet|HSetNX|HDel|Del|Unlink|Set|Expire|PExpire|XAdd)\(`)
		marker := regexp.MustCompile(`(^|\W)(friend\.)?(StateKey|EventsKey|WakeModeKey)\(|:(state|events|wakemode)"`)
		scanned := 0
		for _, dir := range []string{"internal/nsprint", "cmd/nova-sprint"} {
			err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d fs.DirEntry, err error) error {
				if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
					return err
				}
				body, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				scanned++
				text := string(body)
				for _, loc := range call.FindAllStringIndex(text, -1) {
					args := fsCallArgs(text[loc[1]:])
					if marker.MatchString(args) {
						rel, _ := filepath.Rel(root, path)
						t.Errorf("%s writes a one-writer friend key from Go: %s%s)", rel, text[loc[0]:loc[1]], args)
					}
				}
				return nil
			})
			fsMust(t, err)
		}
		if scanned == 0 {
			t.Fatal("scanned no Go source; the rule is reading the wrong tree")
		}
	})
	t.Run("events_maxlen", func(t *testing.T) {
		xadd := regexp.MustCompile(`redis\.call\('XADD',\s*'friend:'\s*\.\.\s*\w+\s*\.\.\s*':events'(.*)`)
		found := 0
		for _, sec := range sections[1:] {
			name := sec[:strings.Index(sec, "\n")]
			for i, line := range strings.Split(sec, "\n") {
				m := xadd.FindStringSubmatch(line)
				if m == nil {
					continue
				}
				found++
				if !strings.Contains(m[1], "'MAXLEN', '~', 100000") {
					t.Errorf("lua/%s line %d appends to friend:<f>:events without MAXLEN ~ 100000: %s", name, i, strings.TrimSpace(line))
				}
			}
		}
		if found == 0 {
			t.Fatal("found no XADD to friend:<f>:events; the rule is reading the wrong source")
		}
	})
}

// fsCallArgs is the argument text of a call whose "(" was just consumed, up
// to its matching ")".
func fsCallArgs(rest string) string {
	depth := 1
	for i, r := range rest {
		switch r {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return rest[:i]
			}
		}
	}
	return rest
}

// fsRepoRoot is the module root (the directory holding go.mod).
func fsRepoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	fsMust(t, err)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod above the test directory")
		}
		dir = parent
	}
}

// fsIdleFixture: johnny is UP with 8 slots, living 0 and three open tasks (an
// idle friend); stella and rowan can receive his work.
func fsIdleFixture(t *testing.T, st *store.Store, client *redis.Client, f string) {
	t.Helper()
	fsFriends(t, client, f, "stella", "johnny", "rowan")
	for _, id := range []string{"build-a", "build-b", "build-c"} {
		fsPush(t, st, f, id, task.KindWork, 0, "")
	}
}

// fsSweepTo runs sweeps until f's rung reaches want, failing after limit.
func fsSweepTo(t *testing.T, l *friend.Ladder, client *redis.Client, f string, want, limit int) int {
	t.Helper()
	for n := 1; n <= limit; n++ {
		if _, err := l.Sweep(context.Background()); err != nil {
			t.Fatal(err)
		}
		if fsState(client, f)["rung"] == strconv.Itoa(want) {
			return n
		}
	}
	t.Fatalf("%s did not reach rung %d in %d sweeps: %v", f, want, limit, fsState(client, f))
	return 0
}

type fsRepairer struct{ calls []string }

func (r *fsRepairer) Repair(_ context.Context, f string, wp friend.WakePath) (string, error) {
	r.calls = append(r.calls, f+" "+wp.String())
	return "repaired " + wp.Unit, nil
}

// TestControl45 (#2756 v6 control 45): a friend with a unit wake path, held
// idle: rung 1 nudges, rung 2 repairs that unit and sends one wake, rung 3
// redistributes. A take at rung 2 stops the ladder. A human friend's rung 2
// sends one notice to its channel. No key or row ever says asleep.
func TestControl45(t *testing.T) {
	t.Parallel()

	policy := friend.Policy{IdleTicks: 3, UnderfullTicks: 3}

	t.Run("unit", func(t *testing.T) {
		st, client := fsRedis(t)
		ctx := context.Background()
		fsIdleFixture(t, st, client, "kim")
		wp, err := friend.ParseWakePath("unit:com.nova.loop.wake-serve-kim@studio", "")
		fsMust(t, err)
		fsMust(t, friend.SetWakePath(ctx, st, "kim", wp, "config", ""))
		repair := &fsRepairer{}
		l := &friend.Ladder{Store: st, Roster: fsRoster, Actor: "reconciler", Policy: policy, Repair: repair}

		if n := fsSweepTo(t, l, client, "kim", 1, 10); n != 3 {
			t.Fatalf("rung 1 after %d sweeps, want 3 (idle_ticks)", n)
		}
		if got := fsState(client, "kim"); got["state"] != friend.StateIdle {
			t.Fatalf("state %v", got)
		}
		if len(fsOutbox(t, client, "kim", "nudge")) != 1 || len(fsOutbox(t, client, "kim", "wake")) != 0 {
			t.Fatal("rung 1 is one nudge and no wake")
		}
		fsSweepTo(t, l, client, "kim", 2, 3)
		if len(fsOutbox(t, client, "kim", "wake")) != 1 || client.LLen(ctx, "friend:kim:wake").Val() != 1 {
			t.Fatal("rung 2 sends one wake on the wake key")
		}
		if len(repair.calls) != 1 || repair.calls[0] != "kim unit:com.nova.loop.wake-serve-kim@studio" {
			t.Fatalf("repair calls %v, want one for that unit", repair.calls)
		}
		if fsOpen(client, "kim") != 3 {
			t.Fatal("nothing moves before rung 3")
		}
		fsSweepTo(t, l, client, "kim", 3, 3)
		if fsOpen(client, "kim") != 0 {
			t.Fatalf("rung 3 left %d open on kim", fsOpen(client, "kim"))
		}
		for _, id := range []string{"build-a", "build-b", "build-c"} {
			if owner := fsQueueOf(client, id, "stella", "johnny", "rowan"); owner != "johnny" {
				t.Fatalf("%s on %q, want the builder johnny", id, owner)
			}
			if title := client.HGet(ctx, "task:"+id, "title").Val(); !strings.Contains(title, "[moved from kim: idle]") {
				t.Fatalf("title %q", title)
			}
		}
		if len(fsOutbox(t, client, "kim", "wake")) != 1 || len(repair.calls) != 1 {
			t.Fatal("rung 3 sent a second wake")
		}
		// With nothing open, the next sweep clears the ladder: the friend is up.
		if _, err := l.Sweep(ctx); err != nil {
			t.Fatal(err)
		}
		if client.Exists(ctx, "friend:kim:state").Val() != 0 {
			t.Fatalf("state survived an empty queue: %v", fsState(client, "kim"))
		}
		fsNoAsleep(t, st, client)
	})

	t.Run("take at rung 2 stops the ladder", func(t *testing.T) {
		st, client := fsRedis(t)
		ctx := context.Background()
		fsIdleFixture(t, st, client, "kim")
		wp, err := friend.ParseWakePath("unit:com.nova.loop.wake-serve-kim@studio", "")
		fsMust(t, err)
		fsMust(t, friend.SetWakePath(ctx, st, "kim", wp, "config", ""))
		l := &friend.Ladder{Store: st, Roster: fsRoster, Actor: "reconciler", Policy: policy}
		fsSweepTo(t, l, client, "kim", 2, 10)
		if _, ok, err := task.Take(ctx, st, task.TakeRequest{Sprint: fsSprint, ID: "build-a", As: "kim", Actor: "kim"}); err != nil || !ok {
			t.Fatalf("take: %v %v", ok, err)
		}
		if _, err := l.Sweep(ctx); err != nil {
			t.Fatal(err)
		}
		if got := fsState(client, "kim"); got["rung"] != "0" {
			t.Fatalf("a take at rung 2 left %v, want rung 0", got)
		}
		for i := 0; i < 20; i++ {
			if _, err := l.Sweep(ctx); err != nil {
				t.Fatal(err)
			}
			if got := fsState(client, "kim"); got["rung"] == "3" {
				t.Fatalf("rung 3 with living > 0: %v", got)
			}
		}
		if fsOpen(client, "kim") != 2 || fsLeased(client, "kim") != 1 {
			t.Fatalf("work moved off a friend with a live child: open %d leased %d", fsOpen(client, "kim"), fsLeased(client, "kim"))
		}
	})

	t.Run("human", func(t *testing.T) {
		st, client := fsRedis(t)
		ctx := context.Background()
		fsIdleFixture(t, st, client, "glenn")
		wp, err := friend.ParseWakePath("human", "bus:To:Glenn")
		fsMust(t, err)
		fsMust(t, friend.SetWakePath(ctx, st, "glenn", wp, "config", ""))
		l := &friend.Ladder{Store: st, Roster: fsRoster, Actor: "reconciler", Policy: policy, Repair: &fsRepairer{}}
		fsSweepTo(t, l, client, "glenn", 2, 10)
		// Stay at rung 2 for the rest of its window: still one notice.
		for i := 0; i < 2; i++ {
			if _, err := l.Sweep(ctx); err != nil {
				t.Fatal(err)
			}
		}
		notices := fsOutbox(t, client, "glenn", "notice")
		if len(notices) != 1 || notices[0]["channel"] != "bus:To:Glenn" || notices[0]["open"] != "3" {
			t.Fatalf("notices %v, want one to bus:To:Glenn with open=3", notices)
		}
		if client.Exists(ctx, "friend:glenn:wake").Val() != 0 || len(fsOutbox(t, client, "glenn", "wake")) != 0 {
			t.Fatal("a human wake path got a unit wake")
		}
		rows, err := friend.Show(ctx, st, "glenn")
		fsMust(t, err)
		if line := rows[0].Line(); !strings.Contains(line, "waiting on human since") || !strings.Contains(line, "wake=human:bus:To:Glenn") {
			t.Fatalf("show line %q", line)
		}
		fsNoAsleep(t, st, client)
	})
}

// fsNoAsleep: no key value and no show line says asleep.
func fsNoAsleep(t *testing.T, st *store.Store, client *redis.Client) {
	t.Helper()
	ctx := context.Background()
	for _, key := range client.Keys(ctx, "friend:*").Val() {
		if client.Type(ctx, key).Val() != "hash" {
			continue
		}
		for field, value := range client.HGetAll(ctx, key).Val() {
			if strings.Contains(field+value, "asleep") {
				t.Fatalf("%s %s=%s says asleep", key, field, value)
			}
		}
	}
	rows, err := friend.Show(ctx, st, "")
	fsMust(t, err)
	for _, r := range rows {
		if strings.Contains(r.Line(), "asleep") {
			t.Fatalf("show says asleep: %s", r.Line())
		}
	}
}

// TestLadderSurvivesRestart (Stella's HOLD7 on #3058: a process-local tick
// resets on restart): kill the reconciler at rung 2 and start a new one; the
// next sweep continues at rung 2 (not 0) with no duplicate wake, and rung 3
// comes on the same sweep count it would have without the restart. A replay
// of one sweep's call (a lost response) does not advance twice.
func TestLadderSurvivesRestart(t *testing.T) {
	t.Parallel()

	st, client := fsRedis(t)
	ctx := context.Background()
	fsIdleFixture(t, st, client, "kim")
	wp, err := friend.ParseWakePath("unit:com.nova.loop.wake-serve-kim@studio", "")
	fsMust(t, err)
	fsMust(t, friend.SetWakePath(ctx, st, "kim", wp, "config", ""))
	policy := friend.Policy{IdleTicks: 3, UnderfullTicks: 3}

	first := &friend.Ladder{Store: st, Roster: fsRoster, Actor: "reconciler", Policy: policy}
	if n := fsSweepTo(t, first, client, "kim", 2, 10); n != 6 {
		t.Fatalf("rung 2 after %d sweeps, want 6", n)
	}
	since := fsState(client, "kim")["since"]
	first = nil // the reconciler is killed; nothing of it survives but the store

	second := &friend.Ladder{Store: st, Roster: fsRoster, Actor: "reconciler", Policy: policy}
	if _, err := second.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	got := fsState(client, "kim")
	if got["rung"] != "2" || got["state"] != friend.StateIdle || got["since"] != since {
		t.Fatalf("after restart %v, want idle rung 2 since %s", got, since)
	}
	if len(fsOutbox(t, client, "kim", "wake")) != 1 || client.LLen(ctx, "friend:kim:wake").Val() != 1 ||
		len(fsOutbox(t, client, "kim", "nudge")) != 1 {
		t.Fatal("the restarted reconciler repeated a rung action")
	}

	// A replayed call (same idem) is answered from the record, not applied.
	before := fsState(client, "kim")["ticks"]
	for i := 0; i < 2; i++ {
		if _, err := friend.Observe(ctx, st, friend.Observation{Friend: "kim", State: friend.StateIdle, Open: 3,
			Ticks: policy.IdleTicks}, "reconciler", "replay-1"); err != nil {
			t.Fatal(err)
		}
	}
	if after := fsState(client, "kim")["ticks"]; after == before {
		t.Fatalf("the first call did not count: ticks %s", after)
	} else if n, _ := strconv.Atoi(after); n != mustAtoi(t, before)+1 {
		t.Fatalf("a replayed idem counted twice: ticks %s -> %s", before, after)
	}

	// The next sweep reaches rung 3: 6 + 1 + 1 (replay) + 1 = 9 = 3 * idle_ticks.
	if _, err := second.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	if got := fsState(client, "kim"); got["rung"] != "3" && client.Exists(ctx, "friend:kim:state").Val() != 0 {
		t.Fatalf("rung 3 did not come on schedule: %v", got)
	}
	if fsOpen(client, "kim") != 0 {
		t.Fatalf("rung 3 left %d open", fsOpen(client, "kim"))
	}
	if len(fsOutbox(t, client, "kim", "wake")) != 1 {
		t.Fatal("a second wake after restart")
	}
}

func mustAtoi(t *testing.T, s string) int {
	t.Helper()
	n, err := strconv.Atoi(s)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// fsFCall runs one Function and returns its reply as strings; a Redis error
// is fatal.
func fsFCall(t *testing.T, client *redis.Client, fn string, args ...any) []string {
	t.Helper()
	reply, err := client.FCall(context.Background(), fn, nil, args...).Slice()
	if err != nil {
		t.Fatalf("%s %v: %v", fn, args, err)
	}
	out := make([]string, len(reply))
	for i, v := range reply {
		out[i] = fmt.Sprint(v)
	}
	return out
}

// fsEvent appends one event through ns_friend_event as f itself.
func fsEvent(t *testing.T, client *redis.Client, f, kind, cause string, at int64) string {
	t.Helper()
	got := fsFCall(t, client, life.FunctionEvent, f, kind, cause, strconv.FormatInt(at, 10), f)
	if len(got) != 2 || got[0] != "OK" {
		t.Fatalf("event %s %s: %v", f, kind, got)
	}
	return got[1]
}

func fsCapLog(t *testing.T, client *redis.Client) []redis.XMessage {
	t.Helper()
	msgs, err := client.XRange(context.Background(), "cap:log", "-", "+").Result()
	fsMust(t, err)
	return msgs
}

// TestFriendStateAcceptsLifeStates: ns_friend_state accepts offline-model and
// wake-missed on the report branch, still refuses an unknown state, and
// both hold against the ladder's idle observation.
func TestFriendStateAcceptsLifeStates(t *testing.T) {
	t.Parallel()

	st, client := fsRedis(t)
	ctx := context.Background()
	fsFriends(t, client, "ada", "stella", "johnny", "rowan")
	for _, state := range []string{friend.StateOfflineModel, friend.StateWakeMissed} {
		got := fsFCall(t, client, friend.FunctionState, "ada", state, "", "test", "keeper", "i-"+state)
		if got[0] != "OK" || got[1] != state {
			t.Fatalf("%s: %v", state, got)
		}
		if fsState(client, "ada")["state"] != state {
			t.Fatalf("stored %v, want %s", fsState(client, "ada"), state)
		}
		step, err := friend.Observe(ctx, st, friend.Observation{Friend: "ada", State: friend.StateIdle, Open: 1, Ticks: 1}, "reconciler", "obs-"+state)
		fsMust(t, err)
		if step.Status != "HELD" || step.State != state {
			t.Fatalf("idle observation over %s: %+v, want HELD", state, step)
		}
	}
	if got := fsFCall(t, client, friend.FunctionState, "ada", "sleeping", "", "test", "keeper", "i-sleep"); got[0] != "INVALID" {
		t.Fatalf("sleeping: %v, want INVALID", got)
	}
}

// TestFriendStateIfMatch: a classifier write (actor life) is applied only
// when the stored state and idem still equal what it read, and only over a
// state it may overwrite; otherwise STALE or HELD and nothing is written.
func TestFriendStateIfMatch(t *testing.T) {
	t.Parallel()

	st, client := fsRedis(t)
	ctx := context.Background()
	fsFriends(t, client, "ada", "stella", "johnny", "rowan")
	life1 := "life:ada:e1:offline-model"
	if got := fsFCall(t, client, friend.FunctionState, "ada", friend.StateOfflineModel, "", "r", friend.ActorLife, life1, "up", ""); got[0] != "OK" {
		t.Fatalf("offline-model: %v", got)
	}
	until := time.Now().Add(time.Hour)
	fsMust(t, friend.Report(ctx, st, friend.ReportRequest{Friend: "ada", State: friend.StateAway, Until: until, Actor: "ada", Idem: "away-1"}))
	away := fsState(client, "ada")
	logLen := len(fsCapLog(t, client))
	outLen := client.XLen(ctx, friend.OutboxKey).Val()

	got := fsFCall(t, client, friend.FunctionState, "ada", "clear", "", "r", friend.ActorLife, "life:ada:e2:clear", friend.StateOfflineModel, life1)
	if !reflect.DeepEqual(got, []string{"STALE", friend.StateAway, "away-1"}) {
		t.Fatalf("clear over a newer away: %v", got)
	}
	got = fsFCall(t, client, friend.FunctionState, "ada", "clear", "", "r", friend.ActorLife, "life:ada:e3:clear", friend.StateAway, "away-1")
	if !reflect.DeepEqual(got, []string{"HELD", friend.StateAway}) {
		t.Fatalf("matching clear over a report: %v", got)
	}
	if now := fsState(client, "ada"); !reflect.DeepEqual(now, away) {
		t.Fatalf("away changed: %v -> %v", away, now)
	}
	if len(fsCapLog(t, client)) != logLen {
		t.Fatal("a refused classifier write reached cap:log")
	}

	// Back at the classifier's own offline-model: a matching clear applies.
	fsMust(t, friend.Report(ctx, st, friend.ReportRequest{Friend: "ada", State: "clear", Actor: "ada", Idem: "back-1"}))
	if got := fsFCall(t, client, friend.FunctionState, "ada", friend.StateOfflineModel, "", "r", friend.ActorLife, life1, "up", ""); got[0] != "OK" {
		t.Fatalf("offline-model again: %v", got)
	}
	got = fsFCall(t, client, friend.FunctionState, "ada", "clear", "", "r", friend.ActorLife, "life:ada:e4:clear", friend.StateOfflineModel, life1)
	if got[0] != "OK" || got[1] != "up" || client.Exists(ctx, friend.StateKey("ada")).Val() != 0 {
		t.Fatalf("matching clear of its own state: %v %v", got, fsState(client, "ada"))
	}

	// A write over an away the classifier did not see: STALE, no wake.
	fsMust(t, friend.Report(ctx, st, friend.ReportRequest{Friend: "ada", State: friend.StateAway, Until: until, Actor: "ada", Idem: "away-2"}))
	outLen = client.XLen(ctx, friend.OutboxKey).Val()
	got = fsFCall(t, client, friend.FunctionState, "ada", friend.StateOfflineModel, "", "r", friend.ActorLife, "life:ada:e5:offline-model", "up", "")
	if got[0] != "STALE" || fsState(client, "ada")["state"] != friend.StateAway {
		t.Fatalf("offline-model over an unseen away: %v %v", got, fsState(client, "ada"))
	}
	if client.XLen(ctx, friend.OutboxKey).Val() != outLen {
		t.Fatal("a STALE write appended a wake")
	}
	// The classifier has no unconditional path.
	if got := fsFCall(t, client, friend.FunctionState, "ada", "clear", "", "r", friend.ActorLife, "life:ada:e6:clear"); got[0] != "INVALID" {
		t.Fatalf("actor life without args 7-8: %v, want INVALID", got)
	}
}

func fsMoveOf(moves []life.Move, f string) *life.Move {
	for i := range moves {
		if moves[i].Friend == f {
			return &moves[i]
		}
	}
	return nil
}

// TestWakeMissedRedistributesSameTick: a silent friend (no beat) in
// wake-missed has its open task moved by the next redistribute call, with
// the wake-missed marker.
func TestWakeMissedRedistributesSameTick(t *testing.T) {
	t.Parallel()

	st, client := fsRedis(t)
	ctx := context.Background()
	fsFriends(t, client, "ada", "stella", "johnny", "rowan")
	fsPush(t, st, "ada", "build-1", task.KindWork, 0, "")
	fsMust(t, client.Del(ctx, "friend:ada:beat").Err())
	fsFCall(t, client, friend.FunctionState, "ada", friend.StateWakeMissed, "", "r", friend.ActorLife, "life:ada:d1:wake-missed", "up", "")
	moves, err := life.Redistribute(ctx, st, fsRoster, "reconciler", "t1")
	fsMust(t, err)
	if m := fsMoveOf(moves, "ada"); m == nil || m.Moved != 1 {
		t.Fatalf("moves %+v, want ada's one task moved", moves)
	}
	if fsOpen(client, "ada") != 0 {
		t.Fatal("the task stayed on ada")
	}
	title := client.HGet(ctx, "task:build-1", "title").Val()
	if !strings.Contains(title, "[moved from ada: wake-missed]") {
		t.Fatalf("title %q lacks the wake-missed marker", title)
	}
}

// TestWakeMissedSurvivesBeat: a friend that still beats (shell alive, model
// gone) in wake-missed has its work moved and stays wake-missed; a beat
// never clears it as it clears down.
func TestWakeMissedSurvivesBeat(t *testing.T) {
	t.Parallel()

	st, client := fsRedis(t)
	ctx := context.Background()
	fsFriends(t, client, "ada", "stella", "johnny", "rowan")
	fsPush(t, st, "ada", "build-1", task.KindWork, 0, "")
	fsFCall(t, client, friend.FunctionState, "ada", friend.StateWakeMissed, "", "r", friend.ActorLife, "life:ada:d1:wake-missed", "up", "")
	for tick := 1; tick <= 2; tick++ {
		moves, err := life.Redistribute(ctx, st, fsRoster, "reconciler", "t"+strconv.Itoa(tick))
		fsMust(t, err)
		if tick == 1 {
			if m := fsMoveOf(moves, "ada"); m == nil || m.Moved != 1 {
				t.Fatalf("tick 1 moves %+v", moves)
			}
		}
		if got := fsState(client, "ada")["state"]; got != friend.StateWakeMissed {
			t.Fatalf("tick %d: state %q, want wake-missed", tick, got)
		}
	}
	for _, m := range fsCapLog(t, client) {
		if m.Values["kind"] == "friend-state-clear" && strings.Contains(fmt.Sprint(m.Values["reason"]), "down: beat returned") {
			t.Fatalf("a beat cleared wake-missed: %v", m.Values)
		}
	}
}

// TestFriendEventOneWriter: ns_friend_event appends the six kinds and
// refuses an unknown kind, a cause off turn-start, an unknown friend and an
// actor that is not the friend (shape validation, not authentication).
func TestFriendEventOneWriter(t *testing.T) {
	t.Parallel()

	_, client := fsRedis(t)
	ctx := context.Background()
	fsFriends(t, client, "ada", "bo", "stella", "johnny", "rowan")
	at := int64(1790000000000)
	id := fsEvent(t, client, "ada", life.EventDeliver, "", at)
	entry, err := client.XRange(ctx, friend.EventsKey("ada"), id, id).Result()
	fsMust(t, err)
	if len(entry) != 1 || entry[0].Values["kind"] != "deliver" || entry[0].Values["at"] != strconv.FormatInt(at, 10) {
		t.Fatalf("deliver entry %v", entry)
	}
	tid := fsEvent(t, client, "ada", life.EventTurnStart, id, at+1000)
	entry, err = client.XRange(ctx, friend.EventsKey("ada"), tid, tid).Result()
	fsMust(t, err)
	if len(entry) != 1 || entry[0].Values["cause"] != id {
		t.Fatalf("turn-start entry %v, want cause %s", entry, id)
	}
	before := client.XLen(ctx, friend.EventsKey("ada")).Val()
	for _, bad := range [][]any{
		{"ada", "sleeping", "", at, "ada"},
		{"ada", life.EventBeat, id, at, "ada"},
		{"zed", life.EventBeat, "", at, "zed"},
		{"ada", life.EventBeat, "", at, "bo"},
		{"ada", life.EventDeliver, "", at, "bo"},
	} {
		if got := fsFCall(t, client, life.FunctionEvent, bad...); got[0] != "INVALID" {
			t.Fatalf("%v: %v, want INVALID", bad, got)
		}
	}
	if after := client.XLen(ctx, friend.EventsKey("ada")).Val(); after != before {
		t.Fatalf("a refused event was appended: %d -> %d", before, after)
	}
	if client.Exists(ctx, friend.EventsKey("zed")).Val() != 0 {
		t.Fatal("an unknown friend got a stream")
	}
}

// TestFriendWakeModeRechecksReceipt: ns_friend_wakemode re-reads the pair it
// is given; a late, wrongly caused or wrong-kind pair is REFUSED and writes
// nothing, a real receipt declares scheduled-model-turn.
func TestFriendWakeModeRechecksReceipt(t *testing.T) {
	t.Parallel()

	_, client := fsRedis(t)
	ctx := context.Background()
	fsFriends(t, client, "ada", "stella", "johnny", "rowan")
	at := int64(1790000000000)
	beat := fsEvent(t, client, "ada", life.EventBeat, "", at)
	d := fsEvent(t, client, "ada", life.EventDeliver, "", at+1000)
	other := fsEvent(t, client, "ada", life.EventDeliver, "", at+2000)
	late := fsEvent(t, client, "ada", life.EventTurnStart, d, at+1000+121000)
	wrong := fsEvent(t, client, "ada", life.EventTurnStart, other, at+1000+30000)
	good := fsEvent(t, client, "ada", life.EventTurnStart, d, at+1000+60000)
	for _, pair := range [][2]string{{d, late}, {d, wrong}, {d, beat}, {beat, good}, {d, "1-1"}} {
		if got := fsFCall(t, client, life.FunctionWakeMode, "ada", pair[0], pair[1], "ada", ""); got[0] != "REFUSED" {
			t.Fatalf("%v: %v, want REFUSED", pair, got)
		}
	}
	if client.Exists(ctx, friend.WakeModeKey("ada")).Val() != 0 {
		t.Fatal("a refused pair wrote a wake mode")
	}
	got := fsFCall(t, client, life.FunctionWakeMode, "ada", d, good, "ada", "")
	if !reflect.DeepEqual(got, []string{"OK", life.WakeModeScheduled, "60000"}) {
		t.Fatalf("real receipt: %v", got)
	}
	h := client.HGetAll(ctx, friend.WakeModeKey("ada")).Val()
	if h["mode"] != life.WakeModeScheduled || h["lag_ms"] != "60000" || h["deliver"] != d || h["turn"] != good {
		t.Fatalf("wakemode %v", h)
	}
	n := 0
	for _, m := range fsCapLog(t, client) {
		if m.Values["kind"] == "friend-wakemode" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("%d friend-wakemode receipts, want 1", n)
	}
}
