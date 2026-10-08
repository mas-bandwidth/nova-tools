package docs

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/goenv"
	"github.com/mas-bandwidth/nova-tools/internal/testbin"
)

// clidocBegin matches one `<!-- clidoc:begin <tool> -->` marker and captures
// the tool it names, the same shape tools/clidoc rewrites.
var clidocBegin = regexp.MustCompile(`<!-- clidoc:begin (nova-[a-z0-9-]+) -->`)

// markerTools returns the tools docs/CLI.md's begin markers name, each once,
// in file order: exactly the tools clidoc regenerates.
func markerTools(doc string) []string {
	var tools []string
	seen := map[string]bool{}
	for _, m := range clidocBegin.FindAllStringSubmatch(doc, -1) {
		if !seen[m[1]] {
			seen[m[1]] = true
			tools = append(tools, m[1])
		}
	}
	return tools
}

// TestCLIReferenceIsGeneratedFromHelp holds docs/CLI.md's clidoc blocks to the
// built tools: it builds every tool a marker names plus tools/clidoc itself,
// runs the generator over the file, and requires the file on disk to be what
// clidoc writes — a reference block that drifts from the binaries' help is red,
// and the failure names the command that regenerates it.
func TestCLIReferenceIsGeneratedFromHelp(t *testing.T) {
	t.Parallel()

	root := testRoot(t)
	raw, err := os.ReadFile(filepath.Join(root, "docs", "CLI.md"))
	require.NoError(t, err, "reading docs/CLI.md: %v", err)
	tools := markerTools(string(raw))
	require.NotEmpty(t, tools,
		"docs/CLI.md carries no <!-- clidoc:begin <tool> --> marker; run `make clidoc` to generate the reference blocks")
	cmds, err := os.ReadDir(filepath.Join(root, "cmd"))
	require.NoError(t, err)
	for _, cmd := range cmds {
		if !cmd.IsDir() || !strings.HasPrefix(cmd.Name(), "nova-") {
			continue
		}
		require.Contains(t, tools, cmd.Name(), "docs/CLI.md needs a clidoc marker for every nova tool; run `make clidoc` after adding %s", cmd.Name())
	}

	stage := filepath.Join(t.TempDir(), "stage")
	require.NoError(t, os.MkdirAll(stage, 0o755))
	bin := t.TempDir()
	for _, tool := range append(tools[:len(tools):len(tools)], "clidoc") {
		pkg := "./cmd/" + tool
		if tool == "clidoc" {
			pkg = "./tools/clidoc"
		}
		out := filepath.Join(stage, tool)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		build := exec.CommandContext(ctx, "go", "build", "-o", out, pkg)
		build.Dir = root
		build.Env = goenv.Clean(os.Environ())
		built, err := build.CombinedOutput()
		cancel()
		require.NoError(t, err, "go build %s: %s", pkg, built)
		require.NoError(t, testbin.Place(out, filepath.Join(bin, tool)))
	}

	generated := filepath.Join(t.TempDir(), "CLI.md")
	run := exec.Command(filepath.Join(bin, "clidoc"), "--bin", bin, "--out", generated)
	run.Dir = root
	run.Env = goenv.Clean(os.Environ())
	out, err := run.CombinedOutput()
	require.NoError(t, err, "tools/clidoc: %s", out)

	want, err := os.ReadFile(generated)
	require.NoError(t, err, "reading clidoc's output: %v", err)
	require.Equal(t, string(want), string(raw),
		"docs/CLI.md differs from what tools/clidoc generates from the built tools' help; run `make clidoc` to regenerate it")
}

// TestMarkerToolsHoldsTheScan pins the scan the generated check builds from: a
// marker names its tool once however often it stands, and text that is no
// marker names nothing.
func TestMarkerToolsHoldsTheScan(t *testing.T) {
	t.Parallel()

	require.Equal(t, []string{"nova-check", "nova-fuse"},
		markerTools("<!-- clidoc:begin nova-check -->\n<!-- clidoc:begin nova-fuse -->\n<!-- clidoc:begin nova-check -->"))
	require.Empty(t, markerTools("no markers here\n<!-- clidoc:begin -->\n<!-- clidoc:end nova-check -->"))
}

// generatedVerbEntry returns the complete generated -h text for one verb,
// including continuation lines and the flags table.
func generatedVerbEntry(t *testing.T, doc, tool, verb string) string {
	t.Helper()
	section := strings.SplitN(doc, "<!-- clidoc:end "+tool+" -->", 2)
	require.Len(t, section, 2, "docs/CLI.md: missing generated %s section", tool)
	entry := strings.SplitN(section[0], "`"+tool+" "+verb+" -h`:\n\n```\n", 2)
	require.Len(t, entry, 2, "docs/CLI.md: missing generated %s %s help entry", tool, verb)
	body := strings.SplitN(entry[1], "\n```", 2)
	require.Len(t, body, 2, "docs/CLI.md: unclosed generated %s %s help entry", tool, verb)
	return body[0]
}
