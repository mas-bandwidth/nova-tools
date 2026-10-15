package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/gitrun"
	"github.com/mas-bandwidth/nova-tools/pkg/oneline"
	"github.com/mas-bandwidth/nova-tools/pkg/safepath"
	"github.com/mas-bandwidth/nova-tools/pkg/swarm"
)

// THE MIRROR (docs/COORDINATOR-TOOLS.md, the mirror-refresh row; docs/FLEET.md).
//
// `nova-swarm mirror` keeps the bench's bare mirrors fresh, so a card's `git clone
// --reference` finds every repository it may borrow from: for each --repos name it clones
// <base>/<name>.git into <dir>/<name>.git when the mirror is absent, then fetches every
// head and every pull-request head into it. It replaces the coordinator's bash
// mirror-refresh loop. Never git prune and never a forced removal: the disk guard owns
// what a mirror leaves behind (diskguard.go), and a clone borrowing a mirror reads its
// objects.

// mirrorRefspecs are the refs a mirror holds: every branch and every pull request's head.
var mirrorRefspecs = []string{"+refs/heads/*:refs/heads/*", "+refs/pull/*/head:refs/pull/*/head"}

// mirrorNameRe is a repository's name: one path element, never a path.
var mirrorNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// mirrorGit is one git run for the mirror verb, a seam for its tests: dir is the
// directory it runs in, "" for none.
type mirrorGit func(ctx context.Context, dir string, args ...string) error

func realMirrorGit(ctx context.Context, dir string, args ...string) error {
	_, err := gitrun.Combined(ctx, gitrun.Options{Dir: dir, Env: append(os.Environ(), "GIT_TERMINAL_PROMPT=0")}, args...)
	return err
}

// mirrorRun is one mirror pass's inputs. benches are the fleet rows to converge; empty is
// this bench's own mirror directory, the flat layout. check makes the pass the
// converge-check: it names the missing mirrors and writes none. links makes the pass point
// <linkDir>/<repo>-mirror.git at this bench's own bare mirror, the link
// swarm.FindBenchMirror reads (stage.go); a converge-check and a run with --no-links write
// no link.
type mirrorRun struct {
	dir, base string
	repos     []string
	benches   []string
	check     bool
	links     bool
	linkDir   string
	git       mirrorGit
	out       io.Writer
}

// pass is one converged mirror pass. In check mode it prints one MIRROR MISSING <bench>
// <repo> per mirror the fleet lacks and returns how many: that is converge-check, and it
// runs no git. Otherwise it creates every missing mirror and fetches every one, one MIRROR
// OK or MIRROR FAILED line each, returning how many failed: one mirror's failure never
// stops the others, so a pass converges every bench it can (docs/FLEET.md, the mirrors'
// refresh row).
func (m *mirrorRun) pass(ctx context.Context) (failed int) {
	benches := m.benches
	if len(benches) == 0 {
		benches = []string{""}
	}
	repos := swarm.MirrorSetRepos(m.repos)
	if m.check {
		faults := MirrorFaults(benches, m.has(), repos)
		for _, f := range faults {
			fmt.Fprintln(m.out, oneline.Escape(f))
		}
		return len(faults)
	}
	for _, b := range benches {
		for _, name := range repos {
			if err := m.refresh(ctx, b, name); err != nil {
				failed++
				fmt.Fprintf(m.out, "MIRROR FAILED %s: %s\n", oneline.Field(mirrorLabel(b, name)), oneline.Err(err))
				continue
			}
			fmt.Fprintf(m.out, "MIRROR OK %s\n", oneline.Field(mirrorLabel(b, name)))
		}
	}
	return failed
}

// has reports whether a bench already carries a repository's mirror, the reading
// converge-check names a fault from.
func (m *mirrorRun) has() func(bench, repo string) bool {
	return func(bench, repo string) bool {
		_, err := os.Stat(mirrorBare(m.dir, bench, repo))
		return err == nil
	}
}

// refresh makes bench's mirror of name as one converged pass would: a bare mirror that is
// absent is initialised first, then every head and pull-request head is fetched into it.
func (m *mirrorRun) refresh(ctx context.Context, bench, name string) error {
	bare := mirrorBare(m.dir, bench, name)
	url := strings.TrimRight(m.base, "/") + "/" + name + ".git"
	if _, err := os.Stat(bare); os.IsNotExist(err) {
		if err := os.MkdirAll(filepath.Dir(bare), 0o755); err != nil {
			return err
		}
		if err := m.git(ctx, "", "init", "--bare", "--quiet", "--", bare); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	if err := m.git(ctx, bare, append([]string{"fetch", "--quiet", "--no-tags", "--prune", "--", url}, mirrorRefspecs...)...); err != nil {
		return err
	}
	// The flat layout is this bench's own mirror, the one FindBenchMirror looks for
	// under /tmp; a bench row in <dir>/<bench>/ has no link (the bench's own home names
	// it). A link failure is the pass's failure: a stage that cannot find the mirror is
	// exactly what the link prevents.
	if m.links && bench == "" {
		return linkMirror(m.linkDir, m.dir, bare, name)
	}
	return nil
}

// linkMirror points <linkDir>/<name>-mirror.git at bare, this bench's own bare mirror, so
// a stage's FindBenchMirror finds what a keeper made (stage.go reads
// /tmp/<repo>-mirror.git after its bench-home candidates). The repository name is one path
// element and bare must resolve strictly under root, both through pkg/safepath rather than
// by a raw join. An existing link is replaced.
func linkMirror(linkDir, root, bare, name string) error {
	if !safepath.NameOK(name) {
		return fmt.Errorf("mirror link: %q is no repository name: one name, never a path", name)
	}
	if strings.TrimSpace(linkDir) == "" || strings.TrimSpace(root) == "" {
		return errors.New("mirror link: the link directory and the mirror directory are wanted")
	}
	target, err := safepath.ResolvedUnder(bare, root)
	if err != nil {
		return fmt.Errorf("mirror link %s: %w", name, err)
	}
	// A relative --dir resolves the target against the process's directory, but a
	// symlink's target is read against the link's own directory: name it absolutely.
	if target, err = filepath.Abs(target); err != nil {
		return fmt.Errorf("mirror link %s: the target does not resolve: %w", name, err)
	}
	link := filepath.Join(linkDir, name+"-mirror.git")
	if _, err := os.Lstat(link); err == nil {
		if err := os.Remove(link); err != nil {
			return fmt.Errorf("mirror link %s: the link %s could not be replaced: %w", name, link, err)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("mirror link %s: %s could not be read: %w", name, link, err)
	}
	if err := os.MkdirAll(linkDir, 0o755); err != nil {
		return fmt.Errorf("mirror link %s: the link directory could not be made: %w", name, err)
	}
	if err := os.Symlink(target, link); err != nil {
		return fmt.Errorf("mirror link %s: %w", name, err)
	}
	return nil
}

// THE CONVERGED MIRROR SET (docs/FLEET.md, the mirrors' refresh row; docs/SPEC-SWARM.md,
// the warm clones and caches card). The mirror set is a converged property of the fleet:
// every bench carries every repository a card can name. The repositories come from
// swarm.MirrorSetRepos (the running sprint's stream repos, --repos, unioned with the fixed
// set every bench carries); the benches are the fleet rows, --benches. A bench without one
// mirror is a fault `nova-swarm mirror --check` names; a refresh pass makes it.

// MirrorFaults reports each bench missing one of repos as a fault naming the bench and the
// mirror it lacks: "MIRROR MISSING <bench> <repo>". It is the check side of the converged
// set: --check prints these and writes nothing (docs/FLEET.md, the mirrors' refresh row).
func MirrorFaults(benches []string, has func(bench, repo string) bool, repos []string) []string {
	var faults []string
	for _, b := range benches {
		for _, r := range repos {
			if !has(b, r) {
				faults = append(faults, fmt.Sprintf("MIRROR MISSING %s %s", oneline.Field(b), oneline.Field(r)))
			}
		}
	}
	return faults
}

// mirrorBare is the bare mirror of repo name for bench: <dir>/<bench>/<name>.git, or
// <dir>/<name>.git for the bench's own mirror (bench ""), the flat layout a bench that
// mirrors for itself keeps (swarm.MirrorPath).
func mirrorBare(dir, bench, name string) string {
	if bench == "" {
		return filepath.Join(dir, name+".git")
	}
	return filepath.Join(dir, bench, name+".git")
}

// mirrorLabel is a mirror's name on a MIRROR OK or MIRROR FAILED line: <bench>/<repo>, or
// the bare <repo> for the bench's own flat mirror.
func mirrorLabel(bench, name string) string {
	if bench == "" {
		return name
	}
	return bench + "/" + name
}

// loop runs pass, then waits every between passes with wait until ctx ends; every of 0
// is one pass. wait returns false when it was cut short by ctx.
func (m *mirrorRun) loop(ctx context.Context, every time.Duration, wait func(context.Context, time.Duration) bool) int {
	failed := m.pass(ctx)
	for every > 0 && wait(ctx, every) {
		failed = m.pass(ctx)
	}
	if failed > 0 {
		return 1
	}
	return 0
}

func waitFor(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// mirrorLinkDir is where a bench's own flat mirror gets its FindBenchMirror link,
// /tmp/<repo>-mirror.git. A test passes its own directory to runMirrorIn, so no test
// writes the shared /tmp.
const mirrorLinkDir = "/tmp"

// cmdMirror is `nova-swarm mirror`.
func cmdMirror(args []string, stdout, stderr io.Writer) int {
	return runMirror(args, stdout, stderr, realMirrorGit, waitFor)
}

func runMirror(args []string, stdout, stderr io.Writer, git mirrorGit, wait func(context.Context, time.Duration) bool) int {
	return runMirrorIn(args, stdout, stderr, git, wait, mirrorLinkDir)
}

// runMirrorIn is runMirror with the link directory named: production passes /tmp, a test
// passes its own t.TempDir().
func runMirrorIn(args []string, stdout, stderr io.Writer, git mirrorGit, wait func(context.Context, time.Duration) bool, linkDir string) int {
	f := newFlags("mirror")
	dir := f.fs.String("dir", "~/nova-bench/mirror", "the `dir` the bare mirrors live in: <dir>/<bench>/<repo>.git, or <dir>/<repo>.git for this bench's own mirror (default ~/nova-bench/mirror)")
	repos := f.fs.String("repos", "", "the running sprint's stream repository names to mirror, a comma-separated `list`; the fixed set every bench carries is added to them")
	base := f.fs.String("base", "", "the `url` each repository is fetched from, <url>/<name>.git, such as https://github.com/<org> or git@<alias>:<org>")
	benches := f.fs.String("benches", "", "the fleet rows (bench names) every mirror is converged to, a comma-separated `list`; empty is this bench's own flat mirror dir")
	check := f.fs.Bool("check", false, "the converge-check: print one MIRROR MISSING <bench> <repo> per mirror the fleet lacks, write nothing, exit 1 when any is missing")
	noLinks := f.fs.Bool("no-links", false, "skip the /tmp/<repo>-mirror.git link this bench's own flat mirror gets, so the pass writes nothing outside --dir")
	every := f.fs.Duration("every", 0, "refresh again after this `duration`, until stopped; 0 refreshes once (default 0)")
	if !f.parse(args, stderr) {
		return 2
	}
	f.want(*base, "base", "the url the repositories live under, <url>/<name>.git")
	f.want(*repos, "repos", "the running sprint's stream repository names to mirror, a,b")
	names := mirrorNames(f, *repos, "repos", "repository")
	benchNames := mirrorNames(f, *benches, "benches", "bench")
	// Separators alone parse to no repository: refuse before --dir is made, so a
	// comma-only list leaves no empty mirror directory behind.
	if len(names) == 0 && strings.TrimSpace(*repos) != "" {
		f.add("--repos names no repository, got " + oneline.Field(*repos) + ": it wants the repository names to mirror, a,b")
	}
	if *check && *every != 0 {
		f.add("--check is the converge-check, one pass that writes nothing, so it takes no --every: run the refresh without --check to repeat")
	}
	if *every < 0 {
		f.add("--every is 0 or a positive duration, got " + oneline.Field(every.String()))
	}
	if f.refused(stderr) {
		return 2
	}
	d := *dir
	if rest, ok := strings.CutPrefix(d, "~/"); ok {
		home, err := os.UserHomeDir()
		if err != nil {
			return refuse(stderr, " mirror", "the user's home could not be read: "+err.Error())
		}
		d = filepath.Join(home, rest)
	}
	// A check reads the fleet's mirrors and writes nothing, so it makes no directory:
	// a missing --dir is every mirror missing, which is exactly the fault it names.
	if !*check {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return refuse(stderr, " mirror", "--dir could not be made: "+oneline.Err(err))
		}
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	m := &mirrorRun{dir: d, base: *base, repos: names, benches: benchNames, check: *check, links: !*noLinks, linkDir: linkDir, git: git, out: stdout}
	return m.loop(ctx, *every, wait)
}

// mirrorNames parses one comma-separated name list, recording at flag what each malformed
// entry is: a repository or bench name is one path element, never a path.
func mirrorNames(f *flags, list, flag, noun string) []string {
	var names []string
	for _, n := range strings.Split(list, ",") {
		if n = strings.TrimSpace(n); n == "" {
			continue
		}
		if !mirrorNameRe.MatchString(n) || strings.Contains(n, "..") {
			f.add("--" + flag + " names " + oneline.Field(n) + ", which is no " + noun + " name: one name, never a path")
			continue
		}
		names = append(names, n)
	}
	return names
}
