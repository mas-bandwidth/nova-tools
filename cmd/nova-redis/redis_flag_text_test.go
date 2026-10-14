package main

// redis_flag_text_test.go pins the text that names the store and what a write
// to it does: the remedies nova-table and nova-redis print and every example
// in nova-redis's help spell the store flag --redis, the family's one name
// (docs/STANDARD.md section 2, "One shape across the set"; --addr is the old
// spelling, kept for one release), and a verb that writes to the store over
// the network states the delivery class, never a write of files on this
// machine.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/onboarding"
	"github.com/mas-bandwidth/nova-tools/pkg/tool"
)

// TestRemediesAndExamplesSayRedisAndStoreWritesAreLabelled: nova-table's
// remedies (read from its source, a main package this one cannot import) and
// nova-redis's own name --redis and not --addr; every example the help prints
// (the banner's example block and each verb's) does not spell --addr, and the
// banner's store examples spell --redis; fn load and spill are labelled
// delivery, not "files on this machine".
func TestRemediesAndExamplesSayRedisAndStoreWritesAreLabelled(t *testing.T) {
	t.Parallel()

	var remedies []string
	src := filepath.Join("..", "nova-table", "library.go")
	f, err := parser.ParseFile(token.NewFileSet(), src, nil, 0)
	require.NoError(t, err, "parse %s", src)
	ast.Inspect(f, func(n ast.Node) bool {
		lit, ok := n.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		if s, err := strconv.Unquote(lit.Value); err == nil && strings.Contains(s, "nova-redis fn load") {
			remedies = append(remedies, s)
		}
		return true
	})
	require.NotEmpty(t, remedies, "%s names no `nova-redis fn load` remedy", src)
	remedies = append(remedies, library().Remedy)
	for _, r := range remedies {
		assert.Contains(t, r, "--redis", "the remedy %q does not name --redis", r)
		assert.NotContains(t, r, "--addr", "the remedy %q names the old spelling --addr", r)
	}

	banner, err := onboarding.ExampleLines(usage, "nova-redis")
	require.NoError(t, err, err)
	require.NotEmpty(t, banner)
	for _, line := range banner {
		assert.NotContains(t, line, "--addr", "the banner's example %q names the old spelling --addr", line)
		if strings.Contains(line, " spill ") || strings.Contains(line, " recall ") {
			assert.Contains(t, line, "--redis", "the banner's store example %q does not name --redis", line)
		}
	}

	rt := redisTool(deps{})
	effects := map[string]tool.Effect{}
	for _, v := range rt.Verbs {
		effects[v.Name] = v.Effect
		for _, line := range strings.Split(v.Example, "\n") {
			assert.NotContains(t, line, "--addr", "%s's example %q names the old spelling --addr", v.Name, line)
		}
	}
	for _, name := range []string{"fn load", "spill"} {
		e, ok := effects[name]
		require.True(t, ok, "nova-redis has no verb %q", name)
		assert.NotContains(t, string(e), "files on this machine", "%s writes to the store over the network, yet says: %s", name, e)
		assert.True(t, strings.HasPrefix(string(e), string(tool.Delivery)), "%s writes to the store over the network; its effect %q is not the delivery class", name, e)
	}
}
