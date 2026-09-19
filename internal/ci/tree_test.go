package ci

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// tree_test.go is the shared repository tree the class tests read: ONE walk and
// ONE parse per test process, which each rule then filters in memory.
//
// Why it exists: every class test in this package asserts a property over the
// repository's own source, and each one used to walk cmd/ and internal/ itself,
// read every .go file off the disk, and hand it to go/parser again. Eight walks
// and eight parses of the same 1,052 files for eight rules that all read the
// same bytes. The tests are READ-ONLY over the tree -- no test in this package
// writes a file the walk can see -- so nothing invalidates the cache, and the
// second walk was never buying anything.
//
// On parse mode: the tree is parsed with mode 0, because every rule that reads
// it walks declarations and expressions and none of them reads a comment. The
// production checkers that DO want comments (the net checker, which reads
// `// net-ok:` reasons) take a caller-supplied root and are not tests, so they
// keep their own walk; if a rule here ever needs comments, cache a second
// variant keyed by parser.Mode rather than widening this one, so that changing
// the mode can never change what an existing rule sees.
//
// Rules filter the tree themselves rather than asking for a pre-filtered list,
// because the filters differ in ways that matter: some rules read testdata (the
// budget rule holds its own fixtures to the wall-clock law), some skip it (the
// shared-temp rule would otherwise find the offenders it plants), and skipping
// the wrong one is the difference between a rule that holds and a rule that
// passes by checking nothing.

// treeFile is one file of the repository as the class tests see it.
type treeFile struct {
	// Path is the absolute path on this machine, the name the walks used to
	// hand to os.ReadFile.
	Path string
	// Rel is the repo-relative, slash-separated path every finding prints.
	Rel string
	// Go is true for a .go file, Test for a _test.go file.
	Go   bool
	Test bool
	// Src is the file's bytes. It is kept for .go files and for everything
	// under .github/, which are the files the rules read as text; for anything
	// else it is nil and only the path is indexed.
	Src []byte
	// AST is the parsed file (mode 0), and ParseErr what go/parser said if it
	// refused. Both are nil for a file that is not .go.
	AST      *ast.File
	ParseErr error
}

// InDir reports whether the file lies under the repo-relative directory dir,
// written with forward slashes and no trailing slash ("cmd", "internal",
// ".github"). This is the filter that replaces `filepath.WalkDir(root/dir, …)`.
func (f *treeFile) InDir(dir string) bool {
	return strings.HasPrefix(f.Rel, dir+"/")
}

// InAnyDir reports whether the file lies under any of the given directories.
func (f *treeFile) InAnyDir(dirs ...string) bool {
	for _, d := range dirs {
		if f.InDir(d) {
			return true
		}
	}
	return false
}

// HasDirNamed reports whether any directory component of the file's path is
// name. It is how a rule replays a walk's `if d.IsDir() && d.Name() == "x" {
// return filepath.SkipDir }`.
func (f *treeFile) HasDirNamed(name string) bool {
	rel := f.Rel
	i := strings.LastIndex(rel, "/")
	if i < 0 {
		return false
	}
	for _, part := range strings.Split(rel[:i], "/") {
		if part == name {
			return true
		}
	}
	return false
}

// repoTreeIndex is the whole tree, walked once.
type repoTreeIndex struct {
	// Root is the absolute repository root every Rel is relative to.
	Root string
	// FSet is the one FileSet every cached AST's positions resolve in.
	FSet *token.FileSet
	// Files is every file found, in walk order, .git excluded.
	Files []*treeFile

	// byRel indexes Files by Rel, built on the first ByRel call. The tree is
	// read from parallel tests, so the map is built once behind its own Once
	// rather than lazily on each caller's goroutine.
	relOnce sync.Once
	byRel   map[string]*treeFile
}

// ByRel returns the file at the repo-relative, slash-separated path, or nil.
func (x *repoTreeIndex) ByRel(rel string) *treeFile {
	x.relOnce.Do(func() {
		x.byRel = make(map[string]*treeFile, len(x.Files))
		for _, f := range x.Files {
			x.byRel[f.Rel] = f
		}
	})
	return x.byRel[rel]
}

// GoFilesUnder returns the .go files under the given repo-relative directories,
// in walk order. tests selects whether _test.go files or the rest are wanted.
func (x *repoTreeIndex) GoFilesUnder(tests bool, dirs ...string) []*treeFile {
	var out []*treeFile
	for _, f := range x.Files {
		if f.Go && f.Test == tests && f.InAnyDir(dirs...) {
			out = append(out, f)
		}
	}
	return out
}

// treeSkipDirs are the directory names the shared walk never descends into.
// .git is not source, it is tens of thousands of objects, and walking it would
// cost more than every rule that reads the tree put together. No rule reads it:
// the ones that walk the whole root skip it by name already.
var treeSkipDirs = map[string]bool{".git": true}

var (
	repoTreeOnce  sync.Once
	repoTreeIdx   *repoTreeIndex
	repoTreeErr   error
	repoTreeCount int
)

// repoTree returns the shared tree, loading it on the first call. Every later
// caller in this process gets the same index, and the walk does not run again.
func repoTree(t *testing.T) *repoTreeIndex {
	t.Helper()
	root := repoRoot(t)
	repoTreeOnce.Do(func() {
		repoTreeCount++
		repoTreeIdx, repoTreeErr = loadRepoTree(root)
	})
	if repoTreeErr != nil {
		t.Fatalf("loading the shared repository tree at %s: %v", root, repoTreeErr)
	}
	return repoTreeIdx
}

// repoTreeLoads is how many times the loader has run in this process. It is
// written inside the sync.Once and read only after it has returned, so every
// read is ordered after the single write.
func repoTreeLoads() int { return repoTreeCount }

// loadRepoTree is the one walk. It reads the bytes of every .go file and of
// everything under .github/, and parses the .go files into one FileSet. A file
// go/parser refuses is kept with its error rather than failing the load, so the
// rule that reads it reports the refusal the way its own walk used to.
func loadRepoTree(root string) (*repoTreeIndex, error) {
	idx := &repoTreeIndex{Root: root, FSet: token.NewFileSet()}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		// The skip is by NAME, before the directory test, because `.git` is not
		// always a directory. In a `git worktree` -- which is how nova-sandbox gives
		// a worker its own checkout -- `.git` is a one-line FILE holding `gitdir:
		// <path>`. A skip written as "a directory called .git" therefore skipped
		// nothing there, the walk took .git in as an ordinary file, and
		// TestSharedRepoTreeSkipsTheGitDirectory failed for every run inside a
		// worktree while passing in a clone. A gate that is red because of where it
		// was run teaches a worker to ignore it.
		if treeSkipDirs[d.Name()] {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		// Anything that is not a directory is a file the walks this replaces
		// would have handed to os.ReadFile, symlinks included. A read that
		// fails fails the LOAD, loudly, rather than quietly shrinking the tree:
		// a rule that sweeps a tree it cannot see passes by checking nothing,
		// and that is the one failure mode every rule here is written against.
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		rel = filepath.ToSlash(rel)
		f := &treeFile{
			Path: path,
			Rel:  rel,
			Go:   strings.HasSuffix(rel, ".go"),
			Test: strings.HasSuffix(rel, "_test.go"),
		}
		if f.Go || strings.HasPrefix(rel, ".github/") {
			raw, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			f.Src = raw
		}
		if f.Go {
			// The name handed to the parser is the absolute path, which is what
			// the walks this replaces passed, so any message go/parser prints
			// reads the same as before.
			f.AST, f.ParseErr = parser.ParseFile(idx.FSet, path, f.Src, 0)
		}
		idx.Files = append(idx.Files, f)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return idx, nil
}
