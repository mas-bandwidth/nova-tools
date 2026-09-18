// Package swarm, warm.go: affinity and warm state (docs/SPEC-JOBS.md section 4).
//
// Every card carries a kind and a repo. A bench keeps a clone per repo in a worktree
// under <slot>/worktrees/<owner>/<name>, and pull prefers the card whose repo it already
// has cloned there rather than paying the setup again: cache warmth is the difference
// between a setup and no setup. With no warm bench it falls back to a fetch from the
// bench mirror.
//
// A kept clone is reused across cards. After each card nova-work clip commits the card's
// branch, harvests the card's result, and resets the worktree to base, so the clone is
// reset rather than re-cloned and the next card never sees this card's uncommitted state
// or its history.
package swarm

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Card is the affinity facts a card carries: its kind ("go", "lisp", "docs",
// "schema-leg") and the repo ("owner/name") it runs against.
type Card struct {
	Name string
	Kind string
	Repo string
}

// PullBench is one bench pull may take a card from: its slot directory, the queue in pull
// order, and the bench mirror a cold clone fetches from.
type PullBench struct {
	Name   string
	Slot   string
	Mirror string
	Queue  []Card
}

// Pull is the card pull takes and where it comes from. Warm is true when the bench
// already held the repo in a kept worktree, so the clone is reused; otherwise Fetch names
// the bench mirror the cold clone comes from.
type Pull struct {
	Bench string
	Slot  string
	Card  Card
	Warm  bool
	Path  string
	Fetch string
}

// WorktreePath is the kept worktree for a repo under a slot:
// <slot>/worktrees/<owner>/<name>, the one place warmth lives. A repo that is not
// owner/name has no worktree and returns "".
func WorktreePath(slot, repo string) string {
	owner, name, ok := splitRepo(repo)
	if !ok || strings.TrimSpace(slot) == "" {
		return ""
	}
	return filepath.Join(slot, "worktrees", owner, name)
}

// splitRepo reads a repo as owner/name, the only shape that has a worktree.
func splitRepo(repo string) (owner, name string, ok bool) {
	parts := strings.Split(strings.TrimSpace(repo), "/")
	if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
		return "", "", false
	}
	return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1]), true
}

// HoldsRepo reports whether slot already holds repo cloned in a kept worktree.
func HoldsRepo(slot, repo string) bool {
	path := WorktreePath(slot, repo)
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// Prefer is the pull decision of section 4. It prefers the first card, in bench order and
// then queue order, whose bench already holds the card's repo in a kept worktree; with no
// warm bench it falls back to the first card and a fetch from that bench's mirror. It
// refuses an empty queue rather than inventing a card.
func Prefer(benches []PullBench) (Pull, error) {
	for _, b := range benches {
		for _, c := range b.Queue {
			if HoldsRepo(b.Slot, c.Repo) {
				return Pull{
					Bench: b.Name,
					Slot:  b.Slot,
					Card:  c,
					Warm:  true,
					Path:  WorktreePath(b.Slot, c.Repo),
				}, nil
			}
		}
	}
	for _, b := range benches {
		if len(b.Queue) > 0 {
			c := b.Queue[0]
			return Pull{
				Bench: b.Name,
				Slot:  b.Slot,
				Card:  c,
				Warm:  false,
				Path:  WorktreePath(b.Slot, c.Repo),
				Fetch: b.Mirror,
			}, nil
		}
	}
	return Pull{}, fmt.Errorf("no card to pull: every bench queue is empty")
}

// ReadCardDir reads a bench's queue: a directory of card files in source order. Each card
// names its kind and its repo on `kind:` and `repo:` lines; the card's name is its file
// name without the .card suffix. A card that names no repo has no affinity and is refused,
// never placed blind.
func ReadCardDir(dir string) ([]Card, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("--queue wants a readable directory of cards: %w", err)
	}
	cards := make([]Card, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		card, err := ParseCard(e.Name(), string(raw))
		if err != nil {
			return nil, err
		}
		cards = append(cards, card)
	}
	return cards, nil
}

// ParseCard reads one card's kind and repo. The card's name is its file name without a
// .card suffix.
func ParseCard(name, body string) (Card, error) {
	c := Card{Name: strings.TrimSuffix(name, ".card")}
	for _, line := range strings.Split(body, "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), ":")
		if !ok {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "kind":
			c.Kind = strings.TrimSpace(value)
		case "repo":
			c.Repo = strings.TrimSpace(value)
		}
	}
	if c.Repo == "" {
		return Card{}, fmt.Errorf("card %s names no repo; a card with no affinity is never placed", c.Name)
	}
	return c, nil
}

// ClipRequest is one clip: the kept worktree to reset, the card's branch to commit, the
// base to reset to, and where the card's result is harvested from and to.
type ClipRequest struct {
	Worktree string // the kept worktree under <slot>/worktrees/<owner>/<name>
	Branch   string // the card's topic branch to commit
	Base     string // the base ref the worktree is reset to, e.g. dev or origin/dev
	Message  string // the commit message; empty takes "clip <branch>"
	Result   string // the card's result file inside the worktree, e.g. RESULT.md
	Harvest  string // the directory the result is harvested into
}

// ClipResult is what a clip landed: the branch commit, the harvested result, and the base
// the worktree now sits on.
type ClipResult struct {
	Commit    string
	Harvested string
	Base      string
}

// Clip is nova-work clip (section 4): commit the card's branch, harvest its result, then
// reset the kept worktree to base. The clone is reset rather than re-cloned, so warmth is
// kept and history never leaks to the next card.
func Clip(req ClipRequest) (ClipResult, error) {
	if strings.TrimSpace(req.Worktree) == "" {
		return ClipResult{}, fmt.Errorf("--worktree is required; it wants the kept worktree to clip")
	}
	if strings.TrimSpace(req.Branch) == "" {
		return ClipResult{}, fmt.Errorf("--branch is required; it wants the card's branch to commit")
	}
	if strings.TrimSpace(req.Base) == "" {
		return ClipResult{}, fmt.Errorf("--base is required; it wants the base to reset the worktree to")
	}
	if _, err := warmGitOut(req.Worktree, "rev-parse", "--is-inside-work-tree"); err != nil {
		return ClipResult{}, fmt.Errorf("the worktree at %s is not a git working tree: %w", req.Worktree, err)
	}

	// (1) COMMIT THE CARD'S BRANCH. The worktree is put on the card's branch and whatever
	// the card left is committed there; nothing on the branch is lost by the reset below.
	if err := warmGitRun(req.Worktree, "checkout", req.Branch); err != nil {
		return ClipResult{}, fmt.Errorf("the worktree is not on the card's branch %s: %w", req.Branch, err)
	}
	dirty, err := warmGitOut(req.Worktree, "status", "--porcelain")
	if err != nil {
		return ClipResult{}, err
	}
	if strings.TrimSpace(dirty) != "" {
		if err := warmGitRun(req.Worktree, "add", "-A"); err != nil {
			return ClipResult{}, err
		}
		msg := strings.TrimSpace(req.Message)
		if msg == "" {
			msg = "clip " + strings.TrimSpace(req.Branch)
		}
		if err := warmGitRun(req.Worktree, "commit", "-q", "-m", msg); err != nil {
			return ClipResult{}, err
		}
	}
	commit, err := warmGitOut(req.Worktree, "rev-parse", "HEAD")
	if err != nil {
		return ClipResult{}, fmt.Errorf("the card's tip could not be read after the commit: %w", err)
	}

	// (2) HARVEST THE RESULT. It is copied out of the worktree before the reset removes it,
	// so the card's report survives the clip.
	harvested := ""
	if result := strings.TrimSpace(req.Result); result != "" {
		src := filepath.Join(req.Worktree, result)
		if _, err := os.Stat(src); err == nil {
			if strings.TrimSpace(req.Harvest) == "" {
				return ClipResult{}, fmt.Errorf("--harvest is required when the card wrote %s", result)
			}
			if err := os.MkdirAll(req.Harvest, 0o755); err != nil {
				return ClipResult{}, err
			}
			dst := filepath.Join(req.Harvest, filepath.Base(result))
			if err := warmCopyFile(src, dst); err != nil {
				return ClipResult{}, err
			}
			harvested = dst
		}
	}

	// (3) RESET TO BASE. The worktree returns to the base clean, so the next card on this
	// kept clone starts from base rather than from this card's state.
	if err := warmGitRun(req.Worktree, "checkout", "-f", req.Base); err != nil {
		return ClipResult{}, fmt.Errorf("the base %s could not be checked out: %w", req.Base, err)
	}
	if err := warmGitRun(req.Worktree, "reset", "--hard", req.Base); err != nil {
		return ClipResult{}, fmt.Errorf("the worktree could not be reset to %s: %w", req.Base, err)
	}
	if err := warmGitRun(req.Worktree, "clean", "-fdq"); err != nil {
		return ClipResult{}, fmt.Errorf("the worktree could not be cleaned: %w", err)
	}
	return ClipResult{Commit: commit, Harvested: harvested, Base: strings.TrimSpace(req.Base)}, nil
}

// warmCopyFile copies src to dst, replacing dst.
func warmCopyFile(src, dst string) error {
	raw, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return os.WriteFile(dst, raw, 0o644)
}

// warmGitOut runs git in dir and returns its trimmed standard output, or the error with
// git's own stderr when git could not run or exited non-zero.
func warmGitOut(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%s: %w", strings.TrimSpace(errb.String()), err)
	}
	return strings.TrimSpace(out.String()), nil
}

// warmGitRun runs git in dir, discarding standard output, and returns any error.
func warmGitRun(dir string, args ...string) error {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Stdout = io.Discard
	var errb bytes.Buffer
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %w", strings.TrimSpace(errb.String()), err)
	}
	return nil
}
