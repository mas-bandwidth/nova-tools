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

// The release catalogue is an adoption surface: one row and a first command
// for each supported tool, with no retired tool advertised elsewhere on the page.
func TestReadmeCatalogueContainsOnlyLivingTools(t *testing.T) {
	t.Parallel()
	root := testRoot(t)
	raw, err := os.ReadFile(filepath.Join(root, "README.md"))
	require.NoError(t, err)
	page := string(raw)
	living := strings.Fields("nova-bus nova-table nova-sprint nova-redis nova-config nova-swarm nova-secrets nova-tokens nova-memory nova-cairn nova-check nova-dev nova-self-talk nova-fuse nova-sandbox nova-ci nova-version nova-update")
	expected := map[string]bool{}
	for _, name := range living {
		expected[name] = true
	}
	rows := regexp.MustCompile(`(?s)<tr><td>.*?</tr>`).FindAllString(page, -1)
	names := regexp.MustCompile(`<td nowrap><a href="[^"]+">(nova-[a-z-]+)</a></td>`)
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
	}
	for _, name := range living {
		assert.True(t, seen[name], "missing catalogue row for %s", name)
	}
	assert.Len(t, rows, len(living), "catalogue has %d rows, want %d", len(rows), len(living))

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
