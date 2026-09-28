package swarm

// cardout.go is the hand-off between a native run and the card wrapper (nova-card).
//
// The wrapper hands its harness NOVA_CARD_OUT and, at a DONE end, commits
// $NOVA_CARD_OUT/repo onto the attempt branch and reads the RESULT line from
// $NOVA_CARD_OUT/RESULT.md. A native run works somewhere else: the card's STEP 1
// clones into <slot>/jobs/<label>/repo (lint rule clone-step) and the card writes
// RESULT.md in that job directory, and the slot is the bench harness's choice, not a
// directory under the wrapper's job. Before this hand-off every DONE card on the native
// route ended NO-COMMIT with a real fix left behind in the slot (quack test,
// 2026-09-24): the wrapper had nothing at out/repo to commit.
//
// So native, when NOVA_CARD_OUT names a directory, moves the job's repo to
// $NOVA_CARD_OUT/repo and copies the card's RESULT.md to $NOVA_CARD_OUT/RESULT.md once
// the child has gone and the results are published. That keeps the wrapper's one
// contract (everything a card leaves is under NOVA_CARD_OUT) whatever slot the bench
// picks.

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// CardOutEnv names the card wrapper's out directory in a harness's environment.
const CardOutEnv = "NOVA_CARD_OUT"

// CardHandOff is what HandOffCardOut did: Repo and Result are the paths it wrote under
// the out directory ("" when it wrote none), Moved says the repo was renamed (false:
// copied, the cross-device case, or not handed off at all).
type CardHandOff struct {
	Repo   string
	Result string
	Moved  bool
}

// HandOffCardOut hands a native job's work to the card wrapper's out directory: the
// card's RESULT.md (as swarm.FindCardResult finds it) is copied to <out>/RESULT.md, and
// <job>/repo is moved to <out>/repo, by rename, or by copy when the two sit on different
// devices. An out directory that already holds either is left alone for that one (the
// harness's own copy wins). An empty out is no hand-off. An out that is inside the job,
// or that is not absolute, is refused: the wrapper would read what a sweep deletes.
func HandOffCardOut(job, out string) (CardHandOff, error) {
	var h CardHandOff
	if strings.TrimSpace(out) == "" {
		return h, nil
	}
	if !filepath.IsAbs(out) || !filepath.IsAbs(job) {
		return h, fmt.Errorf("%s %s and the job %s must be absolute", CardOutEnv, out, job)
	}
	if rel, err := filepath.Rel(job, out); err == nil && (rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))) {
		return h, fmt.Errorf("%s %s is inside the job directory %s", CardOutEnv, out, job)
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		return h, err
	}
	// RESULT.md first: FindCardResult looks in repo/ too, and the repo is about to move.
	if from, ok := FindCardResult(job); ok {
		to := filepath.Join(out, "RESULT.md")
		if _, err := os.Lstat(to); errors.Is(err, fs.ErrNotExist) {
			if err := copyTreeFile(from, to, 0o644); err != nil {
				return h, fmt.Errorf("RESULT.md: %w", err)
			}
			h.Result = to
		}
	}
	src := filepath.Join(job, "repo")
	fi, err := os.Lstat(filepath.Join(src, ".git"))
	if err != nil || fi.Mode()&fs.ModeSymlink != 0 {
		return h, nil // no clone of the card's own: nothing to commit
	}
	dst := filepath.Join(out, "repo")
	if _, err := os.Lstat(dst); !errors.Is(err, fs.ErrNotExist) {
		return h, nil
	}
	if err := os.Rename(src, dst); err == nil {
		h.Repo, h.Moved = dst, true
		return h, nil
	} else if !errors.Is(err, syscall.EXDEV) {
		return h, fmt.Errorf("repo: %w", err)
	}
	if err := copyRepoTree(src, dst); err != nil {
		return h, fmt.Errorf("repo copy: %w", err)
	}
	h.Repo = dst
	return h, nil
}

// copyRepoTree copies a repository: directories, regular files with their mode, and
// symlinks as symlinks (never followed). Anything else (a FIFO, a socket) is skipped.
func copyRepoTree(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		to := filepath.Join(dst, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case d.IsDir():
			return os.MkdirAll(to, info.Mode().Perm()|0o700)
		case info.Mode()&fs.ModeSymlink != 0:
			target, err := os.Readlink(p)
			if err != nil {
				return err
			}
			return os.Symlink(target, to)
		case info.Mode().IsRegular():
			return copyTreeFile(p, to, info.Mode().Perm())
		}
		return nil
	})
}

func copyTreeFile(from, to string, mode fs.FileMode) error {
	if err := statRegular(from); err != nil {
		return err
	}
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer in.Close()
	w, err := os.OpenFile(to, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(w, in); err != nil {
		w.Close()
		return err
	}
	return w.Close()
}
