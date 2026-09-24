package land

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// Reliability for the lander, Issue #3139 rev 7 build B9: land serve's reclaim sweeper (§5.6),
// the STUCK alarm, step deadlines (§8.6), and benching (§8.3, L20). Refusals print through the
// one table in refusals.go (§8.1).

const (
	// DefaultSweepInterval is land serve's reclaim tick (§5.6: detection <= 15 s TTL + 2 s sweep).
	DefaultSweepInterval = 2 * time.Second
	// DefaultStepP99 stands in for a class p99 no `land measure` has written for a bench yet.
	DefaultStepP99 = 10 * time.Minute
	// BadGatesToBench is how many bad gates in a row bench a bench (§8.3).
	BadGatesToBench = 3
)

// BenchLandKey is bench:<b>:land (§2.2): slots, per-class p99_ms_<class>, consecutive_bad, benched.
func BenchLandKey(bench string) string { return "bench:" + bench + ":land" }

// Sweeper is land serve's reclaim sweeper (§5.6, §7): every tick it requeues each gating batch
// whose worker heartbeat is gone (DEAD) and names each gating batch whose live worker has held it
// longer than twice its class p99 on that bench (STUCK; the step deadline makes that gate ERROR).
type Sweeper struct {
	Client   *redis.Client
	Repo     string
	Base     string
	Interval time.Duration    // 0 is DefaultSweepInterval
	Now      func() time.Time // nil is time.Now; claimed_at is Redis ms
	Log      io.Writer        // Run's lines; nil is os.Stdout
}

// StuckGate is one STUCK alarm.
type StuckGate struct {
	BatchID, Bench, Slot, Class string
	Age, Bound                  time.Duration
}

// SweepReport is one tick: the requeues, the alarms, and their lines in order.
type SweepReport struct {
	Requeued []RequeuedBatch
	Stuck    []StuckGate
	Lines    []string
}

// TickInterval is the sweeper's tick.
func (s *Sweeper) TickInterval() time.Duration {
	if s.Interval > 0 {
		return s.Interval
	}
	return DefaultSweepInterval
}

func (s *Sweeper) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// Tick runs one sweep.
func (s *Sweeper) Tick(ctx context.Context) (SweepReport, error) {
	var rep SweepReport
	requeued, err := SweepReclaim(ctx, s.Client, s.Repo, s.Base)
	if err != nil {
		return rep, err
	}
	for _, r := range requeued {
		rep.Requeued = append(rep.Requeued, r)
		rep.Lines = append(rep.Lines, fmt.Sprintf("DEAD b%s worker=%s/%s attempt=%d", r.BatchID, r.OldBench, r.OldSlot, r.Attempt))
	}
	stuck, err := s.stuck(ctx)
	if err != nil {
		return rep, err
	}
	for _, g := range stuck {
		rep.Stuck = append(rep.Stuck, g)
		rep.Lines = append(rep.Lines, fmt.Sprintf("STUCK b%s worker=%s/%s class=%s age=%ds bound=%ds",
			g.BatchID, g.Bench, g.Slot, g.Class, int64(g.Age/time.Second), int64(g.Bound/time.Second)))
	}
	return rep, nil
}

// stuck reads the chain in three pipelined round trips (batches, then worker keys and p99s).
func (s *Sweeper) stuck(ctx context.Context) ([]StuckGate, error) {
	ids, err := s.Client.ZRange(ctx, ChainKey(s.Repo, s.Base), 0, -1).Result()
	if err != nil || len(ids) == 0 {
		return nil, err
	}
	pipe := s.Client.Pipeline()
	hm := make([]*redis.SliceCmd, len(ids))
	for i, id := range ids {
		hm[i] = pipe.HMGet(ctx, BatchKey(s.Repo, s.Base, id), "state", "bench", "slot", "class", "claimed_at")
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, fmt.Errorf("stuck sweep batches: %w", err)
	}
	type cand struct {
		g       StuckGate
		claimed int64
		alive   *redis.IntCmd
		p99     *redis.StringCmd
	}
	var cands []*cand
	pipe = s.Client.Pipeline()
	for i, id := range ids {
		v := hm[i].Val()
		if len(v) < 5 || str(v[0]) != "gating" || str(v[1]) == "" || str(v[2]) == "" {
			continue
		}
		claimed, err := strconv.ParseInt(str(v[4]), 10, 64)
		if err != nil || claimed <= 0 {
			continue
		}
		class := str(v[3])
		if class == "" {
			class = ClassGo
		}
		c := &cand{g: StuckGate{BatchID: id, Bench: str(v[1]), Slot: str(v[2]), Class: class}, claimed: claimed}
		c.alive = pipe.Exists(ctx, "worker:"+c.g.Bench+":"+c.g.Slot)
		c.p99 = pipe.HGet(ctx, BenchLandKey(c.g.Bench), "p99_ms_"+class)
		cands = append(cands, c)
	}
	if len(cands) == 0 {
		return nil, nil
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, fmt.Errorf("stuck sweep workers: %w", err)
	}
	now := s.now().UnixMilli()
	var out []StuckGate
	for _, c := range cands {
		if c.alive.Val() == 0 {
			continue // a dead worker is the requeue's, not an alarm
		}
		c.g.Bound = 2 * p99From(c.p99.Val())
		c.g.Age = time.Duration(now-c.claimed) * time.Millisecond
		if c.g.Age > c.g.Bound {
			out = append(out, c.g)
		}
	}
	return out, nil
}

// Run ticks until ctx is done, printing each line; a failed tick prints and the next one retries.
func (s *Sweeper) Run(ctx context.Context) error {
	log := s.Log
	if log == nil {
		log = os.Stdout
	}
	t := time.NewTicker(s.TickInterval())
	defer t.Stop()
	for {
		rep, err := s.Tick(ctx)
		for _, l := range rep.Lines {
			_, _ = fmt.Fprintln(log, l)
		}
		if err != nil && ctx.Err() == nil {
			_, _ = fmt.Fprintf(log, "SWEEP ABORTED %v\n", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
		}
	}
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

func p99From(ms string) time.Duration {
	if n, err := strconv.ParseInt(ms, 10, 64); err == nil && n > 0 {
		return time.Duration(n) * time.Millisecond
	}
	return DefaultStepP99
}

// StepDeadline is a step's deadline on bench for class: twice its class p99 there (§8.6).
func StepDeadline(ctx context.Context, c *redis.Client, bench, class string) (time.Duration, error) {
	ms, err := c.HGet(ctx, BenchLandKey(bench), "p99_ms_"+class).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return 0, err
	}
	return 2 * p99From(ms), nil
}

// RunStepWithDeadline runs step under deadline d; killed reports the deadline ended it.
func RunStepWithDeadline(ctx context.Context, d time.Duration, step func(context.Context) error) (killed bool, err error) {
	sctx, cancel := context.WithTimeout(ctx, d)
	defer cancel()
	err = step(sctx)
	killed = err != nil && ctx.Err() == nil && errors.Is(sctx.Err(), context.DeadlineExceeded)
	return killed, err
}

// StepVerdict: load is ERROR, not RED (§8.6). A step its deadline killed is a bench fault.
func StepVerdict(err error, killed bool) string {
	switch {
	case killed:
		return "ERROR"
	case err != nil:
		return "RED"
	}
	return "GREEN"
}

// GateOutcome is RecordGate's answer.
type GateOutcome struct {
	Bad            bool     // this gate is a bad gate for its bench
	ConsecutiveBad int      // the bench's run of bad gates after this one
	Charged        []string // other benches whose RED this GREEN disputes (a bad gate each)
	Benched        []string // benches this record benched
	Lines          []string // BENCHED <b> bad=<n> last=<batch>
}

// recordGateScript is one atomic transition (§8.3): an ERROR, or a RED another bench gated GREEN
// with the same input_id, is a bad gate (a GREEN arriving after a RED charges the RED bench);
// any other gate resets the run; the third bad gate in a row sets benched and writes BENCHED.
// It runs as EVAL because land.lua is outside B9's PATHS; it moves into the library as
// ns_bench_fault with the worker wiring.
var recordGateScript = redis.NewScript(`
local repo, bench, verdict, input, batch = ARGV[1], ARGV[2], ARGV[3], ARGV[4], ARGV[5]
local t = redis.call('TIME')
local now = tostring(tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000))
local charged = {}
local self_bad = (verdict == 'ERROR')
if input ~= '' then
  local ikey = 'land:' .. repo .. ':inputv:' .. input
  local peers = redis.call('HGETALL', ikey)
  for i = 1, #peers, 2 do
    local pb, pv = peers[i], peers[i + 1]
    if pb ~= bench then
      if verdict == 'RED' and pv == 'GREEN' then self_bad = true end
      if verdict == 'GREEN' and pv == 'RED' then table.insert(charged, pb) end
    end
  end
  redis.call('HSET', ikey, bench, verdict)
end
local benched = {}
local function charge(b)
  local k = 'bench:' .. b .. ':land'
  local n = redis.call('HINCRBY', k, 'consecutive_bad', 1)
  redis.call('HSET', k, 'last_bad', batch, 'last_bad_at', now)
  if n >= tonumber(ARGV[6]) and redis.call('HGET', k, 'benched') ~= '1' then
    redis.call('HSET', k, 'benched', '1', 'benched_at', now, 'benched_reason', 'bad=' .. n .. ' last=' .. batch)
    redis.call('XADD', 'land:' .. repo .. ':events', 'MAXLEN', '~', '100000', '*',
      'event', 'BENCHED', 'repo', repo, 'bench', b, 'bad', tostring(n), 'batch', batch, 'at', now)
    table.insert(benched, b .. ':' .. n)
  end
  return n
end
local consecutive = 0
if self_bad then
  consecutive = charge(bench)
else
  redis.call('HSET', 'bench:' .. bench .. ':land', 'consecutive_bad', '0')
end
for _, b in ipairs(charged) do charge(b) end
return { self_bad and 1 or 0, consecutive, table.concat(charged, ','), table.concat(benched, ',') }
`)

// RecordGate records one gate's verdict (GREEN, RED, ERROR, CONFLICT) for bench (§8.3, L20).
func RecordGate(ctx context.Context, c *redis.Client, repo, bench, verdict, inputID, batch string) (GateOutcome, error) {
	if bench == "" || strings.ContainsAny(bench, " :\t\n") {
		return GateOutcome{}, fmt.Errorf("bench name %q", bench)
	}
	res, err := recordGateScript.Run(ctx, c, nil, repo, bench, verdict, inputID, batch, BadGatesToBench).Slice()
	if err != nil {
		return GateOutcome{}, fmt.Errorf("record gate: %w", err)
	}
	if len(res) != 4 {
		return GateOutcome{}, fmt.Errorf("record gate: reply %v", res)
	}
	bad, _ := res[0].(int64)
	n, _ := res[1].(int64)
	out := GateOutcome{Bad: bad == 1, ConsecutiveBad: int(n)}
	if s := str(res[2]); s != "" {
		out.Charged = strings.Split(s, ",")
	}
	if s := str(res[3]); s != "" {
		for _, bn := range strings.Split(s, ",") {
			b, cnt, _ := strings.Cut(bn, ":")
			out.Benched = append(out.Benched, b)
			out.Lines = append(out.Lines, fmt.Sprintf("BENCHED %s bad=%s last=%s", b, cnt, batch))
		}
	}
	return out, nil
}

// IsBenched reports bench:<b>:land benched.
func IsBenched(ctx context.Context, c *redis.Client, bench string) (bool, error) {
	v, err := c.HGet(ctx, BenchLandKey(bench), "benched").Result()
	if errors.Is(err, redis.Nil) {
		return false, nil
	}
	return v == "1", err
}

// AdmitBench refuses a gate to a benched bench with the named refusal (§8.3).
func AdmitBench(ctx context.Context, c *redis.Client, bench string) error {
	b, err := IsBenched(ctx, c, bench)
	if err != nil {
		return err
	}
	if b {
		return &RefusedError{Fn: "gate_take", Reason: BenchedReason(bench)}
	}
	return nil
}

// BenchOut is `land bench <b> --out <reason>`: benched by hand, with a BENCHED event.
func BenchOut(ctx context.Context, c *redis.Client, repo, bench, reason string) error {
	if bench == "" || strings.ContainsAny(bench, " :\t\n") {
		return fmt.Errorf("bench name %q", bench)
	}
	now, err := c.Time(ctx).Result()
	if err != nil {
		return err
	}
	at := strconv.FormatInt(now.UnixMilli(), 10)
	pipe := c.TxPipeline()
	pipe.HSet(ctx, BenchLandKey(bench), "benched", "1", "benched_at", at, "benched_reason", "out="+reason)
	pipe.XAdd(ctx, &redis.XAddArgs{Stream: EventsStream(repo), MaxLen: 100000, Approx: true,
		Values: []any{"event", "BENCHED", "repo", repo, "bench", bench, "reason", "out=" + reason, "at", at}})
	_, err = pipe.Exec(ctx)
	return err
}

// reinstateScript: only a benched bench, only on a bench-conform PASS no older than ARGV[3] s.
var reinstateScript = redis.NewScript(`
local repo, bench, fresh = ARGV[1], ARGV[2], tonumber(ARGV[3])
local k = 'bench:' .. bench .. ':land'
if redis.call('HGET', k, 'benched') ~= '1' then return 'NOTBENCHED' end
local cf = redis.call('HMGET', 'bench:' .. bench .. ':conform', 'verdict', 'at')
local t = redis.call('TIME')
local now_s = tonumber(t[1])
local at = tonumber(cf[2] or '')
if cf[1] ~= 'PASS' or not at or now_s - at > fresh or at > now_s + 60 then return 'NOCONFORM' end
local now = tostring(now_s * 1000 + math.floor(tonumber(t[2]) / 1000))
redis.call('HSET', k, 'benched', '0', 'consecutive_bad', '0', 'reinstated_at', now, 'reinstated_conform_at', tostring(at))
redis.call('XADD', 'land:' .. repo .. ':events', 'MAXLEN', '~', '100000', '*',
  'event', 'REINSTATED', 'repo', repo, 'bench', bench, 'conform_at', tostring(at), 'at', now)
return 'OK'
`)

// Reinstate is `land bench <b> --reinstate` (§8.3): refused unless the bench is benched and
// bench:<b>:conform holds a PASS younger than 15 min.
func Reinstate(ctx context.Context, c *redis.Client, repo, bench string) error {
	res, err := reinstateScript.Run(ctx, c, nil, repo, bench, int64((15 * time.Minute).Seconds())).Text()
	if err != nil {
		return fmt.Errorf("reinstate: %w", err)
	}
	switch res {
	case "OK":
		return nil
	case "NOTBENCHED":
		return &RefusedError{Fn: "bench_reinstate", Reason: NotBenchedReason(bench)}
	case "NOCONFORM":
		return &RefusedError{Fn: "bench_reinstate", Reason: ReinstateReason(bench)}
	}
	return fmt.Errorf("reinstate: reply %q", res)
}
