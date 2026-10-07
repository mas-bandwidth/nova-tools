package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/config"
	"github.com/mas-bandwidth/nova-tools/internal/gitrun"
	"github.com/mas-bandwidth/nova-tools/internal/gocache"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/safepath"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// FRIENDS' WORKING DIRECTORIES (docs/FRIENDS.md; ideas#833). The owner, 2026-10-02: "we
// might just need the same thing for friends! eg. working directories." and "cleanup must
// be auto!". A bench slot leaves no checkout once its launch is done (nova-swarm member);
// a friend's job had no rule, so her working directory filled with clones and build output
// of jobs long reported. friend clean is the rule, run nightly from a loop row on the
// machine that holds the directories, over every friend row of nova-config (the
// coordinator is one):
//
//   - a job is a directory inbox/<job>/ (the clone beside the brief, the layout before the
//     brief's line) or jobs/<job>/ (the brief's line, docs/FRIENDS.md); it is done when
//     outbox/<job>/REPORT.md exists (the inbox/outbox standard), its age that report's;
//   - inside a done job at least --days old, each clone (a directory holding .git, a
//     repository's directory or a worktree's file) with nothing uncommitted, no stash and
//     no commit missing from every remote-tracking ref is removed, and so is build output
//     (cleanBuildRE) that is no clone; nothing else of the job, and nothing in outbox/, is
//     ever touched;
//   - a dirty clone of a done job at least --days old is listed, one line a run, and kept
//     until the job is cleanDirtyDays old, when it is removed whatever its state, its line
//     saying it was dirty;
//   - the friend's one build cache, <name>-working/.cache/go-build, is held under
//     gocache.Limit as the member holds its pool's.
//
// A job not done, a job younger than --days, a link, and every file outside a removed
// directory are left as they are. Every removal is safepath.RemoveUnderRoots under the
// job's own directory. The rule is decided by cleanJob over what the walk found; the
// walk, git and the removal are the only I/O, and a dry run does all but the removal.

// cleanDays is how many days a done job's clean clones are kept: the owner's three
// (ideas#833).
const cleanDays = 3

// cleanDirtyDays is the age at which a done job's dirty clone is removed whatever its
// state, listed every night before it from cleanDays.
const cleanDirtyDays = 14

// cleanBuildRE names a directory of build output inside a job: what a rebuild makes again.
// A clone is known by its .git, whatever its name.
var cleanBuildRE = regexp.MustCompile(`^(node_modules|target|gocache|gocache-.+|\.gocache|go-build|wt-.+)$`)

// friendClean is one run of the rule over every friend's directory.
type friendClean struct {
	root  string // the directory holding each <name>-working
	days  int
	dry   bool
	now   time.Time
	git   string // the git program; "" is git on PATH
	cache gocache.Bounds

	lines          []string
	freed          int64
	listed, failed int
}

// cleanTarget is one directory the rule may remove: a clone (checked with git first) or
// build output; under is the directory it must sit strictly below.
type cleanTarget struct {
	path, under string
	clone       bool
}

// friend clean's own exit codes, its -h's line (verbhelp.go).
func init() {
	verbExit["friend clean"] = "exit codes: 0 FRIENDS-CLEAN OK, 1 a removal or a read failed (FRIENDS-CLEAN FAILED names each; the summary is FRIENDS-CLEAN INCOMPLETE) or a friend row's name refused, 2 usage, 3 the config could not be read or holds no friend row"
}

func (a *app) cmdFriendClean(args []string, stdout, stderr io.Writer) int {
	const name = "friend clean"
	fs, c := a.verbSetup(name)
	pg := fs.String("pg", "", "the config store whose friend rows are the roster, Postgres postgres://user@host:port/db with no password (else NOVA_PG_DSN; the password from the variable NOVA_PG_PASSWORD_ENV names), as nova-config takes it")
	file := fs.String("file", "", "a nova-config store file in PostgreSQL's place (nova-config --file), for trying it with no database")
	root := fs.String("root", "", "the directory holding each friend's <name>-working (else HOME, else the user's home)")
	days := fs.Int("days", cleanDays, "a done job's clean clones and build output are removed once its REPORT.md is this many days old")
	dry := fs.Bool("dry-run", false, "print every removal and listing with the bytes it would free, and remove nothing")
	pos, err := parse(fs, args)
	switch {
	case err != nil:
		return refuse(stderr, name, err.Error())
	case len(pos) > 0:
		return refuse(stderr, name, "takes no words, found "+oneline.Escape(pos[0]))
	case *pg != "" && *file != "":
		return refuse(stderr, name, "--pg and --file are exclusive: --file reads the friend rows from a nova-config store file in PostgreSQL's place")
	case *days < 1:
		return refuse(stderr, name, fmt.Sprintf("--days is at least 1, got %d: the days a done job's report is old before its clean clones go", *days))
	}
	if *root == "" {
		*root = a.getenv("HOME")
	}
	if *root == "" {
		// ignored: no home is the refusal below, naming the empty --root
		*root, _ = os.UserHomeDir()
	}
	if fi, err := os.Stat(*root); *root == "" || err != nil || !fi.IsDir() {
		return refuse(stderr, name, "--root wants the directory holding each friend's <name>-working, and "+oneline.Escape(*root)+" is not one")
	}
	read := a.friends
	if *file != "" {
		read = fileFriends(*file)
	}
	rows, err := read(context.Background(), *pg)
	if err != nil {
		fmt.Fprintf(stderr, "%s %s: the config cannot be read: %s; nothing was removed\n", prog, name, oneline.WithRemedy(err.Error(), "nova-config friend list"))
		return exitCannotRead
	}
	if len(rows) == 0 {
		fmt.Fprintf(stderr, "%s %s: the config holds no friend row; is this the fleet's config? run: nova-config friend list; nothing was removed\n", prog, name)
		return exitCannotRead
	}
	names := make([]string, len(rows))
	for i, r := range rows {
		names[i] = r.Name
	}
	for _, n := range names {
		if !sprint.ValidID(n) || !safepath.NameOK(n) {
			fmt.Fprintf(stderr, "%s %s: a friend name wants letters, digits, _ and -: %s; fix the friend row in nova-config; nothing was removed\n", prog, name, oneline.Escape(n))
			return 1
		}
	}
	cl := &friendClean{root: *root, days: *days, dry: *dry, now: a.now(),
		cache: gocache.Bounds{Limit: gocache.Limit, Slack: gocache.Slack, Remove: math.MaxInt}}
	for _, n := range names {
		cl.friend(n)
	}
	return cl.report(stdout, c.json)
}

// fileFriends reads the friend rows from a nova-config store file (--file).
func fileFriends(path string) friendsFn {
	return func(ctx context.Context, _ string) ([]config.Row, error) {
		st, err := config.OpenFile(path)
		if err != nil {
			return nil, err
		}
		return st.List(ctx, config.KindFriend)
	}
}

// report prints the run's lines and its summary (or, --json, one object), and is the exit
// code: 1 when a removal or a read failed.
func (cl *friendClean) report(stdout io.Writer, asJSON bool) int {
	facts := map[string]any{"freed": cl.freed, "listed": cl.listed, "failed": cl.failed, "dry_run": cl.dry, "lines": orEmpty(cl.lines)}
	line := fmt.Sprintf("FRIENDS-CLEAN OK freed=%d listed=%d", cl.freed, cl.listed)
	if cl.dry {
		line += " dry-run: nothing was removed"
	}
	if cl.failed > 0 {
		line = fmt.Sprintf("FRIENDS-CLEAN INCOMPLETE freed=%d listed=%d failed=%d", cl.freed, cl.listed, cl.failed)
	}
	if !asJSON {
		for _, l := range cl.lines {
			fmt.Fprintln(stdout, l)
		}
	}
	if cl.failed > 0 {
		if asJSON {
			facts["status"], facts["exit"] = "incomplete", 1
			sayOK(stdout, true, "friend clean", line, facts)
		} else {
			fmt.Fprintln(stdout, line)
		}
		return 1
	}
	sayOK(stdout, asJSON, "friend clean", line, facts)
	return 0
}

// say adds one line to the run's output; every argument is escaped by its caller.
func (cl *friendClean) say(format string, args ...any) {
	cl.lines = append(cl.lines, fmt.Sprintf(format, args...))
}

// friend applies the rule to one friend's directory, and says what it found on one line.
func (cl *friendClean) friend(name string) {
	w := filepath.Join(cl.root, name+"-working")
	if fi, err := os.Lstat(w); err != nil || !fi.IsDir() {
		cl.say("FRIENDS-CLEAN FRIEND %s dir=%s absent: no working directory on this machine", oneline.Escape(name), oneline.Escape(w))
		return
	}
	freed, listed := cl.freed, cl.listed
	jobs, done := map[string]bool{}, map[string]bool{}
	for _, area := range []string{"inbox", "jobs"} {
		entries, err := os.ReadDir(filepath.Join(w, area))
		if err != nil {
			if !errors.Is(err, fs.ErrNotExist) {
				cl.fail(name, filepath.Join(w, area), err)
			}
			continue
		}
		for _, e := range entries {
			job := e.Name()
			if !e.IsDir() || strings.HasPrefix(job, ".") || !safepath.NameOK(job) {
				continue // a file (README.md), a link, a hidden or odd name: no job
			}
			jobs[job] = true
			report, err := os.Lstat(filepath.Join(w, "outbox", job, "REPORT.md"))
			if err != nil || !report.Mode().IsRegular() {
				continue // not done
			}
			done[job] = true
			if age := cl.now.Sub(report.ModTime()); age >= time.Duration(cl.days)*24*time.Hour {
				cl.job(name, filepath.Join(w, area, job), area == "jobs", age)
			}
		}
	}
	cl.cleanCache(name, w)
	cl.say("FRIENDS-CLEAN FRIEND %s dir=%s jobs=%d done=%d freed=%d listed=%d", oneline.Escape(name), oneline.Escape(w), len(jobs), len(done), cl.freed-freed, cl.listed-listed)
}

// job removes a done, old job's clean clones and build output, and lists its dirty clones
// until cleanDirtyDays.
func (cl *friendClean) job(name, dir string, jobsArea bool, age time.Duration) {
	ageDays := int(age / (24 * time.Hour))
	for _, t := range cl.targets(name, dir, jobsArea) {
		why := ""
		if t.clone {
			why = cl.dirty(t.path)
		}
		if why != "" && ageDays < cleanDirtyDays {
			cl.listed++
			cl.say("FRIENDS-CLEAN DIRTY friend=%s path=%s age=%dd why=%s: kept; removed at %dd whatever its state",
				oneline.Escape(name), oneline.Escape(t.path), ageDays, oneline.Escape(why), cleanDirtyDays)
			continue
		}
		kind := "build"
		if t.clone {
			kind = "clone"
		}
		if why != "" {
			kind += " dirty=" + why
		}
		size := treeBytes(t.path)
		action := "WOULD-REMOVE"
		if !cl.dry {
			if err := safepath.RemoveUnderRoots(t.path, t.under); err != nil {
				cl.fail(name, t.path, err)
				continue
			}
			action = "REMOVED"
		}
		cl.freed += size
		cl.say("FRIENDS-CLEAN %s friend=%s path=%s bytes=%d age=%dd kind=%s", action, oneline.Escape(name), oneline.Escape(t.path), size, ageDays, oneline.Escape(kind))
	}
}

// targets are the clones and build output inside a job's directory, none inside another.
// A job directory under jobs/ that is itself a clone is one target; one under inbox/ is
// never removed (the brief is in it) and is said as a NOTE.
func (cl *friendClean) targets(name, dir string, jobsArea bool) (ts []cleanTarget) {
	if isClone(dir) {
		if jobsArea {
			return []cleanTarget{{path: dir, under: filepath.Dir(dir), clone: true}}
		}
		cl.say("FRIENDS-CLEAN NOTE friend=%s path=%s: the inbox job is itself a clone, and its brief is in it; left as it is (docs/FRIENDS.md puts the clone in jobs/<job>/)", oneline.Escape(name), oneline.Escape(dir))
		return nil
	}
	// ignored: the walk is never stopped by an error; a part it cannot read is skipped, never removed
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || p == dir || !d.IsDir() {
			return nil // a file, a link (never followed), or what could not be read
		}
		switch {
		case d.Name() == ".git":
			return filepath.SkipDir
		case isClone(p):
			ts = append(ts, cleanTarget{path: p, under: dir, clone: true})
			return filepath.SkipDir
		case cleanBuildRE.MatchString(d.Name()):
			ts = append(ts, cleanTarget{path: p, under: dir})
			return filepath.SkipDir
		}
		return nil
	})
	return ts
}

// isClone says dir holds a repository or a worktree: a .git directory or file.
func isClone(dir string) bool {
	fi, err := os.Lstat(filepath.Join(dir, ".git"))
	return err == nil && (fi.IsDir() || fi.Mode().IsRegular())
}

// dirty is why a clone holds work that is nowhere else, "" when it holds none: an
// uncommitted path, a stash, or a commit of HEAD or a local branch on no remote-tracking
// ref (no network: what the clone last fetched). A git that fails is a reason: a state
// that cannot be read is never clean.
func (cl *friendClean) dirty(dir string) string {
	o := gitrun.Options{Bin: cl.git, C: dir, OwnRepo: true}
	g := func(args ...string) (string, error) {
		// the clone's config is the friend's data: no fsmonitor command of hers runs here
		return gitrun.Output(context.Background(), o, append([]string{"-c", "core.fsmonitor=false"}, args...)...)
	}
	status, err := g("status", "--porcelain")
	switch {
	case err != nil:
		return "git status failed: " + oneline.Err(err)
	case status != "":
		if n := len(strings.Split(status, "\n")); n > 1 {
			return fmt.Sprintf("%d uncommitted paths", n)
		}
		return "1 uncommitted path"
	}
	if _, err := g("rev-parse", "-q", "--verify", "refs/stash"); err == nil {
		return "a stash"
	}
	// HEAD joins the revisions only when it names a commit (an unborn HEAD would fail the
	// log), and before --not, which negates every revision after it
	log := []string{"log", "--oneline", "-1", "--branches"}
	if _, err := g("rev-parse", "-q", "--verify", "HEAD"); err == nil {
		log = append(log, "HEAD")
	}
	switch out, err := g(append(log, "--not", "--remotes")...); {
	case err != nil:
		return "git log failed: " + oneline.Err(err)
	case out != "":
		return "commits on no remote"
	}
	return ""
}

// cleanCache holds the friend's one Go build cache under the limit (gocache.Hold).
func (cl *friendClean) cleanCache(name, w string) {
	dir := filepath.Join(w, ".cache", "go-build")
	if fi, err := os.Lstat(dir); err != nil || !fi.IsDir() {
		return
	}
	b := cl.cache
	b.Dry = cl.dry
	r := gocache.Hold(dir, cl.now, b)
	if r.Removed == 0 && r.Failed == 0 {
		return
	}
	cl.freed += r.Freed
	cl.failed += r.Failed
	action := "removed"
	if cl.dry {
		action = "would-remove"
	}
	why := ""
	if r.Failed > 0 {
		why = fmt.Sprintf(" failed=%d first=%s", r.Failed, r.Why)
	}
	cl.say("FRIENDS-CLEAN CACHE friend=%s path=%s %s=%d freed=%d size=%d limit=%d%s",
		oneline.Escape(name), oneline.Escape(dir), oneline.Escape(action), r.Removed, r.Freed, r.Size, b.Limit, oneline.Escape(why))
}

// fail says a removal or a read that failed, one line, and counts it.
func (cl *friendClean) fail(name, path string, err error) {
	cl.failed++
	cl.say("FRIENDS-CLEAN FAILED friend=%s path=%s: %s", oneline.Escape(name), oneline.Escape(path), oneline.Escape(err.Error()))
}

// treeBytes is the bytes of the regular files under dir, links not followed; what cannot
// be read counts nothing (the size is the line's report, never a decision).
func treeBytes(dir string) (n int64) {
	// ignored: the walk is never stopped by an error; an unreadable part counts nothing
	_ = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() {
			if fi, ierr := d.Info(); ierr == nil {
				n += fi.Size()
			}
		}
		return nil
	})
	return n
}
