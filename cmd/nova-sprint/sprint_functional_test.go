//go:build functional

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/redis/go-redis/v9"
)

// sprintRedis starts a throwaway redis-server with the function library
// loaded, the same shape as capacity_test.go.
func sprintRedis(t *testing.T) (string, *redis.Client) {
	t.Helper()
	addr := testutil.Start(t)
	client := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = client.Close() })
	ctx := context.Background()
	if err := fn.Load(ctx, client); err != nil {
		t.Fatal(err)
	}
	t.Setenv(store.UserEnv, "")
	return addr, client
}

// TestSprintOpenLetsTaskTakeClaim is the stage-1 #2939 DONE-WHEN: task take on
// an unopened sprint prints NONE; after sprint open the same take claims; a
// second open resumes with the same counts; close and status round-trip.
func TestSprintOpenLetsTaskTakeClaim(t *testing.T) {
	addr, client := sprintRedis(t)
	ctx := context.Background()
	const s = "control-2939"
	t.Setenv("NOVA_FRIEND", "ctl-open") // #2929: push and take run from a seat
	client.SAdd(ctx, "friends", "ctl-open")
	client.HSet(ctx, "friend:ctl-open:desired", "slots", 2, "paused", "0")
	client.HSet(ctx, "friend:ctl-open:beat", "host", "fixture", "at", strconv.FormatInt(time.Now().UnixMilli(), 10))

	step := func(want string, args ...string) {
		t.Helper()
		code, out, errOut := runSprint(args...)
		if code != 0 || out != want+"\n" {
			t.Fatalf("%s: code=%d out=%q stderr=%q; want 0 %q", strings.Join(args, " "), code, out, errOut, want)
		}
	}
	step("PUSH CREATED id=t1", "task", "push", "--redis", addr, "--sprint", s, "--id", "t1",
		"--title", "first", "--payload-sha", "p1", "--to", "ctl-open")
	step(s+" absent 0/1 0% -> eta ?", "sprint", "status", "--redis", addr, "--sprint", s)
	step("NONE trips=1", "task", "take", "--redis", addr, "--sprint", s, "--as", "ctl-open")

	step("OPEN "+s+" units=0 pushed=0 existed=0 closed=0 skipped_done=0", "sprint", "open", "--redis", addr, "--sprint", s)
	step("OPEN "+s+" units=0 pushed=0 existed=0 closed=0 skipped_done=0", "sprint", "open", "--redis", addr, "--sprint", s)
	if st, _ := client.HGet(ctx, "s:"+s, "status").Result(); st != "open" {
		t.Fatalf("s:%s status=%q want open", s, st)
	}
	if ok, _ := client.SIsMember(ctx, "sprints", s).Result(); !ok {
		t.Fatalf("sprints does not hold %s after open", s)
	}
	if n, _ := client.ZCard(ctx, "sprint:order").Result(); n != 1 {
		t.Fatalf("sprint:order has %d members after two opens, want 1", n)
	}
	step(s+" open 0/1 0% -> eta ?", "sprint", "status", "--redis", addr, "--sprint", s)

	code, out, errOut := runSprint("task", "take", "--redis", addr, "--as", "ctl-open")
	// #3261: the take names its round trips, one pipeline and one FCALL.
	if code != 0 || !strings.HasPrefix(out, "CLAIMED "+s+"/t1 attempt=1 ") || !strings.Contains(out, " trips=2\n") {
		t.Fatalf("take after open: code=%d out=%q stderr=%q; want CLAIMED %s/t1", code, out, errOut, s)
	}

	step(s+" status=closed", "sprint", "close", "--redis", addr, "--sprint", s)
	// #3571: a second close is refused, so status=closed exit 0 means open.
	if code, out, errOut := runSprint("sprint", "close", "--redis", addr, "--sprint", s); code != 1 ||
		!strings.HasPrefix(out, "REFUSED "+s+" already closed (closed_at ") {
		t.Fatalf("second close: code=%d out=%q stderr=%q; want exit 1 REFUSED already closed", code, out, errOut)
	}
	if ok, _ := client.SIsMember(ctx, "sprints", s).Result(); ok {
		t.Fatalf("sprints still holds %s after close", s)
	}
	step(s+" closed 0/1 0% -> eta ?", "sprint", "status", "--redis", addr, "--sprint", s)
}

const (
	fxFriend = "stella"
	fxNow    = "1790175600" // 2026-09-23 15:00 UTC
	fxLater  = "1790179200" // one hour on
)

// openFixture is the loopback redis with friend f (stella) registered in
// friends with a beat and two slots, seated as NOVA_FRIEND, and the gate
// seam GREEN.
func openFixture(t *testing.T) (string, *redis.Client) {
	t.Helper()
	addr, c := sprintRedis(t)
	registerFriend(t, c, fxFriend, 2)
	// #2929 rev 6: task push and take run from the seat, and take --as must
	// equal it, so the fixture's seat is the friend the units name.
	t.Setenv("NOVA_FRIEND", fxFriend)
	setGate(t, nil)
	return addr, c
}

func registerFriend(t *testing.T, c *redis.Client, name string, slots int) {
	t.Helper()
	ctx := context.Background()
	for _, err := range []error{
		c.SAdd(ctx, "friends", name).Err(),
		c.HSet(ctx, "friend:"+name+":desired", "slots", slots, "paused", "0").Err(),
		c.HSet(ctx, "friend:"+name+":beat", "host", "fixture", "at", strconv.FormatInt(time.Now().UnixMilli(), 10)).Err(),
	} {
		if err != nil {
			t.Fatal(err)
		}
	}
}

// setGate swaps the gate seam for one test; nil is GREEN.
func setGate(t *testing.T, g func(ctx context.Context, name string) (bool, string)) {
	t.Helper()
	old := sprintGate
	sprintGate = g
	t.Cleanup(func() { sprintGate = old })
}

// pushKill is a go-redis hook that fails every ns_task_push after the first
// `after` ones before it reaches the server: a killed open.
type pushKill struct{ after, seen int }

func (h *pushKill) DialHook(next redis.DialHook) redis.DialHook { return next }

func (h *pushKill) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}

func (h *pushKill) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		args := cmd.Args()
		if strings.EqualFold(cmd.Name(), "fcall") && len(args) > 1 && fmt.Sprint(args[1]) == "ns_task_push" {
			h.seen++
			if h.seen > h.after {
				err := errors.New("push hook: killed")
				cmd.SetErr(err)
				return err
			}
		}
		return next(ctx, cmd)
	}
}

// killPushesAfter makes the verb's next store fail every push after n.
func killPushesAfter(t *testing.T, n int) {
	t.Helper()
	old := sprintStoreOpen
	sprintStoreOpen = func(ctx context.Context, addr string) (*store.Store, error) {
		st, err := store.Open(ctx, addr)
		if err == nil {
			st.Client().AddHook(&pushKill{after: n})
		}
		return st, err
	}
	t.Cleanup(func() { sprintStoreOpen = old })
}

func healPushes(t *testing.T) {
	t.Helper()
	sprintStoreOpen = store.Open
}

// unit is one (unit ...) form owned by owner with a title and a done-when;
// extra is appended verbatim.
func unit(id, owner, extra string) string {
	return fmt.Sprintf(`(unit %q :kind work :owner %q :title "%s title" :done-when "%s passes" %s)`,
		id, owner, id, id, extra)
}

func writeWorkSet(t *testing.T, name string, units ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name+".lisp")
	body := fmt.Sprintf("(work-set %q :title \"fixture\" :units (\n  %s))\n", name, strings.Join(units, "\n  "))
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func fileSHA(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func dbsize(t *testing.T, c *redis.Client) int64 {
	t.Helper()
	n, err := c.DBSize(context.Background()).Result()
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func openArgs(addr, s, from string) []string {
	args := []string{"sprint", "open", "--redis", addr, "--sprint", s, "--now", fxNow}
	if from != "" {
		args = append(args, "--from", from)
	}
	return args
}

// expect runs the verb and requires the exit code and the exact stdout.
func expect(t *testing.T, code int, stdout string, args ...string) string {
	t.Helper()
	got, out, errOut := runSprint(args...)
	if got != code || out != stdout {
		t.Fatalf("%s:\n code=%d out=%q stderr=%q\n want %d %q", strings.Join(args, " "), got, out, errOut, code, stdout)
	}
	return errOut
}

// takeOne is f's take of one task; it returns stdout.
func takeOne(t *testing.T, addr string, extra ...string) string {
	t.Helper()
	args := append([]string{"task", "take", "--redis", addr, "--as", fxFriend, "--n", "1"}, extra...)
	code, out, errOut := runSprint(args...)
	if code != 0 {
		t.Fatalf("take: code=%d out=%q stderr=%q", code, out, errOut)
	}
	return out
}

func hget(t *testing.T, c *redis.Client, key, field string) string {
	t.Helper()
	return c.HGet(context.Background(), key, field).Val()
}

func members(t *testing.T, c *redis.Client, key string) string {
	t.Helper()
	m, err := c.SMembers(context.Background(), key).Result()
	if err != nil {
		t.Fatal(err)
	}
	return strings.Join(sortStrings(m), ",")
}

func sortStrings(s []string) []string {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
	return s
}

func exists(t *testing.T, c *redis.Client, key string) bool {
	t.Helper()
	return c.Exists(context.Background(), key).Val() == 1
}

func pushEvents(t *testing.T, c *redis.Client, s string) int {
	t.Helper()
	msgs, err := c.XRange(context.Background(), "s:"+s+":log", "-", "+").Result()
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, m := range msgs {
		if m.Values["kind"] == "task push" {
			n++
		}
	}
	return n
}

// TestControl19 is #2939 control 19: content and owner checks write nothing.
func TestControl19(t *testing.T) {
	addr, c := openFixture(t)
	const s = "control-19"
	refused := func(name, want string, units ...string) {
		t.Helper()
		before := dbsize(t, c)
		from := writeWorkSet(t, name, units...)
		code, out, errOut := runSprint(openArgs(addr, s, from)...)
		if code != 1 {
			t.Fatalf("%s: code=%d out=%q stderr=%q; want exit 1", name, code, out, errOut)
		}
		if want != "" && out != want+"\n" {
			t.Fatalf("%s: out=%q; want exactly %q", name, out, want)
		}
		if after := dbsize(t, c); after != before {
			t.Fatalf("%s: DBSIZE %d -> %d; want nothing written", name, before, after)
		}
		if exists(t, c, "s:"+s) {
			t.Fatalf("%s: s:%s written", name, s)
		}
		if strings.Count(out, "\n") != 1 {
			t.Fatalf("%s: out=%q; want one finding line", name, out)
		}
	}
	refused("no-done-when", "", unit("u1", fxFriend, ""),
		`(unit "u2" :kind work :owner "stella" :title "u2 title")`)
	refused("no-owner", "", `(unit "u1" :kind work :title "u1 title" :done-when "u1 passes")`)
	refused("empty-owner", "", `(unit "u1" :kind work :owner "" :title "u1 title" :done-when "u1 passes")`)
	refused("owner-all", "", unit("u1", "all", ""))
	for _, name := range []string{"no-owner", "empty-owner", "owner-all"} {
		_ = name
	}
	refused("owner-emma", "OWNER u1 Emma not in friends", unit("u1", "Emma", ""))
	refused("owner-child", "OWNER u1 child:opus not in friends", unit("u1", "child:opus", ""))
	refused("kind-read", "KIND u1 read: open pushes no read or review task",
		`(unit "u1" :kind read :owner "stella" :title "u1 title" :done-when "u1 passes")`)

	// Each owner finding names its unit.
	for _, tc := range []struct{ name, unit string }{
		{"no-owner", `(unit "u7" :kind work :title "u7 title" :done-when "u7 passes")`},
		{"empty-owner", `(unit "u7" :kind work :owner "" :title "u7 title" :done-when "u7 passes")`},
		{"owner-all", unit("u7", "all", "")},
	} {
		_, out, _ := runSprint(openArgs(addr, s, writeWorkSet(t, tc.name+"-7", tc.unit))...)
		if !strings.Contains(out, "u7") {
			t.Fatalf("%s: out=%q does not name the unit", tc.name, out)
		}
	}

	// The gate seam RED refuses a clean set with its reason on stderr.
	setGate(t, func(ctx context.Context, name string) (bool, string) {
		return false, "LINEUP RED fixture: probe card not landed"
	})
	before := dbsize(t, c)
	code, out, errOut := runSprint(openArgs(addr, s, writeWorkSet(t, "clean", unit("u1", fxFriend, "")))...)
	if code != 1 || out != "" || !strings.Contains(errOut, "LINEUP RED fixture: probe card not landed") {
		t.Fatalf("gate RED: code=%d out=%q stderr=%q; want exit 1 and the seam's reason on stderr", code, out, errOut)
	}
	if after := dbsize(t, c); after != before {
		t.Fatalf("gate RED: DBSIZE %d -> %d; want nothing written", before, after)
	}
}

// TestControl20 is #2939 control 20: a killed open resumes idempotently.
func TestControl20(t *testing.T) {
	addr, c := openFixture(t)
	ctx := context.Background()
	const s = "control-20"
	from := writeWorkSet(t, "five", unit("u1", fxFriend, ""), unit("u2", fxFriend, ""),
		unit("u3", fxFriend, ""), unit("u4", fxFriend, ""), unit("u5", fxFriend, ""))

	killPushesAfter(t, 2)
	if code, out, errOut := runSprint(openArgs(addr, s, from)...); code == 0 {
		t.Fatalf("killed open: code=0 out=%q stderr=%q; want a failure", out, errOut)
	}
	healPushes(t)
	if st := hget(t, c, "s:"+s, "status"); st != "opening" {
		t.Fatalf("after the kill: status=%q want opening", st)
	}
	if sha := hget(t, c, "s:"+s, "from_sha"); sha != fileSHA(t, from) {
		t.Fatalf("after the kill: from_sha=%q want %q", sha, fileSHA(t, from))
	}
	if out := takeOne(t, addr); out != "NONE trips=1\n" {
		t.Fatalf("take after the kill: %q; want NONE", out)
	}

	expect(t, 0, "OPEN "+s+" units=5 pushed=3 existed=2 closed=0 skipped_done=0\n", openArgs(addr, s, from)...)
	opened := hget(t, c, "s:"+s, "opened_at")
	if opened == "" {
		t.Fatal("opened_at not set")
	}
	if n := c.ZCard(ctx, "sprint:order").Val(); n != 1 {
		t.Fatalf("sprint:order has %d entries, want 1", n)
	}
	if n := pushEvents(t, c, s); n != 5 {
		t.Fatalf("s:%s:log has %d push events, want 5", s, n)
	}

	out := takeOne(t, addr, "--sprint", s)
	var id, token string
	if _, err := fmt.Sscanf(out, "CLAIMED "+s+"/%s attempt=1 token=%s", &id, &token); err != nil {
		t.Fatalf("take: %q: %v", out, err)
	}
	expect(t, 0, "DONE DONE id="+id+"\n", "task", "done", "--redis", addr, "--sprint", s, "--id", id,
		"--token", token, "--evidence", "fixture done")

	before := dbsize(t, c)
	args := append(openArgs(addr, s, from)[:6], "--now", fxLater, "--from", from)
	expect(t, 0, "OPEN "+s+" units=5 pushed=0 existed=4 closed=1 skipped_done=0\n", args...)
	if after := dbsize(t, c); after != before {
		t.Fatalf("third open: DBSIZE %d -> %d; want no key added", before, after)
	}
	if n := pushEvents(t, c, s); n != 5 {
		t.Fatalf("third open: s:%s:log has %d push events, want 5", s, n)
	}
	if got := hget(t, c, "s:"+s, "opened_at"); got != opened {
		t.Fatalf("opened_at moved %s -> %s; want HSETNX", opened, got)
	}

	expect(t, 0, s+" status=closed\n", "sprint", "close", "--redis", addr, "--sprint", s, "--now", fxNow)
	closedAt := hget(t, c, "s:"+s, "closed_at")
	// #3571: the second close is refused and writes nothing.
	expect(t, 1, "REFUSED "+s+" already closed (closed_at "+closedAt+"); nothing written; there is no reopen\n",
		"sprint", "close", "--redis", addr, "--sprint", s, "--now", fxLater)
	if got := hget(t, c, "s:"+s, "closed_at"); closedAt == "" || got != closedAt {
		t.Fatalf("closed_at %q -> %q; want one stamp", closedAt, got)
	}
}

// TestControl22 is #2939 control 22: take and needs.
func TestControl22(t *testing.T) {
	addr, c := openFixture(t)
	const s = "control-22"
	from := writeWorkSet(t, "needs", unit("a", fxFriend, ""), unit("b", fxFriend, `:needs ("a")`))
	expect(t, 0, "OPEN "+s+" units=2 pushed=2 existed=0 closed=0 skipped_done=0\n", openArgs(addr, s, from)...)
	if got := hget(t, c, "task:b", "needs"); got != "a" {
		t.Fatalf("s:%s:task:b needs=%q want a", s, got)
	}

	code, out, errOut := runSprint("task", "take", "--redis", addr, "--sprint", s, "--as", fxFriend)
	// #2929 rev 6: a claim prints its CLAIMED line and one TASK line.
	if code != 0 || strings.Count(out, "\n") != 2 || !strings.HasPrefix(out, "CLAIMED "+s+"/a attempt=1 ") ||
		!strings.Contains(out, "\nTASK a ") {
		t.Fatalf("take: code=%d out=%q stderr=%q; want a claimed and b passed over", code, out, errOut)
	}
	var token string
	if _, err := fmt.Sscanf(out, "CLAIMED "+s+"/a attempt=1 token=%s", &token); err != nil {
		t.Fatal(err)
	}
	expect(t, 7, "BLOCKED needs a\n", "task", "take", "--redis", addr, "--sprint", s, "--as", fxFriend, "--id", "b")
	expect(t, 0, "DONE DONE id=a\n", "task", "done", "--redis", addr, "--sprint", s, "--id", "a",
		"--token", token, "--evidence", "fixture done")
	code, out, errOut = runSprint("task", "take", "--redis", addr, "--sprint", s, "--as", fxFriend, "--id", "b")
	if code != 0 || !strings.HasPrefix(out, "CLAIMED "+s+"/b attempt=1 ") {
		t.Fatalf("take --id b after done a: code=%d out=%q stderr=%q", code, out, errOut)
	}

	// A need on a unit done in the file does not block.
	const s2 = "control-22-done"
	// one task store (#3778): ids are global, so this sprint's units are c, d
	from2 := writeWorkSet(t, "done-need", unit("c", fxFriend, ":done t"), unit("d", fxFriend, `:needs ("c")`))
	expect(t, 0, "OPEN "+s2+" units=2 pushed=1 existed=0 closed=0 skipped_done=1\n", openArgs(addr, s2, from2)...)
	if exists(t, c, "task:c") {
		t.Fatalf("task:c exists; a done unit is never pushed (%s)", s2)
	}
	if got := hget(t, c, "task:d", "needs"); got != "" {
		t.Fatalf("task:d needs=%q want empty (%s)", got, s2)
	}
	if out := takeOne(t, addr, "--sprint", s2); !strings.HasPrefix(out, "CLAIMED "+s2+"/d attempt=1 ") {
		t.Fatalf("take after open: %q; want b claimed", out)
	}
}

// TestSprintStatusOpenOnly: status with no --sprint prints only open sprints.
func TestSprintStatusOpenOnly(t *testing.T) {
	addr, _ := openFixture(t)
	expect(t, 0, "OPEN status-open units=1 pushed=1 existed=0 closed=0 skipped_done=0\n",
		openArgs(addr, "status-open", writeWorkSet(t, "one", unit("u1", fxFriend, "")))...)
	expect(t, 0, "OPEN status-closed units=1 pushed=1 existed=0 closed=0 skipped_done=0\n",
		openArgs(addr, "status-closed", writeWorkSet(t, "two", unit("v1", fxFriend, "")))...)
	expect(t, 0, "status-closed status=closed\n", "sprint", "close", "--redis", addr, "--sprint", "status-closed", "--now", fxNow)
	expect(t, 0, "status-open open 0/1 0% -> eta ?\n", "sprint", "status", "--redis", addr, "--now", fxLater)
}

// TestSprintOpenReplay pins the existing-sprint rule.
func TestSprintOpenReplay(t *testing.T) {
	sha8 := func(t *testing.T, p string) string { return fileSHA(t, p)[:8] }
	setup := func(t *testing.T, s string) (string, *redis.Client, string) {
		addr, c := openFixture(t)
		a := writeWorkSet(t, "A", unit("u1", fxFriend, ""), unit("u2", fxFriend, ""))
		return addr, c, a
	}
	t.Run("SameSourceOpen", func(t *testing.T) {
		const s = "replay-same"
		addr, c, a := setup(t, s)
		expect(t, 0, "OPEN "+s+" units=2 pushed=2 existed=0 closed=0 skipped_done=0\n", openArgs(addr, s, a)...)
		expect(t, 0, "OPEN "+s+" units=2 pushed=0 existed=2 closed=0 skipped_done=0\n", openArgs(addr, s, a)...)
		if got := members(t, c, "s:"+s+":units"); got != "u1,u2" {
			t.Fatalf("units=%q want u1,u2", got)
		}
		if got := hget(t, c, "s:"+s, "from_sha"); got != fileSHA(t, a) {
			t.Fatalf("from_sha=%q", got)
		}
		if out := takeOne(t, addr); !strings.HasPrefix(out, "CLAIMED "+s+"/u1 ") {
			t.Fatalf("take: %q; want u1", out)
		}
	})
	t.Run("ChangedSource", func(t *testing.T) {
		const s = "replay-changed"
		addr, c, a := setup(t, s)
		expect(t, 0, "OPEN "+s+" units=2 pushed=2 existed=0 closed=0 skipped_done=0\n", openArgs(addr, s, a)...)
		b := writeWorkSet(t, "A", unit("u1", fxFriend, ""), unit("u2", fxFriend, ""), unit("u3", fxFriend, ""))
		before := dbsize(t, c)
		expect(t, 1, "REFUSED "+s+" from_sha "+sha8(t, a)+" != "+sha8(t, b)+"\n", openArgs(addr, s, b)...)
		if after := dbsize(t, c); after != before {
			t.Fatalf("DBSIZE %d -> %d", before, after)
		}
		if got := members(t, c, "s:"+s+":units"); got != "u1,u2" {
			t.Fatalf("units=%q want u1,u2", got)
		}
		if got := hget(t, c, "s:"+s, "from_sha"); got != fileSHA(t, a) {
			t.Fatalf("from_sha=%q", got)
		}
		if exists(t, c, "task:u3") {
			t.Fatal("u3 was pushed")
		}
	})
	t.Run("ChangedSourceAfterKill", func(t *testing.T) {
		const s = "replay-kill"
		addr, c, a := setup(t, s)
		killPushesAfter(t, 1)
		if code, out, errOut := runSprint(openArgs(addr, s, a)...); code == 0 {
			t.Fatalf("killed open: out=%q stderr=%q", out, errOut)
		}
		healPushes(t)
		if st := hget(t, c, "s:"+s, "status"); st != "opening" {
			t.Fatalf("status=%q want opening", st)
		}
		if got := hget(t, c, "s:"+s, "from_sha"); got != fileSHA(t, a) {
			t.Fatalf("from_sha=%q", got)
		}
		if !exists(t, c, "task:u1") {
			t.Fatal("u1 was not pushed before the kill")
		}
		b := writeWorkSet(t, "B", unit("u1", fxFriend, ""), unit("u3", fxFriend, ""))
		before := dbsize(t, c)
		expect(t, 1, "REFUSED "+s+" from_sha "+sha8(t, a)+" != "+sha8(t, b)+"\n", openArgs(addr, s, b)...)
		if after := dbsize(t, c); after != before {
			t.Fatalf("DBSIZE %d -> %d", before, after)
		}
		if st := hget(t, c, "s:"+s, "status"); st != "opening" {
			t.Fatalf("status=%q want opening", st)
		}
		if exists(t, c, "task:u3") {
			t.Fatal("u3 was pushed")
		}
		if out := takeOne(t, addr); out != "NONE trips=1\n" {
			t.Fatalf("take: %q want NONE", out)
		}
		expect(t, 0, "OPEN "+s+" units=2 pushed=1 existed=1 closed=0 skipped_done=0\n", openArgs(addr, s, a)...)
		if st := hget(t, c, "s:"+s, "status"); st != "open" {
			t.Fatalf("status=%q want open", st)
		}
	})
	t.Run("AfterClose", func(t *testing.T) {
		const s = "replay-closed"
		addr, c, a := setup(t, s)
		expect(t, 0, "OPEN "+s+" units=2 pushed=2 existed=0 closed=0 skipped_done=0\n", openArgs(addr, s, a)...)
		expect(t, 0, s+" status=closed\n", "sprint", "close", "--redis", addr, "--sprint", s, "--now", fxNow)
		before := dbsize(t, c)
		expect(t, 1, "REFUSED "+s+" closed\n", openArgs(addr, s, a)...)
		if after := dbsize(t, c); after != before {
			t.Fatalf("DBSIZE %d -> %d", before, after)
		}
		if st := hget(t, c, "s:"+s, "status"); st != "closed" {
			t.Fatalf("status=%q want closed", st)
		}
		if out := takeOne(t, addr); out != "NONE trips=1\n" {
			t.Fatalf("take: %q want NONE", out)
		}
	})
}

// TestSprintOpenPushRefused pins the refusal rule: CONFLICT, INVALID and
// OVERLAP leave the sprint opening and exit 1 NOT-OPENED.
func TestSprintOpenPushRefused(t *testing.T) {
	notOpened := func(t *testing.T, c *redis.Client, addr, s string) {
		t.Helper()
		if st := hget(t, c, "s:"+s, "status"); st != "opening" {
			t.Fatalf("status=%q want opening", st)
		}
		if exists(t, c, "s:"+s+":units") {
			t.Fatal("s:<S>:units exists")
		}
		if _, err := c.ZScore(context.Background(), "sprint:order", s).Result(); err == nil {
			t.Fatalf("sprint:order holds %s", s)
		}
		if out := takeOne(t, addr); out != "NONE trips=1\n" {
			t.Fatalf("take: %q want NONE", out)
		}
	}
	conflict := func(t *testing.T, s string) (string, *redis.Client, string) {
		t.Helper()
		addr, c := openFixture(t)
		a := writeWorkSet(t, "A", unit("u1", fxFriend, ""), unit("u2", fxFriend, ""))
		expect(t, 0, "PUSH CREATED id=u2\n", "task", "push", "--redis", addr, "--sprint", s, "--id", "u2",
			"--to", fxFriend, "--title", "another title")
		expect(t, 1, "CONFLICT "+s+" u2: id held by another payload (task:u2; ids are global); remedy: give the unit a new id in "+a+"\n"+
			"NOT-OPENED "+s+" units=2 pushed=1 existed=0 closed=0 skipped_done=0 refused=1\n", openArgs(addr, s, a)...)
		notOpened(t, c, addr, s)
		return addr, c, a
	}
	t.Run("Conflict", func(t *testing.T) {
		conflict(t, "refused-conflict")
	})
	t.Run("ConflictNewSprint", func(t *testing.T) {
		const s, s2 = "refused-conflict-new", "refused-conflict-new2"
		addr, c, a := conflict(t, s)
		ctx := context.Background()
		// The writes redistribute.lua makes (:295-298); no verb cancels it.
		key := "task:u2"
		sha := hget(t, c, key, "payload_sha")
		c.ZRem(ctx, "s:"+s+":open:"+fxFriend, "u2")
		c.SRem(ctx, "s:"+s+":idx:task:open", "u2")
		c.SAdd(ctx, "s:"+s+":idx:task:cancelled", "u2")
		c.HSet(ctx, key, "state", "cancelled")

		expect(t, 1, "CONFLICT "+s+" u2: id held by another payload (task:u2; ids are global); remedy: give the unit a new id in "+a+"\n"+
			"NOT-OPENED "+s+" units=2 pushed=0 existed=1 closed=0 skipped_done=0 refused=1\n", openArgs(addr, s, a)...)
		if st := hget(t, c, key, "state"); st != "cancelled" || hget(t, c, key, "payload_sha") != sha {
			t.Fatalf("u2 state=%q payload moved; want cancelled with its first payload", st)
		}
		notOpened(t, c, addr, s)

		expect(t, 0, s+" status=closed\n", "sprint", "close", "--redis", addr, "--sprint", s, "--now", fxNow)
		if st := hget(t, c, "s:"+s, "status"); st != "closed" {
			t.Fatalf("status=%q want closed", st)
		}
		expect(t, 1, "REFUSED "+s+" closed\n", openArgs(addr, s, a)...)

		oldS := c.HGetAll(ctx, "s:"+s).Val()
		oldU2 := c.HGetAll(ctx, key).Val()
		// one task store (#3778): the ids stay held; the new sprint's units
		// carry new ids
		a = writeWorkSet(t, "A2", unit("u1n", fxFriend, ""), unit("u2n", fxFriend, ""))
		expect(t, 0, "OPEN "+s2+" units=2 pushed=2 existed=0 closed=0 skipped_done=0\n", openArgs(addr, s2, a)...)
		if st := hget(t, c, "s:"+s2, "status"); st != "open" {
			t.Fatalf("s:%s status=%q want open", s2, st)
		}
		if got := members(t, c, "s:"+s2+":units"); got != "u1n,u2n" {
			t.Fatalf("s:%s:units=%q", s2, got)
		}
		if out := takeOne(t, addr, "--sprint", s2); !strings.HasPrefix(out, "CLAIMED "+s2+"/u1n ") {
			t.Fatalf("take --sprint %s: %q; want u1n", s2, out)
		}
		if fmt.Sprint(c.HGetAll(ctx, "s:"+s).Val()) != fmt.Sprint(oldS) || fmt.Sprint(c.HGetAll(ctx, key).Val()) != fmt.Sprint(oldU2) {
			t.Fatal("the new sprint's open changed s:<S> or s:<S>:task:u2")
		}
		order := c.ZRange(ctx, "sprint:order", 0, -1).Val()
		if strings.Join(order, ",") != s2 {
			t.Fatalf("sprint:order=%v; want only %s", order, s2)
		}
	})
	t.Run("Overlap", func(t *testing.T) {
		const s = "refused-overlap"
		addr, c := openFixture(t)
		ctx := context.Background()
		// An open sprint T holding a live work task over internal/x/.
		c.HSet(ctx, "s:t-overlap", "status", "open")
		c.SAdd(ctx, "sprints", "t-overlap")
		c.ZAdd(ctx, "sprint:order", redis.Z{Score: 1, Member: "t-overlap"})
		expect(t, 0, "PUSH CREATED id=x1\n", "task", "push", "--redis", addr, "--sprint", "t-overlap", "--id", "x1",
			"--to", fxFriend, "--title", "x work | PATHS: internal/x/")
		a := writeWorkSet(t, "A", unit("u1", fxFriend, ""),
			`(unit "u2" :kind work :owner "stella" :title "u2 title | PATHS: internal/x/y.go" :done-when "u2 passes")`)
		code, out, errOut := runSprint(openArgs(addr, s, a)...)
		lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
		if code != 1 || len(lines) != 2 || !strings.HasPrefix(lines[0], "OVERLAP "+s+" id=u2 with=t-overlap/x1 ") ||
			lines[1] != "NOT-OPENED "+s+" units=2 pushed=1 existed=0 closed=0 skipped_done=0 refused=1" {
			t.Fatalf("code=%d out=%q stderr=%q", code, out, errOut)
		}
		if exists(t, c, "task:u2") {
			t.Fatal("u2 was written")
		}
		// T's own claim is not this test's point; take only s's queue.
		if st := hget(t, c, "s:"+s, "status"); st != "opening" {
			t.Fatalf("status=%q want opening", st)
		}
		if exists(t, c, "s:"+s+":units") {
			t.Fatal("s:<S>:units exists")
		}
		if _, err := c.ZScore(ctx, "sprint:order", s).Result(); err == nil {
			t.Fatalf("sprint:order holds %s", s)
		}
		if out := takeOne(t, addr, "--sprint", s); out != "NONE trips=1\n" {
			t.Fatalf("take: %q want NONE", out)
		}
	})
}
