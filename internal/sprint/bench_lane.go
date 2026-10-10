package sprint

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Bench lanes (docs/SPEC-SPRINT.md section 18, bench lanes). A lane the machinery
// starts on a bench (a worker, a reader, a lander, a bench gate) is run by
// RunBenchLane, in order:
//
//  1. make: mkdir -p <root>/<kind>/<job>/tmp, the lane's own directory.
//  2. cap: the bench's one shared GOCACHE is measured, and cleaned with
//     go clean -cache when it is over its cap (the sprint's bench_cache_gib,
//     else BenchCacheCapGiBDefault).
//  3. exec: cd <dir>/repo, then the command under nice -n 19 with TMPDIR and
//     GOTMPDIR the lane's tmp and GOCACHE the shared cache.
//  4. remove: the lane's directory and nothing else, deferred, whatever happened
//     in 1 to 3, under a context the caller's cancellation does not end.
//
// A lane killed before its remove is the next tick's: SweepBenchLanes removes
// each directory under the root whose lane is not live. BenchTmpFrom and
// BenchTmpJudgment are the check beside the mechanism: a bench whose /tmp is
// over BenchTmpOverPct raises one judgment naming it and its largest directories.

const (
	// BenchLaneWorker is a card's worker lane.
	BenchLaneWorker = "worker"
	// BenchLaneReader is a read's lane.
	BenchLaneReader = "reader"
	// BenchLaneLander is a lander's lane.
	BenchLaneLander = "lander"
	// BenchLaneGate is a bench gate's lane.
	BenchLaneGate = "gate"
)

// BenchLaneKinds is every bench lane kind, the directories under BenchLaneRoot.
var BenchLaneKinds = []string{BenchLaneWorker, BenchLaneReader, BenchLaneLander, BenchLaneGate}

const (
	// BenchLaneRoot is where the lanes' directories are made, relative to the login's home.
	BenchLaneRoot = "nova-bench/lanes"
	// BenchCacheDefault is the bench's one Go build cache, shared by every lane on it.
	BenchCacheDefault = "nova-bench/cache/go-build"
	// PropBenchCacheGiB is the work table's property: the shared cache's cap, whole GiB.
	PropBenchCacheGiB = "bench_cache_gib"
	// BenchCacheCapGiBDefault is the cap when the sprint sets none.
	BenchCacheCapGiBDefault = 20
	// BenchTmpOverPct is the share of a bench's /tmp above which it is a judgment.
	BenchTmpOverPct = 80
	// BenchTmpLargest is how many of /tmp's largest directories the judgment names.
	BenchTmpLargest = 5
	// NoBenchAnswer is ssh's own exit status: the bench did not answer.
	NoBenchAnswer = 255
	// BenchLaneStep bounds one make, cap, remove or sweep line.
	BenchLaneStep = 2 * time.Minute
	// NBenchTmp is the judgment of a bench whose /tmp is over BenchTmpOverPct.
	NBenchTmp = "a bench's /tmp is nearly full"
)

// BenchShell is what a lane asks of the bench: one line under the login shell, its output
// streamed. The int is the remote status (NoBenchAnswer when the bench did not answer).
// The tests pass a fake; a bench transport's shell is one.
type BenchShell interface {
	Shell(ctx context.Context, host, line string, stdout, stderr io.Writer) (int, error)
}

// BenchLane is one lane on a bench: its kind and its job, the directory's name.
type BenchLane struct {
	Kind string
	Job  string
}

var benchJobRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._~-]*$`)

// Check refuses a lane whose kind is not a bench lane kind or whose job is not one plain
// path element (no slash, no "..", no quote).
func (l BenchLane) Check() error {
	if !slices.Contains(BenchLaneKinds, l.Kind) {
		return fmt.Errorf("bench lane kind %q is not one of %s", l.Kind, strings.Join(BenchLaneKinds, ", "))
	}
	if !benchJobRe.MatchString(l.Job) || strings.Contains(l.Job, "..") {
		return fmt.Errorf("bench lane job %q is not one plain name (letters, digits, '.', '_', '~' and '-')", l.Job)
	}
	return nil
}

// Dir is the lane's directory on the bench, relative to the login's home.
func (l BenchLane) Dir() string { return BenchLaneRoot + "/" + l.Kind + "/" + l.Job }

// Tmp is the lane's TMPDIR and GOTMPDIR, inside its directory.
func (l BenchLane) Tmp() string { return l.Dir() + "/tmp" }

// Env is the environment the lane's command runs under. TMPDIR, GOTMPDIR and
// GOCACHE are named as paths under the login's home (benchHome), so they resolve
// against the home even though the command has cd'd into the checkout.
func (l BenchLane) Env(cache string) []string {
	return []string{
		"TMPDIR=" + benchHome(l.Tmp()),
		"GOTMPDIR=" + benchHome(l.Tmp()),
		"GOCACHE=" + benchHome(cache),
		"GOFLAGS=-mod=readonly",
		"NOVA_TEST_NO_HOST=1",
	}
}

// benchHome is a bench path (relative to the login's home) as the shell reads it:
// "$HOME"/<path>, so it names the home however the login spells it; an absolute
// path is kept as it is.
func benchHome(p string) string {
	if strings.HasPrefix(p, "/") {
		return benchQuote(p)
	}
	return `"$HOME"/` + benchQuote(p)
}

// BenchLaneOptions is one lane's run.
type BenchLaneOptions struct {
	// Cache is the bench's shared GOCACHE, relative to the login's home; empty is
	// BenchCacheDefault. CacheCapGiB is its cap; 0 is BenchCacheCapGiBDefault.
	Cache       string
	CacheCapGiB int
	// Copy, when set, puts the tree into the lane's <dir>/repo after the make.
	Copy func(ctx context.Context, host, dst string) error
	// Stdout and Stderr receive the command's output.
	Stdout, Stderr io.Writer
}

// BenchLaneResult is what a lane did.
type BenchLaneResult struct {
	Dir          string
	Code         int
	CacheCleaned bool
	Removed      bool
	RemoveErr    error
}

// RunBenchLane runs argv as lane on host: make, cap, exec, and the deferred remove of the
// lane's directory whatever the verdict. The result's Code is the command's status; err
// says why the lane did not run it to its end (a make refused, a copy failed, the bench
// gone, the context ended).
func RunBenchLane(ctx context.Context, sh BenchShell, host string, lane BenchLane, o BenchLaneOptions, argv []string) (res BenchLaneResult, err error) {
	if err := lane.Check(); err != nil {
		return res, err
	}
	if len(argv) == 0 || argv[0] == "" {
		return res, errors.New("bench lane: no command")
	}
	cache := o.Cache
	if cache == "" {
		cache = BenchCacheDefault
	}
	capGiB := o.CacheCapGiB
	if capGiB <= 0 {
		capGiB = BenchCacheCapGiBDefault
	}
	stdout, stderr := o.Stdout, o.Stderr
	if stdout == nil {
		stdout = io.Discard
	}
	if stderr == nil {
		stderr = io.Discard
	}
	res.Dir = lane.Dir()
	defer func() {
		rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), BenchLaneStep)
		defer cancel()
		code, rerr := sh.Shell(rctx, host, "rm -rf -- "+benchQuote(res.Dir), io.Discard, stderr)
		if rerr == nil && code != 0 {
			rerr = fmt.Errorf("%s: rm of %s exited %d", host, res.Dir, code)
		}
		res.Removed, res.RemoveErr = rerr == nil, rerr
	}()
	if code, err := benchShell(ctx, sh, host, "mkdir -p "+benchQuote(lane.Tmp()), io.Discard, stderr); err != nil || code != 0 {
		res.Code = code
		if err == nil {
			err = fmt.Errorf("exit %d", code)
		}
		return res, fmt.Errorf("%s: the lane's directory %s was not made: %w", host, res.Dir, err)
	}
	if o.Copy != nil {
		if err := o.Copy(ctx, host, res.Dir+"/repo"); err != nil {
			res.Code = 1
			return res, fmt.Errorf("%s: copy into %s/repo: %w", host, res.Dir, err)
		}
	}
	var du bytes.Buffer
	if code, err := benchShell(ctx, sh, host, "du -sk "+benchQuote(cache)+" 2>/dev/null || true", &du, stderr); err == nil && code == 0 && benchCacheOver(du.String(), capGiB) {
		if code, err := benchShell(ctx, sh, host, "GOCACHE="+benchQuote(cache)+" go clean -cache", io.Discard, stderr); err == nil && code == 0 {
			res.CacheCleaned = true
		}
	}
	quoted := make([]string, len(argv))
	for i, a := range argv {
		quoted[i] = benchQuote(a)
	}
	line := "cd " + benchQuote(res.Dir+"/repo") + " && export " + strings.Join(lane.Env(cache), " ") +
		" && nice -n 19 " + strings.Join(quoted, " ")
	res.Code, err = sh.Shell(ctx, host, line, stdout, stderr)
	return res, err
}

// benchCacheOver says du -sk's answer is over capGiB.
func benchCacheOver(du string, capGiB int) bool {
	f := strings.Fields(du)
	if len(f) == 0 {
		return false
	}
	kb, err := strconv.ParseInt(f[0], 10, 64)
	return err == nil && kb > int64(capGiB)<<20
}

// BenchCacheCapGiB is the shared cache's cap: the sprint's bench_cache_gib, a whole
// number from 1, else BenchCacheCapGiBDefault.
func (s *Snapshot) BenchCacheCapGiB() int {
	if s != nil && s.Work != nil {
		if v, ok := s.Work.Prop(PropBenchCacheGiB); ok {
			if n, err := strconv.Atoi(v); err == nil && n >= 1 {
				return n
			}
		}
	}
	return BenchCacheCapGiBDefault
}

// SweepBenchLanes removes, on host, each lane directory under BenchLaneRoot whose lane is
// not in live: a lane killed before its own remove. It names what it removed, sorted.
func SweepBenchLanes(ctx context.Context, sh BenchShell, host string, live []BenchLane) ([]string, error) {
	var ls bytes.Buffer
	if code, err := benchShell(ctx, sh, host, "ls -d "+benchQuote(BenchLaneRoot)+"/*/* 2>/dev/null || true", &ls, io.Discard); err != nil || code != 0 {
		if err == nil {
			err = fmt.Errorf("exit %d", code)
		}
		return nil, fmt.Errorf("%s: the lanes under %s were not listed: %w", host, BenchLaneRoot, err)
	}
	keep := map[string]bool{}
	for _, l := range live {
		if l.Check() == nil {
			keep[l.Dir()] = true
		}
	}
	var gone []string
	for _, d := range strings.Fields(ls.String()) {
		rest, ok := strings.CutPrefix(d, BenchLaneRoot+"/")
		if !ok {
			continue
		}
		kind, job, ok := strings.Cut(rest, "/")
		if !ok || strings.Contains(job, "/") || (BenchLane{Kind: kind, Job: job}).Check() != nil || keep[d] {
			continue
		}
		gone = append(gone, d)
	}
	slices.Sort(gone)
	var removed []string
	var errs []error
	for _, d := range gone {
		code, err := benchShell(ctx, sh, host, "rm -rf -- "+benchQuote(d), io.Discard, io.Discard)
		if err == nil && code != 0 {
			err = fmt.Errorf("%s: rm of %s exited %d", host, d, code)
		}
		if err != nil {
			errs = append(errs, err)
			continue
		}
		removed = append(removed, d)
	}
	return removed, errors.Join(errs...)
}

// BenchTmpLine is the line whose answer BenchTmpFrom reads: /tmp's use, then its largest
// entries in KiB, largest first.
const BenchTmpLine = "df -Pk /tmp | tail -n 1; du -xsk /tmp/* 2>/dev/null | sort -rn | head -n 5"

// BenchDir is one directory of a bench's /tmp and its size.
type BenchDir struct {
	Path string
	KiB  int64
}

// BenchTmp is a bench's /tmp: how full, and its largest directories.
type BenchTmp struct {
	Bench   string
	UsedPct int
	Largest []BenchDir
}

// BenchTmpFrom reads BenchTmpLine's answer on bench. A refusal names no input:
// the answer can carry a path, and an error must not.
func BenchTmpFrom(bench, out string) (BenchTmp, error) {
	t := BenchTmp{Bench: bench}
	seen := false
	for _, l := range strings.Split(out, "\n") {
		f := strings.Fields(l)
		switch {
		case len(f) >= 6 && strings.HasSuffix(f[4], "%"):
			n, err := strconv.Atoi(strings.TrimSuffix(f[4], "%"))
			if err == nil {
				t.UsedPct, seen = n, true
			}
		case len(f) == 2:
			if kb, err := strconv.ParseInt(f[0], 10, 64); err == nil && len(t.Largest) < BenchTmpLargest {
				t.Largest = append(t.Largest, BenchDir{Path: f[1], KiB: kb})
			}
		}
	}
	if !seen {
		return t, errors.New("no df line for /tmp")
	}
	return t, nil
}

// BenchTmpJudgment is the judgment of a bench whose /tmp is over BenchTmpOverPct, naming
// the bench and its largest directories: one a bench while one is open, none at or under.
func BenchTmpJudgment(s *Snapshot, who string, t BenchTmp) (Note, bool) {
	if s == nil || t.UsedPct <= BenchTmpOverPct {
		return Note{}, false
	}
	head := "bench " + t.Bench + ": "
	for _, o := range s.Open {
		if o.Note.Type == NBenchTmp && strings.HasPrefix(o.Note.What, head) {
			return Note{}, false
		}
	}
	var dirs []string
	for _, d := range t.Largest {
		dirs = append(dirs, fmt.Sprintf("%s %.1f GiB", d.Path, float64(d.KiB)/(1<<20)))
	}
	largest := "none listed"
	if len(dirs) > 0 {
		largest = strings.Join(dirs, ", ")
	}
	what := fmt.Sprintf("%s/tmp is %d%% full, over %d%%; its largest: %s; a lane runs in %s and removes its own directory, so these are left by something else",
		head, t.UsedPct, BenchTmpOverPct, largest, BenchLaneRoot)
	return Note{Kind: Judgment, Type: NBenchTmp, Primaries: []string{t.Bench}, Who: who, To: s.Coordinator, At: s.Now, What: what}, true
}

func benchShell(ctx context.Context, sh BenchShell, host, line string, stdout, stderr io.Writer) (int, error) {
	sctx, cancel := context.WithTimeout(ctx, BenchLaneStep)
	defer cancel()
	return sh.Shell(sctx, host, line, stdout, stderr)
}

// benchQuote is s in single quotes for the bench's shell.
func benchQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }
