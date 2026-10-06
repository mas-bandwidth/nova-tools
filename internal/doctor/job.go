package doctor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os/exec"
	"regexp"
	"runtime/debug"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/friend"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// A job is one thing a machine is set up to do (keep local notes, message, be a friend,
// work cards, coordinate), and its readiness is a chain: each step stands on the ones
// before it, so the first step that fails is the one to fix, and every step after it is
// blocked, not failed. The steps call the checks the tools already have (nova-config
// status, nova-redis fn check, nova-friend check, ...); none is rewritten here
// (docs/SPEC-DOCTOR.md, "Jobs").

// Blocked is a job step not run because a step before it failed.
const Blocked Status = "blocked"

// Stage is where a step sits in the dependency order; a job's steps run in stage order.
type Stage int

const (
	Connectivity Stage = iota + 1
	Authentication
	ConfigRevision
	AppliedState
	Installed
	Supervisor
	SessionStage
)

var stageNames = map[Stage]string{
	Connectivity:   "connectivity",
	Authentication: "authentication",
	ConfigRevision: "schema and config revision",
	AppliedState:   "applied Redis state",
	Installed:      "installed binaries and functions",
	Supervisor:     "supervisor",
	SessionStage:   "session response",
}

func (s Stage) String() string { return stageNames[s] }

// JobInput is what the job's steps are about: the flags of `nova-doctor --job`, with the
// Redis address falling back to the environment the tools themselves read.
type JobInput struct {
	Job     string
	Redis   string // host:port
	As      string // the friend (friend job) or the coordinator (coordinator job)
	Dir     string // the friend's working directory, for the fix lines that need it
	Harness string // the friend's harness, when the friend's own check cannot say
}

// Step is one dependency of a job, checked by calling an existing tool through run.
type Step struct {
	Name  string
	Stage Stage
	Run   func(ctx context.Context, r *jobRun) Result
}

// Jobs are the job names, in the order help lists them.
var Jobs = []string{"local-notes", "messaging", "friend", "worker", "coordinator"}

// jobSteps names each job's steps; JobSteps sorts them by stage.
var jobSteps = map[string][]string{
	"local-notes": {"redis-reachable", "redis-login", "binaries"},
	"messaging":   {"redis-reachable", "redis-login", "binaries", "self"},
	"friend": {"redis-reachable", "redis-login", "binaries", "self",
		"daemon-running", "harness-responsive", "message-delivered", "session-receipt", "card-completion"},
	"worker": {"redis-reachable", "redis-login", "redis-functions", "binaries", "self", "swarm-binary"},
	"coordinator": {"redis-reachable", "redis-login", "store-login", "config-schema", "config-applied",
		"redis-acl", "redis-functions", "binaries", "self", "friends"},
}

// jobTools are the nova tools each job needs on PATH.
var jobTools = map[string][]string{
	"local-notes": {"nova-redis"},
	"messaging":   {"nova-bus"},
	"friend":      {"nova-bus", "nova-friend"},
	"worker":      {"nova-redis", "nova-sprint", "nova-swarm"},
	"coordinator": {"nova-bus", "nova-config", "nova-friend", "nova-redis", "nova-sprint", "nova-swarm"},
}

// steps is every step by name.
var steps = map[string]Step{}

func init() {
	for _, s := range []Step{
		{"redis-reachable", Connectivity, stepRedisReachable},
		{"redis-login", Authentication, stepRedisLogin},
		{"store-login", Authentication, stepStoreLogin},
		{"config-schema", ConfigRevision, stepConfigSchema},
		{"config-applied", AppliedState, stepConfigApplied},
		{"redis-acl", AppliedState, stepRedisACL},
		{"redis-functions", Installed, stepRedisFunctions},
		{"binaries", Installed, stepBinaries},
		{"self", Installed, func(ctx context.Context, r *jobRun) Result { return checkSelf(ctx, r.env) }},
		{"swarm-binary", Installed, stepSwarmBinary},
		{"daemon-running", Supervisor, stepDaemonRunning},
		{"harness-responsive", SessionStage, stepHarnessResponsive},
		{"message-delivered", SessionStage, stepMessageDelivered},
		{"session-receipt", SessionStage, stepSessionReceipt},
		{"card-completion", SessionStage, stepCardCompletion},
		{"friends", SessionStage, stepFriends},
	} {
		steps[s.Name] = s
	}
}

// JobSteps is a job's steps in dependency order, or nil for a name that is no job.
func JobSteps(job string) []Step {
	names, ok := jobSteps[job]
	if !ok {
		return nil
	}
	out := make([]Step, 0, len(names))
	for _, n := range names {
		out = append(out, steps[n])
	}
	slices.SortStableFunc(out, func(a, b Step) int { return int(a.Stage) - int(b.Stage) })
	return out
}

// The bounds of a job's output: a step's evidence and fix are cut to these many bytes,
// so the JSON of a whole job stays under a few kilobytes whatever a tool printed.
const (
	maxEvidence = 240
	maxFix      = 240
	stepTimeout = 15 * time.Second
)

// JobReport is the --job --json value: one result per step, the first missing one and
// its fix, and how many tools the run called.
type JobReport struct {
	Job          string   `json:"job"`
	Exit         int      `json:"exit"`
	Ready        bool     `json:"ready"`
	FirstMissing string   `json:"first_missing,omitempty"`
	Next         string   `json:"next,omitempty"`
	Calls        int      `json:"calls"`
	Steps        []Result `json:"steps"`
}

// SummaryLine is the line after the step lines: ready, or the first missing step and the
// command to run next.
func (j JobReport) SummaryLine() string {
	if j.Ready {
		return fmt.Sprintf("DOCTOR job=%s ready steps=%d calls=%d", j.Job, len(j.Steps), j.Calls)
	}
	return fmt.Sprintf("DOCTOR job=%s not-ready first_missing=%s calls=%d next: %s", j.Job, j.FirstMissing, j.Calls, j.Next)
}

// RunJob runs a job's steps in order. The first fail stops the chain: every later step
// is reported blocked on it. A warn does not stop it.
func RunJob(ctx context.Context, env Env, in JobInput, strict bool) (JobReport, error) {
	list := JobSteps(in.Job)
	if list == nil {
		return JobReport{}, fmt.Errorf("no job named %q; the jobs are %s", in.Job, strings.Join(Jobs, ", "))
	}
	r := &jobRun{env: env, in: in, memo: map[string]call{}}
	rep := JobReport{Job: in.Job}
	for _, s := range list {
		if rep.FirstMissing != "" {
			rep.Steps = append(rep.Steps, Result{Check: s.Name, Dependency: s.Stage.String(), Status: Blocked,
				Evidence: "not run: " + rep.FirstMissing + " fails first"})
			continue
		}
		res := runOne(ctx, env, Check{Name: s.Name, Dependency: s.Stage.String(),
			Run: func(ctx context.Context, _ Env) Result { return s.Run(ctx, r) }})
		res.Evidence, res.Fix = clip(res.Evidence, maxEvidence), clip(res.Fix, maxFix)
		if res.Status == Fail {
			rep.FirstMissing, rep.Next = s.Name, res.Fix
		}
		rep.Steps = append(rep.Steps, res)
	}
	rep.Calls = r.calls
	rep.Exit = ExitCode(rep.Steps, strict)
	rep.Ready = rep.FirstMissing == ""
	return rep, nil
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n - len("...")
	for cut > 0 && !utf8Start(s[cut]) {
		cut--
	}
	return s[:cut] + "..."
}

func utf8Start(b byte) bool { return b&0xC0 != 0x80 }

// jobRun is one job's run: the input, the Env, and every tool call it made, so a call two
// steps read (acl check is both the login and the users) runs once.
type jobRun struct {
	env   Env
	in    JobInput
	memo  map[string]call
	calls int
}

// call is one tool's answer: its stdout, what it said when it did not exit 0 (its stderr,
// else the error), and its exit code; -1 when it did not run at all.
type call struct {
	out, said string
	code      int
}

func (r *jobRun) exec(ctx context.Context, name string, args ...string) call {
	key := strings.Join(append([]string{name}, args...), "\x00")
	if c, ok := r.memo[key]; ok {
		return c
	}
	r.calls++
	cctx, cancel := context.WithTimeout(ctx, stepTimeout)
	out, err := r.env.Exec(cctx, name, args...)
	cancel()
	c := call{out: out}
	var coded interface{ ExitCode() int }
	var exitErr *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &exitErr):
		c.code, c.said = exitErr.ExitCode(), strings.TrimSpace(string(exitErr.Stderr))
	case errors.As(err, &coded):
		c.code, c.said = coded.ExitCode(), err.Error()
	default:
		c.code, c.said = -1, err.Error()
	}
	if c.said == "" {
		c.said = firstLine(out)
	}
	r.memo[key] = c
	return c
}

// notRun is the result for a tool that could not be started: it is not installed.
func notRun(tool string, c call) Result {
	return Result{Status: Fail, Evidence: fmt.Sprintf("%s did not run: %s", tool, c.said), Fix: installFix(tool)}
}

// installFix is the one command that installs a nova tool: from the module this binary
// was built from, as nova-up's binaries step names it.
func installFix(tool string) string {
	if bi, ok := debug.ReadBuildInfo(); ok && strings.Contains(bi.Main.Path, ".") {
		return fmt.Sprintf("go install %s/cmd/%s@latest", bi.Main.Path, tool)
	}
	return "go install ./cmd/" + tool + " (in a checkout of the nova-tools repository)"
}

func firstLine(s string) string {
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			return l
		}
	}
	return ""
}

var (
	remedyQuoted = regexp.MustCompile(`remedy="((?:[^"\\]|\\.)*)"`)
	remedyBare   = regexp.MustCompile(`remedy=(\S+)`)
	runNext      = regexp.MustCompile(`(?:;|^|\s)(?:run|next):\s*(.+)$`)
)

// remedy is the next command a tool named in what it said, or def when it named none or
// named only a help page (a help page is a door, not a fix).
func remedy(said, def string) string {
	var got string
	if m := remedyQuoted.FindStringSubmatch(said); m != nil {
		got = strings.ReplaceAll(m[1], `\"`, `"`)
	} else if m := remedyBare.FindStringSubmatch(said); m != nil {
		got = m[1]
	} else if m := runNext.FindStringSubmatch(said); m != nil {
		got = m[1]
	}
	got = strings.TrimSpace(got)
	if got == "" || strings.HasSuffix(got, " -h") || strings.HasSuffix(got, " help") || !strings.HasPrefix(got, "nova-") {
		return def
	}
	return got
}

func (r *jobRun) redisAddr() string {
	if r.in.Redis != "" {
		return r.in.Redis
	}
	for _, k := range []string{"NOVA_REDIS_ADDR", "NOVA_SPRINT_REDIS"} {
		if v := r.env.Getenv(k); v != "" {
			return v
		}
	}
	return ""
}

// again is this job's own command, for a fix that is to run the doctor with more to go on.
func (r *jobRun) again(extra string) string {
	s := "nova-doctor --job " + r.in.Job
	if r.in.As != "" {
		s += " --as " + r.in.As
	}
	if r.in.Dir != "" {
		s += " --dir " + r.in.Dir
	}
	return s + extra
}

func stepRedisReachable(ctx context.Context, r *jobRun) Result {
	addr := r.redisAddr()
	if addr == "" {
		return Result{Status: Fail, Evidence: "no Redis address: --redis, NOVA_REDIS_ADDR and NOVA_SPRINT_REDIS are unset",
			Fix: r.again(" --redis 127.0.0.1:6390")}
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return Result{Status: Fail, Evidence: fmt.Sprintf("the Redis address %q is not host:port: %v", addr, err),
			Fix: r.again(" --redis 127.0.0.1:6390")}
	}
	dctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if err := r.env.Dial(dctx, "tcp", addr); err != nil {
		fix := "tailscale ping " + host
		if ip := net.ParseIP(host); host == "localhost" || (ip != nil && ip.IsLoopback()) {
			fix = fmt.Sprintf("nova-redis serve --bind %s --port %s --dir ~/nova/stores/redis", host, port)
		}
		return Result{Status: Fail, Evidence: fmt.Sprintf("Redis at %s did not answer: %v", addr, err), Fix: fix}
	}
	return Result{Status: OK, Evidence: "Redis at " + addr + " answers"}
}

// aclCheck is `nova-redis acl check`: exit 0 the users are as rendered, 1 the store
// answered and they differ (or it refused a read), 2 it did not answer or refused the login.
func (r *jobRun) aclCheck(ctx context.Context) call {
	return r.exec(ctx, "nova-redis", "acl", "check", "--addr", r.redisAddr())
}

func stepRedisLogin(ctx context.Context, r *jobRun) Result {
	c := r.aclCheck(ctx)
	switch c.code {
	case -1:
		return notRun("nova-redis", c)
	case 0, 1:
		return Result{Status: OK, Evidence: "the store at " + r.redisAddr() + " accepted the login"}
	}
	return Result{Status: Fail, Evidence: "nova-redis acl check: " + c.said,
		Fix: "export NOVA_REDIS_USER=<user> NOVA_REDIS_PASSWORD_ENV=<variable holding its password>, then " + r.again("")}
}

func stepRedisACL(ctx context.Context, r *jobRun) Result {
	c := r.aclCheck(ctx)
	switch c.code {
	case -1:
		return notRun("nova-redis", c)
	case 0:
		return Result{Status: OK, Evidence: lineWith(c.out, "ACL CHECK")}
	}
	// The drift line's remedy is the apply command followed by prose ("... sets the users
	// that differ"), so the fix is the apply itself, with the address this check used; when
	// apply needs a password for a user the store lacks, its refusal names that next step.
	ev := lineWith(c.out, "ACL CHECK")
	if ev == "" {
		ev = c.said
	}
	return Result{Status: Fail, Evidence: "nova-redis acl check: " + ev, Fix: "nova-redis acl apply --addr " + r.redisAddr()}
}

// lineWith is the first line of out that starts with prefix, or "" when none does.
func lineWith(out, prefix string) string {
	for _, l := range strings.Split(out, "\n") {
		if l = strings.TrimSpace(l); strings.HasPrefix(l, prefix) {
			return l
		}
	}
	return ""
}

func stepRedisFunctions(ctx context.Context, r *jobRun) Result {
	c := r.exec(ctx, "nova-redis", "fn", "check", "--addr", r.redisAddr())
	switch c.code {
	case -1:
		return notRun("nova-redis", c)
	case 0:
		return Result{Status: OK, Evidence: firstLine(c.out)}
	}
	return Result{Status: Fail, Evidence: "nova-redis fn check: " + c.said,
		Fix: "nova-redis fn load --addr " + r.redisAddr()}
}

func stepStoreLogin(ctx context.Context, r *jobRun) Result {
	c := r.exec(ctx, "nova-config", "login", "--check")
	switch c.code {
	case -1:
		return notRun("nova-config", c)
	case 0:
		return Result{Status: OK, Evidence: "the recorded login resolves"}
	}
	return Result{Status: Fail, Evidence: "nova-config login --check: " + c.said,
		Fix: remedy(c.said, "nova-config login --store <dir> --as <seat> --key <file> --secret <NAME> --dsn <dsn> --friend <actor>")}
}

func stepConfigSchema(ctx context.Context, r *jobRun) Result {
	c := r.exec(ctx, "nova-config", "status")
	switch c.code {
	case -1:
		return notRun("nova-config", c)
	case 0:
		return Result{Status: OK, Evidence: firstLine(c.out)}
	}
	return Result{Status: Fail, Evidence: "nova-config status: " + c.said, Fix: remedy(c.said, "nova-config migrate")}
}

var configCheckLine = regexp.MustCompile(`^CONFIG CHECK kind=(\S+) add=(\d+) set=(\d+) remove=(\d+) rev=(\d+) applied=(\d+)`)

// stepConfigApplied reads `nova-config apply --check`: every kind with nothing to add, set
// or remove, and its applied revision the store's, is applied.
func stepConfigApplied(ctx context.Context, r *jobRun) Result {
	c := r.exec(ctx, "nova-config", "apply", "--check")
	switch c.code {
	case -1:
		return notRun("nova-config", c)
	case 0:
	default:
		return Result{Status: Fail, Evidence: "nova-config apply --check: " + c.said, Fix: remedy(c.said, "nova-config apply")}
	}
	var behind []string
	kinds := 0
	for _, l := range strings.Split(c.out, "\n") {
		m := configCheckLine.FindStringSubmatch(strings.TrimSpace(l))
		if m == nil {
			continue
		}
		kinds++
		if m[2] != "0" || m[3] != "0" || m[4] != "0" || m[5] != m[6] {
			behind = append(behind, fmt.Sprintf("%s(add=%s set=%s remove=%s rev=%s applied=%s)", m[1], m[2], m[3], m[4], m[5], m[6]))
		}
	}
	if kinds == 0 {
		return Result{Status: Fail, Evidence: "nova-config apply --check printed no CONFIG CHECK line: " + firstLine(c.out),
			Fix: "nova-config apply --check"}
	}
	if len(behind) > 0 {
		return Result{Status: Fail, Evidence: "Redis is behind the config: " + strings.Join(behind, " "), Fix: "nova-config apply"}
	}
	return Result{Status: OK, Evidence: strconv.Itoa(kinds) + " kinds applied at the store's revision"}
}

func stepBinaries(_ context.Context, r *jobRun) Result {
	found := toolsOnPath(r.env)
	var missing []string
	for _, t := range jobTools[r.in.Job] {
		if found[t] == "" {
			missing = append(missing, t)
		}
	}
	if len(missing) > 0 {
		return Result{Status: Fail, Evidence: "not on PATH: " + strings.Join(missing, ", "), Fix: installFix(missing[0])}
	}
	return Result{Status: OK, Evidence: "on PATH: " + strings.Join(jobTools[r.in.Job], ", ")}
}

// stepSwarmBinary is `nova-swarm doctor`: the PATH binary is the local build, and the
// headless harnesses it reports.
func stepSwarmBinary(ctx context.Context, r *jobRun) Result {
	c := r.exec(ctx, "nova-swarm", "doctor")
	switch c.code {
	case -1:
		return notRun("nova-swarm", c)
	case 0:
		return Result{Status: OK, Evidence: firstLine(c.out)}
	}
	said := c.said
	for _, l := range strings.Split(said, "\n") {
		if strings.HasPrefix(l, "DOCTOR REFUSED") {
			said = l
		}
	}
	return Result{Status: Fail, Evidence: "nova-swarm doctor: " + said,
		Fix: "install -m 0755 ~/.local/bin/nova-swarm \"$(command -v nova-swarm)\""}
}

// friendCheck is `nova-friend check <friend> --json`, read once for the five friend steps.
func (r *jobRun) friendCheck(ctx context.Context, who ...string) (friend.CheckReport, call, error) {
	args := append([]string{"check"}, who...)
	c := r.exec(ctx, "nova-friend", append(args, "--json")...)
	var rep friend.CheckReport
	if c.code == -1 || c.code == 2 {
		return rep, c, errors.New(c.said)
	}
	if err := json.Unmarshal([]byte(c.out), &rep); err != nil {
		return rep, c, fmt.Errorf("nova-friend check --json printed no report: %v", err)
	}
	return rep, c, nil
}

// theFriend is the friend job's one row, or the result that says why there is none.
func (r *jobRun) theFriend(ctx context.Context) (friend.FriendCheck, *Result) {
	if r.in.As == "" {
		return friend.FriendCheck{}, &Result{Status: Fail, Evidence: "the friend job needs the friend's name", Fix: r.again(" --as <friend>")}
	}
	rep, c, err := r.friendCheck(ctx, r.in.As)
	if c.code == -1 {
		res := notRun("nova-friend", c)
		return friend.FriendCheck{}, &res
	}
	if err != nil {
		return friend.FriendCheck{}, &Result{Status: Fail, Evidence: "nova-friend check: " + err.Error(),
			Fix: "nova-friend check " + r.in.As}
	}
	for _, f := range rep.Friends {
		if f.Friend == r.in.As {
			return f, nil
		}
	}
	return friend.FriendCheck{}, &Result{Status: Fail, Evidence: "nova-friend check knows no friend " + r.in.As,
		Fix: r.installFix(friend.FriendCheck{})}
}

// installFix is the friend's install line: install again starts the daemon and clears a
// broken session (docs/SPEC-FRIEND.md).
func (r *jobRun) installFix(f friend.FriendCheck) string {
	h := r.in.Harness
	if h == "" && f.Harness.Harness != "" && f.Harness.Harness != "-" {
		h = f.Harness.Harness
	}
	if h == "" {
		h = "<harness>"
	}
	return fmt.Sprintf("nova-friend install --as %s --harness %s --dir %s", r.in.As, h, r.dirOf())
}

// dirOf is the friend's working directory for a fix line, or a placeholder naming it.
func (r *jobRun) dirOf() string {
	if r.in.Dir != "" {
		return r.in.Dir
	}
	return "<the friend's working directory>"
}

func stepDaemonRunning(ctx context.Context, r *jobRun) Result {
	f, bad := r.theFriend(ctx)
	if bad != nil {
		return *bad
	}
	d := f.Daemon
	ev := fmt.Sprintf("daemon status=%s agent=%s pid=%s", d.Status, d.Agent, d.PID)
	if d.Status != "ok" {
		return Result{Status: Fail, Evidence: "the daemon is not running: " + ev, Fix: r.installFix(f)}
	}
	return Result{Status: OK, Evidence: "the daemon is running: " + ev}
}

func stepHarnessResponsive(ctx context.Context, r *jobRun) Result {
	f, bad := r.theFriend(ctx)
	if bad != nil {
		return *bad
	}
	h := f.Harness
	switch {
	case h.Broken != "" && h.Broken != "-":
		return Result{Status: Fail, Evidence: fmt.Sprintf("the session is broken since %s: %s", h.Broken, h.Reason), Fix: r.installFix(f)}
	case h.Delivered > 0 && h.Failed == h.Delivered:
		return Result{Status: Fail, Evidence: fmt.Sprintf("every delivery into the %s harness failed (%d of %d), last exit %s", h.Harness, h.Failed, h.Delivered, h.LastExit),
			Fix: r.installFix(f)}
	}
	return Result{Status: OK, Evidence: fmt.Sprintf("the %s harness takes turns: route=%s last_exit=%s failed_of_last20=%d", h.Harness, h.Route, h.LastExit, h.FailedOfLast20)}
}

// coordinator is who pings the friend in a fix line.
func (r *jobRun) coordinator() string {
	for _, k := range []string{"NOVA_SPRINT_ACTOR", "NOVA_FRIEND"} {
		if v := r.env.Getenv(k); v != "" && v != r.in.As {
			return v
		}
	}
	return "<coordinator>"
}

func stepMessageDelivered(ctx context.Context, r *jobRun) Result {
	f, bad := r.theFriend(ctx)
	if bad != nil {
		return *bad
	}
	h := f.Harness
	if h.Delivered == 0 {
		return Result{Status: Fail, Evidence: "no message was delivered into the session in the window",
			Fix: fmt.Sprintf("nova-friend ping --as %s --to %s", r.coordinator(), r.in.As)}
	}
	return Result{Status: OK, Evidence: fmt.Sprintf("%d messages delivered, %d failed, last %s", h.Delivered, h.Failed, h.Last)}
}

// stepSessionReceipt is nova-friend status's evidence rule (docs/SPEC-FRIEND.md, "Presence
// is her session's evidence"): a session pong under sprint.FriendPongWindow old, or a card
// of hers finished under sprint.FriendFinishWindow old, at the Env's clock. nova-friend
// check's pong_age is the age of the last pong ever recorded, so a pong on record is no
// receipt by itself; past both windows the session is deaf. Messages back are shown, and
// her daemon's beat is never read.
func stepSessionReceipt(ctx context.Context, r *jobRun) Result {
	f, bad := r.theFriend(ctx)
	if bad != nil {
		return *bad
	}
	shown := fmt.Sprintf("messages_back=%d last_back=%s", f.Bus.RealSince, f.Bus.LastReal)
	pongAge, pongErr := time.ParseDuration(f.Daemon.PongAge)
	if pongErr == nil && pongAge >= 0 && pongAge < sprint.FriendPongWindow {
		return Result{Status: OK, Evidence: fmt.Sprintf("the session answered: session pong %s ago; %s", f.Daemon.PongAge, shown)}
	}
	finished, finErr := time.Parse(time.RFC3339, f.Work.NewestAt)
	finishAge := r.env.Now().Sub(finished)
	if finErr == nil && finishAge >= 0 && finishAge < sprint.FriendFinishWindow {
		return Result{Status: OK, Evidence: fmt.Sprintf("the session answered: finished %s %s ago; %s", f.Work.NewestOutbox, friend.AgeString(finishAge), shown)}
	}
	ev := "deaf: no session pong within " + sprint.FriendPongWindow.String()
	if pongErr == nil {
		ev += " (last " + f.Daemon.PongAge + " ago)"
	}
	ev += ", no card finished within " + sprint.FriendFinishWindow.String()
	if finErr == nil {
		ev += " (last " + friend.AgeString(finishAge) + " ago)"
	}
	return Result{Status: Fail, Evidence: ev + "; " + shown + " (nova-friend verdict " + f.Verdict.Verdict + ")",
		Fix: fmt.Sprintf("nova-friend check --as %s --harness %s --dir %s", r.in.As, r.harnessOf(f), r.dirOf())}
}

func (r *jobRun) harnessOf(f friend.FriendCheck) string {
	if r.in.Harness != "" {
		return r.in.Harness
	}
	if f.Harness.Harness != "" && f.Harness.Harness != "-" {
		return f.Harness.Harness
	}
	return "<harness>"
}

// stepCardCompletion reports the friend's outbox: a completed card is a fact about the
// work, never a requirement for readiness, so none yet is ok and says so.
func stepCardCompletion(ctx context.Context, r *jobRun) Result {
	f, bad := r.theFriend(ctx)
	if bad != nil {
		return *bad
	}
	w := f.Work
	if w.Outbox == 0 {
		return Result{Status: OK, Evidence: fmt.Sprintf("no card completed yet: outbox=0 inbox=%d", w.Inbox)}
	}
	return Result{Status: OK, Evidence: fmt.Sprintf("cards completed: outbox=%d newest=%s at %s", w.Outbox, w.NewestOutbox, w.NewestAt)}
}

// stepFriends is the coordinator's view of every friend: a friend that is not ok is a
// warn naming the friend job to run for it, never a fail of the coordinator's own setup.
func stepFriends(ctx context.Context, r *jobRun) Result {
	var who []string
	if r.in.As != "" {
		who = []string{"--as", r.in.As}
	}
	rep, c, err := r.friendCheck(ctx, who...)
	if c.code == -1 {
		return notRun("nova-friend", c)
	}
	if err != nil {
		return Result{Status: Fail, Evidence: "nova-friend check: " + err.Error(), Fix: "nova-friend check"}
	}
	s := rep.Summary
	ev := fmt.Sprintf("friends=%d ok=%d broken=%d deaf=%d silent=%d down=%d untrue=%d", s.Friends, s.OK, s.Broken, s.Deaf, s.Silent, s.Down, s.Untrue)
	for _, f := range rep.Friends {
		if f.Verdict.Verdict != "ok" {
			return Result{Status: Warn, Evidence: ev + "; first not ok: " + f.Friend + " " + f.Verdict.Verdict + ": " + f.Verdict.Why,
				Fix: "nova-doctor --job friend --as " + f.Friend}
		}
	}
	return Result{Status: OK, Evidence: ev}
}
