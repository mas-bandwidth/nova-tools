package docs

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// toplevel_links_test.go holds the top-level documents' relative links against
// the files they name: a link resolves relative to the file it stands in.
// docs/SPEC.md DESCRIBES this checker, so it quotes markdown grammar inside
// inline code spans that are not links; fences and code spans are skipped
// first, or the description would fail the check it describes. It reads the
// repo root and docs/ directly and never descends into a subdirectory.

// inlineCodeRe is one backtick, any run of non-backticks, one backtick — the
// inline code span stripped before a line is searched for links.
var inlineCodeRe = regexp.MustCompile("`[^`]*`")

// linkTargetRe is the narrow inline-link form: the destination of `](dest)`,
// with an optional title after whitespace.
var linkTargetRe = regexp.MustCompile(`\]\(([^)\s]+)(?:\s[^)]*)?\)`)

// TestEveryTopLevelDocLinkResolves reads every `*.md` at the repository root
// and every `*.md` directly in docs/, and checks that each relative link
// target names a file that exists.
func TestEveryTopLevelDocLinkResolves(t *testing.T) {
	t.Parallel()

	var files []string
	for _, pattern := range []string{"../../*.md", "../../docs/*.md"} {
		matches, err := filepath.Glob(pattern)
		require.NoError(t, err, "globbing %s: %v", pattern, err)
		files = append(files, matches...)
	}
	require.GreaterOrEqual(t, len(files), 10, "collected %d top-level documents, want at least ten; the scan is reading the wrong directory", len(files))

	for _, file := range files {
		data, err := os.ReadFile(file)
		if !assert.NoError(t, err, "%s: %v", file, err) {
			continue
		}
		inFence := false
		for i, line := range strings.Split(string(data), "\n") {
			lineNo := i + 1
			if strings.HasPrefix(strings.TrimSpace(line), "```") {
				inFence = !inFence
				continue
			}
			if inFence {
				continue
			}
			line = inlineCodeRe.ReplaceAllString(line, "")
			for _, m := range linkTargetRe.FindAllStringSubmatch(line, -1) {
				written := m[1]
				target := written
				if cut := strings.IndexAny(target, "#?"); cut >= 0 {
					target = target[:cut]
				}
				if target == "" || strings.HasPrefix(target, "http://") ||
					strings.HasPrefix(target, "https://") || strings.HasPrefix(target, "mailto:") {
					continue
				}
				resolved := filepath.Join(filepath.Dir(file), target)
				_, statErr := os.Stat(resolved)
				assert.NoError(t, statErr, "%s:%d: link target %q resolves to %s, which does not exist", file, lineNo, written, resolved)
			}
		}
	}
}
