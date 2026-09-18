package ci

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
	"unicode"
	"unicode/utf8"
)

// namedPathsAllowlistPath is the shrink-only list of the names that LOOK like a path in
// this repository and are not one: the invented paths a specification uses as examples
// (`cmd/nova-foo`), and the retired paths a comment names on purpose to say what a thing
// replaced. Every entry carries its reason; every entry is checked in BOTH directions -- a
// name not listed is a red run, and a listed name that no file names any more is a stale
// entry and also a red run -- so the list can only ever get shorter. It lives in testdata
// so a reader sees the whole exception set without reading the test.
const namedPathsAllowlistPath = "testdata/namedpaths_allowlist.txt"

// namedPathRootDirs are the top-level directories of THIS repository. A token that starts
// with one of them and carries a slash is a name a friend will follow, so it must exist.
// Anything else is somebody else's tree -- an import path, a URL, a path on a bench -- and
// this rule says nothing about it.
var namedPathRootDirs = []string{"cmd", "internal", "docs", "tools", "scripts", "testdata", "fleet", "infra", ".github"}

// namedPathRe finds the candidates. The alternation is namedPathRootDirs, and the tail is
// the character set a path of ours is written in; a glob or a template breaks out of that
// set at its first metacharacter, which is how namedPathIsTemplate sees one.
var namedPathRe = regexp.MustCompile(`(?:\.github|cmd|internal|docs|tools|scripts|testdata|fleet|infra)/[\w./-]+`)

// namedPathMetaRunes are the characters that mean the token is a PATTERN rather than a
// path: a shell or doublestar glob, a Go or shell template, a printf verb, an ellipsis.
// One of them on either side of a candidate takes the candidate out of the rule.
const namedPathMetaRunes = "*?{}<>$%[|…"

// TestEveryNamedRepoPathExists is the class rule behind Glenn's law that a prompt gives
// positive instructions with exact paths, and behind the learning loop: the text a friend
// reads at startup IS the mechanism, so a path in that text that does not exist is not a
// typo, it is a dead end that costs a friend a search.
//
// The hurt: `cmd/nova-pulse/fleet_verbs.go:6` and `internal/pulse/fleetstandard.go:6` both
// named `scripts/bench-standard.sh`. The file is `tools/bench-standard.sh` and has been
// since it moved; a friend following either comment finds nothing, and neither the
// compiler nor any test had an opinion, because a path inside a comment or a string is
// just text to Go.
//
// The rule closes the shape rather than the two comments: every token in this
// repository's Go source (strings AND comments) and in its docs that starts with one of
// this repository's own top-level directories and carries a slash must name a file or a
// directory that is in the tree.
func TestEveryNamedRepoPathExists(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	allow := readNamedPathAllowlist(t)
	files := namedPathSources(t, root)
	if len(files) == 0 {
		t.Fatal("no Go source or docs to read; this test is looking in the wrong place")
	}

	// earning is every allowlisted name this run found MISSING, so the stale half below
	// can tell a narrowing that is still doing work from one that is not. A listed name
	// that is in the tree now is in realNow instead, and is the good kind of red: the
	// planned file was written, so the row goes.
	earning := map[string]bool{}
	realNow := map[string]bool{}
	// sites is the first place each missing name is written, so the failure points at the
	// line to fix rather than at the name alone.
	sites := map[string]string{}
	var missing []string

	for _, rel := range files {
		raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		for line, text := range strings.Split(string(raw), "\n") {
			for _, name := range namedPathsIn(text) {
				if namedPathExists(root, name) {
					if allow[name] {
						realNow[name] = true
					}
					continue
				}
				if allow[name] {
					earning[name] = true
					continue
				}
				if _, already := sites[name]; already {
					continue
				}
				sites[name] = fmt.Sprintf("%s:%d", rel, line+1)
				missing = append(missing, name)
			}
		}
	}

	sort.Strings(missing)
	for _, name := range missing {
		t.Errorf("%s: %s names no file or directory in this tree; a friend following it finds nothing -- correct the path, or add it to %s with the reason it is not a real path",
			sites[name], name, namedPathsAllowlistPath)
	}

	var stale []string
	for name := range allow {
		if !earning[name] {
			stale = append(stale, name)
		}
	}
	sort.Strings(stale)
	for _, name := range stale {
		if realNow[name] {
			t.Errorf("%s lists %s, and it is in the tree now; delete the entry -- the name is a real path and the rule should hold it (the list only shrinks)",
				namedPathsAllowlistPath, name)
			continue
		}
		t.Errorf("%s lists %s, and nothing names it any more; delete the stale entry (the list only shrinks)",
			namedPathsAllowlistPath, name)
	}
}

// namedPathsIn returns the repository paths one line of text names. It is the whole
// heuristic, and TestTheNamedPathHeuristicReadsWhatItClaims holds it to hand-written lines.
func namedPathsIn(text string) []string {
	var found []string
	for _, loc := range namedPathRe.FindAllStringIndex(text, -1) {
		start, end := loc[0], loc[1]
		if !namedPathStartsAToken(text, start) {
			continue
		}
		if namedPathIsTemplate(text, start, end) {
			continue
		}
		name := namedPathTrimmed(text[start:end])
		if name == "" || !strings.Contains(name, "/") {
			continue
		}
		if namedPathIsSymbol(name) {
			continue
		}
		found = append(found, name)
	}
	return found
}

// namedPathStartsAToken reports whether the candidate begins a word. It is what keeps an
// import path (`github.com/mas-bandwidth/nova-tools/internal/ci`), a URL and a path on a
// bench (`~/rowan-working/tools/x`) out of the rule: the rule is about paths written from
// the root of THIS repository, and those are the only ones whose existence it can judge.
func namedPathStartsAToken(text string, start int) bool {
	if start == 0 {
		return true
	}
	r, _ := utf8.DecodeLastRuneInString(text[:start])
	if r == '/' || r == '.' || r == '-' || r == '_' || r == '~' {
		return false
	}
	return !unicode.IsLetter(r) && !unicode.IsDigit(r)
}

// namedPathIsTemplate reports whether the candidate is a PATTERN. A glob or a template
// breaks out of the path character set at its metacharacter, so the candidate stops
// there; the metacharacter that stopped it is the rune immediately after, and a pattern
// whose metacharacter comes first (`**/docs/x`) is caught by the rune immediately before.
func namedPathIsTemplate(text string, start, end int) bool {
	if end < len(text) {
		r, _ := utf8.DecodeRuneInString(text[end:])
		if strings.ContainsRune(namedPathMetaRunes, r) {
			return true
		}
	}
	if start > 0 {
		r, _ := utf8.DecodeLastRuneInString(text[:start])
		if strings.ContainsRune(namedPathMetaRunes, r) {
			return true
		}
	}
	return false
}

// namedPathIsSymbol reports whether the token names a Go SYMBOL rather than a file. This
// package writes `internal/merge.Enqueuer.Enqueue`, `internal/bus.IsProgress` and
// `internal/lockfile/TestLockRule1` all the time -- a package directory, then the exported
// thing inside it -- and none of them is a path a friend opens. The signal is Go's own: an
// exported identifier begins with an upper-case letter, and no file extension we write
// does. So a last element whose first rune after the package name is upper case, or which
// is upper case outright, is a symbol.
func namedPathIsSymbol(name string) bool {
	last := name[strings.LastIndex(name, "/")+1:]
	if last == "" {
		return false
	}
	if _, ident, ok := strings.Cut(last, "."); ok {
		r, _ := utf8.DecodeRuneInString(ident)
		return unicode.IsUpper(r)
	}
	r, _ := utf8.DecodeRuneInString(last)
	return unicode.IsUpper(r)
}

// namedPathTrimmed drops the punctuation a sentence leaves on the end of a path: the full
// stop of "see docs/SPEC-CI.md.", the dash of a range, the separator of a list. A trailing
// slash is kept -- it says the name is a directory, which is a thing we can check.
func namedPathTrimmed(name string) string {
	for {
		trimmed := strings.TrimRight(name, ".-")
		if trimmed == name {
			return name
		}
		name = trimmed
	}
}

// namedPathExists reports whether the name is in the tree. A name written with a trailing
// slash must be a directory; anything else may be either.
//
// A `testdata/...` name is the one relative name this repository writes, because a
// fixture path is always written from the package that owns it -- every class test in this
// package holds its allowlist as `const … = "testdata/…"` -- so such a name is looked for
// under every package, not only at the root.
func namedPathExists(root, name string) bool {
	if namedPathAt(root, name) {
		return true
	}
	if !strings.HasPrefix(name, "testdata/") {
		return false
	}
	for _, dir := range namedPathTestdataDirs(root) {
		if namedPathAt(dir, strings.TrimPrefix(name, "testdata/")) {
			return true
		}
	}
	return false
}

// namedPathAt is the plain question: is this name a file or a directory under base?
func namedPathAt(base, name string) bool {
	info, err := os.Stat(filepath.Join(base, filepath.FromSlash(strings.TrimSuffix(name, "/"))))
	if err != nil {
		return false
	}
	if strings.HasSuffix(name, "/") {
		return info.IsDir()
	}
	return true
}

// namedPathTestdataDirs is every `testdata` directory in the tree, found once and kept,
// because the existence question above asks for them on every candidate.
var namedPathTestdataDirs = func() func(root string) []string {
	var once sync.Once
	var dirs []string
	return func(root string) []string {
		once.Do(func() {
			_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
				if err != nil || !d.IsDir() {
					return nil //nolint:nilerr // an unreadable directory holds no fixtures we can name
				}
				if d.Name() == ".git" {
					return filepath.SkipDir
				}
				if d.Name() == "testdata" {
					dirs = append(dirs, path)
					return filepath.SkipDir
				}
				return nil
			})
		})
		return dirs
	}
}()

// namedPathSources lists what the rule reads, repo-relative and slash-separated: every
// non-test `.go` file in the tree, and every `.md` file under `docs/`. Test files and
// fixtures under a `testdata/` directory are not read -- their whole job is to be an
// invented tree, and `internal/check/nocode_test.go` alone writes eight of them -- and
// neither is anything under `.git`.
func namedPathSources(t *testing.T, root string) []string {
	t.Helper()
	var files []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			base := d.Name()
			if rel != "." && (base == ".git" || base == "testdata" || base == "vendor" || base == "node_modules") {
				return filepath.SkipDir
			}
			return nil
		}
		switch {
		case strings.HasSuffix(rel, ".go") && !strings.HasSuffix(rel, "_test.go"):
			files = append(files, rel)
		case strings.HasSuffix(rel, ".md") && strings.HasPrefix(rel, "docs/"):
			files = append(files, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(files)
	return files
}

// readNamedPathAllowlist reads the shrink-only list. One name per line, then its reason.
func readNamedPathAllowlist(t *testing.T) map[string]bool {
	t.Helper()
	raw, err := os.ReadFile(namedPathsAllowlistPath)
	if err != nil {
		t.Fatal(err)
	}
	allow := map[string]bool{}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, reason, _ := strings.Cut(line, " ")
		if strings.TrimSpace(reason) == "" {
			t.Errorf("%s: %q carries no reason; every narrowing says why it is one", namedPathsAllowlistPath, name)
		}
		allow[strings.TrimSpace(name)] = true
	}
	return allow
}

// TestTheNamedPathHeuristicReadsWhatItClaims holds the reader against hand-written lines,
// so a rule that quietly stopped matching anything cannot pass as a green run.
func TestTheNamedPathHeuristicReadsWhatItClaims(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		text string
		want []string
	}{
		{
			name: "a path in a comment",
			text: "// `fleet standard` is tools/bench-standard.sh and the Mac bench's standard",
			want: []string{"tools/bench-standard.sh"},
		},
		{
			name: "a path in a string literal",
			text: `const p = "internal/ci/testdata/net-allowlist.txt"`,
			want: []string{"internal/ci/testdata/net-allowlist.txt"},
		},
		{
			name: "two paths on one line",
			text: "// internal/ci/doc.go -> tools/bench-standard.sh",
			want: []string{"internal/ci/doc.go", "tools/bench-standard.sh"},
		},
		{
			name: "a sentence's full stop is not part of the path",
			text: "See docs/SPEC-CI.md.",
			want: []string{"docs/SPEC-CI.md"},
		},
		{
			name: "an import path is somebody else's tree",
			text: `import "github.com/mas-bandwidth/nova-tools/internal/ci"`,
		},
		{
			name: "a path on a bench is not ours",
			text: "// runs from ~/rowan-working/tools/fill-loop.sh",
		},
		{
			name: "a glob is a pattern, not a path",
			text: "// reads .github/workflows/*.yml and docs/SPEC-*.md",
		},
		{
			name: "a doublestar prefix is a pattern too",
			text: "// matched by **/internal/ci/doc.go",
		},
		{
			name: "a template is a pattern",
			text: "// writes docs/{{.Name}}.md",
		},
		{
			name: "a printf verb is a pattern",
			text: `fmt.Sprintf("internal/%s/doc.go", pkg)`,
		},
		{
			name: "a bare directory with no slash is not a name to follow",
			text: "// the internal package and the cmd package",
		},
		{
			name: "a directory named with a trailing slash",
			text: "// everything under internal/ci/testdata/ is a fixture",
			want: []string{"internal/ci/testdata/"},
		},
		{
			name: "a word that merely ends in a root name",
			text: "// the subcommand is in mycmd/nova-foo",
		},
		{
			name: "a package-qualified symbol is not a file",
			text: "// admission is internal/merge.Enqueuer.Enqueue and nothing else",
		},
		{
			name: "an exported name under a package directory is not a file",
			text: "// the rule is internal/lockfile/TestLockRule1",
		},
		{
			name: "a file whose name is upper case is still a file",
			text: "// see docs/SPEC-CI.md for the rule",
			want: []string{"docs/SPEC-CI.md"},
		},
	}
	for _, tc := range cases {
		got := namedPathsIn(tc.text)
		if len(got) != len(tc.want) {
			t.Errorf("%s: namedPathsIn(%q) = %v, want %v", tc.name, tc.text, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("%s: namedPathsIn(%q) = %v, want %v", tc.name, tc.text, got, tc.want)
				break
			}
		}
	}
}

// TestTheNamedPathExistenceCheckReadsTheTree holds the other half: the existence question
// itself, against this package's own directory, so a check that answered "yes" to
// everything could not pass.
func TestTheNamedPathExistenceCheckReadsTheTree(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	for _, name := range []string{"internal/ci/doc.go", "internal/ci", "internal/ci/"} {
		if !namedPathExists(root, name) {
			t.Errorf("namedPathExists(%q) = false, want true", name)
		}
	}
	for _, name := range []string{"internal/ci/no-such-file.go", "internal/ci/doc.go/", "cmd/no-such-tool"} {
		if namedPathExists(root, name) {
			t.Errorf("namedPathExists(%q) = true, want false", name)
		}
	}
}
