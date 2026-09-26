package fleetbuild

// Release (#4306, #4356 A) is `nova-sprint fleet release <sha>|dev`: the whole
// roll of a landed dev commit, which was nine hand commands across three tools
// on 2026-09-26 (an ssh for the admin password, fn deploy and fn check; go
// build, mv and version for the Studio; the bench play and an ssh verify), as
// one verb. In order, one receipt per step and per bench:
//
//	build     SOURCE (the release clone at the sha), BUILT (the release's
//	          own nova-sprint here, which carries the release's Lua), then
//	          fleet:release set, CONVERGED, and the builder's compile of
//	          every bench platform with its BUILD OK and MANIFEST lines (the
//	          published build the play installs)
//	fn        fn deploy by the release's nova-sprint as the Redis admin
//	          user; the password is the variable AdminEnv names (NS_ADMIN),
//	          else the seat's sealed NOVA_REDIS_ADMIN_PASSWORD, never an ssh
//	fn-check  fn check by the same binary: the store holds this release's
//	          library and ns_ping answers
//	play      the bench play through ansible, limited to the benches (so the
//	          coordinator's fn-load play never reloads the old library), one
//	          RECAP line per host; then the beat of every bench not yet on
//	          the release restarted through ansible, one BEAT line per bench
//	self      self update of this machine at the commit (SelfUpdate), then
//	          its loops kickstarted
//	verify    every bench's beat build read from the store until each names
//	          the version or the wait is spent, one VERIFY line per bench
//
// Each step ends in one `RELEASE <step> OK <detail>`, `RELEASE <step>
// REFUSED: <why> (<remedy>)` or `RELEASE <step> SKIPPED: <why>` line. A
// refusal never stops the steps after it that can still run (fn needs the
// release binary, the play needs the published build, self needs the
// commit), and the verify always runs. The last line is FLEET RELEASE
// OK|BEHIND|FAIL version=<v> benches=<n> behind=<list>|-.

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/buildinfo"
)

// ExecRunner runs one child in dir with extra environment and returns its
// combined output. Production execs the argv; tests fake it, so no test
// starts a process or reaches a host.
type ExecRunner interface {
	Run(ctx context.Context, dir string, env []string, argv []string) (string, error)
}

// ReleaseUnits are the loops on this machine that run the old binary until
// kickstarted (measured 2026-09-25: a swapped binary leaves the running loop
// on the old code), in the order they are restarted.
var ReleaseUnits = []string{"nova-sprint-reconciler", "sprint-table-live", "nova-sprint-bench-beat"}

// BeatUnit is the bench loop restarted on every rolled bench.
const BeatUnit = "nova-sprint-bench-beat"

const (
	// DefaultAdminEnv names the variable the admin password is read from.
	DefaultAdminEnv = "NS_ADMIN"
	// AdminUser is the Redis ACL user fn deploy runs as.
	AdminUser = "admin"
	// SealCommand seals the admin password into a seat once; the value
	// travels from the store host into nova-secrets seal --stdin only.
	SealCommand = "make -C ~/rowan-working/rowan-tools/fleet store-seal ROLE=admin SEAT="
	// ReleaseRepoURL is where the release clone comes from: the ssh remote
	// the coordinator's own clones use.
	ReleaseRepoURL = "git@github.com:mas-bandwidth/nova-tools.git"
	// SrcDirRel is the release clone, relative to home; ReleaseBinRel is
	// where the release's own nova-sprint is built, beside it.
	SrcDirRel     = "nova-bench/release-src/nova-tools"
	ReleaseBinRel = "nova-bench/release-src/bin"
	// DevArg is the argument that names dev's tip rather than a sha.
	DevArg = "dev"
)

// The step names, in order.
const (
	StepBuild   = "build"
	StepFn      = "fn"
	StepFnCheck = "fn-check"
	StepPlay    = "play"
	StepSelf    = "self"
)

// Step states.
const (
	StateOK      = "OK"
	StateRefused = "REFUSED"
	StateSkipped = "SKIPPED"
)

var shaRe = regexp.MustCompile(`^[0-9a-f]{8,40}$`)

// Release is one roll of a sha.
type Release struct {
	Client  *redis.Client // the fleet store: fleet:release, the beats
	Runner  ExecRunner
	Home    string // this machine's home: <Home>/.local/bin
	UID     int    // launchd's gui/<uid> domain
	GOOS    string // this machine's OS; "" is runtime.GOOS
	PID     int    // names self update's temp file
	Redis   string // the store address fn deploy and fn check are given
	RepoURL string
	SrcDir  string // "" is <Home>/SrcDirRel
	Train   string // "" is DefaultTrain
	// AdminEnv names the variable fn deploy reads the admin password from;
	// AdminPassword is its value (from that variable, else the seat), and
	// AdminWhy says why it is empty, remedy included.
	AdminEnv      string
	AdminPassword string
	AdminWhy      string
	Seat          string // the seat this run logs in as; fn check's login without the admin password
	BuildCmd      string // the builder's build command, "" is the compile verb
	Machines      []Machine
	Benches       []string // "" is every registry machine with the bench role
	PlayDir       string
	Play          string // "" is DefaultPlay
	Registry      string // the machines registry path the play's inventory reads
	Wait, Poll    time.Duration
	// Sleep pauses between verify reads; nil sleeps on a timer. Tests hand
	// in a fake, so no test waits on the clock.
	Sleep func(ctx context.Context, d time.Duration) error
	Out   io.Writer
}

// Step is one step's outcome.
type Step struct{ Name, State, Detail string }

// Line is the step's receipt.
func (s Step) Line() string {
	if s.State == StateOK {
		return "RELEASE " + s.Name + " OK " + s.Detail
	}
	return "RELEASE " + s.Name + " " + s.State + ": " + s.Detail
}

// ReleaseResult is what a run did.
type ReleaseResult struct {
	Version, Commit string
	Steps           []Step
	Checks          []BeatCheck
}

// Behind names the benches whose beat is not on the release.
func (r ReleaseResult) Behind() []string { return behind(r.Checks) }

// Failed names the steps that did not answer OK.
func (r ReleaseResult) Failed() []string {
	var out []string
	for _, s := range r.Steps {
		if s.State != StateOK {
			out = append(out, s.Name)
		}
	}
	return out
}

// OK is true when every step answered and every bench beat names the version.
func (r ReleaseResult) OK() bool {
	return len(r.Failed()) == 0 && len(r.Checks) > 0 && len(r.Behind()) == 0
}

// Line is the release's last receipt. FAIL wins over BEHIND: a step that did
// not answer is the thing to fix, and the benches behind are named either way.
func (r ReleaseResult) Line() string {
	word := "OK"
	b := r.Behind()
	switch {
	case len(r.Failed()) > 0 || len(r.Checks) == 0:
		word = "FAIL"
	case len(b) > 0:
		word = "BEHIND"
	}
	list := "-"
	if len(b) > 0 {
		list = strings.Join(b, ",")
	}
	return fmt.Sprintf("FLEET RELEASE %s version=%s benches=%d behind=%s", word, r.Version, len(r.Checks), list)
}

func (r *Release) printf(format string, a ...any) {
	if r.Out != nil {
		fmt.Fprintf(r.Out, format, a...)
	}
}

func (r *Release) step(res *ReleaseResult, name, state, format string, a ...any) {
	s := Step{Name: name, State: state, Detail: fmt.Sprintf(format, a...)}
	res.Steps = append(res.Steps, s)
	r.printf("%s\n", s.Line())
}

func (r *Release) goos() string {
	if r.GOOS != "" {
		return r.GOOS
	}
	return runtime.GOOS
}

func (r *Release) train() string {
	if r.Train != "" {
		return r.Train
	}
	return DefaultTrain
}

func (r *Release) srcDir() string {
	if r.SrcDir != "" {
		return r.SrcDir
	}
	return filepath.Join(r.Home, filepath.FromSlash(SrcDirRel))
}

func (r *Release) play() string {
	if r.Play != "" {
		return r.Play
	}
	return DefaultPlay
}

func (r *Release) run(ctx context.Context, dir string, env []string, argv ...string) (string, error) {
	return r.Runner.Run(ctx, dir, env, argv)
}

func (r *Release) sleep(ctx context.Context, d time.Duration) error {
	if r.Sleep != nil {
		return r.Sleep(ctx, d)
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// RestartArgv is the command that restarts loop unit on a machine of goos:
// launchd's kickstart in domain (gui/<uid> here, system on a LaunchDaemon
// bench, with sudo) or systemd's user restart.
func RestartArgv(goos, domain, unit string, sudo bool) ([]string, error) {
	switch goos {
	case "darwin":
		argv := []string{"launchctl", "kickstart", "-k", domain + "/com.nova.loop." + unit}
		if sudo {
			argv = append([]string{"sudo", "-n"}, argv...)
		}
		return argv, nil
	case "linux":
		return []string{"systemctl", "--user", "restart", "nova-loop-" + unit + ".service"}, nil
	}
	return nil, refused("no loop restart for %s (darwin kickstarts, linux restarts)", goos)
}

// AdminRemedy is what fills the admin password when it is empty: the seal of
// the store's admin password into the seat, once, then a rerun as that seat.
func AdminRemedy(env, seat string) string {
	if seat == "" {
		seat = "<seat>"
	}
	return "seal it once: " + SealCommand + seat + ", then rerun with --seat " + seat + " (or with " + env + " set)"
}

// versionOf is the build identity a nova-sprint version line names, "" when
// the line is not one.
func versionOf(out string) string {
	if f, ok := buildinfo.Parse(lastLine(out)); ok && f.Tool == "nova-sprint" {
		return f.Version
	}
	return ""
}

// rollList is the benches to roll: Benches, else every registry machine
// with the bench role, in registry order.
func (r *Release) rollList() []string {
	if len(r.Benches) > 0 {
		return r.Benches
	}
	var out []string
	for _, m := range r.Machines {
		if m.HasRole("bench") {
			out = append(out, m.Name)
		}
	}
	return out
}

// CheckArg refuses an argument that is neither DevArg nor a sha of 8 to 40
// hex digits, before anything is dialed.
func CheckArg(arg string) error {
	arg = strings.ToLower(strings.TrimSpace(arg))
	if arg != DevArg && !shaRe.MatchString(arg) {
		return refused("%q is not a commit sha of 8 to 40 hex digits (fleet release <sha> wants a landed dev commit, or dev for dev's tip)", arg)
	}
	return nil
}

// Resolve names the commit an argument means: dev's tip for DevArg (one git
// ls-remote, a DEV TIP line), else the argument as a sha of 8 to 40 hex
// digits. Refused before any step runs.
func (r *Release) Resolve(ctx context.Context, arg string) (string, error) {
	if err := CheckArg(arg); err != nil {
		return "", err
	}
	arg = strings.ToLower(strings.TrimSpace(arg))
	if arg != DevArg {
		return arg, nil
	}
	repo := r.RepoURL
	if repo == "" {
		repo = ReleaseRepoURL
	}
	out, err := r.run(ctx, "", nil, DevTipArgv(repo)...)
	if err != nil {
		return "", refused("git ls-remote %s %s: %v: %s (name the commit: fleet release <sha>)", repo, ReleaseBase, err, lastLine(out))
	}
	tip, ok := ParseDevTip(out)
	if !ok {
		return "", refused("git ls-remote %s answered %q, not a %s tip (name the commit: fleet release <sha>)", repo, lastLine(out), ReleaseBase)
	}
	r.printf("DEV TIP %s\n", tip)
	return tip, nil
}

// Run rolls the sha (or DevArg). An error is a refusal before any step
// (ErrRefused: a bad sha, no store, no benches, dev's tip unreadable) or the
// store failing; every step's own refusal is in the result.
func (r *Release) Run(ctx context.Context, arg string) (ReleaseResult, error) {
	var res ReleaseResult
	if r.Runner == nil {
		return res, refused("fleet release has no runner")
	}
	if r.Client == nil {
		return res, refused("fleet release needs the fleet store (--redis <addr>) for fleet:release and the verify")
	}
	benches := r.rollList()
	if len(benches) == 0 {
		return res, refused("no benches to roll: --benches <a,b,...>, or a machines registry with bench roles (--machines <file>, or %s)", MachinesEnv)
	}
	sha, err := r.Resolve(ctx, arg)
	if err != nil {
		return res, err
	}
	res.Version = r.train() + "-dev." + sha[:8]

	bin, published, err := r.build(ctx, sha, benches, &res)
	if err != nil {
		return res, err
	}
	r.fn(ctx, bin, &res)
	r.fnCheck(ctx, bin, &res)
	if err := r.playStep(ctx, published, benches, &res); err != nil {
		return res, err
	}
	if err := r.self(ctx, &res); err != nil {
		return res, err
	}
	return res, r.verify(ctx, benches, &res)
}

// build is the build step: the source, the release's own nova-sprint, the
// release fields and the builder's compile. It returns the release binary
// ("" when it was not built) and whether the builder published the build;
// an error is the store failing.
func (r *Release) build(ctx context.Context, sha string, benches []string, res *ReleaseResult) (string, bool, error) {
	refuse := func(err error) (string, bool, error) {
		r.step(res, StepBuild, StateRefused, "%s", strings.TrimPrefix(err.Error(), ErrRefused.Error()+": "))
		return "", false, nil
	}
	commit, err := r.source(ctx, sha)
	if err != nil {
		return refuse(err)
	}
	res.Commit = commit
	bin, binErr := r.releaseBin(ctx, res.Version)

	if _, err := SetFields(ctx, r.Client, []string{"version=" + res.Version, "commit=" + commit}); err != nil {
		return "", false, err
	}
	r.printf("FLEET RELEASE SET version=%s commit=%s\n", res.Version, commit)
	facts, err := ReadFacts(ctx, r.Client)
	if err != nil {
		return "", false, err
	}
	facts.Machines = r.Machines
	if conv := Converge(facts); len(conv) > 0 {
		if err := WriteConverged(ctx, r.Client, conv); err != nil {
			return "", false, err
		}
		r.printf("CONVERGED %s\n", Fields(conv))
	}
	cfg, err := ReadConfig(ctx, r.Client)
	if err != nil {
		return "", false, err
	}
	plan, err := MakePlan(cfg, benches)
	if err != nil {
		if binErr != nil {
			return refuse(binErr)
		}
		r.step(res, StepBuild, StateRefused, "%s (the release binary %s is built; the play needs the builder's build)", strings.TrimPrefix(err.Error(), ErrRefused.Error()+": "), bin)
		return bin, false, nil
	}
	d := &Deployer{Client: r.Client, Runner: argvRunner{r.Runner}, Home: r.Home, BuildCmd: r.BuildCmd, Redis: r.Redis, Out: r.Out}
	tools, err := d.Build(ctx, plan)
	if err != nil {
		return "", false, err
	}
	switch {
	case binErr != nil:
		r.step(res, StepBuild, StateRefused, "%s", strings.TrimPrefix(binErr.Error(), ErrRefused.Error()+": "))
		return "", tools != nil, nil
	case tools == nil:
		r.step(res, StepBuild, StateRefused, "the builder %s did not publish %s: read the BUILD FAIL or MANIFEST FAIL line above (rerun once it builds; what is done is kept)", plan.Builder, res.Version)
		return bin, false, nil
	}
	r.step(res, StepBuild, StateOK, "version=%s commit=%s builder=%s platforms=%s tools=%d bin=%s",
		res.Version, commit[:12], plan.Builder, strings.Join(plan.BuildPlatforms, ","), len(tools), bin)
	return bin, true, nil
}

// source clones (shallow, dev only) on first use, fetches dev, resolves the
// sha to its 40 digits and checks it out detached.
func (r *Release) source(ctx context.Context, sha string) (string, error) {
	dir := r.srcDir()
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
			return "", refused("release source: %v", err)
		}
		repo := r.RepoURL
		if repo == "" {
			repo = ReleaseRepoURL
		}
		if out, err := r.run(ctx, "", nil, "git", "clone", "-q", "--depth", "50", "--single-branch", "--branch", ReleaseBase, repo, dir); err != nil {
			return "", refused("git clone %s: %v: %s (check this machine's GitHub ssh key)", repo, err, lastLine(out))
		}
	}
	if out, err := r.run(ctx, dir, nil, "git", "fetch", "-q", "--depth", "50", "origin", ReleaseBase); err != nil {
		return "", refused("git fetch origin %s in %s: %v: %s (check this machine's GitHub ssh key)", ReleaseBase, dir, err, lastLine(out))
	}
	resolve := func() (string, bool) {
		out, err := r.run(ctx, dir, nil, "git", "rev-parse", "--verify", "--quiet", sha+"^{commit}")
		c := lastLine(out)
		return c, err == nil && commitRe.MatchString(c)
	}
	commit, ok := resolve()
	if !ok && len(sha) == 40 {
		if out, err := r.run(ctx, dir, nil, "git", "fetch", "-q", "--depth", "1", "origin", sha); err != nil {
			return "", refused("git fetch origin %s: %v: %s (is it pushed?)", sha, err, lastLine(out))
		}
		commit, ok = resolve()
	}
	if !ok {
		return "", refused("%s is not a commit in %s's last 50 (fleet release wants a landed dev sha; a full 40-digit sha is fetched on its own)", sha, ReleaseBase)
	}
	if out, err := r.run(ctx, dir, nil, "git", "checkout", "-q", "--detach", commit); err != nil {
		return "", refused("git checkout %s: %v: %s", commit, err, lastLine(out))
	}
	r.printf("SOURCE %s %s\n", dir, commit)
	return commit, nil
}

// releaseBin builds the release's own nova-sprint for this machine, with the
// module's pinned Go, into ReleaseBinRel/nova-sprint-<v>: the binary fn
// deploy and fn check run, so the library they load and compare is the
// release's. A file already answering version is kept.
func (r *Release) releaseBin(ctx context.Context, version string) (string, error) {
	bin := filepath.Join(r.Home, filepath.FromSlash(ReleaseBinRel), "nova-sprint-"+version)
	if out, err := r.run(ctx, "", nil, bin, "version"); err == nil && versionOf(out) == version {
		r.printf("KEPT %s %s\n", bin, version)
		return bin, nil
	}
	tc, err := PinnedToolchain(filepath.Join(r.srcDir(), "go.mod"))
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		return "", refused("release bin directory: %v", err)
	}
	out, err := r.run(ctx, r.srcDir(), []string{"GOTOOLCHAIN=" + tc}, "go", "build", "-trimpath", "-ldflags", "-X main.version="+version, "-o", bin, "./cmd/nova-sprint")
	if err != nil {
		return "", refused("go build %s with %s: %v: %s (fn and fn-check need it; the builder and self update build on their own)", version, tc, err, lastLine(out))
	}
	out, err = r.run(ctx, "", nil, bin, "version")
	if got := versionOf(out); err != nil || got != version {
		return "", refused("%s answers %q, not %s: %v (the build is not the release)", bin, lastLine(out), version, err)
	}
	r.printf("BUILT %s %s\n", version, bin)
	return bin, nil
}

// adminEnv is the fn children's login as the Redis admin user: the password
// in the variable AdminEnv names, in the child's environment only, and no
// seat (a seat's login would win over the admin's).
func (r *Release) adminEnv() []string {
	env := r.AdminEnv
	if env == "" {
		env = DefaultAdminEnv
	}
	return []string{"NOVA_SEAT=", "NOVA_SPRINT_REDIS_USER=" + AdminUser, "NOVA_SPRINT_REDIS_PASSWORD_ENV=" + env, env + "=" + r.AdminPassword}
}

// fn is the fn step: fn deploy by the release binary as the admin user.
func (r *Release) fn(ctx context.Context, bin string, res *ReleaseResult) {
	if bin == "" {
		r.step(res, StepFn, StateSkipped, "no release binary to deploy the library from (the build line names why)")
		return
	}
	if r.AdminPassword == "" {
		why := r.AdminWhy
		if why == "" {
			env := r.AdminEnv
			if env == "" {
				env = DefaultAdminEnv
			}
			why = env + " is empty and no seat holds the admin password (" + AdminRemedy(env, r.Seat) + ")"
		}
		r.step(res, StepFn, StateRefused, "no admin password for %s: %s", r.Redis, why)
		return
	}
	out, err := r.run(ctx, "", r.adminEnv(), bin, "fn", "deploy", "--redis", r.Redis)
	if err != nil {
		r.step(res, StepFn, StateRefused, "fn deploy --redis %s as %s: %v: %s (check the store address and the admin password)", r.Redis, AdminUser, err, lastLine(out))
		return
	}
	r.step(res, StepFn, StateOK, "%s", lastLine(out))
}

// fnCheck is the fn-check step: fn check by the release binary, as the admin
// user when its password is known, else as this run's seat.
func (r *Release) fnCheck(ctx context.Context, bin string, res *ReleaseResult) {
	if bin == "" {
		r.step(res, StepFnCheck, StateSkipped, "no release binary to compare the library with (the build line names why)")
		return
	}
	var env []string
	switch {
	case r.AdminPassword != "":
		env = r.adminEnv()
	case r.Seat != "":
		env = []string{"NOVA_SEAT=" + r.Seat}
	}
	out, err := r.run(ctx, "", env, bin, "fn", "check", "--redis", r.Redis)
	if err != nil {
		r.step(res, StepFnCheck, StateRefused, "%s: %v (the store does not hold %s's library; fix the fn step and rerun)", lastLine(out), err, res.Version)
		return
	}
	r.step(res, StepFnCheck, StateOK, "%s", lastLine(out))
}

// playStep is the play step: the bench play through ansible, then the beats
// not on the release restarted through ansible. An error is the store failing.
func (r *Release) playStep(ctx context.Context, published bool, benches []string, res *ReleaseResult) error {
	if !published {
		r.step(res, StepPlay, StateSkipped, "the builder published no build of %s to install (the build line names why)", res.Version)
		return nil
	}
	if r.Registry == "" {
		r.step(res, StepPlay, StateRefused, "the play's inventory reads the machines registry (--machines <file>, or %s)", MachinesEnv)
		return nil
	}
	if r.PlayDir == "" {
		r.step(res, StepPlay, StateRefused, "no fleet play directory (--play-dir <dir>, or %s)", PlayDirEnv)
		return nil
	}
	for _, f := range []string{PlayInventory, r.play()} {
		if _, err := os.Stat(filepath.Join(r.PlayDir, f)); err != nil {
			r.step(res, StepPlay, StateRefused, "the fleet play directory %s has no %s (--play-dir <dir>, or %s)", r.PlayDir, f, PlayDirEnv)
			return nil
		}
	}
	// --limit always: the play's last play is the coordinator's fn-load,
	// which would reload the library of the coordinator's own (not yet
	// updated) nova-sprint over the one the fn step deployed.
	pctx, cancel := context.WithTimeout(ctx, PlayTimeout)
	out, playErr := r.run(pctx, r.PlayDir, PlayEnv(r.Registry), PlayArgv(r.play(), res.Version, benches)...)
	cancel()
	recap := Recap(out)
	for _, row := range recap {
		r.printf("RECAP %s\n", row)
	}

	checks, err := VerifyBeats(ctx, r.Client, benches, res.Version)
	if err != nil {
		return err
	}
	restart := behind(checks)
	var failed []string
	if len(restart) > 0 {
		rctx, cancel := context.WithTimeout(ctx, RestartTimeout)
		out, _ := r.run(rctx, r.PlayDir, PlayEnv(r.Registry), RestartBeatsArgv(restart)...)
		cancel()
		st := ParseAdhoc(out)
		for _, b := range restart {
			switch s := st[b]; s {
			case "CHANGED", "SUCCESS":
				r.printf("BEAT %s restarted\n", b)
			default:
				if s == "" {
					s = "no answer"
				}
				r.printf("BEAT %s failed %s\n", b, strings.ToLower(strings.TrimSuffix(s, "!")))
				failed = append(failed, b)
			}
		}
	}
	switch {
	case playErr != nil:
		r.step(res, StepPlay, StateRefused, "%s %s: %s: %s (read the RECAP lines; a rerun converges what is left)",
			r.play(), res.Version, strings.Join(strings.Fields(playErr.Error()), "_"), lastLine(out))
	case len(failed) > 0:
		r.step(res, StepPlay, StateRefused, "the beat restart failed on %s (read the BEAT lines; a rerun restarts the beats still behind)", strings.Join(failed, ","))
	default:
		r.step(res, StepPlay, StateOK, "%s version=%s hosts=%d beats-restarted=%d", r.play(), res.Version, len(recap), len(restart))
	}
	return nil
}

// self is the self step: self update of this machine at the commit, then
// its loops kickstarted, unless the binary was current and this machine's
// beat already names the version (the loops run it). An error is the store
// failing.
func (r *Release) self(ctx context.Context, res *ReleaseResult) error {
	if res.Commit == "" {
		r.step(res, StepSelf, StateSkipped, "no source commit to build (the build line names why)")
		return nil
	}
	su := &SelfUpdate{Runner: r.Runner, Home: r.Home, Sha: res.Commit, Train: r.Train, RepoURL: r.RepoURL, PID: r.PID, Out: r.Out}
	sr, err := su.Run(ctx)
	if err != nil {
		r.step(res, StepSelf, StateRefused, "%s", strings.TrimPrefix(err.Error(), ErrRefused.Error()+": "))
		return nil
	}
	if sr.Skipped {
		if me := onlyWithRole(r.Machines, "coordination"); me != "" {
			checks, err := VerifyBeats(ctx, r.Client, []string{me}, res.Version)
			if err != nil {
				return err
			}
			if checks[0].OK() {
				r.step(res, StepSelf, StateOK, "%s already answers %s, its loops too", sr.Bin, sr.New)
				return nil
			}
		}
	}
	for _, unit := range ReleaseUnits {
		argv, err := RestartArgv(r.goos(), fmt.Sprintf("gui/%d", r.UID), unit, false)
		if err != nil {
			r.step(res, StepSelf, StateRefused, "%s", strings.TrimPrefix(err.Error(), ErrRefused.Error()+": "))
			return nil
		}
		if out, err := r.run(ctx, "", nil, argv...); err != nil {
			r.step(res, StepSelf, StateRefused, "%s: %v: %s (the binary is installed; kickstart the loop by hand, then rerun)", strings.Join(argv, " "), err, lastLine(out))
			return nil
		}
		r.printf("KICKSTARTED %s\n", unit)
	}
	r.step(res, StepSelf, StateOK, "%s -> %s bin=%s", sr.Old, sr.New, sr.Bin)
	return nil
}

// verify reads every bench's beat until each names the version or the wait
// is spent, then prints one VERIFY line per bench. It always runs.
func (r *Release) verify(ctx context.Context, benches []string, res *ReleaseResult) error {
	wait, poll := r.Wait, r.Poll
	if poll <= 0 {
		poll = DefaultVerifyPoll
	}
	if wait < 0 {
		wait = 0
	}
	for reads := int(wait / poll); ; reads-- {
		checks, err := VerifyBeats(ctx, r.Client, benches, res.Version)
		if err != nil {
			return err
		}
		res.Checks = checks
		if reads <= 0 || len(res.Behind()) == 0 {
			break
		}
		if err := r.sleep(ctx, poll); err != nil {
			return err
		}
	}
	for _, c := range res.Checks {
		r.printf("%s\n", c.Line())
	}
	return nil
}

type argvRunner struct{ r ExecRunner }

func (a argvRunner) Run(ctx context.Context, argv []string) (string, error) {
	return a.r.Run(ctx, "", nil, argv)
}
