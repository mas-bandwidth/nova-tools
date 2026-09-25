// Package fleetbuild is `nova-sprint fleet build` (#3310): one release, built
// once on the builder, installed on every bench and on this machine, from the
// fleet Redis rather than from a script's hardcoded lists. The build itself is
// Compile (compile.go), run on the builder with its own Go caches (#4080).
//
// Redis holds the whole plan:
//
//	fleet:release   hash: version (v<x>.<y>.<z>-dev.<sha8>), commit (the full
//	                sha of <sha8>), builder (the bench that builds), self (this
//	                machine's bench name), tools (the nova-* list the release
//	                manifest named on the last run), platform:<bench>
//	                (<goos>-<goarch>) for every bench, self included
//	benches         set: the benches to install on
//
// and one pipeline reads it. Since #4050 nothing in it is typed: land merge
// writes version and commit on every landing into dev (the stream package),
// convergence fills builder, self and platform:<bench> from the bench registry
// and the machines registry (converge.go), and the tools are the release
// manifest's. The run: the builder builds the release for every platform
// named (one build command, which skips a platform already built); the
// manifest names the tools; each bench rsyncs its platform's tools from the builder into a staging
// directory and renames them into its bin directory, then prints its
// nova-sprint version line; this machine does the same locally. A bench whose
// version line names the release gets its receipt, one pipeline of
// `HSET bench:<b> build <V> build_sha <sha40> build_at <utc>`; a bench that
// fails or answers another version keeps its old receipt. Last, the freshly
// installed nova-sprint here runs `fn deploy`, so the store's function library
// is the one this release carries.
package fleetbuild

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	// ConfigKey is the release hash; BenchesKey the bench set.
	ConfigKey  = "fleet:release"
	BenchesKey = "benches"
	// ReleaseRoot is where the builder keeps <V>/<platform>/, relative to its home.
	ReleaseRoot = "nova-bench/release"
	// BinDir and StageDir are relative to each machine's home.
	BinDir   = ".local/bin"
	StageDir = ".local/bin.new"
	// LegacyBuildCmd is rowan-tools' space-build script, the build before
	// #4080: run here as `<cmd> --host <builder> --version <v> --commit <sha>
	// --platform <list>` when --build-cmd names it (or any other command). With
	// no build command the build is CompileArgv: this package's Compile, run on
	// the builder by its own nova-sprint, with the build's own Go caches.
	LegacyBuildCmd = "space-build"
	// ReleaseRepo and ReleaseBase are where a landing names a release: land
	// merge of nova-tools into dev writes version and commit (#4050).
	ReleaseRepo = "nova-tools"
	ReleaseBase = "dev"
	// DefaultTrain is the v<x>.<y>.<z> of a landing's version when
	// fleet:release holds none yet; after that the stored version's train
	// carries (fleet build set version=... moves it).
	DefaultTrain = "v0.16.0"
	// BenchTimeout bounds one bench's install; BuildTimeout the build.
	BenchTimeout = 80 * time.Second
	BuildTimeout = 20 * time.Minute
)

// DefaultTools are the tools a dry run names when fleet:release holds no
// manifest list yet; a real run installs what the manifest lists.
var DefaultTools = []string{"nova-sprint", "nova-swarm", "nova-card", "nova-wake"}

var (
	versionRe  = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+-dev\.([0-9a-f]{8})$`)
	commitRe   = regexp.MustCompile(`^[0-9a-f]{40}$`)
	nameRe     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
	toolRe     = regexp.MustCompile(`^nova-[a-z0-9-]+$`)
	platformRe = regexp.MustCompile(`^(linux|darwin)-(amd64|arm64)$`)
)

// ErrRefused is a plan the config cannot support; the message names the remedy.
var ErrRefused = errors.New("refused")

func refused(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrRefused, fmt.Sprintf(format, a...))
}

// Config is fleet:release plus the bench set, as read.
type Config struct {
	Version, Commit, Builder, Self string
	Tools                          []string
	Platforms                      map[string]string
	Benches                        []string
}

// ReadConfig reads fleet:release and benches in one pipeline.
func ReadConfig(ctx context.Context, c *redis.Client) (Config, error) {
	pipe := c.Pipeline()
	h := pipe.HGetAll(ctx, ConfigKey)
	m := pipe.SMembers(ctx, BenchesKey)
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return Config{}, err
	}
	cfg := Config{Platforms: map[string]string{}}
	for k, v := range h.Val() {
		switch {
		case k == "version":
			cfg.Version = v
		case k == "commit":
			cfg.Commit = v
		case k == "builder":
			cfg.Builder = v
		case k == "self":
			cfg.Self = v
		case k == "tools":
			for _, t := range strings.Split(v, ",") {
				if t = strings.TrimSpace(t); t != "" {
					cfg.Tools = append(cfg.Tools, t)
				}
			}
		case strings.HasPrefix(k, "platform:"):
			cfg.Platforms[strings.TrimPrefix(k, "platform:")] = v
		}
	}
	cfg.Benches = m.Val()
	sort.Strings(cfg.Benches)
	return cfg, nil
}

// SetFields validates key=value pairs for fleet:release and writes them in one HSET.
func SetFields(ctx context.Context, c *redis.Client, pairs []string) (int, error) {
	if len(pairs) == 0 {
		return 0, refused("name at least one key=value (version, commit, builder, self, tools, platform:<bench>)")
	}
	var args []any
	for _, p := range pairs {
		k, v, ok := strings.Cut(p, "=")
		if !ok || v == "" {
			return 0, refused("%q is not key=value", p)
		}
		var bad bool
		switch {
		case k == "version":
			bad = !versionRe.MatchString(v)
		case k == "commit":
			bad = !commitRe.MatchString(v)
		case k == "builder" || k == "self":
			bad = !nameRe.MatchString(v)
		case k == "tools":
			for _, t := range strings.Split(v, ",") {
				bad = bad || !toolRe.MatchString(t)
			}
		case strings.HasPrefix(k, "platform:"):
			bad = !nameRe.MatchString(strings.TrimPrefix(k, "platform:")) || !platformRe.MatchString(v)
		default:
			return 0, refused("unknown key %q; want version, commit, builder, self, tools or platform:<bench>", k)
		}
		if bad {
			return 0, refused("%s=%q is not a valid value", k, v)
		}
		args = append(args, k, v)
	}
	return len(pairs), c.HSet(ctx, ConfigKey, args...).Err()
}

// Target is one machine to install on.
type Target struct {
	Bench, Platform string
	Local           bool // this machine (Config.Self)
}

// Plan is a validated Config narrowed to the targets of one run.
type Plan struct {
	Config
	Targets        []Target
	BuildPlatforms []string // distinct platforms to build, sorted
}

// MakePlan validates cfg and picks the targets: every bench plus self, or only
// the names in only (each a bench or self). It refuses before anything runs.
func MakePlan(cfg Config, only []string) (Plan, error) {
	sm := versionRe.FindStringSubmatch(cfg.Version)
	switch {
	case sm == nil:
		return Plan{}, refused("fleet:release version %q is not v<x>.<y>.<z>-dev.<sha8> (HSET fleet:release version <v>)", cfg.Version)
	case !commitRe.MatchString(cfg.Commit):
		return Plan{}, refused("fleet:release commit %q is not a full 40-hex sha (HSET fleet:release commit <sha40>)", cfg.Commit)
	case !strings.HasPrefix(cfg.Commit, sm[1]):
		return Plan{}, refused("commit %s is not the version's sha %s (set both from one dev commit)", cfg.Commit, sm[1])
	case !nameRe.MatchString(cfg.Builder):
		return Plan{}, refused("fleet:release builder %q is not a bench name (HSET fleet:release builder <bench>)", cfg.Builder)
	case !nameRe.MatchString(cfg.Self):
		return Plan{}, refused("fleet:release self %q is not a bench name (HSET fleet:release self <this machine>)", cfg.Self)
	}
	if len(cfg.Tools) == 0 {
		cfg.Tools = DefaultTools
	}
	hasSprint := false
	for _, t := range cfg.Tools {
		if !toolRe.MatchString(t) {
			return Plan{}, refused("tool %q is not nova-<name>", t)
		}
		hasSprint = hasSprint || t == "nova-sprint"
	}
	if !hasSprint {
		return Plan{}, refused("tools %s leave out nova-sprint, which proves each install and runs fn deploy", strings.Join(cfg.Tools, ","))
	}
	member := map[string]bool{cfg.Self: true}
	for _, b := range cfg.Benches {
		member[b] = true
	}
	names := append([]string{}, only...)
	if len(names) == 0 {
		names = append(append(names, cfg.Benches...), cfg.Self)
	}
	seen := map[string]bool{}
	p := Plan{Config: cfg}
	plats := map[string]bool{}
	for _, b := range names {
		if seen[b] {
			continue
		}
		seen[b] = true
		if !nameRe.MatchString(b) {
			return Plan{}, refused("bench %q is not a bench name", b)
		}
		if !member[b] {
			return Plan{}, refused("%s is neither in the benches set nor fleet:release self", b)
		}
		plat := cfg.Platforms[b]
		if !platformRe.MatchString(plat) {
			return Plan{}, refused("bench %s has platform %q (HSET fleet:release platform:%s <goos>-<goarch>)", b, plat, b)
		}
		plats[plat] = true
		p.Targets = append(p.Targets, Target{Bench: b, Platform: plat, Local: b == cfg.Self})
	}
	sort.Slice(p.Targets, func(i, j int) bool { return p.Targets[i].Bench < p.Targets[j].Bench })
	for pl := range plats {
		p.BuildPlatforms = append(p.BuildPlatforms, pl)
	}
	sort.Strings(p.BuildPlatforms)
	return p, nil
}

// Runner starts one child and returns its combined output. Production runs the
// argv; tests fake it, so no test reaches a host.
type Runner interface {
	Run(ctx context.Context, argv []string) (string, error)
}

// Deployer runs a Plan.
type Deployer struct {
	Client   *redis.Client
	Runner   Runner
	Home     string // this machine's home: <Home>/.local/bin
	BuildCmd string
	Redis    string // the address fn deploy is given
	Now      func() time.Time
	Out      io.Writer
}

// BuildArgv is the one build command: CompileArgv, or BuildCmd with
// space-build's argv when one is named.
func (d *Deployer) BuildArgv(p Plan) []string {
	if d.BuildCmd == "" {
		return CompileArgv(p)
	}
	return []string{d.BuildCmd, "--host", p.Builder, "--version", p.Version, "--commit", p.Commit,
		"--platform", strings.Join(p.BuildPlatforms, ",")}
}

// CompileArgv runs `nova-sprint fleet build compile` on the builder in one ssh
// session, with the nova-sprint the last fleet build installed there. Every
// value is validated by MakePlan, so the remote command line holds no shell
// metacharacter; the remote shell starts in the builder's home.
func CompileArgv(p Plan) []string {
	return []string{"ssh", "-n", "-o", "BatchMode=yes", "-o", "ConnectTimeout=15", p.Builder,
		BinDir + "/nova-sprint", "fleet", "build", "compile", "--version", p.Version, "--commit", p.Commit,
		"--platform", strings.Join(p.BuildPlatforms, ",")}
}

// Source is where target t rsyncs from: a local path on the builder itself,
// builder:<path> anywhere else.
func Source(p Plan, t Target) string {
	dir := ReleaseRoot + "/" + p.Version + "/" + t.Platform + "/"
	if t.Bench == p.Builder {
		return dir
	}
	return p.Builder + ":" + dir
}

func toolFilter(tools []string) []string {
	var f []string
	for _, t := range tools {
		f = append(f, "--include="+t)
	}
	return append(f, "--exclude=*")
}

// RemoteArgv installs on a remote bench in one ssh session. Every value in the
// script is validated by MakePlan (names, tools, version, platform), so it holds
// no quote or shell metacharacter; the script runs under bash whatever the
// bench user's login shell is.
func RemoteArgv(p Plan, t Target) []string {
	var filt []string
	for _, f := range toolFilter(p.Tools) {
		if f == "--exclude=*" {
			f = `--exclude="*"`
		}
		filt = append(filt, f)
	}
	script := "set -e; mkdir -p ~/" + StageDir +
		"; rsync -a --checksum " + strings.Join(filt, " ") + " " + Source(p, t) + " ~/" + StageDir + "/" +
		"; for t in " + strings.Join(p.Tools, " ") + "; do mv -f ~/" + StageDir + "/$t ~/" + BinDir + "/$t; done" +
		"; ~/" + BinDir + "/nova-sprint version | head -1"
	return []string{"ssh", "-n", "-o", "BatchMode=yes", "-o", "ConnectTimeout=10", t.Bench, "bash -c '" + script + "'"}
}

// Line is one target's outcome.
type Line struct {
	Bench, Platform, Status, Detail string // Status OK | MISMATCH | FAIL
}

// Result is the run's outcome.
type Result struct {
	Built  bool
	Tools  []string // the release manifest's tools, once built
	Lines  []Line
	FnLine string
	FnOK   bool
}

// OK is true when every target installed the release and fn deploy answered.
func (r Result) OK() bool {
	if !r.Built || !r.FnOK {
		return false
	}
	for _, l := range r.Lines {
		if l.Status != "OK" {
			return false
		}
	}
	return true
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if l := strings.TrimSpace(lines[i]); l != "" {
			return l
		}
	}
	return ""
}

// probe judges a nova-sprint version line against the release.
func probe(p Plan, t Target, out string, err error) Line {
	l := Line{Bench: t.Bench, Platform: t.Platform}
	last := lastLine(out)
	switch f := strings.Fields(last); {
	case err != nil:
		l.Status, l.Detail = "FAIL", fmt.Sprintf("%v: %s", err, last)
	case len(f) >= 2 && f[0] == "nova-sprint" && f[1] == p.Version:
		l.Status, l.Detail = "OK", last
	default:
		l.Status, l.Detail = "MISMATCH", last
	}
	return l
}

func (d *Deployer) local(ctx context.Context, p Plan, t Target) Line {
	stage := filepath.Join(d.Home, StageDir)
	bin := filepath.Join(d.Home, BinDir)
	fail := func(err error) Line {
		return Line{Bench: t.Bench, Platform: t.Platform, Status: "FAIL", Detail: err.Error()}
	}
	if err := os.MkdirAll(stage, 0o755); err != nil {
		return fail(err)
	}
	if err := os.MkdirAll(bin, 0o755); err != nil {
		return fail(err)
	}
	src := Source(p, t)
	if t.Bench == p.Builder {
		src = filepath.Join(d.Home, src) + "/"
	}
	argv := append(append([]string{"rsync", "-a", "--checksum"}, toolFilter(p.Tools)...), src, stage+"/")
	if out, err := d.Runner.Run(ctx, argv); err != nil {
		return fail(fmt.Errorf("rsync: %v: %s", err, lastLine(out)))
	}
	for _, tool := range p.Tools {
		if err := os.Rename(filepath.Join(stage, tool), filepath.Join(bin, tool)); err != nil {
			return fail(err)
		}
	}
	out, err := d.Runner.Run(ctx, []string{filepath.Join(bin, "nova-sprint"), "version"})
	return probe(p, t, out, err)
}

func (d *Deployer) printf(format string, a ...any) {
	if d.Out != nil {
		fmt.Fprintf(d.Out, format, a...)
	}
}

// Deploy builds, installs on every target at once, writes the receipts, and
// runs fn deploy with this machine's new nova-sprint.
func (d *Deployer) Deploy(ctx context.Context, p Plan) (Result, error) {
	var r Result
	bctx, cancel := context.WithTimeout(ctx, BuildTimeout)
	out, err := d.Runner.Run(bctx, d.BuildArgv(p))
	cancel()
	if err != nil {
		d.printf("BUILD FAIL builder=%s version=%s: %v: %s\n", p.Builder, p.Version, err, lastLine(out))
		return r, nil
	}
	d.printf("BUILD OK builder=%s version=%s platforms=%s: %s\n", p.Builder, p.Version, strings.Join(p.BuildPlatforms, ","), lastLine(out))
	// The tools are the release manifest's (#4050): every nova-* the build
	// produced, never a typed list, recorded on fleet:release.
	tools, err := d.Manifest(ctx, p)
	if err != nil {
		d.printf("MANIFEST FAIL builder=%s version=%s: %v\n", p.Builder, p.Version, err)
		return r, nil
	}
	p.Tools = tools
	if err := d.Client.HSet(ctx, ConfigKey, "tools", strings.Join(tools, ",")).Err(); err != nil {
		return r, fmt.Errorf("record tools: %w", err)
	}
	r.Built, r.Tools = true, tools
	d.printf("MANIFEST tools=%d %s\n", len(tools), strings.Join(tools, ","))

	r.Lines = make([]Line, len(p.Targets))
	var wg sync.WaitGroup
	for i, t := range p.Targets {
		wg.Add(1)
		go func(i int, t Target) {
			defer wg.Done()
			tctx, cancel := context.WithTimeout(ctx, BenchTimeout)
			defer cancel()
			if t.Local {
				r.Lines[i] = d.local(tctx, p, t)
				return
			}
			out, err := d.Runner.Run(tctx, RemoteArgv(p, t))
			r.Lines[i] = probe(p, t, out, err)
		}(i, t)
	}
	wg.Wait()

	now := time.Now
	if d.Now != nil {
		now = d.Now
	}
	at := now().UTC().Format(time.RFC3339)
	pipe := d.Client.Pipeline()
	n := 0
	selfOK := false
	for _, l := range r.Lines {
		d.printf("%s %s platform=%s: %s\n", l.Status, l.Bench, l.Platform, l.Detail)
		if l.Status != "OK" {
			continue
		}
		pipe.HSet(ctx, "bench:"+l.Bench, "build", p.Version, "build_sha", p.Commit, "build_at", at)
		n++
		selfOK = selfOK || l.Bench == p.Self
	}
	if n > 0 {
		if _, err := pipe.Exec(ctx); err != nil {
			return r, fmt.Errorf("write receipts: %w", err)
		}
	}
	if !selfOK {
		selfTarget := false
		for _, t := range p.Targets {
			selfTarget = selfTarget || t.Local
		}
		r.FnLine = "FN SKIPPED: " + p.Self + " did not install " + p.Version
		if !selfTarget {
			// A run on other benches only: this machine's library is not
			// this run's to change, and skipping it is not a failure.
			r.FnLine, r.FnOK = "FN SKIPPED: "+p.Self+" is not a target", true
		}
		d.printf("%s\n", r.FnLine)
		return r, nil
	}
	out, err = d.Runner.Run(ctx, []string{filepath.Join(d.Home, BinDir, "nova-sprint"), "fn", "deploy", "--redis", d.Redis})
	r.FnLine = lastLine(out)
	r.FnOK = err == nil
	if err != nil {
		r.FnLine = fmt.Sprintf("FN FAIL: %v: %s", err, r.FnLine)
	}
	d.printf("%s\n", r.FnLine)
	return r, nil
}

// ManifestName is the release manifest in each <V>/<platform>/ directory the
// build publishes: one "<sha256>  <file>" line per file it produced.
const ManifestName = "SHA256SUMS"

// ParseManifest is the tools a manifest lists: every file named nova-<name>,
// sorted. It refuses a manifest without nova-sprint, which proves each
// install and runs fn deploy.
func ParseManifest(s string) ([]string, error) {
	seen := map[string]bool{}
	var tools []string
	for _, line := range strings.Split(s, "\n") {
		f := strings.Fields(line)
		if len(f) != 2 || len(f[0]) != 64 {
			continue
		}
		name := strings.TrimPrefix(f[1], "*")
		if toolRe.MatchString(name) && !seen[name] {
			seen[name] = true
			tools = append(tools, name)
		}
	}
	sort.Strings(tools)
	if !seen["nova-sprint"] {
		return nil, refused("the manifest lists %d tools and not nova-sprint", len(tools))
	}
	return tools, nil
}

// Manifest reads the release manifest of the plan's first build platform
// from the builder (a local read when the builder is this machine): the
// command set is one per commit, so one platform's list is every platform's.
func (d *Deployer) Manifest(ctx context.Context, p Plan) ([]string, error) {
	if len(p.BuildPlatforms) == 0 {
		return nil, refused("no build platform")
	}
	rel := ReleaseRoot + "/" + p.Version + "/" + p.BuildPlatforms[0] + "/" + ManifestName
	var body string
	if p.Builder == p.Self {
		b, err := os.ReadFile(filepath.Join(d.Home, rel))
		if err != nil {
			return nil, err
		}
		body = string(b)
	} else {
		out, err := d.Runner.Run(ctx, []string{"ssh", "-n", "-o", "BatchMode=yes", "-o", "ConnectTimeout=10", p.Builder, "cat " + rel})
		if err != nil {
			return nil, fmt.Errorf("%v: %s", err, lastLine(out))
		}
		body = out
	}
	return ParseManifest(body)
}
