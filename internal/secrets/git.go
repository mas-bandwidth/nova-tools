package secrets

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// GitRefStatus holds the result of verifying invariant 8 against a git working copy.
type GitRefStatus struct {
	HeadSHA   string
	RemoteRef string
	RemoteSHA string
	Clean     bool
	Refusal   string
}

// CheckGitWorkingCopy inspects .git directly without invoking the git binary.
// It verifies invariant 8: HEAD equals the remote-tracking ref it tracks.
// Refusals are returned for:
// - .git is missing or not a directory (a file indicates submodule or worktree)
// - detached HEAD
// - branch with no remote tracking upstream
func CheckGitWorkingCopy(storeDir string) (GitRefStatus, error) {
	var status GitRefStatus
	gitDir := filepath.Join(storeDir, ".git")
	fi, err := os.Stat(gitDir)
	if err != nil {
		return status, fmt.Errorf("store %s is not a git repository: missing .git directory; clone it: git clone <url> %s", storeDir, storeDir)
	}
	if !fi.IsDir() {
		return status, fmt.Errorf("store %s: .git is a file (a worktree or submodule); expected a directory working copy", storeDir)
	}

	headBytes, err := os.ReadFile(filepath.Join(gitDir, "HEAD"))
	if err != nil {
		return status, fmt.Errorf("store %s: unable to read .git/HEAD: %w", storeDir, err)
	}
	headContent := strings.TrimSpace(string(headBytes))
	if !strings.HasPrefix(headContent, "ref: refs/heads/") {
		return status, fmt.Errorf("store %s: detached HEAD (%s); expected a branch tracking an upstream", storeDir, headContent)
	}
	branch := strings.TrimPrefix(headContent, "ref: refs/heads/")

	localSHA, err := resolveRef(gitDir, "refs/heads/"+branch)
	if err != nil {
		return status, fmt.Errorf("store %s: unable to resolve local branch %s: %w", storeDir, branch, err)
	}
	if len(localSHA) >= 7 {
		status.HeadSHA = localSHA[:7]
	} else {
		status.HeadSHA = localSHA
	}

	remote, mergeRef, err := readBranchConfig(gitDir, branch)
	if err != nil {
		return status, fmt.Errorf("store %s: %w", storeDir, err)
	}
	if remote == "" || mergeRef == "" {
		return status, fmt.Errorf("store %s: branch %s has no upstream tracking branch configured in .git/config", storeDir, branch)
	}

	upstreamBranch := strings.TrimPrefix(mergeRef, "refs/heads/")
	remoteTrackingRef := fmt.Sprintf("refs/remotes/%s/%s", remote, upstreamBranch)
	status.RemoteRef = remoteTrackingRef

	remoteSHA, err := resolveRef(gitDir, remoteTrackingRef)
	if err != nil {
		return status, fmt.Errorf("store %s: unable to resolve tracking ref %s: %w; run: git -C %s fetch", storeDir, remoteTrackingRef, err, storeDir)
	}
	status.RemoteSHA = remoteSHA

	if localSHA != remoteSHA {
		status.Clean = false
		return status, fmt.Errorf("working copy HEAD (%s) differs from remote-tracking ref %s (%s); run: git -C %s pull --ff-only", localSHA[:min(7, len(localSHA))], remoteTrackingRef, remoteSHA[:min(7, len(remoteSHA))], storeDir)
	}

	status.Clean = true
	return status, nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func resolveRef(gitDir, refPath string) (string, error) {
	// Try direct file first
	loosePath := filepath.Join(gitDir, filepath.FromSlash(refPath))
	data, err := os.ReadFile(loosePath)
	if err == nil {
		return strings.TrimSpace(string(data)), nil
	}

	// Try packed-refs
	packedPath := filepath.Join(gitDir, "packed-refs")
	f, err := os.Open(packedPath)
	if err != nil {
		return "", fmt.Errorf("ref %s not found in loose files or packed-refs", refPath)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "#") || strings.HasPrefix(line, "^") || line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[1] == refPath {
			return fields[0], nil
		}
	}
	return "", fmt.Errorf("ref %s not found", refPath)
}

func readBranchConfig(gitDir, branch string) (remote, merge string, err error) {
	configPath := filepath.Join(gitDir, "config")
	f, err := os.Open(configPath)
	if err != nil {
		return "", "", fmt.Errorf("unable to read .git/config: %w", err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	inBranchSection := false
	targetHeader := fmt.Sprintf("[branch %q]", branch)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "[") {
			inBranchSection = (line == targetHeader)
			continue
		}
		if !inBranchSection {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])
		switch key {
		case "remote":
			remote = val
		case "merge":
			merge = val
		}
	}
	return remote, merge, scanner.Err()
}
