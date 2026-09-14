package secrets

import (
	"bufio"
	"bytes"
	"compress/zlib"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
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
	if !isValidHexSHA(localSHA) {
		return status, fmt.Errorf("store %s: invalid local branch SHA %q", storeDir, localSHA)
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
	if !isValidHexSHA(remoteSHA) {
		return status, fmt.Errorf("store %s: invalid remote tracking SHA %q", storeDir, remoteSHA)
	}
	status.RemoteSHA = remoteSHA

	if localSHA != remoteSHA {
		status.Clean = false
		return status, fmt.Errorf("working copy HEAD (%s) differs from remote-tracking ref %s (%s); run: git -C %s pull --ff-only", localSHA[:min(7, len(localSHA))], remoteTrackingRef, remoteSHA[:min(7, len(remoteSHA))], storeDir)
	}

	status.Clean = true
	return status, nil
}

func isValidHexSHA(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
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
		sha := strings.TrimSpace(string(data))
		if isValidHexSHA(sha) {
			return sha, nil
		}
		return "", fmt.Errorf("invalid ref content in %s: %q", loosePath, sha)
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
			sha := fields[0]
			if isValidHexSHA(sha) {
				return sha, nil
			}
			return "", fmt.Errorf("invalid ref content for %s in packed-refs: %q", refPath, sha)
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

// ReadHEADTreeBlobs reads the tree of the HEAD commit and returns a map of relative path -> blob SHA1.
// It inspects loose objects directly in pure Go, with fallback to git ls-tree if needed.
func ReadHEADTreeBlobs(storeDir string) (map[string]string, error) {
	gitDir := filepath.Join(storeDir, ".git")
	headBytes, err := os.ReadFile(filepath.Join(gitDir, "HEAD"))
	if err != nil {
		return nil, fmt.Errorf("unable to read .git/HEAD: %w", err)
	}
	headContent := strings.TrimSpace(string(headBytes))
	var commitSHA string
	if strings.HasPrefix(headContent, "ref: refs/heads/") {
		branch := strings.TrimPrefix(headContent, "ref: refs/heads/")
		commitSHA, err = resolveRef(gitDir, "refs/heads/"+branch)
		if err != nil {
			return nil, fmt.Errorf("unable to resolve branch %s: %w", branch, err)
		}
	} else if isValidHexSHA(headContent) {
		commitSHA = headContent
	} else {
		return nil, fmt.Errorf("invalid HEAD ref or commit %q", headContent)
	}

	if !isValidHexSHA(commitSHA) {
		return nil, fmt.Errorf("invalid commit SHA %q in HEAD", commitSHA)
	}

	// Try pure-Go loose object reading first
	blobs, err := readLooseCommitTree(gitDir, commitSHA)
	if err == nil {
		return blobs, nil
	}

	// Fallback to git ls-tree -r if available
	cmd := exec.Command("git", "-C", storeDir, "ls-tree", "-r", commitSHA)
	out, kErr := cmd.Output()
	if kErr == nil {
		res := make(map[string]string)
		scanner := bufio.NewScanner(bytes.NewReader(out))
		for scanner.Scan() {
			line := scanner.Text()
			tabIdx := strings.IndexByte(line, '\t')
			if tabIdx < 0 {
				continue
			}
			filePath := line[tabIdx+1:]
			meta := line[:tabIdx]
			fields := strings.Fields(meta)
			if len(fields) >= 3 && fields[1] == "blob" {
				res[filepath.Clean(filepath.ToSlash(filePath))] = fields[2]
			}
		}
		return res, nil
	}

	return nil, fmt.Errorf("unable to read HEAD tree objects: %w", err)
}

func readLooseObject(gitDir, sha string) (string, []byte, error) {
	if len(sha) < 2 {
		return "", nil, fmt.Errorf("sha too short")
	}
	loosePath := filepath.Join(gitDir, "objects", sha[:2], sha[2:])
	f, err := os.Open(loosePath)
	if err != nil {
		return "", nil, err
	}
	defer f.Close()

	zr, err := zlib.NewReader(f)
	if err != nil {
		return "", nil, err
	}
	defer zr.Close()

	data, err := io.ReadAll(zr)
	if err != nil {
		return "", nil, err
	}

	nullIdx := bytes.IndexByte(data, 0)
	if nullIdx < 0 {
		return "", nil, fmt.Errorf("malformed loose object %s: missing null byte", sha)
	}
	header := string(data[:nullIdx])
	parts := strings.SplitN(header, " ", 2)
	if len(parts) != 2 {
		return "", nil, fmt.Errorf("malformed loose object %s header", sha)
	}
	objType := parts[0]
	return objType, data[nullIdx+1:], nil
}

func readLooseCommitTree(gitDir, commitSHA string) (map[string]string, error) {
	objType, data, err := readLooseObject(gitDir, commitSHA)
	if err != nil {
		return nil, err
	}
	if objType != "commit" {
		return nil, fmt.Errorf("object %s is not a commit (got %s)", commitSHA, objType)
	}

	var treeSHA string
	lines := strings.Split(string(data), "\n")
	for _, l := range lines {
		if strings.HasPrefix(l, "tree ") {
			treeSHA = strings.TrimSpace(strings.TrimPrefix(l, "tree "))
			break
		}
	}
	if treeSHA == "" || !isValidHexSHA(treeSHA) {
		return nil, fmt.Errorf("commit %s has invalid tree %q", commitSHA, treeSHA)
	}

	blobs := make(map[string]string)
	err = readLooseTreeRecursive(gitDir, treeSHA, "", blobs)
	if err != nil {
		return nil, err
	}
	return blobs, nil
}

func readLooseTreeRecursive(gitDir, treeSHA, prefix string, out map[string]string) error {
	objType, data, err := readLooseObject(gitDir, treeSHA)
	if err != nil {
		return err
	}
	if objType != "tree" {
		return fmt.Errorf("object %s is not a tree (got %s)", treeSHA, objType)
	}

	offset := 0
	for offset < len(data) {
		spaceIdx := bytes.IndexByte(data[offset:], ' ')
		if spaceIdx < 0 {
			break
		}
		mode := string(data[offset : offset+spaceIdx])
		offset += spaceIdx + 1

		nullIdx := bytes.IndexByte(data[offset:], 0)
		if nullIdx < 0 {
			return fmt.Errorf("corrupt tree object %s: missing null after name", treeSHA)
		}
		name := string(data[offset : offset+nullIdx])
		offset += nullIdx + 1

		if offset+20 > len(data) {
			return fmt.Errorf("corrupt tree object %s: truncated sha", treeSHA)
		}
		entrySHABytes := data[offset : offset+20]
		entrySHA := hex.EncodeToString(entrySHABytes)
		offset += 20

		fullPath := name
		if prefix != "" {
			fullPath = prefix + "/" + name
		}

		cleanPath := filepath.Clean(filepath.ToSlash(fullPath))
		if mode == "40000" || mode == "040000" {
			if err := readLooseTreeRecursive(gitDir, entrySHA, cleanPath, out); err != nil {
				return err
			}
		} else {
			out[cleanPath] = entrySHA
		}
	}
	return nil
}

// ValidateAdmissibleStore verifies that the git working copy at storeDir is an admissible store.
// It verifies:
// 1. Invariant 8 ref tracking: HEAD equals remote-tracking ref (via CheckGitWorkingCopy).
// 2. HEAD commit tree and git index can be read.
// 3. Every tracked store file (*.yaml, .sops.yaml, recovery.pub) in HEAD tree, git index, or working copy:
//   - exists in working copy (no deletions)
//   - is tracked in index and committed in HEAD tree (no untracked or uncommitted additions)
//   - working copy blob SHA1 matches HEAD tree blob SHA1 and git index blob SHA1 (no unstaged or staged modifications)
func ValidateAdmissibleStore(storeDir string) (status GitRefStatus, headBlobs map[string]string, indexData *GitIndexData, failures []CheckFailure, refusal error) {
	st, err := CheckGitWorkingCopy(storeDir)
	if err != nil {
		msg := err.Error()
		if strings.Contains(msg, "detached HEAD") || strings.Contains(msg, "no upstream") || strings.Contains(msg, "worktree or submodule") || strings.Contains(msg, "is not a git repository") {
			return st, nil, nil, nil, err
		}
		failures = append(failures, CheckFailure{
			Kind:   "stale-working-copy",
			File:   "working copy",
			Reason: msg,
		})
	}
	status = st

	hBlobs, headErr := ReadHEADTreeBlobs(storeDir)
	if headErr != nil {
		failures = append(failures, CheckFailure{
			Kind:   "stale-working-copy",
			File:   "HEAD",
			Reason: fmt.Sprintf("failed to read HEAD tree: %v", headErr),
		})
	}
	headBlobs = hBlobs

	idxData, idxErr := ReadGitIndex(storeDir)
	if idxErr != nil {
		failures = append(failures, CheckFailure{
			Kind:   "stale-working-copy",
			File:   ".git/index",
			Reason: fmt.Sprintf("failed to read git index: %v", idxErr),
		})
	}
	indexData = idxData

	if headErr != nil || idxErr != nil {
		return status, headBlobs, indexData, failures, nil
	}

	candidates := make(map[string]bool)
	for p := range headBlobs {
		if strings.HasSuffix(p, ".yaml") || p == "recovery.pub" {
			candidates[p] = true
		}
	}
	for p := range indexData.Entries {
		if strings.HasSuffix(p, ".yaml") || p == "recovery.pub" {
			candidates[p] = true
		}
	}

	var sortedPaths []string
	for p := range candidates {
		sortedPaths = append(sortedPaths, p)
	}
	sort.Strings(sortedPaths)

	for _, p := range sortedPaths {
		headSHA, inHead := headBlobs[p]
		indexEntry, inIndex := indexData.Entries[p]
		filePath := filepath.Join(storeDir, filepath.FromSlash(p))
		fi, statErr := os.Stat(filePath)

		if statErr != nil {
			if os.IsNotExist(statErr) {
				failures = append(failures, CheckFailure{
					Kind:   "stale-working-copy",
					File:   p,
					Reason: fmt.Sprintf("working copy differs from HEAD commit tree; missing tracked file %s", p),
				})
			} else {
				failures = append(failures, CheckFailure{
					Kind:   "stale-working-copy",
					File:   p,
					Reason: fmt.Sprintf("unable to stat %s: %v", p, statErr),
				})
			}
			continue
		}

		if fi.IsDir() {
			continue
		}

		if !inHead {
			failures = append(failures, CheckFailure{
				Kind:   "stale-working-copy",
				File:   p,
				Reason: fmt.Sprintf("working copy differs from HEAD commit tree; uncommitted file in index %s", p),
			})
		}
		if !inIndex {
			failures = append(failures, CheckFailure{
				Kind:   "stale-working-copy",
				File:   p,
				Reason: fmt.Sprintf("working copy differs from git index; untracked file %s", p),
			})
		}

		data, readErr := os.ReadFile(filePath)
		if readErr != nil {
			failures = append(failures, CheckFailure{
				Kind:   "stale-working-copy",
				File:   p,
				Reason: fmt.Sprintf("unable to read %s: %v", p, readErr),
			})
			continue
		}

		actualBlob := GitBlobSHA1(data)
		actualHexSHA := hex.EncodeToString(actualBlob[:])

		if inHead && actualHexSHA != headSHA {
			failures = append(failures, CheckFailure{
				Kind:   "stale-working-copy",
				File:   p,
				Reason: fmt.Sprintf("working copy differs from HEAD commit tree (working copy blob differs from HEAD tree); uncommitted modifications in %s", p),
			})
		}
		if inIndex && actualBlob != indexEntry.BlobSHA1 {
			failures = append(failures, CheckFailure{
				Kind:   "stale-working-copy",
				File:   p,
				Reason: fmt.Sprintf("working copy differs from git index; uncommitted changes in %s", p),
			})
		}
	}

	return status, headBlobs, indexData, failures, nil
}
