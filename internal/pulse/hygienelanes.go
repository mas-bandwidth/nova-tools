package pulse

// The lane-clone half of hygiene: the seams and the entry point. The machine
// behind them is the green commit; this is what the red tests are written
// against.

import (
	"errors"
	"fmt"
	"io"
	"time"
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

// HygieneLanes walks the clones under Root and removes the ones whose work is
// finished and elsewhere. It returns 0 when it ran and 2 on a refusal.
func HygieneLanes(in HygieneLanesInput) int {
	if in.Stderr != nil {
		fmt.Fprintln(in.Stderr, "HYGIENE REFUSED: --lane-dirs has no machine behind it yet")
	}
	return 2
}

// errNotImplemented is what the seams answer until the green commit.
var errNotImplemented = errors.New("nova-pulse hygiene --lane-dirs: not implemented")

// OSLaneGit is the real LaneGit: three bounded `git` reads per checkout.
type OSLaneGit struct{ Timeout time.Duration }

func (OSLaneGit) Branch(dir string) (string, error) { return "", errNotImplemented }

func (OSLaneGit) Dirty(dir string) (bool, error) { return false, errNotImplemented }

func (OSLaneGit) Unpushed(dir string) (bool, error) { return false, errNotImplemented }

// GHLanePRs is the real LanePRSource: one bounded `gh pr list --head` per
// checkout, run inside the checkout so gh reads the repository from the remote
// the clone already has.
type GHLanePRs struct{ Timeout time.Duration }

func (GHLanePRs) ForBranch(dir, branch string) (LanePR, error) {
	return LanePR{}, errNotImplemented
}
