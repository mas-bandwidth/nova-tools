package fleetbuild

// Release (#4306) is `nova-sprint fleet release <sha>`: the deploy of a landed
// dev tip that was six hand steps on 2026-09-26, as one verb with one receipt
// line per step, in this order:
//
//	SOURCE      the clone under ~/nova-bench/release-src/nova-tools fetches dev
//	            and checks out the sha (a short sha resolves to its 40 digits)
//	BUILT       go build -trimpath -ldflags "-X main.version=<v>" of
//	            cmd/nova-sprint to ~/.local/bin/nova-sprint.new
//	MOVED       the new binary renamed over the live one (mv, never cp over a
//	            running binary), then `nova-sprint version` must name <v>
//	KICKSTARTED the reconciler, the live table and the bench beat here
//	            (launchctl kickstart -k gui/<uid>/com.nova.loop.<unit> on
//	            darwin, systemctl --user restart nova-loop-<unit> on linux)
//	FN          `nova-sprint fn deploy --redis <store>` as the Redis admin
//	            user (the coordinator seat may not FUNCTION LOAD, #4175); the
//	            password is the variable AdminEnv names, never an ssh
//	ROLLED      the bench roll: fleet:release set to <v>/<sha>, then `fleet
//	            build` per pair of benches (seven at once hit the 80 s bound;
//	            pairs install in 35-65 s), each rolled bench's beat restarted
//
// The first failure refuses (ErrRefused, the line names the remedy) and
// nothing after it runs, with one exception: an empty admin password refuses
// the FN step alone, prints the exact remedy, and the roll still runs, so
// the verb ends FAIL with fn=refused. It is idempotent: a Studio already
// answering <v> is SKIPPED (no build, move or kickstart), and so is a bench
// whose beat already names <v>.

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

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
	// AdminPassFile is where the stack host keeps the admin password.
	AdminPassFile = "/var/lib/nova-redis/admin.pass"
	// ReleaseRepoURL is where the release clone comes from: the ssh remote
	// the coordinator's own clones use.
	ReleaseRepoURL = "git@github.com:mas-bandwidth/nova-tools.git"
	// SrcDirRel is the release clone, relative to home.
	SrcDirRel = "nova-bench/release-src/nova-tools"
	// RollPair is how many benches one fleet build installs at once.
	RollPair = 2
)

var shaRe = regexp.MustCompile(`^[0-9a-f]{8,40}$`)

// Release is one deploy of a sha.
type Release struct {
	Client        *redis.Client // the fleet store, for the roll
	Runner        ExecRunner
	Home          string // this machine's home: <Home>/.local/bin
	UID           int    // launchd's gui/<uid> domain
	GOOS          string // this machine's OS; "" is runtime.GOOS
	Redis         string // the store address fn deploy and the roll are given
	RepoURL       string
	SrcDir        string // "" is <Home>/SrcDirRel
	Train         string // "" is DefaultTrain
	AdminEnv      string // the variable holding the admin password
	AdminPassword string // its value; "" refuses the FN step alone
	BuildCmd      string // fleet build's --build-cmd, "" is the compile verb
	Machines      []Machine
	Benches       []string // "" is every registry machine with the bench role
	StudioOnly    bool     // steps SOURCE..FN only
	BenchesOnly   bool     // the roll only
	Out           io.Writer
}

// ReleaseResult is what a run did.
type ReleaseResult struct {
	Version, Commit string
	Studio          string // BUILT, SKIPPED or - (benches only)
	Fn              string // ok, refused, or - (benches only)
	Rolled, Skipped []string
}

// OK is true when every step it ran answered.
func (r ReleaseResult) OK() bool { return r.Fn != "refused" }

// Line is the release's last receipt: FLEET RELEASE OK|FAIL version=...
func (r ReleaseResult) Line() string {
	word := "OK"
	if !r.OK() {
		word = "FAIL"
	}
	commit := r.Commit
	if len(commit) > 12 {
		commit = commit[:12]
	}
	return fmt.Sprintf("FLEET RELEASE %s version=%s commit=%s studio=%s fn=%s rolled=%d skipped=%d",
		word, r.Version, commit, strings.ToLower(r.Studio), r.Fn, len(r.Rolled), len(r.Skipped))
}

func (r *Release) printf(format string, a ...any) {
	if r.Out != nil {
		fmt.Fprintf(r.Out, format, a...)
	}
}

func (r *Release) goos() string {
	if r.GOOS != "" {
		return r.GOOS
	}
	return runtime.GOOS
}

func (r *Release) srcDir() string {
	if r.SrcDir != "" {
		return r.SrcDir
	}
	return filepath.Join(r.Home, filepath.FromSlash(SrcDirRel))
}

func (r *Release) run(ctx context.Context, dir string, env []string, argv ...string) (string, error) {
	return r.Runner.Run(ctx, dir, env, argv)
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

// Remedy is the line that fills the admin password, printed when it is empty.
func Remedy(env, stackHost, sha string) string {
	return "export " + env + "=$(ssh " + stackHost + " 'sudo cat " + AdminPassFile + "'), then rerun nova-sprint fleet release " + sha +
		" (the Studio and every bench already on it are SKIPPED)"
}

// versionOf is the build identity a nova-sprint version line names, "" when
// the line is not one.
func versionOf(out string) string {
	if f, ok := buildinfo.Parse(lastLine(out)); ok && f.Tool == "nova-sprint" {
		return f.Version
	}
	return ""
}

// Run does the steps. An error is a refusal (ErrRefused) naming the remedy,
// or the store failing; the result says how far it got.
func (r *Release) Run(ctx context.Context, sha string) (ReleaseResult, error) {
	var res ReleaseResult
	sha = strings.ToLower(strings.TrimSpace(sha))
	if !shaRe.MatchString(sha) {
		return res, refused("%q is not a commit sha of 8 to 40 hex digits (fleet release <sha> wants a landed dev commit)", sha)
	}
	if r.StudioOnly && r.BenchesOnly {
		return res, refused("--studio-only and --benches-only leave nothing to do")
	}
	train := r.Train
	if train == "" {
		train = DefaultTrain
	}
	commit := sha
	if !(r.BenchesOnly && len(sha) == 40) {
		c, err := r.source(ctx, sha)
		if err != nil {
			return res, err
		}
		commit = c
	}
	res.Commit = commit
	res.Version = train + "-dev." + commit[:8]
	res.Studio, res.Fn = "-", "-"
	if !r.BenchesOnly {
		studio, err := r.studio(ctx, res.Version)
		if err != nil {
			return res, err
		}
		res.Studio = studio
		fnState, err := r.fn(ctx, sha)
		if err != nil {
			return res, err
		}
		res.Fn = fnState
	}
	if !r.StudioOnly {
		if err := r.roll(ctx, &res); err != nil {
			return res, err
		}
	}
	return res, nil
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
			return "", refused("git clone %s: %v: %s", repo, err, lastLine(out))
		}
	}
	if out, err := r.run(ctx, dir, nil, "git", "fetch", "-q", "--depth", "50", "origin", ReleaseBase); err != nil {
		return "", refused("git fetch origin %s in %s: %v: %s", ReleaseBase, dir, err, lastLine(out))
	}
	resolve := func() (string, bool) {
		out, err := r.run(ctx, dir, nil, "git", "rev-parse", "--verify", "--quiet", sha+"^{commit}")
		c := lastLine(out)
		return c, err == nil && commitRe.MatchString(c)
	}
	commit, ok := resolve()
	if !ok && len(sha) == 40 {
		if out, err := r.run(ctx, dir, nil, "git", "fetch", "-q", "--depth", "1", "origin", sha); err != nil {
			return "", refused("git fetch origin %s: %v: %s", sha, err, lastLine(out))
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

// studio builds, moves and kickstarts here; SKIPPED when the live binary
// already answers version.
func (r *Release) studio(ctx context.Context, version string) (string, error) {
	bin := filepath.Join(r.Home, filepath.FromSlash(BinDir), "nova-sprint")
	if out, err := r.run(ctx, "", nil, bin, "version"); err == nil && versionOf(out) == version {
		r.printf("SKIPPED studio %s\n", version)
		return "SKIPPED", nil
	}
	newBin := bin + ".new"
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		return "", refused("bin directory: %v", err)
	}
	out, err := r.run(ctx, r.srcDir(), nil, "go", "build", "-trimpath", "-ldflags", "-X main.version="+version, "-o", newBin, "./cmd/nova-sprint")
	if err != nil {
		return "", refused("go build %s: %v: %s", version, err, lastLine(out))
	}
	r.printf("BUILT %s %s\n", version, newBin)
	if err := os.Rename(newBin, bin); err != nil {
		return "", refused("mv %s %s: %v", newBin, bin, err)
	}
	out, err = r.run(ctx, "", nil, bin, "version")
	if got := versionOf(out); err != nil || got != version {
		return "", refused("%s version answers %q, not %s: %v (the moved binary is not the build)", bin, lastLine(out), version, err)
	}
	r.printf("MOVED %s %s\n", bin, version)
	for _, unit := range ReleaseUnits {
		argv, err := RestartArgv(r.goos(), fmt.Sprintf("gui/%d", r.UID), unit, false)
		if err != nil {
			return "", err
		}
		if out, err := r.run(ctx, "", nil, argv...); err != nil {
			return "", refused("%s: %v: %s (the binary is moved; kickstart the loop by hand, then rerun)", strings.Join(argv, " "), err, lastLine(out))
		}
		r.printf("KICKSTARTED %s\n", unit)
	}
	return "BUILT", nil
}

// fn runs fn deploy as the admin user with the password from AdminEnv, or
// refuses that step alone with the remedy when the variable is empty.
func (r *Release) fn(ctx context.Context, sha string) (string, error) {
	env := r.AdminEnv
	if env == "" {
		env = DefaultAdminEnv
	}
	if r.AdminPassword == "" {
		stack := onlyWithRole(r.Machines, "services")
		if stack == "" {
			stack = "space"
		}
		r.printf("FN REFUSED store=%s reason=no-admin-password env=%s remedy=%s\n", r.Redis, env, Remedy(env, stack, sha))
		return "refused", nil
	}
	bin := filepath.Join(r.Home, filepath.FromSlash(BinDir), "nova-sprint")
	out, err := r.run(ctx, "", []string{"NOVA_SPRINT_REDIS_USER=" + AdminUser, "NOVA_SPRINT_REDIS_PASSWORD_ENV=" + env},
		bin, "fn", "deploy", "--redis", r.Redis)
	if err != nil {
		return "", refused("fn deploy --redis %s as %s: %v: %s", r.Redis, AdminUser, err, lastLine(out))
	}
	r.printf("FN %s\n", lastLine(out))
	return "ok", nil
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

func (r *Release) hostOf(bench string) string {
	for _, m := range r.Machines {
		if m.Name == bench {
			return m.Host()
		}
	}
	return bench
}

type argvRunner struct{ r ExecRunner }

func (a argvRunner) Run(ctx context.Context, argv []string) (string, error) {
	return a.r.Run(ctx, "", nil, argv)
}

// roll sets fleet:release, skips the benches whose beat names the version,
// installs the rest in pairs through the fleet build deployer, and restarts
// each rolled bench's beat.
func (r *Release) roll(ctx context.Context, res *ReleaseResult) error {
	if r.Client == nil {
		return refused("the roll needs the fleet store (--redis <addr>)")
	}
	benches := r.rollList()
	if len(benches) == 0 {
		return refused("no benches to roll: --benches <a,b,...>, or a machines registry with bench roles (--machines <file>, or %s)", MachinesEnv)
	}
	if _, err := SetFields(ctx, r.Client, []string{"version=" + res.Version, "commit=" + res.Commit}); err != nil {
		return err
	}
	r.printf("FLEET RELEASE SET version=%s commit=%s\n", res.Version, res.Commit)
	facts, err := ReadFacts(ctx, r.Client)
	if err != nil {
		return err
	}
	facts.Machines = r.Machines
	conv := Converge(facts)
	if len(conv) > 0 {
		if err := WriteConverged(ctx, r.Client, conv); err != nil {
			return err
		}
		r.printf("CONVERGED %s\n", Fields(conv))
	}
	cfg, err := ReadConfig(ctx, r.Client)
	if err != nil {
		return err
	}
	var todo []string
	for _, b := range benches {
		if line, ok := facts.Beat[b]; ok && versionOf(line) == res.Version {
			r.printf("SKIPPED %s %s\n", b, res.Version)
			res.Skipped = append(res.Skipped, b)
			continue
		}
		todo = append(todo, b)
	}
	for i := 0; i < len(todo); i += RollPair {
		pair := todo[i:min(i+RollPair, len(todo))]
		plan, err := MakePlan(cfg, pair)
		if err != nil {
			return err
		}
		d := &Deployer{Client: r.Client, Runner: argvRunner{r.Runner}, Home: r.Home, BuildCmd: r.BuildCmd, Redis: r.Redis, Out: r.Out}
		dr, err := d.Deploy(ctx, plan)
		if err != nil {
			return err
		}
		if !dr.Built {
			return refused("the build of %s on %s failed; read the BUILD FAIL line", plan.Version, plan.Builder)
		}
		for _, l := range dr.Lines {
			if l.Status != "OK" {
				return refused("bench %s %s: %s (the benches before it are rolled; rerun for the rest)", l.Bench, l.Status, l.Detail)
			}
			goos, _, _ := strings.Cut(l.Platform, "-")
			argv, err := RestartArgv(goos, "system", BeatUnit, true)
			if err != nil {
				return err
			}
			ssh := append([]string{"ssh", "-n", "-o", "BatchMode=yes", "-o", "ConnectTimeout=6", r.hostOf(l.Bench)}, argv...)
			if out, err := r.run(ctx, "", nil, ssh...); err != nil {
				return refused("beat restart on %s: %v: %s (the binary is installed; restart the beat by hand, then rerun)", l.Bench, err, lastLine(out))
			}
			r.printf("BEAT %s restarted\n", l.Bench)
			r.printf("ROLLED %s %s\n", l.Bench, res.Version)
			res.Rolled = append(res.Rolled, l.Bench)
		}
	}
	return nil
}
