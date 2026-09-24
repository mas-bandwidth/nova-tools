package land

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

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
func BuildTrain(ctx context.Context, p TrainParams) (*TrainResult, error) {
	if p.FromTip == "" {
		return nil, fmt.Errorf("build train: from_tip is required")
	}
	currentHead := p.FromTip
	currentTree := ""
	var commits []string

	for i, memberHead := range p.Members {
		if memberHead == "" {
			continue
		}
		// 1. git merge-tree --write-tree <currentHead> <memberHead>
		mergeArgs := []string{"merge-tree", "--write-tree", currentHead, memberHead}
		cmd := exec.CommandContext(ctx, "git", mergeArgs...)
		if p.GitDir != "" {
			cmd.Dir = p.GitDir
		}
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			return nil, fmt.Errorf("merge-tree %s %s: %w (stderr: %s)", currentHead, memberHead, err, stderr.String())
		}
		lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
		if len(lines) == 0 || len(lines[0]) != 40 {
			return nil, fmt.Errorf("merge-tree %s %s: invalid tree output %q", currentHead, memberHead, stdout.String())
		}
		treeSHA := lines[0]
		currentTree = treeSHA

		// 2. git commit-tree <treeSHA> -p <currentHead> -p <memberHead> -m <message>
		msg := fmt.Sprintf("train %s member %d: %s", p.BatchID, i+1, memberHead)
		commitArgs := []string{"commit-tree", treeSHA, "-p", currentHead, "-p", memberHead, "-m", msg}
		commitCmd := exec.CommandContext(ctx, "git", commitArgs...)
		if p.GitDir != "" {
			commitCmd.Dir = p.GitDir
		}
		date := p.CreatedAt
		if date == "" {
			date = "1790252098"
		}
		commitCmd.Env = append(cmd.Environ(),
			"GIT_AUTHOR_NAME=nova-sprint",
			"GIT_AUTHOR_EMAIL=nova-sprint@mas-bandwidth.com",
			"GIT_AUTHOR_DATE="+date,
			"GIT_COMMITTER_NAME=nova-sprint",
			"GIT_COMMITTER_EMAIL=nova-sprint@mas-bandwidth.com",
			"GIT_COMMITTER_DATE="+date,
		)
		stdout.Reset()
		stderr.Reset()
		commitCmd.Stdout = &stdout
		commitCmd.Stderr = &stderr
		if err := commitCmd.Run(); err != nil {
			return nil, fmt.Errorf("commit-tree %s: %w (stderr: %s)", treeSHA, err, stderr.String())
		}
		commitSHA := strings.TrimSpace(stdout.String())
		if len(commitSHA) != 40 {
			return nil, fmt.Errorf("commit-tree returned invalid commit SHA: %q", commitSHA)
		}
		commits = append(commits, commitSHA)
		currentHead = commitSHA
	}

	return &TrainResult{
		TrainHead: currentHead,
		TrainTree: currentTree,
		Commits:   commits,
	}, nil
}
