// Package repotree walks a checkout once per query and shares that read with
// every test in the process. Class tests that judge the tree call Files or
// Parsed instead of walking and parsing on their own.
//
// The cache stores read-only data. File.Src, ParsedFile.AST and ParsedFile.FSet
// are shared across callers of the same query and must not be mutated. The tree
// a query names is read once; a later call in the same process returns the same
// bytes and the same parse.
//
//	files := repotree.Files(t, repotree.Query{Under: []string{"cmd", "internal", "pkg"}, Ext: ".go", SkipDirs: []string{"testdata"}})
//	parsed := repotree.Parsed(t, q)
//
// The walk is filepath.WalkDir, the call internal/ci's source seams name. That
// package's shared index lives in a test file and cannot be imported, so this
// package does not copy it.
package repotree

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

// Query selects files under a checkout. Under lists repo-relative directories
// (slash-separated); an empty list walks the checkout root. Ext is a file
// extension including the dot (".go"); empty selects every file. SkipDirs names
// directories the walk does not descend into. ".git" is always skipped.
type Query struct {
	Under    []string
	Ext      string
	SkipDirs []string
}

// File is one selected file. Rel is slash-separated and relative to the
// checkout root. Src is read-only and shared with every caller of the query.
type File struct {
	Rel string
	Src []byte
}

// ParsedFile is one Go file parsed once for a query. AST and FSet are read-only
// and shared. Err is set when go/parser refused the file; AST may still be partial.
type ParsedFile struct {
	Rel  string
	AST  *ast.File
	FSet *token.FileSet
	Err  error
}

// Root returns the checkout root, the directory whose go.mod declares this module.
func Root(t testing.TB) string {
	t.Helper()
	root, err := checkoutRoot()
	require.NoError(t, err)
	return root
}

// Files returns the files Query selects, relative to the checkout root, with
// their bytes read once per process.
func Files(t testing.TB, q Query) []File {
	t.Helper()
	return filesAt(t, Root(t), q)
}

// Parsed returns the go/parser ASTs of the .go files Query selects, parsed once
// per process and shared by every caller of the same query.
func Parsed(t testing.TB, q Query) []ParsedFile {
	t.Helper()
	return parsedAt(t, Root(t), q)
}

func filesAt(t testing.TB, root string, q Query) []File {
	t.Helper()
	e := mustEntry(t, root, q)
	out := make([]File, len(e.files))
	copy(out, e.files)
	return out
}

func parsedAt(t testing.TB, root string, q Query) []ParsedFile {
	t.Helper()
	e := mustEntry(t, root, q)
	out := make([]ParsedFile, len(e.parsed))
	copy(out, e.parsed)
	return out
}

func mustEntry(t testing.TB, root string, q Query) *entry {
	t.Helper()
	checkQuery(t, q)
	e, err := entryFor(root, q)
	require.NoError(t, err)
	return e
}

func checkQuery(t testing.TB, q Query) {
	t.Helper()
	for _, u := range q.Under {
		require.True(t, filepath.IsLocal(filepath.FromSlash(u)), "repotree: Under %q is not a path inside the root", u)
	}
	for _, s := range q.SkipDirs {
		require.NotEmpty(t, s, "repotree: SkipDirs names a directory")
		require.False(t, strings.ContainsAny(s, `/\`), "repotree: SkipDirs %q names a directory, not a path", s)
	}
}

var (
	rootOnce sync.Once
	rootPath string
	rootErr  error
)

func checkoutRoot() (string, error) {
	rootOnce.Do(func() {
		dir, err := os.Getwd()
		if err != nil {
			rootErr = err
			return
		}
		for {
			b, err := os.ReadFile(filepath.Join(dir, "go.mod"))
			if err == nil && bytes.HasPrefix(b, []byte("module github.com/mas-bandwidth/nova-tools\n")) {
				rootPath = dir
				return
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				rootErr = fmt.Errorf("repotree: no module root above the working directory")
				return
			}
			dir = parent
		}
	})
	return rootPath, rootErr
}

type entry struct {
	once      sync.Once
	files     []File
	parsed    []ParsedFile
	err       error
	loads     atomic.Int64
	fileReads atomic.Int64
}

var cache sync.Map

func entryFor(root string, q Query) (*entry, error) {
	fresh := &entry{}
	v, _ := cache.LoadOrStore(cacheKey(root, q), fresh)
	e := v.(*entry)
	e.once.Do(func() { e.load(root, q) })
	return e, e.err
}

func cacheKey(root string, q Query) string {
	under := slices.Clone(q.Under)
	skip := slices.Clone(q.SkipDirs)
	slices.Sort(under)
	slices.Sort(skip)
	return root + "\x00" + q.Ext + "\x00" + strings.Join(under, "\x00") + "\x00" + strings.Join(skip, "\x00")
}

func (e *entry) load(root string, q Query) {
	e.loads.Add(1)
	skip := map[string]bool{".git": true}
	for _, name := range q.SkipDirs {
		skip[name] = true
	}
	unders := q.Under
	if len(unders) == 0 {
		unders = []string{"."}
	}
	seen := map[string]struct{}{}
	var files []File
	for _, under := range unders {
		dir := root
		if under != "." {
			dir = filepath.Join(root, filepath.FromSlash(under))
		}
		err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if d.IsDir() {
				if skip[d.Name()] {
					return fs.SkipDir
				}
				return nil
			}
			if q.Ext != "" && filepath.Ext(path) != q.Ext {
				return nil
			}
			rel, relErr := filepath.Rel(root, path)
			if relErr != nil {
				return relErr
			}
			rel = filepath.ToSlash(rel)
			if _, ok := seen[rel]; ok {
				return nil
			}
			seen[rel] = struct{}{}
			b, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			e.fileReads.Add(1)
			files = append(files, File{Rel: rel, Src: b})
			return nil
		})
		if err != nil {
			e.err = fmt.Errorf("repotree: walk %s: %w", dir, err)
			return
		}
	}
	slices.SortFunc(files, func(a, b File) int { return strings.Compare(a.Rel, b.Rel) })
	e.files = files
	fset := token.NewFileSet()
	for _, f := range files {
		if !strings.HasSuffix(f.Rel, ".go") {
			continue
		}
		astFile, err := parser.ParseFile(fset, f.Rel, f.Src, parser.ParseComments)
		e.parsed = append(e.parsed, ParsedFile{Rel: f.Rel, AST: astFile, FSet: fset, Err: err})
	}
}
