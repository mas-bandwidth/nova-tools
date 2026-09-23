package width_test

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/reconcile"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/task"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/width"
	"github.com/redis/go-redis/v9"
)

// startRedis starts a throwaway redis-server, the same shape as the task
// package's controls: skip with the reason when the binary is absent.
func startRedis(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("redis-server"); err != nil {
		t.Skipf("redis-server unavailable; run this control on a Redis bench: %v", err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	log, err := os.Create(filepath.Join(dir, "redis.log"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = log.Close() })
	port := strings.TrimPrefix(addr, "127.0.0.1:")
	cmd := exec.Command("redis-server", "--bind", "127.0.0.1", "--port", port,
		"--save", "", "--appendonly", "no", "--dir", dir)
	cmd.Stdout, cmd.Stderr = log, log
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	client := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = client.Close() })
	deadline := time.Now().Add(30 * time.Second)
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		err := client.Ping(ctx).Err()
		cancel()
		if err == nil {
			return addr
		}
		if time.Now().After(deadline) {
			t.Fatalf("throwaway redis did not start: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

const sprint = "control-width"

func controlRedis(t *testing.T) (*store.Store, *redis.Client) {
	t.Helper()
	addr := startRedis(t)
	client := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = client.Close() })
	ctx := context.Background()
	if err := fn.Load(ctx, client); err != nil {
		t.Fatalf("load nova_sprint library: %v", err)
	}
	client.HSet(ctx, "s:"+sprint, "status", "open")
	client.ZAdd(ctx, "sprint:order", redis.Z{Score: 1, Member: sprint})
	return store.New(client), client
}

func seedFriend(t *testing.T, client *redis.Client, friend string, slots int) {
	t.Helper()
	ctx := context.Background()
	if err := client.SAdd(ctx, "friends", friend).Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.HSet(ctx, "friend:"+friend+":desired", "slots", slots, "paused", "0").Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.HSet(ctx, "friend:"+friend+":beat", "host", "fixture").Err(); err != nil {
		t.Fatal(err)
	}
}

func push(t *testing.T, st *store.Store, to, id string, kind task.Kind) {
	t.Helper()
	req := task.PushRequest{Sprint: sprint, ID: id, Kind: kind, Title: "task " + id,
		Effects: task.EffectsNone, To: to, Ref: "briefs/" + id + ".md"}
	if kind == task.KindRead {
		req.Repo, req.PR, req.Head = "nova-tools", 9001, strings.Repeat("a", 40)
		if err := st.Client().HSet(context.Background(), "s:"+sprint+":pr:nova-tools:9001", "head", req.Head).Err(); err != nil {
			t.Fatal(err)
		}
	}
	if got, err := task.Push(context.Background(), st, req); err != nil || got != task.PushCreated {
		t.Fatalf("push %s = %s, %v; want CREATED", id, got, err)
	}
}

func pushN(t *testing.T, st *store.Store, to, prefix string, n int) []string {
	t.Helper()
	ids := make([]string, n)
	for i := range ids {
		ids[i] = fmt.Sprintf("%s%02d", prefix, i+1)
		push(t, st, to, ids[i], task.KindWork)
	}
	return ids
}

// leases holds each control store's lease:reconciler: the tick is fenced, so
// a control ticks as the reconciler would, with the holder's token.
var leases sync.Map // *store.Store -> *reconcile.Lease

func lease(t *testing.T, st *store.Store) *reconcile.Lease {
	t.Helper()
	if l, ok := leases.Load(st); ok {
		return l.(*reconcile.Lease)
	}
	l, err := reconcile.Acquire(context.Background(), st, reconcile.AcquireOptions{Host: "fixture", TTL: time.Minute})
	if err != nil {
		t.Fatalf("acquire lease:reconciler: %v", err)
	}
	leases.Store(st, l)
	t.Cleanup(func() { leases.Delete(st) })
	return l
}

func tick(t *testing.T, st *store.Store, p width.Policy) width.Result {
	t.Helper()
	res, err := width.Tick(context.Background(), st, p, lease(t, st).Token(), "control", "")
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	return res
}

func row(t *testing.T, res width.Result, friend string) width.Row {
	t.Helper()
	for _, r := range res.Rows {
		if r.Friend == friend {
			return r
		}
	}
	t.Fatalf("no width row for %s in %+v", friend, res.Rows)
	return width.Row{}
}

// ackLauncher is a harness whose child ACKs at once: Launch sends the
// child's first beat (claimed -> working).
type ackLauncher struct {
	st        *store.Store
	preflight error
	mu        sync.Mutex
	live      map[string]width.Reserved
}

func newAck(st *store.Store) *ackLauncher {
	return &ackLauncher{st: st, live: map[string]width.Reserved{}}
}

func (l *ackLauncher) Preflight(context.Context) error { return l.preflight }

func (l *ackLauncher) Launch(ctx context.Context, r width.Reserved) error {
	got, err := task.Beat(ctx, l.st, task.BeatRequest{Sprint: r.Sprint, ID: r.ID, Token: r.Claim.Token, Actor: "child"})
	if err != nil || got != task.BeatWorking {
		return fmt.Errorf("child ack %s = %s, %v", r.ID, got, err)
	}
	l.mu.Lock()
	l.live[r.ID] = r
	l.mu.Unlock()
	return nil
}

// finish closes n live children (a child completion) in id order.
func (l *ackLauncher) finish(t *testing.T, n int) {
	t.Helper()
	l.mu.Lock()
	defer l.mu.Unlock()
	ids := make([]string, 0, len(l.live))
	for id := range l.live {
		ids = append(ids, id)
	}
	sortStrings(ids)
	for _, id := range ids[:n] {
		r := l.live[id]
		got, err := task.Done(context.Background(), l.st, task.DoneRequest{
			Sprint: r.Sprint, ID: r.ID, Token: r.Claim.Token, Evidence: "https://example.test/" + r.ID,
		})
		if err != nil || got != task.DoneClosed {
			t.Fatalf("done %s = %s, %v", id, got, err)
		}
		delete(l.live, id)
	}
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

func refill(t *testing.T, st *store.Store, friend string, l width.Launcher) width.RefillResult {
	t.Helper()
	res, err := width.Refill(context.Background(), st, friend, l, "harness", "")
	if err != nil {
		t.Fatalf("refill %s: %v", friend, err)
	}
	return res
}

// TestControlA_RefillReachesSlotsInTwoTicksAndHolds is #3071 control (a):
// slots 8, 20 open ready tasks, a harness that takes: working reaches 8
// within 2 ticks and stays 8 until the remaining work is under 8.
func TestControlA_RefillReachesSlotsInTwoTicksAndHolds(t *testing.T) {
	st, client := controlRedis(t)
	seedFriend(t, client, "fa", 8)
	pushN(t, st, "fa", "a", 20)
	h := newAck(st)
	p := width.Policy{RebalanceTicks: 3}

	// Tick 1 sees the deficit and the harness takes; by tick 2 the children
	// have ACKed and working is 8.
	if r := row(t, tick(t, st, p), "fa"); r.Working != 0 || r.Deficit != 8 {
		t.Fatalf("tick 1: %s; want 0/8 deficit 8", r.Line())
	}
	refill(t, st, "fa", h)
	if r := row(t, tick(t, st, p), "fa"); r.Working != 8 || r.Desired != 8 {
		t.Fatalf("tick 2: working = %d; want 8 (%s)", r.Working, r.Line())
	}

	remaining := 20
	for remaining > 0 {
		h.finish(t, 1)
		remaining--
		refill(t, st, "fa", h)
		r := row(t, tick(t, st, p), "fa")
		want := 8
		if remaining < 8 {
			want = remaining
		}
		if r.Working != want {
			t.Fatalf("remaining %d: working = %d; want %d (%s)", remaining, r.Working, want, r.Line())
		}
	}
}

// cappedHarness takes and ACKs up to max children, never more, whatever the
// declared slots say (Johnny's grok harness at 8, Stella's ChatGPT at 2).
func cappedTake(t *testing.T, st *store.Store, friend string, max int, h *ackLauncher) {
	t.Helper()
	ctx := context.Background()
	for {
		h.mu.Lock()
		n := len(h.live)
		h.mu.Unlock()
		if n >= max {
			return
		}
		gen, _, ids, err := width.ReadyIDs(ctx, st, friend)
		if err != nil {
			t.Fatal(err)
		}
		if len(ids) == 0 {
			return
		}
		claim, _, err := width.Reserve(ctx, st, ids[0], friend, gen, "harness", "")
		if err != nil {
			t.Fatalf("capped harness reserve: %v", err)
		}
		if err := h.Launch(ctx, width.Reserved{Ready: ids[0], Claim: claim}); err != nil {
			t.Fatal(err)
		}
	}
}

// TestControlB_CapAndUnderfullRebalance is #3071 control (b): a harness that
// never exceeds 3: after rebalance_ticks slots=3 is written, and 5 of the
// builds move to the friend with free width with [moved from f: underfull].
// The read on f's queue stays, and f's WORKING tasks never move (Stella:
// transfer during WORKING).
func TestControlB_CapAndUnderfullRebalance(t *testing.T) {
	st, client := controlRedis(t)
	ctx := context.Background()
	seedFriend(t, client, "fb", 8)
	seedFriend(t, client, "gb", 8)
	builds := pushN(t, st, "fb", "b", 8)
	push(t, st, "fb", "zread", task.KindRead)
	h := newAck(st)
	p := width.Policy{RebalanceTicks: 2}

	var events []string
	var capTick int
	for i := 1; i <= 6 && capTick == 0; i++ {
		cappedTake(t, st, "fb", 3, h)
		res := tick(t, st, p)
		events = append(events, res.Events...)
		for _, e := range res.Events {
			if strings.HasPrefix(e, "CAP fb") {
				capTick = i
			}
		}
	}
	if capTick == 0 {
		t.Fatalf("no CAP within 6 ticks; events %q", events)
	}
	if !contains(events, "CAP fb slots=3 measured=3") {
		t.Fatalf("events %q; want CAP fb slots=3 measured=3", events)
	}
	if got, _ := client.HGet(ctx, width.Key("fb"), "slots").Result(); got != "3" {
		t.Fatalf("friend:fb:width slots = %q; want 3", got)
	}
	moves := 0
	for _, e := range events {
		if strings.HasPrefix(e, "MOVE ") {
			moves++
			if !strings.HasSuffix(e, "from=fb to=gb reason=underfull") {
				t.Fatalf("move %q; want from=fb to=gb reason=underfull", e)
			}
		}
	}
	if moves != 5 {
		t.Fatalf("moves = %d; want 5 (events %q)", moves, events)
	}
	onG, _ := client.ZRange(ctx, "s:"+sprint+":open:gb", 0, -1).Result()
	if len(onG) != 5 {
		t.Fatalf("gb queue = %v; want 5 builds", onG)
	}
	for _, id := range onG {
		title, _ := client.HGet(ctx, "s:"+sprint+":task:"+id, "title").Result()
		if !strings.Contains(title, "[moved from fb: underfull]") {
			t.Fatalf("moved %s title %q lacks the marker", id, title)
		}
		if id == "zread" {
			t.Fatal("a read moved; reads stay with their readers")
		}
	}
	// The three WORKING builds stay with fb, leases intact.
	for _, id := range builds[:3] {
		state, _ := client.HGet(ctx, "s:"+sprint+":task:"+id, "state").Result()
		owner, _ := client.HGet(ctx, "s:"+sprint+":task:"+id, "owner").Result()
		if state != "working" || owner != "fb" {
			t.Fatalf("%s state=%s owner=%s; want working on fb", id, state, owner)
		}
	}
	if n, _ := client.ZCard(ctx, "friend:fb:living").Result(); n != 3 {
		t.Fatalf("fb living = %d; want 3", n)
	}
	onF, _ := client.ZRange(ctx, "s:"+sprint+":open:fb", 0, -1).Result()
	if len(onF) != 1 || onF[0] != "zread" {
		t.Fatalf("fb queue after rebalance = %v; want only zread", onF)
	}
	r := row(t, tick(t, st, p), "fb")
	if r.Slots != 3 || r.Working != 3 || r.Deficit != 0 || !strings.Contains(r.Idle, "capped=5") {
		t.Fatalf("fb after cap: %s; want 3/3 slots=3 deficit=0 capped=5", r.Line())
	}
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// holdReaders puts every reader at its slots with ACKed read work of its own.
func holdReaders(t *testing.T, st *store.Store, client *redis.Client, readers ...string) {
	t.Helper()
	for _, r := range readers {
		seedFriend(t, client, r, 1)
		push(t, st, r, "hold-"+r, task.KindWork)
		refill(t, st, r, newAck(st))
	}
}

// TestControlC_ReadBoundDealsNoPRProducingCard is #3071 control (c): all
// readers at cap: READ-BOUND is printed and no PR-producing card is dealt.
func TestControlC_ReadBoundDealsNoPRProducingCard(t *testing.T) {
	st, client := controlRedis(t)
	ctx := context.Background()
	holdReaders(t, st, client, "r1", "r2")
	seedFriend(t, client, "rowan", 4)
	pushN(t, st, "rowan", "c", 2)
	push(t, st, "rowan", "cread", task.KindRead)
	p := width.Policy{RebalanceTicks: 2, Readers: []string{"r1", "r2"}, Coordinator: "rowan"}

	res := tick(t, st, p)
	if !res.ReadBound() || !contains(res.Events, "READ-BOUND readers=2") {
		t.Fatalf("events %q; want READ-BOUND readers=2", res.Events)
	}
	if got, _ := client.HGet(ctx, width.FleetKey, "read_bound").Result(); got != "1" {
		t.Fatalf("sprint:width read_bound = %q; want 1", got)
	}
	got, err := width.Fill(ctx, st, "rowan", "rowan", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "cread" {
		t.Fatalf("fill while READ-BOUND = %+v; want only the read", got)
	}
	for _, id := range []string{"c01", "c02"} {
		if state, _ := client.HGet(ctx, "s:"+sprint+":task:"+id, "state").Result(); state != "open" {
			t.Fatalf("PR-producing %s dealt while READ-BOUND: state %s", id, state)
		}
	}
	// A direct reservation of a build refuses READBOUND too.
	gen, _ := client.HGet(ctx, width.Key("rowan"), "gen").Result()
	if gen == "" {
		gen = "0"
	}
	if _, _, err := width.Reserve(ctx, st, width.Ready{Sprint: sprint, ID: "c01"}, "rowan", gen, "rowan", ""); !errors.Is(err, width.ErrReadBound) {
		t.Fatalf("reserve build while READ-BOUND = %v; want READBOUND", err)
	}

	// One reader frees a slot: the bound lifts and builds are dealt again.
	client.ZRemRangeByRank(ctx, "friend:r1:living", 0, -1)
	if res := tick(t, st, p); res.ReadBound() {
		t.Fatalf("still READ-BOUND with a free reader: %q", res.Events)
	}
	if got, err := width.Fill(ctx, st, "rowan", "rowan", ""); err != nil || len(got) != 2 {
		t.Fatalf("fill after the bound lifts = %+v, %v; want the two builds", got, err)
	}
}

// TestControlD_CoordinatorWakeCarriesDeficitFillPrintsThatMany is #3071
// control (d): a coordinator wake carries the deficit and `sprint fill`
// prints exactly that many ready ids. Tasks whose DEPENDS-ON is not merged
// are not ready (#3066) and are neither counted nor printed.
func TestControlD_CoordinatorWakeCarriesDeficitFillPrintsThatMany(t *testing.T) {
	st, client := controlRedis(t)
	ctx := context.Background()
	seedFriend(t, client, "rowan", 5)
	pushN(t, st, "rowan", "d", 3)
	// dep is closed with an open PR; two tasks depend on it.
	push(t, st, "", "dep", task.KindWork)
	client.HSet(ctx, "s:"+sprint+":task:dep", "state", "closed", "repo", "nova-tools", "pr", "7001")
	for _, id := range []string{"x1", "x2"} {
		push(t, st, "rowan", id, task.KindWork)
		client.HSet(ctx, "s:"+sprint+":task:"+id, "depends_on", "dep")
	}
	p := width.Policy{RebalanceTicks: 2, Coordinator: "rowan"}

	res := tick(t, st, p)
	r := row(t, res, "rowan")
	if r.Deficit != 3 || r.Fillable != 3 || r.ReadyOpen != 3 || !strings.Contains(r.Idle, "deps=2") {
		t.Fatalf("rowan row %s; want deficit 3, ready 3, deps=2", r.Line())
	}
	if !contains(res.Events, "UNDERFULL rowan deficit=3 spawn=3") {
		t.Fatalf("events %q; want UNDERFULL rowan deficit=3 spawn=3", res.Events)
	}
	wakes, err := client.XRange(ctx, width.WakeKey("rowan"), "-", "+").Result()
	if err != nil || len(wakes) != 1 || wakes[0].Values["deficit"] != "3" || wakes[0].Values["spawn"] != "3" {
		t.Fatalf("wake stream = %v, %v; want one underfull wake with deficit 3", wakes, err)
	}
	// A repeated tick with the same deficit does not wake again.
	tick(t, st, p)
	if n, _ := client.XLen(ctx, width.WakeKey("rowan")).Result(); n != 1 {
		t.Fatalf("wakes after a repeat tick = %d; want 1", n)
	}

	got, err := width.Fill(ctx, st, "rowan", "rowan", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("fill printed %d ids; want exactly the wake's 3: %+v", len(got), got)
	}
	for i, g := range got {
		if want := fmt.Sprintf("d%02d", i+1); g.ID != want || !strings.HasPrefix(g.Line(), want+" briefs/"+want+".md sprint="+sprint) {
			t.Fatalf("fill line %d = %q; want %s with its brief", i, g.Line(), want)
		}
	}

	// Merge dep's PR: x1 and x2 become ready on the next tick.
	client.HSet(ctx, "s:"+sprint+":pr:nova-tools:7001", "merged", "1")
	if r := row(t, tick(t, st, p), "rowan"); r.ReadyOpen != 2 || r.Deficit != 5 {
		t.Fatalf("after merge: %s; want ready 2 deficit 5 (3 starting + 2)", r.Line())
	}
}

// TestControlE_UtilisationEqualsLeaseSecondsOverDesired is #3071 control
// (e): utilisation in the fold equals the sum of lease seconds over desired
// seconds within 1%.
func TestControlE_UtilisationEqualsLeaseSecondsOverDesired(t *testing.T) {
	leases := []width.Lease{{Start: 0, End: 100_000}, {Start: 0, End: 50_400}, {Start: 20_600, End: 80_250}, {Start: 90_100, End: 99_900}}
	const desired = 4
	var samples []width.Sample
	for at := int64(0); at <= 100_000; at += 1000 {
		w := 0
		for _, l := range leases {
			if l.Start <= at && at < l.End {
				w++
			}
		}
		samples = append(samples, width.Sample{At: at, Working: w, Desired: desired})
	}
	workingMS, desiredMS, ratio := width.Utilisation(samples)
	leaseMS := width.LeaseMS(leases)
	want := float64(leaseMS) / float64(desiredMS)
	if desiredMS != desired*100_000 {
		t.Fatalf("desired ms = %d; want %d", desiredMS, desired*100_000)
	}
	if math.Abs(ratio-want)/want > 0.01 {
		t.Fatalf("utilisation %.4f (working %d ms); lease seconds over desired %.4f (%d ms): off by more than 1%%", ratio, workingMS, want, leaseMS)
	}

	// The tick's own accumulator and the fold over width:log agree.
	st, client := controlRedis(t)
	ctx := context.Background()
	seedFriend(t, client, "fe", 4)
	pushN(t, st, "fe", "e", 2)
	p := width.Policy{RebalanceTicks: 2}
	h := newAck(st)
	for i := 0; i < 4; i++ {
		tick(t, st, p)
		refill(t, st, "fe", h)
	}
	tick(t, st, p)
	samplesRedis, err := width.ReadSamples(ctx, st, "fe")
	if err != nil || len(samplesRedis) != 5 {
		t.Fatalf("samples = %d, %v; want 5", len(samplesRedis), err)
	}
	w, d, _ := width.Utilisation(samplesRedis)
	hw, _ := client.HGet(ctx, width.Key("fe"), "working_ms").Int64()
	hd, _ := client.HGet(ctx, width.Key("fe"), "desired_ms").Int64()
	if w != hw || d != hd {
		t.Fatalf("fold %d/%d ms; tick accumulator %d/%d ms; want equal", w, d, hw, hd)
	}
}

// TestStella1_TwoDispatchersOneFreeSlot: two dispatchers compete for one
// free slot; exactly one reservation is made, and a reservation at a stale
// slot generation refuses STALE.
func TestStella1_TwoDispatchersOneFreeSlot(t *testing.T) {
	st, client := controlRedis(t)
	ctx := context.Background()
	seedFriend(t, client, "f1", 1)
	pushN(t, st, "f1", "s", 3)

	var wg sync.WaitGroup
	got := make(chan int, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := width.Fill(ctx, st, "f1", "dispatcher", "")
			if err != nil {
				t.Error(err)
			}
			got <- len(res)
		}()
	}
	wg.Wait()
	close(got)
	total := 0
	for n := range got {
		total += n
	}
	if total != 1 {
		t.Fatalf("reservations = %d; want exactly 1 for one free slot", total)
	}
	if n, _ := client.ZCard(ctx, "friend:f1:starting").Result(); n != 1 {
		t.Fatalf("starting = %d; want 1", n)
	}

	seedFriend(t, client, "f1", 3)
	gen, _, ids, err := width.ReadyIDs(ctx, st, "f1")
	if err != nil || len(ids) != 2 {
		t.Fatalf("ready = %v, %v; want 2", ids, err)
	}
	if _, _, err := width.Reserve(ctx, st, ids[0], "f1", gen, "d1", ""); err != nil {
		t.Fatalf("first reserve: %v", err)
	}
	if _, _, err := width.Reserve(ctx, st, ids[1], "f1", gen, "d2", ""); !errors.Is(err, width.ErrStale) {
		t.Fatalf("reserve at the old generation = %v; want STALE", err)
	}
}

// TestStella2_TakeWithNoChildACKIsNotWorking: a reservation with no child
// ACK stays starting; working rises only on the first beat.
func TestStella2_TakeWithNoChildACKIsNotWorking(t *testing.T) {
	st, client := controlRedis(t)
	ctx := context.Background()
	seedFriend(t, client, "f2", 4)
	pushN(t, st, "f2", "n", 2)
	res, err := width.Fill(ctx, st, "f2", "harness", "")
	if err != nil || len(res) != 2 {
		t.Fatalf("fill = %+v, %v", res, err)
	}
	p := width.Policy{RebalanceTicks: 2}
	r := row(t, tick(t, st, p), "f2")
	if r.Working != 0 || r.Starting != 2 || r.Deficit != 2 || !strings.Contains(r.Idle, "starting=2") {
		t.Fatalf("no ACK: %s; want working 0 starting 2 deficit 2", r.Line())
	}
	if state, _ := client.HGet(ctx, "s:"+sprint+":task:n01", "state").Result(); state != "claimed" {
		t.Fatalf("n01 state %s; want claimed until the ACK", state)
	}
	if got, err := task.Beat(ctx, st, task.BeatRequest{Sprint: sprint, ID: res[0].ID, Token: res[0].Claim.Token}); err != nil || got != task.BeatWorking {
		t.Fatalf("ack = %s, %v", got, err)
	}
	r = row(t, tick(t, st, p), "f2")
	if r.Working != 1 || r.Starting != 1 || r.Deficit != 1 {
		t.Fatalf("after one ACK: %s; want working 1 starting 1 deficit 1", r.Line())
	}
}

// TestStella3_TransferDuringWorkingNeverMoves: an underfull rebalance moves
// only open tasks; a WORKING task keeps its owner and lease.
func TestStella3_TransferDuringWorkingNeverMoves(t *testing.T) {
	st, client := controlRedis(t)
	ctx := context.Background()
	seedFriend(t, client, "f3", 4)
	seedFriend(t, client, "g3", 4)
	ids := pushN(t, st, "f3", "w", 6)
	h := newAck(st)
	cappedTake(t, st, "f3", 1, h) // the harness starts one and then stalls
	p := width.Policy{RebalanceTicks: 2}
	var moved []string
	for i := 0; i < 2; i++ {
		for _, e := range tick(t, st, p).Events {
			if strings.HasPrefix(e, "MOVE ") {
				moved = append(moved, strings.Fields(e)[1])
			}
		}
	}
	if len(moved) != 2 {
		t.Fatalf("moved %v; want the 2 ready tasks beyond f3's free width", moved)
	}
	if contains(moved, ids[0]) {
		t.Fatalf("the WORKING task %s moved", ids[0])
	}
	state, _ := client.HGet(ctx, "s:"+sprint+":task:"+ids[0], "state").Result()
	owner, _ := client.HGet(ctx, "s:"+sprint+":task:"+ids[0], "owner").Result()
	if state != "working" || owner != "f3" {
		t.Fatalf("%s state=%s owner=%s; want working on f3", ids[0], state, owner)
	}
}

// TestStella4_SixSlotsTenReadyOneForOne: six slots, ten ready; then each
// completion is replaced one for one.
func TestStella4_SixSlotsTenReadyOneForOne(t *testing.T) {
	st, client := controlRedis(t)
	ctx := context.Background()
	seedFriend(t, client, "f4", 6)
	pushN(t, st, "f4", "k", 10)
	h := newAck(st)
	if res := refill(t, st, "f4", h); len(res.Launched) != 6 {
		t.Fatalf("first refill launched %d; want 6", len(res.Launched))
	}
	for _, n := range []int{1, 2, 1} {
		h.finish(t, n)
		if res := refill(t, st, "f4", h); len(res.Launched) != n {
			t.Fatalf("after %d completions refill launched %d; want %d", n, len(res.Launched), n)
		}
		if live, _ := client.ZCard(ctx, "friend:f4:living").Result(); live != 6 {
			t.Fatalf("living = %d; want 6", live)
		}
	}
}

// TestStella5_WrapperExit125MutatesNothing: a pre-mutation wrapper failure
// (nova-secrets exec exit 125) leaves every task OPEN and writes nothing.
func TestStella5_WrapperExit125MutatesNothing(t *testing.T) {
	st, client := controlRedis(t)
	ctx := context.Background()
	seedFriend(t, client, "f5", 4)
	ids := pushN(t, st, "f5", "p", 3)
	before, _ := client.XLen(ctx, "s:"+sprint+":log").Result()
	h := newAck(st)
	h.preflight = &width.ExitError{Code: 125, Err: errors.New("nova-secrets exec: sealed branch unavailable")}
	res, err := width.Refill(ctx, st, "f5", h, "harness", "")
	var exit *width.ExitError
	if !errors.As(err, &exit) || exit.Code != 125 || res.Reason != width.IdleWrapper {
		t.Fatalf("refill = %+v, %v; want exit 125 and reason wrapper", res, err)
	}
	after, _ := client.XLen(ctx, "s:"+sprint+":log").Result()
	if after != before {
		t.Fatalf("receipts %d -> %d; want zero mutation", before, after)
	}
	for _, id := range ids {
		if state, _ := client.HGet(ctx, "s:"+sprint+":task:"+id, "state").Result(); state != "open" {
			t.Fatalf("%s state %s; want open", id, state)
		}
	}
	if n, _ := client.ZCard(ctx, "friend:f5:starting").Result(); n != 0 {
		t.Fatalf("starting = %d; want 0", n)
	}
}

// failLauncher fails every launch after its reservation.
type failLauncher struct{}

func (failLauncher) Preflight(context.Context) error { return nil }
func (failLauncher) Launch(context.Context, width.Reserved) error {
	return errors.New("spawn refused")
}

// TestStella5b_LaunchFailureGivesBackAndReadsBack: a launch that fails after
// its reservation gives the task back and reports the read-back state.
func TestStella5b_LaunchFailureGivesBackAndReadsBack(t *testing.T) {
	st, client := controlRedis(t)
	seedFriend(t, client, "f5b", 2)
	pushN(t, st, "f5b", "q", 1)
	res, err := width.Refill(context.Background(), st, "f5b", failLauncher{}, "harness", "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Returned["q01"] != "open" || len(res.Launched) != 0 {
		t.Fatalf("refill = %+v; want q01 given back and read back open", res)
	}
}

// TestStella6_SomeWorkingDoesNotMaskUnfilledSlots: with two WORKING and four
// ready on six slots, the deficit is 4 (from slots), not 0 (from "any
// WORKING"), and the fleet line prints working/desired.
func TestStella6_SomeWorkingDoesNotMaskUnfilledSlots(t *testing.T) {
	st, client := controlRedis(t)
	seedFriend(t, client, "f6", 6)
	pushN(t, st, "f6", "m", 6)
	h := newAck(st)
	cappedTake(t, st, "f6", 2, h)
	res := tick(t, st, width.Policy{RebalanceTicks: 5})
	r := row(t, res, "f6")
	if r.Working != 2 || r.Desired != 6 || r.Deficit != 4 || r.Fillable != 4 {
		t.Fatalf("row %s; want 2/6 deficit 4 fillable 4", r.Line())
	}
	if !strings.HasPrefix(r.Line(), "WIDTH f6 2/6 slots=6") || !strings.Contains(r.Line(), "idle=fillable=4") {
		t.Fatalf("line %q", r.Line())
	}
	if res.Fleet() != "width 2/6" {
		t.Fatalf("fleet %q; want width 2/6", res.Fleet())
	}
}
