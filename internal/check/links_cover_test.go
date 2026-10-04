package check

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLinksCoverChecksExactlyTheListedFiles pins the main path of LinksFiles:
// it checks exactly the listed markdown files and nothing else, so a two-file
// review does not expand to the whole tree; dir is still the resolution root
// (a root-relative target resolves against it), an absolute listed path is
// used as-is, a link that resolves into an excluded prefix is skipped, and
// every reported path stays repo-relative.
func TestLinksCoverChecksExactlyTheListedFiles(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		files       map[string]string
		listed      []string // "<abs>/..." entries are passed as absolute paths under dir
		exclude     []string
		wantMDFiles int
		wantChecked int
		wantBroken  []string // substrings of "file:target:reason"; empty = none
	}{
		{
			name: "two listed files, the unlisted broken one never scanned",
			files: map[string]string{
				"a.md": "[b](b.md)",
				"b.md": "x",
				"c.md": "[gone](missing.md)",
			},
			listed:      []string{"a.md", "b.md"},
			wantMDFiles: 2,
			wantChecked: 1,
		},
		{
			name: "root-relative target resolves against dir",
			files: map[string]string{
				"sub/a.md": "[k](/top.md)",
				"top.md":   "x",
			},
			listed:      []string{"sub/a.md"},
			wantMDFiles: 1,
			wantChecked: 1,
		},
		{
			name: "absolute listed path used as-is, finding stays repo-relative",
			files: map[string]string{
				"sub/a.md": "[gone](missing.md)",
			},
			listed:      []string{"<abs>/sub/a.md"},
			wantMDFiles: 1,
			wantChecked: 1,
			wantBroken:  []string{"sub/a.md", "missing.md", "does not exist"},
		},
		{
			name: "link into an excluded prefix is skipped",
			files: map[string]string{
				"a.md":                "[f](testdata/fixture.md) [real](real.md)",
				"real.md":             "x",
				"testdata/fixture.md": "[partial](missing.md)",
			},
			listed:      []string{"a.md"},
			exclude:     []string{"testdata"},
			wantMDFiles: 1,
			wantChecked: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			writeTree(t, dir, tt.files)
			listed := make([]string, 0, len(tt.listed))
			for _, f := range tt.listed {
				if rest, ok := strings.CutPrefix(f, "<abs>/"); ok {
					f = filepath.Join(dir, filepath.FromSlash(rest))
				}
				listed = append(listed, f)
			}
			res, err := LinksFiles(dir, listed, tt.exclude)
			require.NoError(t, err, "unexpected error: %v", err)
			assert.Equal(t, tt.wantMDFiles, res.MDFiles, "MDFiles = %d, want %d", res.MDFiles, tt.wantMDFiles)
			assert.Equal(t, tt.wantChecked, res.Checked, "Checked = %d, want %d", res.Checked, tt.wantChecked)
			var asFailures []Failure
			for _, b := range res.Broken {
				asFailures = append(asFailures, Failure{b.File + ":" + b.Target, b.Reason})
			}
			wantFailures(t, asFailures, tt.wantBroken)
		})
	}
}

// TestLinksCoverRefusesNonMarkdownFile pins LinksFiles' one refusal: a listed
// file that is not markdown is refused with the name it was listed under,
// never guessed at or silently skipped as the walk does.
func TestLinksCoverRefusesNonMarkdownFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeTree(t, dir, map[string]string{"notes.txt": "[fake](missing.md)"})
	res, err := LinksFiles(dir, []string{"notes.txt"}, nil)
	require.Error(t, err, "a non-markdown listed file is a refusal, not a scan")
	assert.Contains(t, err.Error(), "notes.txt", "the refusal names the file as listed: %s", err)
	assert.Contains(t, err.Error(), "not a markdown file", "the refusal says what was wrong: %s", err)
	assert.Zero(t, res.MDFiles, "the refused file is not counted: MDFiles = %d", res.MDFiles)
}
