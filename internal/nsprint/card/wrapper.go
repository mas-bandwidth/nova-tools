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
//  5. kills the harness's group when the card clock runs out (FAILED timeout)
//     or a beat is fenced (FAILED other);
//  6. copies <job>/out and harness.log into the results directory, writes
//     wrapper.line (exit class, wall, the harness's RESULT line) and hands the
//     end to the ledger, which writes end.record there FIRST and then calls
//     ns_card_end (#2928): a fenced end still leaves the record for the
//     reconciler;
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
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/safepath"
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

// WrapperCard is what the wrapper reads before it writes anything.
type WrapperCard struct {
	State    string
	Bench    string
	Attempt  int
	Identity string // <S>/<label>/<base sha8>/<bench>/<attempt>
}

// WrapperEnd is the exit class the wrapper hands to the ledger.
type WrapperEnd struct {
	Outcome    string // DONE, FAILED (the wrapper never infers ABSTAIN or BLOCKED)
	Reason     string // done, crash, timeout, other
	Exit       int    // the harness exit code; -1 when it was killed
	ResultsDir string
	// PushedSHA is the commit step's commit on the card branch, or "-"
	// (nothing committed); "" is read as "-".
	PushedSHA string
	// Commit is the commit step's note: COMMITTED, NO-COMMIT or OVERSIZE <file>.
	Commit string
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
	Launched(ctx context.Context, branch, jobDir string) (int, error)
	Beat(ctx context.Context) (int, error)
	End(ctx context.Context, end WrapperEnd) (int, error)
}

// WrapperConfig is one wrapper run. Everything but Now, After and Tick is
// required; a missing value is a usage refusal before anything is read.
type WrapperConfig struct {
	Sprint  string
	Label   string
	Attempt int
	Bench   string

	Harness     string // absolute path of the harness program
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
	if !filepath.IsAbs(c.Harness) {
		missing = append(missing, "harness (absolute path)")
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

	// 1. Refuse a card that is not ours before anything is written.
	c, err := ledger.Card(ctx)
	if err != nil {
		return refuse(WrapperExitRedis, "card read: "+err.Error())
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
	code, err := ledger.Launched(ctx, WrapperBranch(cfg.Sprint, cfg.Label, cfg.Attempt), job)
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
	if err := makeJobDir(cfg.JobsRoot, job); err != nil {
		// The card is launched and the harness never ran: a crash, recorded.
		return finish(ctx, cfg, ledger, &rep, job, results, began, now, WrapperEnd{Outcome: "FAILED", Reason: "crash", Exit: -1}, "job dir: "+err.Error(), cleanup)
	}

	// 3. The harness, in its own group, token-free.
	log, err := os.Create(filepath.Join(job, "harness.log"))
	if err != nil {
		cleanup()
		return refuse(WrapperExitCouldNot, "harness log: "+err.Error())
	}
	cmd := exec.Command(cfg.Harness)
	cmd.Dir = job
	cmd.Stdout, cmd.Stderr = log, log
	cmd.Env = harnessEnv(os.Environ(), cfg, job, results)
	harnessGroup(cmd)
	if err := cmd.Start(); err != nil {
		log.Close()
		// The card is launched and the harness never ran: a crash, recorded.
		return finish(ctx, cfg, ledger, &rep, job, results, began, now, WrapperEnd{Outcome: "FAILED", Reason: "crash", Exit: -1}, "harness start: "+err.Error(), cleanup)
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()

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
	var waitErr error
	done := false
	if !beat() {
		killGroup(cmd)
		waitErr, done = <-exited, true
		end.Reason = "other"
	}
	for !done {
		select {
		case waitErr = <-exited:
			done = true
			end = classify(waitErr)
		case <-clock:
			killGroup(cmd)
			waitErr, done = <-exited, true
			end = WrapperEnd{Outcome: "FAILED", Reason: "timeout", Exit: -1}
			why = "card clock " + cfg.Clock.String() + " ran out"
		case <-ticks:
			if !beat() {
				killGroup(cmd)
				waitErr, done = <-exited, true
				end = WrapperEnd{Outcome: "FAILED", Reason: "other", Exit: -1}
			}
		}
	}
	stop()
	_ = waitErr
	log.Close()
	// A fenced beat ends here too: the ledger writes end.record before its
	// end call, which is then fenced, and the record stays for the reconciler.
	return finish(ctx, cfg, ledger, &rep, job, results, began, now, end, why, cleanup)
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
func finish(ctx context.Context, cfg WrapperConfig, ledger WrapperLedger, rep *WrapperReport, job, results string, began time.Time, now func() time.Time, end WrapperEnd, why string, cleanup func()) WrapperReport {
	wall := now().Sub(began)
	end.ResultsDir = results
	rep.Outcome, rep.Reason, rep.Exit, rep.Wall, rep.Why = end.Outcome, end.Reason, end.Exit, wall, why
	end.PushedSHA, end.Commit = NoCommit, "NO-COMMIT"
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
	}
	if err := copyOut(job, results); err != nil {
		// The job dir is kept: its results are the only copy.
		rep.Code, rep.Why = WrapperExitCouldNot, "copy out: "+err.Error()
		return *rep
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

// classify maps the harness's exit to an outcome and a reason from the 3.3
// table. Exit 0 is DONE; any other exit, or a signal the wrapper did not
// send, is FAILED crash. The wrapper does not read the harness's RESULT line
// to decide; it copies it into wrapper.line for the reader.
func classify(err error) WrapperEnd {
	if err == nil {
		return WrapperEnd{Outcome: "DONE", Reason: "done", Exit: 0}
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return WrapperEnd{Outcome: "FAILED", Reason: "crash", Exit: ee.ExitCode()}
	}
	return WrapperEnd{Outcome: "FAILED", Reason: "crash", Exit: -1}
}

// harnessEnv is the wrapper's environment minus anything naming a token,
// plus where the harness runs and writes.
func harnessEnv(base []string, cfg WrapperConfig, job, results string) []string {
	var env []string
	for _, kv := range base {
		k, _, _ := strings.Cut(kv, "=")
		if strings.Contains(strings.ToUpper(k), "TOKEN") || strings.HasPrefix(k, "NOVA_CARD_") {
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
	line := fmt.Sprintf("WRAPPER card=%s outcome=%s reason=%s exit=%d beats=%d wall_ms=%d commit=%s result=%s\n",
		cfg.card(), end.Outcome, end.Reason, end.Exit, beats, wall.Milliseconds(), strconv.Quote(end.Commit), strconv.Quote(resultLine(results)))
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
