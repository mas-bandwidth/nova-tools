package docs

import (
	"fmt"
	"io/fs"
	"os"
	"path"
	"regexp"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var documentedToolName = regexp.MustCompile(`(?i)\bnova-[a-z][a-z0-9]*(?:-[a-z0-9]+)*\b`)

// Read directory names only. A tool returned to cmd/ is living again; its
// former retirement is not a ban. The deleted tool below has no directory left
// and is also pinned by the README catalogue test. Historical records outside
// active docs are not read.
func parkedDocumentationReferences(tree fs.FS) ([]string, error) {
	parked := map[string]bool{"nova-pulse": true}
	entries, err := fs.ReadDir(tree, "cmd")
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), "nova-") {
			delete(parked, entry.Name())
		}
	}

	pages := []string{"README.md"}
	if err := fs.WalkDir(tree, "docs", func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && strings.EqualFold(path.Ext(name), ".md") {
			pages = append(pages, name)
		}
		return nil
	}); err != nil {
		return nil, err
	}

	var findings []string
	for _, page := range pages {
		body, err := fs.ReadFile(tree, page)
		if err != nil {
			return nil, err
		}
		for index, line := range strings.Split(string(body), "\n") {
			for _, name := range documentedToolName.FindAllString(line, -1) {
				if parked[strings.ToLower(name)] {
					findings = append(findings, fmt.Sprintf("%s:%d: parked tool %s; remove its documentation, including examples and retirement notices", page, index+1, name))
				}
			}
		}
	}
	slices.Sort(findings)
	return findings, nil
}

func TestActiveDocumentationOmitsParkedTools(t *testing.T) {
	t.Parallel()
	findings, err := parkedDocumentationReferences(os.DirFS(testRoot(t)))
	require.NoError(t, err)
	for _, finding := range findings {
		t.Error(finding)
	}
}

func TestParkedDocumentationReferences(t *testing.T) {
	t.Parallel()
	fixture := func() fstest.MapFS {
		return fstest.MapFS{
			"cmd/nova-active":       {Mode: fs.ModeDir},
			"README.md":             {Data: []byte("nova-active help\n")},
			"docs/CLI.md":           {Data: []byte("nova-active help\n")},
			"docs/guide/nested.md":  {Data: []byte("nova-active help\n")},
			"CHANGELOG.md":          {Data: []byte("nova-pulse historical record\n")},
			"notes/old-contract.md": {Data: []byte("nova-pulse historical contract\n")},
		}
	}
	cases := []struct {
		name, page, text string
		want             int
	}{
		{"living", "docs/CLI.md", "nova-active help", 0},
		{"readme", "README.md", "nova-pulse help", 1},
		{"command", "docs/CLI.md", "`nova-pulse run`", 1},
		{"notice", "docs/CLI.md", "Retired: nova-pulse; use nova-active.", 1},
		{"capitalized", "docs/CLI.md", "Nova-Pulse is parked.", 1},
		{"link", "docs/guide/nested.md", "[guide](../nova-pulse.md)", 1},
		{"nested", "docs/guide/nested.md", "nova-pulse help\nnova-pulse run", 2},
		{"distinct-name", "docs/CLI.md", "nova-pulse-helper help", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tree := fixture()
			tree[tc.page] = &fstest.MapFile{Data: []byte(tc.text)}
			findings, err := parkedDocumentationReferences(tree)
			require.NoError(t, err)
			require.Len(t, findings, tc.want, "findings=%v err=%v; want %d", findings, err, tc.want)
			for _, finding := range findings {
				assert.True(t, strings.HasPrefix(finding, tc.page+":"), "finding lost page location: %s", finding)
			}
		})
	}
	t.Run("returned-tool", func(t *testing.T) {
		t.Parallel()
		tree := fixture()
		tree["cmd/nova-pulse"] = &fstest.MapFile{Mode: fs.ModeDir}
		tree["docs/CLI.md"] = &fstest.MapFile{Data: []byte("nova-pulse help")}
		findings, err := parkedDocumentationReferences(tree)
		require.NoError(t, err)
		require.Empty(t, findings, "returned tool refused: %v / %v", findings, err)
	})
}
