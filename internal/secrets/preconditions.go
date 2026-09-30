package secrets

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// CheckStorePreconditions checks directory existence, git working copy (.git),
// .sops.yaml presence, and branch/upstream preconditions against storeDir.
// It returns all verifiable violations in a single pass before any operation begins.
func CheckStorePreconditions(storeDir string, needUpstream bool, needBranch bool) []string {
	if storeDir == "" {
		return nil
	}
	fi, err := os.Stat(storeDir)
	if err != nil || !fi.IsDir() {
		return []string{fmt.Sprintf("store %s is not a directory; clone it: git clone <url> %s", storeDir, storeDir)}
	}

	var issues []string
	gitDir := filepath.Join(storeDir, ".git")
	gFi, err := os.Lstat(gitDir)
	if err != nil {
		issues = append(issues, fmt.Sprintf("store %s has no .git directory; clone it: git clone <url> %s", storeDir, storeDir))
		if needUpstream {
			issues = append(issues, fmt.Sprintf("store %s: branch with upstream tracking ref is required; check and exec compare HEAD with the ref the branch tracks (see: nova-secrets check --help)", storeDir))
		} else if needBranch {
			issues = append(issues, fmt.Sprintf("store %s is not on a branch; expected a named branch to return to (run: git -C %s switch <branch>)", storeDir, storeDir))
		}
	} else if !gFi.IsDir() {
		issues = append(issues, fmt.Sprintf("store %s: .git is a file (a worktree or submodule); expected a directory working copy", storeDir))
	} else {
		// .git directory exists
		if needUpstream {
			_, gitErr := CheckGitWorkingCopy(storeDir)
			if gitErr != nil {
				issues = append(issues, gitErr.Error())
			}
		} else if needBranch {
			headBytes, err := os.ReadFile(filepath.Join(gitDir, "HEAD"))
			if err == nil {
				headContent := strings.TrimSpace(string(headBytes))
				if !strings.HasPrefix(headContent, "ref: refs/heads/") {
					issues = append(issues, fmt.Sprintf("store %s is not on a branch; expected a named branch to return to (run: git -C %s switch <branch>)", storeDir, storeDir))
				}
			}
		}
	}

	sopsConfigPath := filepath.Join(storeDir, ".sops.yaml")
	if _, err := os.Stat(sopsConfigPath); err != nil {
		issues = append(issues, fmt.Sprintf("store %s carries no .sops.yaml; create it with creation_rules (see docs/SPEC-SECRETS.md)", storeDir))
	}

	return issues
}

// FormatRefusal formats missing required flags, invalid flag messages, and store
// precondition issues into a single refusal error, including an example command
// when required flags are missing or invalid.
func FormatRefusal(missingFlags []string, invalidFlags []string, storeIssues []string, example string) error {
	var parts []string
	if len(missingFlags) > 0 {
		parts = append(parts, fmt.Sprintf("missing required flags: %s", strings.Join(missingFlags, ", ")))
	}
	if len(invalidFlags) > 0 {
		parts = append(parts, strings.Join(invalidFlags, "; "))
	}
	if len(storeIssues) > 0 {
		parts = append(parts, strings.Join(storeIssues, "; "))
	}
	if len(parts) == 0 {
		return nil
	}
	msg := strings.Join(parts, "; ")
	if example != "" && (len(missingFlags) > 0 || len(invalidFlags) > 0) {
		msg += fmt.Sprintf("; example: %s", example)
	}
	return errors.New(msg)
}
