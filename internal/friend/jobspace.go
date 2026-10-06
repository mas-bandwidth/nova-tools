package friend

import (
	"context"
	"errors"
	"fmt"
	"github.com/mas-bandwidth/nova-tools/internal/safepath"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// DefaultJobsCap is the scratch limit in bytes (SPEC-FRIEND, jobs capacity).
const DefaultJobsCap int64 = 20 << 30

// JobCapacity is the jobs tree and the available capacity of its volume.
// Inodes is nil on volumes that report no inode count.
type JobCapacity struct {
	Jobs, Free int64
	Inodes     *int64
	Error      string
}

// JobsBytes measures scratch without following links (SPEC-FRIEND, jobs capacity).
func JobsBytes(dir string) (int64, error) {
	root := filepath.Join(dir, JobsDir)
	fi, err := os.Lstat(root)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if !fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 {
		return 0, fmt.Errorf("jobs wants a directory without a symlink: %s", root)
	}
	return treeBytes(root)
}

func treeBytes(root string) (int64, error) {
	var bytes int64
	err := filepath.WalkDir(root, func(path string, e fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		fi, err := e.Info()
		if err != nil {
			return err
		}
		bytes += fi.Size()
		return nil
	})
	return bytes, err
}

// MeasureJobCapacity uses the filesystem's available counts (SPEC-FRIEND, jobs capacity).
func MeasureJobCapacity(dir string) (JobCapacity, error) {
	return measureJobCapacity(dir, volumeCapacity)
}

func measureJobCapacity(dir string, volume func(string) (int64, *int64, error)) (JobCapacity, error) {
	jobs, err := JobsBytes(dir)
	if err != nil {
		return JobCapacity{}, err
	}
	free, inodes, err := volume(dir)
	return JobCapacity{Jobs: jobs, Free: free, Inodes: inodes}, err
}

// Admit refuses scratch that would reach or pass the cap, before a lane starts.
// Staging additionally reserves the next worktree's size (SPEC-FRIEND, jobs capacity).
func (s *Stager) Admit(extra int64) error {
	jobs, err := JobsBytes(s.Dir)
	if err != nil {
		return err
	}
	cap := s.jobsCap()
	if extra < 0 || jobs >= cap || extra > cap-jobs {
		return fmt.Errorf("jobs cap %d bytes refuses a new lane: jobs=%d reserve=%d; finish and publish work, then run nova-friend gc", cap, jobs, extra)
	}
	return nil
}
func (s *Stager) jobsCap() int64 {
	if s.JobsCap > 0 {
		return s.JobsCap
	}
	return DefaultJobsCap
}

// GCResult preserves one count per class, including deletion refusals that defer.
type GCResult struct {
	Freed, Planned, Jobs, Cap int64
	Classes                   map[string]int
	Removed                   []string
	Notes                     []string
}

// GC removes only owned, clean, reported, origin-confirmed jobs (SPEC-FRIEND,
// jobs capacity). The live map and every inbox/mark keep an active lane. The oldest
// finished jobs go first, with a bounded number of removals per pass.
func (s *Stager) GC(ctx context.Context, live map[string]bool, dry bool, kept int) (GCResult, error) {
	result := GCResult{Cap: s.jobsCap(), Classes: map[string]int{"active": 0, "unowned": 0, "unpublished": 0, "removed": 0, "deferred": 0}}
	if !s.admission.TryLock() {
		result.Classes["deferred"]++
		var err error
		result.Jobs, err = JobsBytes(s.Dir)
		return result, err
	}
	defer s.admission.Unlock()
	root := filepath.Join(s.Dir, JobsDir)
	if _, err := JobsBytes(s.Dir); err != nil {
		return result, err
	}
	inbox := filepath.Join(s.Dir, "inbox")
	if fi, err := os.Lstat(inbox); err == nil {
		if !fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 {
			return result, fmt.Errorf("inbox is not a readable owned directory")
		}
		if _, err := os.ReadDir(inbox); err != nil {
			return result, err
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return result, err
	}
	entries, err := os.ReadDir(root)
	if errors.Is(err, fs.ErrNotExist) {
		return result, nil
	}
	if err != nil {
		return result, err
	}
	type candidate struct {
		job, repo, mirror string
		at                time.Time
	}
	var done []candidate
	for _, e := range entries {
		job := e.Name()
		if !e.IsDir() || !validJob(job) {
			result.Classes["unowned"]++
			continue
		}
		mark, marked := ReadLaneMark(s.Dir, job)
		if live[job] || s.localJobProtected(job, mark, marked) {
			result.Classes["active"]++
			continue
		}
		repo, mirror, ok := s.worktreeOf(filepath.Join(root, job, "repo"))
		if !ok {
			repo, ok = s.ownedClone(job)
			if !ok {
				result.Classes["unowned"]++
				continue
			}
		}
		fi, err := os.Lstat(filepath.Join(root, job, JobFile))
		if err != nil || !fi.Mode().IsRegular() {
			result.Classes["unowned"]++
			continue
		}
		done = append(done, candidate{job, repo, mirror, fi.ModTime()})
	}
	sort.Slice(done, func(i, j int) bool {
		if !done[i].at.Equal(done[j].at) {
			return done[i].at.Before(done[j].at)
		}
		return done[i].job < done[j].job
	})
	eligible := done[:max(len(done)-max(kept, 0), 0)]
	if len(eligible) > 0 {
		start := s.gcCursor % len(eligible)
		window := min(32, len(eligible))
		eligible = append(append([]candidate{}, eligible[start:]...), eligible[:start]...)[:window]
		s.gcCursor = (start + min(window, PrunePerPass)) % len(done)
	}
	proofs := 0
	for _, c := range eligible {
		if len(result.Removed) >= PrunePerPass {
			result.Classes["deferred"]++
			continue
		}
		lock := s.repoLock(c.repo)
		if !lock.TryLock() {
			result.Classes["deferred"]++
			continue
		}
		if proofs >= PrunePerPass {
			lock.Unlock()
			result.Classes["deferred"]++
			continue
		}
		proofs++
		safe, why := s.publishedJob(ctx, c.job, c.repo)
		if !safe {
			lock.Unlock()
			result.Classes["unpublished"]++
			if why != "" {
				result.Notes = append(result.Notes, c.job+": "+why)
			}
			continue
		}
		// Recheck local activity after the potentially slow origin proof.
		mark, marked := ReadLaneMark(s.Dir, c.job)
		if s.localJobProtected(c.job, mark, marked) {
			lock.Unlock()
			result.Classes["active"]++
			continue
		}
		bytes, err := treeBytes(JobDir(s.Dir, c.job))
		if err == nil && !dry {
			if c.mirror != "" {
				err = s.pruneJob(ctx, c.mirror, c.job)
			} else {
				err = safepath.RemoveUnder(root, JobDir(s.Dir, c.job))
			}
		}
		lock.Unlock()
		if err != nil {
			result.Classes["deferred"]++
			result.Notes = append(result.Notes, c.job+": deferred: "+err.Error())
			continue
		}
		result.Removed = append(result.Removed, c.job)
		if dry {
			result.Planned += bytes
		} else {
			result.Classes["removed"]++
			result.Freed += bytes
		}
	}
	result.Jobs, err = JobsBytes(s.Dir)
	return result, err
}

// publishedJob requires the report's head, current clean HEAD, and origin tip to
// agree; a stale remote-tracking ref proves nothing (SPEC-FRIEND, jobs capacity).
func (s *Stager) publishedJob(ctx context.Context, job, repo string) (bool, string) {
	checkout := filepath.Join(JobDir(s.Dir, job), "repo")
	for _, path := range []string{JobDir(s.Dir, job), checkout} {
		fi, err := os.Lstat(path)
		if err != nil || !fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 {
			return false, "directory is not owned scratch"
		}
	}
	jobPath := filepath.Join(JobDir(s.Dir, job), JobFile)
	jobInfo, err := os.Lstat(jobPath)
	if err != nil || !jobInfo.Mode().IsRegular() || jobInfo.Size() > ReportCap {
		return false, "JOB is not bounded regular ownership evidence"
	}
	rawJob, err := os.ReadFile(jobPath)
	if err != nil {
		return false, "JOB ownership cannot be read"
	}
	canonical := string(rawJob)
	if !strings.Contains(canonical, "The staged checkout: "+checkout+" (a git worktree of "+repo+" at ") && !strings.Contains(canonical, "The staged checkout: "+checkout+" (a clone of "+repo+" at ") {
		return false, "JOB does not name the owned checkout"
	}
	if _, mirror, ok := s.worktreeOf(checkout); ok {
		for _, path := range []string{filepath.Join(s.Dir, MirrorsDir), filepath.Dir(mirror), mirror} {
			fi, err := os.Lstat(path)
			if err != nil || !fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 {
				return false, "mirror ownership contains a symlink or unreadable directory"
			}
		}
	}
	report := filepath.Join(s.Dir, "outbox", job, "REPORT.md")
	fi, err := os.Lstat(report)
	if err != nil || !fi.Mode().IsRegular() || fi.Size() > ReportCap {
		return false, "report is missing or not a bounded regular file"
	}
	raw, err := os.ReadFile(report)
	if err != nil {
		return false, err.Error()
	}
	verdict, head := reportVerdict(string(raw))
	if (verdict != "LAND" && verdict != "HOLD" && verdict != "FAIL") || !shaRE.MatchString(head) {
		return false, "report has no verdict and published head"
	}
	remote, err := s.git(ctx, 0, "-C", checkout, "remote", "get-url", "origin")
	if err != nil || remote != s.url(repo) {
		return false, "checkout origin is not the owned repository"
	}
	status, err := s.git(ctx, 0, "-C", checkout, "status", "--porcelain", "--untracked-files=all")
	if err != nil || status != "" {
		return false, "checkout has uncommitted work or cannot be read"
	}
	current, err := s.git(ctx, 0, "-C", checkout, "rev-parse", "HEAD")
	if err != nil || current != head {
		return false, "report head is not the checkout head"
	}
	branch, err := s.git(ctx, 0, "-C", checkout, "symbolic-ref", "--short", "HEAD")
	if err != nil || !validRef(branch) {
		return false, "checkout names no branch"
	}
	tip, err := s.Tip(ctx, repo, branch)
	if err != nil {
		return false, "origin proof deferred: " + err.Error()
	}
	if tip != head {
		return false, "reported head is not on origin"
	}
	return true, ""
}

// reserveWorktree uses blob sizes plus directory/metadata allowance, before
// creating any job scratch (SPEC-FRIEND, jobs capacity).
func (s *Stager) reserveWorktree(ctx context.Context, mirror, sha string) error {
	out, err := s.git(ctx, 0, "-C", mirror, "ls-tree", "-rl", sha)
	if err != nil {
		return err
	}
	reserve := int64(1 << 20)
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		n, err := strconv.ParseInt(fields[3], 10, 64)
		if err != nil {
			return err
		}
		reserve += n + 4096
	}
	return s.Admit(reserve)
}

// ownedClone recognizes the legacy stager's explicit JOB path and repository,
// and checks the clone's origin before adopting scratch (SPEC-FRIEND, jobs capacity).
func (s *Stager) ownedClone(job string) (string, bool) {
	dir := JobDir(s.Dir, job)
	checkout := filepath.Join(dir, "repo")
	for _, path := range []string{checkout, filepath.Join(checkout, ".git")} {
		fi, err := os.Lstat(path)
		if err != nil || !fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 {
			return "", false
		}
	}
	path := filepath.Join(dir, JobFile)
	fi, err := os.Lstat(path)
	if err != nil || !fi.Mode().IsRegular() || fi.Size() > ReportCap {
		return "", false
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	text := string(raw)
	if !strings.Contains(text, "The staged checkout: "+checkout+" (a clone of ") {
		return "", false
	}
	_, tail, ok := strings.Cut(text, "(a clone of ")
	if !ok {
		return "", false
	}
	repo, _, ok := strings.Cut(tail, " at ")
	if !ok || !repoRE.MatchString(repo) {
		return "", false
	}
	return repo, true
}
func (s *Stager) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// Any ambiguous local activity evidence protects scratch, including malformed marks.
func (s *Stager) localJobProtected(job string, mark LaneMark, marked bool) bool {
	_, err := os.Lstat(filepath.Join(s.Dir, "inbox", job))
	if !errors.Is(err, fs.ErrNotExist) {
		return true
	}
	if marked {
		return !mark.Ended && (mark.At.IsZero() || s.now().Sub(mark.At) < LaneMarkStale)
	}
	_, err = os.Lstat(laneMarkPath(s.Dir, job))
	return !errors.Is(err, fs.ErrNotExist)
}

// ClaimJob serializes a fresh lane's mark with staging and deletion. A reported
// job cannot be claimed even if it was selected before its report appeared.
func (s *Stager) ClaimJob(job, who string, now time.Time) (string, error) {
	if !s.admission.TryLock() {
		return "job collection or staging", nil
	}
	defer s.admission.Unlock()
	for _, file := range []string{"REPORT.md", "RESULT.md"} {
		_, err := os.Lstat(filepath.Join(s.Dir, "outbox", job, file))
		if !errors.Is(err, fs.ErrNotExist) {
			return "reported job", nil
		}
	}
	return ClaimLane(s.Dir, job, who, now)
}

type capacityResult struct {
	jobs int64
	err  error
	at   time.Time
}

// CapacityAdmission measures afresh for each prospective claim, asynchronously.
// It consumes each result once and refuses old results, preserving beat liveness.
type CapacityAdmission struct {
	Dir     string
	Cap     int64
	Now     func() time.Time
	Measure func(string) (int64, error)
	pending chan capacityResult
	wg      sync.WaitGroup
}

func (a *CapacityAdmission) Check(ctx context.Context, _ Card) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if a.pending != nil {
		select {
		case sample := <-a.pending:
			a.pending = nil
			if a.Now().Sub(sample.at) > 2*BeatEvery {
				return fmt.Errorf("jobs cap %d bytes refuses a new lane: admission measurement expired", a.Cap)
			}
			if sample.err != nil {
				return fmt.Errorf("jobs cap %d bytes refuses a new lane: %w", a.Cap, sample.err)
			}
			if sample.jobs >= a.Cap {
				return fmt.Errorf("jobs cap %d bytes refuses a new lane: jobs=%d", a.Cap, sample.jobs)
			}
			return nil
		default:
			return fmt.Errorf("jobs cap %d bytes refuses a new lane until fresh admission measurement completes", a.Cap)
		}
	}
	a.pending = make(chan capacityResult, 1)
	pending := a.pending
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		jobs, err := a.Measure(a.Dir)
		pending <- capacityResult{jobs: jobs, err: err, at: a.Now()}
	}()
	return fmt.Errorf("jobs cap %d bytes refuses a new lane until fresh admission measurement completes", a.Cap)
}

// Wait joins the admission measurement during a daemon's orderly stop.
func (a *CapacityAdmission) Wait() { a.wg.Wait() }
