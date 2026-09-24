package main

import (
	"context"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/task"
	"github.com/redis/go-redis/v9"
)

// seatSprint is the one open sprint the #2929 control runs in. The verbs are
// given no --redis and no --sprint: the address comes from NOVA_SPRINT_REDIS
// and the sprint from sprint:order, so only NOVA_SPRINT_REDIS and NOVA_FRIEND
// are set.
const seatSprint = "s2929"

type seatFixture struct {
	t      *testing.T
	client *redis.Client
	log    string
}

// newSeat starts a throwaway Redis with the function library, friends {a, b}
// both beating with free slots, and one open sprint at the head of
// sprint:order.
func newSeat(t *testing.T) *seatFixture {
	t.Helper()
	addr, client := sprintRedis(t)
	t.Setenv("NOVA_SPRINT_REDIS", addr)
	t.Setenv("NOVA_REDIS_ADDR", "")
	ctx := context.Background()
	for _, f := range []string{"a", "b"} {
		client.SAdd(ctx, "friends", f)
		client.HSet(ctx, "friend:"+f+":desired", "slots", 4, "paused", "0")
		client.HSet(ctx, "friend:"+f+":beat", "host", "fixture")
	}
	client.HSet(ctx, "s:"+seatSprint, "status", "open")
	client.SAdd(ctx, "sprints", seatSprint)
	client.ZAdd(ctx, "sprint:order", redis.Z{Score: 1, Member: seatSprint})
	return &seatFixture{t: t, client: client, log: "s:" + seatSprint + ":log"}
}

// as sets the seat's friend; an empty name unsets NOVA_FRIEND entirely.
func (f *seatFixture) as(name string) {
	f.t.Helper()
	f.t.Setenv("NOVA_FRIEND", name)
	if name == "" {
		_ = os.Unsetenv("NOVA_FRIEND")
	}
}

func (f *seatFixture) xlen() int64 {
	return f.client.XLen(context.Background(), f.log).Val()
}

// receipts returns every receipt of one kind, oldest first.
func (f *seatFixture) receipts(kind string) []map[string]any {
	var out []map[string]any
	for _, m := range f.client.XRange(context.Background(), f.log, "-", "+").Val() {
		if m.Values["kind"] == kind {
			out = append(out, m.Values)
		}
	}
	return out
}

func (f *seatFixture) lastReceipt(kind string) map[string]any {
	f.t.Helper()
	all := f.receipts(kind)
	if len(all) == 0 {
		f.t.Fatalf("no %q receipt in %s", kind, f.log)
	}
	return all[len(all)-1]
}

func (f *seatFixture) wantReceipt(kind, actor, forFriend string) {
	f.t.Helper()
	r := f.lastReceipt(kind)
	if r["actor"] != actor || r["for"] != forFriend {
		f.t.Fatalf("%s receipt actor=%v for=%v; want actor=%s for=%s (%v)", kind, r["actor"], r["for"], actor, forFriend, r)
	}
}

// run runs one verb and checks its exit code and, when want is not empty,
// that stdout+stderr contains want.
func (f *seatFixture) run(code int, want string, args ...string) string {
	f.t.Helper()
	got, out, errOut := runSprint(args...)
	if got != code || (want != "" && !strings.Contains(out+errOut, want)) {
		f.t.Fatalf("%s: exit %d stdout=%q stderr=%q; want exit %d with %q", strings.Join(args, " "), got, out, errOut, code, want)
	}
	return out
}

func (f *seatFixture) push(to, id string, extra ...string) string {
	f.t.Helper()
	args := append([]string{"task", "push", "--to", to, "--id", id, "--kind", "work", "--ref", "r", "--title", "T"}, extra...)
	return f.run(0, "PUSH CREATED id="+id, args...)
}

func (f *seatFixture) wantOpen(id string) {
	f.t.Helper()
	h := f.client.HGetAll(context.Background(), "s:"+seatSprint+":task:"+id).Val()
	if h["state"] != "open" || h["owner"] != "" {
		f.t.Fatalf("task %s state=%q owner=%q; want open with no owner", id, h["state"], h["owner"])
	}
}

// TestControl2929PushTakeFromSeat is the nova-tools#2929 rev 6 DONE-WHEN: push
// and take run from the seat, the initiator is $NOVA_FRIEND checked by the CLI
// verbs, --actor is gone, a take for another friend is denied with one
// receipt, a down friend gets no push, and the Functions never judge the
// actor they are given.
func TestControl2929PushTakeFromSeat(t *testing.T) {
	ctx := context.Background()
	claimRx := regexp.MustCompile(`^CLAIMED ` + seatSprint + `/t1 attempt=1 token=1\.[0-9a-f]{32}\nTASK t1 kind=work ref=r title="T"\n$`)

	t.Run("cross_push_then_self_take", func(t *testing.T) {
		f := newSeat(t)
		f.as("a")
		f.push("b", "t1")
		if got := f.client.HGet(ctx, "s:"+seatSprint+":task:t1", "pushed_by").Val(); got != "a" {
			t.Fatalf("pushed_by=%q, want a", got)
		}
		f.wantReceipt("task push", "a", "b")
		f.as("b")
		out := f.run(0, "", "task", "take", "--as", "b", "--id", "t1")
		if !claimRx.MatchString(out) {
			t.Fatalf("take stdout %q; want CLAIMED %s/t1 and its TASK line", out, seatSprint)
		}
		f.wantReceipt("task take", "b", "b")
		if n := f.client.Exists(ctx, "task:t1", "q:b").Val(); n != 0 {
			t.Fatalf("%d bash-keyspace keys exist; want none", n)
		}
	})

	t.Run("take_for_other_denied", func(t *testing.T) {
		f := newSeat(t)
		f.as("a")
		f.push("b", "t2")
		before := f.xlen()
		out := f.run(6, "", "task", "take", "--as", "b", "--id", "t2")
		if out != "TAKE DENIED id=t2 as=b initiator=a\n" {
			t.Fatalf("stdout %q; want TAKE DENIED id=t2 as=b initiator=a", out)
		}
		f.wantOpen("t2")
		if got := f.xlen(); got != before+1 {
			t.Fatalf("log grew by %d; want exactly one receipt", got-before)
		}
		r := f.lastReceipt("task take denied")
		if r["actor"] != "a" || r["for"] != "b" || r["reason"] != "as-not-initiator" || r["id"] != "t2" {
			t.Fatalf("denied receipt %v; want actor=a for=b reason=as-not-initiator id=t2", r)
		}
	})

	t.Run("no_friend_env_refused", func(t *testing.T) {
		f := newSeat(t)
		f.as("")
		before := f.xlen()
		f.run(2, "want NOVA_FRIEND in friends", "task", "push", "--to", "b", "--id", "n1", "--kind", "work", "--ref", "r", "--title", "T")
		f.run(2, "want NOVA_FRIEND in friends", "task", "take", "--as", "b")
		if got := f.xlen(); got != before {
			t.Fatalf("log grew by %d; want unchanged", got-before)
		}
	})

	t.Run("unregistered_initiator_refused", func(t *testing.T) {
		f := newSeat(t)
		f.as("a")
		f.push("b", "u1")
		f.as("z")
		before := f.xlen()
		f.run(2, "want NOVA_FRIEND in friends", "task", "push", "--to", "b", "--id", "u2", "--kind", "work", "--ref", "r", "--title", "T")
		f.run(2, "want NOVA_FRIEND in friends", "task", "take", "--as", "z")
		f.run(2, "want NOVA_FRIEND in friends", "task", "take", "--as", "b", "--id", "u1")
		if got := f.xlen(); got != before {
			t.Fatalf("log grew by %d; want unchanged", got-before)
		}
		if n := f.client.Exists(ctx, "s:"+seatSprint+":task:u2").Val(); n != 0 {
			t.Fatalf("u2 was written")
		}
		f.wantOpen("u1")
	})

	t.Run("push_to_unknown_friend_refused", func(t *testing.T) {
		f := newSeat(t)
		f.as("a")
		before := f.xlen()
		f.run(2, "", "task", "push", "--to", "z", "--id", "k1", "--kind", "work", "--ref", "r", "--title", "T")
		if got := f.xlen(); got != before {
			t.Fatalf("log grew by %d; want unchanged", got-before)
		}
	})

	t.Run("actor_flag_gone", func(t *testing.T) {
		f := newSeat(t)
		f.as("a")
		f.push("a", "g1")
		before := f.xlen()
		f.run(2, "actor", "task", "push", "--to", "b", "--id", "g2", "--kind", "work", "--ref", "r", "--title", "T", "--actor", "b")
		f.run(2, "actor", "task", "take", "--as", "a", "--id", "g1", "--actor", "b")
		if got := f.xlen(); got != before {
			t.Fatalf("log grew by %d; want unchanged", got-before)
		}
		f.wantOpen("g1")
	})

	t.Run("build_kind_refused", func(t *testing.T) {
		f := newSeat(t)
		f.as("a")
		before := f.xlen()
		f.run(2, "want work", "task", "push", "--to", "b", "--id", "w1", "--kind", "build", "--ref", "r", "--title", "T")
		if got := f.xlen(); got != before {
			t.Fatalf("log grew by %d; want unchanged", got-before)
		}
	})

	t.Run("hello_claims_as_initiator", func(t *testing.T) {
		f := newSeat(t)
		f.client.Del(ctx, "friend:b:beat")
		f.client.HSet(ctx, "friend:b:desired", "slots", 2, "machine", "m2929")
		f.client.HSet(ctx, "machine:m2929:ceiling", "slots", 8)
		f.as("a")
		f.push("b", "h1")
		before := f.xlen()
		hello := []string{"friend", "hello", "--as", "b", "--once", "--host", "m2929", "--session", "sess-b"}
		f.run(2, "want --as equal to NOVA_FRIEND", hello...)
		if n := f.client.Exists(ctx, "friend:b:beat").Val(); n != 0 {
			t.Fatalf("friend:b:beat exists after a refused hello")
		}
		if got := f.xlen(); got != before {
			t.Fatalf("log grew by %d; want unchanged", got-before)
		}
		f.wantOpen("h1")

		f.as("b")
		out := f.run(0, "", hello...)
		want := regexp.MustCompile(`^b up slots=2 taken=1\nCLAIMED ` + seatSprint + `/h1 attempt=1 token=1\.[0-9a-f]{32}\n$`)
		if !want.MatchString(out) {
			t.Fatalf("hello stdout %q; want b up slots=2 taken=1 and CLAIMED %s/h1", out, seatSprint)
		}
		f.wantReceipt("task take", "b", "b")
		for _, stream := range []string{f.log, "cap:log"} {
			for _, m := range f.client.XRange(ctx, stream, "-", "+").Val() {
				if m.Values["actor"] == "friend" {
					t.Fatalf("%s has a receipt with actor=friend: %v", stream, m.Values)
				}
			}
		}
	})

	t.Run("push_to_down_friend_refused", func(t *testing.T) {
		f := newSeat(t)
		const marker = "out-of-credits@2026-09-23T21:00:00Z"
		f.client.Set(ctx, "friend:b:down", marker, 0)
		f.client.Del(ctx, "friend:b:beat")
		f.as("a")
		before := f.xlen()
		for _, c := range []struct {
			id    string
			extra []string
		}{{"d1", nil}, {"d2", []string{"--front"}}} {
			args := append([]string{"task", "push", "--to", "b", "--id", c.id, "--kind", "work", "--ref", "r", "--title", "T"}, c.extra...)
			out := f.run(7, "", args...)
			if want := "PUSH DOWN id=" + c.id + " to=b down=" + marker + "\n"; out != want {
				t.Fatalf("stdout %q; want %q", out, want)
			}
			if n := f.client.Exists(ctx, "s:"+seatSprint+":task:"+c.id).Val(); n != 0 {
				t.Fatalf("task %s written for a down friend", c.id)
			}
			if f.client.ZScore(ctx, "s:"+seatSprint+":open:b", c.id).Err() != redis.Nil ||
				f.client.SIsMember(ctx, "s:"+seatSprint+":idx:task:open", c.id).Val() {
				t.Fatalf("task %s queued for a down friend", c.id)
			}
		}
		if got := f.xlen(); got != before {
			t.Fatalf("log grew by %d; want unchanged", got-before)
		}
		if got := f.client.Get(ctx, "friend:b:down").Val(); got != marker {
			t.Fatalf("friend:b:down=%q after push; want it untouched", got)
		}
		f.client.Del(ctx, "friend:b:down")
		f.push("b", "d1")
		f.wantReceipt("task push", "a", "b")
	})

	t.Run("function_does_not_judge_actor", func(t *testing.T) {
		f := newSeat(t)
		st := store.New(f.client)
		before := f.xlen()
		status, err := task.Push(ctx, st, task.PushRequest{
			Sprint: seatSprint, ID: "f1", Kind: task.KindWork, Title: "F", To: "b", Actor: "z",
		})
		if err != nil || status != task.PushCreated {
			t.Fatalf("library push with actor z: %v %v; want CREATED", status, err)
		}
		f.wantReceipt("task push", "z", "b")
		if _, ok, err := task.Take(ctx, st, task.TakeRequest{Sprint: seatSprint, ID: "f1", As: "b"}); err != nil || !ok {
			t.Fatalf("library take with no actor: ok=%v err=%v; want a claim", ok, err)
		}
		f.wantReceipt("task take", "", "b")
		if got := f.xlen(); got != before+2 {
			t.Fatalf("log grew by %d; want exactly 2", got-before)
		}
		if n := len(f.receipts("task take denied")); n != 0 {
			t.Fatalf("%d denied receipts; want none", n)
		}
	})
}
