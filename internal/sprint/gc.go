package sprint

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/gocache"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/safepath"
)

// GC is nova-sprint gc's pass over the machine it runs on (docs/SPEC-SPRINT.md section 1,
// "gc"). On 2026-10-06 the coordinator freed 314 GiB with a hand-written clean-jobs.py run
// from a shell loop every ten minutes; the owner, 2026-10-04: "no bash scripts, ship
// verbs", and a cold AI coordinating with nova alone had no way to reclaim the disk. GC
// removes exactly the scratch the machinery made and no longer needs, class by class:
//
//   - jobs: <w>/jobs/<job> of every working directory <w> (a <home>/<name>-working link,
//     <ai-root>/<name>/working, <ai-root>/buds/<name>/working, or a plain <home>/<name>-working
//     directory, as a bench keeps them) whose lane is
//     finished or absent: its runner's log (runner.log in <w> or beside it) ended the job
//     (END) and no START, RESUME or LIMIT came after; with no log, its outbox REPORT.md is
//     at least GCReportGrace old; and a job the log never names whose directory is older
//     than the max age is an absent lane. inbox/ and outbox/ are never touched;
//   - reads: <w>/reads/<id> whose RESULT.md names a verdict (ok or broken, the finding the
//     reader's daemon records) and is at least GCReportGrace old;
//   - landers: the linked worktrees of land's clones under the land root, older than the
//     max age; the clones themselves are land's and kept;
//   - bench: <bench-root>/runs/run.* and <bench-root>/buds/<name>/{jobs,reads}/<x> older
//     than the max age. A bench directory is a copy of a tree that lives elsewhere; a clone
//     inside it with uncommitted work keeps the directory, as any other removal does;
//   - cache: every Go build cache of the bench root and of each working directory, held
//     under its cap (gocache.Hold).
//
// Every removal is safepath.RemoveUnderRoots under its class's own directory, and that
// directory must resolve strictly under a known scratch root (the AI root, the bench root,
// the land root, a plain <home>/<name>-working directory); a path under none is REFUSED and
// never removed, so a working directory linked from outside the AI root is never walked.
// The AI root is GCReq.AIRoot when it is a directory, else the one the home's links name
// (GCAIRoot): a machine that exports no NOVA_AI_ROOT and has no ~/ai keeps its working
// directories as links to /Volumes/nova/ai/<name>/working and .../buds/<name>/working. A clone (a directory holding .git)
// inside a removal with uncommitted work, a stash or commits on no remote (Dirty) keeps the
// whole removal: KEPT with why. A dry run reads all of it and removes nothing.

// The classes, in the order a pass says them.
const (
	GCJobs    = "jobs"
	GCReads   = "reads"
	GCLanders = "landers"
	GCBench   = "bench"
	GCCache   = "cache"
)

// GCMaxAge is how old a bench directory, a lander worktree or a job no runner names is
// before gc removes it (--max-age): two days.
const GCMaxAge = 48 * time.Hour

// GCReportGrace is how old a report or a read's result is before gc takes a job or a read
// whose lane no runner's log names: the daemon that wrote it records it in that time.
const GCReportGrace = time.Hour

// gcLogCap bounds the runner log a pass reads: its last gcLogCap bytes.
const gcLogCap = 4 << 20

// GCReq is one pass. Dirty is why a clone holds work that is nowhere else ("" none);
// Worktrees lists a land clone's linked worktrees (nil: none); Volume is the use, in
// percent, of the volume holding a path. Cache bounds the caches' trim (zero: gocache's
// defaults).
type GCReq struct {
	Home, AIRoot, BenchRoot, LandRoot string
	MaxAge                            time.Duration
	Now                               time.Time
	Dry                               bool
	Dirty                             func(dir string) string
	Worktrees                         func(clone string) []string
	Volume                            func(path string) (int, error)
	Cache                             gocache.Bounds
}

// GCClass is what a pass did in one class: directories (or, for the cache, entries)
// removed and their bytes, kept for a reason, refused, failed.
type GCClass struct {
	Name                  string
	Count                 int
	Bytes                 int64
	Kept, Refused, Failed int
}

// GCResult is a pass: each class, the detail lines (a removal, a keep, a refusal, a
// failure), the paths it removed, what it freed, the fullest volume of its roots (-1 not
// read) and the failures. Removed lets a caller that records each removal by name (the
// friend daemon's prune pass) name the jobs without reading the detail's text.
type GCResult struct {
	Classes []GCClass
	Detail  []string
	Removed []string
	Freed   int64
	Volume  int
	Failed  int
	Dry     bool
}

// Lines are the pass's output: the detail, one line per class, then the summary.
func (r GCResult) Lines() []string {
	out := append([]string(nil), r.Detail...)
	for _, c := range r.Classes {
		out = append(out, fmt.Sprintf("GC %s count=%d bytes=%d kept=%d refused=%d failed=%d", c.Name, c.Count, c.Bytes, c.Kept, c.Refused, c.Failed))
	}
	vol := "-"
	if r.Volume >= 0 {
		vol = fmt.Sprintf("%d%%", r.Volume)
	}
	switch {
	case r.Failed > 0:
		out = append(out, fmt.Sprintf("GC INCOMPLETE freed=%d volume=%s failed=%d", r.Freed, vol, r.Failed))
	case r.Dry:
		out = append(out, fmt.Sprintf("GC OK freed=%d volume=%s dry-run: nothing was removed", r.Freed, vol))
	default:
		out = append(out, fmt.Sprintf("GC OK freed=%d volume=%s", r.Freed, vol))
	}
	return out
}

// gcPass is one pass in progress.
type gcPass struct {
	r     GCReq
	res   GCResult
	class *GCClass
	roots []string // the known scratch roots that exist, resolved
	aiWhy string   // why the home's links name no AI root, when none was given
}

// GCAIRoot is the AI root a pass works under: aiRoot when it is a directory, else the one
// the home's <name>-working links name (resolved); "" none.
func GCAIRoot(home, aiRoot string) string {
	if fi, err := os.Stat(aiRoot); aiRoot != "" && err == nil && fi.IsDir() {
		return aiRoot
	}
	root, _ := gcLinkedAIRoot(home) // ignored: why there is none is the pass's to say
	return root
}

// gcLinkedAIRoot is the AI root the home's links name, as the machinery lays them out: a
// link <home>/<name>-working that resolves to <root>/<name>/working or
// <root>/buds/<name>/working names <root>. Every link that names one must name the same,
// else there is none and why says so; a link to anything else names nothing (the pass
// refuses it).
func gcLinkedAIRoot(home string) (root, why string) {
	es, err := os.ReadDir(home)
	if err != nil {
		return "", ""
	}
	var named []string
	for _, e := range es {
		name, ok := strings.CutSuffix(e.Name(), "-working")
		if !ok || name == "" || e.Type()&fs.ModeSymlink == 0 || !safepath.NameOK(e.Name()) {
			continue
		}
		real, err := filepath.EvalSymlinks(filepath.Join(home, e.Name()))
		if err != nil || filepath.Base(real) != "working" || filepath.Base(filepath.Dir(real)) != name {
			continue
		}
		r := filepath.Dir(filepath.Dir(real))
		if filepath.Base(r) == "buds" {
			r = filepath.Dir(r)
		}
		if !slices.Contains(named, r) {
			named = append(named, r)
		}
	}
	switch len(named) {
	case 0:
		return "", ""
	case 1:
		return named[0], ""
	}
	return "", "the home's working links name more than one AI root (" + strings.Join(named, ", ") + ")"
}

// GC runs one pass: the rule is the comment at the top of this file.
func GC(r GCReq) GCResult {
	if r.MaxAge <= 0 {
		r.MaxAge = GCMaxAge
	}
	if r.Cache.Limit == 0 {
		r.Cache = gocache.Bounds{Limit: gocache.Limit, Slack: gocache.Slack, Remove: 1 << 30}
	}
	p := &gcPass{r: r, res: GCResult{Volume: -1, Dry: r.Dry}}
	if real, why := p.knownRoot(r.AIRoot); real == "" && why == "" {
		p.r.AIRoot, p.aiWhy = gcLinkedAIRoot(r.Home)
	}
	for _, root := range []string{p.r.AIRoot, r.BenchRoot, r.LandRoot} {
		if real, _ := p.knownRoot(root); real != "" {
			p.roots = append(p.roots, real)
		}
	}
	p.begin(GCJobs)
	works := p.workDirs()
	for _, w := range works {
		p.jobs(w)
	}
	p.begin(GCReads)
	for _, w := range works {
		p.reads(w)
	}
	p.begin(GCLanders)
	p.landers()
	p.begin(GCBench)
	p.bench()
	p.begin(GCCache)
	p.caches(works)
	p.volume()
	return p.res
}

// begin starts a class's line.
func (p *gcPass) begin(name string) {
	p.res.Classes = append(p.res.Classes, GCClass{Name: name})
	p.class = &p.res.Classes[len(p.res.Classes)-1]
}

func (p *gcPass) say(format string, args ...any) {
	p.res.Detail = append(p.res.Detail, fmt.Sprintf(format, args...))
}

func (p *gcPass) refuse(path, why string) {
	p.class.Refused++
	p.say("GC REFUSED class=%s path=%s why=%s", p.class.Name, oneline.Escape(path), oneline.Escape(why))
}

func (p *gcPass) fail(path string, err error) {
	p.class.Failed++
	p.res.Failed++
	p.say("GC FAILED class=%s path=%s why=%s", p.class.Name, oneline.Escape(path), oneline.Escape(err.Error()))
}

// knownRoot is root resolved when it is a directory gc may work under, "" when it is
// empty or does not exist; why is set when it exists and is refused: the disk, the home or
// a directory above the home is never a scratch root.
func (p *gcPass) knownRoot(root string) (real, why string) {
	if strings.TrimSpace(root) == "" {
		return "", ""
	}
	real, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", ""
	}
	if fi, err := os.Stat(real); err != nil || !fi.IsDir() {
		return "", ""
	}
	if real == filepath.Dir(real) {
		return "", "a scratch root is never the whole disk"
	}
	if home, err := filepath.EvalSymlinks(p.r.Home); err == nil && (home == real || under(real, home)) {
		return "", "a scratch root is never the home or a directory above it"
	}
	return real, ""
}

// under says path is strictly below root, both resolved and clean.
func under(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// placed is dir resolved when it is strictly under a known scratch root, else why not.
func (p *gcPass) placed(dir string) (string, string) {
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", "it does not resolve: " + err.Error()
	}
	for _, root := range p.roots {
		if under(root, real) {
			return real, ""
		}
	}
	return "", "not under a known scratch root (" + strings.Join(p.roots, ", ") + ")"
}

// workDirs are the working directories on this machine under the AI root, each once: the
// home's <name>-working entries and the AI root's <name>/working and buds/<name>/working.
// A home entry that resolves outside the AI root is refused (said in the jobs class).
func (p *gcPass) workDirs() []string {
	var cands, refused, plain []string
	if es, err := os.ReadDir(p.r.Home); err == nil {
		for _, e := range es {
			if !strings.HasSuffix(e.Name(), "-working") || !safepath.NameOK(e.Name()) {
				continue
			}
			if e.IsDir() {
				// a plain directory, as a bench keeps a friend's: its own scratch root
				plain = append(plain, filepath.Join(p.r.Home, e.Name()))
			} else {
				cands = append(cands, filepath.Join(p.r.Home, e.Name()))
			}
		}
	}
	if p.r.AIRoot != "" {
		for _, glob := range []string{filepath.Join(p.r.AIRoot, "*", "working"), filepath.Join(p.r.AIRoot, "buds", "*", "working")} {
			ms, _ := filepath.Glob(glob) // ignored: the pattern is literal and well formed
			cands = append(cands, ms...)
		}
	}
	ai, aiWhy := p.knownRoot(p.r.AIRoot)
	if aiWhy != "" {
		p.refuse(p.r.AIRoot, aiWhy)
	}
	seen := map[string]bool{}
	var out []string
	for _, c := range plain {
		if real, why := p.knownRoot(c); real != "" && !seen[real] {
			seen[real] = true
			p.roots = append(p.roots, real)
			out = append(out, real)
		} else if why != "" {
			p.refuse(c, why)
		}
	}
	for _, c := range cands {
		real, err := filepath.EvalSymlinks(c)
		if err != nil {
			continue
		}
		if fi, err := os.Stat(real); err != nil || !fi.IsDir() {
			continue
		}
		if ai == "" || !under(ai, real) {
			refused = append(refused, c)
			continue
		}
		if !seen[real] {
			seen[real] = true
			out = append(out, real)
		}
	}
	root := "the AI root " + p.r.AIRoot
	switch {
	case p.aiWhy != "":
		root = p.aiWhy
	case ai == "":
		root = "no AI root: NOVA_AI_ROOT, --ai-root, ~/ai and the home's working links name none"
	}
	for _, c := range refused {
		p.refuse(c, "a working directory not under a known scratch root ("+root+")")
	}
	return out
}

// jobs takes the job directories of w whose lane is finished or absent.
func (p *gcPass) jobs(w string) {
	log := gcRunnerLog(w)
	es, err := os.ReadDir(filepath.Join(w, "jobs"))
	// ignored: a working directory with no jobs/ (or one it cannot read) has no job to take
	if err != nil {
		return
	}
	for _, e := range es {
		job := e.Name()
		if !e.IsDir() || strings.HasPrefix(job, ".") || !safepath.NameOK(job) {
			continue // a file, a link (never followed), a hidden or odd name: no job
		}
		dir := filepath.Join(w, "jobs", job)
		lane := GCLane(log, job)
		why := ""
		switch {
		case lane == GCLaneLive:
			continue
		case lane == GCLaneEnded:
			why = "its runner ended the lane"
		case p.aged(filepath.Join(w, "outbox", job, "REPORT.md"), GCReportGrace, true):
			why = "its report is written and no runner names its lane"
		case p.aged(dir, p.r.MaxAge, false):
			why = "no runner names its lane and it is older than " + p.r.MaxAge.String()
		default:
			continue
		}
		p.remove(dir, filepath.Join(w, "jobs"), why, true)
	}
}

// reads takes the reader checkouts of w whose finding is recorded.
func (p *gcPass) reads(w string) {
	es, err := os.ReadDir(filepath.Join(w, "reads"))
	// ignored: a working directory with no reads/ (or one it cannot read) has no read to take
	if err != nil {
		return
	}
	for _, e := range es {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") || !safepath.NameOK(e.Name()) {
			continue
		}
		dir := filepath.Join(w, "reads", e.Name())
		result := filepath.Join(dir, "RESULT.md")
		if !p.aged(result, GCReportGrace, true) || !gcVerdictRE.Match(gcHead(result)) {
			continue
		}
		p.remove(dir, filepath.Join(w, "reads"), "its finding is recorded", true)
	}
}

// gcVerdictRE is a read's recorded verdict, as the reader's daemon reads RESULT.md.
var gcVerdictRE = regexp.MustCompile(`(?im)^\s*verdict:\s*(ok|broken)\b`)

// gcHead is a file's first 64 KiB, nil when it cannot be read.
func gcHead(path string) []byte {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }() // ignored: only read
	b := make([]byte, 64<<10)
	n, _ := io.ReadFull(f, b) // ignored: a short file is its whole text
	return b[:n]
}

// landers takes the linked worktrees of land's clones older than the max age.
func (p *gcPass) landers() {
	if p.r.LandRoot == "" || p.r.Worktrees == nil {
		return
	}
	if _, why := p.knownRoot(p.r.LandRoot); why != "" {
		p.refuse(p.r.LandRoot, why)
		return
	}
	es, err := os.ReadDir(p.r.LandRoot)
	// ignored: a land root it cannot read has no worktree to take
	if err != nil {
		return
	}
	seen := map[string]bool{}
	for _, e := range es {
		clone := filepath.Join(p.r.LandRoot, e.Name())
		if !e.IsDir() || !gcIsClone(clone) {
			continue
		}
		for _, wt := range p.r.Worktrees(clone) {
			wt = filepath.Clean(wt)
			if seen[wt] || wt == clone {
				continue
			}
			seen[wt] = true
			if fi, err := os.Lstat(wt); err != nil || !fi.IsDir() {
				continue
			}
			if !p.aged(wt, p.r.MaxAge, false) {
				continue
			}
			p.remove(wt, filepath.Dir(wt), "a lander worktree older than "+p.r.MaxAge.String(), true)
		}
	}
}

// bench takes the bench root's run directories and the buds' job and read copies older
// than the max age. A clone under one keeps that directory when Dirty says the work
// is nowhere else, the same guard as every other removal.
func (p *gcPass) bench() {
	b := p.r.BenchRoot
	if b == "" {
		return
	}
	if _, why := p.knownRoot(b); why != "" {
		p.refuse(b, why)
		return
	}
	var dirs []string
	runs, _ := filepath.Glob(filepath.Join(b, "runs", "run.*")) // ignored: a literal pattern
	dirs = append(dirs, runs...)
	for _, kind := range []string{"jobs", "reads"} {
		ms, _ := filepath.Glob(filepath.Join(b, "buds", "*", kind, "*")) // ignored: a literal pattern
		dirs = append(dirs, ms...)
	}
	for _, d := range dirs {
		if fi, err := os.Lstat(d); err != nil || !fi.IsDir() || !safepath.NameOK(filepath.Base(d)) {
			continue
		}
		if p.aged(d, p.r.MaxAge, false) {
			p.remove(d, filepath.Dir(d), "a bench directory older than "+p.r.MaxAge.String(), true)
		}
	}
}

// caches holds every Go build cache of the bench root and of the working directories
// under its cap.
func (p *gcPass) caches(works []string) {
	var dirs []string
	if b := p.r.BenchRoot; b != "" {
		dirs = append(dirs, filepath.Join(b, "cache", "go-build"))
		ms, _ := filepath.Glob(filepath.Join(b, "buds", "*", "cache", "go-build")) // ignored: a literal pattern
		dirs = append(dirs, ms...)
	}
	for _, w := range works {
		dirs = append(dirs, filepath.Join(w, ".cache", "go-build"))
	}
	for _, d := range dirs {
		if fi, err := os.Lstat(d); err != nil || !fi.IsDir() {
			continue
		}
		if _, why := p.placed(d); why != "" {
			p.refuse(d, why)
			continue
		}
		b := p.r.Cache
		b.Dry = p.r.Dry
		c := gocache.Hold(d, p.r.Now, b)
		if c.Removed == 0 && c.Failed == 0 {
			continue
		}
		p.class.Count += c.Removed
		p.class.Bytes += c.Freed
		p.res.Freed += c.Freed
		action := "REMOVED"
		if p.r.Dry {
			action = "WOULD-REMOVE"
		}
		p.say("GC %s class=cache path=%s entries=%d bytes=%d size=%d limit=%d", action, oneline.Escape(d), c.Removed, c.Freed, c.Size, b.Limit)
		if c.Failed > 0 {
			p.class.Failed += c.Failed
			p.res.Failed += c.Failed
			p.say("GC FAILED class=cache path=%s failed=%d why=%s", oneline.Escape(d), c.Failed, oneline.Escape(c.Why))
		}
	}
}

// volume is the fullest volume of the known roots and the home.
func (p *gcPass) volume() {
	if p.r.Volume == nil {
		return
	}
	for _, d := range append(slices.Clone(p.roots), p.r.Home) {
		if d == "" {
			continue
		}
		if use, err := p.r.Volume(d); err == nil && use > p.res.Volume {
			p.res.Volume = use
		}
	}
}

// aged says path exists (a regular file when file) and its newest time (its own, and a
// directory's entries' one level down) is at least age before now.
func (p *gcPass) aged(path string, age time.Duration, file bool) bool {
	fi, err := os.Lstat(path)
	if err != nil || (file && !fi.Mode().IsRegular()) || (!file && !fi.IsDir()) {
		return false
	}
	newest := fi.ModTime()
	if !file {
		es, _ := os.ReadDir(path) // ignored: what cannot be read adds no time
		for _, e := range es {
			if i, err := e.Info(); err == nil && i.ModTime().After(newest) {
				newest = i.ModTime()
			}
		}
	}
	return p.r.Now.Sub(newest) >= age
}

// remove takes dir, strictly under classDir, when it resolves under a known scratch root
// and (clones) no clone inside it holds work that is nowhere else.
func (p *gcPass) remove(dir, classDir, why string, clones bool) {
	if _, refused := p.placed(dir); refused != "" {
		p.refuse(dir, refused)
		return
	}
	var size int64
	var keep, keepWhy string
	// ignored: the walk is never stopped by an error; a part it cannot read counts nothing
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.Type().IsRegular() {
			if fi, err := d.Info(); err == nil {
				size += fi.Size()
			}
			return nil
		}
		if !d.IsDir() || keep != "" || !clones || d.Name() == ".git" || !gcIsClone(path) || p.r.Dirty == nil {
			return nil
		}
		if w := p.r.Dirty(path); w != "" {
			keep, keepWhy = path, w
		}
		return nil
	})
	if keep != "" {
		p.class.Kept++
		p.say("GC KEPT class=%s path=%s why=%s", p.class.Name, oneline.Escape(keep), oneline.Escape(keepWhy))
		return
	}
	action := "WOULD-REMOVE"
	if !p.r.Dry {
		if err := safepath.RemoveUnderRoots(dir, classDir); err != nil {
			p.fail(dir, err)
			return
		}
		action = "REMOVED"
	}
	p.res.Removed = append(p.res.Removed, dir)
	p.class.Count++
	p.class.Bytes += size
	p.res.Freed += size
	p.say("GC %s class=%s path=%s bytes=%d why=%s", action, p.class.Name, oneline.Escape(dir), size, oneline.Escape(why))
}

// gcIsClone says dir holds a repository or a worktree: a .git directory or file.
func gcIsClone(dir string) bool {
	fi, err := os.Lstat(filepath.Join(dir, ".git"))
	return err == nil && (fi.IsDir() || fi.Mode().IsRegular())
}

// The states of a job's lane in its runner's log (GCLane).
const (
	GCLaneNone  = ""      // the log never names the job
	GCLaneLive  = "live"  // started, resumed or stopped at a limit (it runs again), not ended since
	GCLaneEnded = "ended" // its last event is an END
)

// GCLane is the state of a job's lane in a runner's log (`<time> START|RESUME|LIMIT|END
// <job> ...`, one event a line, as a bud's runner writes it; RunnerEnded reads the same).
func GCLane(log, job string) string {
	state := GCLaneNone
	for _, line := range strings.Split(log, "\n") {
		f := strings.Fields(line)
		for i := 0; i+1 < len(f); i++ {
			if f[i+1] != job {
				continue
			}
			switch f[i] {
			case "START", "RESUME", "LIMIT":
				state = GCLaneLive
			case "END":
				state = GCLaneEnded
			default:
				continue
			}
			break
		}
	}
	return state
}

// gcRunnerLog is the last gcLogCap bytes of the runner log of w (resolved): runner.log in
// it, else beside it (a bud's runner keeps <bud>/runner.log and <bud>/working); "" none.
func gcRunnerLog(w string) string {
	for _, path := range []string{filepath.Join(w, "runner.log"), filepath.Join(filepath.Dir(w), "runner.log")} {
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		fi, err := f.Stat()
		if err != nil || !fi.Mode().IsRegular() {
			_ = f.Close() // ignored: only read
			continue
		}
		off := max(fi.Size()-gcLogCap, 0)
		b := make([]byte, fi.Size()-off)
		n, _ := f.ReadAt(b, off) // ignored: a short read is a shorter log
		_ = f.Close()            // ignored: only read
		return string(b[:n])
	}
	return ""
}
