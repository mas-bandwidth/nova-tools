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
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

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
}

var (
	testPkgRE  = regexp.MustCompile(`^\.(/[A-Za-z0-9_.-]+)*/?(\.\.\.)?$`)
	testNameRE = regexp.MustCompile(`^(Test|Example|Fuzz)[A-Za-z0-9_]*$`)
)

// TestCommand is the card's TEST line as argv: `<package> <TestName>` is
// `go test <package> -run ^<TestName>$ -count=1`. Anything else (absent,
// `none`, a free command) has no argv and why says so: the wrapper runs only
// the declared grammar and never assumes a package from PATHS.
func TestCommand(test string) (argv []string, why string) {
	f := strings.Fields(test)
	switch {
	case len(f) == 0:
		return nil, "no TEST line on the card"
	case len(f) == 1 && f[0] == "none":
		return nil, "TEST: none"
	case len(f) != 2 || !testPkgRE.MatchString(f[0]) || hasDotDot(f[0]) || !testNameRE.MatchString(f[1]):
		return nil, "TEST is not `<package> <TestName>`"
	}
	return []string{"go", "test", f[0], "-run", "^" + f[1] + "$", "-count=1"}, ""
}

func hasDotDot(p string) bool {
	for _, seg := range strings.Split(p, "/") {
		if seg == ".." {
			return true
		}
	}
	return false
}

// RunCheck runs the card's TEST line in repo under timeout, calling beat every
// tick while it runs (the card's lease stays live past the harness).
func RunCheck(ctx context.Context, repo, test string, timeout time.Duration, ticks <-chan time.Time, beat func()) CheckRun {
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
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, argv[0], argv[1:]...)
	cmd.Dir = repo
	cmd.Env = checkEnv(os.Environ())
	var out tailBuffer
	cmd.Stdout, cmd.Stderr = &out, &out
	harnessGroup(cmd)
	cmd.Cancel = func() error { killGroup(cmd); return nil }
	cmd.WaitDelay = 10 * time.Second
	began := time.Now()
	done := make(chan error, 1)
	if err := cmd.Start(); err != nil {
		return CheckRun{Check: "not-run", Cmd: cmdline, Gate: cmdline + ": not-run (" + oneField(err.Error()) + ")"}
	}
	go func() { done <- cmd.Wait() }()
	var err error
wait:
	for {
		select {
		case err = <-done:
			break wait
		case <-ticks:
			if beat != nil {
				beat()
			}
		}
	}
	r := CheckRun{Cmd: cmdline, Wall: time.Since(began), Tail: out.tail(3)}
	secs := fmt.Sprintf("%.2fs", r.Wall.Seconds())
	switch {
	case err == nil:
		r.Check = "pass"
		r.Gate = cmdline + ": pass " + secs
		r.Green = cmdline + ": pass " + secs
		if r.Tail != "" {
			r.Green += "; " + r.Tail
		}
	case cctx.Err() != nil:
		r.Check = "fail"
		r.Gate = cmdline + ": fail timeout after " + timeout.String()
	default:
		r.Check = "fail"
		r.Gate = cmdline + ": fail " + secs
		if r.Tail != "" {
			r.Gate += ": " + r.Tail
		}
	}
	return r
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
func ChangedPaths(repo, base string) []string {
	if base == "" {
		return nil
	}
	if _, err := os.Stat(filepath.Join(repo, ".git")); err != nil {
		return nil
	}
	cmd := exec.Command("git", "-C", repo, "diff", "--name-only", "-z", "--no-renames", base+"..HEAD", "--")
	cmd.Env = append(checkEnv(os.Environ()), "GIT_TERMINAL_PROMPT=0")
	var out bytes.Buffer
	cmd.Stdout = &out
	if cmd.Run() != nil {
		return nil
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
	return paths
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
		paths := ChangedPaths(repo, cf.BaseSHA)
		out := typedrec.Synthesize(raw, typedrec.WrapperFacts{
			Kind: cf.Kind, Attempt: e.cfg.Attempt, Repo: cf.Repo, Branch: branch, Paths: paths,
			Check: check.Check, Red: "not-run", Green: check.Green, Gates: []string{check.Gate},
		})
		// The model's own file stays beside the record as a log.
		_ = os.WriteFile(filepath.Join(e.results, ModelResultName), raw, 0o644)
		if err := os.WriteFile(resultPath, out, 0o644); err != nil {
			return typedrec.Result{}, true, 0, fmt.Errorf("write RESULT.md: %w", err)
		}
		raw = out
		note := strings.Join(m.Note, " | ")
		if len(note) > typedrec.MaxNoteBytes {
			note = note[:typedrec.MaxNoteBytes]
		}
		facts = append(facts,
			Fact{"w_synth", "1"}, Fact{"w_line2", oneField(m.Line2)}, Fact{"w_note", note},
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
