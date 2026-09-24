package width

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

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
