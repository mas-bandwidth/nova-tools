package verbs

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/config"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/sprintfn"
	"github.com/mas-bandwidth/nova-tools/internal/tset"
)

// IT19's tests drive add, release and rank on the composed twin (sprintfn.Twin
// over tset.Mem, with X, the derivation, J, the parts and the queries of the
// stack) through Env, and count round trips with Counting. addWorld is this
// file's own world, beside IT18's, so the two merge without a clash.

const addPrefix = "a:"

var addNames = sprint.Names{Prefix: addPrefix}

var addColumns = map[string][]string{
	sprint.Work:    {"waiting", "ready", "working", "review", "merging", "landed"},
	sprint.Readers: {"asked", "reading", "ok", "broken"},
	sprint.Merge:   {"queued", "merged", "stuck", "returned", "ctl"},
	sprint.Fleet:   {"ready", "working", "withdrawn", "ok", "failed", "ctl"},
}

type addWorld struct {
	t   *testing.T
	log *sprintfn.MemLog
	m   *tset.Mem
	tw  *sprintfn.Twin
	cc  *Counting
	env *Env
	mu  sync.Mutex
	now time.Time
}

func newAddWorld(t *testing.T) *addWorld {
	t.Helper()
	m := tset.NewMem()
	for _, table := range []string{sprint.Work, sprint.Readers, sprint.Merge, sprint.Fleet} {
		if err := m.DefineTable(addPrefix, table, tset.TableDefinition{Columns: addColumns[table],
			MemberPrefix: addNames.TSetMemberPrefix(table), EpochKey: addNames.EpochKey(), EpochField: "n"}); err != nil {
			t.Fatalf("define %s: %v", table, err)
		}
	}
	w := &addWorld{t: t, m: m, log: sprintfn.NewMemLog(), now: time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)}
	w.tw = sprintfn.NewTwin(m, w.log, addNames)
	w.tw.UseQueries()
	w.tw.UseIntents()
	w.tw.SetClock(func() time.Time {
		w.mu.Lock()
		defer w.mu.Unlock()
		return w.now
	})
	w.cc = &Counting{C: w.tw}
	w.env = &Env{C: w.cc, Names: addNames, Actor: "coord", noWait: true}
	cfg := config.NewMem()
	ctx := context.Background()
	if _, err := cfg.Insert(ctx, config.KindFriend, config.Row{Name: "coord", Fields: map[string]string{"slots": "1", "tiers": "pro"}}, "test"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := cfg.Update(ctx, config.KindSprint, config.KindSprint, map[string]string{"coordinator": "coord"}, "test"); err != nil {
		t.Fatal(err)
	}
	if _, err := Init(ctx, w.env, InitReq{Config: cfg}); err != nil {
		t.Fatalf("init: %v", err)
	}
	return w
}

// env is another Env over the same twin: a second verb running at once.
func (w *addWorld) other() *Env { return &Env{C: w.tw, Names: addNames, Actor: "coord", noWait: true} }

// add runs an add and fails the test on an error.
func (w *addWorld) add(r sprint.AddReq) Result {
	w.t.Helper()
	res, err := Add(context.Background(), w.env, r)
	if err != nil {
		w.t.Fatalf("add %+v: %v", r, err)
	}
	return res
}

// work is the work table's records at epoch 0, by id.
func (w *addWorld) work() map[string]tset.MemRecord {
	w.t.Helper()
	snap, err := w.m.Snapshot(addPrefix)
	if err != nil {
		w.t.Fatal(err)
	}
	return snap.Epochs["0"].Tables[sprint.Work].Records
}

// card is one work card's record, failing when it has none.
func (w *addWorld) card(id string) tset.MemRecord {
	w.t.Helper()
	r, ok := w.work()[id]
	if !ok {
		w.t.Fatalf("no card %s", id)
	}
	return r
}

// logged says a line of the log at epoch 0 holds every one of the words.
func (w *addWorld) logged(words ...string) bool {
	for _, l := range w.log.Lines(addPrefix, "0") {
		all := true
		for _, word := range words {
			all = all && strings.Contains(string(l), word)
		}
		if all {
			return true
		}
	}
	return false
}

// counter is {p}next@e at epoch 0.
func (w *addWorld) counter() map[string]string {
	return w.tw.SprintKeys()[addNames.Key("next@0")].Hash
}

// zset is a sprint key's sorted set at epoch 0.
func (w *addWorld) zset(name string) map[string]float64 {
	return w.tw.SprintKeys()[addNames.Key(name+"@0")].ZSet
}

// jopen is the judgments open on a subject.
func (w *addWorld) jopen(subject string) map[string]string {
	return w.tw.SprintKeys()[addNames.Key("jopen:"+subject+"@0")].Hash
}

// refusedAs fails the test unless err is a refusal of the code whose text holds
// each of the words.
func refusedAs(t *testing.T, err error, code string, words ...string) *Refused {
	t.Helper()
	var rf *Refused
	if !errors.As(err, &rf) {
		t.Fatalf("want a %s refusal, got %v", code, err)
	}
	if rf.Code() != code {
		t.Fatalf("want %s, got %s: %v", code, rf.Code(), err)
	}
	for _, w := range words {
		if !strings.Contains(err.Error(), w) {
			t.Fatalf("the refusal %q does not name %q", err.Error(), w)
		}
	}
	return rf
}

func score(t *testing.T, r tset.MemRecord) float64 {
	t.Helper()
	f, err := strconv.ParseFloat(r.Score, 64)
	if err != nil {
		t.Fatalf("score %q: %v", r.Score, err)
	}
	return f
}

func ids(prefix string, from, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("%s%d", prefix, from+i)
	}
	return out
}

// TestAddNamedAnySizeInParts: 4,500 named ids go in three parts (1.5.3: named
// ids of any number; 1.5.4), in the order named, at contiguous integer scores
// from the counter, in ready; n parts cost n + 1 round trips; a resume of the
// finished op writes nothing.
func TestAddNamedAnySizeInParts(t *testing.T) {
	t.Parallel()
	w := newAddWorld(t)
	w.cc.Reset()
	named := ids("c", 1, 4500)
	res := w.add(sprint.AddReq{Stream: "s1", IDs: named})
	if res.Parts != 3 || w.cc.Trips() != 4 {
		t.Fatalf("parts %d, trips %d; want 3 parts in 4 round trips", res.Parts, w.cc.Trips())
	}
	cards := w.work()
	for i, id := range named {
		c, ok := cards[id]
		if !ok || c.Row != "s1" || c.Column != "ready" || score(t, c) != float64(1+i) || c.Fields["kind"] != "primary" {
			t.Fatalf("%s: %+v", id, c)
		}
	}
	if got := w.counter()["score"]; got != "4501" {
		t.Fatalf("the counter is %q, want 4501", got)
	}
	if got := w.counter()["streams"]; got != "1" {
		t.Fatalf("streams is %q, want 1 (one stream opened)", got)
	}
	again, err := Add(context.Background(), w.env, sprint.AddReq{Stream: "s1", IDs: named, Op: res.Op})
	if err != nil || !again.Replay || again.Parts != 0 {
		t.Fatalf("a resume of the finished op: %+v, %v", again, err)
	}
}

// threeStreams runs add --stream s1,s2,s3 --count n --sentinel-every every
// (none after the last) and holds it to the op: 3 x n cards and 3 x (n-1)/every
// gates, at contiguous integer scores over the op from 1, each stream's slice
// in line; a gate after every `every` cards of each stream, in waiting as a
// sentinel, the cards behind a gate in waiting, the first `every` of each
// stream in ready; the counter raised past the op; and the part cut and the
// round trips exact.
func threeStreams(t *testing.T, n, every, wantParts, wantTrips int) {
	w := newAddWorld(t)
	w.cc.Reset()
	res := w.add(sprint.AddReq{Stream: "s1,s2,s3", Count: n, Every: every})
	if res.Parts != wantParts || w.cc.Trips() != wantTrips || w.cc.Steps() != wantParts {
		t.Fatalf("parts %d, trips %d, steps %d; want %d parts, %d round trips, %d steps", res.Parts, w.cc.Trips(), w.cc.Steps(), wantParts, wantTrips, wantParts)
	}
	g := (n - 1) / every
	cards := w.work()
	var scores []float64
	gates, ready := 0, 0
	for id, c := range cards {
		scores = append(scores, score(t, c))
		switch {
		case strings.Contains(id, "-gate-"):
			gates++
			if c.Column != "waiting" || c.Fields["kind"] != "sentinel" {
				t.Fatalf("gate %s: %+v", id, c)
			}
		case c.Column == "ready":
			ready++
		}
	}
	if len(cards) != 3*(n+g) || gates != 3*g || ready != 3*every {
		t.Fatalf("%d cards, %d gates, %d ready; want %d, %d, %d", len(cards), gates, ready, 3*(n+g), 3*g, 3*every)
	}
	sort.Float64s(scores)
	for i, s := range scores {
		if s != float64(1+i) {
			t.Fatalf("score %d is %v: the op's scores are not contiguous from 1", i, s)
		}
	}
	for i, s := range []string{"s1", "s2", "s3"} {
		base := i * (n + g)
		at := func(id string, col string, sc int) {
			if c := w.card(id); (col != "" && c.Column != col) || score(t, c) != float64(base+sc) {
				t.Fatalf("%s: %+v, want %s at %d", id, c, col, base+sc)
			}
		}
		at(fmt.Sprintf("%s-%d", s, every), "ready", every)
		at(s+"-gate-1", "waiting", every+1)
		at(fmt.Sprintf("%s-%d", s, every+1), "waiting", every+2)
		at(fmt.Sprintf("%s-%d", s, n), "waiting", n+g)
		if got := len(w.zset("sent:" + s)); got != g {
			t.Fatalf("sent:%s holds %d sentinels, want %d", s, got, g)
		}
	}
	want := map[string]string{"score": strconv.Itoa(3*(n+g) + 1), "streams": "3", "id:s1": strconv.Itoa(n + 1), "gate:s1": strconv.Itoa(g + 1)}
	for f, v := range want {
		if got := w.counter()[f]; got != v {
			t.Fatalf("the counter's %s is %q, want %q", f, got, v)
		}
	}
}

// TestAddCountThreeStreamsGatesScaled is TestAddCountThreeStreamsGates (the
// slow tier's 3 x 30,000) at a twentieth: 3 x 1,500 with a gate after every
// 50, 4,500 cards and 87 gates with three control cards in 3 parts of 2,000
// changed members, 4 round trips.
func TestAddCountThreeStreamsGatesScaled(t *testing.T) {
	t.Parallel()
	threeStreams(t, 1500, 50, 3, 4)
}

// hooked runs fn once, just before the flush that carries the at-th step (from
// 1): another verb that runs between two parts of an op.
type hooked struct {
	c     sprintfn.Client
	mu    sync.Mutex
	steps int
	at    int
	fn    func()
}

func (h *hooked) Pipeline(ctx context.Context, items []sprintfn.Item) ([]sprintfn.Result, error) {
	h.mu.Lock()
	run := false
	for _, it := range items {
		if it.Step != nil {
			h.steps++
			run = run || h.steps == h.at
		}
	}
	h.mu.Unlock()
	if run {
		h.fn()
	}
	return h.c.Pipeline(ctx, items)
}

// step sends one raw request to the twin (a fixture no verb of this item
// makes), failing the test on a refusal.
func (w *addWorld) step(req *sprintfn.Request) {
	w.t.Helper()
	req.Epoch = "0"
	res, err := sprintfn.Step(context.Background(), w.tw, req)
	if err != nil || res.Refusal != nil || res.Err != nil {
		w.t.Fatalf("step: err %v, refusal %v, result err %v", err, res.Refusal, res.Err)
	}
}

// land moves a card from its cell to landed (a fixture: the merge verb that
// lands cards is IT21's).
func (w *addWorld) land(id string) {
	w.t.Helper()
	c := w.card(id)
	w.step(&sprintfn.Request{Meta: sprintfn.Meta{Verb: "merge", Actor: "coord"}, Body: sprintfn.Body{Entries: []tset.Entry{{Kind: "move",
		Table: sprint.Work, From: c.Row + ":" + c.Column, To: c.Row + ":landed", IDs: []string{id}, About: []string{id}}}}})
}

// TestAddConcurrentAllocatesAbove: an add that runs between the parts of
// another allocates above the first one's reservation (1.5.4), so the first
// op's cards are contiguous and their number is the one asked, and the second
// op's cards lie above them; the counter is raised past both.
func TestAddConcurrentAllocatesAbove(t *testing.T) {
	t.Parallel()
	w := newAddWorld(t)
	w.add(sprint.AddReq{Stream: "s1", IDs: []string{"first"}})
	var other Result
	var otherErr error
	h := &hooked{c: w.tw, at: 2, fn: func() {
		other, otherErr = Add(context.Background(), w.other(), sprint.AddReq{Stream: "s1", IDs: []string{"x1", "x2", "x3"}})
	}}
	env := &Env{C: h, Names: addNames, Actor: "coord", noWait: true}
	res, err := Add(context.Background(), env, sprint.AddReq{Stream: "s1", Count: 4500})
	if err != nil || otherErr != nil || res.Parts != 3 || other.Parts != 1 {
		t.Fatalf("add: %+v, %v; the other: %+v, %v", res, err, other, otherErr)
	}
	cards := w.work()
	for i := 1; i <= 4500; i++ {
		if c := cards[fmt.Sprintf("s1-%d", i)]; score(t, c) != float64(1+i) || c.Column != "ready" {
			t.Fatalf("s1-%d: %+v, want ready at %d", i, c, 1+i)
		}
	}
	for i, id := range []string{"x1", "x2", "x3"} {
		if c := w.card(id); score(t, c) != float64(4502+i) {
			t.Fatalf("%s: %+v, want %d, above the reservation", id, c, 4502+i)
		}
	}
	if got := w.counter()["score"]; got != "4505" {
		t.Fatalf("the counter is %q, want 4505", got)
	}
}

// TestAddMissingNeedAdmitted: a card whose need has no record is admitted to
// waiting with open 1, in wait:n, n in missing, and "blocked on something
// missing" open on it (1.3.3 waitfor); the add that then creates n closes that
// judgment in its own step (errata 3, H3 madeclose), and the waiter stays in
// wait:n, waiting for n to land.
func TestAddMissingNeedAdmitted(t *testing.T) {
	t.Parallel()
	w := newAddWorld(t)
	w.add(sprint.AddReq{Stream: "s1", IDs: []string{"w1", "w2"}, Needs: []string{"n1"}})
	for _, id := range []string{"w1", "w2"} {
		c := w.card(id)
		if c.Column != "waiting" || c.Fields["open"] != "1" || c.Fields["needs"] != "n1" {
			t.Fatalf("%s: %+v, want waiting with open 1", id, c)
		}
		if _, ok := w.zset("wait:n1")[id]; !ok {
			t.Fatalf("%s is not in wait:n1", id)
		}
		if _, ok := w.jopen(id)[sprint.NMissingNeed+"|n1"]; !ok {
			t.Fatalf("no missing judgment on %s: %v", id, w.jopen(id))
		}
	}
	if _, ok := w.zset("missing")["n1"]; !ok {
		t.Fatal("n1 is not in missing")
	}
	w.add(sprint.AddReq{Stream: "s2", IDs: []string{"n1"}})
	for _, id := range []string{"w1", "w2"} {
		if _, ok := w.jopen(id)[sprint.NMissingNeed+"|n1"]; ok {
			t.Fatalf("the missing judgment on %s is still open after n1 was added: %v", id, w.jopen(id))
		}
		if _, ok := w.zset("wait:n1")[id]; !ok || w.card(id).Fields["open"] != "1" {
			t.Fatalf("%s no longer waits for n1 to land", id)
		}
	}
}

// TestAddNeedsAlwaysWaiting: a card that names needs is created in waiting
// whatever the needs' state (1.5.4: NeedsHold), open counting the needs not
// landed: an open need makes it 1, a landed one 0, and the card with open 0
// is free to go (elig), for R3 to release.
func TestAddNeedsAlwaysWaiting(t *testing.T) {
	t.Parallel()
	w := newAddWorld(t)
	w.add(sprint.AddReq{Stream: "s1", IDs: []string{"open1", "done1"}})
	w.land("done1")
	w.add(sprint.AddReq{Stream: "s1", IDs: []string{"a"}, Needs: []string{"open1"}})
	w.add(sprint.AddReq{Stream: "s1", IDs: []string{"b"}, Needs: []string{"done1"}})
	if c := w.card("a"); c.Column != "waiting" || c.Fields["open"] != "1" {
		t.Fatalf("a: %+v, want waiting with open 1", c)
	}
	if c := w.card("b"); c.Column != "waiting" || c.Fields["open"] != "0" {
		t.Fatalf("b: %+v, want waiting with open 0", c)
	}
	if _, ok := w.zset("elig:s1")["b"]; !ok {
		t.Fatalf("b is not free to go: elig:s1 is %v", w.zset("elig:s1"))
	}
	if _, ok := w.zset("elig:s1")["a"]; ok {
		t.Fatal("a is free to go while open1 is open")
	}
}

// TestAddCycleRefusedBeforePart1: an add whose needs reach a card that waits
// for one of its new ids is refused before part 1 (3: the cycle check over the
// named set and every existing card that names a new id), naming the chain;
// nothing is written, in one round trip.
func TestAddCycleRefusedBeforePart1(t *testing.T) {
	t.Parallel()
	w := newAddWorld(t)
	w.add(sprint.AddReq{Stream: "s1", IDs: []string{"c1"}, Needs: []string{"x"}})
	w.add(sprint.AddReq{Stream: "s1", IDs: []string{"c2"}, Needs: []string{"c1"}})
	before := w.counter()["score"]
	w.cc.Reset()
	_, err := Add(context.Background(), w.env, sprint.AddReq{Stream: "s1", IDs: []string{"x"}, Needs: []string{"c2"}})
	rf := refusedAs(t, err, "XGUARD", "cycle", "c1 needs x")
	if !rf.Local || w.cc.Trips() != 1 || w.cc.Steps() != 0 {
		t.Fatalf("refused after %d round trips and %d steps (local %v), want part 1's read alone", w.cc.Trips(), w.cc.Steps(), rf.Local)
	}
	if _, ok := w.work()["x"]; ok || w.counter()["score"] != before {
		t.Fatal("the refused add wrote")
	}
	_, err = Add(context.Background(), w.env, sprint.AddReq{Stream: "s1", IDs: []string{"y"}, Needs: []string{"y"}})
	refusedAs(t, err, sprintfn.CodeRequest, "cycle")
	// a generated id: c3 waits for s9-2, which --count 2 of s9 would create
	w.add(sprint.AddReq{Stream: "s1", IDs: []string{"c3"}, Needs: []string{"s9-2"}})
	_, err = Add(context.Background(), w.env, sprint.AddReq{Stream: "s9", Count: 2, Needs: []string{"c3"}})
	refusedAs(t, err, "XGUARD", "cycle", "c3 needs s9-2")
	w.add(sprint.AddReq{Stream: "s9", Count: 1, Needs: []string{"c3"}}) // s9-1 only: no cycle
}

// TestAddCycleWalkBound: the add's needs walk reads at most 2,000 records and
// is refused past it, naming the bound and the chain's head (3), before part
// 1: two trees of 1,122 records each under the needs named.
func TestAddCycleWalkBound(t *testing.T) {
	t.Parallel()
	w := newAddWorld(t)
	for _, tree := range []string{"a", "b"} {
		leaves := ids(tree+"-leaf-", 1, 1100)
		w.add(sprint.AddReq{Stream: "s1", IDs: leaves})
		var mids []string
		for i := 0; i < 20; i++ {
			mid := fmt.Sprintf("%s-mid-%d", tree, i)
			mids = append(mids, mid)
			w.add(sprint.AddReq{Stream: "s1", IDs: []string{mid}, Needs: leaves[i*55 : (i+1)*55]})
		}
		w.add(sprint.AddReq{Stream: "s1", IDs: []string{tree + "-top"}, Needs: mids})
	}
	w.cc.Reset()
	_, err := Add(context.Background(), w.env, sprint.AddReq{Stream: "s1", IDs: []string{"z"}, Needs: []string{"a-top", "b-top"}})
	refusedAs(t, err, "XGUARD", "2000", "a-top", "bound")
	if w.cc.Steps() != 0 {
		t.Fatalf("%d steps sent, want none", w.cc.Steps())
	}
	if _, ok := w.work()["z"]; ok {
		t.Fatal("z was written")
	}
}

// TestAddNewStreamSizeBound: an add that creates a stream is refused past the
// sprint's size (3: members and streams together at most 250), naming the
// bound; at the bound it is admitted.
func TestAddNewStreamSizeBound(t *testing.T) {
	t.Parallel()
	w := newAddWorld(t)
	members := ids("m", 1, 249)
	for i := 0; i < len(members); i += 100 {
		w.step(&sprintfn.Request{Meta: sprintfn.Meta{Verb: "fleet up", Actor: "coord"},
			Body: sprintfn.Body{Entries: []tset.Entry{{Kind: "rows", Table: sprint.Fleet, Add: members[i:min(i+100, len(members))]}}}})
	}
	w.add(sprint.AddReq{Stream: "s1", IDs: []string{"c1"}})
	_, err := Add(context.Background(), w.env, sprint.AddReq{Stream: "s2", IDs: []string{"c2"}})
	refusedAs(t, err, sprintfn.CodeLimit, "s2", "250")
	w.add(sprint.AddReq{Stream: "s1", IDs: []string{"c3"}})
	if _, ok := w.work()["c2"]; ok {
		t.Fatal("c2 was written")
	}
}

// TestInsertNoScoreRefusedNamesRank: an insertion between two cards with no
// score between them that is not an integer is refused before any write,
// naming rank (1.5.4).
func TestInsertNoScoreRefusedNamesRank(t *testing.T) {
	t.Parallel()
	w := newAddWorld(t)
	w.add(sprint.AddReq{Stream: "s1", IDs: []string{"a", "b", "c"}})
	x := 1.5
	if _, err := Rank(context.Background(), w.env, sprint.RankReq{IDs: []string{"b"}, Score: &x}); err != nil {
		t.Fatal(err)
	}
	y := math.Nextafter(1.5, 2)
	if _, err := Rank(context.Background(), w.env, sprint.RankReq{IDs: []string{"c"}, Score: &y}); err != nil {
		t.Fatal(err)
	}
	w.cc.Reset()
	_, err := Add(context.Background(), w.env, sprint.AddReq{Stream: "s1", IDs: []string{"n"}, After: "b"})
	refusedAs(t, err, sprintfn.CodeRequest, "no score lies between", "rank")
	if _, ok := w.work()["n"]; ok || w.cc.Steps() != 0 {
		t.Fatal("the refused insertion wrote")
	}
	// with room, the insertion goes between, at a score that is no integer
	w.add(sprint.AddReq{Stream: "s1", IDs: []string{"n"}, After: "a"})
	if v := score(t, w.card("n")); !(v > 1 && v < 1.5) || v == math.Trunc(v) {
		t.Fatalf("n is at %v, want between a (1) and b (1.5), not an integer", v)
	}
}

// TestReleaseNeedsReason: release is refused without --reason; with one, the
// sentinel goes waiting -> landed with KNOW "sentinel landed"; a sentinel with
// an open card before it is refused, naming the count.
func TestReleaseNeedsReason(t *testing.T) {
	t.Parallel()
	w := newAddWorld(t)
	w.add(sprint.AddReq{Stream: "s1", IDs: []string{"g"}, Sentinel: true})
	w.add(sprint.AddReq{Stream: "s2", IDs: []string{"c"}})
	w.add(sprint.AddReq{Stream: "s2", IDs: []string{"g2"}, Sentinel: true})
	_, err := Release(context.Background(), w.env, sprint.ReleaseReq{IDs: []string{"g"}})
	refusedAs(t, err, sprintfn.CodeRequest, "--reason")
	if c := w.card("g"); c.Column != "waiting" {
		t.Fatalf("g moved without a reason: %+v", c)
	}
	_, err = Release(context.Background(), w.env, sprint.ReleaseReq{IDs: []string{"g2"}, Reason: "looked"})
	refusedAs(t, err, sprintfn.CodeRequest, "1 open cards", "g2")
	res, err := Release(context.Background(), w.env, sprint.ReleaseReq{IDs: []string{"g"}, Reason: "the build is green"})
	if err != nil {
		t.Fatal(err)
	}
	if c := w.card("g"); c.Column != "landed" {
		t.Fatalf("g: %+v, want landed", c)
	}
	if res.Trips != 3 {
		t.Fatalf("release took %d round trips, want 3", res.Trips)
	}
	if !w.logged(sprint.NSentinelLanded, "the build is green") {
		t.Fatal("no KNOW \"sentinel landed\" with the reason in the log")
	}
	w.add(sprint.AddReq{Stream: "s3", IDs: []string{"g3"}, Sentinel: true})
	stranger := &Env{C: w.tw, Names: addNames, Actor: "stranger", noWait: true}
	_, err = Release(context.Background(), stranger, sprint.ReleaseReq{IDs: []string{"g3"}, Reason: "looked"})
	refusedAs(t, err, sprintfn.CodeNotCoord)
}

// TestAddReadyGuardedBySentinel: a sentinel placed before an add's cards
// between the part's read and its step refuses the step (S.zguard(sent:s,
// rcount, -inf, the highest score admitted to ready, atmost 0), 1.5.4, on
// X's set guard); the part is planned again on a fresh read, and its cards go
// to waiting behind the sentinel.
func TestAddReadyGuardedBySentinel(t *testing.T) {
	t.Parallel()
	w := newAddWorld(t)
	w.add(sprint.AddReq{Stream: "s1", IDs: []string{"a"}})
	h := &hooked{c: w.tw, at: 1, fn: func() {
		w.step(&sprintfn.Request{Meta: sprintfn.Meta{Verb: "add", Actor: "coord"}, Body: sprintfn.Body{Entries: []tset.Entry{{Kind: "create",
			Table: sprint.Work, To: "s1:waiting", IDs: []string{"g"}, About: []string{"g"}, Scores: []string{"1.5"},
			Set: map[string]string{"kind": "sentinel", "stream": "s1", "attempt": "0"}}}}})
	}}
	env := &Env{C: h, Names: addNames, Actor: "coord", noWait: true}
	res, err := Add(context.Background(), env, sprint.AddReq{Stream: "s1", IDs: []string{"b", "c"}})
	if err != nil || res.Retries != 1 {
		t.Fatalf("add: %+v, %v; want one retry after the guard refused", res, err)
	}
	for _, id := range []string{"b", "c"} {
		if c := w.card(id); c.Column != "waiting" {
			t.Fatalf("%s: %+v, want waiting behind g", id, c)
		}
	}
}

// TestRankAboveCounterRaisesIt: rank --score x at or above the counter raises
// the counter past the scores it gives (U2), so an add after it allocates
// above them; several cards take x, x + 1, ... in the order of their scores.
func TestRankAboveCounterRaisesIt(t *testing.T) {
	t.Parallel()
	w := newAddWorld(t)
	w.add(sprint.AddReq{Stream: "s1", IDs: []string{"a", "b"}})
	x := 10.0
	res, err := Rank(context.Background(), w.env, sprint.RankReq{IDs: []string{"a"}, Score: &x})
	if err != nil || res.Trips != 3 {
		t.Fatalf("rank: %+v, %v (want 3 round trips: the cards, then the read and the step)", res, err)
	}
	if score(t, w.card("a")) != 10 || w.counter()["score"] != "11" {
		t.Fatalf("a at %v, counter %q; want 10 and 11", score(t, w.card("a")), w.counter()["score"])
	}
	y := 20.5
	if _, err := Rank(context.Background(), w.env, sprint.RankReq{IDs: []string{"a", "b"}, Score: &y}); err != nil {
		t.Fatal(err)
	}
	if score(t, w.card("b")) != 20.5 || score(t, w.card("a")) != 21.5 || w.counter()["score"] != "22" {
		t.Fatalf("b %v, a %v, counter %q; want 20.5, 21.5 and 22", score(t, w.card("b")), score(t, w.card("a")), w.counter()["score"])
	}
	w.add(sprint.AddReq{Stream: "s1", IDs: []string{"c"}})
	if score(t, w.card("c")) != 22 {
		t.Fatalf("c at %v, want 22, above the ranked cards", score(t, w.card("c")))
	}
}

// TestRankNeverOnReservedScore: during an add in parts, rank --after the last
// card the add has placed chooses a score that is not an integer, so it never
// takes a score the add has reserved and not yet placed (U2); the add's later
// parts then place every reserved score.
func TestRankNeverOnReservedScore(t *testing.T) {
	t.Parallel()
	w := newAddWorld(t)
	w.add(sprint.AddReq{Stream: "s1", IDs: []string{"q"}})
	var rankErr error
	h := &hooked{c: w.tw, at: 2, fn: func() {
		_, rankErr = Rank(context.Background(), w.other(), sprint.RankReq{IDs: []string{"q"}, After: "s1-2000"})
	}}
	env := &Env{C: h, Names: addNames, Actor: "coord", noWait: true}
	if _, err := Add(context.Background(), env, sprint.AddReq{Stream: "s1", Count: 4000}); err != nil || rankErr != nil {
		t.Fatalf("add: %v; rank: %v", err, rankErr)
	}
	v := score(t, w.card("q"))
	if !(v > 2001 && v < 2002) || v == math.Trunc(v) {
		t.Fatalf("q is at %v, want between s1-2000 (2001) and the next reserved score (2002), not an integer", v)
	}
	cards := w.work()
	for i := 1; i <= 4000; i++ {
		if got := score(t, cards[fmt.Sprintf("s1-%d", i)]); got != float64(1+i) {
			t.Fatalf("s1-%d at %v, want %d", i, got, 1+i)
		}
	}
}

// TestRankIntegerBelowCounterRefused: rank --score x with x an integer below
// the counter is refused REQUEST (U2: integers below the counter are the
// adds'), and nothing moves.
func TestRankIntegerBelowCounterRefused(t *testing.T) {
	t.Parallel()
	w := newAddWorld(t)
	w.add(sprint.AddReq{Stream: "s1", IDs: []string{"a", "b", "c"}})
	x := 2.0
	_, err := Rank(context.Background(), w.env, sprint.RankReq{IDs: []string{"a"}, Score: &x})
	refusedAs(t, err, sprintfn.CodeRequest, "integer", "counter")
	if score(t, w.card("a")) != 1 {
		t.Fatal("a moved")
	}
	y := 2.5
	if _, err := Rank(context.Background(), w.env, sprint.RankReq{IDs: []string{"a"}, Score: &y}); err != nil {
		t.Fatalf("a score that is not an integer below the counter: %v", err)
	}
	if score(t, w.card("a")) != 2.5 || w.counter()["score"] != "4" {
		t.Fatalf("a at %v, counter %q; want 2.5 and 4 unchanged", score(t, w.card("a")), w.counter()["score"])
	}
}

// TestRankSeveralKeepOrder: rank d,b --after a places both between a and its
// neighbour c (the ranked cards skipped), at scores that are not integers,
// keeping their order (b before d); c is not moved.
func TestRankSeveralKeepOrder(t *testing.T) {
	t.Parallel()
	w := newAddWorld(t)
	w.add(sprint.AddReq{Stream: "s1", IDs: []string{"a", "b", "c", "d"}})
	res, err := Rank(context.Background(), w.env, sprint.RankReq{IDs: []string{"d", "b"}, After: "a"})
	if err != nil || res.Trips != 3 {
		t.Fatalf("rank: %+v, %v (want 3 round trips)", res, err)
	}
	b, d, c := score(t, w.card("b")), score(t, w.card("d")), score(t, w.card("c"))
	if !(1 < b && b < d && d < 3) || b == math.Trunc(b) || d == math.Trunc(d) || c != 3 {
		t.Fatalf("b %v, d %v, c %v; want 1 < b < d < 3, neither an integer, c at 3", b, d, c)
	}
}

// TestRankAndInsertCloseReached: a card placed before a reached sentinel, by
// rank in line or by an insertion, closes "sentinel reached" in its own step
// (errata 3, H13 rankclose, in the model's form: SprintEvents.tla
// ReachedPassed), and a card placed after it leaves it open.
func TestRankAndInsertCloseReached(t *testing.T) {
	t.Parallel()
	w := newAddWorld(t)
	w.add(sprint.AddReq{Stream: "s1", IDs: []string{"g"}, Sentinel: true})
	w.add(sprint.AddReq{Stream: "s1", IDs: []string{"a", "b"}})
	field := sprint.NSentinelReached + "|" + sprint.ReachedCause
	reach := func(op string) {
		w.step(&sprintfn.Request{Meta: sprintfn.Meta{Verb: "resolve", Actor: "machine"}, Body: sprintfn.Body{
			Op:    &sprintfn.Op{ID: op, Intent: op},
			Notes: []sprintfn.NoteReq{{Op: "open", Type: sprint.NSentinelReached, Cause: sprint.ReachedCause, Subjects: []string{"g"}, Text: "reached"}}}})
		if _, ok := w.jopen("g")[field]; !ok {
			t.Fatalf("the fixture opened no reached judgment on g: %v", w.jopen("g"))
		}
	}
	reach("reach-1")
	if _, err := Rank(context.Background(), w.env, sprint.RankReq{IDs: []string{"b"}, After: "a"}); err != nil {
		t.Fatal(err)
	}
	if _, ok := w.jopen("g")[field]; !ok {
		t.Fatal("a rank after the sentinel closed its reached judgment")
	}
	if _, err := Rank(context.Background(), w.env, sprint.RankReq{IDs: []string{"b"}, Before: "g"}); err != nil {
		t.Fatal(err)
	}
	if _, ok := w.jopen("g")[field]; ok {
		t.Fatal("a card ranked before the sentinel left it reached")
	}
	reach("reach-2")
	w.add(sprint.AddReq{Stream: "s1", IDs: []string{"n"}, Before: "g"})
	if _, ok := w.jopen("g")[field]; ok {
		t.Fatal("a card inserted before the sentinel left it reached")
	}
}
