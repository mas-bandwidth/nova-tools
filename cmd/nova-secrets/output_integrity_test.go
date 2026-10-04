package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/tools/go/packages"
)

// Test 16: TestNoKeychainAndNoCryptoDependency
func TestNoKeychainAndNoCryptoDependency(t *testing.T) {
	t.Parallel()
	pkgs := []string{"cmd/nova-secrets", "internal/secrets"}

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

		// golang.org/x/tools/go/packages, the replacement parser.ParseDir's
		// deprecation names (SA1019): the imports of the package and its test
		// variants as the build reads them.
		cfg := packages.Config{Mode: packages.NeedName | packages.NeedImports, Dir: dir, Tests: true}
		loaded, err := packages.Load(&cfg, ".")
		require.NoError(t, err, "failed to load package in %s: %v", dir, err)
		require.NotEmpty(t, loaded, "no package loaded from %s", dir)

		for _, lp := range loaded {
			for imp := range lp.Imports {
				for _, forb := range forbiddenImports {
					assert.NotContains(t, imp, forb, "%s imports %s; forbidden cryptography or keychain dependency", lp.PkgPath, imp)
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
