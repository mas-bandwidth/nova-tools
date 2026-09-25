package benchreset

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/goenv"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/deal"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
)

type fakeStopper struct {
	status string
	before func([]Card)
}

func (f fakeStopper) Stop(_ context.Context, _ deal.Bench, cards []Card, _ time.Duration) ([]StopResult, error) {
	if f.before != nil {
		f.before(cards)
	}
	out := make([]StopResult, len(cards))
	for i, c := range cards {
		out[i] = StopResult{Card: c, Status: f.status}
	}
	return out, nil
}

func resetRedis(t *testing.T) *redis.Client {
	t.Helper()
	addr := testutil.Start(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = c.Close() })
	if err := fn.Load(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	return c
}

func seedResetCards(t *testing.T, c *redis.Client, states ...string) []Card {
	t.Helper()
	ctx := context.Background()
	bench := "b-test"
	pipe := c.Pipeline()
	pipe.SAdd(ctx, "benches", bench)
	pipe.HSet(ctx, "bench:"+bench+":beat", "host", "example.invalid", "user", "tester", "at", "1")
	pipe.HSet(ctx, "bench:"+bench+":state", "state", "UP") // dev deals only to UP benches
	pipe.HSet(ctx, "bench:"+bench+":desired", "slots", "8")
	var cards []Card
	for i, state := range states {
		S := "s-one"
		if i%2 == 1 {
			S = "s-two"
		}
		label := fmt.Sprintf("card-%d", i)
		attempt := i + 1
		card := Card{Sprint: S, Label: label, Attempt: attempt, State: state}
		cards = append(cards, card)
		token := fmt.Sprintf("%d.%032x", attempt, i+1)
		sum := sha256.Sum256([]byte(token))
		sha := hex.EncodeToString(sum[:])[:12]
		ck := "s:" + S + ":card:" + label
		pipe.HSet(ctx, ck, "state", state, "bench", bench, "attempt", attempt, "token", token, "token_sha", sha, "pin", bench, "priority", i, "retries", "7")
		pipe.SAdd(ctx, "s:"+S+":idx:card:"+state, label)
		pipe.ZAdd(ctx, "s:"+S+":bench:"+bench+":queue", redis.Z{Score: float64(i), Member: label})
		// the card is in the bench's one working set (#3998) by its id
		pipe.ZAdd(ctx, "bench:"+bench+":cards:working", redis.Z{Score: 1, Member: ck})
	}
	if _, err := pipe.Exec(ctx); err != nil {
		t.Fatal(err)
	}
	return cards
}

func TestBenchResetRequeuesInFlight(t *testing.T) {
	t.Run("requeue", func(t *testing.T) {
		c := resetRedis(t)
		cards := seedResetCards(t, c, "dealt", "launched", "running")
		ctx := context.Background()
		stop := fakeStopper{status: "STOPPED", before: func(got []Card) {
			if len(got) != 3 {
				t.Fatalf("stop cards=%d", len(got))
			}
			guardResetting(t, c, cards[0])
		}}
		res, err := Reset(ctx, c, Request{Bench: "b-test", Actor: "stella", ID: "reset-a", Stopper: stop, Grace: time.Millisecond})
		if err != nil {
			t.Fatal(err)
		}
		if res.Code != 0 || res.Requeued != 3 || res.Alive != 0 {
			t.Fatalf("result %+v", res)
		}
		for i, card := range cards {
			ck := "s:" + card.Sprint + ":card:" + card.Label
			h := c.HMGet(ctx, ck, "state", "token", "reason", "retries", "pin").Val()
			if fmt.Sprint(h[0]) != "queued" || fmt.Sprint(h[1]) != "" || fmt.Sprint(h[2]) != "bench-reset" || fmt.Sprint(h[3]) != "7" || fmt.Sprint(h[4]) != "b-test" {
				t.Fatalf("%s hash=%v", card.Identity(), h)
			}
			if c.ZScore(ctx, "s:"+card.Sprint+":pool", card.Label).Err() != nil {
				t.Fatalf("%s not pooled", card.Identity())
			}
			if c.ZScore(ctx, "s:"+card.Sprint+":bench:b-test:queue", card.Label).Err() != nil {
				t.Fatalf("%s lost its pinned bench queue score", card.Identity())
			}
			token := fmt.Sprintf("%d.%032x", card.Attempt, i+1)
			reply, err := c.FCall(ctx, "ns_card_beat", []string{ck, "s:" + card.Sprint + ":log", "s:" + card.Sprint + ":idem"}, card.Sprint, card.Label, token).Text()
			if err != nil || !strings.HasPrefix(reply, "3|FENCED|") {
				t.Fatalf("%s stale token reply=%v err=%v", card.Identity(), reply, err)
			}
		}
		if c.ZCard(ctx, "bench:b-test:cards:working").Val() != 0 {
			t.Fatal("bench still leased")
		}
		if c.Exists(ctx, "bench:b-test:reset").Val() != 0 {
			t.Fatal("reset record remains")
		}
	})

	t.Run("keep_queue", func(t *testing.T) {
		c := resetRedis(t)
		cards := seedResetCards(t, c, "dealt", "launched", "running")
		ctx := context.Background()
		seen := 0
		res, err := Reset(ctx, c, Request{Bench: "b-test", Actor: "stella", ID: "reset-keep", KeepQueue: true, Stopper: fakeStopper{status: "GONE", before: func(got []Card) { seen = len(got) }}})
		if err != nil {
			t.Fatal(err)
		}
		if res.Code != 0 || res.Kept != 1 || res.Requeued != 2 || seen != 2 {
			t.Fatalf("result=%+v stopped=%d", res, seen)
		}
		if got := c.HGet(ctx, "s:"+cards[0].Sprint+":card:"+cards[0].Label, "state").Val(); got != "dealt" {
			t.Fatalf("dealt state=%s", got)
		}
	})

	t.Run("held_survives_clock", func(t *testing.T) {
		c := resetRedis(t)
		cards := seedResetCards(t, c, "running")
		ctx := context.Background()
		seedDealLease(t, c)
		res, err := Reset(ctx, c, Request{Bench: "b-test", Actor: "stella", ID: "reset-held", Stopper: fakeStopper{status: "ALIVE"}})
		if err != nil {
			t.Fatal(err)
		}
		if res.Code != 1 || res.Alive != 1 {
			t.Fatalf("result %+v", res)
		}
		if got := c.HGet(ctx, "bench:b-test:reset", "phase").Val(); got != "held" {
			t.Fatalf("phase=%s", got)
		}
		if ttl := c.TTL(ctx, "bench:b-test:reset").Val(); ttl != -1 {
			t.Fatalf("ttl=%s", ttl)
		}
		c.HSet(ctx, "bench:b-test:reset", "at", time.Now().Add(-121*time.Second).UnixMilli())
		guardResetting(t, c, cards[0])
		res, err = Reset(ctx, c, Request{Bench: "b-test", Actor: "stella", ID: "reset-retry", Stopper: fakeStopper{status: "STOPPED"}})
		if err != nil || res.Code != 0 {
			t.Fatalf("rerun %+v err=%v", res, err)
		}
		if c.Exists(ctx, "bench:b-test:reset").Val() != 0 {
			t.Fatal("record remains")
		}
		reply := dealOne(t, c, cards[0], cards[0].Attempt+1)
		if len(reply) == 0 || reply[0] != "DEALT" {
			t.Fatalf("deal after reset=%v", reply)
		}
	})

	t.Run("takeover_fences_old", func(t *testing.T) {
		c := resetRedis(t)
		cards := seedResetCards(t, c, "running")
		ctx := context.Background()
		mustReply(t, c.FCall(ctx, FunctionBegin, nil, "b-test", "run-a", "stella"), "STARTED")
		c.HSet(ctx, "bench:b-test:reset", "at", time.Now().Add(-121*time.Second).UnixMilli())
		mustReply(t, c.FCall(ctx, FunctionBegin, nil, "b-test", "run-b", "stella"), "STARTED")
		before := c.Dump(ctx, "bench:b-test:reset").Val()
		r := mustReply(t, c.FCall(ctx, FunctionRequeue, nil, "b-test", "run-a", "stella", cards[0].Sprint, cards[0].Label, strconv.Itoa(cards[0].Attempt), "STOPPED"), "FENCED")
		_ = r
		mustReply(t, c.FCall(ctx, FunctionEnd, nil, "b-test", "run-a", "stella", "0", "0", "0", "0"), "FENCED")
		after := c.Dump(ctx, "bench:b-test:reset").Val()
		if before != after {
			t.Fatal("old run changed takeover record")
		}
		if got := c.HGet(ctx, "s:"+cards[0].Sprint+":card:"+cards[0].Label, "state").Val(); got != "running" {
			t.Fatalf("state=%s", got)
		}
	})

	t.Run("clear", func(t *testing.T) {
		c := resetRedis(t)
		seedResetCards(t, c, "running")
		ctx := context.Background()
		res, err := Reset(ctx, c, Request{Bench: "b-test", Actor: "stella", ID: "held-clear", Stopper: fakeStopper{status: "ALIVE"}})
		if err != nil || res.Code != 1 {
			t.Fatalf("held %+v %v", res, err)
		}
		clear, err := Clear(ctx, c, "b-test", "operator", "receipted recovery")
		if err != nil || clear.Code != 0 {
			t.Fatalf("clear %+v %v", clear, err)
		}
		if c.Exists(ctx, "bench:b-test:reset").Val() != 0 {
			t.Fatal("clear left record")
		}
		msgs := c.XRevRangeN(ctx, "cap:log", "+", "-", 10).Val()
		found := false
		for _, m := range msgs {
			if fmt.Sprint(m.Values["kind"]) == "bench-reset-cleared" && fmt.Sprint(m.Values["why"]) == "receipted recovery" {
				found = true
			}
		}
		if !found {
			t.Fatal("clear receipt missing")
		}
		mustReply(t, c.FCall(ctx, FunctionBegin, nil, "b-test", "fresh", "stella"), "STARTED")
		busy, err := Clear(ctx, c, "b-test", "operator", "too soon")
		if err != nil || busy.Code != 2 || busy.Why != "busy" {
			t.Fatalf("fresh clear %+v %v", busy, err)
		}
	})
}

func seedDealLease(t *testing.T, c *redis.Client) {
	t.Helper()
	c.HSet(context.Background(), "lease:reconciler", "token", "fence", "instance", "test", "host", "test")
}
func guardResetting(t *testing.T, c *redis.Client, card Card) {
	t.Helper()
	seedDealLease(t, c)
	reply := dealOne(t, c, card, card.Attempt+1)
	if len(reply) != 2 || reply[0] != "NONE" || reply[1] != "resetting" {
		t.Fatalf("deal guard=%v", reply)
	}
}
func dealOne(t *testing.T, c *redis.Client, card Card, attempt int) []string {
	t.Helper()
	token := fmt.Sprintf("%d.%032x", attempt, 99)
	sum := sha256.Sum256([]byte(token))
	sha := hex.EncodeToString(sum[:])[:12]
	reply, err := c.FCall(context.Background(), "ns_card_deal", nil, "b-test", "fence", "test", "idem", card.Sprint, card.Label, strconv.Itoa(attempt), token, sha).StringSlice()
	if err != nil {
		t.Fatal(err)
	}
	return reply
}
func mustReply(t *testing.T, cmd *redis.Cmd, want string) []string {
	t.Helper()
	reply, err := cmd.StringSlice()
	if err != nil {
		t.Fatal(err)
	}
	if len(reply) == 0 || reply[0] != want {
		t.Fatalf("reply=%v want=%s", reply, want)
	}
	return reply
}

// fakeBench is a fake ssh Program for RemoteStopper (testguard accepts a
// program under a temp dir). It runs the remote command word benchsh sends
// (`bash -s -- <quoted args>`, the stop script on stdin) as the bench would,
// with a HOME whose .bash_profile puts a fake nova-sprint (novaSprint, a bash
// body) first on the login PATH the script's `bash -lc` reads.
func fakeBench(t *testing.T, novaSprint string) string {
	t.Helper()
	dir := t.TempDir()
	bin, home := filepath.Join(dir, "bin"), filepath.Join(dir, "home")
	for _, d := range []string{bin, home} {
		if err := os.Mkdir(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	files := []struct {
		path, body string
		mode       os.FileMode
	}{
		{filepath.Join(bin, "nova-sprint"), "#!/bin/bash\n" + novaSprint, 0o755},
		{filepath.Join(home, ".bash_profile"), fmt.Sprintf("PATH=%q:\"$PATH\"\n", bin), 0o644},
		{filepath.Join(dir, "fake-ssh"), fmt.Sprintf("#!/bin/bash\nexport HOME=%q\neval \"${@: -1}\"\n", home), 0o755},
	}
	for _, f := range files {
		if err := os.WriteFile(f.path, []byte(f.body), f.mode); err != nil {
			t.Fatal(err)
		}
	}
	return filepath.Join(dir, "fake-ssh")
}

// TestRemoteStopperDrivesRealCardStop runs the built `nova-sprint card stop`
// behind RemoteStopper through a fake ssh Program (#3589 rowan hold 2, item 1):
// the real bench-side stdout must parse. No `nova-card` group with these
// identities exists, so every card answers GONE and nothing is signalled.
func TestRemoteStopperDrivesRealCardStop(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "nova-sprint")
	build := exec.Command("go", "build", "-o", bin, "github.com/mas-bandwidth/nova-tools/cmd/nova-sprint")
	build.Env = goenv.Clean(os.Environ())
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build nova-sprint: %v\n%s", err, out)
	}
	errFile := filepath.Join(t.TempDir(), "stderr")
	program := fakeBench(t, fmt.Sprintf(`case "$*" in
  "card stop --stdin --grace 10ms") ;;
  *) echo "unexpected nova-sprint args: $*" >&2; exit 97 ;;
esac
exec %q "$@" 2>%q
`, bin, errFile))
	cards := []Card{
		{Sprint: "s-rstop-3589", Label: "card-a", Attempt: 1},
		{Sprint: "s-rstop-3589", Label: "card-b", Attempt: 2},
	}
	got, err := RemoteStopper{Program: program}.Stop(context.Background(), deal.Bench{Name: "b-test", Host: "example.invalid", User: "tester"}, cards, 10*time.Millisecond)
	if err != nil {
		t.Fatalf("real card stop reply did not parse: %v", err)
	}
	if len(got) != len(cards) {
		t.Fatalf("results %+v", got)
	}
	for i, r := range got {
		if r.Card.Identity() != cards[i].Identity() || r.Status != "GONE" {
			t.Fatalf("result %d = %+v", i, r)
		}
	}
	summary, err := os.ReadFile(errFile)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(summary)) != "STOP stopped=0 gone=2 alive=0" {
		t.Fatalf("stderr summary %q", summary)
	}
}

// TestBenchResetAliveRequeuesSiblings drives Reset through RemoteStopper with
// a bench that answers the card-stop protocol with one STOPPED and one ALIVE
// card and exits 0 (#3589 rowan hold 2, item 2): the STOPPED card is requeued,
// the ALIVE card stays running, and the record is held with why=alive:1, not
// an ssh failure.
func TestBenchResetAliveRequeuesSiblings(t *testing.T) {
	c := resetRedis(t)
	cards := seedResetCards(t, c, "running", "running")
	ctx := context.Background()
	program := fakeBench(t, `n=0
while read -r s l a; do
  [ -z "$s" ] && continue
  if [ "$n" -eq 0 ]; then echo "STOPPED $s/$l/$a"; else echo "ALIVE $s/$l/$a"; fi
  n=$((n+1))
done
echo "STOP stopped=1 gone=0 alive=1" >&2
exit 0
`)
	res, err := Reset(ctx, c, Request{Bench: "b-test", Actor: "stella", ID: "reset-alive", Stopper: RemoteStopper{Program: program}, Grace: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if res.Code != 1 || res.Why != "alive:1" || res.Requeued != 1 || res.Alive != 1 {
		t.Fatalf("result %+v", res)
	}
	first := c.HGet(ctx, "s:"+cards[0].Sprint+":card:"+cards[0].Label, "state").Val()
	second := c.HGet(ctx, "s:"+cards[1].Sprint+":card:"+cards[1].Label, "state").Val()
	if first != "queued" || second != "running" {
		t.Fatalf("states first=%s second=%s", first, second)
	}
	h := c.HMGet(ctx, "bench:b-test:reset", "phase", "why").Val()
	if fmt.Sprint(h[0]) != "held" || fmt.Sprint(h[1]) != "alive:1" {
		t.Fatalf("reset record %v", h)
	}
}

// TestBenchResetBeatErrorHolds: a beat error that is not a fence (here the
// caller's context is cancelled mid-stop) holds the record with its receipt
// instead of reporting fenced and leaving it running (#3589 rowan hold 2,
// item 5).
func TestBenchResetBeatErrorHolds(t *testing.T) {
	c := resetRedis(t)
	seedResetCards(t, c, "running")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stop := fakeStopper{status: "STOPPED", before: func([]Card) {
		cancel()
		time.Sleep(100 * time.Millisecond) // the beater sees ctx.Done first
	}}
	res, err := Reset(ctx, c, Request{Bench: "b-test", Actor: "stella", ID: "reset-beat", Stopper: stop, BeatEvery: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	if res.Code != 1 || !strings.HasPrefix(res.Why, "beat:") {
		t.Fatalf("result %+v; want a hold with why=beat:..., not fenced", res)
	}
	bg := context.Background()
	h := c.HMGet(bg, "bench:b-test:reset", "phase", "why").Val()
	if fmt.Sprint(h[0]) != "held" || !strings.HasPrefix(fmt.Sprint(h[1]), "beat:") {
		t.Fatalf("reset record %v", h)
	}
	found := false
	for _, m := range c.XRevRangeN(bg, "cap:log", "+", "-", 10).Val() {
		if fmt.Sprint(m.Values["kind"]) == "bench-reset-held" && strings.HasPrefix(fmt.Sprint(m.Values["why"]), "beat:") {
			found = true
		}
	}
	if !found {
		t.Fatal("bench-reset-held receipt missing")
	}
}
