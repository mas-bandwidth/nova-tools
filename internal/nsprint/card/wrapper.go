package card

// wrapper.go is the card wrapper (#3059): the process that owns one card
// attempt on a bench, from `card launched` to the deleted job directory.
//
// `nova-sprint card launch --stdin` (#2931) starts it detached as
// `nova-card <S>/<label>/<attempt>` with the token as its first stdin line.
// The wrapper then, in this order and nowhere else:
//
//  1. reads the card and refuses (exit 4, nothing written, no directory made)
//     unless the card is in state dealt, for this bench, at this attempt;
//  2. claims the attempt in Redis (Claim: one wrapper per attempt, #3328),
//     writes `launched` through ns_card_launched, and only then makes the job
//     directory, clearing a leftover one under the jobs root through safepath;
//  3. starts the harness in its own process group, stdin /dev/null, output to
//     <job>/harness.log, with the token nowhere in its argv or environment;
//  4. beats: once at start (the start acknowledgement that moves launched to
//     running) and then every BeatEvery until the harness exits;
//  5. kills the harness's group when the card clock runs out (FAILED timeout),
//     when the card's wall cap runs out (FAILED wall, #3653: EST x 1.5
//     minutes, recorded as wall_max_s on the card hash at launched) or a beat
//     is fenced (FAILED other); a harness that exits non-zero after its
//     program printed a NATIVE REFUSED line ends FAILED refused, that line the
//     end's why on the card record and its w_why fact (#3194), never a bare
//     crash;
//  6. copies <job>/out and harness.log into the results directory, writes
//     the card's record to Redis (#3689, wrapper_result.go: the typed RESULT
//     fields synthesized from facts around the model's two lines, the card's
//     TEST line run, and the wrapper's own facts, in one ns_card_result
//     call), writes wrapper.line (exit class, wall, the harness's RESULT line)
//     and hands the end to the ledger, which writes end.record there FIRST
//     and then calls ns_card_end (#2928): a fenced end still leaves the record
//     for the reconciler;
//  7. deletes the job directory through safepath (results were copied out in
//     step 6; a results directory is never deleted here).
//
// The clock and the beat ticker are injected, so a test drives both as events.

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card/harvestcopy"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/safepath"
	"github.com/mas-bandwidth/nova-tools/internal/typedrec"
)

// Wrapper exit codes. They follow the card verb's codes where one exists.
const (
	WrapperExitEnded    = 0 // the end was recorded, whatever the outcome
	WrapperExitUsage    = 1 // configuration or argv is not a card
	WrapperExitCouldNot = 2 // the wrapper could not run (I/O, a refused end)
	WrapperExitFenced   = 3 // a call presented a token that is not this attempt's
	WrapperExitNotDealt = 4 // the card is not dealt to this bench at this attempt
	WrapperExitRedis    = 6 // Redis unavailable
)

// DefaultBeatEvery is the card beat cadence (#2756 2: 60 s per card).
const DefaultBeatEvery = 60 * time.Second

// DefaultEstMin is the EST a card without one is given when cfg:card carries
// no wall_max_min (#3653).
const DefaultEstMin = 30

// WallFactor is the wall cap's multiple of the card's EST (#3653).
const WallFactor = 1.5

// WallMax is the card's wall cap (#3653): EST x 1.5 minutes, whole seconds
// (at least one). estMin is the card's est field; when it is absent (0) the
// EST is defaultMin (cfg:card wall_max_min), and DefaultEstMin when that is
// absent too.
func WallMax(estMin, defaultMin float64) time.Duration {
	est := estMin
	if est <= 0 {
		est = defaultMin
	}
	if est <= 0 {
		est = DefaultEstMin
	}
	secs := math.Round(est * WallFactor * 60)
	if secs < 1 {
		secs = 1
	}
	return time.Duration(secs) * time.Second
}

// WrapperCard is what the wrapper reads before it writes anything.
type WrapperCard struct {
	State    string
	Bench    string
	Attempt  int
	Identity string // <S>/<label>/<base sha8>/<bench>/<attempt>
	// EstMin is the card's est field (its EST: line in minutes, stored by
	// card push); 0 is absent. WallMaxMin is cfg:card wall_max_min, the EST
	// of a card that carries none; 0 is absent. See WallMax.
	EstMin     float64
	WallMaxMin float64
	// Kind is the card hash's kind (a RESULT kind, or a runner kind; "" is a
	// model card). The end step reads it to tell a code card, which must
	// commit, from one that legitimately commits nothing (NoCommitKinds).
	Kind string
}

// WrapperEnd is the exit class the wrapper hands to the ledger.
type WrapperEnd struct {
	Outcome    string // DONE, FAILED; ABSTAIN or BLOCKED only from the model's line 2 (ModelEnd)
	Reason     string // done, crash, timeout, wall, refused, other
	Exit       int    // the harness exit code; -1 when it was killed
	ResultsDir string
	// PushedSHA is the commit step's commit on the card branch, or "-"
	// (nothing committed); "" is read as "-".
	PushedSHA string
	// Commit is the commit step's note: COMMITTED, NO-COMMIT or OVERSIZE <file>.
	Commit string
	// WallMax is the attempt's wall cap (#3653); wrapper.line names it.
	WallMax time.Duration
	// Why is the end's one-line evidence, written to the card's why by card
	// end: for FAILED refused, the refusal line (#3194). Empty is no evidence.
	Why string
	// RepoDir is the checkout the commit step committed PushedSHA in
	// (<job>/out/repo), set on a DONE end; a copy's ledger pushes from it
	// (#4227) before the job dir is deleted.
	RepoDir string
}

// WrapperLedger is the Redis side of one attempt. Every method returns the
// verb's exit code (0 applied, 3 fenced, 6 Redis down, ...) and an error only
// for a call that could not be made. RedisLedger is the production one.
type WrapperLedger interface {
	Card(ctx context.Context) (WrapperCard, error)
	// Claim is the attempt claim (#3328): the one atomic step that lets
	// exactly one wrapper invocation own a dealt attempt. nonce is this
	// invocation's; a retry of the same call with the same nonce is 0 again.
	Claim(ctx context.Context, nonce string) (int, error)
	// Launched records the attempt's wall cap as wall_max_s (#3653).
	Launched(ctx context.Context, branch, jobDir string, wallMax time.Duration) (int, error)
	Beat(ctx context.Context) (int, error)
	End(ctx context.Context, end WrapperEnd) (int, error)
}

// ResultRecorder is the ledger side of the #2506 producer: it records one
// parsed RESULT envelope (ns_card_result writes the write-once result hash).
// It is a separate interface so a ledger that records no result (a test
// double) still satisfies WrapperLedger; RedisLedger implements both.
//
// facts (#3689) are the wrapper's own w_<name> fields, written in the same
// ns_card_result call; a Fact w_result=absent is a card that wrote no
// RESULT.md, recorded with valid "" (no parse) and the facts alone.
type ResultRecorder interface {
	Result(ctx context.Context, res typedrec.Result, resultsDir string, facts ...Fact) (int, error)
}

// ResultFacts is the trusted card side of a RESULT parse: the card's KIND
// and its contract line (line 1), read from the card, never from the file
// being judged. RedisLedger implements it from the card hash.
type ResultFacts interface {
	ResultFacts(ctx context.Context) (typedrec.ParseOptions, error)
}

var (
	_ ResultRecorder = (*RedisLedger)(nil)
	_ ResultFacts    = (*RedisLedger)(nil)
)

// ResultFacts reads the card hash's kind and contract fields. kind is a
// typed-record expectation only when it is one of typedrec.Kinds: the hash
// also carries runner kinds (model, script) that say nothing about RESULT.
// contract, when the card carries it, is line 1 verbatim.
func (l *RedisLedger) ResultFacts(ctx context.Context) (typedrec.ParseOptions, error) {
	if l.Store == nil || l.Store.Client() == nil {
		return typedrec.ParseOptions{}, errors.New("no store")
	}
	vals, err := l.Store.Client().HMGet(ctx, CardKey(l.Sprint, l.Label), "kind", "contract").Result()
	if err != nil {
		return typedrec.ParseOptions{}, err
	}
	kind, _ := vals[0].(string)
	contract, _ := vals[1].(string)
	var opt typedrec.ParseOptions
	if typedrec.IsKind(kind) {
		opt.ExpectedKind = kind
	}
	opt.ContractLine = contract
	return opt, nil
}

// RecordResult is what the wrapper does with a card's RESULT.md at end: read
// <resultsDir>/RESULT.md, parse it with typedrec.ParseResult (the one parser)
// against the card's trusted facts (attempt, and KIND and line 1 when rec
// knows them), and hand the parse to rec. A card that wrote no RESULT.md
// (fs.ErrNotExist) records nothing and returns found=false, err=nil; any other
// read error, or a failure reading the card's facts, is returned as err with
// nothing recorded. The returned code is ns_card_result's reply code.
func RecordResult(ctx context.Context, rec ResultRecorder, resultsDir string, attempt int) (res typedrec.Result, found bool, code int, err error) {
	data, rerr := os.ReadFile(filepath.Join(resultsDir, "RESULT.md"))
	if errors.Is(rerr, fs.ErrNotExist) {
		return typedrec.Result{}, false, 0, nil
	}
	if rerr != nil {
		return typedrec.Result{}, false, 0, fmt.Errorf("read RESULT.md: %w", rerr)
	}
	var opt typedrec.ParseOptions
	if f, ok := rec.(ResultFacts); ok {
		if opt, err = f.ResultFacts(ctx); err != nil {
			return typedrec.Result{}, true, WrapperExitRedis, fmt.Errorf("card facts: %w", err)
		}
	}
	opt.ExpectedAttempt = attempt
	res = typedrec.ParseResult(data, opt)
	code, err = rec.Result(ctx, res, resultsDir)
	return res, true, code, err
}

// Result calls ns_card_result (#2506) to record the parsed RESULT envelope:
// schema, kind, valid, field, defect, line, the raw bytes and their sha256,
// and one c_<field> claim per typed field. The hash is write-once per attempt.
func (l *RedisLedger) Result(ctx context.Context, res typedrec.Result, resultsDir string, facts ...Fact) (int, error) {
	const verb = "card result"
	if l.Store == nil || l.Store.Client() == nil || !validSprintLabel(l.Sprint, l.Label) || l.Token == "" {
		return usage(verb, l.Label).Code, nil
	}
	c, err := l.Card(ctx)
	if err != nil {
		return WrapperExitRedis, err
	}
	attemptStr := strconv.Itoa(c.Attempt)
	if c.Attempt < 1 && res.Attempt > 0 {
		attemptStr = strconv.Itoa(res.Attempt)
	}
	validStr := "0"
	if res.Valid {
		validStr = "1"
	}
	for _, f := range facts {
		if f.Name == "w_result" && f.Value == "absent" {
			validStr = "" // no RESULT.md: no parse, the wrapper's facts only
		}
	}
	args := []any{
		l.Sprint, l.Label, attemptStr, l.Token,
		res.Schema, res.Kind, validStr, res.Field, res.Defect, strconv.Itoa(res.Line),
		res.RawSHA256, string(res.RawBytes), resultsDir,
		// line1 is the file's line 1 as read; ns_card_result checks it against
		// the card's contract field in Redis, independent of the parse.
		"line1", res.Line1,
	}
	claimKeys := make([]string, 0, len(res.Claims))
	for k := range res.Claims {
		claimKeys = append(claimKeys, k)
	}
	sort.Strings(claimKeys)
	for _, k := range claimKeys {
		args = append(args, "c_"+strings.ToLower(k), res.Claims[k])
	}
	for _, f := range facts {
		if strings.HasPrefix(f.Name, "w_") {
			args = append(args, f.Name, f.Value)
		}
	}
	reply, err := fcall(ctx, l.Store, "ns_card_result", cardKeys(l.Sprint, l.Label), args...)
	if err != nil {
		if r, down := redisDown(verb, l.Label, err); down {
			return r.Code, nil
		}
		return 0, err
	}
	return reply.Code, nil
}

// WrapperConfig is one wrapper run. Everything but Now, After and Tick is
// required; a missing value is a usage refusal before anything is read.
type WrapperConfig struct {
	Sprint  string
	Label   string
	Attempt int
	Bench   string

	// Harness is the absolute path of the harness program, exec'd in the job
	// dir; or, with no program, InProcess is the Go harness (run.go, #3681)
	// called in this process with Store, the wrapper's own connection.
	// Exactly one of the two.
	Harness     string
	InProcess   *RunConfig
	Store       *store.Store
	JobsRoot    string // job dirs live at <JobsRoot>/<S>/<label>/<attempt>
	ResultsRoot string // results live at <ResultsRoot>/<identity>
	Clock       time.Duration
	BeatEvery   time.Duration // zero means DefaultBeatEvery
	// LaunchDeadline is the launcher's absolute batch deadline. A wrapper
	// reached at or after it must not claim or launch the attempt.
	LaunchDeadline time.Time
	// Started is called after Redis accepts the launched transition and
	// before the job directory or harness is created.
	Started func()

	Now   func() time.Time
	After func(time.Duration) <-chan time.Time
	Tick  func(time.Duration) (<-chan time.Time, func())
	// WallAfter is the wall cap's timer (#3653), separate from After so a
	// test driving the card clock never fires the wall; nil is time.After.
	WallAfter func(time.Duration) <-chan time.Time
	// CheckTimeout bounds the wrapper's run of the card's TEST line at end
	// (#3689); zero is DefaultCheckTimeout.
	CheckTimeout time.Duration
	// Copy is a consumer copy's id (#3998): the ledger is a CopyLedger and
	// the in-process harness renders the card from task:<copy>.
	Copy string
}

// WrapperReport is what one run did; the command prints Line.
type WrapperReport struct {
	Code    int
	Card    string
	Outcome string
	Reason  string
	Exit    int
	Beats   int
	Wall    time.Duration
	Why     string
}

// Line is the wrapper's one output line. It never carries the token.
func (r WrapperReport) Line() string {
	if r.Outcome == "" {
		return fmt.Sprintf("REFUSED nova-card %s code=%d why=%s", r.Card, r.Code, strconv.Quote(r.Why))
	}
	return fmt.Sprintf("ENDED nova-card %s outcome=%s reason=%s exit=%d beats=%d wall_ms=%d code=%d",
		r.Card, r.Outcome, r.Reason, r.Exit, r.Beats, r.Wall.Milliseconds(), r.Code)
}

// WrapperBranch is the attempt's branch (#2756 1.7): nova/<S>/<label>-a<attempt>.
func WrapperBranch(sprint, label string, attempt int) string {
	return fmt.Sprintf("nova/%s/%s-a%d", sprint, label, attempt)
}

// WrapperJobDir is where the harness runs. It is deleted at card end.
func WrapperJobDir(root, sprint, label string, attempt int) string {
	return filepath.Join(root, sprint, label, strconv.Itoa(attempt))
}

func (c WrapperConfig) card() string { return fmt.Sprintf("%s/%s/%d", c.Sprint, c.Label, c.Attempt) }

func (c WrapperConfig) check() error {
	var missing []string
	if !validSprintLabel(c.Sprint, c.Label) || c.Attempt < 1 {
		missing = append(missing, "card <S>/<label>/<attempt>")
	}
	if !benchRE.MatchString(c.Bench) {
		missing = append(missing, "bench")
	}
	switch {
	case c.InProcess != nil && c.Harness != "":
		missing = append(missing, "one harness: a program or in-process, not both")
	case c.InProcess != nil && (c.Store == nil || c.Store.Client() == nil):
		missing = append(missing, "store (the in-process harness reads the card as the bench)")
	case c.InProcess == nil && !filepath.IsAbs(c.Harness):
		missing = append(missing, "harness (absolute path, or in-process)")
	}
	if !filepath.IsAbs(c.JobsRoot) {
		missing = append(missing, "jobs root (absolute path)")
	}
	if !filepath.IsAbs(c.ResultsRoot) {
		missing = append(missing, "results root (absolute path)")
	}
	if c.Clock <= 0 {
		missing = append(missing, "clock")
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing %s", strings.Join(missing, ", "))
	}
	return nil
}

// RunWrapper owns one card attempt end to end. See the file comment for the
// order of its effects.
func RunWrapper(ctx context.Context, cfg WrapperConfig, ledger WrapperLedger) WrapperReport {
	rep := WrapperReport{Card: cfg.card(), Exit: -1}
	refuse := func(code int, why string) WrapperReport {
		rep.Code, rep.Why = code, why
		return rep
	}
	if err := cfg.check(); err != nil {
		return refuse(WrapperExitUsage, err.Error())
	}
	now, after, tick := cfg.Now, cfg.After, cfg.Tick
	if now == nil {
		now = time.Now
	}
	if after == nil {
		after = time.After
	}
	if tick == nil {
		tick = func(d time.Duration) (<-chan time.Time, func()) {
			t := time.NewTicker(d)
			return t.C, t.Stop
		}
	}
	every := cfg.BeatEvery
	if every <= 0 {
		every = DefaultBeatEvery
	}
	expired := func() bool {
		return !cfg.LaunchDeadline.IsZero() && !now().Before(cfg.LaunchDeadline)
	}
	if expired() {
		return refuse(WrapperExitCouldNot, "launch deadline exceeded")
	}

	// 1. Refuse a card that is not ours before anything is written. This read
	// is the first command on the store (Open sends none, #3277), so an
	// unreachable Redis is refused here and named.
	c, err := ledger.Card(ctx)
	if err != nil {
		return refuse(WrapperExitRedis, "redis: card read: "+err.Error())
	}
	switch {
	case c.State == "":
		return refuse(WrapperExitNotDealt, "no such card")
	case c.State != "dealt":
		return refuse(WrapperExitNotDealt, "card is "+c.State+", not dealt")
	case c.Bench != cfg.Bench:
		return refuse(WrapperExitNotDealt, "card is dealt to "+c.Bench+", not "+cfg.Bench)
	case c.Attempt != cfg.Attempt:
		return refuse(WrapperExitNotDealt, fmt.Sprintf("card is at attempt %d, not %d", c.Attempt, cfg.Attempt))
	}
	id, err := ParseIdentity(c.Identity)
	if err != nil || id.Sprint != cfg.Sprint || id.Label != cfg.Label || id.Bench != cfg.Bench || id.Attempt != cfg.Attempt {
		return refuse(WrapperExitNotDealt, "card identity "+strconv.Quote(c.Identity)+" is not this attempt")
	}
	if expired() {
		return refuse(WrapperExitCouldNot, "launch deadline exceeded")
	}

	// 2. The claim and launched in Redis, then the job directory. Redis is the
	// only claim (#3328): with two wrappers racing for the same dealt attempt,
	// exactly one Claim returns 0; the other exits with the Redis code and
	// never touches the disk. The job dir path is deterministic, so launched
	// records it before it exists. cleanup is defined only once this
	// invocation has won the claim, so a loser never removes the winner's
	// live harness output.
	job := WrapperJobDir(cfg.JobsRoot, cfg.Sprint, cfg.Label, cfg.Attempt)
	results := filepath.Join(cfg.ResultsRoot, filepath.FromSlash(id.String()))
	nonce, err := claimNonce()
	if err != nil {
		return refuse(WrapperExitCouldNot, "claim nonce: "+err.Error())
	}
	if code, err := ledger.Claim(ctx, nonce); err != nil || code != 0 {
		return refuse(ledgerCode(code, err), fmt.Sprintf("card claim refused code=%d%s", code, errSuffix(err)))
	}
	if expired() {
		return refuse(WrapperExitCouldNot, "launch deadline exceeded")
	}
	wallMax := WallMax(c.EstMin, c.WallMaxMin)
	code, err := ledger.Launched(ctx, WrapperBranch(cfg.Sprint, cfg.Label, cfg.Attempt), job, wallMax)
	if err != nil || code != 0 {
		return refuse(ledgerCode(code, err), fmt.Sprintf("card launched refused code=%d%s", code, errSuffix(err)))
	}
	if cfg.Started != nil {
		cfg.Started()
	}
	cleanup := func() {
		if err := safepath.RemoveUnder(cfg.JobsRoot, job); err != nil {
			if rep.Why == "" {
				rep.Why = "job dir left: " + err.Error()
			}
			return
		}
		// <S>/<label> go too once empty; os.Remove never removes a non-empty dir.
		for dir := filepath.Dir(job); dir != filepath.Clean(cfg.JobsRoot) && strings.HasPrefix(dir, filepath.Clean(cfg.JobsRoot)+string(filepath.Separator)); dir = filepath.Dir(dir) {
			if os.Remove(dir) != nil {
				break
			}
		}
	}
	began := now()
	wallAfter := cfg.WallAfter
	if wallAfter == nil {
		wallAfter = time.After
	}
	if err := makeJobDir(cfg.JobsRoot, job); err != nil {
		// The card is launched and the harness never ran: a crash, recorded.
		return finish(ctx, cfg, ledger, &rep, c.Kind, job, results, began, now, WrapperEnd{Outcome: "FAILED", Reason: "crash", Exit: -1, WallMax: wallMax}, "job dir: "+err.Error(), cleanup)
	}

	// 3. The harness, in its own group, token-free: the program cfg.Harness
	// names, or the Go harness in-process (#3681), whose runner is the group.
	log, err := os.Create(filepath.Join(job, "harness.log"))
	if err != nil {
		cleanup()
		return refuse(WrapperExitCouldNot, "harness log: "+err.Error())
	}
	var proc harnessProc
	if cfg.InProcess != nil {
		rc := *cfg.InProcess
		rc.Sprint, rc.Label, rc.Attempt, rc.Bench = cfg.Sprint, cfg.Label, cfg.Attempt, cfg.Bench
		rc.CopyID = cfg.Copy
		rc.JobDir, rc.OutDir = job, filepath.Join(job, "out")
		base := rc.Env
		if base == nil {
			base = os.Environ()
		}
		rc.Env = harnessEnv(base, cfg, job, results)
		proc = &inprocHarness{ctx: ctx, st: cfg.Store, cfg: rc, log: log}
	} else {
		cmd := exec.Command(cfg.Harness)
		cmd.Dir = job
		cmd.Stdout, cmd.Stderr = log, log
		cmd.Env = harnessEnv(os.Environ(), cfg, job, results)
		harnessGroup(cmd)
		proc = &execHarness{cmd: cmd}
	}
	if err := proc.start(); err != nil {
		log.Close()
		// The card is launched and the harness never ran: a crash, recorded.
		return finish(ctx, cfg, ledger, &rep, c.Kind, job, results, began, now, WrapperEnd{Outcome: "FAILED", Reason: "crash", Exit: -1, WallMax: wallMax}, "harness start: "+err.Error(), cleanup)
	}
	exited := proc.exited()
	// The wall cap runs from the harness start, whatever the beats say.
	wall := wallAfter(wallMax)

	// 4 and 5. Beat until the harness exits, the clock runs out, or a beat is fenced.
	end := WrapperEnd{Outcome: "FAILED", Reason: "crash", Exit: -1}
	why := ""
	beat := func() bool {
		code, err := ledger.Beat(ctx)
		if err == nil && code == 0 {
			rep.Beats++
			return true
		}
		why = fmt.Sprintf("card beat refused code=%d%s", code, errSuffix(err))
		return false
	}
	clock := after(cfg.Clock)
	ticks, stop := tick(every)
	var exit harnessExit
	done := false
	if !beat() {
		proc.kill()
		exit, done = <-exited, true
		end.Reason = "other"
	}
	for !done {
		select {
		case exit = <-exited:
			done = true
			end = classify(exit)
		case <-clock:
			proc.kill()
			exit, done = <-exited, true
			end = WrapperEnd{Outcome: "FAILED", Reason: "timeout", Exit: -1}
			why = "card clock " + cfg.Clock.String() + " ran out"
		case <-wall:
			// The harness's whole group goes; out/ is copied for the read.
			proc.kill()
			exit, done = <-exited, true
			end = WrapperEnd{Outcome: "FAILED", Reason: "wall", Exit: -1}
			why = fmt.Sprintf("card wall %s ran out (EST x %.1f, wall_max_s=%d)", wallMax, WallFactor, int64(wallMax/time.Second))
		case <-ticks:
			if !beat() {
				proc.kill()
				exit, done = <-exited, true
				end = WrapperEnd{Outcome: "FAILED", Reason: "other", Exit: -1}
			}
		}
	}
	stop()
	_ = exit
	log.Close()
	end.WallMax = wallMax
	// A harness that ended non-zero on its own after its program refused the
	// card (#3194) is FAILED refused, with the refusal line as the evidence:
	// a card the bench could not start (no pool identity.tsv, #3193) must
	// read as refused on the card record, never as a crash nobody can read.
	if end.Outcome == "FAILED" && end.Reason == "crash" && end.Exit > 0 {
		if line, ok := NativeRefusal(job); ok {
			end.Reason, end.Why, why = "refused", line, line
		}
	}
	// A fenced beat ends here too: the ledger writes end.record before its
	// end call, which is then fenced, and the record stays for the reconciler.
	return finish(ctx, cfg, ledger, &rep, c.Kind, job, results, began, now, end, why, cleanup)
}

// makeJobDir makes <job>/out for a launched attempt. A directory already at
// the job path is a leftover of an earlier run (the claim, not the disk, says
// who owns the attempt), so it is removed under the jobs root through
// safepath first and never copied into this attempt's results.
func makeJobDir(root, job string) error {
	if _, err := os.Lstat(job); err == nil {
		if err := safepath.RemoveUnder(root, job); err != nil {
			return fmt.Errorf("leftover %s: %w", job, err)
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return os.MkdirAll(filepath.Join(job, "out"), 0o700)
}

// claimNonce names one wrapper invocation in its claim.
func claimNonce() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// Claim is the attempt claim (#3328) through ns_card_claim (card_run.lua,
// #3551): a registered Redis Function, never EVAL/EVALSHA, since the bench's
// Redis user may FCALL only. It is fenced on the attempt's token and on state
// dealt, and writes claim (<token_sha>:<nonce>, never the token) and claim_at
// (Redis TIME, ms): one writer (the attempt's wrapper), no TTL beyond the
// attempt's lease. Codes: 0 claimed (or this nonce's own retry), 2 not dealt,
// 3 fenced, 4 another wrapper holds the attempt, 5 no card.
func (l *RedisLedger) Claim(ctx context.Context, nonce string) (int, error) {
	const verb = "card claim"
	if l.Store == nil || l.Store.Client() == nil || !validSprintLabel(l.Sprint, l.Label) || l.Token == "" || nonce == "" {
		return usage(verb, l.Label).Code, nil
	}
	reply, err := fcall(ctx, l.Store, "ns_card_claim", cardKeys(l.Sprint, l.Label), l.Sprint, l.Label, l.Token, nonce)
	if err != nil {
		if res, down := redisDown(verb, l.Label, err); down {
			return res.Code, nil
		}
		return 0, err
	}
	return reply.Code, nil
}

// finish is step 6 and 7: copy out, record, end, delete.
func finish(ctx context.Context, cfg WrapperConfig, ledger WrapperLedger, rep *WrapperReport, kind, job, results string, began time.Time, now func() time.Time, end WrapperEnd, why string, cleanup func()) WrapperReport {
	wall := now().Sub(began)
	end.ResultsDir = results
	rep.Outcome, rep.Reason, rep.Exit, rep.Wall, rep.Why = end.Outcome, end.Reason, end.Exit, wall, why
	end.PushedSHA, end.Commit = NoCommit, "NO-COMMIT"
	// The model's line 2 (#3919): ABSTAIN or BLOCKED is the end, not DONE,
	// and there is nothing to commit.
	if me, ok := ModelEnd(kind, filepath.Join(job, "out"), end); ok {
		end = me
		rep.Outcome, rep.Reason = end.Outcome, end.Reason
		if rep.Why == "" {
			rep.Why = end.Why
		}
	}
	if end.Outcome == "DONE" {
		// The commit step (#2932): <job>/out/repo onto the card branch, before copy out.
		msg := resultLine(filepath.Join(job, "out"))
		c, err := CommitOutput(filepath.Join(job, "out", "repo"), WrapperBranch(cfg.Sprint, cfg.Label, cfg.Attempt), msg, cfg.Bench)
		if err != nil {
			c = CommitResult{SHA: NoCommit, Note: "NO-COMMIT"}
			if rep.Why == "" {
				rep.Why = "commit step: " + err.Error()
			}
		}
		end.PushedSHA, end.Commit = c.SHA, c.Note
		// The checkout the commit lives in: a copy's end pushes from it
		// (#4227). The end runs before cleanup, so it is still there.
		end.RepoDir = filepath.Join(job, "out", "repo")
		// A code card whose model said DONE and committed nothing ends
		// done/fail reason no-commit in this same end, never a false ok that
		// harvest (pushed_sha "-") would skip forever.
		if nc, ok := NoCommitEnd(kind, filepath.Join(job, "out"), end); ok {
			end = nc
			rep.Outcome, rep.Reason = end.Outcome, end.Reason
			if rep.Why == "" {
				rep.Why = end.Why
			} else {
				rep.Why = end.Why + "; " + rep.Why
			}
		}
	}
	if err := copyOut(job, results); err != nil {
		// The job dir is kept: its results are the only copy.
		rep.Code, rep.Why = WrapperExitCouldNot, "copy out: "+err.Error()
		return *rep
	}
	// The RESULT record comes before the end: a DONE whose RESULT.md could
	// not be read (other than absent) or whose record was refused ends FAILED
	// other, so ns_harvest_due (ended DONE only) never offers an attempt that
	// has no validated result hash behind it.
	if rec, ok := ledger.(ResultRecorder); ok {
		// #3689: the wrapper writes the record (typed fields synthesized, its
		// facts beside them) and Redis holds it; the beat keeps the lease live
		// while the card's TEST line runs.
		er := endRecord{cfg: cfg, job: job, results: results, end: end, wall: wall, why: rep.Why}
		every := cfg.BeatEvery
		if every <= 0 {
			every = DefaultBeatEvery
		}
		var stop func()
		if cfg.Tick != nil {
			er.ticks, stop = cfg.Tick(every)
		} else {
			t := time.NewTicker(every)
			er.ticks, stop = t.C, t.Stop
		}
		er.beat = func() {
			if code, err := ledger.Beat(ctx); err == nil && code == 0 {
				rep.Beats++
			}
		}
		res, synth, rcode, rerr := recordEnd(ctx, rec, er)
		stop()
		if synth && rerr == nil && rcode == 0 && res.Defect == typedrec.DefectContradictory && wrapperField(res.Field) {
			// The wrapper wrote this field from the card: a disagreement is a
			// wrapper bug, logged; the card stays DONE.
			bug := "wrapper bug: " + res.Field + " contradictory"
			if rep.Why == "" {
				rep.Why = bug
			} else {
				rep.Why += "; " + bug
			}
		}
		if rerr != nil || rcode != 0 {
			rwhy := fmt.Sprintf("result not recorded code=%d%s", rcode, errSuffix(rerr))
			if end.Outcome == "DONE" {
				end.Outcome, end.Reason = "FAILED", "other"
				rep.Outcome, rep.Reason = end.Outcome, end.Reason
			}
			if rep.Why == "" {
				rep.Why = rwhy
			} else {
				rep.Why += "; " + rwhy
			}
		}
	}
	writeWrapperLine(results, cfg, end, rep.Beats, wall)
	code, err := ledger.End(ctx, end)
	cleanup()
	if err != nil || code != 0 {
		rep.Code = ledgerCode(code, err)
		rep.Why = fmt.Sprintf("card end refused code=%d%s", code, errSuffix(err))
		return *rep
	}
	rep.Code = WrapperExitEnded
	return *rep
}

// NativeRefusedMark is what `nova-swarm native` prints when it refuses a card
// before the card runs: `NATIVE REFUSED: <reason>` on its stderr, exit 2.
const NativeRefusedMark = "NATIVE REFUSED"

// HarnessRefusedMark is what the harness itself writes when it refuses the
// card before native runs: `REFUSED <why> for <card>` in its own log (run.go
// step 1 and 2 refusals, and the wrapper's receipt line `REFUSED card run
// ... code=2`), exit 2. It is read only from the harness's own logs, never
// from native's output, where the word may be the model's (#4234).
const HarnessRefusedMark = "REFUSED "

// refusalSources are the files, relative to the job dir, that NativeRefusal
// reads, in order: the harness's own output, then what the bench harness
// keeps of native's stderr and stdout and its own log under the out dir.
// own marks the harness's own logs, where HarnessRefusedMark counts too.
var refusalSources = []struct {
	rel string
	own bool
}{{"harness.log", true}, {"out/native.err", false}, {"out/native.out", false}, {"out/harness.log", true}}

// refusalTail is how much of each source's end NativeRefusal reads.
const refusalTail = 64 << 10

// NativeRefusal finds the refusal line a harness's program printed: the first
// line in refusalSources carrying NativeRefusedMark, from the mark on (a
// leading log stamp is dropped). ok is false when no source holds one.
func NativeRefusal(job string) (line string, ok bool) {
	for _, src := range refusalSources {
		if line, ok := refusalIn(filepath.Join(job, filepath.FromSlash(src.rel)), src.own); ok {
			return line, true
		}
	}
	return "", false
}

// refusalLine is the refusal a log line carries, from its mark on: the native
// mark anywhere, or, in the harness's own log, HarnessRefusedMark at the
// start of the line or right after one log stamp.
func refusalLine(text string, own bool) (string, bool) {
	if i := strings.Index(text, NativeRefusedMark); i >= 0 {
		return strings.TrimSpace(text[i:]), true
	}
	if !own {
		return "", false
	}
	i := strings.Index(text, HarnessRefusedMark)
	if i < 0 {
		return "", false
	}
	if lead := strings.TrimSpace(text[:i]); lead != "" && strings.ContainsAny(lead, " \t") {
		return "", false
	}
	return strings.TrimSpace(text[i:]), true
}

func refusalIn(path string, own bool) (string, bool) {
	f, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return "", false
	}
	if size := info.Size(); size > refusalTail {
		if _, err := f.Seek(size-refusalTail, io.SeekStart); err != nil {
			return "", false
		}
	}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 4096), refusalTail+1)
	for sc.Scan() {
		if line, ok := refusalLine(sc.Text(), own); ok {
			return line, true
		}
	}
	return "", false
}

// classify maps the harness's exit to an outcome and a reason from the 3.3
// table. Exit 0 is DONE; any other exit, or a signal the wrapper did not
// send, is FAILED crash (RunWrapper then reads a refusal line, if any, to
// make a non-zero exit FAILED refused). The wrapper does not read the harness's RESULT line
// to decide; it copies it into wrapper.line for the reader.
func classify(exit harnessExit) WrapperEnd {
	if exit.err == nil && exit.code == 0 {
		return WrapperEnd{Outcome: "DONE", Reason: "done", Exit: 0}
	}
	return WrapperEnd{Outcome: "FAILED", Reason: "crash", Exit: exit.code}
}

// harnessProc is one attempt's harness as the wrapper drives it: started
// once, waited on through exited, and killed with its whole group.
type harnessProc interface {
	start() error
	exited() <-chan harnessExit
	kill()
}

// harnessExit is how a harness ended: its exit code (-1 killed or never
// ran) and the wait error, if any.
type harnessExit struct {
	code int
	err  error
}

// execHarness is the program cfg.Harness names, in its own process group.
type execHarness struct {
	cmd  *exec.Cmd
	done chan harnessExit
}

func (h *execHarness) start() error {
	if err := h.cmd.Start(); err != nil {
		return err
	}
	h.done = make(chan harnessExit, 1)
	go func() {
		err := h.cmd.Wait()
		h.done <- harnessExit{code: exitCode(err), err: err}
	}()
	return nil
}

func (h *execHarness) exited() <-chan harnessExit { return h.done }
func (h *execHarness) kill()                      { killGroup(h.cmd) }

// inprocHarness is the Go harness (Run, #3681) in this process. Its runner
// is the process group the wrapper kills; a kill before the runner has
// started stops it the moment it does.
type inprocHarness struct {
	ctx  context.Context
	st   *store.Store
	cfg  RunConfig
	log  *os.File // the job's harness.log: the run's receipt line goes there
	done chan harnessExit

	mu     sync.Mutex
	cmd    *exec.Cmd
	killed bool
}

func (h *inprocHarness) start() error {
	h.cfg.Proc = func(c *exec.Cmd) {
		h.mu.Lock()
		defer h.mu.Unlock()
		h.cmd = c
		if h.killed {
			killGroup(c)
		}
	}
	h.done = make(chan harnessExit, 1)
	go func() {
		rep := Run(h.ctx, h.st, h.cfg)
		fmt.Fprintln(h.log, rep.Line())
		h.done <- harnessExit{code: rep.Code}
	}()
	return nil
}

func (h *inprocHarness) exited() <-chan harnessExit { return h.done }

func (h *inprocHarness) kill() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.killed = true
	if h.cmd != nil {
		killGroup(h.cmd)
	}
}

// harnessEnv is the wrapper's environment minus anything naming a token,
// plus where the harness runs and writes. The bench's push credential
// (harvestcopy.TokenEnv, #4227) is named here on its own: it is the
// wrapper's for its end and never the harness's, whatever the general rules
// say.
func harnessEnv(base []string, cfg WrapperConfig, job, results string) []string {
	var env []string
	for _, kv := range base {
		k, _, _ := strings.Cut(kv, "=")
		if k == harvestcopy.TokenEnv || k == harvestcopy.AskpassEnv ||
			strings.Contains(strings.ToUpper(k), "TOKEN") || strings.HasPrefix(k, "NOVA_CARD_") {
			continue
		}
		env = append(env, kv)
	}
	return append(env,
		"NOVA_CARD="+cfg.card(),
		"NOVA_CARD_BRANCH="+WrapperBranch(cfg.Sprint, cfg.Label, cfg.Attempt),
		"NOVA_CARD_JOB="+job,
		"NOVA_CARD_OUT="+filepath.Join(job, "out"),
	)
}

// copyOut copies <job>/out/** and harness.log into results. Symlinks and
// other non-regular files are not followed or copied.
func copyOut(job, results string) error {
	if err := os.MkdirAll(results, 0o755); err != nil {
		return err
	}
	if err := copyFile(filepath.Join(job, "harness.log"), filepath.Join(results, "harness.log")); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	out := filepath.Join(job, "out")
	return filepath.WalkDir(out, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		rel, err := filepath.Rel(out, p)
		if err != nil || rel == "." {
			return err
		}
		dst := filepath.Join(results, rel)
		if d.IsDir() {
			return os.MkdirAll(dst, 0o755)
		}
		if !d.Type().IsRegular() || rel == EndRecordName {
			return nil // a harness never writes the end record
		}
		return copyFile(p, dst)
	})
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// writeWrapperLine records what end.record has no field for: the wall and the
// harness's RESULT line (line 1 of out/RESULT.md, if it wrote one).
func writeWrapperLine(results string, cfg WrapperConfig, end WrapperEnd, beats int, wall time.Duration) {
	line := fmt.Sprintf("WRAPPER card=%s outcome=%s reason=%s exit=%d beats=%d wall_ms=%d wall_max_s=%d commit=%s result=%s why=%s\n",
		cfg.card(), end.Outcome, end.Reason, end.Exit, beats, wall.Milliseconds(), int64(end.WallMax/time.Second), strconv.Quote(end.Commit), strconv.Quote(resultLine(results)), strconv.Quote(end.Why))
	_ = os.WriteFile(filepath.Join(results, "wrapper.line"), []byte(line), 0o644)
}

func resultLine(results string) string {
	f, err := os.Open(filepath.Join(results, "RESULT.md"))
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	if sc.Scan() {
		return sc.Text()
	}
	return ""
}

func ledgerCode(code int, err error) int {
	if err != nil && code == 0 {
		return WrapperExitCouldNot
	}
	return code
}

func errSuffix(err error) string {
	if err == nil {
		return ""
	}
	return " err=" + strconv.Quote(err.Error())
}
