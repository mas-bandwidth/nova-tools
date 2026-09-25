package land

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// Base red is a freeze, from local receipts (Issue #3139 rev 7 §8.4, build B11,
// control L21). land serve runs TipWatch.Tick beside the sweeper: one
// ns_tip_tick call (internal/nsprint/fn/lua/land_tip.lua) gates the tip, freezes
// the base on a red tip receipt, thaws it on a green one, gates every landed tip
// since the last green one in parallel, and plans the revert train of exactly
// the first red tip's batch. The gate workers and the publisher build that
// revert commit with BuildRevert, so both reach the same sha.

// DefaultTipFullEvery is tip_full_every (§8.4): how often a green tip is gated again.
const DefaultTipFullEvery = 10 * time.Minute

// TipGateID is the tip gate batch of sha: kind tip, class full, no members, off the chain.
func TipGateID(sha string) string { return "tip-" + sha }

// RevertID is the revert train of batch: kind revert, at the front of the chain.
func RevertID(batch string) string { return "revert-" + batch }

// TipGatesKey is land:<repo>:<base>:tipgates, the tip gate batches by queue time
// (the newest 64): the reclaim sweep's index, so nothing is scanned.
func TipGatesKey(repo, base string) string { return fmt.Sprintf("land:%s:%s:tipgates", repo, base) }

// LandedTipsKey is land:<repo>:<base>:landed, the landed batches by landing time
// (the newest 256, written by ns_land): the tips a freeze gates back through.
func LandedTipsKey(repo, base string) string { return fmt.Sprintf("land:%s:%s:landed", repo, base) }

// TipGreenKey is land:<repo>:<base>:tipgreen: sha, batch, score (its landed
// score) and at of the newest tip whose full gate was green.
func TipGreenKey(repo, base string) string { return fmt.Sprintf("land:%s:%s:tipgreen", repo, base) }

// TipWatch is land serve's tip tick for one repo and base, under its lease.
type TipWatch struct {
	Client *redis.Client
	Sprint string
	Repo   string
	Base   string
	Lease  string        // <gen>:<token>, the publisher lease this serve holds
	Every  time.Duration // tip_full_every; 0 is DefaultTipFullEvery, negative never re-gates a gated tip
}

// TipReport is one tick: the transition lines in order, or why nothing ran.
type TipReport struct {
	Refused string // the fence's reason; nothing was written
	NoTip   bool   // no tip record yet
	Lines   []string
}

// Tick runs one ns_tip_tick.
func (w *TipWatch) Tick(ctx context.Context) (TipReport, error) {
	every := w.Every
	if every == 0 {
		every = DefaultTipFullEvery
	}
	ms := every.Milliseconds()
	if every < 0 {
		ms = 0
	}
	res, err := w.Client.FCall(ctx, "ns_tip_tick", nil, w.Sprint, w.Repo, w.Base, w.Lease, strconv.FormatInt(ms, 10)).StringSlice()
	if err != nil {
		return TipReport{}, fmt.Errorf("ns_tip_tick: %w", err)
	}
	if len(res) == 0 {
		return TipReport{}, errors.New("ns_tip_tick: empty reply")
	}
	switch res[0] {
	case "REFUSED":
		reason := "refused"
		if len(res) > 1 {
			reason = res[1]
		}
		return TipReport{Refused: reason, Lines: []string{RefusedLine(reason)}}, nil
	case "NOTIP":
		return TipReport{NoTip: true}, nil
	case "OK":
		return TipReport{Lines: res[1:]}, nil
	}
	return TipReport{}, fmt.Errorf("ns_tip_tick: unexpected reply %v", res)
}

// CallFreeze is land freeze: status OK or ALREADY (detail is the standing reason).
func CallFreeze(ctx context.Context, c *redis.Client, repo, base, reason, by string) (status, detail string, err error) {
	res, err := c.FCall(ctx, "ns_freeze", nil, repo, base, reason, by).StringSlice()
	if err != nil || len(res) == 0 {
		return "", "", fmt.Errorf("ns_freeze: %v %w", res, err)
	}
	if len(res) > 1 {
		detail = res[1]
	}
	return res[0], detail, nil
}

// CallThaw is land thaw: status OK (source is what froze it: tip or hand) or NOTFROZEN.
func CallThaw(ctx context.Context, c *redis.Client, repo, base, by string) (status, source string, err error) {
	res, err := c.FCall(ctx, "ns_thaw", nil, repo, base, by).StringSlice()
	if err != nil || len(res) == 0 {
		return "", "", fmt.Errorf("ns_thaw: %v %w", res, err)
	}
	if len(res) > 1 {
		source = res[1]
	}
	return res[0], source, nil
}

// RevertParams are the inputs of a revert train: the tip it is planned on and
// the red batch's from_tip and train_head.
type RevertParams struct {
	GitDir       string
	FromTip      string // the tip the revert lands on
	RevertHead   string // the red batch's train_head (the first red tip)
	RevertParent string // the red batch's from_tip (green)
	BatchID      string
	CreatedAt    string
}

// BuildRevert builds the deterministic revert commit of one landed batch on
// from_tip: the three-way merge of from_tip and the batch's from_tip over the
// batch's train_head (git merge-tree --merge-base), so the batch's changes and
// nothing else are undone, committed under the fixed author, committer and date
// of BuildTrain. A conflict (a later batch changed the same lines) is a
// *MergeConflictError: the gate receipts CONFLICT and the base stays frozen.
func BuildRevert(ctx context.Context, p RevertParams) (*TrainResult, error) {
	if p.FromTip == "" || p.RevertHead == "" || p.RevertParent == "" {
		return nil, fmt.Errorf("build revert: from_tip, revert_head and revert_parent are required")
	}
	date, err := trainDate(p.CreatedAt)
	if err != nil {
		return nil, err
	}
	out, errOut, err := trainGit(ctx, p.GitDir, nil, "merge-tree", "--write-tree", "--merge-base="+p.RevertHead, p.FromTip, p.RevertParent)
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return nil, &MergeConflictError{CurrentHead: p.FromTip, MemberHead: p.RevertParent, Stdout: out, Stderr: errOut}
		}
		return nil, fmt.Errorf("merge-tree revert %s on %s: %w: %s", p.RevertHead, p.FromTip, err, errOut)
	}
	tree := strings.Split(out, "\n")[0]
	if len(tree) != 40 {
		return nil, fmt.Errorf("merge-tree revert: invalid tree output %q", out)
	}
	env := []string{
		"GIT_AUTHOR_NAME=nova-sprint",
		"GIT_AUTHOR_EMAIL=nova-sprint@mas-bandwidth.com",
		"GIT_AUTHOR_DATE=" + date,
		"GIT_COMMITTER_NAME=nova-sprint",
		"GIT_COMMITTER_EMAIL=nova-sprint@mas-bandwidth.com",
		"GIT_COMMITTER_DATE=" + date,
	}
	msg := fmt.Sprintf("train %s: revert %s to %s", p.BatchID, p.RevertHead, p.RevertParent)
	sha, errOut, err := trainGit(ctx, p.GitDir, env, "-c", "commit.gpgsign=false", "commit-tree", tree, "-p", p.FromTip, "-m", msg)
	if err != nil {
		return nil, fmt.Errorf("commit-tree revert %s: %w: %s", tree, err, errOut)
	}
	if len(sha) != 40 {
		return nil, fmt.Errorf("commit-tree revert returned invalid commit sha %q", sha)
	}
	return &TrainResult{TrainHead: sha, TrainTree: tree, Commits: []string{sha}}, nil
}
