package width

import (
	"context"
	"errors"
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
		defer func() { _ = st.Close() }()

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
			client.HSet(ctx, "task:"+id, "state", "open", "kind", "work", "ref", id)
		}

		res, err := task.WidthFill(ctx, st, "f1", sprint, 0, "f1", "")
		if err != nil {
			t.Fatalf("task.WidthFill failed: %v", err)
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

		res2, err := task.WidthFill(ctx, st, "f1", sprint, 0, "f1", "")
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
		defer func() { _ = st.Close() }()

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
			client.HSet(ctx, "task:"+id, "state", "open", "kind", "work", "ref", id)
		}

		type fillOut struct {
			res task.FillResult
			err error
		}
		ch := make(chan fillOut, 2)
		for i := 0; i < 2; i++ {
			go func() {
				r, e := task.WidthFill(ctx, st, "f1", sprint, 0, "f1", "")
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
		defer func() { _ = st.Close() }()

		sprint := "s1"
		client.SAdd(ctx, "sprints", sprint)
		client.ZAdd(ctx, "sprint:order", redis.Z{Score: 1, Member: sprint})
		client.HSet(ctx, "s:"+sprint, "status", "open")

		client.SAdd(ctx, "friends", "f1")
		client.HSet(ctx, "friend:f1:desired", "slots", "4")
		client.HSet(ctx, "friend:f1:beat", "at", "1")

		client.ZAdd(ctx, "s:"+sprint+":open:f1", redis.Z{Score: 1, Member: "t1"})
		client.HSet(ctx, "task:t1", "state", "open", "kind", "work", "ref", "t1")

		res, err := task.WidthFill(ctx, st, "f1", sprint, 0, "f1", "")
		if err != nil || res.N != 1 {
			t.Fatalf("fill: res=%+v err=%v", res, err)
		}

		timeVal, err := client.Time(ctx).Result()
		if err != nil {
			t.Fatal(err)
		}
		nowMs := timeVal.UnixMilli()

		client.HSet(ctx, "task:t1", "claimed_at", strconv.FormatInt(nowMs-61000, 10))

		reply, err := client.FCall(ctx, "ns_task_expire", nil, sprint, "t1", "reconciler", "").Result()
		if err != nil {
			t.Fatalf("expire failed: %v", err)
		}
		vals := reply.([]any)
		if vals[0] != "REOPENED" {
			t.Fatalf("expire reply = %v; want REOPENED", vals)
		}

		state := client.HGet(ctx, "task:t1", "state").Val()
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
		defer func() { _ = st.Close() }()

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
		client.ZAdd(ctx, "friend:A:cards:working",
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
		client.HSet(ctx, "task:e1", "state", "open", "depends_on", "")
		client.HSet(ctx, "task:e2", "state", "open", "depends_on", "")

		for i := 1; i <= 10; i++ {
			id := fmt.Sprintf("d%d", i)
			client.ZAdd(ctx, "s:"+sprint+":open:A", redis.Z{Score: float64(i + 2), Member: id})
			client.HSet(ctx, "task:"+id, "state", "open", "depends_on", "dep_task")
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
		// through the one task move (#3778): a hand write would be drift
		for i, id := range []string{"e1", "e2"} {
			if got := client.FCall(ctx, "ns_tcard_move", nil, id, "ready", "test", "back to A", "friend", "A",
				"sprint", sprint, "state", "open", "qscore", strconv.Itoa(i+1)).Val(); !strings.HasPrefix(fmt.Sprint(got), "MOVED") {
				t.Fatalf("move %s back to A: %v", id, got)
			}
		}
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
			client.HSet(ctx, "task:"+id, "state", "open", "depends_on", "")
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
			client.Del(ctx, "task:"+id)
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
		defer func() { _ = st.Close() }()

		sprint := "s1"
		client.SAdd(ctx, "sprints", sprint)
		client.ZAdd(ctx, "sprint:order", redis.Z{Score: 1, Member: sprint})
		client.HSet(ctx, "s:"+sprint, "status", "open")

		client.SAdd(ctx, "friends", "f1")
		client.HSet(ctx, "friend:f1:desired", "slots", "4")
		client.HSet(ctx, "friend:f1:beat", "at", "1")

		client.ZAdd(ctx, "s:"+sprint+":open:f1", redis.Z{Score: 1, Member: "t-deps"})
		client.HSet(ctx, "task:t-deps",
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

		res, err := task.WidthFill(ctx, st, "f1", sprint, 0, "f1", "")
		if err != nil {
			t.Fatalf("task.WidthFill failed: %v", err)
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
		defer func() { _ = st.Close() }()

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
		client.HSet(ctx, "task:task-move",
			"state", "open", "kind", "work", "repo", repo, "pr", pr, "head", head, "depends_on", "")

		lease, err := reconcile.Acquire(ctx, st, reconcile.AcquireOptions{Host: "test", Instance: "c6"})
		if err != nil {
			t.Fatal(err)
		}

		// Case 1: B holds closed task for same (repo, PR, head)
		client.SAdd(ctx, "s:"+sprint+":done:B", "task-closed")
		client.HSet(ctx, "task:task-closed",
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
		client.ZAdd(ctx, "friend:B:cards:working", redis.Z{Score: float64(nowMs), Member: sprint + "/task-working/1"})
		client.HSet(ctx, "task:task-working",
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

		client.Del(ctx, "friend:B:cards:working")

		// Case 3: Read task is never moved
		client.HSet(ctx, "task:task-move", "kind", "read")
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
		defer func() { _ = st.Close() }()

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

		client.ZAdd(ctx, "friend:r1:cards:working",
			redis.Z{Score: float64(nowMs), Member: sprint + "/r-live1/1"},
			redis.Z{Score: float64(nowMs), Member: sprint + "/r-live2/1"},
		)

		client.ZAdd(ctx, "s:"+sprint+":open:r1", redis.Z{Score: 1, Member: "read-task"})
		client.HSet(ctx, "task:read-task",
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
		defer func() { _ = st.Close() }()

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
			client.ZAdd(ctx, "friend:w1:cards:working", redis.Z{Score: float64(nowMs), Member: fmt.Sprintf("m%d", i)})
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
		want := "WIDTH stale_friend slots=? leased=? working=? deficit=? eligible=? idle=? peak=?@? at=?"
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
		client.ZAdd(ctx, "friend:f1:cards:working", redis.Z{Score: 1, Member: "start1"})
		client.ZAdd(ctx, "friend:f1:cards:working", redis.Z{Score: float64(time.Now().UnixMilli()), Member: "live1"})
		client.ZAdd(ctx, "s:"+sprint+":open:f1", redis.Z{Score: 1, Member: "t1"})
		client.HSet(ctx, "task:t1", "state", "open")

		hook := &cmdHook{}
		client.AddHook(hook)

		st, err := store.Open(ctx, addr)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = st.Close() }()

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
		if fs1.Slots != gw1.Desired || fs1.Leased != gw1.Leased || fs1.Deficit != gw1.Free {
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
			client8.ZAdd(ctx, "friend:"+f+":cards:working", redis.Z{Score: 1, Member: "start_" + f})
			client8.ZAdd(ctx, "friend:"+f+":cards:working", redis.Z{Score: float64(time.Now().UnixMilli()), Member: "live_" + f})
			client8.ZAdd(ctx, "s:"+sprint+":open:"+f, redis.Z{Score: 1, Member: "task_" + f})
			client8.HSet(ctx, "task:task_"+f, "state", "open")
		}

		hook8 := &cmdHook{}
		client8.AddHook(hook8)

		st8, err := store.Open(ctx, addr8)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = st8.Close() }()

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
			if fs.Slots != gw.Desired || fs.Leased != gw.Leased || fs.Deficit != gw.Free {
				t.Fatalf("%s fillstate %+v != GetWidth %+v", f, fs, gw)
			}
		}
	})
}

// TestWidthMoveRequiresFence tests Defect 1:
// ns_width_move must require the fence token even when empty (""), failing closed with FENCED.
func TestWidthMoveRequiresFence(t *testing.T) {
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
	defer func() { _ = st.Close() }()

	// Calling ns_width_move with an empty fence string must return FENCED, not skip the check.
	res, err := client.FCall(ctx, "ns_width_move", nil, "", "fromF", "toF", "s1", "30000", "width", "").Result()
	if err != nil {
		t.Fatalf("ns_width_move unexpected error: %v", err)
	}
	slice, ok := res.([]any)
	if !ok || len(slice) == 0 || slice[0] != "FENCED" {
		t.Fatalf("ns_width_move with empty fence returned %v; want FENCED", res)
	}
}

type moveRefusalHook struct {
	client *redis.Client
}

func (h *moveRefusalHook) DialHook(next redis.DialHook) redis.DialHook { return next }

func (h *moveRefusalHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		name := strings.ToLower(cmd.Name())
		if name == "fcall" {
			args := cmd.Args()
			if len(args) > 1 && fmt.Sprint(args[1]) == FunctionWrite {
				defer func() {
					_ = h.client.HSet(ctx, "lease:reconciler", "token", "stolen-lease-token").Err()
				}()
			}
		}
		return next(ctx, cmd)
	}
}

func (h *moveRefusalHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		return next(ctx, cmds)
	}
}

// TestWidthDutySurfacesMoveRefusal tests Defect 2:
// width duty must not drop the move reply; it must return ErrFenced on FENCED from ns_width_move.
func TestWidthDutySurfacesMoveRefusal(t *testing.T) {
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
	defer func() { _ = st.Close() }()

	sprint := "s1"
	client.SAdd(ctx, "sprints", sprint)
	client.ZAdd(ctx, "sprint:order", redis.Z{Score: 1, Member: sprint})
	client.HSet(ctx, "s:"+sprint, "status", "open")

	timeVal, err := client.Time(ctx).Result()
	if err != nil {
		t.Fatal(err)
	}
	nowMs := timeVal.UnixMilli()
	rebalAfter := 30 * time.Second

	client.SAdd(ctx, "friends", "A", "B")
	client.HSet(ctx, "friend:A:desired", "slots", "8")
	client.HSet(ctx, "friend:A:beat", "at", "1")
	client.HSet(ctx, "friend:B:desired", "slots", "8")
	client.HSet(ctx, "friend:B:beat", "at", "1")
	client.HSet(ctx, "friend:B:fillstate", "fill_at", strconv.FormatInt(nowMs-5000, 10), "fill_n", "1")

	unfilledSince := nowMs - int64((rebalAfter + time.Second).Milliseconds())
	client.HSet(ctx, "friend:A:fillstate",
		"unfilled_since", strconv.FormatInt(unfilledSince, 10),
		"idle_unfilled", "2",
	)
	client.ZAdd(ctx, "s:"+sprint+":open:A", redis.Z{Score: 1, Member: "e1"})
	client.HSet(ctx, "task:e1", "state", "open", "kind", "work", "depends_on", "")

	lease, err := reconcile.Acquire(ctx, st, reconcile.AcquireOptions{Host: "test", Instance: "c-move-fenced"})
	if err != nil {
		t.Fatal(err)
	}

	// Install hook to mutate lease token after FunctionWrite completes, causing ns_width_move to receive FENCED
	st.Client().AddHook(&moveRefusalHook{client: client})

	duty := &Duty{Store: st, RebalanceAfter: rebalAfter}
	_, err = duty.Run(ctx, lease)
	if !errors.Is(err, reconcile.ErrFenced) {
		t.Fatalf("duty.Run returned err = %v; want ErrFenced", err)
	}
}

type unfencedHook struct {
	unfencedWrites int
}

func (h *unfencedHook) DialHook(next redis.DialHook) redis.DialHook { return next }

func (h *unfencedHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		name := strings.ToLower(cmd.Name())
		args := cmd.Args()
		if len(args) > 1 {
			key := fmt.Sprint(args[1])
			if name == "sadd" && key == "width:readers" {
				h.unfencedWrites++
			}
			if (name == "hset" || name == "hsetnx") && strings.HasSuffix(key, ":backpressure") {
				h.unfencedWrites++
			}
			if name == "set" && key == "sprint:read_bound" {
				h.unfencedWrites++
			}
		}
		return next(ctx, cmd)
	}
}

func (h *unfencedHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		for _, cmd := range cmds {
			name := strings.ToLower(cmd.Name())
			args := cmd.Args()
			if len(args) > 1 {
				key := fmt.Sprint(args[1])
				if name == "sadd" && key == "width:readers" {
					h.unfencedWrites++
				}
				if (name == "hset" || name == "hsetnx") && strings.HasSuffix(key, ":backpressure") {
					h.unfencedWrites++
				}
				if name == "set" && key == "sprint:read_bound" {
					h.unfencedWrites++
				}
			}
		}
		return next(ctx, cmds)
	}
}

// TestWidthReadBoundFencedAtomic tests Defect 3:
// READ-BOUND state must NOT be written outside the fenced Function.
// Unfenced plain client writes to width:readers, s:<S>:backpressure, sprint:read_bound are forbidden.
func TestWidthReadBoundFencedAtomic(t *testing.T) {
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
	defer func() { _ = st.Close() }()

	sprint := "s1"
	client.SAdd(ctx, "sprints", sprint)
	client.ZAdd(ctx, "sprint:order", redis.Z{Score: 1, Member: sprint})
	client.HSet(ctx, "s:"+sprint, "status", "open")

	client.SAdd(ctx, "friends", "r1")
	client.HSet(ctx, "friend:r1:desired", "slots", "2")
	client.HSet(ctx, "friend:r1:beat", "at", "1")

	timeVal, err := client.Time(ctx).Result()
	if err != nil {
		t.Fatal(err)
	}
	nowMs := timeVal.UnixMilli()

	client.ZAdd(ctx, "friend:r1:cards:working",
		redis.Z{Score: float64(nowMs), Member: sprint + "/r-live1/1"},
		redis.Z{Score: float64(nowMs), Member: sprint + "/r-live2/1"},
	)
	client.ZAdd(ctx, "s:"+sprint+":open:r1", redis.Z{Score: 1, Member: "read-task"})
	client.HSet(ctx, "task:read-task",
		"state", "open", "kind", "read", "repo", "nova-tools", "pr", "10", "head", "abcdef")

	hook := &unfencedHook{}
	st.Client().AddHook(hook)

	lease, err := reconcile.Acquire(ctx, st, reconcile.AcquireOptions{Host: "test", Instance: "c-atomic"})
	if err != nil {
		t.Fatal(err)
	}

	duty := &Duty{Store: st, Policy: Policy{Readers: []string{"r1"}}}
	if _, err := duty.Run(ctx, lease); err != nil {
		t.Fatal(err)
	}

	// Must have 0 unfenced writes outside the function
	if hook.unfencedWrites != 0 {
		t.Fatalf("unfenced plain writes = %d; want 0 (must be written inside ns_width_write under the fence)", hook.unfencedWrites)
	}

	// Verify state was correctly written
	isMember, _ := client.SIsMember(ctx, "width:readers", "r1").Result()
	if !isMember {
		t.Fatalf("width:readers missing r1")
	}
	bp, _ := client.HGetAll(ctx, "s:"+sprint+":backpressure").Result()
	if bp["read_bound"] != "1" {
		t.Fatalf("s:%s:backpressure read_bound = %q; want 1", sprint, bp["read_bound"])
	}
	rb, _ := client.Get(ctx, "sprint:read_bound").Result()
	if rb != "1" {
		t.Fatalf("sprint:read_bound = %q; want 1", rb)
	}
}

// TestWidthMoveConsumesSpare is the #3484 hold 1 control: sequential
// ns_width_move calls in one pass (two senders, two sprints) never move more
// tasks to a recipient than its original spare (deficit - eligible).
func TestWidthMoveConsumesSpare(t *testing.T) {
	addr := testutil.Start(t)
	client := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = client.Close() })
	ctx := context.Background()
	if err := fn.Load(ctx, client); err != nil {
		t.Fatal(err)
	}
	tv, err := client.Time(ctx).Result()
	if err != nil {
		t.Fatal(err)
	}
	now := tv.UnixMilli()
	client.HSet(ctx, "lease:reconciler", "token", "tok")
	for _, s := range []string{"s1", "s2"} {
		client.SAdd(ctx, "sprints", s)
		client.HSet(ctx, "s:"+s, "status", "open")
	}
	for _, f := range []string{"A", "B", "C"} {
		client.SAdd(ctx, "friends", f)
		client.HSet(ctx, "friend:"+f+":beat", "at", "1")
	}
	for _, f := range []string{"A", "C"} {
		client.HSet(ctx, "friend:"+f+":fillstate", "idle_unfilled", "5", "unfilled_since", strconv.FormatInt(now-60000, 10))
	}
	// B: deficit 3, eligible 1 -> spare 2.
	client.HSet(ctx, "friend:B:fillstate", "deficit", "3", "eligible", "1", "fill_at", strconv.FormatInt(now, 10))
	seed := func(s, f string, n int) {
		for i := 0; i < n; i++ {
			id := fmt.Sprintf("%s-%s-%d", f, s, i)
			client.HSet(ctx, "task:"+id, "state", "open", "kind", "work", "owner", f)
			client.ZAdd(ctx, "s:"+s+":open:"+f, redis.Z{Score: float64(i), Member: id})
		}
	}
	seed("s1", "A", 2)
	seed("s2", "A", 2)
	seed("s1", "C", 2)

	total := 0
	for _, c := range [][2]string{{"A", "s1"}, {"A", "s2"}, {"C", "s1"}} {
		res, err := client.FCall(ctx, "ns_width_move", nil, "tok", c[0], "B", c[1], "30000", "width", "").Result()
		if err != nil {
			t.Fatalf("move %s->B %s: %v", c[0], c[1], err)
		}
		sl, ok := res.([]any)
		if !ok || len(sl) < 2 || sl[0] != "OK" {
			t.Fatalf("move %s->B %s: reply %v", c[0], c[1], res)
		}
		n, _ := strconv.Atoi(fmt.Sprint(sl[1]))
		total += n
	}
	got := int(client.ZCard(ctx, "s:s1:open:B").Val() + client.ZCard(ctx, "s:s2:open:B").Val())
	if total != 2 || got != 2 {
		t.Fatalf("recipient spare 2: moves reported %d, B holds %d; want 2 and 2", total, got)
	}
	if e := client.HGet(ctx, "friend:B:fillstate", "eligible").Val(); e != "3" {
		t.Fatalf("B eligible after moves = %q; want 3 (1 + 2 received)", e)
	}
	if u := client.HGet(ctx, "friend:A:fillstate", "idle_unfilled").Val(); u != "3" {
		t.Fatalf("A idle_unfilled after giving 2 = %q; want 3", u)
	}
}

// TestWidthFillNoPredictableToken is the #3484 hold 2 control: an unbounded
// fill over 65 ready tasks never mints a token from time/index/slots/deficit.
// Every claim's suffix is 32 hex from the caller's random parts; a claim with
// no random part left is not made; a malformed part refuses the call.
func TestWidthFillNoPredictableToken(t *testing.T) {
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
	defer func() { _ = st.Close() }()
	client.HSet(ctx, "s:s1", "status", "open")
	client.SAdd(ctx, "sprints", "s1")
	client.SAdd(ctx, "friends", "f1")
	client.HSet(ctx, "friend:f1:desired", "slots", "70")
	client.HSet(ctx, "friend:f1:beat", "at", "1")
	for i := 0; i < 65; i++ {
		id := fmt.Sprintf("t%02d", i)
		client.HSet(ctx, "task:"+id, "state", "open", "kind", "work", "owner", "f1")
		client.ZAdd(ctx, "s:s1:open:f1", redis.Z{Score: float64(i), Member: id})
	}

	// Malformed part: refused before any write.
	if _, err := client.FCall(ctx, "ns_width_fill", nil, "f1", "s1", "0", "f1", "", "0000000000000001").Result(); err == nil {
		t.Fatal("fill with a 16-hex part: want an error, got none")
	}
	if n := client.ZCard(ctx, "s:s1:open:f1").Val(); n != 65 {
		t.Fatalf("after refused fill, open = %d; want 65", n)
	}

	// Fail closed: an unbounded call with 3 parts over 65 ready claims 3.
	args := []any{"f1", "s1", "0", "f1", ""}
	for i := 0; i < 3; i++ {
		p, err := task.RandomToken()
		if err != nil {
			t.Fatal(err)
		}
		args = append(args, p)
	}
	r1, err := client.FCall(ctx, "ns_width_fill", nil, args...).Result()
	if err != nil {
		t.Fatal(err)
	}
	v := r1.([]any)
	if fmt.Sprint(v[1]) != "3" {
		t.Fatalf("fill with 3 parts claimed %v; want 3 (no claim without a part)", v[1])
	}
	for i := 3; i+3 < len(v); i += 4 {
		tok := fmt.Sprint(v[i+3])
		parts := strings.SplitN(tok, ".", 2)
		if len(parts) != 2 || len(parts[1]) != 32 || strings.Trim(parts[1], "0123456789abcdef") != "" {
			t.Fatalf("claim token %q: want <attempt>.<32 hex>", tok)
		}
	}

	// task.WidthFill supplies one part per claim: the other 62 all claim.
	res, err := task.WidthFill(ctx, st, "f1", "s1", 0, "f1", "")
	if err != nil {
		t.Fatal(err)
	}
	if res.N != 62 || len(res.Tasks) != 62 {
		t.Fatalf("fill n=%d tasks=%d; want the remaining 62", res.N, len(res.Tasks))
	}
	for i, tk := range res.Tasks {
		parts := strings.SplitN(tk.Token, ".", 2)
		if len(parts) != 2 || len(parts[1]) != 32 || strings.Trim(parts[1], "0123456789abcdef") != "" {
			t.Fatalf("claim %d token %q: want <attempt>.<32 hex>", i+1, tk.Token)
		}
	}
	if n := client.ZCard(ctx, "s:s1:open:f1").Val(); n != 0 {
		t.Fatalf("after 65 claims, open = %d; want 0", n)
	}
}

// TestWidthFillSizesPartsToClaims is the recut-3071 fill control: task.WidthFill
// sizes its random parts to the claim count (min(slots, max)), so an
// unbounded fill of 65 ready tasks claims all 65, not 64, and every claim's
// suffix is a distinct 32-hex part. A bounded fill supplies max parts.
func TestWidthFillSizesPartsToClaims(t *testing.T) {
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
	defer func() { _ = st.Close() }()
	client.HSet(ctx, "s:s1", "status", "open")
	client.SAdd(ctx, "sprints", "s1")
	for _, f := range []string{"f1", "f2"} {
		client.SAdd(ctx, "friends", f)
		client.HSet(ctx, "friend:"+f+":desired", "slots", "70")
		client.HSet(ctx, "friend:"+f+":beat", "at", "1")
		for i := 0; i < 65; i++ {
			id := fmt.Sprintf("%s-t%02d", f, i)
			client.HSet(ctx, "task:"+id, "state", "open", "kind", "work", "owner", f)
			client.ZAdd(ctx, "s:s1:open:"+f, redis.Z{Score: float64(i), Member: id})
		}
	}

	res, err := task.WidthFill(ctx, st, "f1", "s1", 0, "f1", "")
	if err != nil {
		t.Fatal(err)
	}
	if res.N != 65 || len(res.Tasks) != 65 {
		t.Fatalf("unbounded fill of 65: n=%d tasks=%d; want 65 claims", res.N, len(res.Tasks))
	}
	seen := map[string]bool{}
	for i, tk := range res.Tasks {
		parts := strings.SplitN(tk.Token, ".", 2)
		if len(parts) != 2 || len(parts[1]) != 32 || strings.Trim(parts[1], "0123456789abcdef") != "" {
			t.Fatalf("claim %d token %q: want <attempt>.<32 hex>", i+1, tk.Token)
		}
		if seen[parts[1]] {
			t.Fatalf("claim %d reuses random part %s", i+1, parts[1])
		}
		seen[parts[1]] = true
	}
	if n := client.ZCard(ctx, "s:s1:open:f1").Val(); n != 0 {
		t.Fatalf("after unbounded fill, f1 open = %d; want 0", n)
	}

	bounded, err := task.WidthFill(ctx, st, "f2", "s1", 3, "f2", "")
	if err != nil {
		t.Fatal(err)
	}
	if bounded.N != 3 || len(bounded.Tasks) != 3 {
		t.Fatalf("fill --max 3: n=%d tasks=%d; want 3", bounded.N, len(bounded.Tasks))
	}
}
