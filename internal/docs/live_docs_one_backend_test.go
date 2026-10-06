package docs

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/goenv"
	"github.com/mas-bandwidth/nova-tools/internal/tool"
)

// storeClients are the client packages that mean a binary opens a store.
var storeClients = map[string]string{
	"github.com/redis/go-redis/v9": tool.Redis,
	"github.com/jackc/pgx/v5":      tool.PostgreSQL,
}

// storeWords are the words a doc uses for each store.
var storeWords = map[string]*regexp.Regexp{
	tool.Redis:      regexp.MustCompile(`\bRedis\b`),
	tool.PostgreSQL: regexp.MustCompile(`\bPostgres(QL)?\b`),
}

// retiredBackendClaims are sentences a live doc may not carry: the bus is
// Redis streams and nothing else (SPEC-BUS.md), never messages over git, and
// the fleet's store holds the bus's only copy (SPEC-REDIS.md), so git is not
// its record.
var retiredBackendClaims = []*regexp.Regexp{
	regexp.MustCompile(`(?i)messages over git`),
	regexp.MustCompile(`(?i)nova-bus[^.\n]*\bover git\b`),
	regexp.MustCompile(`(?i)git stays the record`),
	regexp.MustCompile(`(?i)redis[^.\n]*never the record`),
}

// releasePin is a published version a doc tells a stranger to install.
var releasePin = regexp.MustCompile(`(?:releases/tag/|@|--branch )(v\d+\.\d+\.\d+)\b|\b(v\d+\.\d+\.\d+)\b`)

// installDocs are the pages a stranger follows from the README to a running
// binary: they name one published version and no other.
var installDocs = []string{"README.md", "docs/USAGE.md", "docs/SETUP.md"}

// liveDocs is every page a reader takes as the present: the root and the
// generated maps, and docs/*.md but the release notes, which record what a
// release was.
func liveDocs(t *testing.T, root string) []string {
	t.Helper()
	pages := []string{"README.md", "AGENTS.md", "cmd/AGENTS.md", "internal/AGENTS.md"}
	matches, err := filepath.Glob(filepath.Join(root, "docs", "*.md"))
	require.NoError(t, err)
	for _, m := range matches {
		if base := filepath.Base(m); !strings.HasPrefix(base, "RELEASE-NOTES-") {
			pages = append(pages, "docs/"+base)
		}
	}
	return pages
}

// TestLiveDocsNameOneBackendPerFact holds every place a tool's backend is
// stated to one declaration, tool.Needs: the stores it names are the client
// packages the binary links, the catalog's row and the README's row name no
// other store, no live doc carries a retired backend claim, and the install
// pages pin one published version.
func TestLiveDocsNameOneBackendPerFact(t *testing.T) {
	t.Parallel()
	root := testRoot(t)

	// The declaration is what the binaries link: one go list over every
	// command, each line a command and its transitive imports.
	list := exec.Command("go", "list", "-f", `{{.ImportPath}} {{join .Deps " "}}`, "./cmd/...")
	list.Env = goenv.Clean(os.Environ())
	list.Dir = root
	out, err := list.Output()
	require.NoErrorf(t, err, "go list ./cmd/...: %v", err)
	linked := map[string][]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		fields := strings.Fields(line)
		name := filepath.Base(fields[0])
		for client, store := range storeClients {
			if slices.Contains(fields[1:], client) {
				linked[name] = append(linked[name], store)
			}
		}
	}
	for _, name := range toolNames(t, root) {
		need, _ := tool.NeedOf(name, "")
		assert.ElementsMatchf(t, linked[name], need.Stores, "%s links a client for %v and tool.Needs declares %v; the declaration is what the binary links (internal/tool/needs.go)", name, linked[name], need.Stores)
	}

	// The catalog and the README name no store a tool does not declare.
	readme, err := os.ReadFile(filepath.Join(root, "README.md"))
	require.NoError(t, err)
	for _, name := range toolNames(t, root) {
		need, _ := tool.NeedOf(name, "")
		entry := DefaultCatalog[slices.IndexFunc(DefaultCatalog, func(e Entry) bool { return e.Path == "cmd/"+name })]
		row := readmeRow(string(readme), name)
		for store, word := range storeWords {
			if slices.Contains(need.Stores, store) {
				continue
			}
			assert.Falsef(t, word.MatchString(entry.Purpose), "the catalog's %s row names %s, which tool.Needs does not declare: %q", name, store, entry.Purpose)
			assert.Falsef(t, word.MatchString(row), "the README's %s row names %s, which tool.Needs does not declare", name, store)
		}
	}

	// No live doc carries a retired backend claim.
	for _, page := range liveDocs(t, root) {
		raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(page)))
		require.NoError(t, err)
		for i, line := range strings.Split(string(raw), "\n") {
			for _, claim := range retiredBackendClaims {
				assert.Falsef(t, claim.MatchString(line), "%s:%d carries a retired backend claim (%s): %q", page, i+1, claim, line)
			}
		}
	}

	// The install pages pin one published version.
	pins := map[string][]string{}
	for _, page := range installDocs {
		raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(page)))
		require.NoError(t, err)
		for i, line := range strings.Split(string(raw), "\n") {
			for _, m := range releasePin.FindAllStringSubmatch(line, -1) {
				v := m[1] + m[2]
				pins[v] = append(pins[v], page+":"+strconv.Itoa(i+1))
			}
		}
	}
	assert.Lenf(t, pins, 1, "the install pages name %d versions, want the one published version: %v", len(pins), pins)
}

// toolNames are the commands under cmd/.
func toolNames(t *testing.T, root string) []string {
	t.Helper()
	dirs, err := os.ReadDir(filepath.Join(root, "cmd"))
	require.NoError(t, err)
	var names []string
	for _, d := range dirs {
		if d.IsDir() && strings.HasPrefix(d.Name(), "nova-") {
			names = append(names, d.Name())
		}
	}
	require.NotEmpty(t, names)
	return names
}

// readmeRow is the README table row whose tool cell names tool; "" if none.
func readmeRow(page, name string) string {
	for _, row := range strings.Split(page, "<tr>") {
		if row, _, _ = strings.Cut(row, "</tr>"); strings.Contains(row, ">"+name+"</a></td>") {
			return row
		}
	}
	return ""
}
