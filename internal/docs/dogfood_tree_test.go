package docs

import (
	"regexp"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/testkit/repotree"
	"github.com/stretchr/testify/require"
)

var (
	// Existing dogfood records use a bare score, an em dash, two hyphens, or a
	// colon; require the score token while leaving each report's prose format intact.
	dogfoodReadLine  = regexp.MustCompile(`(?m)^READ[ \t]+(?:[0-9]|10)/10(?:$|[ \t—:-])`)
	dogfoodUseLine   = regexp.MustCompile(`(?m)^USE[ \t]+(?:[0-9]|10)/10(?:$|[ \t—:-])`)
	dogfoodCountLine = regexp.MustCompile(`^urgent=[0-9]+ next=[0-9]+$`)
)

// TestDocsTreeIsConsistent keeps the current Codex dogfood family scannable and
// its verdict counts easy to collect. Other historical tool/friend reports use
// distinct contracts, so this newer structure is scoped to its own family.
func TestDocsTreeIsConsistent(t *testing.T) {
	t.Parallel()

	files := repotree.Files(t, repotree.Query{
		Under: []string{"docs/dogfood/2026-10-06/codex"},
		Ext:   ".md",
	})
	for _, f := range files {
		t.Run(f.Rel, func(t *testing.T) {
			t.Parallel()

			text := string(f.Src)
			require.Len(t, dogfoodReadLine.FindAllString(text, -1), 1, "%s needs one READ score", f.Rel)
			require.Len(t, dogfoodUseLine.FindAllString(text, -1), 1, "%s needs one USE score", f.Rel)
			lines := strings.Split(strings.TrimSpace(text), "\n")
			require.NotEmpty(t, lines)
			require.Regexp(t, dogfoodCountLine, lines[len(lines)-1], "%s must end with urgent/next counts", f.Rel)
		})
	}
}
