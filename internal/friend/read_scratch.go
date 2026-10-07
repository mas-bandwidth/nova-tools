package friend

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/safepath"
)

// StageRead implements SPEC-FRIEND scratch: reader checkouts share the job mirror.
// A read is detached at its exact head and gains no mutable branch of its own.
func (s *Stager) StageRead(ctx context.Context, r AskedRead) error {
	repo := briefField(repoLine, r.Packet.Brief)
	if !validJob(r.ID) || !repoRE.MatchString(repo) || strings.Contains(repo, "..") || !fullSha.MatchString(r.Packet.Head) {
		return fmt.Errorf("read packet names no safe directory, repository or full head")
	}
	dir := filepath.Join(s.Dir, "reads", r.ID)
	if _, err := safepath.ResolvedUnder(dir, filepath.Join(s.Dir, "reads")); err != nil {
		return err
	}
	receipt, err := os.ReadFile(filepath.Join(dir, scratchReceipt))
	if err != nil || string(receipt) != r.ID {
		return errScratchRetained
	}
	lock := s.repoLock(repo)
	lock.Lock()
	defer lock.Unlock()
	mirror, err := s.mirror(ctx, repo)
	if err != nil {
		return err
	}
	checkout := filepath.Join(dir, "repo")
	if _, err := os.Lstat(checkout); err == nil {
		actualRepo, actualMirror, ok := s.worktreeOf(checkout)
		head, headErr := s.git(ctx, 0, "-C", checkout, "rev-parse", "HEAD")
		if !ok || actualRepo != repo || actualMirror != mirror || headErr != nil || head != r.Packet.Head {
			return errScratchRetained
		}
		return nil
	}
	_, err = s.git(ctx, 0, "-C", mirror, "worktree", "add", "--detach", "--", checkout, r.Packet.Head)
	return err
}

// ReleaseRead removes only the worktree StageRead made under its validated read directory.
// The caller first persists the finding outside that scratch and confirms delivery.
func (s *Stager) ReleaseRead(ctx context.Context, id string) error {
	if !validJob(id) {
		return errScratchRetained
	}
	dir := filepath.Join(s.Dir, "reads", id)
	if _, err := safepath.ResolvedUnder(dir, filepath.Join(s.Dir, "reads")); err != nil {
		return err
	}
	raw, err := os.ReadFile(filepath.Join(dir, scratchReceipt))
	if err != nil || string(raw) != id {
		return errScratchRetained
	}
	checkout := filepath.Join(dir, "repo")
	if _, err := os.Lstat(checkout); os.IsNotExist(err) {
		return nil
	}
	if _, err := safepath.ResolvedUnder(checkout, dir); err != nil {
		return err
	}
	repo, mirror, ok := s.worktreeOf(checkout)
	if !ok {
		return errScratchRetained
	}
	lock := s.repoLock(repo)
	lock.Lock()
	defer lock.Unlock()
	_, err = s.git(ctx, 0, "-C", mirror, "worktree", "remove", "--", checkout)
	return err
}
