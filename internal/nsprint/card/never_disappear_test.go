package card_test

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/deal"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/table"
	"github.com/redis/go-redis/v9"
)

// ONE PLACE (nova-tools#3692). Glenn 2026-09-24 11:35 PM ET: "cards are not
// allowed to disappear. They go from ready -> working -> done, and either OK
// or fail. they cannot DISAPPEAR." 2026-09-25 12:28 AM: "a card can only ever
// be in no set, or one of these sets that drive the tables. it can only be one
// place. this is invariant."

const (
	ndSprint = "control-3692"
	ndBench  = "ctl-3692"
	ndStream = "swarm: cards"
	ndFence  = "fence-3692"
)

// oneTable is the Go-side check, independent of the Lua walk: every id in the
// roster is in exactly one place per dimension (bench, stream) or, null, in
// none; the dealer's pool holds exactly the ready cards and its waiting list
// exactly the waiting ones; ok and fail split done exactly; each card's
// record names the place that holds it; and the per-place sums equal the
// roster.
func oneTable(t *testing.T, ctx context.Context, client *redis.Client, step string, wantCards int) map[string]int64 {
	t.Helper()
	ids, err := client.ZRange(ctx, card.RosterKey(ndSprint), 0, -1).Result()
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != wantCards {
		t.Fatalf("%s: roster holds %d cards, want %d: a card disappeared or was never listed", step, len(ids), wantCards)
	}
	count := map[string]int64{}
	for _, id := range ids {
		h, err := client.HGetAll(ctx, id).Result()
		if err != nil || len(h) == 0 {
			t.Fatalf("%s: roster id %s has no record (%v)", step, id, err)
		}
		var benchIn, streamIn []string
		for _, b := range []string{ndBench, "_pool"} {
			for _, w := range append(append([]string{}, card.Places...), "ok", "fail") {
				if zHas(t, ctx, client, card.BenchCardsKey(b, w), id) {
					benchIn = append(benchIn, b+"/"+w)
				}
			}
		}
		for _, w := range card.Places {
			if zHas(t, ctx, client, "ws:"+ndStream+":"+w, id) {
				streamIn = append(streamIn, w)
			}
		}
		where := h["where"]
		count[where]++
		bench := h["bench"]
		if bench == "" {
			bench = "_pool"
		}
		want := []string{bench + "/" + where}
		if where == "done" {
			want = append(want, bench+"/"+h["where_ok"])
			count[h["where_ok"]]++
		}
		if where == "" {
			want = nil
		}
		if strings.Join(benchIn, ",") != strings.Join(want, ",") {
			t.Fatalf("%s: %s (state %s) bench views %v, its record says %v", step, id, h["state"], benchIn, want)
		}
		// Every ZSET is scored by the card's created_at, uniformly (#3692).
		created, _ := strconv.ParseFloat(h["created_at"], 64)
		scored := map[string]string{card.RosterKey(ndSprint): id}
		for _, v := range benchIn {
			b, w, _ := strings.Cut(v, "/")
			scored[card.BenchCardsKey(b, w)] = id
		}
		label := strings.TrimPrefix(id, "s:"+ndSprint+":card:")
		if where == "ready" {
			scored["s:"+ndSprint+":pool"] = label
		}
		for key, m := range scored {
			if sc := client.ZScore(ctx, key, m).Val(); sc != created {
				t.Fatalf("%s: %s scores %s at %v, its created_at is %v", step, key, m, sc, created)
			}
		}
		if inPool, inWaiting := zHas(t, ctx, client, "s:"+ndSprint+":pool", label), setHas(t, ctx, client, "s:"+ndSprint+":waiting", label); inPool != (where == "ready") || inWaiting != (where == "waiting") {
			t.Fatalf("%s: %s where=%s but pool=%v waiting=%v", step, id, where, inPool, inWaiting)
		}
		wantStream := ""
		if h["stream"] != "" && where != "" {
			wantStream = where
		}
		if strings.Join(streamIn, ",") != wantStream {
			t.Fatalf("%s: %s stream views %v, its record says %q", step, id, streamIn, wantStream)
		}
	}
	rep, err := card.Fsck(ctx, client, ndSprint, false)
	if err != nil {
		t.Fatalf("%s: fsck: %v", step, err)
	}
	if rep.Drift != 0 {
		t.Fatalf("%s: %s\n%s", step, rep.Line("FSCK"), strings.Join(rep.Lines, "\n"))
	}
	if sum := rep.Null + rep.Waiting + rep.Ready + rep.Working + rep.Done + rep.Parked; sum != rep.Cards || rep.Cards != int64(wantCards) {
		t.Fatalf("%s: places sum %d, cards %d, want %d", step, sum, rep.Cards, wantCards)
	}
	if n := client.ZCard(ctx, "s:"+ndSprint+":pool").Val(); n != count["ready"] {
		t.Fatalf("%s: pool holds %d, ready cards %d", step, n, count["ready"])
	}
	if n := client.SCard(ctx, "s:"+ndSprint+":waiting").Val(); n != count["waiting"] {
		t.Fatalf("%s: waiting list holds %d, waiting cards %d", step, n, count["waiting"])
	}
	if rep.OK+rep.Fail != rep.Done {
		t.Fatalf("%s: ok %d + fail %d != done %d", step, rep.OK, rep.Fail, rep.Done)
	}
	if rep.Waiting != count["waiting"] || rep.Ready != count["ready"] || rep.Working != count["working"] ||
		rep.Done != count["done"] || rep.OK != count["ok"] || rep.Fail != count["fail"] {
		t.Fatalf("%s: fsck %s, records %v", step, rep.Line("FSCK"), count)
	}
	return count
}

func ndPush(t *testing.T, ctx context.Context, client *redis.Client, repo, label, depends, place string) {
	t.Helper()
	f := validCard(repo)
	f.label = label
	f.depends = depends
	body := string(f.render()) + "STREAM: " + ndStream + "\nORIGIN: mas-bandwidth/nova-tools#3692\n"
	res := card.Push(ctx, client, ndSprint, []byte(body))
	if res.Code != 0 || !strings.Contains(res.Stdout, "place="+place+"\n") {
		t.Fatalf("push %s place=%s: %+v", label, place, res)
	}
}

func ndEnd(t *testing.T, ctx context.Context, client *redis.Client, label, token, outcome, reason string, why ...string) {
	t.Helper()
	id, err := card.ParseIdentity(hashOf(t, ctx, client, ndSprint, label)["identity"])
	if err != nil {
		t.Fatal(err)
	}
	results := canonicalResults(t, id)
	writeRecord(t, results, card.EndRecord{
		Identity: id, Outcome: outcome, Reason: reason, ExitCode: 0,
		TokenSHA: card.TokenSHA(token), PushedSHA: "89abcdef0123456789abcdef0123456789abcdef", At: "1970-01-01T00:00:00Z",
	})
	got, err := card.End(ctx, storeOf(client), card.EndRequest{
		Sprint: ndSprint, Label: label, Token: token, Outcome: outcome, Reason: reason, ResultsDir: results,
		Why: strings.Join(why, " "),
	})
	if err != nil || !got.Resolved || got.Code != 0 {
		t.Fatalf("end %s %s = %+v, %v", label, outcome, got, err)
	}
}

// hostRow renders the live table and returns the bench row's cells.
func hostRow(t *testing.T, ctx context.Context, client *redis.Client) []string {
	t.Helper()
	now := time.Now().UTC()
	// The bash bench-row's own counts are wrong on purpose: the table must
	// print the sets, never these.
	if err := client.HSet(ctx, "bench:"+ndBench, "host", ndBench, "at", now.Format("2006-01-02T15:04:05Z"),
		"queue", "77", "working", "88", "done", "99", "ok", "98", "fail", "97", "load1", "0.10").Err(); err != nil {
		t.Fatal(err)
	}
	snap, err := table.ReadLive(ctx, client, table.LiveConfig{Friends: []string{"rowan"}, Sprint: ndSprint})
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(snap.RenderLive(now), "\n") {
		cells := strings.Split(line, "|")
		if len(cells) > 6 && strings.TrimSpace(cells[0]) == ndBench {
			for i := range cells {
				cells[i] = strings.TrimSpace(cells[i])
			}
			return cells
		}
	}
	t.Fatalf("no %s row in the table", ndBench)
	return nil
}

func assertHostRow(t *testing.T, ctx context.Context, client *redis.Client, step string) {
	t.Helper()
	cells := hostRow(t, ctx, client)
	for i, w := range []string{"ready", "working", "done", "ok", "fail"} {
		n := client.ZCard(ctx, card.BenchCardsKey(ndBench, w)).Val()
		if cells[i+1] != fmt.Sprint(n) {
			t.Fatalf("%s: host row %s = %s, ZCARD %s = %d (row %v)", step, w, cells[i+1], card.BenchCardsKey(ndBench, w), n, cells)
		}
	}
}

// TestCardsNeverDisappear is #3692's DONE-WHEN: push, deal, launch, end DONE,
// end FAILED (a native refusal, #3194), an invalid result, a harvest refusal, an undeal, a land and a
// release, with card fsck and the Go-side check after every transition, and
// the live table's host row read against the bench ZCARDs.
func TestCardsNeverDisappear(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	_, client := newSprint(t)
	srv := repoServer(t)
	repo := srv.URL + "/acme/public.git"
	for _, cmd := range [][]any{
		{"HSET", "s:" + ndSprint, "status", "open"},
		{"HSET", "lease:reconciler", "token", ndFence},
		{"SADD", "benches", ndBench},
		{"HSET", "bench:" + ndBench + ":state", "state", "UP"},
		{"HSET", "bench:" + ndBench + ":desired", "slots", "8", "paused", "0"},
		{"HSET", "lease:harvest:" + ndBench, "instance", "hv", "token", "hv-token"},
	} {
		if err := client.Do(ctx, cmd...).Err(); err != nil {
			t.Fatal(err)
		}
	}

	labels := []string{"nd-a", "nd-b", "nd-c", "nd-d"}
	for i, l := range labels {
		ndPush(t, ctx, client, repo, l, "none", "pool")
		oneTable(t, ctx, client, "push "+l, i+1)
	}
	ndPush(t, ctx, client, repo, "nd-e", "nd-d", "waiting")
	c := oneTable(t, ctx, client, "push nd-e (waiting)", 5)
	if c["ready"] != 4 || c["waiting"] != 1 {
		t.Fatalf("after push: %v", c)
	}
	assertHostRow(t, ctx, client, "push")

	tokens := map[string]string{}
	args := []any{ndBench, ndFence, "test", "3692"}
	for i, l := range labels {
		tokens[l] = attemptToken(1, fmt.Sprintf("%032x", i+1))
		args = append(args, ndSprint, l, "1", tokens[l], card.TokenSHA(tokens[l]))
	}
	reply, err := client.FCall(ctx, "ns_card_deal", nil, args...).StringSlice()
	if err != nil || len(reply) != 1+4*4 {
		t.Fatalf("deal = %v, %v", reply, err)
	}
	c = oneTable(t, ctx, client, "deal", 5)
	if c["working"] != 4 {
		t.Fatalf("after deal: %v", c)
	}
	assertHostRow(t, ctx, client, "deal")

	for _, l := range labels[:3] {
		if got, err := card.Launched(ctx, storeOf(client), card.LaunchRequest{
			Sprint: ndSprint, Label: l, Token: tokens[l], Branch: card.WrapperBranch(ndSprint, l, 1), JobDir: "/jobs/" + l,
		}); err != nil || !got.Resolved {
			t.Fatalf("launched %s = %+v, %v", l, got, err)
		}
		oneTable(t, ctx, client, "launch "+l, 5)
		if got, err := card.Beat(ctx, storeOf(client), card.BeatRequest{Sprint: ndSprint, Label: l, Token: tokens[l]}); err != nil || !got.Resolved {
			t.Fatalf("beat %s = %+v, %v", l, got, err)
		}
		oneTable(t, ctx, client, "beat "+l, 5)
	}

	ndEnd(t, ctx, client, "nd-a", tokens["nd-a"], "DONE", "done")
	c = oneTable(t, ctx, client, "end DONE", 5)
	if c["ok"] != 1 || c["fail"] != 0 {
		t.Fatalf("after end DONE: %v", c)
	}
	// A native refusal (#3194) is a FAILED end like any other: the one move
	// to done/fail, its refusal line the card's why, fsck clean after it.
	refusal := "NATIVE REFUSED: pool /p has no identity row in identity.tsv; remedy: make -C fleet converge"
	ndEnd(t, ctx, client, "nd-b", tokens["nd-b"], "FAILED", "refused", refusal)
	c = oneTable(t, ctx, client, "end FAILED refused", 5)
	if c["ok"] != 1 || c["fail"] != 1 {
		t.Fatalf("after end FAILED refused: %v", c)
	}
	if h := hashOf(t, ctx, client, ndSprint, "nd-b"); h["where"] != "done" || h["where_ok"] != "fail" || h["reason"] != "refused" || h["why"] != refusal {
		t.Fatalf("refused card %v, want done/fail reason refused why the refusal line", h)
	}
	ndEnd(t, ctx, client, "nd-c", tokens["nd-c"], "DONE", "done")
	oneTable(t, ctx, client, "end DONE nd-c", 5)
	assertHostRow(t, ctx, client, "end")

	// An invalid typed result after a DONE end: done/ok -> done/fail.
	res, err := client.FCall(ctx, "ns_card_result", nil, ndSprint, "nd-c", "1", tokens["nd-c"],
		"typed-record/v1", "fix", "0", "KIND", "malformed", "2", strings.Repeat("a", 64), "10", "/r").Text()
	if err != nil || !strings.HasPrefix(res, "0|OK") {
		t.Fatalf("result = %q, %v", res, err)
	}
	c = oneTable(t, ctx, client, "invalid result", 5)
	if c["ok"] != 1 || c["fail"] != 2 {
		t.Fatalf("after an invalid result: %v", c)
	}

	// nd-d never launched: its reservation goes back (working -> ready).
	if got, err := client.FCall(ctx, "ns_card_undeal", nil, ndBench, ndFence, "spawn-timeout", "test", "3692u",
		ndSprint, "nd-d", "1").StringSlice(); err != nil || strings.Join(got, " ") != "UNDEALT 1" {
		t.Fatalf("undeal = %v, %v", got, err)
	}
	c = oneTable(t, ctx, client, "undeal", 5)
	if c["ready"] != 1 || c["working"] != 0 {
		t.Fatalf("after undeal: %v", c)
	}

	// nd-d lands by merge (ready -> done/ok) and nd-e, waiting on it, is released.
	mustLandIn(t, ctx, client, "nd-d")
	oneTable(t, ctx, client, "land", 5)
	res2 := card.Release(ctx, client, ndSprint)
	if res2.Code != 0 {
		t.Fatalf("release: %+v", res2)
	}
	c = oneTable(t, ctx, client, "release", 5)
	if c["ready"] != 1 || c["waiting"] != 0 || c["done"] != 4 || c["ok"] != 2 || c["fail"] != 2 {
		t.Fatalf("final: %v", c)
	}
	assertHostRow(t, ctx, client, "final")
}

// TestCardMoveRefusesOffGraphAndDrift: a done/ok card never returns to the
// pool; a card found in two places refuses to move until fsck --repair; and
// repair restores the one place.
func TestCardMoveRefusesOffGraphAndDrift(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	_, client := newSprint(t)
	srv := repoServer(t)
	ndPush(t, ctx, client, srv.URL+"/acme/public.git", "og-a", "none", "pool")
	id := card.CardKey(ndSprint, "og-a")
	if got := client.FCall(ctx, "ns_card_move", nil, id, "working").Val(); !strings.HasPrefix(fmt.Sprint(got), "REFUSED OFFGRAPH") {
		t.Fatalf("queued -> working without a deal = %v", got)
	}
	if got := client.FCall(ctx, "ns_card_move", nil, id, "done", "ok").Val(); !strings.HasPrefix(fmt.Sprint(got), "REFUSED OFFGRAPH") {
		t.Fatalf("ready -> done/ok without a run = %v", got)
	}
	if got := client.FCall(ctx, "ns_card_move", nil, id, "waiting").Val(); got != "OK" {
		t.Fatalf("ready -> waiting = %v", got)
	}
	oneTable(t, ctx, client, "move to waiting", 1)

	// A second place: the move refuses and writes nothing.
	client.ZAdd(ctx, card.BenchCardsKey("_pool", "working"), redis.Z{Score: 1, Member: id})
	if got := client.FCall(ctx, "ns_card_move", nil, id, "ready").Val(); !strings.HasPrefix(fmt.Sprint(got), "REFUSED DRIFT twice") {
		t.Fatalf("move with the id in two places = %v", got)
	}
	if w := client.HGet(ctx, id, "where").Val(); w != "waiting" {
		t.Fatalf("a refused move wrote where=%s", w)
	}
	rep, err := card.Fsck(ctx, client, ndSprint, false)
	if err != nil || rep.Drift != 1 || !strings.HasPrefix(rep.Lines[0], "stray bench:_pool:cards:working") {
		t.Fatalf("fsck = %+v, %v", rep, err)
	}
	// A lost link (the set lacks the id its record names).
	client.ZRem(ctx, "ws:"+ndStream+":waiting", id)
	rep, err = card.Fsck(ctx, client, ndSprint, true)
	if err != nil || rep.Drift != 2 || rep.Fixed != 2 {
		t.Fatalf("repair = %+v, %v", rep, err)
	}
	oneTable(t, ctx, client, "after repair", 1)
}

// TestReindexAdoptsCardsThatPredateTheModel: records written before #3692
// (no roster, no where) are adopted by the one-time reindex, and a move on
// one adopts it first.
func TestReindexAdoptsCardsThatPredateTheModel(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	_, client := newSprint(t)
	for l, st := range map[string]string{"old-q": "queued", "old-r": "running", "old-e": "ended", "old-w": "queued"} {
		key := card.CardKey(ndSprint, l)
		client.HSet(ctx, key, "state", st, "bench", ndBench, "outcome", "DONE", "cut_at", "1700000000", "stream", ndStream)
		client.SAdd(ctx, "s:"+ndSprint+":idx:card:"+st, l)
	}
	client.SAdd(ctx, "s:"+ndSprint+":waiting", "old-w")
	rep, err := card.Fsck(ctx, client, ndSprint, false)
	// Four records outside the roster, and old-w in the waiting list with no
	// card there to point back to it.
	if err != nil || rep.Drift != 5 || rep.Cards != 0 {
		t.Fatalf("fsck before reindex = %+v, %v", rep, err)
	}
	rep, err = card.Fsck(ctx, client, ndSprint, true)
	if err != nil || rep.Cards != 4 || rep.Fixed != 4 {
		t.Fatalf("reindex = %+v, %v", rep, err)
	}
	c := oneTable(t, ctx, client, "reindexed", 4)
	if !zHas(t, ctx, client, "s:"+ndSprint+":pool", "old-q") || !setHas(t, ctx, client, "s:"+ndSprint+":waiting", "old-w") {
		t.Fatal("reindex did not put the ready card in the pool and the waiting card in the waiting list")
	}
	if c["waiting"] != 1 || c["ready"] != 1 || c["working"] != 1 || c["done"] != 1 || c["ok"] != 1 {
		t.Fatalf("reindexed places %v", c)
	}
	if s := client.ZScore(ctx, card.RosterKey(ndSprint), card.CardKey(ndSprint, "old-q")).Val(); s != 1700000000000 {
		t.Fatalf("created_at score %v, want the cut_at in ms", s)
	}
}

func storeOf(client *redis.Client) *store.Store { return store.New(client) }

func mustLandIn(t *testing.T, ctx context.Context, client *redis.Client, label string) {
	t.Helper()
	res := card.Land(ctx, client, ndSprint, label, strings.Repeat("d", 40))
	if res.Code != 0 {
		t.Fatalf("land %s: %+v", label, res)
	}
}

// ndSetup is TestCardsNeverDisappear's open sprint, lease and one UP bench.
func ndSetup(t *testing.T, ctx context.Context, client *redis.Client) {
	t.Helper()
	for _, cmd := range [][]any{
		{"HSET", "s:" + ndSprint, "status", "open"},
		{"HSET", "lease:reconciler", "token", ndFence},
		{"SADD", "benches", ndBench},
		{"HSET", "bench:" + ndBench + ":state", "state", "UP"},
		{"HSET", "bench:" + ndBench + ":desired", "slots", "8", "paused", "0"},
	} {
		if err := client.Do(ctx, cmd...).Err(); err != nil {
			t.Fatal(err)
		}
	}
}

// TestMoveRefusesDriftOnTheNewBenchBeforeAnyWrite (#3695 hold item 1): a
// stray link on the bench a deal moves the card to is found BEFORE any
// write; the deal deals nothing and the record, the pool, the starting set
// and the log are exactly as they were.
func TestMoveRefusesDriftOnTheNewBenchBeforeAnyWrite(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	_, client := newSprint(t)
	ndSetup(t, ctx, client)
	srv := repoServer(t)
	ndPush(t, ctx, client, srv.URL+"/acme/public.git", "pr-a", "none", "pool")
	id := card.CardKey(ndSprint, "pr-a")
	client.ZAdd(ctx, card.BenchCardsKey(ndBench, "done"), redis.Z{Score: 1, Member: id})
	before := hashOf(t, ctx, client, ndSprint, "pr-a")
	logLen := client.XLen(ctx, card.LogKey(ndSprint)).Val()
	token := attemptToken(1, strings.Repeat("a", 32))
	reply, err := client.FCall(ctx, "ns_card_deal", nil, ndBench, ndFence, "test", "hold1",
		ndSprint, "pr-a", "1", token, card.TokenSHA(token)).StringSlice()
	if err != nil || strings.Join(reply, " ") != "DEALT" {
		t.Fatalf("deal = %v, %v; want DEALT with no card", reply, err)
	}
	after := hashOf(t, ctx, client, ndSprint, "pr-a")
	if fmt.Sprint(after) != fmt.Sprint(before) {
		t.Fatalf("a refused deal wrote the record:\nbefore %v\nafter  %v", before, after)
	}
	if !zHas(t, ctx, client, "s:"+ndSprint+":pool", "pr-a") || client.ZCard(ctx, card.BenchWorkingKey(ndBench)).Val() != 0 ||
		client.XLen(ctx, card.LogKey(ndSprint)).Val() != logLen || client.ZCard(ctx, card.BenchCardsKey(ndBench, "working")).Val() != 0 {
		t.Fatal("a refused deal wrote the pool, the starting set, the log or the bench view")
	}
	rep, err := card.Fsck(ctx, client, ndSprint, true)
	if err != nil || rep.Drift != 1 || rep.Fixed != 1 {
		t.Fatalf("repair = %+v, %v", rep, err)
	}
	reply, err = client.FCall(ctx, "ns_card_deal", nil, ndBench, ndFence, "test", "hold1b",
		ndSprint, "pr-a", "1", token, card.TokenSHA(token)).StringSlice()
	if err != nil || len(reply) != 5 {
		t.Fatalf("deal after repair = %v, %v", reply, err)
	}
	oneTable(t, ctx, client, "deal after repair", 1)
}

// TestFsckProvesTheDealerLists (#3695 hold item 2): the pool holds exactly
// the ready cards and the waiting list exactly the waiting ones; a ready card
// missing from the pool, or a label left in the waiting list, is drift that
// --repair fixes.
func TestFsckProvesTheDealerLists(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	_, client := newSprint(t)
	srv := repoServer(t)
	repo := srv.URL + "/acme/public.git"
	ndPush(t, ctx, client, repo, "dl-a", "none", "pool")
	ndPush(t, ctx, client, repo, "dl-b", "dl-a", "waiting")
	oneTable(t, ctx, client, "pushed", 2)
	client.ZRem(ctx, "s:"+ndSprint+":pool", "dl-a")
	client.SAdd(ctx, "s:"+ndSprint+":waiting", "dl-a")
	rep, err := card.Fsck(ctx, client, ndSprint, false)
	if err != nil || rep.Drift != 2 || !strings.Contains(strings.Join(rep.Lines, "\n"), "unlinked s:"+ndSprint+":pool dl-a") ||
		!strings.Contains(strings.Join(rep.Lines, "\n"), "stray s:"+ndSprint+":waiting dl-a") {
		t.Fatalf("fsck = %+v, %v", rep, err)
	}
	if rep, err = card.Fsck(ctx, client, ndSprint, true); err != nil || rep.Fixed != 2 {
		t.Fatalf("repair = %+v, %v", rep, err)
	}
	oneTable(t, ctx, client, "repaired", 2)
	if !zHas(t, ctx, client, "s:"+ndSprint+":pool", "dl-a") || setHas(t, ctx, client, "s:"+ndSprint+":waiting", "dl-a") {
		t.Fatal("repair did not restore the dealer lists")
	}
}

// TestInvalidResultRefusedMoveSurfaces (#3695 hold item 3): when the done/ok
// -> done/fail move of an invalid result is refused, ns_card_result stores
// nothing, logs the refusal, returns 2|MOVE, and the card stays counted
// where its sets say.
func TestInvalidResultRefusedMoveSurfaces(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	_, client := newSprint(t)
	ndSetup(t, ctx, client)
	srv := repoServer(t)
	ndPush(t, ctx, client, srv.URL+"/acme/public.git", "ir-a", "none", "pool")
	token := attemptToken(1, strings.Repeat("b", 32))
	if reply, err := client.FCall(ctx, "ns_card_deal", nil, ndBench, ndFence, "test", "hold3",
		ndSprint, "ir-a", "1", token, card.TokenSHA(token)).StringSlice(); err != nil || len(reply) != 5 {
		t.Fatalf("deal = %v, %v", reply, err)
	}
	if got, err := card.Launched(ctx, storeOf(client), card.LaunchRequest{Sprint: ndSprint, Label: "ir-a", Token: token,
		Branch: card.WrapperBranch(ndSprint, "ir-a", 1), JobDir: "/jobs/ir-a"}); err != nil || !got.Resolved {
		t.Fatalf("launched = %+v, %v", got, err)
	}
	if got, err := card.Beat(ctx, storeOf(client), card.BeatRequest{Sprint: ndSprint, Label: "ir-a", Token: token}); err != nil || !got.Resolved {
		t.Fatalf("beat = %+v, %v", got, err)
	}
	ndEnd(t, ctx, client, "ir-a", token, "DONE", "done")
	oneTable(t, ctx, client, "ended ok", 1)
	id := card.CardKey(ndSprint, "ir-a")
	client.ZAdd(ctx, card.BenchCardsKey("_pool", "fail"), redis.Z{Score: 1, Member: id}) // drift
	res, err := client.FCall(ctx, "ns_card_result", nil, ndSprint, "ir-a", "1", token,
		"typed-record/v1", "fix", "0", "KIND", "malformed", "2", strings.Repeat("a", 64), "10", "/r").Text()
	if err != nil || !strings.HasPrefix(res, "2|MOVE|1|") {
		t.Fatalf("result with a refused move = %q, %v; want 2|MOVE", res, err)
	}
	if client.Exists(ctx, id+":result:a1").Val() != 0 {
		t.Fatal("a refused move stored the result")
	}
	if w := client.HGet(ctx, id, "where_ok").Val(); w != "ok" || !zHas(t, ctx, client, card.BenchCardsKey(ndBench, "ok"), id) {
		t.Fatalf("where_ok %q after a refused move; the card must stay where its sets say", w)
	}
	msgs, _ := client.XRange(ctx, "sprint:"+ndSprint+":moves", "-", "+").Result()
	if len(msgs) == 0 || fmt.Sprint(msgs[len(msgs)-1].Values["to"]) != "REFUSED" {
		t.Fatalf("the refusal is not logged: %v", msgs)
	}
	client.ZRem(ctx, card.BenchCardsKey("_pool", "fail"), id)
	res, err = client.FCall(ctx, "ns_card_result", nil, ndSprint, "ir-a", "1", token,
		"typed-record/v1", "fix", "0", "KIND", "malformed", "2", strings.Repeat("a", 64), "10", "/r").Text()
	if err != nil || !strings.HasPrefix(res, "0|OK") {
		t.Fatalf("result after the drift is gone = %q, %v", res, err)
	}
	if c := oneTable(t, ctx, client, "invalid result", 1); c["fail"] != 1 {
		t.Fatalf("after an invalid result %v", c)
	}
}

// TestFsckChecksEveryScore (#3695 hold 7 item 1): every ZSET is scored by the
// card's created_at; a view or the roster re-scored by hand is drift that
// --repair re-scores.
func TestFsckChecksEveryScore(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	_, client := newSprint(t)
	srv := repoServer(t)
	ndPush(t, ctx, client, srv.URL+"/acme/public.git", "sc-a", "none", "pool")
	id := card.CardKey(ndSprint, "sc-a")
	for _, z := range [][2]string{{card.BenchCardsKey("_pool", "ready"), id}, {"ws:" + ndStream + ":ready", id},
		{"s:" + ndSprint + ":pool", "sc-a"}, {card.RosterKey(ndSprint), id}} {
		client.ZAdd(ctx, z[0], redis.Z{Score: 1, Member: z[1]})
	}
	rep, err := card.Fsck(ctx, client, ndSprint, false)
	if err != nil || rep.Drift != 4 || !strings.HasPrefix(rep.Lines[0], "score ") {
		t.Fatalf("fsck over four re-scored sets = %+v, %v", rep, err)
	}
	if rep, err = card.Fsck(ctx, client, ndSprint, true); err != nil || rep.Fixed != 4 {
		t.Fatalf("repair = %+v, %v", rep, err)
	}
	oneTable(t, ctx, client, "re-scored", 1)
}

// TestPoolIsAgeOrderedAndTheDealerDealsByPriority (#3695 hold 7 item 2): the
// pool is scored by created_at like every view; the deal priority is the
// record's priority field, and the dealer deals the lowest priority value
// first, the oldest first among equals.
func TestPoolIsAgeOrderedAndTheDealerDealsByPriority(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	_, client := newSprint(t)
	ndSetup(t, ctx, client)
	client.SAdd(ctx, "sprints", ndSprint)
	client.ZAdd(ctx, "sprint:order", redis.Z{Score: 1, Member: ndSprint})
	srv := repoServer(t)
	repo := srv.URL + "/acme/public.git"
	for _, c := range []struct{ label, prio string }{{"pa-old5", "5"}, {"pa-mid1", "1"}, {"pa-new1", "1"}} {
		f := validCard(repo)
		f.label = c.label
		body := string(f.render()) + "PRIORITY: " + c.prio + "\n"
		if res := card.Push(ctx, client, ndSprint, []byte(body)); res.Code != 0 {
			t.Fatalf("push %s: %+v", c.label, res)
		}
	}
	oneTable(t, ctx, client, "pushed", 3)
	// oneTable proved every pool score is the card's created_at; push order
	// is age order (two pushes in one millisecond tie, and ZRANGE breaks the
	// tie by label).
	var last float64
	for _, l := range []string{"pa-old5", "pa-mid1", "pa-new1"} {
		sc := client.ZScore(ctx, "s:"+ndSprint+":pool", l).Val()
		if sc < last {
			t.Fatalf("pool score of %s is %v, older than the card pushed before it (%v)", l, sc, last)
		}
		last = sc
	}
	in, err := deal.RedisSource{Client: client}.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	batches := deal.Plan(in, time.Minute)
	var got []string
	for _, b := range batches {
		for _, c := range b.Cards {
			got = append(got, c.Label)
		}
	}
	if strings.Join(got, ",") != "pa-mid1,pa-new1,pa-old5" {
		t.Fatalf("deal order %v, want priority 1 oldest first, then priority 5", got)
	}
}
