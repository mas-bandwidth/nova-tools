package swarm

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/cardcontract"
	"github.com/mas-bandwidth/nova-tools/internal/typedrec"
)

// restageAtTip stages a rework's checkout at the tip of its base branch (docs/SPEC-CARD-CONTRACT.md,
// where a rework starts; nova-tools#5215; tla/CardContract.tla, StageFroms, with the invariants
// ReworkStagedAtTheTip and NoPushedWorkUnreachable): origin's branch is fetched, the checkout's branch is
// put at its tip, and the work of the last earlier attempt that pushed (rw.Prev, which the stage
// has already fetched and checked out) is carried on top as one commit by a three-way merge of
// that head into the tip, squashed. A head that already descends from the tip is the staged
// commit as it is; work the tip already holds adds nothing; work that does not apply cleanly
// leaves the bare tip, and JOB.md says that work must be redone. A rework staged at the old head
// instead kept a base hours old (diaryd-67: 16 commits on a base 698 behind), and a child told to
// start again from the tip was refused at its finish for not descending from the staged commit.
//
// A base that is a full sha, a tag (a ref the clone holds as a tag and not as a branch) or none
// never moves: the checkout stays where the stage put it and the carry is nil. A fetch of the
// branch that fails is the stage's failure (what names the step), never a stage at a stale tip.
func restageAtTip(ctx context.Context, git func(context.Context, ...string) *exec.Cmd, dir, branch, base string, rw Rework,
	fetchTime, checkoutTime *time.Duration) (c *cardcontract.Carry, what string, out []byte, err error) {
	if base == "" || typedrec.IsFullSha(base) {
		return nil, "", nil, nil
	}
	in := func(args ...string) []string { return append([]string{"-C", dir}, args...) }
	has := func(ref string) bool {
		return git(ctx, in("rev-parse", "-q", "--verify", "--end-of-options", ref+"^{commit}")...).Run() == nil
	}
	remote := "refs/remotes/origin/" + base
	if !has(remote) && has("refs/tags/"+base) {
		return nil, "", nil, nil
	}
	if out, err := stageTimedOutput(git(ctx, in("fetch", "-q", "--no-tags", "--", "origin", "+refs/heads/"+base+":"+remote)...), fetchTime); err != nil {
		return nil, "fetch of the base branch " + base + ", whose tip a rework is staged at,", out, err
	}
	tipOut, err := git(ctx, in("rev-parse", "--verify", "--end-of-options", remote+"^{commit}")...).Output()
	tip := strings.TrimSpace(string(tipOut))
	if err != nil || !typedrec.IsFullSha(tip) {
		return nil, "rev-parse of the base branch's tip " + remote, tipOut, cmpErr(err, "no full commit sha")
	}
	c = &cardcontract.Carry{Base: base, Tip: tip, Prev: rw.Prev, From: rw.From, Staged: tip, State: cardcontract.CarryNone}
	switchTo := func(sha string) ([]byte, error) {
		return stageTimedOutput(git(ctx, in("switch", "-q", "-C", branch, "--end-of-options", sha)...), checkoutTime)
	}
	if rw.Prev == "" {
		if out, err := switchTo(tip); err != nil {
			return nil, "checkout of the base branch's tip", out, err
		}
		return c, "", nil, nil
	}
	if git(ctx, in("merge-base", "--is-ancestor", "--end-of-options", tip, rw.Prev)...).Run() == nil {
		// the head the attempt before pushed already stands on the tip: it is the staged commit
		if out, err := switchTo(rw.Prev); err != nil {
			return nil, "checkout of the previous head", out, err
		}
		c.Staged, c.State = rw.Prev, cardcontract.CarryOK
		return c, "", nil, nil
	}
	if out, err := switchTo(tip); err != nil {
		return nil, "checkout of the base branch's tip", out, err
	}
	merged, err := stageTimedOutput(git(ctx, in("-c", "merge.ff=true", "merge", "-q", "--squash", "--end-of-options", rw.Prev)...), checkoutTime)
	if err != nil {
		// a conflict leaves unmerged paths in the index; a merge that failed with none (an
		// index lock, histories with no merge base after a rewritten base, the deadline) is
		// not a conflict, and is the stage's failure with git's own words, never a
		// "conflict" whose redo hint would name a diff with no merge base
		unmerged, uerr := git(ctx, in("ls-files", "--unmerged")...).Output()
		if stageTimedOut(ctx, err) || uerr != nil || len(strings.TrimSpace(string(unmerged))) == 0 {
			return nil, "merge of the previous head onto the tip, which is no conflict,", merged, err
		}
		// it does not apply cleanly: the checkout goes back to the bare tip. A squash merge never
		// moves HEAD, so HEAD is still the tip and the reset names no commit: `git reset` 2.43 (the
		// CI runners' git) refuses a commit after `--end-of-options` ("option '--end-of-options'
		// must come before non-option arguments"), and a reset with no operand is one command on
		// every git
		if out, err := stageTimedOutput(git(ctx, in("reset", "-q", "--hard")...), checkoutTime); err != nil {
			return nil, "reset to the base branch's tip after a carry that did not apply", out, err
		}
		c.State = cardcontract.CarryConflict
		return c, "", nil, nil
	}
	if err := git(ctx, in("diff", "--cached", "--quiet")...).Run(); err == nil {
		c.State = cardcontract.CarryHeld // the tip already holds the work
		return c, "", nil, nil
	} else if exit := (*exec.ExitError)(nil); !errors.As(err, &exit) || exit.ExitCode() != 1 {
		return nil, "diff of the carried work", nil, err
	}
	msg := fmt.Sprintf("carry attempt %d's work (%s) onto the tip of %s (%s)\n\nnova-swarm staged this rework at the tip of its base branch with the work of the attempt before carried on top (docs/SPEC-CARD-CONTRACT.md, where a rework starts).",
		rw.From, short12(rw.Prev), base, short12(tip))
	if out, err := stageTimedOutput(git(ctx, in("-c", "commit.gpgsign=false", "commit", "-q", "--no-verify", "-m", msg)...), checkoutTime); err != nil {
		return nil, "commit of the carried work", out, err
	}
	headOut, err := git(ctx, in("rev-parse", "HEAD")...).Output()
	head := strings.TrimSpace(string(headOut))
	if err != nil || !typedrec.IsFullSha(head) {
		return nil, "rev-parse of the carried commit", headOut, cmpErr(err, "no full commit sha")
	}
	c.Staged, c.State = head, cardcontract.CarryOK
	return c, "", nil, nil
}

// cmpErr is err, else an error saying why.
func cmpErr(err error, why string) error {
	if err != nil {
		return err
	}
	return errors.New(why)
}
