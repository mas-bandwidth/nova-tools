package main

import (
	"bytes"
	"context"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/metrics/metricstest"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/land"
)

// landerFakes are the four program seams in process: a gate that answers the
// scripted verdicts in order, a bisect that finds every member green alone on
// a green base, a lander and a filer that record what reached them.
type landerFakes struct {
	mu       sync.Mutex
	verdicts []land.Verdict
	gated    [][]int
	landed   [][]int
	filed    []string
}

func numbers(b land.Batch) []int {
	out := make([]int, len(b.Members))
	for i, m := range b.Members {
		out[i] = m.Number
	}
	return out
}

func (f *landerFakes) Run(_ context.Context, b land.Batch, _ int) (land.Verdict, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.gated = append(f.gated, numbers(b))
	v := f.verdicts[0]
	f.verdicts = f.verdicts[1:]
	return v, nil
}

func (f *landerFakes) Alone(_ context.Context, b land.Batch, _ land.Verdict) (bool, []bool, error) {
	green := make([]bool, len(b.Members))
	for i := range green {
		green[i] = true
	}
	return true, green, nil
}

func (f *landerFakes) Land(_ context.Context, b land.Batch, _ land.Verdict) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.landed = append(f.landed, numbers(b))
	return nil
}

func (f *landerFakes) File(_ context.Context, repo, title, _ string) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.filed = append(f.filed, repo+" "+title)
	return 4242, nil
}

// quietClock does not block on the PollGap between UNKNOWN re-polls.
type quietClock struct {
	mu    sync.Mutex
	at    time.Time
	slept int
}

func (c *quietClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.at
}

func (c *quietClock) Sleep(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.at = c.at.Add(d)
	c.slept++
}

// TestLanderVerbServesMetrics (nx-g61, #2720; Stella's hold on #3259 at
// 64dbe017: no command ran the lander on dev). The production `nova-sprint
// lander --metrics-addr` builds land.Lane over the sprint's records in Redis
// and serves /metrics itself. A batch of three where #3 stays UNKNOWN keeps
// two; the gate goes red on a named test, every member passes alone, the
// retry is green and the batch lands. A GET on the verb's own listener, as it
// finishes, reads the batch depth (3), the members admitted (2), one forge
// latency per mergeable read (1+1+3) and one gate latency per run (2); the
// flaky hash is in Redis with the one issue filed.
func TestLanderVerbServesMetrics(t *testing.T) {
	addr := startThrowawayRedis(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = c.Close() })
	ctx := context.Background()
	const S, repo = "control-2720land", "nova-tools"
	pipe := c.TxPipeline()
	pipe.HSet(ctx, "s:"+S+":pr:"+repo+":11", "head", "aaaa1111", "mergeable", "MERGEABLE")
	pipe.HSet(ctx, "s:"+S+":pr:"+repo+":12", "head", "bbbb2222", "mergeable", "mergeable")
	pipe.HSet(ctx, "s:"+S+":pr:"+repo+":13", "head", "cccc3333", "mergeable", "")
	if _, err := pipe.Exec(ctx); err != nil {
		t.Fatal(err)
	}

	fakes := &landerFakes{verdicts: []land.Verdict{
		{Step: "test", Package: "internal/pulse", Test: "TestFlake"},
		{OK: true},
	}}
	clock := &quietClock{at: time.Date(2026, 9, 23, 20, 0, 0, 0, time.UTC)}
	seams, prevClock := landerSeams, landerClock
	landerSeams = func(landerPrograms) (land.Gate, land.Bisect, land.Lander, land.Filer) {
		return fakes, fakes, fakes, fakes
	}
	landerClock = clock
	t.Cleanup(func() { landerSeams, landerClock = seams, prevClock })
	scraped := metricstest.AtClose(t)

	var out, errOut bytes.Buffer
	code := runLander(ctx, []string{"--redis", addr, "--sprint", S, "--repo", repo, "--batch", "b-2720",
		"--gate", "gate", "--bisect", "bisect", "--land", "land", "--file", "file",
		"--metrics-addr", "127.0.0.1:0", "11", "12", "13"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("lander exit %d, want 0; stdout %q stderr %q", code, out.String(), errOut.String())
	}
	want := "LANDER batch=b-2720 repo=nova-tools landed=true runs=2 retries=1 kept=2 dropped=#13:mergeable-unknown class=gate-red flaky=flaky:nova-tools:internal/pulse.TestFlake filed=true\n"
	if !strings.Contains(out.String(), want) {
		t.Fatalf("stdout %q, want %q", out.String(), want)
	}
	if !strings.Contains(out.String(), "METRICS lander url=http://127.0.0.1:") {
		t.Fatalf("stdout %q, want the METRICS lander url line", out.String())
	}
	if len(fakes.landed) != 1 || len(fakes.landed[0]) != 2 || len(fakes.filed) != 1 || clock.slept != 2 {
		t.Fatalf("landed %v filed %v slept %d; want one landing of [11 12], one issue, two poll gaps", fakes.landed, fakes.filed, clock.slept)
	}
	flaky, err := c.HGetAll(ctx, "flaky:nova-tools:internal/pulse.TestFlake").Result()
	if err != nil || flaky["issue"] != "4242" || flaky["lanes_hit"] != "1" || flaky["first_seen"] != "2026-09-23T20:00:10Z" {
		t.Fatalf("flaky hash %v err %v; want issue 4242, lanes_hit 1, first_seen at the lane's clock", flaky, err)
	}
	body := scraped()
	if body == "" {
		t.Fatalf("lander never served /metrics; stdout %q", out.String())
	}
	metricstest.Want(t, body,
		`nova_queue_depth{component="lander"} 3`,
		`nova_leases_held{component="lander"} 2`,
		`nova_provider_latency_seconds_count{component="lander",provider="forge"} 5`,
		`nova_provider_latency_seconds_count{component="lander",provider="gate"} 2`,
	)
}

// TestLanderRefusesAMemberWithNoRecord: the lane gates only what the sprint
// knows, so a PR number with no head in Redis refuses before any gate runs.
func TestLanderRefusesAMemberWithNoRecord(t *testing.T) {
	addr := startThrowawayRedis(t)
	fakes := &landerFakes{}
	seams := landerSeams
	landerSeams = func(landerPrograms) (land.Gate, land.Bisect, land.Lander, land.Filer) {
		return fakes, fakes, fakes, fakes
	}
	t.Cleanup(func() { landerSeams = seams })
	var out, errOut bytes.Buffer
	code := runLander(context.Background(), []string{"--redis", addr, "--sprint", "control-none", "--repo", "nova-tools",
		"--batch", "b", "--gate", "g", "--bisect", "b", "--land", "l", "--file", "f", "99"}, &out, &errOut)
	if code != 2 || !strings.Contains(errOut.String(), "nova-tools#99 has no head") {
		t.Fatalf("exit %d stderr %q, want 2 and the missing record named", code, errOut.String())
	}
	if len(fakes.gated) != 0 {
		t.Fatalf("gate ran %v on a batch the sprint does not know", fakes.gated)
	}
}

// seedShadow writes pr:<name>:<n> records the way `pr record` and `pr lines`
// leave them: head, state, ci, mergeable and the newline-joined typed reads.
func seedShadow(t *testing.T, c *redis.Client, repo string, recs map[int][]string) {
	t.Helper()
	ctx := context.Background()
	pipe := c.TxPipeline()
	for n, kv := range recs {
		args := make([]any, len(kv))
		for i, v := range kv {
			args[i] = v
		}
		pipe.HSet(ctx, "pr:"+repo+":"+strconv.Itoa(n), args...)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		t.Fatal(err)
	}
}

// shadowFixture is one member per verdict: #1 lands, #2 waits on ci, #3 is
// read under the floor, #4 is held at head, #5 does not merge, #6 was read
// only by jev. A read at an older head never counts.
func shadowFixture(t *testing.T, c *redis.Client) {
	seedShadow(t, c, "nova-tools", map[int][]string{
		1: {"head", "aaaa1111ffff", "state", "open", "ci", "green", "mergeable", "true",
			"reads", "SCORE who=emma head=0000aaaa score=10/10\nSCORE who=emma head=aaaa1111 score=9/10"},
		2: {"head", "bbbb2222ffff", "state", "open", "ci", "pending", "mergeable", "",
			"reads", "SCORE who=stella head=bbbb2222 score=8/10"},
		3: {"head", "cccc3333ffff", "state", "open", "ci", "green", "mergeable", "true",
			"reads", "SCORE who=emma head=cccc3333 score=7/10"},
		4: {"head", "dddd4444ffff", "state", "open", "ci", "green", "mergeable", "true",
			"reads", "SCORE who=emma head=dddd4444 score=9/10\nHOLD who=stella head=dddd4444 reason=scope"},
		5: {"head", "eeee5555ffff", "state", "open", "ci", "green", "mergeable", "false",
			"reads", "SCORE who=emma head=eeee5555 score=9/10"},
		6: {"head", "ffff6666ffff", "state", "open", "ci", "green", "mergeable", "true",
			"reads", "SCORE who=jev head=ffff6666 score=10/10"},
	})
}

var shadowWant = []string{
	"SHADOW nova-tools#1 head=aaaa1111 verdict=LAND why=read:emma=9,ci:green",
	"SHADOW nova-tools#2 head=bbbb2222 verdict=WAIT-CI why=ci:pending",
	"SHADOW nova-tools#3 head=cccc3333 verdict=NO-READ why=score:7<8",
	"SHADOW nova-tools#4 head=dddd4444 verdict=HOLD why=hold:stella",
	"SHADOW nova-tools#5 head=eeee5555 verdict=CONFLICT why=mergeable:false",
	"SHADOW nova-tools#6 head=ffff6666 verdict=NO-READ why=no-read-at-head",
	"LANDER shadow repo=nova-tools members=6 land=1 wait-ci=1 no-read=2 hold=1 conflict=1 no-record=0",
}

// TestLanderShadowNeedsNoPrograms (nova-tools#3613): `lander --shadow` runs
// with no --gate/--bisect/--land/--file, no --sprint and no --batch, never
// builds the program seams, and prints one SHADOW line per member in the
// order given, the first gate that stops it, then its receipt.
func TestLanderShadowNeedsNoPrograms(t *testing.T) {
	addr := startThrowawayRedis(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = c.Close() })
	shadowFixture(t, c)
	seams := landerSeams
	landerSeams = func(landerPrograms) (land.Gate, land.Bisect, land.Lander, land.Filer) {
		t.Errorf("--shadow built the program seams")
		return nil, nil, nil, nil
	}
	t.Cleanup(func() { landerSeams = seams })

	var out, errOut bytes.Buffer
	code := runLander(context.Background(), []string{"--shadow", "--redis", addr, "--repo", "mas-bandwidth/nova-tools",
		"1", "2", "3", "4", "5", "6"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("lander --shadow exit %d, want 0; stdout %q stderr %q", code, out.String(), errOut.String())
	}
	if got := strings.Split(strings.TrimSpace(out.String()), "\n"); strings.Join(got, "\n") != strings.Join(shadowWant, "\n") {
		t.Fatalf("stdout\n%s\nwant\n%s", out.String(), strings.Join(shadowWant, "\n"))
	}
}

// redisWrites are the write commands a shadow run must never send.
var redisWrites = []string{"set", "setnx", "hset", "hsetnx", "hmset", "hdel", "hincrby", "del", "unlink", "expire",
	"pexpire", "incr", "incrby", "sadd", "srem", "zadd", "zrem", "zincrby", "xadd", "xack", "lpush", "rpush",
	"eval", "evalsha", "fcall", "function", "publish", "rename", "copy"}

// dumpAll is every key's DUMP: the whole keyspace, byte for byte.
func dumpAll(t *testing.T, c *redis.Client) map[string]string {
	t.Helper()
	ctx := context.Background()
	keys, err := c.Keys(ctx, "*").Result() // test-only: a throwaway server
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, k := range keys {
		v, err := c.Dump(ctx, k).Result()
		if err != nil {
			t.Fatal(err)
		}
		out[k] = v
	}
	return out
}

// TestLanderShadowWritesNothing (nova-tools#3613): after a shadow run over
// every verdict the keyspace is byte-identical and the server counted no
// write command at all (INFO commandstats since a CONFIG RESETSTAT).
func TestLanderShadowWritesNothing(t *testing.T) {
	addr := startThrowawayRedis(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = c.Close() })
	ctx := context.Background()
	shadowFixture(t, c)
	if err := c.HSet(ctx, "cfg:land", "min_score", "8").Err(); err != nil {
		t.Fatal(err)
	}
	before := dumpAll(t, c)
	if err := c.ConfigResetStat(ctx).Err(); err != nil {
		t.Fatal(err)
	}

	var out, errOut bytes.Buffer
	code := runLander(ctx, []string{"--shadow", "--redis", addr, "--repo", "nova-tools", "1", "2", "3", "4", "5", "6", "77"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("lander --shadow exit %d, want 0; stdout %q stderr %q", code, out.String(), errOut.String())
	}
	stats, err := c.Info(ctx, "commandstats").Result()
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range redisWrites {
		if strings.Contains(stats, "cmdstat_"+w+":") {
			t.Errorf("shadow run sent %s; commandstats:\n%s", strings.ToUpper(w), stats)
		}
	}
	if !strings.Contains(stats, "cmdstat_hgetall:") {
		t.Fatalf("commandstats shows no record read; the check would pass vacuously:\n%s", stats)
	}
	after := dumpAll(t, c)
	if len(after) != len(before) {
		t.Fatalf("keyspace %d keys after, %d before", len(after), len(before))
	}
	for k, v := range before {
		if after[k] != v {
			t.Errorf("key %s changed in a shadow run", k)
		}
	}
}

// TestLanderShadowNoRecordIsALine (nova-tools#3613): a member with no record
// is its own NO-RECORD line, the batch is not refused, the other members
// still get their verdicts and the verb exits 0 (the plain lander refuses
// the whole batch, TestLanderRefusesAMemberWithNoRecord).
func TestLanderShadowNoRecordIsALine(t *testing.T) {
	addr := startThrowawayRedis(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = c.Close() })
	shadowFixture(t, c)
	seedShadow(t, c, "nova-tools", map[int][]string{8: {"state", "open", "ci", "green"}})

	var out, errOut bytes.Buffer
	code := runLander(context.Background(), []string{"--shadow", "--redis", addr, "--repo", "nova-tools", "99", "#1", "8"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("lander --shadow exit %d, want 0 with a member missing; stdout %q stderr %q", code, out.String(), errOut.String())
	}
	want := strings.Join([]string{
		"SHADOW nova-tools#99 head=- verdict=NO-RECORD why=no-record",
		"SHADOW nova-tools#1 head=aaaa1111 verdict=LAND why=read:emma=9,ci:green",
		"SHADOW nova-tools#8 head=- verdict=NO-RECORD why=no-head",
		"LANDER shadow repo=nova-tools members=3 land=1 wait-ci=0 no-read=0 hold=0 conflict=0 no-record=2",
	}, "\n")
	if got := strings.TrimSpace(out.String()); got != want {
		t.Fatalf("stdout\n%s\nwant\n%s", got, want)
	}
	if errOut.Len() != 0 {
		t.Fatalf("stderr %q, want nothing: a missing record is a line, not a refusal", errOut.String())
	}
}

// TestLanderShadowRecordAppendsStream (nova-tools#3821): lander --shadow --record
// appends each SHADOW verdict to land:<repo>:shadow (fields repo, n, head, verdict, why, at)
// in one pipeline, writing nothing else.
func TestLanderShadowRecordAppendsStream(t *testing.T) {
	addr := startThrowawayRedis(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = c.Close() })
	ctx := context.Background()
	shadowFixture(t, c)
	if err := c.HSet(ctx, "cfg:land", "min_score", "8").Err(); err != nil {
		t.Fatal(err)
	}

	beforeKeys, err := c.Keys(ctx, "*").Result()
	if err != nil {
		t.Fatal(err)
	}

	var out, errOut bytes.Buffer
	code := runLander(ctx, []string{"--shadow", "--record", "--redis", addr, "--repo", "mas-bandwidth/nova-tools", "1", "2", "3", "4", "5", "6"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("lander --shadow --record exit %d: %s", code, errOut.String())
	}

	// Verify only land:nova-tools:shadow was created
	afterKeys, err := c.Keys(ctx, "*").Result()
	if err != nil {
		t.Fatal(err)
	}
	if len(afterKeys) != len(beforeKeys)+1 {
		t.Fatalf("expected 1 new key, got before=%d after=%d", len(beforeKeys), len(afterKeys))
	}

	msgs, err := c.XRange(ctx, "land:nova-tools:shadow", "-", "+").Result()
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 6 {
		t.Fatalf("expected 6 messages in stream, got %d", len(msgs))
	}

	for i, m := range msgs {
		repo := m.Values["repo"]
		if repo != "nova-tools" {
			t.Errorf("msg %d repo=%v, want nova-tools", i, repo)
		}
		if _, ok := m.Values["n"]; !ok {
			t.Errorf("msg %d missing field n", i)
		}
		if _, ok := m.Values["head"]; !ok {
			t.Errorf("msg %d missing field head", i)
		}
		if _, ok := m.Values["verdict"]; !ok {
			t.Errorf("msg %d missing field verdict", i)
		}
		if _, ok := m.Values["why"]; !ok {
			t.Errorf("msg %d missing field why", i)
		}
		atVal, ok := m.Values["at"].(string)
		if !ok || atVal == "" {
			t.Errorf("msg %d missing or invalid field at: %v", i, m.Values["at"])
		} else if _, err := time.Parse(time.RFC3339, atVal); err != nil {
			t.Errorf("msg %d at timestamp not RFC3339: %v", i, err)
		}
	}
}
