package docs

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

var (
	// Existing dogfood records use a bare score, an em dash, two hyphens, or a
	// colon; require the score token while leaving each report's prose format intact.
	dogfoodReadLine  = regexp.MustCompile(`(?m)^READ[ \t]+(?:[0-9]|10)/10(?:$|[ \t—:-])`)
	dogfoodUseLine   = regexp.MustCompile(`(?m)^USE[ \t]+(?:[0-9]|10)/10(?:$|[ \t—:-])`)
	dogfoodCountLine = regexp.MustCompile(`^urgent=[0-9]+ next=[0-9]+$`)
)

// TestDocsTreeIsConsistent keeps each dated dogfood record scannable and its
// verdict counts easy to collect, regardless of which tool or friend wrote it.
func TestDocsTreeIsConsistent(t *testing.T) {
	t.Parallel()

	root := filepath.Join("..", "..", "docs", "dogfood")
	var reports []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() && filepath.Ext(path) == ".md" {
			reports = append(reports, path)
		}
		return nil
	})
	require.NoError(t, err, "walk docs/dogfood")

	for _, path := range reports {
		path := path
		t.Run(filepath.ToSlash(path), func(t *testing.T) {
			t.Parallel()

			data, readErr := os.ReadFile(path)
			require.NoError(t, readErr, "%s", path)
			text := string(data)
			require.Len(t, dogfoodReadLine.FindAllString(text, -1), 1, "%s needs one READ score", path)
			require.Len(t, dogfoodUseLine.FindAllString(text, -1), 1, "%s needs one USE score", path)

			lines := strings.Split(strings.TrimSpace(text), "\n")
			require.NotEmpty(t, lines)
			require.Regexp(t, dogfoodCountLine, lines[len(lines)-1], "%s must end with urgent/next counts", path)
		})
	}
}
