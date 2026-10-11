package docs

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/redisacl"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// architecture_test.go holds docs/ARCHITECTURE.md, the one page that explains
// how nova-tools fits together, against the tree it describes. The page is a
// stranger's map: the concepts the specs share, every tool under cmd/ with its
// role, the stores and their ACL users, the machines, and a card's path from
// add to land. A map that omits a tool, cites a spec that is gone, points at a
// link that does not resolve, or forgets a store the tools keep is worse than
// no map, because it reads as complete.

// architecturePath is the page this guard reads, relative to the repo root.
const architecturePath = "docs/ARCHITECTURE.md"

// architectureLinkRe is the narrow inline-link form `](dest)`, with an optional
// title after whitespace: the shape the page writes every link in.
var architectureLinkRe = regexp.MustCompile(`\]\(([^)\s]+)(?:\s[^)]*)?\)`)

// architectureSpecRe matches a cited spec document: docs/SPEC.md and the
// numbered docs/SPEC-*.md files a claim points at.
var architectureSpecRe = regexp.MustCompile(`^SPEC[A-Z0-9-]*\.md$`)

// TestArchitecturePageNamesEveryToolAndItsStores reads docs/ARCHITECTURE.md and
// refuses a page that omits a tool directory under cmd/, omits a store or an
// ACL user the store renders, cites a spec that does not exist, or carries a
// relative link that does not resolve (file, and for a .md target an anchor).
func TestArchitecturePageNamesEveryToolAndItsStores(t *testing.T) {
	t.Parallel()

	root := testRoot(t)
	raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(architecturePath)))
	require.NoError(t, err, "%s is the page the concepts and the architecture live on", architecturePath)
	page := string(raw)

	// Every tool under cmd/ is named. The name is matched at word boundaries so
	// nova-up does not pass on nova-update's line.
	entries, err := os.ReadDir(filepath.Join(root, "cmd"))
	require.NoError(t, err)
	var tools []string
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), "nova-") {
			tools = append(tools, e.Name())
		}
	}
	require.NotEmpty(t, tools, "no tool directories under cmd/")
	sort.Strings(tools)
	for _, tool := range tools {
		assert.Regexp(t, `\b`+regexp.QuoteMeta(tool)+`\b`, page, "%s does not name the tool cmd/%s", architecturePath, tool)
	}

	// The stores the tools keep are named, and so is every ACL user this build
	// renders: the page is where their read and write sets are drawn.
	for _, store := range []string{"Redis", "Postgres", "secrets", "git"} {
		assert.Contains(t, strings.ToLower(page), strings.ToLower(store), "%s does not name the %s store", architecturePath, store)
	}
	for _, role := range redisacl.Roles() {
		assert.Contains(t, page, role.User, "%s does not name the %s ACL user", architecturePath, role.User)
	}

	// Every inline link resolves relative to the page, and every cited spec is
	// one of the spec files the tree holds. A fence is a diagram, not a link.
	inFence := false
	for i, line := range strings.Split(page, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		line = inlineCodeRe.ReplaceAllString(line, "")
		for _, m := range architectureLinkRe.FindAllStringSubmatch(line, -1) {
			written := m[1]
			target, anchor := written, ""
			if cut := strings.IndexByte(target, '#'); cut >= 0 {
				anchor = target[cut+1:]
				target = target[:cut]
			}
			if target == "" || strings.HasPrefix(target, "http://") ||
				strings.HasPrefix(target, "https://") || strings.HasPrefix(target, "mailto:") {
				continue
			}
			if architectureSpecRe.MatchString(target) {
				_, statErr := os.Stat(filepath.Join(root, "docs", target))
				assert.NoError(t, statErr, "%s:%d: cited spec %q does not exist", architecturePath, i+1, written)
			}
			resolved := filepath.Join(root, "docs", target)
			if _, statErr := os.Stat(resolved); !assert.NoError(t, statErr, "%s:%d: link %q does not resolve to %s", architecturePath, i+1, written, resolved) {
				continue
			}
			if anchor == "" || !strings.HasSuffix(resolved, ".md") {
				continue
			}
			data, readErr := os.ReadFile(resolved)
			if readErr != nil {
				continue
			}
			var anchors []string
			for _, l := range strings.Split(string(data), "\n") {
				for _, prefix := range []string{"## ", "### "} {
					if h, ok := strings.CutPrefix(l, prefix); ok {
						anchors = append(anchors, readmeAnchor(h))
					}
				}
			}
			assert.Contains(t, anchors, anchor, "%s:%d: anchor #%s does not resolve in %s", architecturePath, i+1, anchor, resolved)
		}
	}
}
