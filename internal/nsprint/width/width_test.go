package width

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/deal"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/reconcile"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/task"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/redis/go-redis/v9"
)

type cmdHook struct {
	trips            int
	tripsBeforeWrite int
	unpipelinedHGet  int
	unpipelinedZCard int
}

func (h *cmdHook) DialHook(next redis.DialHook) redis.DialHook { return next }

func (h *cmdHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		name := strings.ToLower(cmd.Name())
		if name == "fcall" {
			args := cmd.Args()
			if len(args) > 1 && fmt.Sprint(args[1]) == FunctionWrite {
				h.tripsBeforeWrite = h.trips
			}
		}
		if name == "hget" {
			h.unpipelinedHGet++
		}
		if name == "zcard" {
			h.unpipelinedZCard++
		}
		h.trips++
		return next(ctx, cmd)
	}
}

func (h *cmdHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		h.trips++
		return next(ctx, cmds)
	}
}

func TestWidthControls(t *testing.T) {
	t.Run("C1", func(t *testing.T) {
		addr := testutil.Start(t)
		client := redis.NewClient(&redis.Options{Addr: addr})
		t.Cleanup(func() { _ = client.Close() })
		ctx := context.Background()
		if err := fn.Load(ctx, client); err != nil {
			t.Fatal(err)
		}

		st, err := store.Open(ctx, addr)
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()

		sprint := "s1"
		client.SAdd(ctx, "sprints", sprint)
		client.ZAdd(ctx, "sprint:order", redis.Z{Score: 1, Member: sprint})
		client.HSet(ctx, "s:"+sprint, "status", "open")

		client.SAdd(ctx, "friends", "f1")
		client.HSet(ctx, "friend:f1:desired", "slots", "8")
		client.HSet(ctx, "friend:f1:beat", "at", "1")

		for i := 1; i <= 20; i++ {
			id := fmt.Sprintf("t%d", i)
			client.ZAdd(ctx, "s:"+sprint+":open:f1", redis.Z{Score: float64(i), Member: id})
			client.HSet(ctx, "s:"+sprint+":task:"+id, "state", "open", "kind", "work", "ref", id)
		}

		res, err := task.Fill(ctx, st, "f1", sprint, 0, "f1", "")
		if err != nil {
			t.Fatalf("task.Fill failed: %v", err)
		}
		if res.N != 8 || len(res.Tasks) != 8 {
			t.Fatalf("res.N=%d, len(res.Tasks)=%d; want 8", res.N, len(res.Tasks))
		}
		if res.Deficit != 0 {
			t.Fatalf("res.Deficit=%d; want 0", res.Deficit)
		}

		lease, err := reconcile.Acquire(ctx, st, reconcile.AcquireOptions{Host: "test", Instance: "c1"})
		if err != nil {
			t.Fatal(err)
		}
		duty := &Duty{Store: st}
		if _, err := duty.Run(ctx, lease); err != nil {
			t.Fatal(err)
		}
		fs, ok, err := ReadFillstate(ctx, st, "f1")
		if err != nil || !ok {
			t.Fatalf("read fillstate: ok=%v err=%v", ok, err)
		}
		if fs.Working != 0 {
			t.Fatalf("fs.Working = %d; want 0 until beats", fs.Working)
		}
		if fs.Deficit != 0 {
			t.Fatalf("fs.Deficit = %d; want 0", fs.Deficit)
		}

		for _, tk := range res.Tasks {
			reply, err := client.FCall(ctx, "ns_task_beat", nil, sprint, tk.ID, tk.Token, "f1", "").Result()
			if err != nil {
				t.Fatalf("beat %s failed: %v", tk.ID, err)
			}
			vals := reply.([]any)
			if vals[0] != "WORKING" {
				t.Fatalf("beat %s = %v; want WORKING", tk.ID, vals)
			}
		}

		if _, err := duty.Run(ctx, lease); err != nil {
			t.Fatal(err)
		}
		fs, _, _ = ReadFillstate(ctx, st, "f1")
		if fs.Working != 8 {
			t.Fatalf("after beats working = %d; want 8", fs.Working)
		}
		if fs.Deficit != 0 {
			t.Fatalf("after beats deficit = %d; want 0", fs.Deficit)
		}

		doneTask := res.Tasks[0]
		_, err = client.FCall(ctx, "ns_task_done", nil, sprint, doneTask.ID, doneTask.Token, "success", "f1", "").Result()
		if err != nil {
			t.Fatalf("done failed: %v", err)
		}

		if _, err := duty.Run(ctx, lease); err != nil {
			t.Fatal(err)
		}
		fs, _, _ = ReadFillstate(ctx, st, "f1")
		if fs.Deficit != 1 {
			t.Fatalf("after done deficit = %d; want 1", fs.Deficit)
		}

		res2, err := task.Fill(ctx, st, "f1", sprint, 0, "f1", "")
		if err != nil {
			t.Fatalf("fill 2 failed: %v", err)
		}
		if res2.N != 1 || len(res2.Tasks) != 1 {
			t.Fatalf("fill 2 res.N=%d, len=%d; want exactly 1", res2.N, len(res2.Tasks))
		}
	})

	t.Run("C2", func(t *testing.T) {
		addr := testutil.Start(t)
		client := redis.NewClient(&redis.Options{Addr: addr})
		t.Cleanup(func() { _ = client.Close() })
		ctx := context.Background()
		if err := fn.Load(ctx, client); err != nil {
			t.Fatal(err)
		}

		st, err := store.Open(ctx, addr)
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()

		sprint := "s1"
		client.SAdd(ctx, "sprints", sprint)
		client.ZAdd(ctx, "sprint:order", redis.Z{Score: 1, Member: sprint})
		client.HSet(ctx, "s:"+sprint, "status", "open")

		client.SAdd(ctx, "friends", "f1")
		client.HSet(ctx, "friend:f1:desired", "slots", "1")
		client.HSet(ctx, "friend:f1:beat", "at", "1")

		for i := 1; i <= 5; i++ {
			id := fmt.Sprintf("t%d", i)
			client.ZAdd(ctx, "s:"+sprint+":open:f1", redis.Z{Score: float64(i), Member: id})
			client.HSet(ctx, "s:"+sprint+":task:"+id, "state", "open", "kind", "work", "ref", id)
		}

		type fillOut struct {
			res task.FillResult
			err error
		}
		ch := make(chan fillOut, 2)
		for i := 0; i < 2; i++ {
			go func() {
				r, e := task.Fill(ctx, st, "f1", sprint, 0, "f1", "")
				ch <- fillOut{res: r, err: e}
			}()
		}
		o1 := <-ch
		o2 := <-ch
		if o1.err != nil || o2.err != nil {
			t.Fatalf("concurrent fill error: o1=%v o2=%v", o1.err, o2.err)
		}
		totalClaimed := o1.res.N + o2.res.N
		if totalClaimed != 1 {
			t.Fatalf("total claimed = %d; want 1", totalClaimed)
		}
		if (o1.res.N == 1 && o2.res.N != 0) || (o2.res.N == 1 && o1.res.N != 0) {
			t.Fatalf("want one winner (n=1) and one loser (n=0): o1.N=%d o2.N=%d", o1.res.N, o2.res.N)
		}
	})

	t.Run("C3", func(t *testing.T) {
		addr := testutil.Start(t)
		client := redis.NewClient(&redis.Options{Addr: addr})
		t.Cleanup(func() { _ = client.Close() })
		ctx := context.Background()
		if err := fn.Load(ctx, client); err != nil {
			t.Fatal(err)
		}

		st, err := store.Open(ctx, addr)
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()

		sprint := "s1"
		client.SAdd(ctx, "sprints", sprint)
		client.ZAdd(ctx, "sprint:order", redis.Z{Score: 1, Member: sprint})
		client.HSet(ctx, "s:"+sprint, "status", "open")

		client.SAdd(ctx, "friends", "f1")
		client.HSet(ctx, "friend:f1:desired", "slots", "4")
		client.HSet(ctx, "friend:f1:beat", "at", "1")

		client.ZAdd(ctx, "s:"+sprint+":open:f1", redis.Z{Score: 1, Member: "t1"})
		client.HSet(ctx, "s:"+sprint+":task:t1", "state", "open", "kind", "work", "ref", "t1")

		res, err := task.Fill(ctx, st, "f1", sprint, 0, "f1", "")
		if err != nil || res.N != 1 {
			t.Fatalf("fill: res=%+v err=%v", res, err)
		}

		timeVal, err := client.Time(ctx).Result()
		if err != nil {
			t.Fatal(err)
		}
		nowMs := timeVal.UnixMilli()

		client.HSet(ctx, "s:"+sprint+":task:t1", "claimed_at", strconv.FormatInt(nowMs-61000, 10))

		reply, err := client.FCall(ctx, "ns_task_expire", nil, sprint, "t1", "reconciler", "").Result()
		if err != nil {
			t.Fatalf("expire failed: %v", err)
		}
		vals := reply.([]any)
		if vals[0] != "REOPENED" {
			t.Fatalf("expire reply = %v; want REOPENED", vals)
		}

		state := client.HGet(ctx, "s:"+sprint+":task:t1", "state").Val()
		if state != "open" {
			t.Fatalf("state = %q; want open", state)
		}

		lease, err := reconcile.Acquire(ctx, st, reconcile.AcquireOptions{Host: "test", Instance: "c3"})
		if err != nil {
			t.Fatal(err)
		}
		duty := &Duty{Store: st}
		if _, err := duty.Run(ctx, lease); err != nil {
			t.Fatal(err)
		}
		fs, ok, err := ReadFillstate(ctx, st, "f1")
		if err != nil || !ok {
			t.Fatalf("read fillstate: ok=%v err=%v", ok, err)
		}
		if fs.Leased != 0 {
			t.Fatalf("fs.Leased = %d; want 0", fs.Leased)
		}
		if fs.Deficit != 4 {
			t.Fatalf("fs.Deficit = %d; want 4", fs.Deficit)
		}
		if fs.Working != 0 {
			t.Fatalf("fs.Working = %d; want 0", fs.Working)
		}
	})

	t.Run("C4", func(t *testing.T) {
		addr := testutil.Start(t)
		client := redis.NewClient(&redis.Options{Addr: addr})
		t.Cleanup(func() { _ = client.Close() })
		ctx := context.Background()
		if err := fn.Load(ctx, client); err != nil {
			t.Fatal(err)
		}

		st, err := store.Open(ctx, addr)
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()

		sprint := "s1"
		client.SAdd(ctx, "sprints", sprint)
		client.ZAdd(ctx, "sprint:order", redis.Z{Score: 1, Member: sprint})
		client.HSet(ctx, "s:"+sprint, "status", "open")

		client.SAdd(ctx, "friends", "A")
		client.HSet(ctx, "friend:A:desired", "slots", "16")
		client.HSet(ctx, "friend:A:beat", "at", "1")

		timeVal, err := client.Time(ctx).Result()
		if err != nil {
			t.Fatal(err)
		}
		nowMs := timeVal.UnixMilli()

		// 2 working (living members with beat inside 60s)
		client.ZAdd(ctx, "friend:A:living",
			redis.Z{Score: float64(nowMs), Member: sprint + "/live1/1"},
			redis.Z{Score: float64(nowMs), Member: sprint + "/live2/1"},
		)

		rebalAfter := 30 * time.Second
		unfilledSince := nowMs - int64((rebalAfter + time.Second).Milliseconds())
		client.HSet(ctx, "friend:A:fillstate",
			"peak", "16",
			"peak_at", strconv.FormatInt(nowMs-10000, 10),
			"unfilled_since", strconv.FormatInt(unfilledSince, 10),
		)

		// 12 open tasks: 2 eligible, 10 deps-blocked
		client.ZAdd(ctx, "s:"+sprint+":open:A",
			redis.Z{Score: 1, Member: "e1"},
			redis.Z{Score: 2, Member: "e2"},
		)
		client.HSet(ctx, "s:"+sprint+":task:e1", "state", "open", "depends_on", "")
		client.HSet(ctx, "s:"+sprint+":task:e2", "state", "open", "depends_on", "")

		for i := 1; i <= 10; i++ {
			id := fmt.Sprintf("d%d", i)
			client.ZAdd(ctx, "s:"+sprint+":open:A", redis.Z{Score: float64(i + 2), Member: id})
			client.HSet(ctx, "s:"+sprint+":task:"+id, "state", "open", "depends_on", "dep_task")
		}

		lease, err := reconcile.Acquire(ctx, st, reconcile.AcquireOptions{Host: "test", Instance: "c4"})
		if err != nil {
			t.Fatal(err)
		}

		duty := &Duty{Store: st, RebalanceAfter: rebalAfter}
		if _, err := duty.Run(ctx, lease); err != nil {
			t.Fatal(err)
		}

		fs, ok, err := ReadFillstate(ctx, st, "A")
		if err != nil || !ok {
			t.Fatalf("read fillstate failed: ok=%v err=%v", ok, err)
		}

		// Width writes deficit=14 eligible=2 idle_deps=10 idle_no_ready=2 idle_unfilled=2; slots stays 16.
		if fs.Slots != 16 {
			t.Errorf("slots = %d; want 16", fs.Slots)
		}
		if fs.Deficit != 14 {
			t.Errorf("deficit = %d; want 14", fs.Deficit)
		}
		if fs.Eligible != 2 {
			t.Errorf("eligible = %d; want 2", fs.Eligible)
		}
		if fs.IdleDeps != 10 {
			t.Errorf("idle_deps = %d; want 10", fs.IdleDeps)
		}
		if fs.IdleNoReady != 2 {
			t.Errorf("idle_no_ready = %d; want 2", fs.IdleNoReady)
		}
		if fs.IdleUnfilled != 2 {
			t.Errorf("idle_unfilled = %d; want 2", fs.IdleUnfilled)
		}
		if fs.Peak != 16 {
			t.Errorf("peak = %d; want 16", fs.Peak)
		}

		// (a) B absent: no task moves.
		openA, _ := client.ZRange(ctx, "s:"+sprint+":open:A", 0, -1).Result()
		if len(openA) != 12 {
			t.Fatalf("C4(a): open:A = %d; want 12", len(openA))
		}

		// (b) B positive: friend:B:beat present, B slots 4, leased 0, 0 eligible (deficit 4 > eligible 0, spare 4),
		// fill_at written 5 s ago with fill_n 1: exactly A's 2 eligible move to B with 2 width move receipts,
		// 10 deps tasks stay on A.
		client.SAdd(ctx, "friends", "B")
		client.HSet(ctx, "friend:B:desired", "slots", "4")
		client.HSet(ctx, "friend:B:beat", "at", "1")
		client.HSet(ctx, "friend:B:fillstate", "fill_at", strconv.FormatInt(nowMs-5000, 10), "fill_n", "1")

		if _, err := duty.Run(ctx, lease); err != nil {
			t.Fatal(err)
		}
		openA, _ = client.ZRange(ctx, "s:"+sprint+":open:A", 0, -1).Result()
		openB, _ := client.ZRange(ctx, "s:"+sprint+":open:B", 0, -1).Result()
		if len(openB) != 2 || len(openA) != 10 {
			t.Fatalf("C4(b): open:B=%d (want 2), open:A=%d (want 10)", len(openB), len(openA))
		}
		msgs, _ := client.XRange(ctx, "s:"+sprint+":log", "-", "+").Result()
		moveCount := 0
		for _, m := range msgs {
			if m.Values["kind"] == "width move" {
				moveCount++
			}
		}
		if moveCount != 2 {
			t.Fatalf("C4(b): move receipts = %d, want 2", moveCount)
		}

		// (c) same B but fill_at written rebalance_after+1 s ago (no fresh fill): 0 moves.
		// Move tasks back to A first
		client.Del(ctx, "s:"+sprint+":open:B")
		client.ZAdd(ctx, "s:"+sprint+":open:A", redis.Z{Score: 1, Member: "e1"}, redis.Z{Score: 2, Member: "e2"})
		client.HSet(ctx, "s:"+sprint+":task:e1", "owner", "A")
		client.HSet(ctx, "s:"+sprint+":task:e2", "owner", "A")
		client.HSet(ctx, "friend:B:fillstate", "fill_at", strconv.FormatInt(nowMs-int64((rebalAfter+time.Second).Milliseconds()), 10))

		if _, err := duty.Run(ctx, lease); err != nil {
			t.Fatal(err)
		}
		openB, _ = client.ZRange(ctx, "s:"+sprint+":open:B", 0, -1).Result()
		if len(openB) != 0 {
			t.Fatalf("C4(c): stale fill_at, open:B=%d; want 0", len(openB))
		}

		// (d) same B but no friend:B:beat key: 0 moves.
		client.HSet(ctx, "friend:B:fillstate", "fill_at", strconv.FormatInt(nowMs-5000, 10))
		client.Del(ctx, "friend:B:beat")
		if _, err := duty.Run(ctx, lease); err != nil {
			t.Fatal(err)
		}
		openB, _ = client.ZRange(ctx, "s:"+sprint+":open:B", 0, -1).Result()
		if len(openB) != 0 {
			t.Fatalf("C4(d): no beat, open:B=%d; want 0", len(openB))
		}

		// (e) same B but 4 eligible of its own (deficit 4 = eligible 4): 0 moves.
		client.HSet(ctx, "friend:B:beat", "at", "1")
		for i := 1; i <= 4; i++ {
			id := fmt.Sprintf("be%d", i)
			client.ZAdd(ctx, "s:"+sprint+":open:B", redis.Z{Score: float64(i), Member: id})
			client.HSet(ctx, "s:"+sprint+":task:"+id, "state", "open", "depends_on", "")
		}
		if _, err := duty.Run(ctx, lease); err != nil {
			t.Fatal(err)
		}
		openB, _ = client.ZRange(ctx, "s:"+sprint+":open:B", 0, -1).Result()
		if len(openB) != 4 {
			t.Fatalf("C4(e): deficit == eligible, open:B=%d; want 4", len(openB))
		}

		// (f) B spare 1 (slots 1, leased 0, eligible 0, fresh fill_at, up): exactly 1 moves.
		for i := 1; i <= 4; i++ {
			id := fmt.Sprintf("be%d", i)
			client.Del(ctx, "s:"+sprint+":task:"+id)
		}
		client.Del(ctx, "s:"+sprint+":open:B")
		client.HSet(ctx, "friend:B:desired", "slots", "1")
		if _, err := duty.Run(ctx, lease); err != nil {
			t.Fatal(err)
		}
		openB, _ = client.ZRange(ctx, "s:"+sprint+":open:B", 0, -1).Result()
		openA, _ = client.ZRange(ctx, "s:"+sprint+":open:A", 0, -1).Result()
		if len(openB) != 1 || len(openA) != 11 {
			t.Fatalf("C4(f): B spare 1, open:B=%d (want 1), open:A=%d (want 11)", len(openB), len(openA))
		}
	})

	t.Run("C5", func(t *testing.T) {
		addr := testutil.Start(t)
		client := redis.NewClient(&redis.Options{Addr: addr})
		t.Cleanup(func() { _ = client.Close() })
		ctx := context.Background()
		if err := fn.Load(ctx, client); err != nil {
			t.Fatal(err)
		}

		st, err := store.Open(ctx, addr)
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()

		sprint := "s1"
		client.SAdd(ctx, "sprints", sprint)
		client.ZAdd(ctx, "sprint:order", redis.Z{Score: 1, Member: sprint})
		client.HSet(ctx, "s:"+sprint, "status", "open")

		client.SAdd(ctx, "friends", "f1")
		client.HSet(ctx, "friend:f1:desired", "slots", "4")
		client.HSet(ctx, "friend:f1:beat", "at", "1")

		client.ZAdd(ctx, "s:"+sprint+":open:f1", redis.Z{Score: 1, Member: "t-deps"})
		client.HSet(ctx, "s:"+sprint+":task:t-deps",
			"state", "open",
			"kind", "work",
			"depends_on", "mas-bandwidth/nova-tools#100",
			"ref", "t-deps",
		)
		client.HSet(ctx, "s:"+sprint+":pr:mas-bandwidth/nova-tools:100", "merged", "0", "base", "dev")

		lease, err := reconcile.Acquire(ctx, st, reconcile.AcquireOptions{Host: "test", Instance: "c5"})
		if err != nil {
			t.Fatal(err)
		}
		duty := &Duty{Store: st}
		if _, err := duty.Run(ctx, lease); err != nil {
			t.Fatal(err)
		}
		fs, ok, err := ReadFillstate(ctx, st, "f1")
		if err != nil || !ok {
			t.Fatalf("read fillstate: ok=%v err=%v", ok, err)
		}
		if fs.Eligible != 0 {
			t.Fatalf("fs.Eligible = %d; want 0", fs.Eligible)
		}
		if fs.IdleDeps != 1 {
			t.Fatalf("fs.IdleDeps = %d; want 1", fs.IdleDeps)
		}

		res, err := task.Fill(ctx, st, "f1", sprint, 0, "f1", "")
		if err != nil {
			t.Fatalf("task.Fill failed: %v", err)
		}
		if res.N != 0 {
			t.Fatalf("claimed N = %d; want 0", res.N)
		}
	})

	t.Run("C6", func(t *testing.T) {
		addr := testutil.Start(t)
		client := redis.NewClient(&redis.Options{Addr: addr})
		t.Cleanup(func() { _ = client.Close() })
		ctx := context.Background()
		if err := fn.Load(ctx, client); err != nil {
			t.Fatal(err)
		}

		st, err := store.Open(ctx, addr)
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()

		sprint := "s1"
		client.SAdd(ctx, "sprints", sprint)
		client.ZAdd(ctx, "sprint:order", redis.Z{Score: 1, Member: sprint})
		client.HSet(ctx, "s:"+sprint, "status", "open")

		client.SAdd(ctx, "friends", "A", "B")
		client.HSet(ctx, "friend:A:desired", "slots", "8")
		client.HSet(ctx, "friend:A:beat", "at", "1")
		client.HSet(ctx, "friend:B:desired", "slots", "8")
		client.HSet(ctx, "friend:B:beat", "at", "1")

		timeVal, err := client.Time(ctx).Result()
		if err != nil {
			t.Fatal(err)
		}
		nowMs := timeVal.UnixMilli()

		rebalAfter := 30 * time.Second
		unfilledSince := nowMs - int64((rebalAfter + time.Second).Milliseconds())

		client.HSet(ctx, "friend:A:fillstate",
			"unfilled_since", strconv.FormatInt(unfilledSince, 10),
			"idle_unfilled", "1",
			"deficit", "8",
			"eligible", "1",
		)
		client.HSet(ctx, "friend:B:fillstate",
			"fill_at", strconv.FormatInt(nowMs-5000, 10),
			"fill_n", "1",
			"deficit", "8",
			"eligible", "0",
		)

		repo := "mas-bandwidth/nova-tools"
		pr := "3073"
		head := "4139b79f"

		client.ZAdd(ctx, "s:"+sprint+":open:A", redis.Z{Score: 1, Member: "task-move"})
		client.HSet(ctx, "s:"+sprint+":task:task-move",
			"state", "open", "kind", "work", "repo", repo, "pr", pr, "head", head, "depends_on", "")

		lease, err := reconcile.Acquire(ctx, st, reconcile.AcquireOptions{Host: "test", Instance: "c6"})
		if err != nil {
			t.Fatal(err)
		}

		// Case 1: B holds closed task for same (repo, PR, head)
		client.SAdd(ctx, "s:"+sprint+":done:B", "task-closed")
		client.HSet(ctx, "s:"+sprint+":task:task-closed",
			"state", "closed", "kind", "work", "repo", repo, "pr", pr, "head", head)

		duty := &Duty{Store: st, RebalanceAfter: rebalAfter}
		if _, err := duty.Run(ctx, lease); err != nil {
			t.Fatal(err)
		}
		openB, _ := client.ZRange(ctx, "s:"+sprint+":open:B", 0, -1).Result()
		if len(openB) != 0 {
			t.Fatalf("closed dedup failed: task moved to B: %v", openB)
		}

		client.SRem(ctx, "s:"+sprint+":done:B", "task-closed")

		// Case 2: B holds working task for same (repo, PR, head)
		client.ZAdd(ctx, "friend:B:living", redis.Z{Score: float64(nowMs), Member: sprint + "/task-working/1"})
		client.HSet(ctx, "s:"+sprint+":task:task-working",
			"state", "working", "kind", "work", "repo", repo, "pr", pr, "head", head)

		client.HSet(ctx, "friend:A:fillstate",
			"unfilled_since", strconv.FormatInt(unfilledSince, 10),
			"idle_unfilled", "1",
		)
		if _, err := duty.Run(ctx, lease); err != nil {
			t.Fatal(err)
		}
		openB, _ = client.ZRange(ctx, "s:"+sprint+":open:B", 0, -1).Result()
		if len(openB) != 0 {
			t.Fatalf("working dedup failed: task moved to B: %v", openB)
		}

		client.Del(ctx, "friend:B:living")

		// Case 3: Read task is never moved
		client.HSet(ctx, "s:"+sprint+":task:task-move", "kind", "read")
		client.HSet(ctx, "friend:A:fillstate",
			"unfilled_since", strconv.FormatInt(unfilledSince, 10),
			"idle_unfilled", "1",
		)
		if _, err := duty.Run(ctx, lease); err != nil {
			t.Fatal(err)
		}
		openB, _ = client.ZRange(ctx, "s:"+sprint+":open:B", 0, -1).Result()
		if len(openB) != 0 {
			t.Fatalf("read task should never move to B: %v", openB)
		}
	})

	t.Run("C7", func(t *testing.T) {
		addr := testutil.Start(t)
		client := redis.NewClient(&redis.Options{Addr: addr})
		t.Cleanup(func() { _ = client.Close() })
		ctx := context.Background()
		if err := fn.Load(ctx, client); err != nil {
			t.Fatal(err)
		}

		st, err := store.Open(ctx, addr)
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()

		sprint := "s1"
		client.SAdd(ctx, "sprints", sprint)
		client.ZAdd(ctx, "sprint:order", redis.Z{Score: 1, Member: sprint})
		client.HSet(ctx, "s:"+sprint, "status", "open")
		client.HSet(ctx, "s:"+sprint+":policy", "share", "1", "backpressure_missing", "open")

		client.SAdd(ctx, "friends", "r1")
		client.HSet(ctx, "friend:r1:desired", "slots", "2")
		client.HSet(ctx, "friend:r1:beat", "at", "1")

		timeVal, err := client.Time(ctx).Result()
		if err != nil {
			t.Fatal(err)
		}
		nowMs := timeVal.UnixMilli()

		client.ZAdd(ctx, "friend:r1:living",
			redis.Z{Score: float64(nowMs), Member: sprint + "/r-live1/1"},
			redis.Z{Score: float64(nowMs), Member: sprint + "/r-live2/1"},
		)

		client.ZAdd(ctx, "s:"+sprint+":open:r1", redis.Z{Score: 1, Member: "read-task"})
		client.HSet(ctx, "s:"+sprint+":task:read-task",
			"state", "open", "kind", "read", "repo", "nova-tools", "pr", "10", "head", "abcdef")

		lease, err := reconcile.Acquire(ctx, st, reconcile.AcquireOptions{Host: "test", Instance: "c7"})
		if err != nil {
			t.Fatal(err)
		}
		duty := &Duty{Store: st, Policy: Policy{Readers: []string{"r1"}}}
		if _, err := duty.Run(ctx, lease); err != nil {
			t.Fatal(err)
		}

		v, err := deal.ReadBackpressure(ctx, st, sprint)
		if err != nil {
			t.Fatalf("ReadBackpressure failed: %v", err)
		}
		if !v.Blocked {
			t.Fatalf("ReadBackpressure Blocked = false; want true (READ-BOUND)")
		}

		client.HSet(ctx, "s:"+sprint+":card:card-bulk",
			"state", "queued", "priority", "1", "tier", deal.TierBulk,
			"repo", "nova-tools", "pr", "10", "base_sha", "12345678",
		)
		client.ZAdd(ctx, "s:"+sprint+":pool", redis.Z{Score: 1, Member: "card-bulk"})
		client.SAdd(ctx, "s:"+sprint+":idx:card:queued", "card-bulk")

		in, err := (&deal.RedisSource{Client: client}).Read(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(in.Sprints) != 1 || !in.Sprints[0].Backpressure {
			t.Fatalf("sprint Backpressure = %v; want true", in.Sprints[0].Backpressure)
		}
		batches := deal.Plan(in, 100)
		for _, b := range batches {
			for _, c := range b.Cards {
				if c.Tier != deal.TierPriority {
					t.Fatalf("deal pass cut bulk card %+v while READ-BOUND", c)
				}
			}
		}
	})

	t.Run("C8", func(t *testing.T) {
		addr := testutil.Start(t)
		client := redis.NewClient(&redis.Options{Addr: addr})
		t.Cleanup(func() { _ = client.Close() })
		ctx := context.Background()
		if err := fn.Load(ctx, client); err != nil {
			t.Fatal(err)
		}

		st, err := store.Open(ctx, addr)
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()

		timeVal, err := client.Time(ctx).Result()
		if err != nil {
			t.Fatal(err)
		}
		nowMs := timeVal.UnixMilli()

		// Some friends working, one friend with slots 8 and leased 0: fleet deficit counts its 8
		client.SAdd(ctx, "friends", "w1", "free1")
		client.HSet(ctx, "friend:w1:desired", "slots", "4")
		client.HSet(ctx, "friend:free1:desired", "slots", "8")
		for i := 1; i <= 4; i++ {
			client.ZAdd(ctx, "friend:w1:living", redis.Z{Score: float64(nowMs), Member: fmt.Sprintf("m%d", i)})
		}

		lease, err := reconcile.Acquire(ctx, st, reconcile.AcquireOptions{Host: "test", Instance: "c8"})
		if err != nil {
			t.Fatal(err)
		}

		duty := &Duty{Store: st}
		if _, err := duty.Run(ctx, lease); err != nil {
			t.Fatal(err)
		}

		fsW1, _, _ := ReadFillstate(ctx, st, "w1")
		fsFree1, _, _ := ReadFillstate(ctx, st, "free1")
		if fsW1.Deficit != 0 {
			t.Errorf("w1 deficit = %d; want 0", fsW1.Deficit)
		}
		if fsFree1.Deficit != 8 {
			t.Errorf("free1 deficit = %d; want 8", fsFree1.Deficit)
		}
		fleetDeficit := fsW1.Deficit + fsFree1.Deficit
		if fleetDeficit != 8 {
			t.Errorf("fleet deficit = %d; want 8", fleetDeficit)
		}

		// width on a fillstate hash 4 s old prints ?
		staleFS := Fillstate{
			Friend: "stale_friend",
			Slots:  8,
			At:     nowMs - 4000,
		}
		line := staleFS.Line(nowMs)
		want := "WIDTH stale_friend slots=? starting=? living=? leased=? working=? deficit=? eligible=? idle=? peak=?@? at=?"
		if line != want {
			t.Errorf("stale line = %q; want %q", line, want)
		}
	})

	t.Run("C9", func(t *testing.T) {
		addr := testutil.Start(t)
		client := redis.NewClient(&redis.Options{Addr: addr})
		t.Cleanup(func() { _ = client.Close() })
		ctx := context.Background()
		if err := fn.Load(ctx, client); err != nil {
			t.Fatal(err)
		}

		sprint := "s1"
		client.SAdd(ctx, "sprints", sprint)
		client.ZAdd(ctx, "sprint:order", redis.Z{Score: 1, Member: sprint})
		client.HSet(ctx, "s:"+sprint, "status", "open")

		// 1 friend fixture
		client.SAdd(ctx, "friends", "f1")
		client.HSet(ctx, "friend:f1:desired", "slots", "10")
		client.HSet(ctx, "friend:f1:beat", "at", "1")
		client.ZAdd(ctx, "friend:f1:starting", redis.Z{Score: 1, Member: "start1"})
		client.ZAdd(ctx, "friend:f1:living", redis.Z{Score: float64(time.Now().UnixMilli()), Member: "live1"})
		client.ZAdd(ctx, "s:"+sprint+":open:f1", redis.Z{Score: 1, Member: "t1"})
		client.HSet(ctx, "s:"+sprint+":task:t1", "state", "open")

		hook := &cmdHook{}
		client.AddHook(hook)

		st, err := store.Open(ctx, addr)
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()

		lease1, err := reconcile.Acquire(ctx, st, reconcile.AcquireOptions{Host: "test", Instance: "c9-1"})
		if err != nil {
			t.Fatal(err)
		}

		duty := &Duty{Store: st}
		if _, err := duty.Run(ctx, lease1); err != nil {
			t.Fatal(err)
		}

		trips1 := hook.tripsBeforeWrite
		if trips1 > 3 {
			t.Fatalf("1 friend round trips before write = %d; want at most 3", trips1)
		}
		if hook.unpipelinedHGet != 0 || hook.unpipelinedZCard != 0 {
			t.Fatalf("unpipelined calls for 1 friend: HGET=%d ZCARD=%d; want 0", hook.unpipelinedHGet, hook.unpipelinedZCard)
		}

		gw1, err := task.GetWidth(ctx, st, "f1")
		if err != nil {
			t.Fatal(err)
		}
		fs1, _, _ := ReadFillstate(ctx, st, "f1")
		if fs1.Slots != gw1.Desired || fs1.Starting != gw1.Starting || fs1.Living != gw1.Living || fs1.Leased != gw1.Leased || fs1.Deficit != gw1.Free {
			t.Fatalf("f1 fillstate %+v != GetWidth %+v", fs1, gw1)
		}

		// 8 friends fixture on fresh redis instance
		addr8 := testutil.Start(t)
		client8 := redis.NewClient(&redis.Options{Addr: addr8})
		t.Cleanup(func() { _ = client8.Close() })
		if err := fn.Load(ctx, client8); err != nil {
			t.Fatal(err)
		}

		client8.SAdd(ctx, "sprints", sprint)
		client8.ZAdd(ctx, "sprint:order", redis.Z{Score: 1, Member: sprint})
		client8.HSet(ctx, "s:"+sprint, "status", "open")

		for i := 1; i <= 8; i++ {
			f := fmt.Sprintf("friend%d", i)
			client8.SAdd(ctx, "friends", f)
			client8.HSet(ctx, "friend:"+f+":desired", "slots", strconv.Itoa(i*2))
			client8.HSet(ctx, "friend:"+f+":beat", "at", "1")
			client8.ZAdd(ctx, "friend:"+f+":starting", redis.Z{Score: 1, Member: "start_" + f})
			client8.ZAdd(ctx, "friend:"+f+":living", redis.Z{Score: float64(time.Now().UnixMilli()), Member: "live_" + f})
			client8.ZAdd(ctx, "s:"+sprint+":open:"+f, redis.Z{Score: 1, Member: "task_" + f})
			client8.HSet(ctx, "s:"+sprint+":task:task_"+f, "state", "open")
		}

		hook8 := &cmdHook{}
		client8.AddHook(hook8)

		st8, err := store.Open(ctx, addr8)
		if err != nil {
			t.Fatal(err)
		}
		defer st8.Close()

		lease8, err := reconcile.Acquire(ctx, st8, reconcile.AcquireOptions{Host: "test", Instance: "c9-8"})
		if err != nil {
			t.Fatal(err)
		}

		duty8 := &Duty{Store: st8}
		if _, err := duty8.Run(ctx, lease8); err != nil {
			t.Fatal(err)
		}

		trips8 := hook8.tripsBeforeWrite
		if trips8 != trips1 {
			t.Fatalf("8 friends round trips before write = %d; want %d (same as 1 friend)", trips8, trips1)
		}
		if trips8 > 3 {
			t.Fatalf("8 friends round trips before write = %d; want at most 3", trips8)
		}
		if hook8.unpipelinedHGet != 0 || hook8.unpipelinedZCard != 0 {
			t.Fatalf("unpipelined calls for 8 friends: HGET=%d ZCARD=%d; want 0", hook8.unpipelinedHGet, hook8.unpipelinedZCard)
		}

		for i := 1; i <= 8; i++ {
			f := fmt.Sprintf("friend%d", i)
			gw, err := task.GetWidth(ctx, st8, f)
			if err != nil {
				t.Fatal(err)
			}
			fs, _, _ := ReadFillstate(ctx, st8, f)
			if fs.Slots != gw.Desired || fs.Starting != gw.Starting || fs.Living != gw.Living || fs.Leased != gw.Leased || fs.Deficit != gw.Free {
				t.Fatalf("%s fillstate %+v != GetWidth %+v", f, fs, gw)
			}
		}
	})
}
