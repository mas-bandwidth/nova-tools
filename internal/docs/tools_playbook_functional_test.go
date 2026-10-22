//go:build functional

package docs

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/goenv"
	"github.com/mas-bandwidth/nova-tools/pkg/testbin"
)

// tools_playbook_functional_test.go is the playbook check over the real tools:
// it builds every tool the page names and runs each one's help, a real build
// and real processes, so it is a functional test (docs/TESTING.md) and runs in
// the merge queue and the nightly. tools_playbook_test.go holds the checker
// itself to canned help in the unit tier.

// toolsPlaybookPath is the page, relative to the repository root.
const toolsPlaybookPath = "docs/TOOLS-PLAYBOOK.md"

// TestToolsPlaybookNamesOnlyRealVerbsAndUsageLines builds the tools the page
// names and holds the page to their help. The witnesses run the same check on
// lines that must be refused, so a check that passes everything fails here.
func TestToolsPlaybookNamesOnlyRealVerbsAndUsageLines(t *testing.T) {
	t.Parallel()

	root := testRoot(t)
	raw, err := os.ReadFile(filepath.Join(root, toolsPlaybookPath))
	require.NoError(t, err, "reading %s: %v", toolsPlaybookPath, err)
	page := string(raw)
	require.Contains(t, page, "## Before you do anything by hand", "the playbook opens with the rule")

	tools := map[string]bool{"nova-friend": true, "nova-doctor": true} // the witnesses' tools
	for _, s := range playbookSpanRe.FindAllStringSubmatch(page, -1) {
		if w := strings.Fields(s[1]); len(w) > 0 && strings.HasPrefix(w[0], "nova-") && !slices.Contains(builtElsewhere, w[0]) {
			tools[w[0]] = true
		}
	}
	stage := filepath.Join(t.TempDir(), "stage")
	require.NoError(t, os.MkdirAll(stage, 0o755))
	bin := t.TempDir()
	// One go build for every tool: the compiles and links run in parallel and the
	// toolchain starts once. Built one at a time, the eleven links took 8.6 s on a
	// 16-core bench against 1.8 s together, and this was the slowest test of a
	// two-core hosted shard.
	pkgs := make([]string, 0, len(tools))
	for tool := range tools {
		_, err := os.Stat(filepath.Join(root, "cmd", tool))
		require.NoError(t, err, "%s names %s, which is no ./cmd/%s here; add it to builtElsewhere if another repository builds it", toolsPlaybookPath, tool, tool)
		pkgs = append(pkgs, "./cmd/"+tool)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	build := exec.CommandContext(ctx, "go", append([]string{"build", "-o", stage + string(filepath.Separator)}, pkgs...)...)
	build.Dir = root
	build.Env = goenv.Clean(os.Environ())
	built, err := build.CombinedOutput()
	cancel()
	require.NoError(t, err, "go build %s: %s", strings.Join(pkgs, " "), built)
	for tool := range tools {
		require.NoError(t, testbin.Place(filepath.Join(stage, tool), filepath.Join(bin, tool)))
	}
	h := &playbookHelp{bin: bin, home: t.TempDir(), cache: map[string]helpRun{}}

	for _, p := range checkToolsPlaybook(t, h, page) {
		t.Errorf("%s %s", toolsPlaybookPath, p)
	}

	good := "usage: `nova-friend lanes --as <me> [--clear-faults] [--dir <d>] [--state-dir <d>]`\n" +
		"run `nova-friend lanes --as <me> --clear-faults` and `nova-doctor run --check harness`\n"
	assert.Empty(t, checkToolsPlaybook(t, h, good), "real usage lines and verbs are refused")
	for text, want := range map[string]string{
		"usage: `nova-friend lanes --fly`":                              "usage line not in nova-friend's help",
		"usage: `nova-doctor version`\n`nova-friend fly --as <me>`":     "nova-friend has no verb fly",
		"usage: `nova-doctor version`\n`nova-friend lanes --fly`":       "nova-friend lanes has no flag --fly",
		"no usage line at all, only `nova-sprint where` from elsewhere": "quotes no usage line of a tool built here",
	} {
		ps := checkToolsPlaybook(t, h, text)
		assert.NotEmpty(t, ps, "the text is not refused: %s", text)
		assert.Contains(t, strings.Join(ps, "\n"), want, "the text is refused for another reason: %s", text)
	}
}
