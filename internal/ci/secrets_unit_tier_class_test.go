package ci

import (
	"errors"
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/goenv"
	"github.com/mas-bandwidth/nova-tools/internal/testbin"
)

// THE CLASS RULE: THE UNIT TIER OF NOVA-SECRETS NEVER SPAWNS REAL SOPS OR REAL AGE-KEYGEN (#4301).
//
// nova-secrets unit tests in cmd/nova-secrets and internal/secrets run under a refusing
// PATH that blocks real sops and age-keygen. Heavy cryptographic operations (Scrypt, Argon2,
// AES-GCM, X25519) in real binaries caused 800%+ CPU spikes across shards on developer benches.
// The unit tier runs against fast fakes that assert argv, check envelopes, and test invariants
// in memory, while tests requiring real binaries run behind //go:build functional.

func TestSecretsUnitTierRefusesRealSopsAndAgeKeygen(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	shimDir := t.TempDir()

	// 1. Install refusing shims for sops, age-keygen, and age on PATH.
	for _, tool := range []string{"sops", "age-keygen", "age"} {
		script := fmt.Sprintf("#!/bin/sh\necho \"unit tier: %s was invoked (exit 86)\" >&2\nexit 86\n", tool)
		path := filepath.Join(shimDir, tool)
		if err := testbin.WriteExecutable(path, []byte(script), 0755); err != nil {
			t.Fatal(err)
		}
		if runtime.GOOS == "windows" {
			bat := fmt.Sprintf("@echo unit tier: %s was invoked (exit 86) 1>&2\r\n@exit /b 86\r\n", tool)
			if err := os.WriteFile(path+".bat", []byte(bat), 0755); err != nil {
				t.Fatal(err)
			}
		}
	}

	// 2. Control: verify the refusing shims fail closed when invoked directly.
	out, err := exec.Command(filepath.Join(shimDir, "sops"), "--version").CombinedOutput()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 86 || !strings.Contains(string(out), "exit 86") {
		t.Fatalf("refusing sops shim did not fail closed with exit 86: err=%v, out=%s", err, out)
	}

	out, err = exec.Command(filepath.Join(shimDir, "age-keygen"), "--version").CombinedOutput()
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 86 || !strings.Contains(string(out), "exit 86") {
		t.Fatalf("refusing age-keygen shim did not fail closed with exit 86: err=%v, out=%s", err, out)
	}

	// 3. Execute unit tests in cmd/nova-secrets and internal/secrets under the refusing PATH.
	cmd := exec.Command("go", "test", "-p", "2", "-parallel", "2", "-count=1", "./cmd/nova-secrets/...", "./internal/secrets/...")
	cmd.Dir = root
	refusingPath := shimDir + string(os.PathListSeparator) + os.Getenv("PATH")
	cmd.Env = append(goenv.Clean(os.Environ()), "PATH="+refusingPath)

	testOut, testErr := cmd.CombinedOutput()
	if testErr != nil {
		t.Fatalf("unit tests failed under refusing sops/age-keygen PATH: %v\noutput:\n%s", testErr, string(testOut))
	}

	if strings.Contains(string(testOut), "exit 86") || strings.Contains(string(testOut), "was invoked") {
		t.Fatalf("unit tests invoked a refusing shim:\n%s", string(testOut))
	}
}

// TestSecretsUnitTierHasNoDirectLookPathForSopsOrAgeKeygen scans all unit test files
// in cmd/nova-secrets and internal/secrets and refuses any direct LookPath for "sops"
// or "age-keygen", or hardcoded homebrew paths. Real binaries are reserved for the
// functional test tier (//go:build functional).
func TestSecretsUnitTierHasNoDirectLookPathForSopsOrAgeKeygen(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	dirs := []string{
		filepath.Join(root, "cmd", "nova-secrets"),
		filepath.Join(root, "internal", "secrets"),
	}

	fset := token.NewFileSet()
	for _, dir := range dirs {
		pkgs, err := parser.ParseDir(fset, dir, func(fi os.FileInfo) bool {
			name := fi.Name()
			if !strings.HasSuffix(name, "_test.go") {
				return false
			}
			// Skip functional and slow test files
			if strings.HasSuffix(name, "_functional_test.go") || strings.HasSuffix(name, "slow_test.go") {
				return false
			}
			return true
		}, parser.ParseComments)
		if err != nil {
			t.Fatal(err)
		}

		for _, pkg := range pkgs {
			for fname, file := range pkg.Files {
				src, err := os.ReadFile(fname)
				if err != nil {
					t.Fatal(err)
				}
				content := string(src)
				// Check for hardcoded paths to real tools in unit tests
				rel, _ := filepath.Rel(root, fname)
				for _, forbidden := range []string{
					`"/opt/homebrew/bin/sops"`,
					`"/opt/homebrew/bin/age-keygen"`,
					`"/usr/local/bin/sops"`,
					`"/usr/local/bin/age-keygen"`,
				} {
					if strings.Contains(content, forbidden) {
						t.Errorf("%s contains forbidden hardcoded binary path %s (unit tests must use unit fakes)", rel, forbidden)
					}
				}
				_ = file
			}
		}
	}
}
