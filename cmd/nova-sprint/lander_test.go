package main

import (
	"bytes"
	"context"
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
	seedLanderUnits(t, c, S, repo, []landerUnit{
		{n: 11, head: "aaaa1111", mergeable: "MERGEABLE", mergeableHead: "aaaa1111"},
		{n: 12, head: "bbbb2222", mergeable: "mergeable", mergeableHead: "bbbb2222"},
		{n: 13, head: "cccc3333", mergeable: "", mergeableHead: "cccc3333"},
	})

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
