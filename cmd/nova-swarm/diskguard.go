package main

import (
	"context"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/cardcontract"
	"github.com/mas-bandwidth/nova-tools/internal/gitrun"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/safepath"
	"github.com/mas-bandwidth/nova-tools/internal/subproc"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
	"github.com/mas-bandwidth/nova-tools/internal/typedrec"
)

// THE DISK GUARD (docs/SPEC-SWARM.md, `disk-guard`; docs/FLEET.md, loops.yml).
//
// The member removes what its own launches leave (slotclean.go, lazyclean.go), and only
// while it runs: everything else a machine of the fleet writes grew without bound. On
// 2026-10-02 the CI runners' Go build caches held 180 GB of one machine's 437, the
// login's own build cache 72 GB of another's, loop logs grew 7 MB a day each, the pools
// of stopped loops kept their checkouts, and land clones stayed for good. The owner: "We
// must not fill discs again" and "cleanup must be auto!".
//
// So every machine runs `nova-swarm disk-guard` every few minutes from its loop row
// (fleet/loops.yml adds the row to every machine), one pass, and each pass:
//
//   - holds every Go build cache it knows under --cache-max-gb by the member's own trim
//     (lazyclean.go, cacheTrim): entries used longest ago first, never one used in the last
//     two hours, so a build running against the cache never loses what it is reading;
//   - empties a module cache over --modcache-max-gb, as go clean -modcache does, only
//     while no go command runs on the machine;
//   - rotates every loop log over --log-max-mb: copied to <log>.1 and emptied in place (the
//     unit's supervisor appends to the file it opened), the copies shifted up, the one past
//     --log-keep removed;
//   - sweeps the pool of a loop that stopped (no process names its root or its slots, and
//     nothing in its slots moved for --pool-idle) as the member sweeps its own at start:
//     ended launches beyond the newest five go; a live pid's stays, and so does a work
//     launch whose checkout moved past its staged commit (the guard cannot prove it pushed);
//   - removes a land clone unused for --clone-age, never one with uncommitted work or one
//     a process names;
//   - removes a mirror's leftover temporary packs (an aborted fetch's tmp_pack_*, .tmp-*)
//     older than an hour when nothing may be fetching into it; never git prune, which can
//     delete objects a clone borrowing the mirror is reading;
//   - warns, under --disk-floor, on its own output, which is its loop log.
//
// One line per action, with the bytes it freed; one line at the end. Nothing is removed
// through a link, and nothing under /tmp: the guard looks only where it is told and where
// the fleet's layout puts these things.

// The guard's defaults: the order of magnitude the owner named for a build cache (10 GiB,
// the member's own), a module cache's larger, a log's size and copies, how long a pool
// must be still, and how long a land clone or a temporary pack must be unused.
const (
	guardCacheGiB   = 10
	guardModGiB     = 50
	guardLogMiB     = 50
	guardLogKeep    = 3
	guardPoolIdle   = 30 * time.Minute
	guardCloneAge   = 24 * time.Hour
	guardFloorGiB   = 10
	guardTmpPackAge = time.Hour
	// guardKeepNewest is how many ended launches a stopped pool keeps: the member's own
	// sweep's figure (keepFailed), held here apart so the two can part when the member's
	// rule changes.
	guardKeepNewest = 5
)

// guard is one disk-guard run: what it looks at, its limits, and its world (the seams a
// test fakes: the clock, the process list, the free disk, the module cache's clean and a
// land clone's status).
type guard struct {
	roots, caches, modCaches   []string
	logDir, landDir, mirrorDir string
	cacheMax, modMax, logMax   int64
	floor                      int64
	logKeep                    int
	poolIdle, cloneAge         time.Duration
	now                        time.Time
	home                       string
	procs                      func() ([]string, error)
	free                       func(string) (uint64, error)
	cleanMod                   func(dir string) error
	dirty                      func(dir string) (bool, error)
	out                        io.Writer
	freed                      int64
	failed                     int
	list                       []string // the process list, read once a run
	dry                        bool     // --dry-run: every rule judged, nothing removed or rotated
	listRead, listFailed       bool
}

// say is one line of the run's output.
func (g *guard) say(line string) {
	if g.dry {
		for done, would := range dryWords {
			if rest, ok := strings.CutPrefix(line, done+" "); ok {
				line = would + " " + rest
				break
			}
		}
	}
	fmt.Fprintln(g.out, oneline.Escape(line))
}

// dryWords is what an action's line says under --dry-run, which does none of them.
var dryWords = map[string]string{"REMOVED": "WOULD-REMOVE", "TRIMMED": "WOULD-TRIM", "CLEANED": "WOULD-CLEAN", "ROTATED": "WOULD-ROTATE"}

// fail is one thing the run could not do: said, counted, and the run ends INCOMPLETE.
func (g *guard) fail(line string) {
	g.failed++
	g.say("NOTE " + line)
}

// unless does a removal, except under --dry-run.
func (g *guard) unless(remove func() error) error {
	if g.dry {
		return nil
	}
	return remove()
}

// processes is the process list, read once a run; ok is false when it cannot be read, and
// then nothing that needs it is removed.
func (g *guard) processes() ([]string, bool) {
	if !g.listRead {
		g.listRead = true
		list, err := g.procs()
		if err != nil {
			g.listFailed = true
			g.fail(fmt.Sprintf("the process list could not be read (%s); nothing a live process may hold is removed this run", oneline.Err(err)))
		}
		g.list = list
	}
	return g.list, !g.listFailed
}

// naming is the first process line with a word that is one of paths or lies under it.
func naming(list []string, paths ...string) string {
	for _, line := range list {
		for _, w := range strings.Fields(line) {
			if _, v, ok := strings.Cut(w, "="); ok && strings.HasPrefix(w, "-") {
				w = v // --root=<dir>
			}
			for _, p := range paths {
				if w == p || strings.HasPrefix(w, p+string(filepath.Separator)) {
					return line
				}
			}
		}
	}
	return ""
}

// run is one pass of every rule, then the floor and the closing line: exit 0, or 1 when
// something could not be done (each said on its NOTE line).
func (g *guard) run() int {
	g.logs()
	g.buildCaches()
	g.modules()
	g.pools()
	g.landClones()
	g.mirrors()
	free := uint64(0)
	for i, p := range append([]string{g.home}, g.roots...) {
		n, err := g.free(p)
		if err != nil {
			if !os.IsNotExist(err) {
				g.fail(fmt.Sprintf("the free disk on the volume of %s could not be read (%s)", oneline.Field(p), oneline.Err(err)))
			}
			continue
		}
		if i == 0 {
			free = n
		}
		if g.floor > 0 && n < uint64(g.floor) {
			g.say(fmt.Sprintf("DISK-GUARD WARN free=%d floor=%d on the volume of %s: members there start no card; run: df -h %s, and read what this log removed and kept", n, g.floor, oneline.Field(p), oneline.Field(p)))
		}
	}
	// the closing line is written here, beside the exit it explains (law #2573): numbers
	// only, so it needs neither say's escape nor its dry-run wording
	if g.failed > 0 {
		fmt.Fprintf(g.out, "DISK-GUARD INCOMPLETE freed=%d free=%d failed=%d\n", g.freed, free, g.failed)
		return 1
	}
	fmt.Fprintf(g.out, "DISK-GUARD OK freed=%d free=%d\n", g.freed, free)
	return 0
}

// ------------------------------------------------------------------------------- logs

// logs rotates every regular *.log file in the log directory over the size.
func (g *guard) logs() {
	if g.logDir == "" {
		return
	}
	entries, err := os.ReadDir(g.logDir)
	if err != nil {
		if !os.IsNotExist(err) {
			g.fail(fmt.Sprintf("the log directory %s could not be listed (%s)", oneline.Field(g.logDir), oneline.Err(err)))
		}
		return
	}
	for _, e := range entries {
		if !e.Type().IsRegular() || !strings.HasSuffix(e.Name(), ".log") {
			continue // a link, a directory, a rotated copy
		}
		path := filepath.Join(g.logDir, e.Name())
		fi, err := e.Info()
		if err != nil || fi.Size() <= g.logMax {
			continue
		}
		freed, err := rotate(path, g.logKeep, g.dry)
		g.freed += freed
		if err != nil {
			g.fail(fmt.Sprintf("the log %s was not rotated (%s)", oneline.Field(path), oneline.Err(err)))
			continue
		}
		g.say(fmt.Sprintf("ROTATED log %s freed=%d size=%d keep=%d", oneline.Field(path), freed, fi.Size(), g.logKeep))
	}
}

// rotate copies path to path.1 and empties path in place, after removing path.<keep> and
// moving each path.<i> to path.<i+1>; freed is the bytes of the copy it removed. A copy
// that is not a regular file stops the rotation, which never removes through a link.
func rotate(path string, keep int, dry bool) (freed int64, err error) {
	last := path + "." + strconv.Itoa(keep)
	if fi, err := os.Lstat(last); err == nil {
		if !fi.Mode().IsRegular() {
			return 0, fmt.Errorf("%q is not a regular file", last)
		}
		if dry {
			return fi.Size(), nil
		}
		if err := safepath.RemoveUnder(filepath.Dir(path), last); err != nil {
			return 0, err
		}
		freed = fi.Size()
	}
	if dry {
		return freed, nil
	}
	for i := keep - 1; i >= 1; i-- {
		from := path + "." + strconv.Itoa(i)
		if fi, err := os.Lstat(from); err != nil || !fi.Mode().IsRegular() {
			continue
		}
		if err := os.Rename(from, path+"."+strconv.Itoa(i+1)); err != nil {
			return freed, err
		}
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return freed, err
	}
	if err := os.WriteFile(path+".1", b, 0o644); err != nil {
		return freed, err
	}
	// emptied in place: the supervisor's open file appends at the new end (launchd's and
	// systemd's append:); a line written between the copy and this is lost
	return freed, os.Truncate(path, 0)
}

// ------------------------------------------------------------------------------- caches

// buildCaches holds every Go build cache under the cap: one walk measures it, and one more,
// only when it is over, removes its entries used longest ago until it is under the cap less
// a fifth (cacheTrim, the member's trim, with the whole cache in one round).
func (g *guard) buildCaches() {
	for _, dir := range g.caches {
		if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
			continue
		}
		t := &cacheTrim{}
		b := cacheBounds{limit: g.cacheMax, slack: g.cacheMax / 5, dirs: cacheSubdirs, remove: math.MaxInt}
		if c := t.round(dir, g.now, b); c.size <= g.cacheMax { // measures, removes nothing
			continue
		}
		if g.dry {
			g.say(fmt.Sprintf("TRIMMED go-build %s size=%d cap=%d", oneline.Field(dir), t.total(), g.cacheMax))
			continue
		}
		c := t.round(dir, g.now, b)
		g.freed += c.freed
		g.say(fmt.Sprintf("TRIMMED go-build %s freed=%d size=%d cap=%d", oneline.Field(dir), c.freed, c.size, g.cacheMax))
		if c.failed > 0 {
			g.fail(fmt.Sprintf("the go build cache %s: %d entries were not removed (%s)", oneline.Field(dir), c.failed, oneline.Escape(c.why)))
		}
	}
}

// modules empties a module cache over its cap, only while no go command runs: a build
// reading it would lose its modules mid-read.
func (g *guard) modules() {
	for _, dir := range g.modCaches {
		if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
			continue
		}
		size := treeSize(dir)
		if size <= g.modMax {
			continue
		}
		list, ok := g.processes()
		if !ok {
			continue
		}
		if line := goRunning(list); line != "" {
			g.say(fmt.Sprintf("KEPT go-mod %s: a go command is running (%s)", oneline.Field(dir), oneline.Escape(line)))
			continue
		}
		if err := g.unless(func() error { return g.cleanMod(dir) }); err != nil {
			g.fail(fmt.Sprintf("the module cache %s was not cleaned (%s)", oneline.Field(dir), oneline.Err(err)))
			continue
		}
		g.freed += size
		g.say(fmt.Sprintf("CLEANED go-mod %s freed=%d cap=%d", oneline.Field(dir), size, g.modMax))
	}
}

// goRunning is the first process line whose program is go.
func goRunning(list []string) string {
	for _, line := range list {
		if f := strings.Fields(line); len(f) > 0 && filepath.Base(f[0]) == "go" {
			return line
		}
	}
	return ""
}

// cleanModCache empties a module cache as go clean -modcache does (its entries are
// read-only; each is made writable, then removed), leaving the directory itself, which
// native made for its children.
func cleanModCache(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if err := safepath.RemoveUnderRoots(filepath.Join(dir, e.Name()), dir); err != nil {
			return err
		}
	}
	return nil
}

// ------------------------------------------------------------------------------- pools

// pools sweeps the pool of every root whose loop stopped.
func (g *guard) pools() {
	for _, root := range g.roots {
		slots := filepath.Join(root, "slots")
		entries, err := os.ReadDir(slots)
		if err != nil || g.poolActive(slots, entries) {
			continue // no pool here, or one whose loop beats
		}
		list, ok := g.processes()
		if !ok || naming(list, root, slots) != "" {
			continue // its loop, or a child of it, runs: the pool is its loop's to clean
		}
		g.sweep(slots, entries)
	}
}

// poolActive is whether the slots directory or an entry of it moved within --pool-idle:
// a loop that beats writes there.
func (g *guard) poolActive(slots string, entries []os.DirEntry) bool {
	if fi, err := os.Stat(slots); err == nil && g.now.Sub(fi.ModTime()) < g.poolIdle {
		return true
	}
	for _, e := range entries {
		if fi, err := e.Info(); err == nil && g.now.Sub(fi.ModTime()) < g.poolIdle {
			return true
		}
	}
	return false
}

// sweep is the member's start-up sweep (slotclean.go) for a pool nothing owns: every
// launch directory with no live pid and still for leftoverIdle is ended; the newest
// guardKeepNewest of them stay, and of the rest a work launch holding commits past its
// staged one is kept and said.
func (g *guard) sweep(slots string, entries []os.DirEntry) {
	type launch struct {
		name string
		at   time.Time
	}
	var ended []launch
	for _, e := range entries {
		name := e.Name()
		if !e.IsDir() || !launchDirRE.MatchString(name) || !safepath.NameOK(name) {
			continue
		}
		if livePID(filepath.Join(slots, name+".pid")) > 0 {
			continue
		}
		if at := lastActivity(slots, name); g.now.Sub(at) >= leftoverIdle {
			ended = append(ended, launch{name, at})
		}
	}
	sort.Slice(ended, func(i, j int) bool { return ended[i].at.After(ended[j].at) })
	for i, l := range ended {
		if i < guardKeepNewest {
			continue
		}
		dir := filepath.Join(slots, l.name)
		if why := heldWork(slots, l.name); why != "" {
			g.say(fmt.Sprintf("KEPT slot %s: %s; nothing removes it until a person has looked", oneline.Field(dir), oneline.Escape(why)))
			continue
		}
		size := treeSize(dir)
		if err := g.unless(func() error { return safepath.RemoveUnderRoots(dir, slots) }); err != nil {
			g.fail(fmt.Sprintf("the slot %s was not removed (%s)", oneline.Field(dir), oneline.Err(err)))
			continue
		}
		g.freed += size
		g.say(fmt.Sprintf("REMOVED slot %s freed=%d", oneline.Field(dir), size))
	}
}

// workLaunchRE is a work launch's name: its card, its generation, its epoch (a read's is
// .a<n>, and a read commits nothing).
var workLaunchRE = regexp.MustCompile(`^([A-Za-z0-9._-]+)\.g[0-9]+\.e[0-9]+$`)

// heldWork says why a launch's checkout may hold work no one pushed: its branch moved past
// the commit native staged, or the guard cannot tell. It reads the checkout's files and
// never runs git there: the child wrote that repository's configuration and hooks.
func heldWork(slots, name string) string {
	m := workLaunchRE.FindStringSubmatch(name)
	if m == nil {
		return ""
	}
	git := filepath.Join(slots, name, "jobs", m[1], swarm.JobRepo, ".git")
	if fi, err := os.Lstat(git); err != nil || !fi.IsDir() {
		return "" // no checkout was staged: nothing of the child's to keep
	}
	staged, err := os.ReadFile(filepath.Join(slots, name, cardcontract.StagedName))
	base := strings.TrimSpace(string(staged))
	if err != nil || !typedrec.IsFullSha(base) {
		return "its checkout records no staged commit to count its own commits from"
	}
	head, err := readHead(git)
	if err != nil {
		return "its checkout's head could not be read (" + err.Error() + ")"
	}
	if head != base {
		return "its checkout holds commits past the staged one (" + head[:12] + " over " + base[:12] + "), which the guard cannot prove pushed"
	}
	return ""
}

// readHead is the commit a repository's HEAD names, read from its files: a detached sha,
// or the ref it names under refs/, loose or packed.
func readHead(git string) (string, error) {
	b, err := os.ReadFile(filepath.Join(git, "HEAD"))
	if err != nil {
		return "", err
	}
	head := strings.TrimSpace(string(b))
	ref, isRef := strings.CutPrefix(head, "ref: ")
	if !isRef {
		if typedrec.IsFullSha(head) {
			return head, nil
		}
		return "", fmt.Errorf("HEAD is %q", oneline.Cap(head, 80))
	}
	if !strings.HasPrefix(ref, "refs/") || safepath.HasDotDot(ref) {
		return "", fmt.Errorf("HEAD names %q", oneline.Cap(ref, 80))
	}
	if b, err := os.ReadFile(filepath.Join(git, filepath.FromSlash(ref))); err == nil {
		if sha := strings.TrimSpace(string(b)); typedrec.IsFullSha(sha) {
			return sha, nil
		}
	}
	if b, err := os.ReadFile(filepath.Join(git, "packed-refs")); err == nil {
		for _, line := range strings.Split(string(b), "\n") {
			if sha, name, ok := strings.Cut(strings.TrimSpace(line), " "); ok && name == ref && typedrec.IsFullSha(sha) {
				return sha, nil
			}
		}
	}
	return "", fmt.Errorf("%q names no commit", ref)
}

// ------------------------------------------------------------------------------- land

// landCloneRE is a land clone's directory name (nova-sprint's repoDirName): a readable
// name, then 16 hex digits of a hash.
var landCloneRE = regexp.MustCompile(`^[A-Za-z0-9.-]+-[0-9a-f]{16}$`)

// landClones removes every land clone unused for --clone-age that no process names and
// that has no uncommitted work; land makes the clone again on its next use.
func (g *guard) landClones() {
	if g.landDir == "" {
		return
	}
	entries, err := os.ReadDir(g.landDir)
	if err != nil {
		if !os.IsNotExist(err) {
			g.fail(fmt.Sprintf("the land directory %s could not be listed (%s)", oneline.Field(g.landDir), oneline.Err(err)))
		}
		return
	}
	for _, e := range entries {
		if !e.IsDir() || !landCloneRE.MatchString(e.Name()) {
			continue
		}
		dir := filepath.Join(g.landDir, e.Name())
		if g.now.Sub(cloneUsed(dir)) < g.cloneAge {
			continue
		}
		list, ok := g.processes()
		if !ok {
			return
		}
		if naming(list, dir) != "" {
			g.say(fmt.Sprintf("KEPT land clone %s: a live process names it", oneline.Field(dir)))
			continue
		}
		dirty, err := g.dirty(dir)
		if err != nil {
			g.say(fmt.Sprintf("KEPT land clone %s: its status could not be read (%s)", oneline.Field(dir), oneline.Err(err)))
			continue
		}
		if dirty {
			g.say(fmt.Sprintf("KEPT land clone %s: it has uncommitted work; run: git -C %s status", oneline.Field(dir), oneline.Field(dir)))
			continue
		}
		size := treeSize(dir)
		if err := g.unless(func() error { return safepath.RemoveUnderRoots(dir, g.landDir) }); err != nil {
			g.fail(fmt.Sprintf("the land clone %s was not removed (%s)", oneline.Field(dir), oneline.Err(err)))
			continue
		}
		g.freed += size
		g.say(fmt.Sprintf("REMOVED land clone %s freed=%d", oneline.Field(dir), size))
	}
}

// cloneUsed is the newest time of a clone's directory and the files git writes when it
// is used: its index, HEAD, FETCH_HEAD, ORIG_HEAD, packed-refs and HEAD's log.
func cloneUsed(dir string) (at time.Time) {
	for _, p := range []string{"", ".git", ".git/index", ".git/HEAD", ".git/FETCH_HEAD", ".git/ORIG_HEAD", ".git/packed-refs", ".git/logs/HEAD"} {
		if fi, err := os.Lstat(filepath.Join(dir, filepath.FromSlash(p))); err == nil && fi.ModTime().After(at) {
			at = fi.ModTime()
		}
	}
	return at
}

// landDirty is whether a land clone has uncommitted work: git status in the clone land
// made (its configuration is land's, never a child's).
func landDirty(dir string) (bool, error) {
	res, err := gitrun.Run(context.Background(), gitrun.Options{C: dir, OwnRepo: true}, "status", "--porcelain")
	if err != nil {
		return false, fmt.Errorf("%s: %w", strings.TrimSpace(string(res.Stderr)), err)
	}
	return strings.TrimSpace(string(res.Stdout)) != "", nil
}

// ------------------------------------------------------------------------------- mirrors

// tmpPackRE is a temporary pack an aborted fetch or index-pack leaves in objects/pack.
var tmpPackRE = regexp.MustCompile(`^(tmp_pack_|\.tmp-)`)

// mirrors removes each mirror's temporary packs older than guardTmpPackAge when nothing
// may be fetching into it (fetching). Never git prune: a clone borrowing the mirror's
// objects can lose one it is reading.
func (g *guard) mirrors() {
	if g.mirrorDir == "" {
		return
	}
	entries, err := os.ReadDir(g.mirrorDir)
	if err != nil {
		if !os.IsNotExist(err) {
			g.fail(fmt.Sprintf("the mirror directory %s could not be listed (%s)", oneline.Field(g.mirrorDir), oneline.Err(err)))
		}
		return
	}
	for _, e := range entries {
		repo := filepath.Join(g.mirrorDir, e.Name())
		pack := mirrorPackDir(repo)
		if !e.IsDir() || pack == "" {
			continue
		}
		old := g.oldTmpPacks(pack)
		if len(old) == 0 {
			continue
		}
		list, ok := g.processes()
		if !ok {
			return
		}
		if line := fetching(list, repo); line != "" {
			g.say(fmt.Sprintf("KEPT mirror %s: a fetch or clone of it may be running (%s)", oneline.Field(repo), oneline.Escape(line)))
			continue
		}
		var n int
		var freed int64
		for _, path := range old {
			fi, err := os.Lstat(path)
			if err != nil || !fi.Mode().IsRegular() {
				continue
			}
			if err := g.unless(func() error { return safepath.RemoveUnder(pack, path) }); err != nil {
				g.fail(fmt.Sprintf("the temporary pack %s was not removed (%s)", oneline.Field(path), oneline.Err(err)))
				continue
			}
			n++
			freed += fi.Size()
		}
		g.freed += freed
		if n > 0 {
			g.say(fmt.Sprintf("REMOVED mirror temp packs %s files=%d freed=%d", oneline.Field(repo), n, freed))
		}
	}
}

// oldTmpPacks is every regular temporary pack in pack older than guardTmpPackAge.
func (g *guard) oldTmpPacks(pack string) (old []string) {
	packs, err := os.ReadDir(pack)
	if err != nil {
		return nil
	}
	for _, p := range packs {
		if !p.Type().IsRegular() || !tmpPackRE.MatchString(p.Name()) {
			continue
		}
		if fi, err := p.Info(); err == nil && g.now.Sub(fi.ModTime()) >= guardTmpPackAge {
			old = append(old, filepath.Join(pack, p.Name()))
		}
	}
	return old
}

// mirrorPackDir is a repository's objects/pack: a bare one's, or a working one's under
// .git; "" for a directory that is neither.
func mirrorPackDir(repo string) string {
	if fi, err := os.Stat(filepath.Join(repo, "HEAD")); err == nil && !fi.IsDir() {
		return filepath.Join(repo, "objects", "pack")
	}
	if fi, err := os.Stat(filepath.Join(repo, ".git", "HEAD")); err == nil && !fi.IsDir() {
		return filepath.Join(repo, ".git", "objects", "pack")
	}
	return ""
}

// fetching is the first process line that names the repository, or that is a git fetch,
// clone, index-pack or fetch-pack naming no absolute path at all: that one may be running
// in the repository's own directory.
func fetching(list []string, repo string) string {
	if line := naming(list, repo); line != "" {
		return line
	}
	for _, line := range list {
		f := strings.Fields(line)
		if len(f) == 0 || !strings.HasPrefix(filepath.Base(f[0]), "git") {
			continue
		}
		verb, path := false, false
		for _, w := range f[1:] {
			switch w {
			case "fetch", "clone", "index-pack", "fetch-pack":
				verb = true
			}
			if filepath.IsAbs(w) {
				path = true
			}
		}
		if verb && !path {
			return line
		}
	}
	return ""
}

// ------------------------------------------------------------------------------- the verb

// cmdDiskGuard is `nova-swarm disk-guard`: one pass of the guard over this machine.
func cmdDiskGuard(args []string, stdout, stderr io.Writer) int {
	f := newFlags("disk-guard")
	var roots, scans, caches []string
	f.fs.Var(stringListValue{&roots}, "root", "a member's or reader's root `dir` (again for more): its pool is <root>/slots, its caches <root>/cache/go-build and go-mod")
	f.fs.Var(stringListValue{&scans}, "scan", "a `dir` each of whose subdirectories holding slots/ is a root (again for more)")
	f.fs.Var(stringListValue{&caches}, "cache", "another Go build cache, a `dir` or a glob such as ~/runner-*/_cache/go-build (again for more); the login's own is always held")
	cacheGB := f.fs.Int("cache-max-gb", guardCacheGiB, "the `GiB` each Go build cache is held under (default 10)")
	modGB := f.fs.Int("modcache-max-gb", guardModGiB, "the `GiB` over which a module cache is emptied, while no go command runs (default 50)")
	logs := f.fs.String("logs", "~/nova-bench/loops", "the `dir` of the loop logs to rotate (default ~/nova-bench/loops)")
	logMB := f.fs.Int("log-max-mb", guardLogMiB, "the `MiB` over which a loop log is rotated (default 50)")
	logKeep := f.fs.Int("log-keep", guardLogKeep, "how many rotated copies of a log stay, `n` (default 3)")
	poolIdle := f.fs.Duration("pool-idle", guardPoolIdle, "how long a pool's slots must be still, with no process naming it, before it is swept, a `duration` of at least 10m (default 30m)")
	land := f.fs.String("land", "", "the `dir` nova-sprint land keeps its clones in (default <the user's cache directory>/nova-sprint/land)")
	cloneAge := f.fs.Duration("clone-age", guardCloneAge, "how long a land clone must be unused before it is removed, a `duration` (default 24h)")
	mirror := f.fs.String("mirrors", "~/nova-bench/mirror", "the `dir` of the bench's mirrors, whose temporary packs older than an hour are removed (default ~/nova-bench/mirror)")
	dry := f.fs.Bool("dry-run", false, "judge every rule and print each line with WOULD-REMOVE, WOULD-TRIM, WOULD-CLEAN or WOULD-ROTATE, removing and rotating nothing")
	floor := f.fs.Int("disk-floor", guardFloorGiB, "the free `GiB` under which the run warns, the members' own floor (default 10; 0 warns never)")
	if !f.parse(args, stderr) {
		return 2
	}
	for _, c := range []struct {
		name string
		v    int
	}{{"cache-max-gb", *cacheGB}, {"modcache-max-gb", *modGB}, {"log-max-mb", *logMB}, {"log-keep", *logKeep}} {
		if c.v < 1 {
			f.add(fmt.Sprintf("--%s is at least 1, got %d: a limit of nothing would remove everything every run", oneline.Field(c.name), c.v))
		}
	}
	if *floor < 0 {
		f.add(fmt.Sprintf("--disk-floor is 0 or more GiB, got %d", *floor))
	}
	if *cloneAge <= 0 {
		f.add("--clone-age is a positive duration, got " + oneline.Field(cloneAge.String()))
	}
	if *poolIdle < leftoverIdle {
		f.add("--pool-idle is at least " + leftoverIdle.String() + ", the member's own stillness rule, got " + oneline.Field(poolIdle.String()))
	}
	if runtime.GOOS == "windows" {
		f.add("a Windows machine is a client of the swarm and never a bench: it has no pools, loops or land clones to guard")
	}
	if f.refused(stderr) {
		return 2
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return refuse(stderr, " disk-guard", "the user's home could not be read: "+err.Error())
	}
	tilde := func(p string) string {
		if rest, ok := strings.CutPrefix(p, "~/"); ok {
			return filepath.Join(home, rest)
		}
		return filepath.Clean(p)
	}
	g := &guard{
		cacheMax: int64(*cacheGB) * gib, modMax: int64(*modGB) * gib, logMax: int64(*logMB) << 20, logKeep: *logKeep,
		floor: int64(*floor) * gib, poolIdle: *poolIdle, cloneAge: *cloneAge, now: time.Now(), home: home,
		logDir: tilde(*logs), mirrorDir: tilde(*mirror), roots: guardRoots(roots, scans, tilde),
		dry: *dry, procs: processList, free: diskFree, cleanMod: cleanModCache, dirty: landDirty, out: stdout,
	}
	if *land != "" {
		g.landDir = tilde(*land)
	} else if cache, err := os.UserCacheDir(); err == nil {
		g.landDir = filepath.Join(cache, "nova-sprint", "land")
	}
	gocache, gomod := loginGoCaches(home)
	g.caches, g.modCaches = []string{gocache}, []string{gomod}
	for _, r := range g.roots {
		g.caches = append(g.caches, filepath.Join(r, "cache", "go-build"))
		g.modCaches = append(g.modCaches, filepath.Join(r, "cache", "go-mod"))
	}
	for _, c := range caches {
		matches, err := filepath.Glob(tilde(c))
		if err != nil {
			return refuse(stderr, " disk-guard", fmt.Sprintf("--cache %q is not a glob: %s", c, oneline.Err(err)))
		}
		g.caches = append(g.caches, matches...)
	}
	g.caches, g.modCaches = unique(g.caches), unique(g.modCaches)
	return g.run()
}

// guardRoots is every --root, and every subdirectory of a --scan holding slots/, once.
func guardRoots(roots, scans []string, tilde func(string) string) []string {
	var out []string
	for _, r := range roots {
		out = append(out, tilde(r))
	}
	for _, s := range scans {
		entries, err := os.ReadDir(tilde(s))
		if err != nil {
			continue
		}
		for _, e := range entries {
			dir := filepath.Join(tilde(s), e.Name())
			if fi, err := os.Stat(filepath.Join(dir, "slots")); err == nil && fi.IsDir() {
				out = append(out, dir)
			}
		}
	}
	return unique(out)
}

// unique is a list of paths with each once, in order, the empty one dropped.
func unique(paths []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, p := range paths {
		if p != "" && !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	return out
}

// loginGoCaches is the login's own Go build and module caches where go puts them by
// default: GOCACHE, else go-build in the user's cache directory; GOMODCACHE, else
// pkg/mod under the first GOPATH, else under ~/go. A cache moved with go env -w is named
// with --cache.
func loginGoCaches(home string) (build, mod string) {
	build = os.Getenv("GOCACHE")
	if build == "" {
		if cache, err := os.UserCacheDir(); err == nil {
			build = filepath.Join(cache, "go-build")
		}
	}
	mod = os.Getenv("GOMODCACHE")
	if mod == "" {
		gopath := filepath.Join(home, "go")
		if p := filepath.SplitList(os.Getenv("GOPATH")); len(p) > 0 && p[0] != "" {
			gopath = p[0]
		}
		mod = filepath.Join(gopath, "pkg", "mod")
	}
	return build, mod
}

// processList is every process's argument line but this one's, from ps (linux and darwin
// alike): the guard's own line names every root it was given, and would keep them all.
func processList() ([]string, error) {
	cmd, cancel := subproc.CommandFor(context.Background(), 30*time.Second, "ps", "-A", "-ww", "-o", "pid=,args=")
	defer cancel()
	b, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	return psLines(b, os.Getpid()), nil
}

// psLines is ps's "pid args" output as argument lines, the line of pid self left out.
func psLines(b []byte, self int) []string {
	var out []string
	for _, line := range strings.Split(string(b), "\n") {
		pid, args, _ := strings.Cut(strings.TrimSpace(line), " ")
		if n, err := strconv.Atoi(pid); err != nil || n == self {
			continue
		}
		if args = strings.TrimSpace(args); args != "" {
			out = append(out, args)
		}
	}
	return out
}
