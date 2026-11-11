package main

import (
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSelfContained holds secrets to the standard library, golang.org/x/sys (system calls) and
// gopkg.in/yaml.v3 (seat add's .sops.yaml rewrite), so it can
// be lifted out of this repository as it is: it reads the imports of every
// .go file here; the tests may also import testify.
func TestSelfContained(t *testing.T) {
	t.Parallel()
	files, err := filepath.Glob("*.go")
	require.NoError(t, err)
	require.NotEmpty(t, files)
	for _, name := range files {
		f, err := parser.ParseFile(token.NewFileSet(), name, nil, parser.ImportsOnly)
		require.NoError(t, err)
		for _, imp := range f.Imports {
			path, err := strconv.Unquote(imp.Path.Value)
			require.NoError(t, err)
			assert.True(t, allowed(path, strings.HasSuffix(name, "_test.go")),
				"%s imports %s: secrets imports only the standard library, golang.org/x/sys and gopkg.in/yaml.v3", name, path)
		}
	}
}

func allowed(path string, test bool) bool {
	under := func(mod string) bool { return path == mod || strings.HasPrefix(path, mod+"/") }
	std := !strings.Contains(strings.SplitN(path, "/", 2)[0], ".")
	return std || under("golang.org/x/sys") || under("gopkg.in/yaml.v3") || (test && under("github.com/stretchr/testify"))
}

// TestAllowedRefusesTheRepository is the rule's reversed witness.
func TestAllowedRefusesTheRepository(t *testing.T) {
	t.Parallel()
	assert.False(t, allowed("github.com/mas-bandwidth/nova-tools/v2/pkg/tool", false))
	assert.False(t, allowed("github.com/mas-bandwidth/nova-tools/v2/pkg/tool", true))
	assert.False(t, allowed("github.com/stretchr/testify/require", false))
	assert.True(t, allowed("golang.org/x/sys/unix", false))
	assert.False(t, allowed("github.com/redis/go-redis/v9", false))
	assert.True(t, allowed("net/http", false))
}
