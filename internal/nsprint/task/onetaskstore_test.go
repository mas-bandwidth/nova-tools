package task_test

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/capacity"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/task"
	"github.com/redis/go-redis/v9"
)

// oneStore is one subtest's throwaway store with the library loaded, an open
// sprint and friends a, b and c registered with 4 slots and a beat.
type oneStore struct {
	t      *testing.T
	ctx    context.Context
	st     *store.Store
	client *redis.Client
	S      string
}

func newOneStore(t *testing.T) *oneStore {
	t.Helper()
	st, client := controlRedis(t)
	fx := &oneStore{t: t, ctx: context.Background(), st: st, client: client, S: "one-a"}
	client.HSet(fx.ctx, "s:"+fx.S, "status", "open")
	for _, f := range []string{"a", "b", "c"} {
		seedFriend(t, client, f, 4)
	}
	return fx
}

func (fx *oneStore) key(id string) string { return "task:" + id }

func (fx *oneStore) push(id, to string, mod func(*task.PushRequest)) task.PushResult {
	fx.t.Helper()
	req := task.PushRequest{Sprint: fx.S, ID: id, Kind: task.KindFix, Title: "fix " + id, To: to, Actor: "a"}
	if mod != nil {
		mod(&req)
	}
	res, err := task.PushChecked(fx.ctx, fx.st, req)
	if err != nil {
		fx.t.Fatalf("push %s: %v", id, err)
	}
	return res
}

func (fx *oneStore) take(id, as string) task.Claim {
	fx.t.Helper()
	claim, ok, err := task.Take(fx.ctx, fx.st, task.TakeRequest{Sprint: fx.S, ID: id, As: as, Actor: as})
	if err != nil || !ok {
		fx.t.Fatalf("take %s as %s: ok=%v err=%v", id, as, ok, err)
	}
	return claim
}

func (fx *oneStore) state(id string) string {
	fx.t.Helper()
	return fx.client.HGet(fx.ctx, fx.key(id), "state").Val()
}

func (fx *oneStore) field(id, f string) string {
	return fx.client.HGet(fx.ctx, fx.key(id), f).Val()
}

func (fx *oneStore) queue(f string) []string {
	return fx.client.ZRange(fx.ctx, "s:"+fx.S+":open:"+f, 0, -1).Val()
}

func (fx *oneStore) member(set, id string) bool {
	return fx.client.SIsMember(fx.ctx, set, id).Val()
}

func (fx *oneStore) counts(f string) task.Counts {
	fx.t.Helper()
	c, err := task.CountsFor(fx.ctx, fx.st, fx.S, f)
	if err != nil {
		fx.t.Fatal(err)
	}
	return c
}

func (fx *oneStore) want(what string, r task.Reply, err error, status string) task.Reply {
	fx.t.Helper()
	if err != nil || r.Status != status {
		fx.t.Fatalf("%s = %v %v; want %s", what, r, err, status)
	}
	return r
}

// snapshot is the DUMP of every key: the write counter of a refusal. A test
// server only, so SCAN is fine here.
func (fx *oneStore) snapshot() map[string]string {
	fx.t.Helper()
	out := map[string]string{}
	iter := fx.client.Scan(fx.ctx, 0, "*", 1000).Iterator()
	for iter.Next(fx.ctx) {
		k := iter.Val()
		out[k] = fx.client.Dump(fx.ctx, k).Val()
	}
	if err := iter.Err(); err != nil {
		fx.t.Fatal(err)
	}
	return out
}

func (fx *oneStore) unchanged(what string, before map[string]string) {
	fx.t.Helper()
	after := fx.snapshot()
	if !reflect.DeepEqual(before, after) {
		for k, v := range after {
			if before[k] != v {
				fx.t.Errorf("%s wrote %s", what, k)
			}
		}
		for k := range before {
			if _, ok := after[k]; !ok {
				fx.t.Errorf("%s deleted %s", what, k)
			}
		}
		fx.t.FailNow()
	}
}

// TestOneTaskStoreControls is #3206 rev 5 PR A's DONE-WHEN: the friend-queue
// verbs on the one store, in the vocabulary of ruling nova-tools#3516 (no
// blocked state; an unmet DEPENDS-ON is waiting and never ready). The migrate
// subtests and drain_gate belong to PR B.
func TestOneTaskStoreControls(t *testing.T) {
	t.Run("push", func(t *testing.T) {
		fx := newOneStore(t)
		res := fx.push("p1", "a", nil)
		if res.Status != task.PushCreated || res.Waiting != 0 || res.OnMet {
			t.Fatalf("push = %+v; want CREATED ready", res)
		}
		if fx.state("p1") != "open" || fx.field("p1", "dest") != "a" || fx.field("p1", "front") != "0" {
			t.Fatalf("hash state=%s dest=%s front=%s", fx.state("p1"), fx.field("p1", "dest"), fx.field("p1", "front"))
		}
		if got := fx.queue("a"); !reflect.DeepEqual(got, []string{"p1"}) {
			t.Fatalf("open:a = %v", got)
		}
		for _, k := range []task.Kind{task.KindRebase, task.KindRecut} {
			id := "k-" + string(k)
			if res := fx.push(id, "a", func(r *task.PushRequest) { r.Kind = k }); res.Status != task.PushCreated {
				t.Fatalf("push kind %s = %+v", k, res)
			}
		}
		if n, _ := receipts(fx.ctx, fx.client, "s:"+fx.S+":log", "task push", "p1"); n != 1 {
			t.Fatalf("push receipts = %d; want 1", n)
		}
		if res := fx.push("p1", "a", nil); res.Status != task.PushExists {
			t.Fatalf("re-push = %s; want EXISTS", res.Status)
		}
		if _, err := task.PushChecked(fx.ctx, fx.st, task.PushRequest{Sprint: fx.S, ID: "bad", Kind: task.KindFix, To: "a", DependsOn: "not a condition"}); err != nil {
			t.Fatal(err)
		} else if fx.client.Exists(fx.ctx, fx.key("bad")).Val() != 0 {
			t.Fatal("a malformed DEPENDS-ON wrote the task")
		}
	})

	t.Run("take", func(t *testing.T) {
		fx := newOneStore(t)
		fx.push("t1", "a", nil)
		claim := fx.take("t1", "a")
		if claim.Attempt != 1 || fx.state("t1") != "claimed" || len(fx.queue("a")) != 0 {
			t.Fatalf("take: %+v state=%s queue=%v", claim, fx.state("t1"), fx.queue("a"))
		}
		fx.push("t2", "b", nil)
		if _, ok, _ := task.Take(fx.ctx, fx.st, task.TakeRequest{Sprint: fx.S, ID: "t2", As: "a"}); ok {
			t.Fatal("a took b's task")
		}
	})

	t.Run("done", func(t *testing.T) {
		fx := newOneStore(t)
		fx.push("d1", "a", nil)
		fx.take("d1", "a")
		if got, err := task.Done(fx.ctx, fx.st, task.DoneRequest{Sprint: fx.S, ID: "d1", As: "b", Evidence: "e"}); err != nil || got != task.DoneFenced {
			t.Fatalf("done --as b = %s %v; want FENCED", got, err)
		}
		if got, err := task.Done(fx.ctx, fx.st, task.DoneRequest{Sprint: fx.S, ID: "d1", As: "a", Evidence: "e"}); err != nil || got != task.DoneClosed {
			t.Fatalf("done --as a = %s %v; want DONE", got, err)
		}
		if fx.state("d1") != "closed" || !fx.member("s:"+fx.S+":done:a", "d1") || fx.counts("a").Working != 0 {
			t.Fatalf("after done: state=%s counts=%v", fx.state("d1"), fx.counts("a"))
		}
		fx.push("d2", "a", nil)
		if got, _ := task.Done(fx.ctx, fx.st, task.DoneRequest{Sprint: fx.S, ID: "d2", As: "a", Evidence: "e"}); got != task.DoneFenced {
			t.Fatalf("done --as of an unleased task = %s; want FENCED", got)
		}
	})

	t.Run("close", func(t *testing.T) {
		fx := newOneStore(t)
		fx.push("c1", "a", nil)
		r, err := task.Close(fx.ctx, fx.st, fx.S, "c1", "", "a", "")
		fx.want("close without evidence", r, err, "NOEVIDENCE")
		r, err = task.Close(fx.ctx, fx.st, fx.S, "c1", "not needed", "a", "")
		fx.want("close", r, err, "CLOSED-NOW")
		if fx.state("c1") != "closed" || fx.field("c1", "cancelled") != "1" || len(fx.queue("a")) != 0 ||
			!fx.member("s:"+fx.S+":idx:task:closed", "c1") || fx.member("s:"+fx.S+":idx:task:open", "c1") ||
			!fx.member("s:"+fx.S+":done:a", "c1") {
			t.Fatal("close left the task live")
		}
		r, err = task.Close(fx.ctx, fx.st, fx.S, "c1", "again", "a", "")
		fx.want("second close", r, err, "CLOSED")
		fx.push("c2", "a", nil)
		fx.take("c2", "a")
		r, err = task.Close(fx.ctx, fx.st, fx.S, "c2", "x", "a", "")
		fx.want("close of a lease", r, err, "LEASED")
		if r.ExitCode() != 4 {
			t.Fatalf("LEASED exit = %d", r.ExitCode())
		}

		// A task pushed before the one-store cutover has no dest field. Close
		// must still remove it from its existing friend's queue and attribute
		// its done index to that friend.
		fx.push("c-legacy", "a", nil)
		fx.client.HDel(fx.ctx, fx.key("c-legacy"), "dest")
		r, err = task.Close(fx.ctx, fx.st, fx.S, "c-legacy", "old task", "a", "")
		fx.want("close of a legacy open task", r, err, "CLOSED-NOW")
		if contains(fx.queue("a"), "c-legacy") || !fx.member("s:"+fx.S+":done:a", "c-legacy") || fx.counts("a").Ready != 0 {
			t.Fatal("close left a legacy task on its old queue")
		}
	})

	t.Run("front", func(t *testing.T) {
		fx := newOneStore(t)
		for _, id := range []string{"f1", "f2", "f3"} {
			fx.push(id, "a", nil)
		}
		r, err := task.Front(fx.ctx, fx.st, fx.S, "f3", "a", "a", "")
		fx.want("front", r, err, "FRONT")
		if q := fx.queue("a"); q[0] != "f3" {
			t.Fatalf("queue after front = %v", q)
		}
		r, err = task.Front(fx.ctx, fx.st, fx.S, "f3", "a", "a", "")
		fx.want("front again", r, err, "SAME")
		r, err = task.Front(fx.ctx, fx.st, fx.S, "f1", "b", "b", "")
		fx.want("front on another queue", r, err, "NONE")
	})

	t.Run("move", func(t *testing.T) {
		fx := newOneStore(t)
		fx.push("m1", "a", func(r *task.PushRequest) { r.Priority = 5 })
		r, err := task.Move(fx.ctx, fx.st, fx.S, "m1", "b", "a", "")
		fx.want("move", r, err, "MOVED")
		if len(fx.queue("a")) != 0 || fx.client.ZScore(fx.ctx, "s:"+fx.S+":open:b", "m1").Val() != 5 ||
			fx.field("m1", "dest") != "b" || fx.field("m1", "moved_from") != "a" {
			t.Fatal("move did not re-own at the same score")
		}
		r, err = task.Move(fx.ctx, fx.st, fx.S, "m1", "b", "a", "")
		fx.want("move to the same friend", r, err, "SAME")

		// Pre-cutover open tasks have queue membership but no dest field.
		fx.push("m-legacy", "a", func(r *task.PushRequest) { r.Priority = 7 })
		fx.client.HDel(fx.ctx, fx.key("m-legacy"), "dest")
		r, err = task.Move(fx.ctx, fx.st, fx.S, "m-legacy", "b", "a", "")
		fx.want("move of a legacy open task", r, err, "MOVED")
		if contains(fx.queue("a"), "m-legacy") || fx.client.ZScore(fx.ctx, "s:"+fx.S+":open:b", "m-legacy").Val() != 7 ||
			fx.field("m-legacy", "dest") != "b" || fx.field("m-legacy", "moved_from") != "a" {
			t.Fatal("move did not resolve a legacy task's queue")
		}

		fx.push("m2", "a", nil)
		claim := fx.take("m2", "a")
		r, err = task.Move(fx.ctx, fx.st, fx.S, "m2", "b", "c", "")
		fx.want("move of an up owner's lease", r, err, "LEASED")
		fx.wantCall("down a", func() (task.Reply, error) { return task.FriendDown(fx.ctx, fx.st, "a", true, "gone", "c", "") }, "DOWN")
		r, err = task.Move(fx.ctx, fx.st, fx.S, "m2", "b", "c", "")
		fx.want("move of a down owner's lease", r, err, "MOVED")
		// one store (#3778): the card names the friend whose queue it is on
		if fx.state("m2") != "open" || fx.field("m2", "owner") != "b" || !contains(fx.queue("b"), "m2") ||
			fx.client.ZCard(fx.ctx, "friend:a:cards:working").Val() != 0 {
			t.Fatal("down owner's lease did not reopen on b")
		}
		if got, _ := task.Done(fx.ctx, fx.st, task.DoneRequest{Sprint: fx.S, ID: "m2", Token: claim.Token, Evidence: "late"}); got != task.DoneFenced {
			t.Fatalf("old owner's done = %s; want FENCED", got)
		}
	})

	t.Run("push_waiting", func(t *testing.T) {
		fx := newOneStore(t)
		const pr = "mas-bandwidth/nova-tools#9001"
		res := fx.push("w1", "a", func(r *task.PushRequest) { r.DependsOn = pr })
		if res.Status != task.PushCreated || res.Waiting != 1 {
			t.Fatalf("push with an unmet DEPENDS-ON = %+v; want CREATED waiting 1", res)
		}
		if fx.state("w1") != "waiting" || len(fx.queue("a")) != 0 || fx.member("s:"+fx.S+":idx:task:open", "w1") ||
			!fx.member("s:"+fx.S+":idx:task:waiting", "w1") || !fx.member("s:"+fx.S+":waits:"+pr, "w1") {
			t.Fatal("a waiting task is on a ready queue or missing from the waiting index")
		}
		if c := fx.counts("a"); c.Ready != 0 || c.Waiting != 1 {
			t.Fatalf("counts = %+v; want ready 0 waiting 1", c)
		}
		if claims, err := task.Fill(fx.ctx, fx.st, fx.S, "a", 0, "a", ""); err != nil || len(claims) != 0 {
			t.Fatalf("fill took a waiting task: %v %v", claims, err)
		}
		if _, ok, _ := task.Take(fx.ctx, fx.st, task.TakeRequest{Sprint: fx.S, ID: "w1", As: "a"}); ok {
			t.Fatal("take --id leased a waiting task")
		}
		r, err := task.Resolve(fx.ctx, fx.st, fx.S, pr, false, "land", "")
		fx.want("unasserted resolve", r, err, "READY")
		if r.Arg(0) != "0" || fx.state("w1") != "waiting" {
			t.Fatal("an external condition was met without the fact")
		}
		r, err = task.Resolve(fx.ctx, fx.st, fx.S, pr, true, "land", "")
		fx.want("asserted resolve", r, err, "READY")
		if r.Arg(0) != "1" || fx.state("w1") != "open" || !contains(fx.queue("a"), "w1") || fx.counts("a").Waiting != 0 {
			t.Fatal("the met task is not ready on its queue")
		}
		if n, _ := receipts(fx.ctx, fx.client, "s:"+fx.S+":log", "task ready", "w1"); n != 1 {
			t.Fatalf("ready receipts = %d", n)
		}
	})

	t.Run("push_on_met", func(t *testing.T) {
		fx := newOneStore(t)
		fx.push("x", "a", nil)
		fx.take("x", "a")
		if got, _ := task.Done(fx.ctx, fx.st, task.DoneRequest{Sprint: fx.S, ID: "x", As: "a", Evidence: "e"}); got != task.DoneClosed {
			t.Fatal(got)
		}
		fx.client.Set(fx.ctx, "gate", "open", 0)
		res := fx.push("y", "b", func(r *task.PushRequest) { r.DependsOn = "task:x; key:gate=open"; r.Front = true })
		if !res.OnMet || res.Waiting != 0 || fx.state("y") != "open" || !contains(fx.queue("b"), "y") {
			t.Fatalf("push with met conditions = %+v state=%s", res, fx.state("y"))
		}
		if fx.field("y", "depends_on") != "task:x;key:gate=open" {
			t.Fatalf("depends_on = %q", fx.field("y", "depends_on"))
		}
	})

	t.Run("resolve_on_done", func(t *testing.T) {
		fx := newOneStore(t)
		fx.push("dep", "a", nil)
		fx.push("w", "b", func(r *task.PushRequest) { r.DependsOn = "task:dep;key:gate=open" })
		fx.push("w2", "c", func(r *task.PushRequest) { r.DependsOn = "task:dep" })
		fx.take("dep", "a")
		if got, _ := task.Done(fx.ctx, fx.st, task.DoneRequest{Sprint: fx.S, ID: "dep", As: "a", Evidence: "e"}); got != task.DoneClosed {
			t.Fatal(got)
		}
		if fx.state("w2") != "open" || !contains(fx.queue("c"), "w2") {
			t.Fatal("done did not release a task whose only dependency it was")
		}
		if fx.state("w") != "waiting" || fx.field("w", "waits_on") != "key:gate=open" {
			t.Fatalf("w state=%s waits_on=%s; want waiting on the key only", fx.state("w"), fx.field("w", "waits_on"))
		}
		r, err := task.Resolve(fx.ctx, fx.st, fx.S, "key:gate=open", true, "x", "")
		fx.want("resolve of an unset key", r, err, "READY")
		if r.Arg(0) != "0" {
			t.Fatal("a key condition was met by assertion")
		}
		fx.client.Set(fx.ctx, "gate", "open", 0)
		r, err = task.Resolve(fx.ctx, fx.st, fx.S, "key:gate=open", false, "x", "")
		fx.want("resolve of the set key", r, err, "READY")
		if r.Arg(0) != "1" || !contains(fx.queue("b"), "w") {
			t.Fatal("the key did not release w")
		}
		// close resolves too.
		fx.push("c", "a", nil)
		fx.push("w3", "a", func(r *task.PushRequest) { r.DependsOn = "task:c" })
		r, err = task.Close(fx.ctx, fx.st, fx.S, "c", "dropped", "a", "")
		fx.want("close", r, err, "CLOSED-NOW")
		if r.Arg(0) != "1" || fx.state("w3") != "open" {
			t.Fatal("close did not release its waiter")
		}
	})

	t.Run("depends", func(t *testing.T) {
		fx := newOneStore(t)
		fx.push("fixit", "b", nil)
		fx.push("job", "a", nil)
		claim := fx.take("job", "a")
		r, err := task.Depends(fx.ctx, fx.st, task.DependsRequest{Sprint: fx.S, ID: "job", On: "task:fixit", As: "b"})
		fx.want("depends by a non-owner", r, err, "FENCED")
		r, err = task.Depends(fx.ctx, fx.st, task.DependsRequest{Sprint: fx.S, ID: "job", On: "task:fixit", As: "a"})
		fx.want("depends", r, err, "WAITING")
		if fx.state("job") != "waiting" || fx.field("job", "dest") != "a" || fx.counts("a").Working != 0 || fx.counts("a").Waiting != 1 {
			t.Fatalf("depends: state=%s dest=%s counts=%+v", fx.state("job"), fx.field("job", "dest"), fx.counts("a"))
		}
		if got, _ := task.Done(fx.ctx, fx.st, task.DoneRequest{Sprint: fx.S, ID: "job", Token: claim.Token, Evidence: "e"}); got != task.DoneFenced {
			t.Fatalf("old lease's done = %s; want FENCED", got)
		}
		fx.take("fixit", "b")
		if got, _ := task.Done(fx.ctx, fx.st, task.DoneRequest{Sprint: fx.S, ID: "fixit", As: "b", Evidence: "e"}); got != task.DoneClosed {
			t.Fatal(got)
		}
		if fx.state("job") != "open" || !contains(fx.queue("a"), "job") {
			t.Fatal("the dependency's done did not return job to its owner's queue")
		}
		r, err = task.Depends(fx.ctx, fx.st, task.DependsRequest{Sprint: fx.S, ID: "job", On: "task:fixit"})
		fx.want("depends on a met condition", r, err, "MET")
		r, err = task.Depends(fx.ctx, fx.st, task.DependsRequest{Sprint: fx.S, ID: "job", On: "stream/one-store"})
		fx.want("depends of an open task", r, err, "WAITING")
		if contains(fx.queue("a"), "job") || fx.field("job", "depends_on") != "task:fixit;stream/one-store" {
			t.Fatalf("open task still ready or depends_on=%q", fx.field("job", "depends_on"))
		}
	})

	t.Run("fill", func(t *testing.T) {
		fx := newOneStore(t)
		for _, id := range []string{"f1", "f2", "f3", "f4", "f5"} {
			fx.push(id, "a", nil)
		}
		fx.push("fw", "a", func(r *task.PushRequest) { r.DependsOn = "task:never"; r.Front = true })
		claims, err := task.Fill(fx.ctx, fx.st, fx.S, "a", 3, "a", "")
		if err != nil || len(claims) != 3 {
			t.Fatalf("fill --max 3 = %d %v", len(claims), err)
		}
		claims, err = task.Fill(fx.ctx, fx.st, fx.S, "a", 0, "a", "")
		if err != nil || len(claims) != 1 {
			t.Fatalf("fill of the last free slot = %d %v; want 1", len(claims), err)
		}
		for _, c := range claims {
			if c.ID == "fw" {
				t.Fatal("fill leased a waiting task")
			}
		}
		if claims, _ := task.Fill(fx.ctx, fx.st, fx.S, "a", 0, "a", ""); len(claims) != 0 {
			t.Fatal("fill past the desired slots")
		}
	})

	t.Run("rebalance", func(t *testing.T) {
		fx := newOneStore(t)
		for _, id := range []string{"b1", "b2", "b3", "b4"} {
			fx.push(id, "b", nil)
		}
		fx.push("bw", "b", func(r *task.PushRequest) { r.Kind = task.KindWork })
		head := strings.Repeat("e", 40)
		fx.client.HSet(fx.ctx, "s:"+fx.S+":pr:nova-tools:7", "head", head)
		if res := fx.push("br", "b", func(r *task.PushRequest) {
			r.Kind, r.Title, r.Author, r.Repo, r.PR, r.Head = task.KindRead, "read a/branch", "a", "nova-tools", 7, head
		}); res.Status != task.PushCreated {
			t.Fatalf("push read = %+v", res)
		}
		fx.push("bh", "b", func(r *task.PushRequest) { r.Title = "release stella's hold" })
		fx.client.HSet(fx.ctx, "s:"+fx.S+":policy", "rebalance_floor", "3")
		moves, err := task.Rebalance(fx.ctx, fx.st, task.RebalanceRequest{Sprint: fx.S, Actor: "rowan"})
		if err != nil {
			t.Fatal(err)
		}
		// b has 7 ready and a and c none; each wants 3 but b never drops
		// below the floor, so 4 move, all fix: never the work task, the read
		// authored by a, or stella's hold release.
		if len(moves) != 4 {
			t.Fatalf("moves = %+v; want 4", moves)
		}
		for _, m := range moves {
			if m.ID == "bw" || m.ID == "bh" || (m.ID == "br" && m.To == "a") {
				t.Fatalf("moved %+v against the rules", m)
			}
		}
		if got := fx.counts("b").Ready; got != 3 {
			t.Fatalf("donor ready = %d; want the floor", got)
		}
		again, err := task.Rebalance(fx.ctx, fx.st, task.RebalanceRequest{Sprint: fx.S, Actor: "rowan"})
		if err != nil || len(again) != 0 {
			t.Fatalf("second pass moved %+v %v", again, err)
		}
		// The hold rules, planned without Redis.
		plan := task.PlanRebalance(task.RebalancePlanInput{
			Floor: 1, Releaser: "stella", Known: []string{"a", "b", "stella", "johnny"},
			Friends: []task.RebalanceFriend{
				{Name: "johnny", Down: true},
				{Name: "stella", Up: true},
				{Name: "b", Up: true, Ready: []task.RebalanceTask{
					{ID: "release-1-johnny", Kind: "fix", Title: "release johnny's hold"},
					{ID: "h2", Kind: "fix", Title: "holds on"},
					{ID: "r1", Kind: "read", Title: "read b/x"},
				}},
			},
		})
		if len(plan) != 1 || plan[0].ID != "release-1-johnny" || plan[0].To != "stella" {
			t.Fatalf("hold plan = %+v; want only the down holder's release to the releaser", plan)
		}
	})

	t.Run("down_refusal", func(t *testing.T) {
		fx := newOneStore(t)
		for _, id := range []string{"c1", "c2", "c3"} {
			fx.push(id, "c", func(r *task.PushRequest) { r.Kind = task.KindWork })
		}
		fx.wantCall("down c", func() (task.Reply, error) {
			return task.FriendDown(fx.ctx, fx.st, "c", true, "out of credits", "rowan", "")
		}, "DOWN")
		if got := fx.client.HGet(fx.ctx, "friend:c:down", "reason").Val(); got != "out of credits" {
			t.Fatalf("down marker reason = %q", got)
		}
		fx.wantCall("down c again", func() (task.Reply, error) {
			return task.FriendDown(fx.ctx, fx.st, "c", true, "out of credits", "rowan", "")
		}, "SAME")

		before := fx.snapshot()
		res := fx.push("new", "c", nil)
		if res.Status != task.PushDown || res.Down != "out of credits" || res.Status.ExitCode() != 7 {
			t.Fatalf("push to a down friend = %+v", res)
		}
		fx.unchanged("push to a down friend", before)
		r, err := task.Move(fx.ctx, fx.st, fx.S, "c1", "c", "rowan", "")
		fx.want("move to a down friend", r, err, "DOWN")
		fx.unchanged("move to a down friend", before)
		if _, _, err := task.Take(fx.ctx, fx.st, task.TakeRequest{Sprint: fx.S, ID: "c1", As: "c"}); err == nil || !strings.Contains(err.Error(), "DOWN") {
			t.Fatalf("take by a down friend = %v; want DOWN", err)
		}
		fx.unchanged("take by a down friend", before)

		// No floor is set: a down friend's work moves anyway.
		moves, err := task.Rebalance(fx.ctx, fx.st, task.RebalanceRequest{Sprint: fx.S, Actor: "rowan"})
		if err != nil || len(moves) != 3 {
			t.Fatalf("rebalance off a down friend = %+v %v; want its 3 ready tasks", moves, err)
		}
		if n := len(fx.queue("c")); n != 0 {
			t.Fatalf("down friend keeps %d ready", n)
		}
		if fx.counts("a").Ready+fx.counts("b").Ready != 3 {
			t.Fatal("the down friend's work did not land on up friends")
		}

		fx.wantCall("up c", func() (task.Reply, error) { return task.FriendDown(fx.ctx, fx.st, "c", false, "", "rowan", "") }, "UP")
		if res := fx.push("new", "c", nil); res.Status != task.PushCreated {
			t.Fatalf("push after up = %s", res.Status)
		}
		// A legacy string marker (rowan-tools #277's hand SET) still refuses.
		fx.client.Set(fx.ctx, "friend:b:down", "legacy", 0)
		if res := fx.push("nb", "b", nil); res.Status != task.PushDown || res.Down != "legacy" {
			t.Fatalf("push to a legacy-down friend = %+v", res)
		}
	})

	t.Run("counts", func(t *testing.T) {
		fx := newOneStore(t)
		for _, id := range []string{"k1", "k2", "k3", "k4", "k5", "k6"} {
			fx.push(id, "a", nil)
		}
		fx.push("kw", "a", func(r *task.PushRequest) { r.DependsOn = "task:k6" })
		fx.take("k1", "a")
		fx.take("k2", "a")
		fx.take("k3", "a")
		if got, _ := task.Done(fx.ctx, fx.st, task.DoneRequest{Sprint: fx.S, ID: "k3", As: "a", Evidence: "e"}); got != task.DoneClosed {
			t.Fatal(got)
		}
		c := fx.counts("a")
		if c.String() != "2 3 1" || c.Waiting != 1 {
			t.Fatalf("counts = %q waiting %d; want \"2 3 1\" waiting 1", c.String(), c.Waiting)
		}
	})

	t.Run("owners", func(t *testing.T) {
		fx := newOneStore(t)
		fx.push("pfx-1", "a", nil)
		fx.push("pfx-2", "a", nil)
		fx.take("pfx-2", "a")
		fx.push("pfx-3", "b", func(r *task.PushRequest) { r.DependsOn = "task:pfx-1" })
		fx.push("pfx-4", "a", nil)
		fx.push("other", "a", nil)
		if r, err := task.Close(fx.ctx, fx.st, fx.S, "pfx-4", "gone", "a", ""); err != nil || r.Status != "CLOSED-NOW" {
			t.Fatal(r, err)
		}
		got, err := task.Owners(fx.ctx, fx.st, fx.S, "pfx-")
		if err != nil {
			t.Fatal(err)
		}
		want := []task.Owner{{"pfx-1", "a", "open"}, {"pfx-2", "a", "claimed"}, {"pfx-3", "b", "waiting"}}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("owners = %+v; want %+v", got, want)
		}
	})

	t.Run("capacity_paused_same", func(t *testing.T) {
		fx := newOneStore(t)
		fx.client.HSet(fx.ctx, "machine:m1:ceiling", "slots", "100")
		set := func(name string, slots int, opts capacity.DesiredOpts) (capacity.Result, error) {
			return capacity.SetFriendWith(fx.ctx, fx.st, name, "m1", slots, "rowan", "", opts)
		}
		if r, err := set("a", 4, capacity.DesiredOpts{Paused: "1"}); err != nil || r.Status != "SET" {
			t.Fatalf("pause = %+v %v", r, err)
		}
		if r, err := capacity.SetFriend(fx.ctx, fx.st, "a", "m1", 5, "rowan", ""); err != nil || r.Status != "SET" ||
			fx.client.HGet(fx.ctx, "friend:a:desired", "paused").Val() != "1" {
			t.Fatalf("six-arg write = %+v %v; want SET keeping paused=1", r, err)
		}
		fx.push("cp", "a", nil)
		if _, _, err := task.Take(fx.ctx, fx.st, task.TakeRequest{Sprint: fx.S, ID: "cp", As: "a"}); err == nil || !strings.Contains(err.Error(), "DOWN") {
			t.Fatalf("take while paused = %v; want DOWN", err)
		}
		at := fx.client.HGet(fx.ctx, "friend:a:desired", "at").Val()
		logLen := fx.client.XLen(fx.ctx, "cap:log").Val()
		if r, err := set("a", 5, capacity.DesiredOpts{Paused: "1"}); err != nil || r.Status != "SAME" {
			t.Fatalf("identical write = %+v %v; want SAME", r, err)
		}
		if fx.client.HGet(fx.ctx, "friend:a:desired", "at").Val() != at || fx.client.XLen(fx.ctx, "cap:log").Val() != logLen {
			t.Fatal("SAME wrote")
		}
		// #2934 on dev: the first desired write registers the friend, with
		// or without Register, and writes no beat.
		if r, err := set("newf", 2, capacity.DesiredOpts{}); err != nil || r.Status != "SET" {
			t.Fatalf("first write = %+v %v; want SET registering newf", r, err)
		}
		if r, err := set("newr", 2, capacity.DesiredOpts{Register: true}); err != nil || r.Status != "SET" {
			t.Fatalf("register = %+v %v", r, err)
		}
		for _, f := range []string{"newf", "newr"} {
			if !fx.member("friends", f) || fx.client.Exists(fx.ctx, "friend:"+f+":beat").Val() != 0 {
				t.Fatalf("%s: write did not add to friends, or wrote a beat", f)
			}
		}
	})
}

func (fx *oneStore) wantCall(what string, f func() (task.Reply, error), status string) {
	fx.t.Helper()
	r, err := f()
	fx.want(what, r, err, status)
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
