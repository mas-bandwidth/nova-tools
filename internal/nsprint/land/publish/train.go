package publish

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// TrainParams are the inputs of the deterministic train (spec 5.3).
type TrainParams struct {
	GitDir    string   // the mirror holding from_tip and every member head
	FromTip   string   // the base commit the batch was gated on
	Members   []string // member heads, in batch order
	BatchID   string
	CreatedAt string // the batch's created_at: author and committer date
}

// TrainResult is the train: its head, its tree and one merge commit per member.
type TrainResult struct {
	TrainHead string
	TrainTree string
	Commits   []string
}

// BuildTrain replays the members onto from_tip with git merge-tree --write-tree
// and git commit-tree under a fixed author, committer, message and date, so the
// head is a pure function of from_tip, the member heads and their order: every
// gate worker and the publisher compute the same sha (5.3, 5.5). It is the same
// construction as the gate worker's (B6); the publisher rebuilds from its own
// mirror and compares with the receipt, never trusting a pushed ref.
func BuildTrain(ctx context.Context, p TrainParams) (*TrainResult, error) {
	if p.FromTip == "" {
		return nil, fmt.Errorf("build train: from_tip is required")
	}
	date, err := gitDate(p.CreatedAt)
	if err != nil {
		return nil, err
	}
	head, tree := p.FromTip, ""
	var commits []string
	for i, member := range p.Members {
		if member == "" {
			continue
		}
		out, err := gitOut(ctx, p.GitDir, nil, "merge-tree", "--write-tree", head, member)
		if err != nil {
			return nil, fmt.Errorf("merge-tree %s %s: %w", head, member, err)
		}
		lines := strings.Split(out, "\n")
		if len(lines[0]) != 40 {
			return nil, fmt.Errorf("merge-tree %s %s: invalid tree output %q", head, member, out)
		}
		tree = lines[0]
		msg := fmt.Sprintf("train %s member %d: %s", p.BatchID, i+1, member)
		env := []string{
			"GIT_AUTHOR_NAME=nova-sprint",
			"GIT_AUTHOR_EMAIL=nova-sprint@mas-bandwidth.com",
			"GIT_AUTHOR_DATE=" + date,
			"GIT_COMMITTER_NAME=nova-sprint",
			"GIT_COMMITTER_EMAIL=nova-sprint@mas-bandwidth.com",
			"GIT_COMMITTER_DATE=" + date,
		}
		sha, err := gitOut(ctx, p.GitDir, env, "-c", "commit.gpgsign=false", "commit-tree", tree, "-p", head, "-p", member, "-m", msg)
		if err != nil {
			return nil, fmt.Errorf("commit-tree %s: %w", tree, err)
		}
		if len(sha) != 40 {
			return nil, fmt.Errorf("commit-tree returned invalid commit sha %q", sha)
		}
		commits = append(commits, sha)
		head = sha
	}
	return &TrainResult{TrainHead: head, TrainTree: tree, Commits: commits}, nil
}

// gitOut runs git in dir and returns its trimmed stdout.
func gitOut(ctx context.Context, dir string, env []string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	if env != nil {
		cmd.Env = append(cmd.Environ(), env...)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(stdout.String()), nil
}

// gitDate turns a batch's created_at into the fixed author and committer date.
// ns_batch_plan stamps created_at in Unix milliseconds, which git refuses as a
// date ("invalid date format"), so the train is dated at the whole second in
// git's internal form "@<seconds> +0000". Seconds pass through; empty is the
// fixed epoch the worker used before a batch carried created_at.
func gitDate(createdAt string) (string, error) {
	if createdAt == "" {
		createdAt = "1790252098"
	}
	n, err := strconv.ParseInt(createdAt, 10, 64)
	if err != nil || n < 0 {
		return "", fmt.Errorf("build train: created_at %q is not a Unix time", createdAt)
	}
	if n >= 1e12 { // milliseconds
		n /= 1000
	}
	return "@" + strconv.FormatInt(n, 10) + " +0000", nil
}
