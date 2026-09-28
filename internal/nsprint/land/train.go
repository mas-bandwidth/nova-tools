package land

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// MergeConflictError indicates that git merge-tree encountered merge conflicts.
type MergeConflictError struct {
	CurrentHead string
	MemberHead  string
	Stdout      string
	Stderr      string
}

func (e *MergeConflictError) Error() string {
	return fmt.Sprintf("merge conflict between %s and %s: %s", e.CurrentHead, e.MemberHead, e.Stdout)
}

// trainDate turns a batch's created_at into the fixed author and committer date,
// by the same rule as publish.gitDate (the publisher rebuilds this train and
// compares shas, 5.3, 5.5): ns_batch_plan stamps Unix milliseconds, so a value
// of 1e12 or more is divided to whole seconds; seconds pass through; empty is
// the fixed epoch; anything else is refused, never handed to git to parse.
func trainDate(createdAt string) (string, error) {
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

// TrainParams holds inputs for deterministic train construction (spec 5.3).
type TrainParams struct {
	GitDir    string   // path to mirror or git repository
	FromTip   string   // base commit SHA
	Members   []string // ordered member head commit SHAs
	BatchID   string   // batch identifier
	CreatedAt string   // batch created_at timestamp string
}

// TrainResult is the deterministic train output.
type TrainResult struct {
	TrainHead string
	TrainTree string
	Commits   []string
}

// BuildTrain constructs the deterministic merge train using git merge-tree --write-tree
// and git commit-tree under fixed author, committer, message and date (spec 5.3).
//
// It is the same construction as publish.BuildTrain, command for command (the
// publisher rebuilds the train and must reach the same sha; TestTrainMatchesPublisher
// holds the two together). The worker cannot call publish.BuildTrain itself:
// package publish imports land, so land importing publish is a cycle. The one
// addition is the typed conflict: merge-tree exits 1 on a conflicted merge and
// prints the conflict on stdout, so exit status 1 is a *MergeConflictError
// (receipted CONFLICT) and any other failure is a plain error (receipted ERROR).
func BuildTrain(ctx context.Context, p TrainParams) (*TrainResult, error) {
	if p.FromTip == "" {
		return nil, fmt.Errorf("build train: from_tip is required")
	}
	date, err := trainDate(p.CreatedAt)
	if err != nil {
		return nil, err
	}
	head, tree := p.FromTip, ""
	var commits []string
	for i, member := range p.Members {
		if member == "" {
			continue
		}
		out, errOut, err := trainGit(ctx, p.GitDir, nil, "merge-tree", "--write-tree", head, member)
		if err != nil {
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
				return nil, &MergeConflictError{CurrentHead: head, MemberHead: member, Stdout: out, Stderr: errOut}
			}
			return nil, fmt.Errorf("merge-tree %s %s: %w: %s", head, member, err, errOut)
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
		sha, errOut, err := trainGit(ctx, p.GitDir, env, "-c", "commit.gpgsign=false", "commit-tree", tree, "-p", head, "-p", member, "-m", msg)
		if err != nil {
			return nil, fmt.Errorf("commit-tree %s: %w: %s", tree, err, errOut)
		}
		if len(sha) != 40 {
			return nil, fmt.Errorf("commit-tree returned invalid commit sha %q", sha)
		}
		commits = append(commits, sha)
		head = sha
	}
	return &TrainResult{TrainHead: head, TrainTree: tree, Commits: commits}, nil
}

// trainGit runs git in dir and returns its trimmed stdout and stderr.
func trainGit(ctx context.Context, dir string, env []string, args ...string) (string, string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	if env != nil {
		cmd.Env = append(cmd.Environ(), env...)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	return strings.TrimSpace(stdout.String()), strings.TrimSpace(stderr.String()), err
}
