package consume

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/task"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/redis/go-redis/v9"
)

// startRedis starts a throwaway redis-server for a control sprint (#2756
// section 8), the same shape as the task and store controls.
func startRedis(t *testing.T) string {
	t.Helper()
	return testutil.Start(t)
}

func controlRedis(t *testing.T) (*store.Store, *redis.Client) {
	addr := startRedis(t)
	client := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = client.Close() })
	if err := fn.Load(context.Background(), client); err != nil {
		t.Fatalf("load nova_sprint library: %v", err)
	}
	return store.New(client), client
}

const (
	ctlRepo  = "nova-tools"
	ctlBench = "ctl-b1"
)

// seedSprint opens a control sprint with the declared reader policy, one
// fixture bench and friends ctl-a, ctl-b, ctl-c UP and ctl-down registered
// but absent.
func seedSprint(t *testing.T, client *redis.Client, sprint string) {
	t.Helper()
	ctx := context.Background()
	must(t, client.HSet(ctx, "s:"+sprint, "status", "open").Err())
	must(t, client.HSet(ctx, "s:"+sprint+":policy", "readers", "1", "readers_security", "2",
		"security_paths", "internal/secrets/ .github/workflows/").Err())
	must(t, client.SAdd(ctx, "benches", ctlBench).Err())
	for _, f := range []string{"ctl-a", "ctl-b", "ctl-c", "ctl-down"} {
		must(t, client.SAdd(ctx, "friends", f).Err())
		must(t, client.HSet(ctx, "friend:"+f+":desired", "slots", "4", "paused", "0").Err())
		if f != "ctl-down" {
			must(t, client.HSet(ctx, "friend:"+f+":beat", "harness", "ctl", "at", "1").Err())
		}
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func redisMillis(t *testing.T, client *redis.Client) int64 {
	t.Helper()
	now, err := client.Time(context.Background()).Result()
	must(t, err)
	return now.UnixMilli()
}

// endCard stands in for ns_card_end (#3011): the card hash, the index move
// and the one `ended` receipt on the sprint log, written in one MULTI.
func endCard(t *testing.T, client *redis.Client, sprint, label, outcome, reason, paths, author string) {
	t.Helper()
	ctx := context.Background()
	identity := fmt.Sprintf("%s/%s/09fbedc9/%s/1", sprint, label, ctlBench)
	at := strconv.FormatInt(redisMillis(t, client), 10)
	pushed := ""
	if outcome == "DONE" {
		pushed = strings.Repeat(label[len(label)-1:], 40)
	}
	_, err := client.TxPipelined(ctx, func(p redis.Pipeliner) error {
		p.HSet(ctx, "s:"+sprint+":card:"+label,
			"kind", "model", "repo", ctlRepo, "base", "dev", "base_sha", "09fbedc9",
			"paths", paths, "author", author, "priority", "5", "state", "ended",
			"attempt", "1", "identity", identity, "bench", ctlBench, "token_sha", "abcdefabcdef",
			"outcome", outcome, "reason", reason, "pushed_sha", pushed, "ended_at", at)
		p.SAdd(ctx, "s:"+sprint+":idx:card:ended", label)
		p.XAdd(ctx, &redis.XAddArgs{Stream: "s:" + sprint + ":log", Values: []any{
			"kind", "card", "id", label, "from", "running", "to", "ended", "attempt", "1",
			"token_sha", "abcdefabcdef", "actor", "card-end", "reason", reason,
			"evidence", identity, "idem", "end:" + identity, "at", at}})
		return nil
	})
	must(t, err)
}

// harvestCard stands in for the harvest worker (#2932): the branch was
// pushed, the PR found or opened, and its head read back by REST equals
// pushed_sha; then the card is harvested with one receipt.
func harvestCard(t *testing.T, client *redis.Client, sprint, label string, pr int, head string) {
	t.Helper()
	ctx := context.Background()
	at := strconv.FormatInt(redisMillis(t, client), 10)
	_, err := client.TxPipelined(ctx, func(p redis.Pipeliner) error {
		p.HSet(ctx, "s:"+sprint+":card:"+label, "state", "harvested", "pr", strconv.Itoa(pr),
			"head", head, "branch", "nova/"+sprint+"/"+label+"-a1", "harvested_at", at)
		p.SMove(ctx, "s:"+sprint+":idx:card:ended", "s:"+sprint+":idx:card:harvested", label)
		p.XAdd(ctx, &redis.XAddArgs{Stream: "s:" + sprint + ":log", Values: []any{
			"kind", "card", "id", label, "from", "ended", "to", "harvested", "attempt", "1",
			"token_sha", "abcdefabcdef", "actor", "card-harvest", "reason", "harvested",
			"evidence", fmt.Sprintf("https://github.com/mas-bandwidth/%s/pull/%d", ctlRepo, pr),
			"idem", "harvest:" + label, "at", at}})
		return nil
	})
	must(t, err)
}

// taskReceipts returns the log receipts of `task push` per task id and the
// card transitions per label.
func logCounts(t *testing.T, client *redis.Client, sprint string) (pushes map[string]int, cards map[string]int) {
	t.Helper()
	entries, err := client.XRange(context.Background(), "s:"+sprint+":log", "-", "+").Result()
	must(t, err)
	pushes, cards = map[string]int{}, map[string]int{}
	for _, e := range entries {
		kind, _ := e.Values["kind"].(string)
		id, _ := e.Values["id"].(string)
		to, _ := e.Values["to"].(string)
		switch kind {
		case "task push":
			pushes[id]++
		case "card":
			cards[id+" "+to]++
		}
	}
	return pushes, cards
}

func reviewTasks(t *testing.T, client *redis.Client, sprint, label string) map[string]map[string]string {
	t.Helper()
	ctx := context.Background()
	ids, err := client.SMembers(ctx, "s:"+sprint+":idx:task:open").Result()
	must(t, err)
	out := map[string]map[string]string{}
	for _, id := range ids {
		h, err := client.HGetAll(ctx, "s:"+sprint+":task:"+id).Result()
		must(t, err)
		if (h["kind"] == "review" || h["kind"] == "read") && strings.Contains(h["ref"], "/"+label+"/") {
			out[id] = h
		}
	}
	return out
}

func newConsumer(st *store.Store, sprint, name string) *OkFriend {
	return &OkFriend{Store: st, Sprint: sprint, Consumer: name, Actor: "ok-to-friend", Block: 50 * time.Millisecond}
}

// TestControl07NoReviewBeforePR is #2756 control 7: a card that ends DONE
// with no PR has no review task; its harvest task exists within 60 s; after
// harvest each required reader has exactly one head-specific read within
// 60 s. The antecedent is the DONE-to-PR gap (370 done, 38 PRs).
func TestControl07NoReviewBeforePR(t *testing.T) {
	st, client := controlRedis(t)
	ctx := context.Background()
	sprint := "control-0707a0b1"
	seedSprint(t, client, sprint)
	ok := newConsumer(st, sprint, "okf-1")
	must(t, ok.Start(ctx))

	// card-1 an ordinary card; card-2 touches a security path (two readers);
	// card-3 ends FAILED (no harvest, no read); card-4 has no verified head.
	endCard(t, client, sprint, "card-1", "DONE", "done", "internal/nsprint/consume/okfriend.go", "ctl-a")
	endCard(t, client, sprint, "card-2", "DONE", "done", "internal/secrets/vault.go", "")
	endCard(t, client, sprint, "card-3", "FAILED", "crash", "internal/x/y.go", "")
	endCard(t, client, sprint, "card-4", "DONE", "done", "internal/x/z.go", "")
	if _, err := ok.Pass(ctx); err != nil {
		t.Fatalf("pass after ended: %v", err)
	}

	for _, label := range []string{"card-1", "card-2", "card-3", "card-4"} {
		if got := reviewTasks(t, client, sprint, label); len(got) != 0 {
			t.Fatalf("%s ended with no PR has review tasks %v; want none before a PR", label, got)
		}
	}
	for _, label := range []string{"card-1", "card-2", "card-4"} {
		h, err := client.HGetAll(ctx, "s:"+sprint+":task:harvest-"+label).Result()
		must(t, err)
		if h["state"] != "open" || h["kind"] != "harvest" {
			t.Fatalf("harvest-%s = %v; want an open harvest task", label, h)
		}
		if _, err := client.ZScore(ctx, "s:"+sprint+":open:harvest:"+ctlBench, "harvest-"+label).Result(); err != nil {
			t.Fatalf("harvest-%s is not in open:harvest:%s: %v", label, ctlBench, err)
		}
		ended, _ := client.HGet(ctx, "s:"+sprint+":card:"+label, "ended_at").Int64()
		created := pushReceiptAt(t, client, sprint, "harvest-"+label)
		if created-ended > 60_000 || created < ended {
			t.Fatalf("harvest-%s created %d ms after the end; want within 60 s", label, created-ended)
		}
	}
	if n, _ := client.Exists(ctx, "s:"+sprint+":task:harvest-card-3").Result(); n != 0 {
		t.Fatal("a FAILED card got a harvest task; only DONE is harvested")
	}

	// Harvest: card-1 and card-2 verified heads; card-4's PR head read back
	// differs from the pushed sha, so it is not a verified head.
	head1 := strings.Repeat("1", 40)
	head2 := strings.Repeat("2", 40)
	harvestCard(t, client, sprint, "card-1", 101, head1)
	harvestCard(t, client, sprint, "card-2", 102, head2)
	harvestCard(t, client, sprint, "card-4", 104, strings.Repeat("9", 40))
	if _, err := ok.Pass(ctx); err != nil {
		t.Fatalf("pass after harvested: %v", err)
	}

	for label, want := range map[string]struct {
		pr      int
		head    string
		readers int
	}{"card-1": {101, head1, 1}, "card-2": {102, head2, 2}} {
		got := reviewTasks(t, client, sprint, label)
		if len(got) != want.readers {
			t.Fatalf("%s has %d review tasks %v; want exactly %d", label, len(got), got, want.readers)
		}
		seen := map[string]bool{}
		harvested, _ := client.HGet(ctx, "s:"+sprint+":card:"+label, "harvested_at").Int64()
		for id, h := range got {
			friend := strings.TrimPrefix(id, fmt.Sprintf("read-%s-%d-%s-", ctlRepo, want.pr, want.head[:12]))
			if id != task.ReviewID(ctlRepo, want.pr, want.head, friend) {
				t.Fatalf("%s review id %q is not read-<repo>-<pr>-<head12>-<friend>", label, id)
			}
			if h["head"] != want.head || h["pr"] != strconv.Itoa(want.pr) {
				t.Fatalf("%s review %s at head %s pr %s; want exactly head %s pr %d", label, id, h["head"], h["pr"], want.head, want.pr)
			}
			if friend == "ctl-down" || friend == "ctl-a" && label == "card-1" {
				t.Fatalf("%s review went to %s (down or the author)", label, friend)
			}
			if seen[friend] {
				t.Fatalf("%s: two reads for %s", label, friend)
			}
			seen[friend] = true
			if _, err := client.ZScore(ctx, "s:"+sprint+":open:"+friend, id).Result(); err != nil {
				t.Fatalf("%s is not in %s's queue: %v", id, friend, err)
			}
			created := pushReceiptAt(t, client, sprint, id)
			if created-harvested > 60_000 || created < harvested {
				t.Fatalf("%s created %d ms after harvest; want within 60 s", id, created-harvested)
			}
		}
		if state, _ := client.HGet(ctx, "s:"+sprint+":card:"+label, "state").Result(); state != "review-ready" {
			t.Fatalf("%s state %q after its reads; want review-ready", label, state)
		}
	}
	if got := reviewTasks(t, client, sprint, "card-4"); len(got) != 0 {
		t.Fatalf("card-4 with an unverified head got reads %v", got)
	}
	if v, _ := client.HGet(ctx, "s:"+sprint+":unresolved", "card-4:unverified-head:09fbedc9").Result(); v == "" {
		t.Fatal("card-4's unverified head left no unresolved item")
	}

	// The read the consumer created is the one a friend takes and closes at
	// that head with a typed disposition (task take and done, #2929/#2745).
	for id, h := range reviewTasks(t, client, sprint, "card-1") {
		friend := strings.TrimPrefix(id, fmt.Sprintf("read-%s-101-%s-", ctlRepo, head1[:12]))
		claim, took, err := task.Take(ctx, st, task.TakeRequest{Sprint: sprint, ID: id, As: friend})
		if err != nil || !took {
			t.Fatalf("take %s as %s = %v, %v", id, friend, took, err)
		}
		got, err := task.Done(ctx, st, task.DoneRequest{Sprint: sprint, ID: id, Token: claim.Token,
			Evidence: "https://example.test/review", Verdict: "APPROVE", Score: "9", Head: h["head"]})
		if err != nil || got != task.DoneClosed {
			t.Fatalf("done %s = %s, %v; want DONE", id, got, err)
		}
	}

	pending, err := client.XPending(ctx, "s:"+sprint+":log", GroupOkFriend).Result()
	must(t, err)
	if pending.Count != 0 {
		t.Fatalf("ok-to-friend left %d events pending", pending.Count)
	}
	if at, _ := client.HGet(ctx, "proc:"+GroupOkFriend, "pass_at").Int64(); at == 0 {
		t.Fatal("proc:ok-to-friend has no pass_at")
	}
}

func pushReceiptAt(t *testing.T, client *redis.Client, sprint, id string) int64 {
	t.Helper()
	entries, err := client.XRange(context.Background(), "s:"+sprint+":log", "-", "+").Result()
	must(t, err)
	for _, e := range entries {
		if e.Values["kind"] == "task push" && e.Values["id"] == id {
			at, err := strconv.ParseInt(fmt.Sprint(e.Values["at"]), 10, 64)
			must(t, err)
			return at
		}
	}
	t.Fatalf("no task push receipt for %s", id)
	return 0
}

var errKilled = errors.New("killed between delivery and ack")

// TestControl14NoCardHandledTwice is #2756 control 14: kill ok-to-friend
// after one delivery and before its ack; on restart no card is handled twice
// and the undelivered event still arrives (Johnny 8).
func TestControl14NoCardHandledTwice(t *testing.T) {
	st, client := controlRedis(t)
	ctx := context.Background()
	sprint := "control-1414c0d2"
	seedSprint(t, client, sprint)

	first := newConsumer(st, sprint, "okf-a")
	first.Count = 1
	first.deliverHook = func(redis.XMessage) error { return errKilled }
	must(t, first.Start(ctx))

	endCard(t, client, sprint, "card-5", "DONE", "done", "internal/a/a.go", "")
	endCard(t, client, sprint, "card-6", "DONE", "done", "internal/b/b.go", "")
	if _, err := first.Pass(ctx); !errors.Is(err, errKilled) {
		t.Fatalf("first instance pass = %v; want the kill", err)
	}
	pending, err := client.XPending(ctx, "s:"+sprint+":log", GroupOkFriend).Result()
	must(t, err)
	if pending.Count != 1 {
		t.Fatalf("after the kill %d events pending; want the one delivered and unacked", pending.Count)
	}
	if n, _ := client.Exists(ctx, "s:"+sprint+":task:harvest-card-5", "s:"+sprint+":task:harvest-card-6").Result(); n != 0 {
		t.Fatal("the killed instance wrote a transition before its ack")
	}

	// Restart under a new instance name: it claims the dead instance's
	// pending entry, handles it once, then reads the undelivered event.
	second := newConsumer(st, sprint, "okf-b")
	must(t, second.Start(ctx))
	if _, err := second.Pass(ctx); err != nil {
		t.Fatalf("restart pass: %v", err)
	}
	head5 := strings.Repeat("5", 40)
	head6 := strings.Repeat("6", 40)
	harvestCard(t, client, sprint, "card-5", 105, head5)
	harvestCard(t, client, sprint, "card-6", 106, head6)

	// A second kill, this time after the CI cut hook ran and before the
	// review transition and its ack: the restart cuts again (ci cut is
	// idempotent per head) and still creates each read once.
	var cuts []string
	third := newConsumer(st, sprint, "okf-c")
	third.Count = 1
	third.CICut = func(_ context.Context, c CICut) error {
		cuts = append(cuts, c.Head)
		return errKilled
	}
	must(t, third.Start(ctx))
	if _, err := third.Pass(ctx); !errors.Is(err, errKilled) {
		t.Fatalf("third instance pass = %v; want the kill after the ci cut", err)
	}
	fourth := newConsumer(st, sprint, "okf-d")
	fourth.CICut = func(_ context.Context, c CICut) error {
		cuts = append(cuts, c.Head)
		return nil
	}
	must(t, fourth.Start(ctx))
	for i := 0; i < 2; i++ {
		if _, err := fourth.Pass(ctx); err != nil {
			t.Fatalf("fourth pass %d: %v", i, err)
		}
	}

	// A redelivery of an event already handled (an ack lost after the
	// function ran) returns the stored result and writes nothing.
	entries, err := client.XRange(ctx, "s:"+sprint+":log", "-", "+").Result()
	must(t, err)
	for _, e := range entries {
		if e.Values["kind"] != "card" || (e.Values["to"] != "ended" && e.Values["to"] != "harvested") {
			continue
		}
		if _, err := fourth.handleBatch(ctx, []redis.XMessage{e}); err != nil {
			t.Fatalf("redelivery of %s: %v", e.ID, err)
		}
		// Each event's first result is recorded once under its event id; a
		// redelivery reads it back (DUP) and never overwrites it.
		result, err := client.HGet(ctx, "s:"+sprint+":idem", GroupOkFriend+":"+e.ID).Result()
		if err != nil {
			t.Fatalf("event %s (%s %s) has no idempotency record: %v", e.ID, e.Values["id"], e.Values["to"], err)
		}
		switch e.Values["to"] {
		case "ended":
			if result != "CREATED harvest-"+fmt.Sprint(e.Values["id"]) {
				t.Fatalf("ended event %s recorded %q; want its one harvest creation", e.ID, result)
			}
		case "harvested":
			if !strings.HasPrefix(result, "CREATED read-") {
				t.Fatalf("harvested event %s recorded %q; want its read creation", e.ID, result)
			}
		}
		reply, err := client.FCall(ctx, FunctionOkFriendSkip, nil, sprint, GroupOkFriend, e.ID, "redelivered", "").Slice()
		if err != nil || len(reply) != 2 || reply[0] != "DUP" || reply[1] != result {
			t.Fatalf("redelivered %s = %v, %v; want DUP %q", e.ID, reply, err, result)
		}
	}

	pushes, cards := logCounts(t, client, sprint)
	for _, label := range []string{"card-5", "card-6"} {
		if pushes["harvest-"+label] != 1 {
			t.Fatalf("harvest-%s pushed %d times; want once", label, pushes["harvest-"+label])
		}
		if cards[label+" review-ready"] != 1 {
			t.Fatalf("%s moved to review-ready %d times; want once", label, cards[label+" review-ready"])
		}
		reads := reviewTasks(t, client, sprint, label)
		if len(reads) != 1 {
			t.Fatalf("%s has %d reads %v; want exactly one", label, len(reads), reads)
		}
		for id := range reads {
			if pushes[id] != 1 {
				t.Fatalf("%s pushed %d times; want once", id, pushes[id])
			}
		}
	}
	if !contains(cuts, head5) || !contains(cuts, head6) {
		t.Fatalf("ci cut heads %v; want both harvested heads cut", cuts)
	}
	pending, err = client.XPending(ctx, "s:"+sprint+":log", GroupOkFriend).Result()
	must(t, err)
	if pending.Count != 0 {
		t.Fatalf("%d events still pending after the restarts", pending.Count)
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

// TestOkFriendReviewConflictStaysHarvested: a required read id already taken
// by a different payload (create_task CONFLICT) must not leave the card
// review-ready without that read. The call writes no task, the card stays
// harvested, the event stays pending with a blocking unresolved item naming
// the id, and once the id is cleared the next pass routes the read and moves
// the card (stella's hold on #3052 at ba232061).
func TestOkFriendReviewConflictStaysHarvested(t *testing.T) {
	st, client := controlRedis(t)
	ctx := context.Background()
	sprint := "control-3052c0f1"
	seedSprint(t, client, sprint)
	ok := newConsumer(st, sprint, "okf-1")
	must(t, ok.Start(ctx))

	endCard(t, client, sprint, "card-5", "DONE", "done", "internal/x/c.go", "")
	if _, err := ok.Pass(ctx); err != nil {
		t.Fatalf("pass after ended: %v", err)
	}
	head := strings.Repeat("5", 40)
	// Every possible reader's deterministic read id is held by another payload.
	conflicts := map[string]string{}
	for _, f := range []string{"ctl-a", "ctl-b", "ctl-c"} {
		id := task.ReviewID(ctlRepo, 105, head, f)
		conflicts[id] = f
		must(t, client.HSet(ctx, "s:"+sprint+":task:"+id, "kind", "review", "state", "open",
			"payload_sha", "not-this-payload").Err())
	}
	harvestCard(t, client, sprint, "card-5", 105, head)

	_, err := ok.Pass(ctx)
	if !errors.Is(err, ErrReviewBlocked) {
		t.Fatalf("pass with a conflicting read id = %v; want ErrReviewBlocked", err)
	}
	if state, _ := client.HGet(ctx, "s:"+sprint+":card:card-5", "state").Result(); state != "harvested" {
		t.Fatalf("card-5 state %q after a CONFLICT; want harvested (review-ready needs every read)", state)
	}
	pending, err := client.XPending(ctx, "s:"+sprint+":log", GroupOkFriend).Result()
	must(t, err)
	if pending.Count != 1 {
		t.Fatalf("pending = %d after a CONFLICT; want the harvested event kept pending", pending.Count)
	}
	unresolved, err := client.HKeys(ctx, "s:"+sprint+":unresolved").Result()
	must(t, err)
	var blocked string
	for _, k := range unresolved {
		if strings.HasPrefix(k, "card-5:review-conflict:") {
			blocked = strings.TrimPrefix(k, "card-5:review-conflict:")
		}
	}
	if conflicts[blocked] == "" {
		t.Fatalf("unresolved %v names no conflicting read id; want card-5:review-conflict:<id>", unresolved)
	}
	pushes, cards := logCounts(t, client, sprint)
	for id := range conflicts {
		if pushes[id] != 0 {
			t.Fatalf("%s has a push receipt after a CONFLICT", id)
		}
	}
	if cards["card-5 review-ready"] != 0 {
		t.Fatal("card-5 has a review-ready receipt after a CONFLICT")
	}

	// A human clears the conflicting ids; the retried event routes the read.
	for id := range conflicts {
		must(t, client.Del(ctx, "s:"+sprint+":task:"+id).Err())
	}
	if _, err := ok.Pass(ctx); err != nil {
		t.Fatalf("pass after the ids were cleared: %v", err)
	}
	if got := reviewTasks(t, client, sprint, "card-5"); len(got) != 1 {
		t.Fatalf("card-5 has %d reads %v after the retry; want exactly 1", len(got), got)
	}
	if state, _ := client.HGet(ctx, "s:"+sprint+":card:card-5", "state").Result(); state != "review-ready" {
		t.Fatalf("card-5 state %q after the retry; want review-ready", state)
	}
	pending, err = client.XPending(ctx, "s:"+sprint+":log", GroupOkFriend).Result()
	must(t, err)
	if pending.Count != 0 {
		t.Fatalf("pending = %d after the retry; want 0", pending.Count)
	}
}

// TestOkFriendRunBacksOffOnBlocked: a persistent BLOCKED event keeps Run
// retrying with a bounded backoff, not a hot loop. The event stays pending
// and the card stays harvested for the whole run.
func TestOkFriendRunBacksOffOnBlocked(t *testing.T) {
	st, client := controlRedis(t)
	ctx := context.Background()
	sprint := "control-3052b0ff"
	seedSprint(t, client, sprint)
	ok := newConsumer(st, sprint, "okf-1")
	must(t, ok.Start(ctx))

	endCard(t, client, sprint, "card-6", "DONE", "done", "internal/x/d.go", "")
	if _, err := ok.Pass(ctx); err != nil {
		t.Fatalf("pass after ended: %v", err)
	}
	head := strings.Repeat("6", 40)
	for _, f := range []string{"ctl-a", "ctl-b", "ctl-c"} {
		id := task.ReviewID(ctlRepo, 106, head, f)
		must(t, client.HSet(ctx, "s:"+sprint+":task:"+id, "kind", "review", "state", "open",
			"payload_sha", "not-this-payload").Err())
	}
	harvestCard(t, client, sprint, "card-6", 106, head)

	ok.RetryWait = time.Second
	ok.RetryMax = 4 * time.Second
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	// Every pass is recorded with the pause that follows it (0 = none). The
	// pause is injected, so the test has no wall-clock bound: the eighth pass
	// cancels the run.
	var passes []error
	var pauses []time.Duration
	ok.passHook = func(err error) {
		passes = append(passes, err)
		pauses = append(pauses, 0)
		if len(passes) == 8 {
			cancel()
		}
	}
	ok.pause = func(_ context.Context, d time.Duration) { pauses[len(pauses)-1] = d }
	if err := ok.Run(runCtx); err != nil {
		t.Fatalf("Run with a persistent BLOCKED event = %v; want nil at ctx end", err)
	}
	want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 4 * time.Second,
		4 * time.Second, 4 * time.Second, 4 * time.Second}
	for i, err := range passes[:len(passes)-1] {
		if !errors.Is(err, ErrReviewBlocked) {
			t.Fatalf("pass %d = %v; want ErrReviewBlocked (the event retried)", i, err)
		}
		if pauses[i] != want[i] {
			t.Fatalf("pauses after blocked passes %v; want %v (bounded backoff, never a hot loop)", pauses, want)
		}
	}
	pending, err := client.XPending(ctx, "s:"+sprint+":log", GroupOkFriend).Result()
	must(t, err)
	if pending.Count != 1 {
		t.Fatalf("pending = %d after Run; want the BLOCKED event retained", pending.Count)
	}
	if state, _ := client.HGet(ctx, "s:"+sprint+":card:card-6", "state").Result(); state != "harvested" {
		t.Fatalf("card-6 state %q after Run; want harvested", state)
	}
	t.Logf("passes=%d pauses=%v", len(passes), pauses)
}

func TestOkFriendNextBackoffBounded(t *testing.T) {
	o := &OkFriend{RetryWait: time.Second, RetryMax: 4 * time.Second}
	var got []time.Duration
	var d time.Duration
	for i := 0; i < 5; i++ {
		d = o.nextBackoff(d)
		got = append(got, d)
	}
	want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 4 * time.Second, 4 * time.Second}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("backoff sequence %v; want %v", got, want)
		}
	}
	if d := (&OkFriend{}).nextBackoff(0); d != time.Second {
		t.Fatalf("default first backoff %v; want 1s", d)
	}
}
