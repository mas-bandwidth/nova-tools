package main

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// classifyTip is classification's one non-Redis read. It asks git for the
// base branch tip; it never invokes a forge API.
func classifyTip(ctx context.Context, repo, base string) (string, error) {
	if repo == "" || base == "" {
		return "", fmt.Errorf("repo and base are required")
	}
	target := repo
	if !strings.Contains(target, "://") && !strings.HasPrefix(target, "git@") {
		target = "https://github.com/" + strings.TrimSuffix(target, ".git") + ".git"
	}
	out, err := exec.CommandContext(ctx, "git", "ls-remote", "--exit-code", target, "refs/heads/"+base).Output()
	if err != nil {
		return "", fmt.Errorf("git ls-remote %s %s: %w", repo, base, err)
	}
	fields := strings.Fields(string(out))
	if len(fields) < 2 || len(fields[0]) != 40 {
		return "", fmt.Errorf("git ls-remote %s %s returned no 40-hex tip", repo, base)
	}
	return fields[0], nil
}
