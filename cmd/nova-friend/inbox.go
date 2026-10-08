package main

import (
	"bufio"
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/friend"
)

// heldStamping is the daemon's Held with the session contract's inbox pass after each answer
// (docs/SPEC-FRIEND.md, the session contract): the epoch of the cards on her row is the
// contract's (a change pushes it to her session), and each work card whose job directory shows
// work begun (a worktree, a branch push) is stamped started once, so a friend whose children
// never run the start verb still counts as working. A nil held stays nil.
func (s *sessionContract) heldStamping(held func(context.Context) (friend.Row, error), progress func(ctx context.Context, argv []string) error, now func() time.Time, record func(string)) func(context.Context) (friend.Row, error) {
	if held == nil || s == nil {
		return held
	}
	return func(ctx context.Context) (friend.Row, error) {
		row, err := held(ctx)
		if err != nil {
			return row, err
		}
		if epoch := friend.RowEpoch(row.Cards); epoch != "" {
			k := s.teller.Contract()
			k.Epoch = epoch
			s.teller.Update(ctx, k)
		}
		if progress == nil {
			return row, nil
		}
		for _, owed := range s.starts.Pass(now(), row.Cards) {
			at := now().UTC().Format(time.RFC3339)
			if perr := progress(ctx, owed.Argv); perr != nil {
				s.starts.Failed(owed.Card.Job, now())
				record(at + " progress: card " + owed.Card.Card + " not stamped started (" + owed.Why + "): " + perr.Error() + "; sent again in " + friend.StartRetry.String())
				continue
			}
			record(at + " progress: card " + owed.Card.Card + " stamped started by the inbox pass: " + owed.Why)
		}
		return row, nil
	}
}

// jobSigns reads a held card's job directory, <dir>/jobs/<job>, for work begun: a checkout in
// it that the stager did not make (a clone or a worktree of the friend's, any directory with a
// .git but the staged repo), or the staged repo's index written after its JOB.md (a checkout, a
// commit); and a push of the card's branch (refs/remotes/origin/<branch>, loose, packed or
// logged, in any checkout's git directory). It reads files only: no git and no network.
func jobSigns(dir string) func(h friend.HeldCard) friend.JobSigns {
	return func(h friend.HeldCard) friend.JobSigns {
		var signs friend.JobSigns
		job := filepath.Join(dir, friend.JobsDir, h.Job)
		entries, err := os.ReadDir(job)
		if err != nil {
			return signs
		}
		staged, stagedErr := os.Stat(filepath.Join(job, friend.JobFile))
		branch := h.Branch
		if p, ok := friend.PacketOf(h); ok && branch == "" {
			branch = p.Branch
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			gitDir, common, ok := gitDirs(filepath.Join(job, e.Name()))
			if !ok {
				continue
			}
			switch {
			case e.Name() != "repo" || stagedErr != nil:
				signs.Worktree = true // a checkout the friend made
			default:
				if fi, err := os.Stat(filepath.Join(gitDir, "index")); err == nil && fi.ModTime().After(staged.ModTime()) {
					signs.Worktree = true // the staged worktree, worked in since the stage
				}
			}
			if branch != "" && pushed(common, branch) {
				signs.Pushed = true
			}
		}
		return signs
	}
}

// gitDirs is a checkout's git directory and its common directory: <path>/.git itself, or the
// directory a .git file names (a worktree), with the commondir it names.
func gitDirs(path string) (gitDir, common string, ok bool) {
	dotGit := filepath.Join(path, ".git")
	fi, err := os.Stat(dotGit)
	if err != nil {
		return "", "", false
	}
	if fi.IsDir() {
		return dotGit, dotGit, true
	}
	raw, err := os.ReadFile(dotGit)
	if err != nil {
		return "", "", false
	}
	gd, found := strings.CutPrefix(strings.TrimSpace(string(raw)), "gitdir: ")
	if !found || gd == "" {
		return "", "", false
	}
	if !filepath.IsAbs(gd) {
		gd = filepath.Join(path, gd)
	}
	common = gd
	if c, err := os.ReadFile(filepath.Join(gd, "commondir")); err == nil {
		common = strings.TrimSpace(string(c))
		if !filepath.IsAbs(common) {
			common = filepath.Join(gd, common)
		}
	}
	return gd, common, true
}

// pushed says the git directory holds origin's ref of branch: a push of it updated it.
func pushed(common, branch string) bool {
	ref := "refs/remotes/origin/" + branch
	for _, p := range []string{filepath.Join(common, filepath.FromSlash(ref)), filepath.Join(common, "logs", filepath.FromSlash(ref))} {
		if _, err := os.Stat(p); err == nil {
			return true
		}
	}
	f, err := os.Open(filepath.Join(common, "packed-refs"))
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }() // ignored: a read-only file
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if _, name, ok := strings.Cut(sc.Text(), " "); ok && name == ref {
			return true
		}
	}
	return false // a packed-refs that cannot be read to its end names no push
}
