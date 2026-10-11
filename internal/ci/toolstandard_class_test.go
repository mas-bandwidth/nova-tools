package ci

import (
	"path"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A tool built on pkg/tool meets its banner's standard by construction
// except for what only its definition can say: every verb's effect (inspection,
// local write or delivery) and a how text of at most five lines of at most 100
// characters. tool.Problems checks both; this holds every package that builds a
// tool.Tool to a test of its own that calls it, so no `effect: unstated` and no
// long how text reaches a banner (docs/SPEC-CI.md#tool-standard).
func TestEveryToolDefinitionIsHeldToTheStandard(t *testing.T) {
	t.Parallel()

	defines, checks := map[string]bool{}, map[string]bool{}
	tree := repoTree(t)
	for _, f := range append(tree.GoFilesUnder(false, "cmd", "internal", "pkg"), tree.GoFilesUnder(true, "cmd", "internal", "pkg")...) {
		dir := path.Dir(f.Rel)
		if f.HasDirNamed("testdata") || dir == "pkg/tool" {
			continue
		}
		switch {
		case f.Test && strings.Contains(string(f.Src), ".Problems()"):
			checks[dir] = true
		case !f.Test && strings.Contains(string(f.Src), "tool.Tool{"):
			defines[dir] = true
		}
	}
	require.NotEmpty(t, defines, "no package builds a tool.Tool; this test is looking in the wrong place and would pass by checking nothing")
	var missing []string
	for dir := range defines {
		if !checks[dir] {
			missing = append(missing, dir)
		}
	}
	sort.Strings(missing)
	for _, dir := range missing {
		assert.Failf(t, "tool standard", "%s builds a tool.Tool and no test of it calls Problems(); remedy=\"add a test that fails on each of its Problems() (as cmd/nova-cairn TestCairnToolMeetsTheStandard)\"", dir)
	}
}
