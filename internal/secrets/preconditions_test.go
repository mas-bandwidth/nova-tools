package secrets

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckStorePreconditions(t *testing.T) {
	td := t.TempDir()

	t.Run("nonexistent_directory", func(t *testing.T) {
		issues := CheckStorePreconditions(filepath.Join(td, "nonexistent"), false, false)
		if len(issues) != 1 || !strings.Contains(issues[0], "is not a directory; clone it: git clone") {
			t.Fatalf("expected not a directory issue, got: %v", issues)
		}
	})

	t.Run("file_not_a_directory", func(t *testing.T) {
		fPath := filepath.Join(td, "regular_file")
		if err := os.WriteFile(fPath, []byte("data"), 0644); err != nil {
			t.Fatal(err)
		}
		issues := CheckStorePreconditions(fPath, false, false)
		if len(issues) != 1 || !strings.Contains(issues[0], "is not a directory; clone it: git clone") {
			t.Fatalf("expected not a directory issue, got: %v", issues)
		}
	})

	t.Run("empty_dir_no_branch_no_upstream", func(t *testing.T) {
		dPath := filepath.Join(td, "empty_plain")
		if err := os.MkdirAll(dPath, 0755); err != nil {
			t.Fatal(err)
		}
		issues := CheckStorePreconditions(dPath, false, false)
		if len(issues) != 2 {
			t.Fatalf("expected 2 issues (no .git and no .sops.yaml), got %d: %v", len(issues), issues)
		}
		if !strings.Contains(issues[0], "has no .git directory; clone it: git clone") {
			t.Errorf("issue[0] should report no .git: %s", issues[0])
		}
		if !strings.Contains(issues[1], "carries no .sops.yaml; create it with creation_rules") {
			t.Errorf("issue[1] should report no .sops.yaml: %s", issues[1])
		}
	})

	t.Run("empty_dir_need_upstream", func(t *testing.T) {
		dPath := filepath.Join(td, "empty_upstream")
		if err := os.MkdirAll(dPath, 0755); err != nil {
			t.Fatal(err)
		}
		issues := CheckStorePreconditions(dPath, true, false)
		if len(issues) != 3 {
			t.Fatalf("expected 3 issues (no .git, upstream required, no .sops.yaml), got %d: %v", len(issues), issues)
		}
		if !strings.Contains(issues[0], "has no .git directory") {
			t.Errorf("issue[0] expected no .git: %s", issues[0])
		}
		if !strings.Contains(issues[1], "branch with upstream tracking ref is required") {
			t.Errorf("issue[1] expected upstream required: %s", issues[1])
		}
		if !strings.Contains(issues[2], "carries no .sops.yaml") {
			t.Errorf("issue[2] expected no .sops.yaml: %s", issues[2])
		}
	})

	t.Run("empty_dir_need_branch", func(t *testing.T) {
		dPath := filepath.Join(td, "empty_branch")
		if err := os.MkdirAll(dPath, 0755); err != nil {
			t.Fatal(err)
		}
		issues := CheckStorePreconditions(dPath, false, true)
		if len(issues) != 3 {
			t.Fatalf("expected 3 issues (no .git, not on a branch, no .sops.yaml), got %d: %v", len(issues), issues)
		}
		if !strings.Contains(issues[0], "has no .git directory") {
			t.Errorf("issue[0] expected no .git: %s", issues[0])
		}
		if !strings.Contains(issues[1], "is not on a branch; expected a named branch to return to") {
			t.Errorf("issue[1] expected not on a branch: %s", issues[1])
		}
		if !strings.Contains(issues[2], "carries no .sops.yaml") {
			t.Errorf("issue[2] expected no .sops.yaml: %s", issues[2])
		}
	})

	t.Run("git_is_file", func(t *testing.T) {
		dPath := filepath.Join(td, "git_as_file")
		if err := os.MkdirAll(dPath, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dPath, ".git"), []byte("gitdir: ../somewhere"), 0644); err != nil {
			t.Fatal(err)
		}
		issues := CheckStorePreconditions(dPath, true, false)
		if len(issues) != 2 {
			t.Fatalf("expected 2 issues (.git is file, no .sops.yaml), got %d: %v", len(issues), issues)
		}
		if !strings.Contains(issues[0], ".git is a file (a worktree or submodule); expected a directory working copy") {
			t.Errorf("issue[0] expected .git is a file: %s", issues[0])
		}
		if !strings.Contains(issues[1], "carries no .sops.yaml") {
			t.Errorf("issue[1] expected no .sops.yaml: %s", issues[1])
		}
	})

	t.Run("git_init_with_sops_yaml", func(t *testing.T) {
		dPath := filepath.Join(td, "git_with_sops")
		if err := os.MkdirAll(dPath, 0755); err != nil {
			t.Fatal(err)
		}
		_ = exec.Command("git", "init", "-q", dPath).Run()
		_ = exec.Command("git", "-C", dPath, "-c", "user.name=test", "-c", "user.email=test@example.com", "commit", "-q", "--allow-empty", "-m", "init").Run()
		if err := os.WriteFile(filepath.Join(dPath, ".sops.yaml"), []byte("creation_rules: []"), 0644); err != nil {
			t.Fatal(err)
		}

		// with needUpstream=false, should have 0 issues
		issues := CheckStorePreconditions(dPath, false, false)
		if len(issues) != 0 {
			t.Errorf("expected 0 issues, got: %v", issues)
		}

		// with needBranch=true, HEAD is on refs/heads/master or main, so should have 0 issues
		issuesBranch := CheckStorePreconditions(dPath, false, true)
		if len(issuesBranch) != 0 {
			t.Errorf("expected 0 issues, got: %v", issuesBranch)
		}

		// with needUpstream=true, branch has no upstream tracking ref configured
		issuesUpstream := CheckStorePreconditions(dPath, true, false)
		if len(issuesUpstream) != 1 || !strings.Contains(issuesUpstream[0], "has no upstream tracking branch configured in .git/config") {
			t.Errorf("expected 1 issue about upstream, got: %v", issuesUpstream)
		}
	})
}

func TestFormatRefusal(t *testing.T) {
	t.Run("nil_when_all_empty", func(t *testing.T) {
		err := FormatRefusal(nil, nil, nil, "example")
		if err != nil {
			t.Errorf("expected nil error, got: %v", err)
		}
	})

	t.Run("missing_flags_only", func(t *testing.T) {
		err := FormatRefusal([]string{"--store <dir>", "--as <name>"}, nil, nil, "nova-secrets names --store s --as a")
		if err == nil {
			t.Fatal("expected non-nil error")
		}
		got := err.Error()
		if !strings.Contains(got, "missing required flags: --store <dir>, --as <name>") {
			t.Errorf("missing flags formatted incorrectly: %s", got)
		}
		if !strings.Contains(got, "; example: nova-secrets names --store s --as a") {
			t.Errorf("example formatted incorrectly: %s", got)
		}
	})

	t.Run("store_issues_only_no_example", func(t *testing.T) {
		err := FormatRefusal(nil, nil, []string{"store has no .git", "store carries no .sops.yaml"}, "nova-secrets names")
		if err == nil {
			t.Fatal("expected non-nil error")
		}
		got := err.Error()
		if strings.Contains(got, "missing required flags") {
			t.Errorf("should not contain missing required flags: %s", got)
		}
		if strings.Contains(got, "example:") {
			t.Errorf("store issues alone should not append example: %s", got)
		}
		if !strings.Contains(got, "store has no .git; store carries no .sops.yaml") {
			t.Errorf("store issues formatted incorrectly: %s", got)
		}
	})

	t.Run("all_combined", func(t *testing.T) {
		err := FormatRefusal(
			[]string{"--as <name>"},
			[]string{"invalid flag --foo"},
			[]string{"store has no .git", "store carries no .sops.yaml"},
			"nova-secrets check ...",
		)
		if err == nil {
			t.Fatal("expected non-nil error")
		}
		got := err.Error()
		parts := strings.Split(got, "; ")
		if len(parts) != 5 {
			t.Errorf("expected 5 parts separated by '; ', got %d: %s", len(parts), got)
		}
		if parts[0] != "missing required flags: --as <name>" {
			t.Errorf("part 0 mismatch: %s", parts[0])
		}
		if parts[1] != "invalid flag --foo" {
			t.Errorf("part 1 mismatch: %s", parts[1])
		}
		if parts[2] != "store has no .git" {
			t.Errorf("part 2 mismatch: %s", parts[2])
		}
		if parts[3] != "store carries no .sops.yaml" {
			t.Errorf("part 3 mismatch: %s", parts[3])
		}
		if parts[4] != "example: nova-secrets check ..." {
			t.Errorf("part 4 mismatch: %s", parts[4])
		}
	})
}
