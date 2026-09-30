package docs

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"regexp"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
)

var documentedToolName = regexp.MustCompile(`(?i)\bnova-[a-z][a-z0-9]*(?:-[a-z0-9]+)*\b`)

// Read directory names only: archived code is never built or tested. A tool
// returned to cmd/ is living again; its former archive location is not a ban.
// The deleted tool below has no directory left and is also pinned by the
// README catalogue test. Historical records outside active docs are not read.
func parkedDocumentationReferences(tree fs.FS) ([]string, error) {
	parked := map[string]bool{"nova-pulse": true}
	for _, dir := range []string{"deprecated/cmd", "cmd"} {
		entries, err := fs.ReadDir(tree, dir)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return nil, err
		}
		for _, entry := range entries {
			if !entry.IsDir() || !strings.HasPrefix(entry.Name(), "nova-") {
				continue
			}
			if dir == "cmd" {
				delete(parked, entry.Name())
			} else {
				parked[entry.Name()] = true
			}
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
	if err != nil {
		t.Fatal(err)
	}
	for _, finding := range findings {
		t.Error(finding)
	}
}

func TestParkedDocumentationReferences(t *testing.T) {
	t.Parallel()
	fixture := func() fstest.MapFS {
		return fstest.MapFS{
			"cmd/nova-active":                 {Mode: fs.ModeDir},
			"deprecated/cmd/nova-retired":     {Mode: fs.ModeDir},
			"README.md":                       {Data: []byte("nova-active help\n")},
			"docs/CLI.md":                     {Data: []byte("nova-active help\n")},
			"docs/guide/nested.md":            {Data: []byte("nova-active help\n")},
			"CHANGELOG.md":                    {Data: []byte("nova-retired historical record\n")},
			"deprecated/docs/old-contract.md": {Data: []byte("nova-retired historical contract\n")},
		}
	}
	cases := []struct {
		name, page, text string
		want             int
	}{
		{"living", "docs/CLI.md", "nova-active help", 0},
		{"readme", "README.md", "nova-retired help", 1},
		{"command", "docs/CLI.md", "`nova-retired run`", 1},
		{"notice", "docs/CLI.md", "Retired: nova-retired; use nova-active.", 1},
		{"capitalized", "docs/CLI.md", "Nova-Retired is parked.", 1},
		{"link", "docs/guide/nested.md", "[guide](../nova-retired.md)", 1},
		{"nested", "docs/guide/nested.md", "nova-retired help\nnova-retired run", 2},
		{"distinct-name", "docs/CLI.md", "nova-retired-helper help", 0},
		{"fully-deleted", "docs/CLI.md", "nova-pulse help", 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tree := fixture()
			tree[tc.page] = &fstest.MapFile{Data: []byte(tc.text)}
			findings, err := parkedDocumentationReferences(tree)
			if err != nil || len(findings) != tc.want {
				t.Fatalf("findings=%v err=%v; want %d", findings, err, tc.want)
			}
			for _, finding := range findings {
				if !strings.HasPrefix(finding, tc.page+":") {
					t.Errorf("finding lost page location: %s", finding)
				}
			}
		})
	}
	t.Run("returned-tool", func(t *testing.T) {
		t.Parallel()
		tree := fixture()
		tree["cmd/nova-retired"] = &fstest.MapFile{Mode: fs.ModeDir}
		tree["docs/CLI.md"] = &fstest.MapFile{Data: []byte("nova-retired help")}
		findings, err := parkedDocumentationReferences(tree)
		if err != nil || len(findings) != 0 {
			t.Fatalf("returned tool refused: %v / %v", findings, err)
		}
	})
}
