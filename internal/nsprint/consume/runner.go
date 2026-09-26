package consume

// runner.go is the pr-to-read rule of `nova-sprint route` (#2756 10.7,
// 10.8.3, control 33; nova-tools #3040 rev 4). It reads ev:github in its own
// consumer group pr-to-read:<S> (the stream is fleet-wide, so each sprint's
// router needs its own group to see every event) and does two things:
//
//   - runner rows: a check_run whose name is a policy runner_rows row lands
//     as field runner:<row> of ci:<repo>:<head>:<gid>:runners (the gid the
//     lander expects for the PR's base, never the write-once receipt),
//     keeping the attempt with the highest key (gen, check_run_id, status
//     rank, at) whatever its conclusion (ns_prtoread_runner, prtoread.lua);
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
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ci"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/land"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/redis/go-redis/v9"
)

// Function names registered by internal/nsprint/fn/lua/prtoread.lua.
const (
	FunctionPRToReadRunner = "ns_prtoread_runner"
	FunctionPRToReadAdopt  = "ns_prtoread_adopt"
)

// RunnerAttempt is the stored value of ci:<repo>:<head>:<gid>:runners field runner:<row>.
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

// ErrCutSkipped is what a CICut returns when the cut cannot be made from
// Redis alone yet (no base tip sha recorded, a head that is not a full sha, or
// ns_ci_cut answered other than CREATED/EXISTS). The adoption WAITs (#3040
// adoption step 1: the cut, then the Function): it prints one WAIT line,
// writes no adopt record and is retried on the next pass, instead of stopping
// the router or adopting with cut=0.
var ErrCutSkipped = errors.New("ci cut skipped")

// ErrNoBaseTip is the ErrCutSkipped of a branch base whose
// land:<repo>:<base>:tip has no sha yet; the adoption prints
// `WAIT <id> no base tip`.
var ErrNoBaseTip = fmt.Errorf("%w: no base tip", ErrCutSkipped)

// StoreCICut is the CICut `nova-sprint route` wires (#3040 rev 4, adoption
// "gets its ci cut"): one ci.Cut per adopted head, against the base tip sha.
// A base that is already a full sha is used as is, with BaseRef naming its
// branch; a branch name is the BaseRef and resolves through the lander's
// land:<repo>:<base>:tip record (land.TipKey), so the rule still makes no
// REST call. ns_ci_cut writes only the card (#3139 rev 7 3.7), so the runner
// rows at ci:<repo>:<head>:<gid>:runners are never touched by a cut.
func StoreCICut(st *store.Store, actor string) func(context.Context, CICut) error {
	return func(ctx context.Context, c CICut) error {
		if !fullSHA(c.Head) {
			return fmt.Errorf("%w: head %q is not a full sha", ErrCutSkipped, c.Head)
		}
		base, baseRef := c.Base, c.BaseRef
		if !fullSHA(base) {
			if baseRef == "" {
				baseRef = base
			}
			tipKey := land.TipKey(c.Repo, baseRef)
			tip, err := st.Client().HGet(ctx, tipKey, "sha").Result()
			if err != nil && !errors.Is(err, redis.Nil) {
				return fmt.Errorf("ci cut %s: %w", tipKey, err)
			}
			if !fullSHA(tip) {
				return fmt.Errorf("%w: no tip sha in %s", ErrNoBaseTip, tipKey)
			}
			base = tip
		}
		if baseRef == "" {
			return fmt.Errorf("%w: no base branch for %s@%s", ErrCutSkipped, c.Repo, head8(c.Head))
		}
		r, err := ci.Cut(ctx, st, ci.CutRequest{Sprint: c.Sprint, Repo: c.Repo, PR: c.PR,
			Head: c.Head, Base: base, BaseRef: baseRef, Actor: actor})
		if err != nil {
			return err
		}
		if r.ExitCode() != ci.ExitOK {
			return fmt.Errorf("%w: ns_ci_cut %s", ErrCutSkipped, r)
		}
		return nil
	}
}

// RouteOkFriend is the ok-to-friend rule exactly as `nova-sprint route`
// wires it (nova-tools #3496): its CICut is StoreCICut, the one hook the
// pr-to-read adoption also uses, so a harvested card's head gets its ci card
// before its reads. A cut that cannot be made yet keeps the event pending.
func RouteOkFriend(st *store.Store, sprint, consumer, actor string) *OkFriend {
	return &OkFriend{Store: st, Sprint: sprint, Consumer: consumer, Actor: actor, CICut: StoreCICut(st, actor)}
}

func fullSHA(s string) bool {
	if len(s) != 40 {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// PRToReadRule is the pr-to-read Handler of `nova-sprint route` (#3040).
type PRToReadRule struct {
	Store    *store.Store
	Sprint   string
	Consumer string    // this router instance's consumer name
	Actor    string    // receipt actor
	Out      io.Writer // one line per action; nil discards
	// CICut is called once per adoption before its review tasks; the cut is
	// idempotent per head and base. An ErrCutSkipped answer WAITs the
	// adoption; nil skips the cut (cut=0 on the line, controls only: route
	// wires StoreCICut).
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
	repo, head, number, row, arg string
	base, gid                    string
	cmd                          *redis.Cmd
}

// runnerAttemptArg is the attempt_json ns_prtoread_runner compares.
type runnerAttemptArg struct {
	CheckRunID string `json:"check_run_id"`
	Action     string `json:"action"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	At         string `json:"at"`
}

// batch is one delivered batch in at most four more round trips: one
// pipeline reading each candidate PR's base from its sprint record
// s:<S>:pr:<repo>:<n>, one reading each base's tip and policy, one pipeline of
// every resolved candidate's ns_prtoread_runner in stream order (the first
// three are skipped when there is no candidate), then one XACK of every id in
// the batch. The gid is civerdict.ExpectedFrom over that base, tip and
// policy, so a row lands at ci:<repo>:<head>:<gid>:runners for the identity
// the lander will expect. Nothing is filled with a default: a check with no
// sprint PR record prints NOBASE, a base with no policy or no tip prints
// NOPOLICY, and neither writes. A failed pipeline acks nothing, so the batch
// is retried from pending on the next pass; the function is a no-op on a
// redelivered entry.
func (p *PRToReadRule) batch(ctx context.Context, msgs []redis.XMessage, policy string, out *strings.Builder) (int, error) {
	if len(msgs) == 0 {
		return 0, nil
	}
	client := p.Store.Client()
	rows := map[string]map[string]bool{}
	var cands []*runnerCandidate
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
		cands = append(cands, &runnerCandidate{repo: repo, head: v("head"), number: v("number"), row: row, arg: string(arg)})
	}
	written, err := p.runnerRows(ctx, cands, out)
	if err != nil {
		return 0, err
	}
	if err := client.XAck(ctx, ghevent.Stream, p.Group(), ids...).Err(); err != nil {
		return written, fmt.Errorf("pr-to-read: ack: %w", err)
	}
	return written, nil
}

// runnerRows resolves each candidate's base and gid and writes the rows it
// can; see batch for the round trips and the refusals.
func (p *PRToReadRule) runnerRows(ctx context.Context, cands []*runnerCandidate, out *strings.Builder) (int, error) {
	if len(cands) == 0 {
		return 0, nil
	}
	client := p.Store.Client()
	baseCmds := map[string]*redis.StringCmd{}
	pipe := client.Pipeline()
	for _, c := range cands {
		if c.number == "" {
			continue
		}
		k := "s:" + p.Sprint + ":pr:" + c.repo + ":" + c.number
		if _, ok := baseCmds[k]; !ok {
			baseCmds[k] = pipe.HGet(ctx, k, "base")
		}
	}
	if len(baseCmds) > 0 {
		if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
			return 0, fmt.Errorf("pr-to-read: runner bases: %w", err)
		}
	}
	type baseRead struct {
		tip *redis.StringCmd
		pol *redis.SliceCmd
	}
	byBase := map[string]*baseRead{}
	pipe = client.Pipeline()
	for _, c := range cands {
		if c.number != "" {
			if cmd := baseCmds["s:"+p.Sprint+":pr:"+c.repo+":"+c.number]; cmd != nil {
				c.base = strings.TrimSpace(cmd.Val())
			}
		}
		if c.base == "" {
			continue
		}
		k := c.repo + "\x00" + c.base
		if _, ok := byBase[k]; !ok {
			byBase[k] = &baseRead{
				tip: pipe.HGet(ctx, civerdict.TipKey(c.repo, c.base), "sha"),
				pol: pipe.HMGet(ctx, civerdict.PolicyKey(c.repo, c.base), "policy_id", "required_set_id", "runner_id"),
			}
		}
	}
	if len(byBase) > 0 {
		if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
			return 0, fmt.Errorf("pr-to-read: runner policy: %w", err)
		}
	}
	var live []*runnerCandidate
	pipe = client.Pipeline()
	for _, c := range cands {
		if c.base == "" {
			fmt.Fprintf(out, "NOBASE %s@%s %s no sprint PR record for #%s\n", c.repo, head8(c.head), c.row, c.number)
			continue
		}
		bc := byBase[c.repo+"\x00"+c.base]
		pol := bc.pol.Val()
		field := func(i int) string {
			if i < len(pol) {
				s, _ := pol[i].(string)
				return s
			}
			return ""
		}
		gid, err := civerdict.ExpectedFrom(c.base, strings.TrimSpace(bc.tip.Val()), field(0), field(1), field(2))
		switch {
		case errors.Is(err, civerdict.ErrNoPolicy):
			fmt.Fprintf(out, "NOPOLICY %s@%s %s %s\n", c.repo, head8(c.head), c.row, civerdict.PolicyKey(c.repo, c.base))
			continue
		case errors.Is(err, civerdict.ErrNoTip):
			fmt.Fprintf(out, "NOPOLICY %s@%s %s no tip in %s\n", c.repo, head8(c.head), c.row, civerdict.TipKey(c.repo, c.base))
			continue
		case err != nil:
			return 0, fmt.Errorf("pr-to-read: runner %s@%s: %w", c.repo, head8(c.head), err)
		}
		c.gid = gid
		c.cmd = pipe.FCall(ctx, FunctionPRToReadRunner, []string{civerdict.RunnersKey(c.repo, c.head, gid)}, c.row, c.arg)
		live = append(live, c)
	}
	if len(live) == 0 {
		return 0, nil
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return 0, fmt.Errorf("pr-to-read: runner rows: %w", err)
	}
	for _, c := range live {
		reply, err := c.cmd.StringSlice()
		if err != nil || len(reply) < 5 {
			return 0, fmt.Errorf("pr-to-read: runner %s@%s %s: %v %v", c.repo, head8(c.head), c.row, reply, err)
		}
		fmt.Fprintf(out, "RUNNER %s@%s %s g%s/%s %s/%s %s\n", c.repo, head8(c.head), c.row,
			reply[1], reply[2], reply[3], reply[4], reply[0])
	}
	return len(live), nil
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
