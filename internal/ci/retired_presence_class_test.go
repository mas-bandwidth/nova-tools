package ci

import (
	"fmt"
	"os"
	"path"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// THE CLASS RULE: THE OLD FRIEND PRESENCE DOES NOT COME BACK (v1.3.5).
//
// Glenn, 2026-10-14: "continue to remove the old stuff, so they don't come
// back, or get used when using the tool, or building new code". v1.3 has one
// truth for whether a friend is there: the nova-hook record and its
// per-minute challenge, which only the model answers (cmd/nova-hook,
// docs/SPEC-HOOK.md, tla/Hook.tla). Everything before it that did the same job
// (the coordinator's one-second keepalive loop, the coordinator's ping, the
// wake ping loop and its launchd agent, the pong wait, the hand beat, the
// daemon's session-check presence and its push proof) is deleted, and this
// rule refuses each of its names in the tree: a Go identifier, a nova-friend
// verb, or a command, flag or label in any text a person or an AI reads. The
// names and what replaced each are one table,
// internal/ci/testdata/retired-presence.txt. History (the changelog, the fix
// and release notes, dated dogfood and stranger records) is not scanned.

const retiredPresenceTable = "internal/ci/testdata/retired-presence.txt"

// retiredName is one row of the table.
type retiredName struct {
	Kind, Name, Replacement string
}

// retiredHistory are the files and directories that record what was, never
// what to do: they may name a retired thing.
var retiredHistory = []string{
	"CHANGELOG.md", "FIXES.md", "RESOLUTIONS.md", "docs/fixes.sexp",
	"docs/RELEASE-NOTES-", "docs/dogfood/", "docs/stranger/",
	"internal/ci/testdata/deleted-tests.txt",
	retiredPresenceTable, "internal/ci/retired_presence_class_test.go",
	"bin/",
}

// retiredTextExts are the files a text row is looked for in.
var retiredTextExts = map[string]bool{
	".go": true, ".md": true, ".sh": true, ".bash": true, ".zsh": true, ".sexp": true,
	".txt": true, ".tsv": true, ".yml": true, ".yaml": true, ".json": true,
	".plist": true, ".tla": true, ".cfg": true, ".tmpl": true,
}

// parseRetiredNames reads the table: `<kind> <name> <replacement>` rows,
// blank lines and # comments skipped. A row of another kind, or with no
// replacement, is a fault: every retirement names what took its place.
func parseRetiredNames(src string) ([]retiredName, []string) {
	var rows []retiredName
	var faults []string
	for i, line := range strings.Split(src, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Fields(line)
		if len(f) < 3 {
			faults = append(faults, fmt.Sprintf("line %d: want `<kind> <name> <what replaced it>`, got %q", i+1, line))
			continue
		}
		kind := f[0]
		name := f[1]
		rest := f[2:]
		if kind == "text" && len(f) >= 4 && strings.HasPrefix(f[1], "nova-") {
			// a command row: the tool and its verb are one name
			name, rest = f[1]+" "+f[2], f[3:]
		}
		switch kind {
		case "ident", "verb", "text":
		default:
			faults = append(faults, fmt.Sprintf("line %d: kind %q is not ident, verb or text", i+1, kind))
			continue
		}
		rows = append(rows, retiredName{Kind: kind, Name: name, Replacement: strings.Join(rest, " ")})
	}
	return rows, faults
}

// retiredMatcher is the regexp a row is found by.
func retiredMatcher(r retiredName) *regexp.Regexp {
	switch r.Kind {
	case "ident":
		return regexp.MustCompile(`\b` + regexp.QuoteMeta(r.Name) + `\b`)
	case "verb":
		return regexp.MustCompile(`\bName:\s*"` + regexp.QuoteMeta(r.Name) + `"`)
	}
	return regexp.MustCompile(regexp.QuoteMeta(r.Name) + `(?:[^A-Za-z0-9_]|$)`)
}

// retiredScanned says whether a row of kind is looked for in the file rel.
func retiredScanned(kind, rel string) bool {
	for _, h := range retiredHistory {
		if rel == h || (strings.HasSuffix(h, "/") || strings.HasSuffix(h, "-")) && strings.HasPrefix(rel, h) {
			return false
		}
	}
	switch kind {
	case "ident":
		return path.Ext(rel) == ".go"
	case "verb":
		return path.Ext(rel) == ".go" && strings.HasPrefix(rel, "cmd/nova-friend/")
	}
	return retiredTextExts[path.Ext(rel)]
}

// retiredFindings is every `<rel>:<line>: <kind> <name>` the rows find in src.
func retiredFindings(rel string, src []byte, rows []retiredName) []string {
	var out []string
	lines := strings.Split(string(src), "\n")
	for _, r := range rows {
		if !retiredScanned(r.Kind, rel) {
			continue
		}
		re := retiredMatcher(r)
		for i, l := range lines {
			if re.MatchString(l) {
				out = append(out, fmt.Sprintf("%s:%d: %s %q is retired; use %s", rel, i+1, r.Kind, r.Name, r.Replacement))
			}
		}
	}
	return out
}

// TestNoRetiredPresenceNameReturns sweeps the tree for every name the table
// retires. The remedy is the row's replacement, never a new row's absence:
// a name that must come back is a decision for the owner, and its row is
// deleted in the change that brings it back, with the reason.
func TestNoRetiredPresenceNameReturns(t *testing.T) {
	t.Parallel()
	tree := repoTree(t)
	raw, err := os.ReadFile(tree.Root + "/" + retiredPresenceTable)
	require.NoError(t, err)
	rows, faults := parseRetiredNames(string(raw))
	require.Empty(t, faults, "%s", retiredPresenceTable)
	require.NotEmpty(t, rows, "%s names nothing", retiredPresenceTable)
	var found []string
	for _, f := range tree.Files {
		if !retiredScanned("text", f.Rel) && !retiredScanned("ident", f.Rel) {
			continue
		}
		src := f.Src
		if src == nil {
			b, err := os.ReadFile(f.Path)
			require.NoError(t, err)
			src = b
		}
		found = append(found, retiredFindings(f.Rel, src, rows)...)
	}
	sort.Strings(found)
	assert.Empty(t, found, "the old friend presence is retired (v1.3.5; %s): liveness is the nova-hook record's alone. Use the replacement each line names.", retiredPresenceTable)
}

// TestRetiredPresenceRuleCatchesEachForm holds the rule on fakes: an
// identifier only as a whole word, a verb only as a nova-friend verb literal,
// a command not when it is the start of a longer word, and history never.
func TestRetiredPresenceRuleCatchesEachForm(t *testing.T) {
	t.Parallel()
	rows, faults := parseRetiredNames(`
# a comment
ident PingEvery nova-hook serve
verb ping nova-hook serve
text nova-friend ping nova-hook serve
text --to-friends nova-hook serve
`)
	require.Empty(t, faults)
	require.Len(t, rows, 4)
	assert.Equal(t, retiredName{Kind: "text", Name: "nova-friend ping", Replacement: "nova-hook serve"}, rows[2])

	goSrc := []byte("package x\nconst PingEvery = 1\nconst PingEveryOther = 2\nvar v = tool.Verb{Name: \"ping\"}\n")
	assert.Equal(t, []string{
		`cmd/nova-friend/x.go:2: ident "PingEvery" is retired; use nova-hook serve`,
		`cmd/nova-friend/x.go:4: verb "ping" is retired; use nova-hook serve`,
	}, retiredFindings("cmd/nova-friend/x.go", goSrc, rows))
	assert.Equal(t, []string{`cmd/nova-bus/x.go:2: ident "PingEvery" is retired; use nova-hook serve`},
		retiredFindings("cmd/nova-bus/x.go", goSrc, rows), "a verb literal is only nova-friend's")

	md := []byte("run nova-friend ping --as ada\nnova-friend ping-install too\nnova-friend pings nothing\nnova-friend pong stays\nping --wake --to-friends\n")
	assert.Equal(t, []string{
		`docs/X.md:1: text "nova-friend ping" is retired; use nova-hook serve`,
		`docs/X.md:2: text "nova-friend ping" is retired; use nova-hook serve`,
		`docs/X.md:5: text "--to-friends" is retired; use nova-hook serve`,
	}, retiredFindings("docs/X.md", md, rows))
	assert.Empty(t, retiredFindings("CHANGELOG.md", md, rows), "history is not scanned")
	assert.Empty(t, retiredFindings("docs/RELEASE-NOTES-1.1.0.md", md, rows), "history is not scanned")
	assert.Empty(t, retiredFindings("docs/dogfood/2026-10-06/x.md", md, rows), "history is not scanned")

	_, faults = parseRetiredNames("ident PingEvery\nother X y\n")
	assert.Len(t, faults, 2, "a row with no replacement, and a kind that is none of the three, are faults")
}
