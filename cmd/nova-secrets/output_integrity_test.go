package main

import (
	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/goenv"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// loadPackagesWithJSON loads packages using go list -json to avoid conflicts with -f flag.
func loadPackagesWithJSON(t *testing.T, dir string, tests bool) []goListPackage {
	t.Helper()
	args := []string{"list", "-json"}
	if tests {
		args = append(args, "./...")
	} else {
		args = append(args, ".")
	}
	cmd := exec.Command("go", args...)
	cmd.Dir = dir
	cmd.Env = goenv.Clean(os.Environ())
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "failed to run go list in %s: %s", dir, string(output))

	var packages []goListPackage
	decoder := json.NewDecoder(strings.NewReader(string(output)))
	for decoder.More() {
		var p goListPackage
		err := decoder.Decode(&p)
		require.NoError(t, err, "failed to decode go list output in %s: %v", dir, err)
		packages = append(packages, p)
	}
	require.NotEmpty(t, packages, "no package loaded from %s", dir)
	return packages
}

type goListPackage struct {
	Name    string   `json:"Name"`
	Dir     string   `json:"Dir"`
	Imports []string `json:"Imports"`
}

// Test 16: TestNoKeychainAndNoCryptoDependency
func TestNoKeychainAndNoCryptoDependency(t *testing.T) {
	t.Parallel()
	pkgs := []string{"cmd/nova-secrets", "pkg/secrets"}

	forbiddenImports := []string{
		"filippo.io/age",
		"getsops",
		"security",
		"keychain",
		"github.com/keybase/go-keychain",
	}

	for _, pkg := range pkgs {
		dir := filepath.Join("..", "..", pkg)
		// If running inside cmd/nova-secrets
		if _, err := os.Stat(dir); err != nil {
			dir = filepath.Join(".", pkg)
			if _, err2 := os.Stat(dir); err2 != nil {
				// Try from repo root
				dir = pkg
			}
		}

		loaded := loadPackagesWithJSON(t, dir, true)

		for _, lp := range loaded {
			for _, imp := range lp.Imports {
				for _, forb := range forbiddenImports {
					assert.NotContains(t, imp, forb, "%s imports %s; forbidden cryptography or keychain dependency", lp.Name, imp)
				}
			}
		}

		// Also check file content for security binary invocations
		_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			content, _ := os.ReadFile(path)
			sContent := string(content)
			assert.NotContains(t, sContent, `"security"`, "%s contains reference to security binary", path)
			return nil
		})
	}
}

func TestAsPathTraversalRefused(t *testing.T) {
	t.Parallel()
	bin := buildNovaSecrets(t)
	_, stderr, code := runNovaSecrets(bin, "names", "--store", "/any/path", "--as", "../outside")
	assert.Equal(t, 2, code, "expected exit 2 on path traversal in --as, got %d", code)
	assert.Contains(t, stderr, "invalid seat name", "expected invalid seat name refusal, got: %s", stderr)
}
