package card

// wrapper_result.go is the record the wrapper writes at card end
// (nova-tools#3689). Glenn 2026-09-24 11:25 PM: "make sure we rely on the
// model as little as possible. Just let it do work." and 11:55 PM: "it should
// be in REDIS, not on files."
//
// The model writes RESULT.md as two lines -- the card's line 1 verbatim and
// `DONE` | `ABSTAIN <why>` | `BLOCKED <why>` -- and an optional note. Before
// RecordResult the wrapper synthesizes the typed fields (typedrec.Synthesize):
// KIND and REPO from the card hash, ATTEMPT from the run, BRANCH =
// WrapperBranch, PATHS from `git diff --name-only <base_sha>..HEAD` in
// out/repo, CHECK, GREEN and the Gates rows from its own bounded run of the
// card's TEST line (`<package> <TestName>`, stored by card push as the card
// hash's test field; absent is CHECK: not-run), RED not-run, and Left owed from
// the model's note. A model-written typed field is overwritten, never
// compared, so BRANCH `rowan/<label>` against `nova/<S>/<label>-a<n>` is no
// longer a contradiction.
//
// The whole result then goes to Redis in the one ns_card_result call: the
// parsed record (c_<field> claims, raw bytes, valid) plus the wrapper's own
// facts as w_<name> fields (the model's two lines and note, outcome, reason,
// exit, wall, commit, the check run, and the provider facts from the harness's
// START line). RESULT.md (the model's copy is kept as RESULT.model.md),
// wrapper.line, harness.log and end.record stay on the bench as logs;
// `nova-sprint card show` prints the record.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/ci/slowtests"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/cardhdr"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/typedrec"
	"github.com/redis/go-redis/v9"
)

// DefaultCheckTimeout bounds the wrapper's run of the card's TEST line.
const DefaultCheckTimeout = 5 * time.Minute

// ModelResultName is where the model's own RESULT.md is kept once the wrapper
// has written the synthesized record over RESULT.md.
const ModelResultName = "RESULT.model.md"

// CardFacts are the card-hash fields the wrapper synthesizes the record from.
type CardFacts struct {
	Kind     string
	Repo     string
	BaseSHA  string
	Test     string
	Contract string
}

// CardFactsReader reads CardFacts; RedisLedger implements it with one HMGET.
type CardFactsReader interface {
	CardFacts(ctx context.Context) (CardFacts, error)
}

// Fact is one w_<name> field of the record the wrapper writes: it goes to
// ns_card_result with the parse (ResultRecorder.Result).
type Fact struct{ Name, Value string }

var _ CardFactsReader = (*RedisLedger)(nil)

// CardFacts reads kind, repo, base_sha, test and contract from the card hash.
func (l *RedisLedger) CardFacts(ctx context.Context) (CardFacts, error) {
	if l.Store == nil || l.Store.Client() == nil {
		return CardFacts{}, errors.New("no store")
	}
	vals, err := l.Store.Client().HMGet(ctx, CardKey(l.Sprint, l.Label), "kind", "repo", "base_sha", "test", "contract").Result()
	if err != nil {
		return CardFacts{}, err
	}
	s := func(i int) string { v, _ := vals[i].(string); return v }
	return CardFacts{Kind: s(0), Repo: s(1), BaseSHA: s(2), Test: s(3), Contract: s(4)}, nil
}

// CheckRun is the wrapper's run of the card's TEST line.
type CheckRun struct {
	Check string // pass, fail, not-run
	Cmd   string // the command run, or the TEST value
	Gate  string // the Gates row
	Green string // on pass: the command and its output tail
	Tail  string
	Wall  time.Duration
	// Red is a fail that proves the test red: go test's fail event for the
	// named test, its package failing to build or run with the test in it,
	// or the run killed at the cap. A test that did not run (no pass and no
	// fail event: [no test files], [no tests to run], a package go test
	// cannot find) is a fail at head and never a red at base.
	Red bool
}

// TestCommand is the card's TEST line as argv: `[-tags <tags>] <package>
// <TestName>` is `go test [-tags <tags>] <package> -run ^<TestName>$
// -count=1`. Anything else (absent, `none <why>`, a free command) has no argv
// and why says so: the wrapper runs only the declared grammar
// (cardhdr.ParseTest) and never assumes a package from PATHS.
func TestCommand(test string) (argv []string, why string) {
	tl, refused := cardhdr.ParseTest(test)
	switch {
	case strings.TrimSpace(test) == "":
		return nil, "no TEST line on the card"
	case tl.None:
		return nil, "TEST: none (" + tl.Why + ")"
	case refused != "":
		return nil, "TEST is not `[-tags <tags>] <package> <TestName>`"
	}
	argv = []string{"go", "test"}
	if tl.Tags != "" {
		argv = append(argv, "-tags", tl.Tags)
	}
	return append(argv, tl.GoPackage(), "-run", "^"+tl.Name+"$", "-count=1"), ""
}

// NoTestsMark is what go test prints for a -run that selected nothing: a
// TEST line naming no test in its package passes vacuously, which the
// wrapper reads as a fail, never as a green.
const NoTestsMark = "[no tests to run]"

// SetupFailedMark is go test's frame for a package it could not even set
// up (the directory is not there): no test ran, so nothing is red.
const SetupFailedMark = "[setup failed]"

// NoTestFilesMark is what go test prints for a package with no test files:
// as vacuous as NoTestsMark.
const NoTestFilesMark = "[no test files]"

// Cmd is one child process the wrapper runs at end: in Dir, the argv, its
// output (stdout and stderr together) to Out, under Timeout, beating on
// every tick while it runs.
type Cmd struct {
	Dir     string
	Argv    []string
	Out     io.Writer
	Timeout time.Duration
	Ticks   <-chan time.Time
	Beat    func()
}

// Runner runs one Cmd and returns its exit status; -1 with an error is a
// command that could not start, and -1 with context.DeadlineExceeded one
// killed at Timeout. It is the end step's one seam: the tests pass a fake
// that answers from a table, so no unit test starts go or git.
type Runner func(ctx context.Context, c Cmd) (int, error)

// execRun is the production Runner: the child in its own group (killed
// whole at the deadline), the wrapper's environment minus any token.
func execRun(ctx context.Context, c Cmd) (int, error) {
	if len(c.Argv) == 0 {
		return -1, errors.New("empty command")
	}
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = DefaultCheckTimeout
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, c.Argv[0], c.Argv[1:]...)
	cmd.Dir = c.Dir
	cmd.Env = checkEnv(os.Environ())
	cmd.Stdout, cmd.Stderr = c.Out, c.Out
	harnessGroup(cmd)
	cmd.Cancel = func() error { killGroup(cmd); return nil }
	cmd.WaitDelay = 10 * time.Second
	done := make(chan error, 1)
	if err := cmd.Start(); err != nil {
		return -1, err
	}
	go func() { done <- cmd.Wait() }()
	var err error
wait:
	for {
		select {
		case err = <-done:
			break wait
		case <-c.Ticks:
			if c.Beat != nil {
				c.Beat()
			}
		}
	}
	if cctx.Err() != nil {
		return -1, context.DeadlineExceeded
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return exit.ExitCode(), nil
	}
	if err != nil {
		return -1, err
	}
	return 0, nil
}

// RunCheck runs the card's TEST line in repo under timeout, calling beat every
// tick while it runs (the card's lease stays live past the harness).
func RunCheck(ctx context.Context, repo, test string, timeout time.Duration, ticks <-chan time.Time, beat func()) CheckRun {
	return runCheck(ctx, nil, repo, test, timeout, ticks, beat)
}

// runCheck is RunCheck through run (nil is execRun). The test runs under
// go test -json, and green is the pass event of the named test itself
// (nova-tools#4313 fix round: exit 0 alone is green for [no test files] and
// [no tests to run], a vacuous pass). The row keeps the plain command.
func runCheck(ctx context.Context, run Runner, repo, test string, timeout time.Duration, ticks <-chan time.Time, beat func()) CheckRun {
	if run == nil {
		run = execRun
	}
	argv, why := TestCommand(test)
	if argv == nil {
		return CheckRun{Check: "not-run", Cmd: test, Gate: "TEST: not-run (" + why + ")"}
	}
	cmdline := strings.Join(argv, " ")
	if _, err := os.Stat(repo); err != nil {
		return CheckRun{Check: "not-run", Cmd: cmdline, Gate: cmdline + ": not-run (no out/repo)"}
	}
	if timeout <= 0 {
		timeout = DefaultCheckTimeout
	}
	tl, _ := cardhdr.ParseTest(test)
	var out lineBuffer
	began := time.Now()
	exit, err := run(ctx, Cmd{Dir: repo, Argv: append(argv, "-json"), Out: &out, Timeout: timeout, Ticks: ticks, Beat: beat})
	ev := readTestEvents(out.all(), tl.Name)
	r := CheckRun{Cmd: cmdline, Wall: time.Since(began), Tail: ev.tail(3)}
	secs := fmt.Sprintf("%.2fs", r.Wall.Seconds())
	switch {
	case err != nil && !errors.Is(err, context.DeadlineExceeded):
		return CheckRun{Check: "not-run", Cmd: cmdline, Gate: cmdline + ": not-run (" + oneField(err.Error()) + ")"}
	case err != nil:
		r.Check, r.Red = "fail", true
		r.Gate = cmdline + ": fail timeout after " + timeout.String()
	case ev.passed && !ev.failed && exit == 0:
		r.Check = "pass"
		r.Gate = cmdline + ": pass " + secs
		r.Green = cmdline + ": pass " + secs
		if r.Tail != "" {
			r.Green += "; " + r.Tail
		}
	case ev.failed || ev.pkgFailed:
		r.Check, r.Red = "fail", true
		r.Gate = cmdline + ": fail " + secs
		if r.Tail != "" {
			r.Gate += ": " + r.Tail
		}
	default:
		// no pass and no fail of the named test: it did not run
		r.Check = "fail"
		what := "go test ran no " + tl.Name + " in " + tl.GoPackage()
		switch {
		case ev.noFiles:
			what += ": " + NoTestFilesMark
		case ev.noTests:
			what += ": " + NoTestsMark
		case ev.setup:
			what += ": " + SetupFailedMark + " " + r.Tail
		case r.Tail != "":
			what += ": " + r.Tail
		}
		if exit != 0 {
			what += fmt.Sprintf(" (exit %d)", exit)
		}
		r.Gate = cmdline + ": fail (" + what + ")"
	}
	return r
}

// testEvents is what a go test -json stream says of one named test.
type testEvents struct {
	// passed and failed are the named test's own pass and fail events
	// (a subtest's are not); pkgFailed is its package failing outside any
	// test (a build error, a panic, TestMain, the -timeout) while the
	// package has test files and is there to set up.
	passed, failed, pkgFailed bool
	noFiles, noTests, setup   bool
	output                    []string
}

func (e testEvents) tail(n int) string {
	rows := e.output
	if len(rows) > n {
		rows = rows[len(rows)-n:]
	}
	return oneField(strings.Join(rows, " | "))
}

// readTestEvents reads go test -json lines for the test named name. A line
// that is not an event (go's own error before any event) is output.
func readTestEvents(lines []string, name string) testEvents {
	var e testEvents
	for _, l := range lines {
		t := strings.TrimSpace(l)
		var ev slowtests.Event
		if t == "" || t[0] != '{' || json.Unmarshal([]byte(t), &ev) != nil {
			if t != "" {
				e.output = append(e.output, t)
			}
			continue
		}
		switch ev.Action {
		case "output", "build-output":
			o := strings.TrimSpace(ev.Output)
			if o == "" {
				continue
			}
			if strings.Contains(o, NoTestFilesMark) {
				e.noFiles = true
			}
			if strings.Contains(o, NoTestsMark) {
				e.noTests = true
			}
			if strings.Contains(o, SetupFailedMark) {
				e.setup = true
			}
			if !strings.HasPrefix(o, "=== ") {
				e.output = append(e.output, o)
			}
		case "pass":
			if ev.Test == name {
				e.passed = true
			}
		case "fail", "build-fail":
			switch {
			case ev.Test == name:
				e.failed = true
			case ev.Test == "":
				e.pkgFailed = true
			}
		}
	}
	if e.noFiles || e.setup {
		// no test to run (no test files), or no package to run it in
		// (go test cannot find the directory): nothing is proved red
		e.pkgFailed = false
	}
	return e
}

// checkEnv is the wrapper's environment minus anything naming a token.
func checkEnv(base []string) []string {
	var env []string
	for _, kv := range base {
		k, _, _ := strings.Cut(kv, "=")
		if strings.Contains(strings.ToUpper(k), "TOKEN") || strings.HasPrefix(k, "NOVA_CARD_") {
			continue
		}
		env = append(env, kv)
	}
	return env
}

// tailBuffer keeps the last 64 KiB written to it.
type tailBuffer struct{ b []byte }

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.b = append(t.b, p...)
	if over := len(t.b) - 64<<10; over > 0 {
		t.b = t.b[over:]
	}
	return len(p), nil
}

// tail is the last n non-empty lines, joined with " | ".
func (t *tailBuffer) tail(n int) string {
	var rows []string
	for _, l := range strings.Split(string(t.b), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			rows = append(rows, l)
		}
	}
	if len(rows) > n {
		rows = rows[len(rows)-n:]
	}
	return oneField(strings.Join(rows, " | "))
}

// ChangedPaths is `git diff --name-only <base>..HEAD` in repo: the files the
// card's commits changed. nil when there is no repo, no base, or git refuses.
// A path with whitespace cannot be a PATHS entry and is left out.
func ChangedPaths(repo, base string) ([]string, error) {
	if base == "" {
		return nil, nil
	}
	if _, err := os.Stat(filepath.Join(repo, ".git")); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	cmd := exec.Command("git", "-C", repo, "diff", "--name-only", "-z", "--no-renames", base+"..HEAD", "--")
	cmd.Env = append(checkEnv(os.Environ()), "GIT_TERMINAL_PROMPT=0")
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		// An empty PATHS from a diff that failed (a bad base, a shallow
		// clone) is a lie the record would carry: the error is returned.
		return nil, fmt.Errorf("git diff %s..HEAD: %w: %s", base, err, strings.TrimSpace(errb.String()))
	}
	var paths []string
	for _, p := range strings.Split(out.String(), "\x00") {
		if p == "" || strings.ContainsAny(p, " \t\n\r") {
			continue
		}
		paths = append(paths, p)
		if len(paths) == 256 {
			break
		}
	}
	return paths, nil
}

// ProviderFacts reads the last START line the bench harness logged
// (`<ts> START <card> bench=.. tier=.. route=.. model=.. key=.. sha=..`) from
// the first of logs that has one: tier, route, model and key name (never a
// value; the harness logs names only).
func ProviderFacts(logs ...string) map[string]string {
	for _, p := range logs {
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var last string
		for _, l := range strings.Split(string(data), "\n") {
			if strings.Contains(l, " START ") || strings.HasPrefix(l, "START ") {
				last = l
			}
		}
		if last == "" {
			continue
		}
		out := map[string]string{}
		for _, f := range strings.Fields(last) {
			k, v, ok := strings.Cut(f, "=")
			switch {
			case !ok:
			case k == "tier" || k == "route" || k == "model" || k == "key":
				out[k] = v
			case k == "sha":
				out["card_sha"] = v
			}
		}
		return out
	}
	return nil
}

func oneField(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 1024 {
		s = s[:1024]
	}
	return s
}

// endRecord is what recordEnd needs from the run.
type endRecord struct {
	cfg     WrapperConfig
	job     string
	results string
	end     WrapperEnd
	wall    time.Duration
	why     string
	ticks   <-chan time.Time
	beat    func()
}

// recordEnd synthesizes the record and writes it, with the wrapper's facts,
// in one ns_card_result call. It returns the parse (valid tells whether it
// validated), whether the wrapper synthesized it, the reply code and an error
// for a call that could not be made.
func recordEnd(ctx context.Context, rec ResultRecorder, e endRecord) (res typedrec.Result, synth bool, code int, err error) {
	var cf CardFacts
	if r, ok := rec.(CardFactsReader); ok {
		if cf, err = r.CardFacts(ctx); err != nil {
			return typedrec.Result{}, false, WrapperExitRedis, fmt.Errorf("card facts: %w", err)
		}
	}
	resultPath := filepath.Join(e.results, "RESULT.md")
	raw, rerr := os.ReadFile(resultPath)
	present := rerr == nil
	if rerr != nil && !errors.Is(rerr, fs.ErrNotExist) {
		return typedrec.Result{}, false, 0, fmt.Errorf("read RESULT.md: %w", rerr)
	}
	branch := WrapperBranch(e.cfg.Sprint, e.cfg.Label, e.cfg.Attempt)
	repo := filepath.Join(e.job, "out", "repo")
	facts := []Fact{
		{"w_outcome", e.end.Outcome}, {"w_reason", e.end.Reason}, {"w_exit", strconv.Itoa(e.end.Exit)},
		{"w_wall_ms", strconv.FormatInt(e.wall.Milliseconds(), 10)},
		{"w_wall_max_s", strconv.FormatInt(int64(e.end.WallMax/time.Second), 10)},
		{"w_commit", e.end.Commit}, {"w_pushed_sha", e.end.PushedSHA}, {"w_branch", branch},
		{"w_why", oneField(e.why)},
	}
	if present {
		// The model's two lines, whatever the card's kind (#3919): ns_card_end
		// copies them onto the card record.
		m := typedrec.SplitModel(raw, cf.Kind)
		facts = append(facts, Fact{"w_line1", oneField(m.Line1)}, Fact{"w_line2", oneField(m.Line2)})
	}
	pf := ProviderFacts(filepath.Join(e.job, "out", "harness.log"), filepath.Join(e.job, "harness.log"))
	for _, k := range []string{"tier", "route", "model", "key", "card_sha"} {
		facts = append(facts, Fact{"w_" + k, pf[k]})
	}
	synth = present && typedrec.IsKind(cf.Kind)
	if synth {
		m := typedrec.SplitModel(raw, cf.Kind)
		var check CheckRun
		switch {
		case e.end.Outcome != "DONE":
			check = CheckRun{Check: "not-run", Gate: "TEST: not-run (outcome " + e.end.Outcome + " " + e.end.Reason + ")"}
		case m.Status != typedrec.StatusDone:
			check = CheckRun{Check: "not-run", Gate: "TEST: not-run (line 2 is not DONE)"}
		default:
			check = RunCheck(ctx, repo, cf.Test, e.cfg.CheckTimeout, e.ticks, e.beat)
		}
		paths, perr := ChangedPaths(repo, cf.BaseSHA)
		if perr != nil {
			// The record says the paths are unknown and why, in its note.
			m.Note = append(m.Note, "paths: "+perr.Error())
		}
		out := typedrec.Synthesize(raw, typedrec.WrapperFacts{
			Kind: cf.Kind, Attempt: e.cfg.Attempt, Repo: cf.Repo, Branch: branch, Paths: paths,
			Check: check.Check, Red: "not-run", Green: check.Green, Gates: []string{check.Gate},
		})
		// The model's own file stays beside the record as a log; the next
		// write replaces RESULT.md, so a failed copy here loses the model's
		// file and is an error.
		if err := os.WriteFile(filepath.Join(e.results, ModelResultName), raw, 0o644); err != nil {
			return typedrec.Result{}, true, 0, fmt.Errorf("write %s: %w", ModelResultName, err)
		}
		if err := os.WriteFile(resultPath, out, 0o644); err != nil {
			return typedrec.Result{}, true, 0, fmt.Errorf("write RESULT.md: %w", err)
		}
		raw = out
		note := strings.Join(m.Note, " | ")
		if len(note) > typedrec.MaxNoteBytes {
			note = note[:typedrec.MaxNoteBytes]
		}
		facts = append(facts,
			Fact{"w_synth", "1"}, Fact{"w_note", note},
			Fact{"w_check", check.Check}, Fact{"w_check_cmd", oneField(check.Cmd)},
			Fact{"w_check_ms", strconv.FormatInt(check.Wall.Milliseconds(), 10)},
			Fact{"w_check_tail", check.Tail}, Fact{"w_paths", strings.Join(paths, " ")})
	}
	if present {
		opt := typedrec.ParseOptions{ContractLine: cf.Contract}
		if typedrec.IsKind(cf.Kind) {
			opt.ExpectedKind = cf.Kind
		}
		if _, ok := rec.(CardFactsReader); !ok {
			if f, ok := rec.(ResultFacts); ok {
				if opt, err = f.ResultFacts(ctx); err != nil {
					return typedrec.Result{}, false, WrapperExitRedis, fmt.Errorf("card facts: %w", err)
				}
			}
		}
		opt.ExpectedAttempt = e.cfg.Attempt
		res = typedrec.ParseResult(raw, opt)
		facts = append(facts, Fact{"w_result", "present"})
	} else {
		facts = append(facts, Fact{"w_result", "absent"})
	}
	code, err = rec.Result(ctx, res, e.results, facts...)
	return res, synth, code, err
}

// wrapperField is a typed field the wrapper writes from the card and its own
// run, so a contradiction on it is a wrapper bug, never the model's.
func wrapperField(f string) bool {
	switch f {
	case "KIND", "ATTEMPT", "REPO", "BRANCH":
		return true
	}
	return false
}

// ShowRecord is `nova-sprint card show` (#3689): the card hash (never its
// token) and its current attempt's result hash, in one ns_card_show call.
// card is empty for a card that does not exist.
func ShowRecord(ctx context.Context, client redisFCaller, sprint, label string) (cardFields, resultFields map[string]string, err error) {
	reply, err := client.FCallRO(ctx, "ns_card_show", nil, sprint, label).StringSlice()
	if err != nil {
		return nil, nil, err
	}
	// The card's pairs, then "--" at a field position, then the result's pairs.
	cardFields, resultFields = map[string]string{}, map[string]string{}
	i := 0
	for ; i+1 < len(reply) && reply[i] != "--"; i += 2 {
		cardFields[reply[i]] = reply[i+1]
	}
	for i++; i+1 < len(reply); i += 2 {
		resultFields[reply[i]] = reply[i+1]
	}
	return cardFields, resultFields, nil
}

// ShowLines is the record as `card.<field> <value>` then `result.<field>
// <value>` lines, each part sorted, every value escaped onto one line.
func ShowLines(cardFields, resultFields map[string]string) []string {
	var out []string
	for _, part := range []struct {
		prefix string
		fields map[string]string
	}{{"card", cardFields}, {"result", resultFields}} {
		names := make([]string, 0, len(part.fields))
		for k := range part.fields {
			names = append(names, k)
		}
		sort.Strings(names)
		for _, k := range names {
			out = append(out, part.prefix+"."+k+" "+oneline.Escape(part.fields[k]))
		}
	}
	return out
}

// redisFCaller is the one call ShowRecord makes.
type redisFCaller interface {
	FCallRO(ctx context.Context, function string, keys []string, args ...interface{}) *redis.Cmd
}
