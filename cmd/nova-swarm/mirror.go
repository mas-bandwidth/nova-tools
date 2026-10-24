package main

import (
	"context"
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
// mirror-refresh loop. Never git prune: the disk guard owns what a mirror leaves behind
// (diskguard.go), and a clone borrowing a mirror reads its objects. The one removal is of a
// damaged mirror, after a re-clone of it has passed its checks and taken its place
// (reclone).

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
// converge-check: it names the missing mirrors and writes none.
type mirrorRun struct {
	dir, base string
	repos     []string
	benches   []string
	check     bool
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
			healed, err := m.refresh(ctx, b, name)
			if healed != "" && err == nil {
				fmt.Fprintf(m.out, "MIRROR RECLONED %s: %s\n", oneline.Field(mirrorLabel(b, name)), oneline.Escape(healed))
			}
			if err != nil {
				failed++
				if healed != "" {
					err = fmt.Errorf("the re-clone after %s failed, the old mirror kept: %w", healed, err)
				}
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
// A mirror that is present is first read for damage (mirrorEmptyObjects), and one whose fetch
// fails is checked with git fsck --connectivity-only; a damaged one is re-cloned
// (reclone), and healed says why ("" when nothing was re-cloned). On 2026-10-24 two benches'
// mirrors held 14-23 empty object files, a write cut short under disk pressure, and every
// staging clone from them failed "object file ... is empty", refusing ten cards: a fetch
// alone never mends a mirror, because the objects it already holds are never sent again.
func (m *mirrorRun) refresh(ctx context.Context, bench, name string) (healed string, err error) {
	bare := mirrorBare(m.dir, bench, name)
	url := strings.TrimRight(m.base, "/") + "/" + name + ".git"
	if _, err := os.Stat(bare); os.IsNotExist(err) {
		if err := os.MkdirAll(filepath.Dir(bare), 0o755); err != nil {
			return "", err
		}
		if err := m.git(ctx, "", "init", "--bare", "--quiet", "--", bare); err != nil {
			return "", err
		}
		return "", m.fetch(ctx, bare, url)
	} else if err != nil {
		return "", err
	}
	if n := mirrorEmptyObjects(bare); n > 0 {
		healed = fmt.Sprintf("%d empty object file(s)", n)
		return healed, m.reclone(ctx, bare, url)
	}
	ferr := m.fetch(ctx, bare, url)
	if ferr == nil {
		return "", nil
	}
	if cerr := m.git(ctx, bare, mirrorFsck...); cerr != nil {
		healed = "the fetch failed (" + oneline.Err(ferr) + ") and git fsck --connectivity-only failed (" + oneline.Err(cerr) + ")"
		return healed, m.reclone(ctx, bare, url)
	}
	return "", ferr
}

// mirrorFsck is the check a mirror must pass: every ref reaches whole history, read without
// opening every blob (the cheap check; a full fsck of a mirror is minutes).
var mirrorFsck = []string{"fsck", "--connectivity-only", "--no-progress"}

// fetch fetches every head and pull-request head of url into the bare repository at bare.
func (m *mirrorRun) fetch(ctx context.Context, bare, url string) error {
	return m.git(ctx, bare, append([]string{"fetch", "--quiet", "--no-tags", "--prune", "--", url}, mirrorRefspecs...)...)
}

// reclone replaces the damaged mirror at bare with a new one of url, and keeps the old one
// until the new one passes: the new mirror is made at <bare>.reclone, fetched whole, checked
// (git fsck --connectivity-only, and no empty object file), and only then is the old one
// moved to <bare>.bad and the new one renamed into its place; the old one is removed last. A
// new mirror that fails any step is removed and the old one stays where it was, so a failed
// re-clone leaves the bench no worse than it found it. Both names are this verb's own, so a
// leftover of an earlier re-clone that was cut short is removed first.
func (m *mirrorRun) reclone(ctx context.Context, bare, url string) error {
	fresh, old := bare+".reclone", bare+".bad"
	root := filepath.Dir(bare)
	for _, p := range []string{fresh, old} {
		if err := safepath.RemoveUnder(root, p); err != nil {
			return fmt.Errorf("a leftover %s could not be removed: %w", p, err)
		}
	}
	discard := func(err error) error {
		// ignored: the error being returned is the one that matters; a leftover is removed by the next re-clone
		_ = safepath.RemoveUnder(root, fresh)
		return err
	}
	if err := m.git(ctx, "", "init", "--bare", "--quiet", "--", fresh); err != nil {
		return discard(err)
	}
	if err := m.fetch(ctx, fresh, url); err != nil {
		return discard(err)
	}
	if err := m.git(ctx, fresh, mirrorFsck...); err != nil {
		return discard(fmt.Errorf("the new mirror fails git fsck --connectivity-only: %w", err))
	}
	if n := mirrorEmptyObjects(fresh); n > 0 {
		return discard(fmt.Errorf("the new mirror holds %d empty object file(s)", n))
	}
	if err := os.Rename(bare, old); err != nil {
		return discard(err)
	}
	if err := os.Rename(fresh, bare); err != nil {
		// ignored: the rename back is the best that can be done; the error says what failed
		_ = os.Rename(old, bare)
		return discard(err)
	}
	if err := safepath.RemoveUnder(root, old); err != nil {
		return fmt.Errorf("re-cloned, but the old mirror %s could not be removed: %w", old, err)
	}
	return nil
}

// mirrorEmptyObjects counts the empty object files of the bare repository at bare: a loose
// object (objects/<2 hex>/<rest>) or a pack's .pack, .idx or .rev of no bytes. git never
// writes one; a write killed part way (disk pressure, a reaper) leaves one, and every read
// of that object then fails "object file ... is empty". A directory that cannot be read
// counts nothing: the fetch and the fsck name what is wrong with it.
func mirrorEmptyObjects(bare string) int {
	objects := filepath.Join(bare, "objects")
	dirs, err := os.ReadDir(objects)
	if err != nil {
		return 0
	}
	n := 0
	for _, d := range dirs {
		loose := len(d.Name()) == 2 && isHex(d.Name())
		if !d.IsDir() || (!loose && d.Name() != "pack") {
			continue
		}
		files, err := os.ReadDir(filepath.Join(objects, d.Name()))
		if err != nil {
			continue
		}
		for _, f := range files {
			if f.IsDir() {
				continue
			}
			if !loose {
				switch filepath.Ext(f.Name()) {
				case ".pack", ".idx", ".rev":
				default:
					continue // a .keep, a .promisor, a tmp_pack_ of a fetch in flight
				}
			}
			if info, err := f.Info(); err == nil && info.Size() == 0 {
				n++
			}
		}
	}
	return n
}

// isHex reports whether s is lower-case hexadecimal digits.
func isHex(s string) bool {
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return s != ""
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

// cmdMirror is `nova-swarm mirror`.
func cmdMirror(args []string, stdout, stderr io.Writer) int {
	return runMirror(args, stdout, stderr, realMirrorGit, waitFor)
}

func runMirror(args []string, stdout, stderr io.Writer, git mirrorGit, wait func(context.Context, time.Duration) bool) int {
	f := newFlags("mirror")
	dir := f.fs.String("dir", "~/nova-bench/mirror", "the `dir` the bare mirrors live in: <dir>/<bench>/<repo>.git, or <dir>/<repo>.git for this bench's own mirror (default ~/nova-bench/mirror)")
	repos := f.fs.String("repos", "", "the running sprint's stream repository names to mirror, a comma-separated `list`; the fixed set every bench carries is added to them")
	base := f.fs.String("base", "", "the `url` each repository is fetched from, <url>/<name>.git, such as https://github.com/<org> or git@<alias>:<org>")
	benches := f.fs.String("benches", "", "the fleet rows (bench names) every mirror is converged to, a comma-separated `list`; empty is this bench's own flat mirror dir")
	check := f.fs.Bool("check", false, "the converge-check: print one MIRROR MISSING <bench> <repo> per mirror the fleet lacks, write nothing, exit 1 when any is missing")
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
	m := &mirrorRun{dir: d, base: *base, repos: names, benches: benchNames, check: *check, git: git, out: stdout}
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
