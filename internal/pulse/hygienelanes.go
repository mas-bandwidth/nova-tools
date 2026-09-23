package pulse

// The lane-clone half of hygiene: `nova-pulse hygiene --lane-dirs <root>`.
//
// Measured on the Studio 2026-09-18: 92 `lane-*` and `dogfood-*` clone
// directories under ~/rowan-working/tmp, one per lane worker of the day, and
// nothing in the fleet that ever takes one away. `hygiene run` does not: those
// are swarm slots, with a jobs directory and a liveness rule, and a lane clone
// is neither.
//
// A lane clone also cannot be swept the way scratch is swept, by age or by
// name, because it is not scratch: it holds a branch, and a branch may be the
// only copy of somebody's work. So this verb is built to the narrowest rule
// that is still worth having -- a clone goes only when the work in it is
// FINISHED AND ELSEWHERE -- and it asks four questions in this order, because
// each one is cheaper and more certain than the next:
//
//	safepath   the directory resolves strictly below the root it was found in
//	clean      `git status --porcelain` says nothing
//	pushed     no commit on a local branch that no remote has
//	settled    the forge says the PR whose head is that branch is MERGED or CLOSED
//
// Anything else is a KEEP line naming the directory and the one reason, and a
// read that FAILED is a keep too: this verb never removes on a guess. The git
// reads come before the forge read on purpose, so a merged pull request can
// never talk it past an uncommitted file.
//
// Both reads of the world are seams -- LaneGit and LanePRSource -- so no test
// of this verb runs git or reaches the network. Every removal goes through
// internal/safepath below the root the caller named, and `--dry-run` prints the
// same lines with removed=no and touches nothing.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bounded"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/safepath"
)

// LanePR is what the forge says about the pull request whose head is a branch.
// A zero Number means the forge knows no pull request for that branch at all,
// which is a reason to KEEP and never a reason to remove.
type LanePR struct {
	Number int
	State  string // OPEN, MERGED, CLOSED
}

// LanePRSource answers which pull request has this branch as its head. The real
// one runs `gh` inside the checkout; tests drive a fake, so no test of this
// verb reaches the network.
type LanePRSource interface {
	ForBranch(dir, branch string) (LanePR, error)
}

// LaneGit is the three git reads the verb makes about a checkout, and nothing
// else: it never writes, never fetches and never removes.
type LaneGit interface {
	Branch(dir string) (string, error) // the checked-out branch, "" when detached
	Dirty(dir string) (bool, error)    // git status --porcelain said something
	Unpushed(dir string) (bool, error) // a commit on a local branch that no remote has
}

// HygieneLanesInput is the lane-clone sweep's input, held apart from flag parsing.
type HygieneLanesInput struct {
	Root          string // the directory whose immediate children are the clones
	DryRun        bool
	OlderThanDays int // 0 means no age filter
	Max           int // the ceiling on per-candidate lines; 0 means all

	Now    func() time.Time
	Git    LaneGit
	PRs    LanePRSource
	Stdout io.Writer
	Stderr io.Writer
}

// The KEEP reasons, one token each, so a reader greps for the class and not for
// a sentence. Every one of them means the clone is still there.
const (
	laneKeepUnsafe   = "unsafe"         // the path does not resolve strictly below the root
	laneKeepMany     = "many-checkouts" // more than one checkout inside: which branch is the lane's?
	laneKeepFresh    = "fresh"          // touched inside the --older-than window
	laneKeepDirty    = "dirty"          // git status --porcelain said something
	laneKeepUnpushed = "unpushed"       // a commit on a local branch that no remote has
	laneKeepDetached = "detached"       // no branch is checked out, so no PR can be asked for
	laneKeepGit      = "git"            // a git read failed; a failed read is never a removal
	laneKeepForge    = "forge"          // the forge read failed
	laneKeepNoPR     = "no-pr"          // no pull request has this branch as its head
	laneKeepOpen     = "open"           // the pull request is still open
	laneKeepState    = "unknown-state"  // the forge answered a state this verb does not know
)

// HygieneLanes walks the clones under Root and removes the ones whose work is
// finished and elsewhere. It returns 0 when it ran and 2 on a refusal.
func HygieneLanes(in HygieneLanesInput) int {
	if in.Stdout == nil {
		in.Stdout = io.Discard
	}
	if in.Stderr == nil {
		in.Stderr = io.Discard
	}
	root := strings.TrimSpace(in.Root)
	if root == "" || !filepath.IsAbs(root) {
		fmt.Fprintf(in.Stderr, "HYGIENE REFUSED: --lane-dirs is an absolute path, got %s (pass the directory whose immediate children are the lane clones, e.g. --lane-dirs $HOME/rowan-working/tmp)\n", oneline.Field(in.Root))
		return 2
	}
	if info, err := os.Stat(root); err != nil || !info.IsDir() {
		fmt.Fprintf(in.Stderr, "HYGIENE REFUSED: --lane-dirs %s is not a directory (pass a directory that exists; this verb walks its immediate children and nothing deeper)\n", oneline.Field(root))
		return 2
	}
	if in.Git == nil || in.PRs == nil {
		fmt.Fprintf(in.Stderr, "HYGIENE REFUSED: --lane-dirs has no git or forge reader (this is a wiring fault, not yours; report it with the command you ran)\n")
		return 2
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		fmt.Fprintf(in.Stderr, "HYGIENE REFUSED: --lane-dirs %s cannot be read: %s (check the directory's permissions)\n", oneline.Field(root), oneline.Err(err))
		return 2
	}
	now := time.Now().UTC()
	if in.Now != nil {
		now = in.Now().UTC()
	}

	list := bounded.Capped(in.Stdout, in.Max, "HYGIENE", "lane", "pass --max 0 to print every candidate")
	candidates, decided, removed, kept := 0, 0, 0, 0
	keep := func(dir, reason string) {
		kept++
		list.Line(fmt.Sprintf("HYGIENE KEEP dir=%s reason=%s", oneline.Field(dir), oneline.Field(reason)))
	}

	for _, e := range entries {
		name := e.Name()
		dir := filepath.Join(root, name)
		checkout, many := laneCheckout(dir)
		if checkout == "" && !many {
			continue // not a clone at all: not a candidate, not counted, never removed
		}
		candidates++
		if many {
			keep(dir, laneKeepMany)
			continue
		}
		// The safepath question comes first and is asked about the directory
		// this verb would REMOVE, which is the immediate child of the root and
		// never the checkout inside it. A symlink is the one shape that can
		// name a directory outside the root, so it is named and kept, not
		// silently skipped: a path that leaves the root is exactly what this
		// line is for.
		if !safepath.NameOK(name) || e.Type()&os.ModeSymlink != 0 {
			keep(dir, laneKeepUnsafe)
			continue
		}
		if _, err := safepath.ResolvedUnder(dir, root); err != nil {
			keep(dir, laneKeepUnsafe)
			continue
		}
		if in.OlderThanDays > 0 && !laneOlderThan(dir, now, in.OlderThanDays) {
			keep(dir, laneKeepFresh)
			continue
		}
		reason, pr, ok := laneVerdict(in, checkout)
		if !ok {
			keep(dir, reason)
			continue
		}
		decided++
		gone := false
		if !in.DryRun {
			if err := safepath.RemoveUnderRoots(dir, root); err != nil {
				fmt.Fprintf(in.Stderr, "HYGIENE REFUSED: %s (the directory stays; it is not strictly below %s)\n", oneline.Err(err), oneline.Field(root))
			} else {
				removed++
				gone = true
			}
		}
		list.Line(fmt.Sprintf("HYGIENE LANE dir=%s pr=%d state=%s removed=%s",
			oneline.Field(dir), pr.Number, oneline.Field(strings.ToUpper(pr.State)), yesNo(gone)))
	}
	list.More()
	fmt.Fprintf(in.Stdout, "HYGIENE LANES root=%s candidates=%d remove=%d removed=%d kept=%d dry-run=%s\n",
		oneline.Field(root), candidates, decided, removed, kept, yesNo(in.DryRun))
	return 0
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// laneVerdict asks the four questions of one checkout in the order that keeps
// the cheapest and most certain first. It answers the KEEP reason, the pull
// request it read, and whether the directory may be removed.
func laneVerdict(in HygieneLanesInput, checkout string) (string, LanePR, bool) {
	dirty, err := in.Git.Dirty(checkout)
	if err != nil {
		fmt.Fprintf(in.Stderr, "HYGIENE NOTE %s: git status could not be read: %s\n", oneline.Field(checkout), oneline.Err(err))
		return laneKeepGit, LanePR{}, false
	}
	if dirty {
		return laneKeepDirty, LanePR{}, false
	}
	unpushed, err := in.Git.Unpushed(checkout)
	if err != nil {
		fmt.Fprintf(in.Stderr, "HYGIENE NOTE %s: the local branches could not be read: %s\n", oneline.Field(checkout), oneline.Err(err))
		return laneKeepGit, LanePR{}, false
	}
	if unpushed {
		return laneKeepUnpushed, LanePR{}, false
	}
	branch, err := in.Git.Branch(checkout)
	if err != nil {
		fmt.Fprintf(in.Stderr, "HYGIENE NOTE %s: the checked-out branch could not be read: %s\n", oneline.Field(checkout), oneline.Err(err))
		return laneKeepGit, LanePR{}, false
	}
	if strings.TrimSpace(branch) == "" {
		return laneKeepDetached, LanePR{}, false
	}
	pr, err := in.PRs.ForBranch(checkout, branch)
	if err != nil {
		fmt.Fprintf(in.Stderr, "HYGIENE NOTE %s: the forge could not be asked about %s: %s\n", oneline.Field(checkout), oneline.Field(branch), oneline.Err(err))
		return laneKeepForge, LanePR{}, false
	}
	if pr.Number == 0 {
		return laneKeepNoPR, pr, false
	}
	switch strings.ToUpper(strings.TrimSpace(pr.State)) {
	case "MERGED", "CLOSED":
		return "", pr, true
	case "OPEN":
		return laneKeepOpen, pr, false
	default:
		return laneKeepState, pr, false
	}
}

// laneOlderThan reports whether the candidate's own mtime is at or beyond the
// window. The directory that is stamped is the one the verb would remove, not
// the checkout inside it, because that is the thing whose age a person means
// when they say a lane clone is old.
func laneOlderThan(dir string, now time.Time, days int) bool {
	info, err := os.Stat(dir)
	if err != nil {
		return false // a directory whose age cannot be read is never swept by age
	}
	return !info.ModTime().After(now.Add(-time.Duration(days) * 24 * time.Hour))
}

// laneCheckout answers the checkout inside one candidate directory, handling the
// two shapes today's lane workers write: the clone itself (`<root>/lane-foo`
// holding `.git`) and the clone one level down (`<root>/lane-foo/repo`). The
// second answer is true when the candidate holds MORE than one checkout, which
// is not a shape this verb can decide -- two checkouts are two branches, and it
// would have to remove both to remove either.
//
// A `.git` ENTRY is the test, not a `.git` directory: a git worktree carries a
// `.git` file, and a worktree of a lane is as much a lane clone as a clone is.
func laneCheckout(dir string) (string, bool) {
	if laneHasGit(dir) {
		return dir, false
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", false
	}
	var found []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if child := filepath.Join(dir, e.Name()); laneHasGit(child) {
			found = append(found, child)
		}
	}
	switch len(found) {
	case 0:
		return "", false
	case 1:
		return found[0], false
	default:
		return "", true
	}
}

func laneHasGit(dir string) bool {
	_, err := os.Lstat(filepath.Join(dir, ".git"))
	return err == nil
}

// ---------------------------------------------------------------------------
// The two real doors.

// OSLaneGit is the real LaneGit: three bounded `git` reads per checkout, none
// of which writes anything or touches the network.
type OSLaneGit struct{ Timeout time.Duration }

func (g OSLaneGit) timeout() time.Duration {
	if g.Timeout <= 0 {
		return 30 * time.Second
	}
	return g.Timeout
}

func (g OSLaneGit) read(dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), g.timeout())
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(out)), nil
}

// Branch is the checked-out branch, or "" when the checkout is detached and
// there is no branch a pull request could have as its head.
func (g OSLaneGit) Branch(dir string) (string, error) {
	out, err := g.read(dir, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "", err
	}
	if out == "HEAD" {
		return "", nil
	}
	return out, nil
}

// Dirty is `git status --porcelain` having anything at all to say: a modified
// file, a staged change, an untracked file. All three are work that exists only
// in this directory.
func (g OSLaneGit) Dirty(dir string) (bool, error) {
	out, err := g.read(dir, "status", "--porcelain")
	if err != nil {
		return false, err
	}
	return out != "", nil
}

// Unpushed is `git rev-list --branches --not --remotes`: any commit reachable
// from a local branch that no remote-tracking ref contains. It is one process
// and it is stricter than asking only about each branch's head -- a branch
// whose head is on the remote but which carries an older commit that is not
// would pass the head test and fail this one, and the stricter answer is the
// one that keeps the directory.
func (g OSLaneGit) Unpushed(dir string) (bool, error) {
	out, err := g.read(dir, "rev-list", "--branches", "--not", "--remotes", "--max-count=1")
	if err != nil {
		return false, err
	}
	return out != "", nil
}

// GHLanePRs is the real LanePRSource: one bounded `gh pr list --head` per
// checkout, run INSIDE the checkout so gh reads the repository from the remote
// the clone already has and this verb never has to be told which repo a lane
// belongs to.
type GHLanePRs struct{ Timeout time.Duration }

func (s GHLanePRs) ForBranch(dir, branch string) (LanePR, error) {
	timeout := s.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "gh", "pr", "list", "--head", branch,
		"--state", "all", "--limit", "10", "--json", "number,state")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return LanePR{}, fmt.Errorf("gh pr list --head %s: %w", branch, err)
	}
	var rows []struct {
		Number int    `json:"number"`
		State  string `json:"state"`
	}
	if err := json.Unmarshal(out, &rows); err != nil {
		return LanePR{}, fmt.Errorf("gh pr list --head %s did not answer JSON: %w", branch, err)
	}
	if len(rows) == 0 {
		return LanePR{}, nil
	}
	// A branch can carry more than one pull request over its life. An OPEN one
	// wins whatever its age, because the answer this verb acts on has to be the
	// one that keeps the directory.
	for _, r := range rows {
		if strings.EqualFold(r.State, "OPEN") {
			return LanePR{Number: r.Number, State: r.State}, nil
		}
	}
	return LanePR{Number: rows[0].Number, State: rows[0].State}, nil
}
