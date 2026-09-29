package docs

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The release catalogue is an adoption surface: one row and a first command
// for each supported tool, with no retired tool advertised elsewhere on the page.
func TestReadmeCatalogueContainsOnlyLivingTools(t *testing.T) {
	t.Parallel()
	root := testRoot(t)
	raw, err := os.ReadFile(filepath.Join(root, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	page := string(raw)
	living := strings.Fields("nova-bus nova-table nova-redis nova-config nova-swarm nova-secrets nova-tokens nova-memory nova-cairn nova-check nova-self-talk nova-fuse nova-sandbox nova-ci nova-version nova-update")
	expected := map[string]bool{}
	for _, name := range living {
		expected[name] = true
	}
	rows := regexp.MustCompile(`(?s)<tr><td>.*?</tr>`).FindAllString(page, -1)
	names := regexp.MustCompile(`<td nowrap><a href="[^"]+">(nova-[a-z-]+)</a></td>`)
	seen := map[string]bool{}
	for _, row := range rows {
		m := names.FindStringSubmatch(row)
		if m == nil {
			t.Errorf("catalogue row has no tool link: %s", row)
			continue
		}
		name := m[1]
		if !expected[name] {
			t.Errorf("catalogue advertises unsupported tool %s", name)
		}
		if seen[name] {
			t.Errorf("duplicate catalogue row for %s", name)
		}
		seen[name] = true
		if !strings.Contains(row, "<code>"+name+" ") {
			t.Errorf("%s has no first command", name)
		}
	}
	for _, name := range living {
		if !seen[name] {
			t.Errorf("missing catalogue row for %s", name)
		}
	}
	if len(rows) != len(living) {
		t.Errorf("catalogue has %d rows, want %d", len(rows), len(living))
	}

	// Read directory names only; do not build or test archived code. Including
	// non-catalogue cmd entries also catches a tool awaiting its physical move.
	forbidden := map[string]bool{"nova-pulse": true}
	for _, dir := range []string{"cmd", "deprecated/cmd"} {
		entries, err := os.ReadDir(filepath.Join(root, dir))
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			name := entry.Name()
			if entry.IsDir() && strings.HasPrefix(name, "nova-") && (dir == "deprecated/cmd" || !expected[name]) {
				forbidden[name] = true
			}
		}
	}
	for name := range forbidden {
		if regexp.MustCompile(`\b` + regexp.QuoteMeta(name) + `\b`).MatchString(page) {
			t.Errorf("README advertises retired or unsupported tool %s", name)
		}
	}
	if regexp.MustCompile(`(?i)\b(deprecated|parked|formerly|previously)\b|development.branch|preparing for|prepares for`).MatchString(page) {
		t.Error("README must describe the supported product without retirement or development-history markers")
	}
}
