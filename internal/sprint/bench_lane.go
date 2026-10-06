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

// The bench lanes (docs/SPEC-SPRINT.md section 18, bench lanes; the owner, 2026-10-02: cleanup
// must be automatic, the machinery removes what it creates, a scan is a check and not the
// mechanism). On 2026-10-06 a bench's shared /tmp (tmpfs, 61 GB) filled to 100%: every lane
// on it ran with the bench's /tmp as its TMPDIR, the go command's work directories and the
// tests' t.TempDir() went there, a gocache lived there, and nothing removed a lane's files
// at its end; three lanes and two reads failed with "no space left on device".
//
// A lane the machinery starts on a bench (a worker, a reader, a lander, a bench gate) is
// run by RunBenchLane, in order:
//
//  1. make: mkdir -p <root>/<kind>/<job>/tmp, the lane's own directory.
//  2. cap: the bench's one shared GOCACHE (BenchCacheDefault) is measured, and cleaned with
//     go clean -cache when it is over its cap (the sprint's bench_cache_gib, else
//     BenchCacheCapGiBDefault).
//  3. exec: cd <dir>/repo, then the command under nice -n 19 with TMPDIR and GOTMPDIR the
//     lane's tmp, GOCACHE the shared cache, GOFLAGS=-mod=readonly and NOVA_TEST_NO_HOST=1.
//  4. remove: rm -rf of the lane's directory and nothing else, deferred, whatever happened
//     in 1 to 3, under a context the caller's cancellation does not end.
//
// A lane killed before its remove (the process gone, the machine rebooted) is the next
// tick's: SweepBenchLanes removes each directory under the root whose lane is not live.
// ParseBenchTmp and BenchTmpJudgment are the check beside the mechanism: a bench whose
// /tmp is over BenchTmpOverPct raises one judgment naming it and its largest directories.

// The lane kinds a bench runs.
const (
	BenchLaneWorker = "worker"
	BenchLaneReader = "reader"
	BenchLaneLander = "lander"
	BenchLaneGate   = "gate"
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
	// BenchLaneStep bounds the make, cap, remove and sweep lines.
	BenchLaneStep = 2 * time.Minute
	// NBenchTmp is the judgment of a bench whose /tmp is over BenchTmpOverPct.
	NBenchTmp = "a bench's /tmp is nearly full"
)

// BenchShell is what a lane asks of the bench: one line under the login shell, its output
// streamed. The int is the remote status (NoBenchAnswer when the bench did not answer).
// internal/bench's ssh transport is one; the tests pass a fake.
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
// path element (no /, no .., no quote).
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

// Env is the environment the lane's command runs under.
func (l BenchLane) Env(cache string) []string {
	return []string{"TMPDIR=" + benchQuote(l.Tmp()), "GOTMPDIR=" + benchQuote(l.Tmp()), "GOCACHE=" + benchQuote(cache),
		"GOFLAGS=-mod=readonly", "NOVA_TEST_NO_HOST=1"}
}

// BenchLaneOptions is one lane's run.
type BenchLaneOptions struct {
	// Cache is the bench's shared GOCACHE, relative to the login's home; empty is
	// BenchCacheDefault. CacheCapGiB is its cap; 0 is BenchCacheCapGiBDefault.
	Cache       string
	CacheCapGiB int
	// Copy, when set, puts the tree into dst (the lane's <dir>/repo) after the make.
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
	if len(argv) == 0 {
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
			rerr = fmt.Errorf("%s: rm -rf of %s exited %d", host, res.Dir, code)
		}
		res.Removed, res.RemoveErr = rerr == nil, rerr
	}()
	step := func(line string, out io.Writer) (int, error) {
		sctx, cancel := context.WithTimeout(ctx, BenchLaneStep)
		defer cancel()
		return sh.Shell(sctx, host, line, out, stderr)
	}
	if code, err := step("mkdir -p "+benchQuote(lane.Tmp()), io.Discard); err != nil || code != 0 {
		res.Code = code
		return res, fmt.Errorf("%s: the lane's directory %s was not made (exit %d): %v", host, res.Dir, code, err)
	}
	if o.Copy != nil {
		if err := o.Copy(ctx, host, res.Dir+"/repo"); err != nil {
			res.Code = 1
			return res, fmt.Errorf("%s: copy into %s/repo: %w", host, res.Dir, err)
		}
	}
	var du bytes.Buffer
	if code, err := step("du -sk "+benchQuote(cache)+" 2>/dev/null || true", &du); err == nil && code == 0 && benchCacheOver(du.String(), capGiB) {
		if code, err := step("GOCACHE="+benchQuote(cache)+" go clean -cache", io.Discard); err == nil && code == 0 {
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
	if s.Work != nil {
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
	sctx, cancel := context.WithTimeout(ctx, BenchLaneStep)
	defer cancel()
	if code, err := sh.Shell(sctx, host, "ls -d "+benchQuote(BenchLaneRoot)+"/*/* 2>/dev/null || true", &ls, io.Discard); err != nil || code != 0 {
		return nil, fmt.Errorf("%s: the lanes under %s were not listed (exit %d): %v", host, BenchLaneRoot, code, err)
	}
	keep := map[string]bool{}
	for _, l := range live {
		keep[l.Dir()] = true
	}
	var gone []string
	for _, d := range strings.Fields(ls.String()) {
		rest, ok := strings.CutPrefix(d, BenchLaneRoot+"/")
		if !ok {
			continue
		}
		kind, job, ok := strings.Cut(rest, "/")
		if !ok || (BenchLane{Kind: kind, Job: job}).Check() != nil || keep[d] {
			continue
		}
		gone = append(gone, d)
	}
	slices.Sort(gone)
	var removed []string
	var errs []error
	for _, d := range gone {
		code, err := sh.Shell(sctx, host, "rm -rf -- "+benchQuote(d), io.Discard, io.Discard)
		if err == nil && code != 0 {
			err = fmt.Errorf("%s: rm -rf of %s exited %d", host, d, code)
		}
		if err != nil {
			errs = append(errs, err)
			continue
		}
		removed = append(removed, d)
	}
	return removed, errors.Join(errs...)
}

// BenchTmpLine is the line whose answer ParseBenchTmp reads: /tmp's use, then its largest
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

// ParseBenchTmp reads BenchTmpLine's answer on bench.
func ParseBenchTmp(bench, out string) (BenchTmp, error) {
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
		return t, fmt.Errorf("%s: no df line for /tmp in %q", bench, out)
	}
	return t, nil
}

// BenchTmpJudgment is the judgment of a bench whose /tmp is over BenchTmpOverPct, naming
// the bench and its largest directories: one a bench while one is open, none at or under.
func BenchTmpJudgment(s *Snapshot, who string, t BenchTmp) (Note, bool) {
	if t.UsedPct <= BenchTmpOverPct {
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
	what := fmt.Sprintf("%s/tmp is %d%% full, over %d%%; its largest: %s; a lane runs in %s and removes its own directory, so these are left by something else: remove what is no lane's, then run: ssh %s 'df -h /tmp'",
		head, t.UsedPct, BenchTmpOverPct, largest, BenchLaneRoot, t.Bench)
	return Note{Kind: Judgment, Type: NBenchTmp, Who: who, To: s.Coordinator, At: s.Now, What: what}, true
}

// benchQuote is s in single quotes for the bench's shell.
func benchQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }
