// Package repotree walks a checkout's files once for the class tests, so each
// rule filters a shared read instead of walking the tree itself.
//
// Files reads the bytes of one query once per process. Parsed parses the Go
// files of that query once per process, with comments, into one FileSet. The
// cache is guarded by sync.Once per query key, because tests run in parallel.
// The cached bytes, ASTs and FileSet are read-only and shared by every caller
// in the process: a caller must not mutate them, and a later write to the
// tree is not seen. .git is always skipped. A Go file that does not parse is
// kept, with the error, so the rule that reads it reports the refusal.
//
//	files := repotree.Files(t, repotree.Query{
//		Under:    []string{"cmd", "internal", "pkg"},
//		Ext:      ".go",
//		SkipDirs: []string{"testdata"},
//	})
package repotree

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// Query selects the files one walk reads. Root empty is the checkout root
// from Root; a fixture sets Root to its own directory. Under lists
// slash-separated directories relative to Root, empty meaning Root itself.
// Ext is the extension including the dot, such as ".go"; empty keeps every
// file. SkipDirs are directory base names the walk does not descend into.
type Query struct {
	Root     string
	Under    []string
	Ext      string
	SkipDirs []string
}

// File is one file of a query. Rel is slash-separated and relative to the
// query's root. Src is the file's bytes, read once and shared; it is
// read-only.
type File struct {
	Rel string
	Src []byte
}

// ParsedFile is one Go file of a query, parsed once. AST and FSet are shared
// and read-only. Err is set when the file does not parse; the bytes are still
// in Src.
type ParsedFile struct {
	File
	AST  *ast.File
	Err  error
	FSet *token.FileSet
}

// Root returns the checkout root, the directory at or above the working
// directory that holds go.mod.
func Root(t testing.TB) string {
	t.Helper()
	root, err := checkoutRoot()
	require.NoError(t, err)
	return root
}

// Files returns the files of q, relative paths with their bytes, read once
// per process for that query and shared. The slice is the caller's; the bytes
// are not.
func Files(t testing.TB, q Query) []File {
	t.Helper()
	e, err := entryFor(q)
	require.NoError(t, err)
	out := make([]File, len(e.files))
	copy(out, e.files)
	return out
}

// Parsed returns the Go files of q parsed once per process, shared by every
// caller that asks. A file that does not parse is included with Err set.
func Parsed(t testing.TB, q Query) []ParsedFile {
	t.Helper()
	e, err := entryFor(q)
	require.NoError(t, err)
	out := make([]ParsedFile, len(e.parsed))
	copy(out, e.parsed)
	return out
}

type entry struct {
	once   sync.Once
	files  []File
	parsed []ParsedFile
	err    error
	reads  int
}

var (
	cacheMu sync.Mutex
	cache   = map[string]*entry{}

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
			if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
				rootPath = dir
				return
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				rootErr = fmt.Errorf("repotree: no go.mod at or above the working directory")
				return
			}
			dir = parent
		}
	})
	return rootPath, rootErr
}

func entryFor(q Query) (*entry, error) {
	q, err := normalize(q)
	if err != nil {
		return nil, err
	}
	key := cacheKey(q)
	cacheMu.Lock()
	e := cache[key]
	if e == nil {
		e = &entry{}
		cache[key] = e
	}
	cacheMu.Unlock()
	e.once.Do(func() {
		e.reads++
		e.files, e.parsed, e.err = walk(q)
	})
	return e, e.err
}

func reads(q Query) int {
	q, err := normalize(q)
	if err != nil {
		return 0
	}
	cacheMu.Lock()
	e := cache[cacheKey(q)]
	cacheMu.Unlock()
	if e == nil {
		return 0
	}
	return e.reads
}

func cacheKey(q Query) string {
	return q.Root + "\x00" + q.Ext + "\x00" + strings.Join(q.Under, "\x00") + "\x00" + strings.Join(q.SkipDirs, "\x00")
}

func normalize(q Query) (Query, error) {
	if q.Root == "" {
		root, err := checkoutRoot()
		if err != nil {
			return Query{}, err
		}
		q.Root = root
	}
	abs, err := filepath.Abs(q.Root)
	if err != nil {
		return Query{}, err
	}
	q.Root = abs
	unders, err := cleanUnders(q.Under)
	if err != nil {
		return Query{}, err
	}
	q.Under = unders
	skips := append([]string(nil), q.SkipDirs...)
	sort.Strings(skips)
	q.SkipDirs = slices.Compact(skips)
	return q, nil
}

func cleanUnders(under []string) ([]string, error) {
	if len(under) == 0 {
		return []string{""}, nil
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(under))
	for _, u := range under {
		u = path.Clean(u)
		if u == "." {
			u = ""
		}
		if u != "" && !filepath.IsLocal(filepath.FromSlash(u)) {
			return nil, fmt.Errorf("repotree: %q escapes the root", u)
		}
		if seen[u] {
			continue
		}
		seen[u] = true
		out = append(out, u)
	}
	sort.Strings(out)
	return out, nil
}

func walk(q Query) ([]File, []ParsedFile, error) {
	skip := map[string]bool{".git": true}
	for _, name := range q.SkipDirs {
		skip[name] = true
	}
	var files []File
	for _, under := range q.Under {
		start := q.Root
		if under != "" {
			start = filepath.Join(q.Root, filepath.FromSlash(under))
		}
		err := filepath.WalkDir(start, func(p string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if d.IsDir() {
				if p != start && skip[d.Name()] {
					return fs.SkipDir
				}
				return nil
			}
			if d.Name() == ".git" {
				return nil
			}
			rel, relErr := filepath.Rel(q.Root, p)
			if relErr != nil {
				return relErr
			}
			rel = filepath.ToSlash(rel)
			if q.Ext != "" && filepath.Ext(rel) != q.Ext {
				return nil
			}
			raw, readErr := os.ReadFile(p)
			if readErr != nil {
				return fmt.Errorf("repotree: reading %s: %w", rel, readErr)
			}
			files = append(files, File{Rel: rel, Src: raw})
			return nil
		})
		if err != nil {
			return nil, nil, fmt.Errorf("repotree: walking %s: %w", start, err)
		}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Rel < files[j].Rel })
	files = slices.CompactFunc(files, func(a, b File) bool { return a.Rel == b.Rel })
	fset := token.NewFileSet()
	var parsed []ParsedFile
	for _, f := range files {
		if filepath.Ext(f.Rel) != ".go" {
			continue
		}
		abs := filepath.Join(q.Root, filepath.FromSlash(f.Rel))
		file, parseErr := parser.ParseFile(fset, abs, f.Src, parser.ParseComments)
		parsed = append(parsed, ParsedFile{File: f, AST: file, Err: parseErr, FSet: fset})
	}
	return files, parsed, nil
}
