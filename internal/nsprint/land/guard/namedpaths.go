package guard

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/mas-bandwidth/nova-tools/internal/ci/allowlist"
)

// NamedPathsAllowlist is the shrink-only list of names that look like a
// path into the repository and are not one (planned files, shape examples,
// other trees), kept by internal/ci beside the class test that owns it. The
// guard reads the tree's own copy.
const NamedPathsAllowlist = "internal/ci/testdata/namedpaths_allowlist.txt"

// namedPathRe finds the candidates: a token that starts with one of the
// repository's own top-level directories and carries a slash. The tail is
// the character set a path of ours is written in; a glob or a template
// breaks out of it at its first metacharacter.
var namedPathRe = regexp.MustCompile(`(?:\.github|cmd|internal|docs|tools|scripts|testdata|fleet|infra)/[\w./-]+`)

// namedPathMetaRunes mean the token is a pattern rather than a path.
const namedPathMetaRunes = "*?{}<>$%[|…"

// namedPaths is the class rule of internal/ci (TestEveryNamedRepoPathExists)
// as a library call: every repository path a non-test Go file or a docs page
// names must exist in the tree. On a merged tree it catches a member that
// moved a file another member's new comment names.
func namedPaths(_ context.Context, root string) Result {
	allow, err := readNamedPathAllowlist(root)
	if err != nil {
		return Result{Err: err}
	}
	files, err := namedPathSources(root)
	if err != nil {
		return Result{Err: err}
	}
	if len(files) == 0 {
		return Result{Err: fmt.Errorf("no Go source or docs under %s", root)}
	}
	testdataDirs := namedPathTestdataDirs(root)
	checked := 0
	for _, relPath := range files {
		raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relPath)))
		if err != nil {
			return Result{Err: err}
		}
		for i, text := range strings.Split(string(raw), "\n") {
			for _, name := range namedPathsIn(text) {
				checked++
				if allow.Has(name) || namedPathExists(root, testdataDirs, name) {
					continue
				}
				return Result{OK: false, File: fmt.Sprintf("%s:%d", relPath, i+1), Why: name + " names no file or directory in this tree; correct the path or list it in " + NamedPathsAllowlist + " with the reason"}
			}
		}
	}
	return Result{OK: true, Why: fmt.Sprintf("%d names in %d files exist", checked, len(files))}
}

// readNamedPathAllowlist reads the list through internal/ci/allowlist, the one
// reader of every allowlist (nova-tools#4339); a tree without the file has
// nothing parked in it.
func readNamedPathAllowlist(root string) (*allowlist.List, error) {
	return allowlist.Load(filepath.Join(root, filepath.FromSlash(NamedPathsAllowlist)), allowlist.Options{MissingIsEmpty: true})
}

// namedPathsIn returns the repository paths one line of text names.
func namedPathsIn(text string) []string {
	var found []string
	for _, loc := range namedPathRe.FindAllStringIndex(text, -1) {
		start, end := loc[0], loc[1]
		if !namedPathStartsAToken(text, start) || namedPathIsTemplate(text, start, end) {
			continue
		}
		name := strings.TrimRight(text[start:end], ".-")
		if name == "" || !strings.Contains(name, "/") || namedPathIsSymbol(name) {
			continue
		}
		found = append(found, name)
	}
	return found
}

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

// namedPathIsSymbol: an exported Go identifier after the package directory
// is a symbol, not a file.
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

func namedPathExists(root string, testdataDirs []string, name string) bool {
	if namedPathAt(root, name) {
		return true
	}
	if !strings.HasPrefix(name, "testdata/") {
		return false
	}
	for _, dir := range testdataDirs {
		if namedPathAt(dir, strings.TrimPrefix(name, "testdata/")) {
			return true
		}
	}
	return false
}

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

func namedPathTestdataDirs(root string) []string {
	var dirs []string
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
	return dirs
}

// namedPathSources: every non-test .go file and every .md under docs/,
// repository-relative, sorted.
func namedPathSources(root string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		r := rel(root, path)
		if d.IsDir() {
			base := d.Name()
			if r != "." && (base == ".git" || base == "testdata" || base == "vendor" || base == "node_modules") {
				return filepath.SkipDir
			}
			return nil
		}
		switch {
		case strings.HasSuffix(r, ".go") && !strings.HasSuffix(r, "_test.go"):
			files = append(files, r)
		case strings.HasSuffix(r, ".md") && strings.HasPrefix(r, "docs/"):
			files = append(files, r)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	return files, nil
}
