package main

import (
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Test 16: TestNoKeychainAndNoCryptoDependency
func TestNoKeychainAndNoCryptoDependency(t *testing.T) {
	t.Parallel()
	pkgs := []string{"cmd/nova-secrets", "internal/secrets"}
	fset := token.NewFileSet()

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

		pkgsMap, err := parser.ParseDir(fset, dir, nil, parser.ImportsOnly) // ignored: SA1019 parser.ParseDir deprecated since Go 1.25
		require.NoError(t, err, "failed to parse package in %s: %v", dir, err)

		for _, p := range pkgsMap {
			for fileName, f := range p.Files {
				for _, imp := range f.Imports {
					pathVal := strings.Trim(imp.Path.Value, `"`)
					for _, forb := range forbiddenImports {
						assert.NotContains(t, pathVal, forb, "%s imports %s; forbidden cryptography or keychain dependency", fileName, pathVal)
					}
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
