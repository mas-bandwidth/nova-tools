package docs

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// livingTools are the tools the release ships for production use.
var livingTools = strings.Fields("nova-bus nova-friend nova-runner nova-table nova-sprint nova-redis nova-config nova-swarm nova-secrets nova-tokens nova-memory nova-decide nova-cairn nova-check nova-self-talk nova-fuse nova-sandbox nova-ci nova-version nova-update nova-local nova-up nova-doctor")

// preAlphaTools are the tools the README may list before they are ready for
// production use. Each says so in one sentence (stageSentence), the same words
// in its README row, in its banner and as the first line of its docs/CLI.md
// section, so a visitor meets the mark wherever they meet the tool. A tool
// leaves this list when it is ready, and the sentence leaves all three places
// with it.
var preAlphaTools = []string{"nova-work", "nova-card"}

// stageSentence is the pre-alpha mark of one tool.
func stageSentence(tool string) string { return tool + " is pre-alpha: not ready for production use." }

// The release catalogue is an adoption surface: one row and a first command
// for each tool that exists and ships, with no retired tool advertised
// elsewhere on the page. A tool not yet ready for production use may be listed
// only when it is on preAlphaTools and its row says so.
func TestReadmeCatalogueContainsOnlyLivingTools(t *testing.T) {
	t.Parallel()
	root := testRoot(t)
	raw, err := os.ReadFile(filepath.Join(root, "README.md"))
	require.NoError(t, err)
	page := string(raw)
	expected := map[string]bool{}
	for _, name := range append(slices.Clone(livingTools), preAlphaTools...) {
		expected[name] = true
	}
	rows := regexp.MustCompile(`(?s)<tr><td>.*?</tr>`).FindAllString(page, -1)
	names := regexp.MustCompile(`<td nowrap><a href="[^"]+">(nova-[a-z0-9-]+)</a></td>`)
	seen := map[string]bool{}
	for _, row := range rows {
		m := names.FindStringSubmatch(row)
		if !assert.NotNil(t, m, "catalogue row has no tool link: %s", row) {
			continue
		}
		name := m[1]
		assert.True(t, expected[name], "catalogue advertises unsupported tool %s", name)
		assert.False(t, seen[name], "duplicate catalogue row for %s", name)
		seen[name] = true
		assert.Contains(t, row, "<code>"+name+" ", "%s has no first command", name)
		if slices.Contains(preAlphaTools, name) {
			assert.Contains(t, row, stageSentence(name), "%s is pre-alpha and its README row does not say so", name)
		} else {
			assert.NotContains(t, row, "pre-alpha", "%s's README row says pre-alpha and it is not on preAlphaTools", name)
		}
	}
	for name := range expected {
		assert.True(t, seen[name], "missing catalogue row for %s", name)
	}
	assert.Len(t, rows, len(expected), "catalogue has %d rows, want %d", len(rows), len(expected))

	// Read directory names only. Including non-catalogue cmd entries also
	// catches a tool that is not in the release.
	forbidden := map[string]bool{"nova-pulse": true}
	entries, err := os.ReadDir(filepath.Join(root, "cmd"))
	require.NoError(t, err)
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() && strings.HasPrefix(name, "nova-") && !expected[name] {
			forbidden[name] = true
		}
	}
	for name := range forbidden {
		assert.False(t, regexp.MustCompile(`\b`+regexp.QuoteMeta(name)+`\b`).MatchString(page), "README advertises retired or unsupported tool %s", name)
	}
	assert.False(t, regexp.MustCompile(`(?i)\b(deprecated|parked|formerly|previously)\b|development.branch|preparing for|prepares for`).MatchString(page), "README must describe the supported product without retirement or development-history markers")
}

// TestAPreAlphaToolSaysSoEverywhere: every tool on preAlphaTools carries its
// stage sentence in all three places a visitor meets it (the README row, held
// above; the banner, whose source in cmd/<tool> holds the sentence as one
// string literal, the tool's own test proving it prints; and the first line
// of its docs/CLI.md section), and no tool off the list claims the mark in
// the README, the reference or any command's source.
func TestAPreAlphaToolSaysSoEverywhere(t *testing.T) {
	t.Parallel()
	root := testRoot(t)
	read := func(rel string) string {
		raw, err := os.ReadFile(filepath.Join(root, rel))
		require.NoError(t, err)
		return string(raw)
	}
	readme, cli := read("README.md"), read(filepath.Join("docs", "CLI.md"))
	sources := map[string]string{} // cmd/<tool>: its non-test Go source
	entries, err := os.ReadDir(filepath.Join(root, "cmd"))
	require.NoError(t, err)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		files, err := filepath.Glob(filepath.Join(root, "cmd", e.Name(), "*.go"))
		require.NoError(t, err)
		var b strings.Builder
		for _, f := range files {
			if !strings.HasSuffix(f, "_test.go") {
				b.WriteString(read(strings.TrimPrefix(f, root+string(filepath.Separator))))
			}
		}
		sources[e.Name()] = b.String()
	}

	for _, tool := range preAlphaTools {
		sentence := stageSentence(tool)
		assert.Contains(t, readme, sentence, "%s is pre-alpha and README.md does not say so", tool)
		assert.Contains(t, sources[tool], `"`+sentence+`"`, "%s is pre-alpha and its banner (cmd/%s) does not carry the sentence", tool, tool)
		_, section, found := strings.Cut(cli, "\n## "+tool+"\n")
		if assert.True(t, found, "docs/CLI.md has no ## %s section", tool) {
			first, _, _ := strings.Cut(strings.TrimLeft(section, "\n"), "\n")
			assert.Equal(t, sentence, first, "the first line of docs/CLI.md's ## %s section is not its pre-alpha sentence", tool)
		}
	}

	claim := regexp.MustCompile(`(nova-[a-z-]+) is pre-alpha`)
	places := map[string]string{"README.md": readme, "docs/CLI.md": cli}
	for tool, src := range sources {
		places["cmd/"+tool] = src
	}
	for where, text := range places {
		for _, m := range claim.FindAllStringSubmatch(text, -1) {
			assert.Contains(t, preAlphaTools, m[1], "%s says %s is pre-alpha and it is not on preAlphaTools", where, m[1])
		}
	}
}
