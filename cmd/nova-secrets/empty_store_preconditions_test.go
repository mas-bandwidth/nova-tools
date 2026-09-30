package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestEmptyStorePreconditionsAllVerbsOneRefusal verifies Canon Property P1:
// verbs names, check, exec, seat (add and inject), seal, and place report every
// missing flag and every store precondition (.git working copy, .sops.yaml,
// branch with upstream / named branch) in one refusal, each with its remedy,
// tested against an empty directory.
func TestEmptyStorePreconditionsAllVerbsOneRefusal(t *testing.T) {
	t.Parallel()
	bin := buildNovaSecrets(t)

	td := t.TempDir()
	emptyStore := filepath.Join(td, "empty_store")
	if err := os.MkdirAll(emptyStore, 0755); err != nil {
		t.Fatal(err)
	}

	validPub := "age1ql3z7hjy54pw3hyww5ayyfg7zqgvc7w3j2elw8zmrj2kg5sfn9aqmcac8p"

	t.Run("names_missing_flags_and_empty_store", func(t *testing.T) {
		_, errOut, code := runNovaSecrets(bin, "names", "--store", emptyStore)
		if code != 2 {
			t.Fatalf("expected code 2, got %d: %s", code, errOut)
		}
		lines := strings.Split(strings.TrimSpace(errOut), "\n")
		if len(lines) != 1 {
			t.Errorf("expected 1 line, got %d: %s", len(lines), errOut)
		}
		for _, want := range []string{
			"missing required flags: --as <name>",
			"has no .git directory; clone it: git clone <url>",
			"carries no .sops.yaml; create it with creation_rules",
			"example: nova-secrets names",
		} {
			if !strings.Contains(errOut, want) {
				t.Errorf("refusal missing %q; got: %s", want, errOut)
			}
		}
	})

	t.Run("names_all_flags_with_empty_store", func(t *testing.T) {
		_, errOut, code := runNovaSecrets(bin, "names", "--store", emptyStore, "--as", "testseat")
		if code != 2 {
			t.Fatalf("expected code 2, got %d: %s", code, errOut)
		}
		lines := strings.Split(strings.TrimSpace(errOut), "\n")
		if len(lines) != 1 {
			t.Errorf("expected 1 line, got %d: %s", len(lines), errOut)
		}
		for _, want := range []string{
			"has no .git directory; clone it: git clone <url>",
			"carries no .sops.yaml; create it with creation_rules",
		} {
			if !strings.Contains(errOut, want) {
				t.Errorf("refusal missing %q; got: %s", want, errOut)
			}
		}
		if strings.Contains(errOut, "missing required flags") {
			t.Errorf("should not report missing flags when flags are provided; got: %s", errOut)
		}
	})

	t.Run("check_missing_flags_and_empty_store", func(t *testing.T) {
		_, errOut, code := runNovaSecrets(bin, "check", "--store", emptyStore)
		if code != 2 {
			t.Fatalf("expected code 2, got %d: %s", code, errOut)
		}
		lines := strings.Split(strings.TrimSpace(errOut), "\n")
		if len(lines) != 1 {
			t.Errorf("expected 1 line, got %d: %s", len(lines), errOut)
		}
		for _, want := range []string{
			"--as <name>",
			"--key <path>",
			"--sops <path>",
			"has no .git directory; clone it: git clone <url>",
			"branch with upstream tracking ref is required",
			"carries no .sops.yaml; create it with creation_rules",
			"example: nova-secrets check",
		} {
			if !strings.Contains(errOut, want) {
				t.Errorf("refusal missing %q; got: %s", want, errOut)
			}
		}
	})

	t.Run("check_all_flags_with_empty_store", func(t *testing.T) {
		_, errOut, code := runNovaSecrets(bin, "check", "--store", emptyStore, "--as", "testseat", "--key", "/dummy/key", "--sops", "/dummy/sops")
		if code != 2 {
			t.Fatalf("expected code 2, got %d: %s", code, errOut)
		}
		lines := strings.Split(strings.TrimSpace(errOut), "\n")
		if len(lines) != 1 {
			t.Errorf("expected 1 line, got %d: %s", len(lines), errOut)
		}
		for _, want := range []string{
			"has no .git directory; clone it: git clone <url>",
			"branch with upstream tracking ref is required",
			"carries no .sops.yaml; create it with creation_rules",
		} {
			if !strings.Contains(errOut, want) {
				t.Errorf("refusal missing %q; got: %s", want, errOut)
			}
		}
		if strings.Contains(errOut, "missing required flags") {
			t.Errorf("should not report missing flags when flags are provided; got: %s", errOut)
		}
	})

	t.Run("exec_missing_flags_and_empty_store", func(t *testing.T) {
		_, errOut, code := runNovaSecrets(bin, "exec", "--store", emptyStore, "--", "true")
		if code != 125 {
			t.Fatalf("expected code 125, got %d: %s", code, errOut)
		}
		lines := strings.Split(strings.TrimSpace(errOut), "\n")
		if len(lines) != 1 {
			t.Errorf("expected 1 line, got %d: %s", len(lines), errOut)
		}
		for _, want := range []string{
			"--as <name>",
			"--key <path>",
			"--sops <path>",
			"--only <names|all>",
			"has no .git directory; clone it: git clone <url>",
			"branch with upstream tracking ref is required",
			"carries no .sops.yaml; create it with creation_rules",
			"example: nova-secrets exec",
		} {
			if !strings.Contains(errOut, want) {
				t.Errorf("refusal missing %q; got: %s", want, errOut)
			}
		}
	})

	t.Run("exec_all_flags_with_empty_store", func(t *testing.T) {
		_, errOut, code := runNovaSecrets(bin, "exec", "--store", emptyStore, "--as", "testseat", "--key", "/dummy/key", "--sops", "/dummy/sops", "--only", "all", "--", "true")
		if code != 125 {
			t.Fatalf("expected code 125, got %d: %s", code, errOut)
		}
		lines := strings.Split(strings.TrimSpace(errOut), "\n")
		if len(lines) != 1 {
			t.Errorf("expected 1 line, got %d: %s", len(lines), errOut)
		}
		for _, want := range []string{
			"has no .git directory; clone it: git clone <url>",
			"branch with upstream tracking ref is required",
			"carries no .sops.yaml; create it with creation_rules",
		} {
			if !strings.Contains(errOut, want) {
				t.Errorf("refusal missing %q; got: %s", want, errOut)
			}
		}
		if strings.Contains(errOut, "missing required flags") {
			t.Errorf("should not report missing flags when flags are provided; got: %s", errOut)
		}
	})

	t.Run("seat_add_missing_flags_and_empty_store", func(t *testing.T) {
		_, errOut, code := runNovaSecrets(bin, "seat", "add", "--store", emptyStore)
		if code != 2 {
			t.Fatalf("expected code 2, got %d: %s", code, errOut)
		}
		lines := strings.Split(strings.TrimSpace(errOut), "\n")
		if len(lines) != 1 {
			t.Errorf("expected 1 line, got %d: %s", len(lines), errOut)
		}
		for _, want := range []string{
			"--as <seat>",
			"--pub <age1…>",
			"--from <source-seat>",
			"--only <NAME,…>",
			"--key <path>",
			"--sops <path>",
			"has no .git directory; clone it: git clone <url>",
			"carries no .sops.yaml; create it with creation_rules",
			"example: nova-secrets seat add",
		} {
			if !strings.Contains(errOut, want) {
				t.Errorf("refusal missing %q; got: %s", want, errOut)
			}
		}
	})

	t.Run("seat_add_all_flags_with_empty_store", func(t *testing.T) {
		_, errOut, code := runNovaSecrets(bin, "seat", "add", "--store", emptyStore, "--as", "newseat", "--pub", validPub, "--from", "oldseat", "--only", "KEY1", "--key", "/dummy/key", "--sops", "/dummy/sops")
		if code != 2 {
			t.Fatalf("expected code 2, got %d: %s", code, errOut)
		}
		lines := strings.Split(strings.TrimSpace(errOut), "\n")
		if len(lines) != 1 {
			t.Errorf("expected 1 line, got %d: %s", len(lines), errOut)
		}
		for _, want := range []string{
			"has no .git directory; clone it: git clone <url>",
			"carries no .sops.yaml; create it with creation_rules",
		} {
			if !strings.Contains(errOut, want) {
				t.Errorf("refusal missing %q; got: %s", want, errOut)
			}
		}
		if strings.Contains(errOut, "missing required flags") {
			t.Errorf("should not report missing flags when flags are provided; got: %s", errOut)
		}
	})

	t.Run("seat_inject_missing_flags_and_empty_store", func(t *testing.T) {
		_, errOut, code := runNovaSecrets(bin, "seat", "inject", "--store", emptyStore)
		if code != 2 {
			t.Fatalf("expected code 2, got %d: %s", code, errOut)
		}
		lines := strings.Split(strings.TrimSpace(errOut), "\n")
		if len(lines) != 1 {
			t.Errorf("expected 1 line, got %d: %s", len(lines), errOut)
		}
		for _, want := range []string{
			"--as <seat>",
			"--from <source-seat>",
			"--only <NAME,…>",
			"--key <path>",
			"--sops <path>",
			"has no .git directory; clone it: git clone <url>",
			"is not on a branch; expected a named branch to return to",
			"carries no .sops.yaml; create it with creation_rules",
			"example: nova-secrets seat inject",
		} {
			if !strings.Contains(errOut, want) {
				t.Errorf("refusal missing %q; got: %s", want, errOut)
			}
		}
	})

	t.Run("seat_inject_all_flags_with_empty_store", func(t *testing.T) {
		_, errOut, code := runNovaSecrets(bin, "seat", "inject", "--store", emptyStore, "--as", "targetseat", "--from", "sourceseat", "--only", "KEY1", "--key", "/dummy/key", "--sops", "/dummy/sops")
		if code != 2 {
			t.Fatalf("expected code 2, got %d: %s", code, errOut)
		}
		lines := strings.Split(strings.TrimSpace(errOut), "\n")
		if len(lines) != 1 {
			t.Errorf("expected 1 line, got %d: %s", len(lines), errOut)
		}
		for _, want := range []string{
			"has no .git directory; clone it: git clone <url>",
			"is not on a branch; expected a named branch to return to",
			"carries no .sops.yaml; create it with creation_rules",
		} {
			if !strings.Contains(errOut, want) {
				t.Errorf("refusal missing %q; got: %s", want, errOut)
			}
		}
		if strings.Contains(errOut, "missing required flags") {
			t.Errorf("should not report missing flags when flags are provided; got: %s", errOut)
		}
	})

	t.Run("seal_missing_flags_and_empty_store", func(t *testing.T) {
		_, errOut, code := runNovaSecrets(bin, "seal", "--store", emptyStore)
		if code != 2 {
			t.Fatalf("expected code 2, got %d: %s", code, errOut)
		}
		lines := strings.Split(strings.TrimSpace(errOut), "\n")
		if len(lines) != 1 {
			t.Errorf("expected 1 line, got %d: %s", len(lines), errOut)
		}
		for _, want := range []string{
			"--as <name>",
			"--key <path>",
			"--sops <path>",
			"--name <NAME>",
			"has no .git directory; clone it: git clone <url>",
			"is not on a branch; expected a named branch to return to",
			"carries no .sops.yaml; create it with creation_rules",
			"example: nova-secrets seal",
		} {
			if !strings.Contains(errOut, want) {
				t.Errorf("refusal missing %q; got: %s", want, errOut)
			}
		}
	})

	t.Run("seal_all_flags_with_empty_store", func(t *testing.T) {
		_, errOut, code := runNovaSecrets(bin, "seal", "--store", emptyStore, "--as", "testseat", "--name", "API_KEY", "--key", "/dummy/key", "--sops", "/dummy/sops")
		if code != 2 {
			t.Fatalf("expected code 2, got %d: %s", code, errOut)
		}
		lines := strings.Split(strings.TrimSpace(errOut), "\n")
		if len(lines) != 1 {
			t.Errorf("expected 1 line, got %d: %s", len(lines), errOut)
		}
		for _, want := range []string{
			"has no .git directory; clone it: git clone <url>",
			"is not on a branch; expected a named branch to return to",
			"carries no .sops.yaml; create it with creation_rules",
		} {
			if !strings.Contains(errOut, want) {
				t.Errorf("refusal missing %q; got: %s", want, errOut)
			}
		}
		if strings.Contains(errOut, "missing required flags") {
			t.Errorf("should not report missing flags when flags are provided; got: %s", errOut)
		}
	})

	t.Run("place_missing_flags_and_empty_store", func(t *testing.T) {
		_, errOut, code := runNovaSecrets(bin, "place", "--store", emptyStore)
		if code != 2 {
			t.Fatalf("expected code 2, got %d: %s", code, errOut)
		}
		lines := strings.Split(strings.TrimSpace(errOut), "\n")
		if len(lines) != 1 {
			t.Errorf("expected 1 line, got %d: %s", len(lines), errOut)
		}
		for _, want := range []string{
			"--machine <name>",
			"--secret <name>",
			"--as <name>",
			"--key <path>",
			"--sops <path>",
			"has no .git directory; clone it: git clone <url>",
			"carries no .sops.yaml; create it with creation_rules",
			"example: nova-secrets place",
		} {
			if !strings.Contains(errOut, want) {
				t.Errorf("refusal missing %q; got: %s", want, errOut)
			}
		}
	})

	t.Run("place_all_flags_with_empty_store", func(t *testing.T) {
		_, errOut, code := runNovaSecrets(bin, "place", "--store", emptyStore, "--as", "testseat", "--machine", "mini", "--secret", "API_KEY", "--key", "/dummy/key", "--sops", "/dummy/sops")
		if code != 2 {
			t.Fatalf("expected code 2, got %d: %s", code, errOut)
		}
		lines := strings.Split(strings.TrimSpace(errOut), "\n")
		if len(lines) != 1 {
			t.Errorf("expected 1 line, got %d: %s", len(lines), errOut)
		}
		for _, want := range []string{
			"has no .git directory; clone it: git clone <url>",
			"carries no .sops.yaml; create it with creation_rules",
		} {
			if !strings.Contains(errOut, want) {
				t.Errorf("refusal missing %q; got: %s", want, errOut)
			}
		}
		if strings.Contains(errOut, "missing required flags") {
			t.Errorf("should not report missing flags when flags are provided; got: %s", errOut)
		}
	})

	t.Run("git_repo_without_sops_and_upstream_reported_together", func(t *testing.T) {
		gitStore := filepath.Join(td, "git_store")
		if err := os.MkdirAll(gitStore, 0755); err != nil {
			t.Fatal(err)
		}
		if out, err := exec.Command("git", "init", "-q", gitStore).CombinedOutput(); err != nil {
			t.Fatalf("git init: %v, out: %s", err, out)
		}
		if out, err := exec.Command("git", "-C", gitStore, "-c", "user.name=test", "-c", "user.email=test@example.com", "commit", "-q", "--allow-empty", "-m", "init").CombinedOutput(); err != nil {
			t.Fatalf("git commit: %v, out: %s", err, out)
		}

		// check on gitStore with all flags
		_, errOut, code := runNovaSecrets(bin, "check", "--store", gitStore, "--as", "testseat", "--key", "/dummy/key", "--sops", "/dummy/sops")
		if code != 2 {
			t.Fatalf("expected code 2, got %d: %s", code, errOut)
		}
		lines := strings.Split(strings.TrimSpace(errOut), "\n")
		if len(lines) != 1 {
			t.Errorf("expected 1 line, got %d: %s", len(lines), errOut)
		}
		if !strings.Contains(errOut, "carries no .sops.yaml; create it with creation_rules") {
			t.Errorf("refusal must report missing .sops.yaml: %s", errOut)
		}
		if !strings.Contains(errOut, "has no upstream tracking branch configured in .git/config") {
			t.Errorf("refusal must report missing upstream ref: %s", errOut)
		}
		if strings.Contains(errOut, "has no .git directory") {
			t.Errorf("store has .git directory, should not report no .git: %s", errOut)
		}

		// exec on gitStore with all flags
		_, errOut, code = runNovaSecrets(bin, "exec", "--store", gitStore, "--as", "testseat", "--key", "/dummy/key", "--sops", "/dummy/sops", "--only", "all", "--", "true")
		if code != 125 {
			t.Fatalf("expected code 125, got %d: %s", code, errOut)
		}
		lines = strings.Split(strings.TrimSpace(errOut), "\n")
		if len(lines) != 1 {
			t.Errorf("expected 1 line, got %d: %s", len(lines), errOut)
		}
		if !strings.Contains(errOut, "carries no .sops.yaml; create it with creation_rules") {
			t.Errorf("refusal must report missing .sops.yaml: %s", errOut)
		}
		if !strings.Contains(errOut, "has no upstream tracking branch configured in .git/config") {
			t.Errorf("refusal must report missing upstream ref: %s", errOut)
		}
		if strings.Contains(errOut, "has no .git directory") {
			t.Errorf("store has .git directory, should not report no .git: %s", errOut)
		}
	})
}
