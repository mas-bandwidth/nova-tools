package consume

// runner.go is the pr-to-read rule of `nova-sprint route` (#2756 10.7,
// 10.8.3, control 33; nova-tools #3040 rev 4). It reads ev:github in its own
// consumer group pr-to-read:<S> (the stream is fleet-wide, so each sprint's
// router needs its own group to see every event) and does two things:
//
//   - runner rows: a check_run whose name is a policy runner_rows row lands
//     as field runner:<row> of the GID key ci:<repo>:<head>:<gid>, keeping the
//     attempt with the highest key (gen, check_run_id, status rank, at)
//     whatever its conclusion (ns_prtoread_runner, prtoread.lua);
//   - adoption: a sprint PR no card produced gets its ci cut and one review
//     task per required reader at its head, once per head (adopt.go).
//
// RunnerReady is the one readiness rule over those rows. The rule makes no
// REST calls.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/civerdict"
	"github.com/mas-bandwidth/nova-tools/internal/ghevent"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/redis/go-redis/v9"
)

// Function names registered by internal/nsprint/fn/lua/prtoread.lua.
const (
	FunctionPRToReadRunner = "ns_prtoread_runner"
	FunctionPRToReadAdopt  = "ns_prtoread_adopt"
)

// Runner row states, as RunnerRow reads the stored latest attempt.
const (
	RunnerStateReady   = "READY"   // completed/success
	RunnerStateFail    = "FAIL"    // completed/failure or completed/timed_out
	RunnerStateMissing = "MISSING" // anything else, an absent field included
)

// RunnerAttempt is the stored value of ci:<repo>:<head>:<gid> field runner:<row>.
type RunnerAttempt struct {
	Gen        int    `json:"gen"`
	CheckRunID string `json:"check_run_id"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	RereqID    string `json:"rereq_id"`
	RereqAt    string `json:"rereq_at"`
	Source     string `json:"source"`
	At         string `json:"at"`
}

// ReadRunnerAttempt decodes field runner:<row> of a ci record; false when the
// field is absent or not an attempt.
func ReadRunnerAttempt(ci map[string]string, row string) (RunnerAttempt, bool) {
	raw, ok := ci["runner:"+row]
	if !ok || raw == "" {
		return RunnerAttempt{}, false
	}
	var a RunnerAttempt
	if err := json.Unmarshal([]byte(raw), &a); err != nil || a.CheckRunID == "" {
		return RunnerAttempt{}, false
	}
	return a, true
}

// RunnerRow is one row's state from the stored latest attempt only: an older
// success never counts once a newer attempt is stored.
func RunnerRow(ci map[string]string, row string) string {
	a, ok := ReadRunnerAttempt(ci, row)
	if !ok || a.Status != "completed" {
		return RunnerStateMissing
	}
	switch a.Conclusion {
	case "success":
		return RunnerStateReady
	case "failure", "timed_out":
		return RunnerStateFail
	}
	return RunnerStateMissing
}

// RunnerReady is the one readiness rule the lander and `land why` call over a
// head's ci:<repo>:<head>:<gid> record: ok only when every named row is READY;
// missing names, in order, every row that is not (MISSING or FAIL; RunnerRow
// says which).
func RunnerReady(ci map[string]string, rows []string) (bool, []string) {
	var missing []string
	for _, row := range rows {
		if RunnerRow(ci, row) != RunnerStateReady {
			missing = append(missing, row)
		}
	}
	return len(missing) == 0, missing
}

// RunnerRows parses policy runner_rows for one short repo name: each entry is
// <job> (every repo) or <repo>:<job> (that repo only), separated by spaces or
// commas. The result keeps the policy's order with no repeats.
func RunnerRows(policy, repo string) []string {
	var rows []string
	seen := map[string]bool{}
	for _, f := range strings.FieldsFunc(policy, func(r rune) bool { return r == ' ' || r == ',' }) {
		job := f
		if i := strings.Index(f, ":"); i >= 0 {
			if f[:i] != repo {
				continue
			}
			job = f[i+1:]
		}
		if job != "" && !seen[job] {
			seen[job] = true
			rows = append(rows, job)
		}
	}
	return rows
}

// shortRepo is the repo name sprint records and ci keys use: ev:github names
// owner/repo.
func shortRepo(repo string) string {
	if i := strings.LastIndex(repo, "/"); i >= 0 {
		return repo[i+1:]
	}
	return repo
}

// PRToReadRule is the pr-to-read Handler of `nova-sprint route` (#3040).
type PRToReadRule struct {
	Store    *store.Store
	Sprint   string
	Consumer string    // this router instance's consumer name
	Actor    string    // receipt actor
	Out      io.Writer // one line per action; nil discards
	// CICut is called once per adoption before its review tasks; the cut is
	// idempotent per head and base. nil skips the cut (cut=0 on the line).
	CICut func(context.Context, CICut) error
	Count int64 // entries per read; 0 means 100
	// Block is the block of the first new-entry read; 0 means 1 s and a
	// negative Block never blocks.
	Block time.Duration
}

// Group is the rule's consumer group on ev:github, one per sprint.
func (p *PRToReadRule) Group() string { return RulePRToRead + ":" + p.Sprint }

func (p *PRToReadRule) check() error {
	if p == nil || p.Store == nil || p.Sprint == "" || p.Consumer == "" {
		return errors.New("pr-to-read: store, sprint and consumer are required")
	}
	return nil
}

// Start creates the group (from the stream's start, so nothing published
// before the first router is lost) and claims a previous instance's pending
// entries.
func (p *PRToReadRule) Start(ctx context.Context) error {
	if err := p.check(); err != nil {
		return err
	}
	client := p.Store.Client()
	err := client.XGroupCreateMkStream(ctx, ghevent.Stream, p.Group(), "0").Err()
	if err != nil && !strings.HasPrefix(err.Error(), "BUSYGROUP") {
		return fmt.Errorf("pr-to-read: group: %w", err)
	}
	start := "0-0"
	for {
		_, next, err := client.XAutoClaim(ctx, &redis.XAutoClaimArgs{
			Stream: ghevent.Stream, Group: p.Group(), Consumer: p.Consumer,
			MinIdle: 0, Start: start, Count: 1000,
		}).Result()
		if err != nil {
			return fmt.Errorf("pr-to-read: reclaim pending: %w", err)
		}
		if next == "0-0" || next == "" {
			return nil
		}
		start = next
	}
}

// Pass drains ev:github (the pending entries once, then new ones until a
// read returns nothing), adopts, writes proc:pr-to-read:<S> and prints
// PASS took_ms=. It returns the number of runner entries written or kept
// plus adoptions.
func (p *PRToReadRule) Pass(ctx context.Context) (int, error) {
	if err := p.check(); err != nil {
		return 0, err
	}
	began := time.Now()
	var out strings.Builder
	n, err := p.pass(ctx, &out)
	took := time.Since(began).Milliseconds()
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	if perr := p.Store.Client().FCall(ctx, FunctionOkFriendPass, nil, RulePRToRead+":"+p.Sprint,
		strconv.FormatInt(took, 10), strconv.Itoa(n), msg).Err(); perr != nil && err == nil {
		err = fmt.Errorf("pr-to-read: proc: %w", perr)
	}
	fmt.Fprintf(&out, "PASS took_ms=%d\n", took)
	if p.Out != nil {
		_, _ = io.WriteString(p.Out, out.String())
	}
	return n, err
}

func (p *PRToReadRule) pass(ctx context.Context, out *strings.Builder) (int, error) {
	policy, err := p.Store.Client().HGetAll(ctx, "s:"+p.Sprint+":policy").Result()
	if err != nil {
		return 0, fmt.Errorf("pr-to-read: policy: %w", err)
	}
	n, err := p.drain(ctx, policy["runner_rows"], out)
	if err != nil {
		return n, err
	}
	a, err := p.adopt(ctx, out)
	return n + a, err
}

func (p *PRToReadRule) read(ctx context.Context, id string, count int64, wait time.Duration) ([]redis.XMessage, error) {
	streams, err := p.Store.Client().XReadGroup(ctx, &redis.XReadGroupArgs{
		Group: p.Group(), Consumer: p.Consumer, Streams: []string{ghevent.Stream, id},
		Count: count, Block: wait,
	}).Result()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("pr-to-read: read %s: %w", id, err)
	}
	var msgs []redis.XMessage
	for _, s := range streams {
		msgs = append(msgs, s.Messages...)
	}
	return msgs, nil
}

// drain handles this instance's pending entries once, then new entries until
// a read returns nothing; only the first new read blocks, and only when
// nothing was pending.
func (p *PRToReadRule) drain(ctx context.Context, policy string, out *strings.Builder) (int, error) {
	count := p.Count
	if count <= 0 {
		count = 100
	}
	block := p.Block
	if block == 0 {
		block = time.Second
	}
	pending, err := p.read(ctx, "0", count, -1)
	if err != nil {
		return 0, err
	}
	handled, err := p.batch(ctx, pending, policy, out)
	if err != nil {
		return handled, err
	}
	wait := block
	if len(pending) > 0 {
		wait = -1
	}
	for {
		msgs, err := p.read(ctx, ">", count, wait)
		if err != nil {
			return handled, err
		}
		if len(msgs) == 0 {
			return handled, nil
		}
		n, err := p.batch(ctx, msgs, policy, out)
		handled += n
		if err != nil {
			return handled, err
		}
		wait = -1
	}
}

type runnerCandidate struct {
	repo, head, row string
	cmd             *redis.Cmd
}

// runnerAttemptArg is the attempt_json ns_prtoread_runner compares.
type runnerAttemptArg struct {
	CheckRunID string `json:"check_run_id"`
	Action     string `json:"action"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	At         string `json:"at"`
}

// batch is one delivered batch in exactly two more round trips: one pipeline
// of every candidate's ns_prtoread_runner in stream order (skipped when there
// is none), then one XACK of every id in the batch. A failed pipeline acks
// nothing, so the batch is retried from pending on the next pass; the
// function is a no-op on a redelivered entry.
func (p *PRToReadRule) batch(ctx context.Context, msgs []redis.XMessage, policy string, out *strings.Builder) (int, error) {
	if len(msgs) == 0 {
		return 0, nil
	}
	client := p.Store.Client()
	rows := map[string]map[string]bool{}
	var cands []*runnerCandidate
	pipe := client.Pipeline()
	ids := make([]string, 0, len(msgs))
	for _, m := range msgs {
		ids = append(ids, m.ID)
		v := func(k string) string { s, _ := m.Values[k].(string); return s }
		if v("kind") != "check_run" || v("check") == "" || v("head") == "" || v("check_run_id") == "" {
			continue
		}
		repo := shortRepo(v("repo"))
		named, ok := rows[repo]
		if !ok {
			named = map[string]bool{}
			for _, r := range RunnerRows(policy, repo) {
				named[r] = true
			}
			rows[repo] = named
		}
		row := v("check")
		if !named[row] {
			continue
		}
		arg, err := json.Marshal(runnerAttemptArg{CheckRunID: v("check_run_id"), Action: v("action"),
			Status: v("status"), Conclusion: v("conclusion"), At: v("at")})
		if err != nil {
			return 0, fmt.Errorf("pr-to-read: attempt %s: %w", m.ID, err)
		}
		c := &runnerCandidate{repo: repo, head: v("head"), row: row}
		ciKey := civerdict.Key(repo, c.head, "expected")
		c.cmd = pipe.FCall(ctx, FunctionPRToReadRunner, []string{ciKey}, row, string(arg))
		cands = append(cands, c)
	}
	if len(cands) > 0 {
		if _, err := pipe.Exec(ctx); err != nil {
			return 0, fmt.Errorf("pr-to-read: runner rows: %w", err)
		}
		for _, c := range cands {
			reply, err := c.cmd.StringSlice()
			if err != nil || len(reply) < 5 {
				return 0, fmt.Errorf("pr-to-read: runner %s@%s %s: %v %v", c.repo, head8(c.head), c.row, reply, err)
			}
			fmt.Fprintf(out, "RUNNER %s@%s %s g%s/%s %s/%s %s\n", c.repo, head8(c.head), c.row,
				reply[1], reply[2], reply[3], reply[4], reply[0])
		}
	}
	if err := client.XAck(ctx, ghevent.Stream, p.Group(), ids...).Err(); err != nil {
		return len(cands), fmt.Errorf("pr-to-read: ack: %w", err)
	}
	return len(cands), nil
}

func head8(h string) string {
	if len(h) > 8 {
		return h[:8]
	}
	return h
}

// JoinPRToRead fills the router's one pr-to-read slot with both of its
// halves: PRRead (prread.go, #2941: heads, reads, land-ready) and
// PRToReadRule (this file, #3040: runner rows, adoption). Each half keeps its
// own source, group and proc key, so each value keeps one writer. Start
// starts both in order; Pass runs both concurrently and returns the sum, and
// the first error that is not retryable, else a retryable one.
func JoinPRToRead(halves ...Handler) PRToRead {
	var hs []Handler
	for _, h := range halves {
		if h != nil {
			hs = append(hs, h)
		}
	}
	return prToReadJoin(hs)
}

type prToReadJoin []Handler

func (j prToReadJoin) Start(ctx context.Context) error {
	for _, h := range j {
		if err := h.Start(ctx); err != nil {
			return err
		}
	}
	return nil
}

func (j prToReadJoin) Pass(ctx context.Context) (int, error) {
	ns := make([]int, len(j))
	errs := make([]error, len(j))
	var wg sync.WaitGroup
	for i, h := range j {
		wg.Add(1)
		go func(i int, h Handler) {
			defer wg.Done()
			ns[i], errs[i] = h.Pass(ctx)
		}(i, h)
	}
	wg.Wait()
	total := 0
	var soft error
	for i := range j {
		total += ns[i]
		switch {
		case errs[i] == nil:
		case retryable(errs[i]):
			if soft == nil {
				soft = errs[i]
			}
		default:
			return total, errs[i]
		}
	}
	return total, soft
}
