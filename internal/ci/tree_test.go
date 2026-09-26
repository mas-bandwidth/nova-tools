package ci

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
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
	// Src is the file's bytes. It is kept for .go files, for everything under
	// .github/ and for docs/*.md, which are the files the rules read as text;
	// for anything else it is nil and only the path is indexed.
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
	// byPath is the same index by absolute Path, for treeSourceFile.
	pathOnce sync.Once
	byPath   map[string]*treeFile
	// dirs is every directory's entries, for treeWalkDir.
	dirOnce sync.Once
	dirs    map[string][]treeEntry
}

// ByPath returns the file at the absolute path the walk gave it, or nil.
func (x *repoTreeIndex) ByPath(path string) *treeFile {
	x.pathOnce.Do(func() {
		x.byPath = make(map[string]*treeFile, len(x.Files))
		for _, f := range x.Files {
			x.byPath[f.Path] = f
		}
	})
	return x.byPath[path]
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

// treeSkipNames are the names the shared walk skips, as a directory it never
// descends into and as a file it never lists. .git is not source, it is tens of
// thousands of objects, and walking it would cost more than every rule that
// reads the tree put together. No rule reads it: the ones that walk the whole
// root skip it by name already.
var treeSkipNames = map[string]bool{".git": true}

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
	idx, err := sharedRepoTree()
	if err != nil {
		t.Fatalf("loading the shared repository tree at %s: %v", repoRoot(t), err)
	}
	return idx
}

// sharedRepoTree is repoTree for a caller with no *testing.T: the checkers'
// file reads, through treeSourceFile. The root is repoRoot's.
func sharedRepoTree() (*repoTreeIndex, error) {
	repoTreeOnce.Do(func() {
		repoTreeCount++
		root, err := filepath.Abs(filepath.Join("..", ".."))
		if err != nil {
			repoTreeErr = err
			return
		}
		repoTreeIdx, repoTreeErr = loadRepoTree(root)
	})
	return repoTreeIdx, repoTreeErr
}

// The production checkers (CheckGoEnv, CheckWaits, CheckNet, CheckTestbins,
// CheckTemplates, CheckCardTemplates, the bench-runner rule) each walk the tree,
// read every file they select and parse the Go ones. Run side by side in this
// package's tests they listed, opened and parsed the same files six times over;
// on macOS the opens alone were most of their time (nova-tools#4328). In this
// test binary the three seams in source.go are answered from the one shared
// read: the walk lists the tree's own index, the reads are its bytes, and a
// parse of those bytes is done once per file and mode. What each checker
// selects, and what it finds, is still its own code's.
func init() {
	walkSourceDir = treeWalkDir
	readSourceFile = treeSourceFile
	parseSource = treeParseSource
}

// treeSourceFile answers a read of a file the shared tree holds bytes for from
// those bytes, and anything else -- a fixture under t.TempDir(), a file the tree
// keeps no bytes for -- from the disk. The bytes are shared: the checkers only
// read them.
func treeSourceFile(path string) ([]byte, error) {
	if idx, err := sharedRepoTree(); err == nil {
		if f := idx.ByPath(path); f != nil && f.Src != nil {
			return f.Src, nil
		}
	}
	return os.ReadFile(path)
}

// repoTreeLoads is how many times the loader has run in this process. It is
// written inside the sync.Once and read only after it has returned, so every
// read is ordered after the single write.
func repoTreeLoads() int { return repoTreeCount }

// loadRepoTree is the one walk. It reads the bytes of every .go file and of
// everything under .github/, and parses the .go files into one FileSet. A file
// go/parser refuses is kept with its error rather than failing the load, so the
// rule that reads it reports the refusal the way its own walk used to.
//
// The walk only lists; the reads and parses are shared by treeLoadWorkers
// goroutines, each file into its own slot, so Files keeps walk order. A FileSet
// is safe for concurrent use. The worker count is small on purpose: the load is
// on the critical path of every class test in the package, but a unit test does
// not take a whole machine's cores to get there (Glenn 2026-09-26, #4328).
func loadRepoTree(root string) (*repoTreeIndex, error) {
	idx := &repoTreeIndex{Root: root, FSet: token.NewFileSet()}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if treeSkipNames[d.Name()] {
			if d.IsDir() {
				return fs.SkipDir
			}
			// A linked worktree's `.git` is a FILE holding one `gitdir:` line.
			// The name is what this list is about, so the entry goes whatever it is.
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
		idx.Files = append(idx.Files, &treeFile{
			Path: path,
			Rel:  rel,
			Go:   strings.HasSuffix(rel, ".go"),
			Test: strings.HasSuffix(rel, "_test.go"),
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	errs := make([]error, len(idx.Files))
	next := make(chan int)
	var wg sync.WaitGroup
	for w := 0; w < treeLoadWorkers(); w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range next {
				errs[i] = loadTreeFile(idx.FSet, idx.Files[i])
			}
		}()
	}
	for i := range idx.Files {
		next <- i
	}
	close(next)
	wg.Wait()
	for _, e := range errs {
		if e != nil {
			return nil, e
		}
	}
	return idx, nil
}

// treeLoadWorkers is how many goroutines read and parse the tree: four, or fewer
// when the process has fewer cores (a CI unit leg runs at two).
func treeLoadWorkers() int {
	return min(4, runtime.GOMAXPROCS(0))
}

// loadTreeFile reads one file's bytes when a rule reads them as text, and parses
// a .go file.
func loadTreeFile(fset *token.FileSet, f *treeFile) error {
	if !f.Go && !strings.HasPrefix(f.Rel, ".github/") && !(strings.HasPrefix(f.Rel, "docs/") && strings.HasSuffix(f.Rel, ".md")) {
		return nil
	}
	raw, err := os.ReadFile(f.Path)
	if err != nil {
		return err
	}
	f.Src = raw
	if f.Go {
		// The name handed to the parser is the absolute path, which is what
		// the walks this replaces passed, so any message go/parser prints
		// reads the same as before.
		f.AST, f.ParseErr = parser.ParseFile(fset, f.Path, f.Src, 0)
	}
	return nil
}

// treeWalkDir is filepath.WalkDir over the shared tree's index when root is a
// directory inside the tree, and filepath.WalkDir itself for anything else (the
// fixtures tests build under t.TempDir()). It keeps WalkDir's contract: lexical
// order within a directory, SkipDir on a directory skips it and on a file skips
// the rest of its directory, SkipAll stops the walk, and neither is returned.
// The index holds every file but .git, so a walk sees what the disk walk saw
// minus .git, which every checker skips by name.
func treeWalkDir(root string, fn fs.WalkDirFunc) error {
	idx, err := sharedRepoTree()
	if err != nil {
		return filepath.WalkDir(root, fn)
	}
	dirs := idx.dirEntries()
	if _, ok := dirs[root]; !ok {
		return filepath.WalkDir(root, fn)
	}
	err = treeWalk(dirs, root, treeEntry{path: root, name: filepath.Base(root), dir: true}, fn)
	if err == fs.SkipDir || err == fs.SkipAll {
		return nil
	}
	return err
}

// treeWalk is filepath's walkDir with the directory read from the index.
func treeWalk(dirs map[string][]treeEntry, path string, d treeEntry, fn fs.WalkDirFunc) error {
	if err := fn(path, d, nil); err != nil || !d.dir {
		if err == fs.SkipDir && d.dir {
			err = nil
		}
		return err
	}
	for _, e := range dirs[path] {
		if err := treeWalk(dirs, e.path, e, fn); err != nil {
			if err == fs.SkipDir {
				break
			}
			return err
		}
	}
	return nil
}

// treeEntry is one fs.DirEntry of the index: a file the walk found, or a
// directory on the way to one.
type treeEntry struct {
	path, name string
	dir        bool
}

func (e treeEntry) Name() string { return e.name }
func (e treeEntry) IsDir() bool  { return e.dir }
func (e treeEntry) Type() fs.FileMode {
	if e.dir {
		return fs.ModeDir
	}
	return 0
}
func (e treeEntry) Info() (fs.FileInfo, error) { return os.Lstat(e.path) }

// dirEntries is every directory of the tree by absolute path, root included,
// with its entries sorted by name the way os.ReadDir returns them. Built once.
func (x *repoTreeIndex) dirEntries() map[string][]treeEntry {
	x.dirOnce.Do(func() {
		x.dirs = map[string][]treeEntry{x.Root: nil}
		for _, f := range x.Files {
			child := treeEntry{path: f.Path, name: filepath.Base(f.Path)}
			for {
				parent := filepath.Dir(child.path)
				_, seen := x.dirs[parent]
				x.dirs[parent] = append(x.dirs[parent], child)
				if seen || parent == x.Root {
					break
				}
				child = treeEntry{path: parent, name: filepath.Base(parent), dir: true}
			}
		}
		for _, entries := range x.dirs {
			sort.Slice(entries, func(i, j int) bool { return entries[i].name < entries[j].name })
		}
	})
	return x.dirs
}

// treeParseSource is parseSource for this test binary. Bytes that ARE a tree
// file's bytes (the same array treeSourceFile handed out) are parsed once per
// name and mode -- mode 0 is the tree's own parse -- and the FileSet and file
// shared; the checkers only read them. Any other bytes -- a fixture's -- are
// parsed afresh every time.
func treeParseSource(name string, src []byte, mode parser.Mode) (*token.FileSet, *ast.File, error) {
	idx, err := sharedRepoTree()
	if err != nil || len(src) == 0 {
		return parseSourceFile(name, src, mode)
	}
	f := idx.ByRel(name)
	if f == nil {
		f = idx.ByPath(name)
	}
	if f == nil || len(f.Src) != len(src) || &f.Src[0] != &src[0] {
		return parseSourceFile(name, src, mode)
	}
	// The tree already parsed these bytes in mode 0, into its own FileSet. The
	// checkers read only lines out of a position, so that parse is theirs too,
	// and a caller that skips object resolution reads nothing resolution adds;
	// a file the tree could not parse is parsed again under the caller's name,
	// so the refusal reads as it always did.
	if (mode == 0 || mode == parser.SkipObjectResolution) && f.AST != nil && f.ParseErr == nil {
		return idx.FSet, f.AST, nil
	}
	v, _ := treeParses.LoadOrStore(treeParseKey{name, mode}, &treeParse{})
	p := v.(*treeParse)
	p.once.Do(func() { p.fset, p.file, p.err = parseSourceFile(name, src, mode) })
	return p.fset, p.file, p.err
}

// treeParses holds one parse per tree file and parser mode.
var treeParses sync.Map

type treeParseKey struct {
	name string
	mode parser.Mode
}

type treeParse struct {
	once sync.Once
	fset *token.FileSet
	file *ast.File
	err  error
}
