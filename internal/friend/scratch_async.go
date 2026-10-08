package friend

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/filelock"
	"github.com/mas-bandwidth/nova-tools/internal/safepath"
)

type cleanupPass struct {
	pending map[string]bool
	live    map[string]bool
	done    chan cleanupResult
}
type cleanupResult struct {
	jobs []string
	err  error
}

// PruneAsync reserves inactive scratch before returning. Classification, origin
// checks and bounded removal run away from the delivery loop. The next call drains
// the receipt; reservations remain until that receipt is consumed.
func (s *Stager) PruneAsync(ctx context.Context, live map[string]bool, kept int) ([]string, error) {
	s.cleanupMu.Lock()
	defer s.cleanupMu.Unlock()
	if s.cleanup != nil {
		s.cleanup.live = copyLive(live)
		select {
		case result := <-s.cleanup.done:
			s.cleanup = nil
			return result.jobs, result.err
		default:
			return nil, nil
		}
	}
	entries, err := os.ReadDir(filepath.Join(s.Dir, JobsDir))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	pass := &cleanupPass{pending: map[string]bool{}, live: copyLive(live), done: make(chan cleanupResult, 1)}
	for _, e := range entries {
		if validJob(e.Name()) && !live[e.Name()] && !exists(filepath.Join(s.Dir, "inbox", e.Name(), "BRIEF.md")) {
			pass.pending[e.Name()] = true
		}
	}
	releases := copyLive(s.releases)
	for id := range releases {
		pass.pending[id] = true
	}
	if len(pass.pending) == 0 {
		return nil, nil
	}
	s.cleanup = pass
	// Non-candidates remain protected even if they leave the row during this pass.
	protected := copyLive(live)
	for _, e := range entries {
		if !pass.pending[e.Name()] {
			protected[e.Name()] = true
		}
	}
	go func() {
		var jobs []string
		var first error
		ids := make([]string, 0, len(releases))
		for id := range releases {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			if len(jobs) == PrunePerPass {
				break
			}
			err := s.Release(ctx, id)
			if err == nil || errors.Is(err, errJobGone) {
				if err == nil {
					jobs = append(jobs, id)
				}
				s.cleanupMu.Lock()
				delete(s.releases, id)
				s.cleanupMu.Unlock()
			} else if !errors.Is(err, errHeadUnconfirmed) && !errors.Is(err, errCheckoutDirty) && !errors.Is(err, errMirrorHeld) {
				first = cmpErr(first, err)
			}
		}
		more, err := s.prune(ctx, protected, kept, PrunePerPass-len(jobs))
		jobs = append(jobs, more...)
		pass.done <- cleanupResult{jobs, cmpErr(first, err)}
	}()
	return nil, nil
}
func copyLive(live map[string]bool) map[string]bool {
	out := map[string]bool{}
	for k, v := range live {
		out[k] = v
	}
	return out
}
func (s *Stager) CleanupPending(job string) bool {
	s.cleanupMu.Lock()
	defer s.cleanupMu.Unlock()
	return s.releases[job] || (s.cleanup != nil && s.cleanup.pending[job])
}
func (s *Stager) cleanupLive(job string) bool {
	s.cleanupMu.Lock()
	defer s.cleanupMu.Unlock()
	return s.cleanup != nil && s.cleanup.live[job]
}
func (d *Daemon) cleanupPending(job string) bool {
	return d.CleanupPending != nil && d.CleanupPending(job)
}
func (s *Stager) mirrorRoot() string {
	if s.MirrorRoot != "" {
		return s.MirrorRoot
	}
	return filepath.Join(s.Dir, MirrorsDir)
}

// A kernel lock complements the per-Stager mutex: friends are separate processes.
func (s *Stager) lockMirror(repo string, try bool) (*filelock.FileLock, error) {
	path := filepath.Join(s.mirrorRoot()+".locks", repo+".lock")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, err
	}
	if try {
		return filelock.TryLock(path, "friend mirror "+repo)
	}
	return filelock.Lock(path, "friend mirror "+repo, 30*time.Second)
}

// StageRead creates a detached, self-contained reader clone from the same bare
// mirror. Local clone object files are hardlinks, so bench --with-git can copy it
// without depending on an alternates path on the originating machine.
func (s *Stager) StageRead(ctx context.Context, r AskedRead) error {
	if s.ReadCleanupPending(r.ID) {
		return fmt.Errorf("reader cleanup is still running")
	}
	repo := briefField(repoLine, r.Packet.Brief)
	if !validJob(r.ID) || !repoRE.MatchString(repo) || !shaRE.MatchString(r.Packet.Head) || !validRef(r.Packet.WorkBranch) || strings.Contains(repo, "..") {
		return fmt.Errorf("invalid reader checkout packet")
	}
	dir := filepath.Join(s.Dir, "reads", r.ID)
	checkout := filepath.Join(dir, "repo")
	marker := filepath.Join(dir, ".checkout-created")
	if exists(checkout) {
		raw, err := os.ReadFile(marker)
		if err != nil || string(raw) != checkout+"\n" {
			return fmt.Errorf("reader checkout was not created by this stager")
		}
		head, err := s.git(ctx, 0, "-C", checkout, "rev-parse", "HEAD")
		if err == nil && head == r.Packet.Head {
			return nil
		}
		if err = s.ReleaseRead(r.ID); err != nil {
			return err
		}
	}
	lock := s.repoLock(repo)
	lock.Lock()
	defer lock.Unlock()
	disk, err := s.lockMirror(repo, false)
	if err != nil {
		return err
	}
	defer disk.Unlock()
	mirror, err := s.mirror(ctx, repo)
	if err != nil {
		return err
	}
	if _, err = s.git(ctx, 0, "-C", mirror, "fetch", "--quiet", "origin", r.Packet.WorkBranch); err != nil {
		return err
	}
	if _, err = s.git(ctx, 0, "-C", mirror, "cat-file", "-e", r.Packet.Head+"^{commit}"); err != nil {
		return err
	}
	if err = os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	// Marker precedes creation so a killed clone is still recognizable on retry.
	if err = os.WriteFile(marker, []byte(checkout+"\n"), 0600); err != nil {
		return err
	}
	if _, err = s.git(ctx, 0, "clone", "--quiet", "--local", "--no-checkout", mirror, checkout); err != nil {
		return err
	}
	if _, err = s.git(ctx, 0, "-C", checkout, "remote", "set-url", "origin", s.url(repo)); err != nil {
		return err
	}
	_, err = s.git(ctx, 0, "-C", checkout, "checkout", "--quiet", "--detach", r.Packet.Head)
	return err
}

// ReleaseRead removes only this factory's checkout, retaining findings and receipts.
func (s *Stager) ReleaseRead(id string) error {
	if !validJob(id) {
		return fmt.Errorf("invalid read id")
	}
	dir := filepath.Join(s.Dir, "reads", id)
	checkout := filepath.Join(dir, "repo")
	if !exists(checkout) {
		return nil
	}
	if _, err := safepath.ResolvedUnder(dir, filepath.Join(s.Dir, "reads")); err != nil {
		return err
	}
	if _, err := safepath.ResolvedUnder(checkout, dir); err != nil {
		return err
	}
	raw, err := os.ReadFile(filepath.Join(dir, ".checkout-created"))
	if err != nil || string(raw) != checkout+"\n" {
		return fmt.Errorf("reader checkout has no matching creator receipt")
	}
	return safepath.RemoveUnder(dir, checkout)
}

var errCleanupQueued = errors.New("scratch cleanup queued")

func (s *Stager) ReleaseAsync(_ context.Context, job string) error {
	if !validJob(job) {
		return fmt.Errorf("invalid job id")
	}
	s.cleanupMu.Lock()
	defer s.cleanupMu.Unlock()
	if s.releases == nil {
		s.releases = map[string]bool{}
	}
	s.releases[job] = true
	return errCleanupQueued
}

// Reader completion also removes scratch away from the delivery loop. A queue
// sweep drains the result and retries failures; a reused read cannot race it.
func (s *Stager) ReleaseReadAsync(id string) error {
	if !validJob(id) {
		return fmt.Errorf("invalid read id")
	}
	s.cleanupMu.Lock()
	defer s.cleanupMu.Unlock()
	if s.readCleanup == nil {
		s.readCleanup = map[string]chan error{}
	}
	if done := s.readCleanup[id]; done != nil {
		select {
		case err := <-done:
			delete(s.readCleanup, id)
			return err
		default:
			return errCleanupQueued
		}
	}
	if !exists(filepath.Join(s.Dir, "reads", id, "repo")) {
		return nil
	}
	done := make(chan error, 1)
	s.readCleanup[id] = done
	go func() { done <- s.ReleaseRead(id) }()
	return errCleanupQueued
}
func (s *Stager) ReadCleanupPending(id string) bool {
	s.cleanupMu.Lock()
	defer s.cleanupMu.Unlock()
	return s.readCleanup[id] != nil
}
