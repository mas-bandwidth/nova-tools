package ci

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// tree_contract_test.go pins the contract of the shared repository tree that
// every class test in this package reads.
//
// The hurt: each class test used to run its own filepath.WalkDir over cmd/ and
// internal/, read every .go file off the disk and hand it to go/parser again.
// Eight walks and eight parses of the same 1,052 files, one per rule, for a
// tree that nothing in this package writes to. Measured on hulk at dev
// 04bb4e1c, `go test ./internal/ci/ -count=1` took 10.2 s and 10.4 s, and
// -race took 49.4 s; roughly a third of that was the same bytes read and the
// same syntax trees built over and over.
//
// The class fix is ONE walk and ONE parse per test process, cached behind a
// sync.Once, which every rule then filters in memory. This test is what makes
// that a contract rather than an implementation detail, because the whole
// saving rests on three properties that are invisible from any single rule:
//
//	(a) the tree is not empty and names this repository, so a rule that filters
//	    it cannot pass by checking nothing -- the failure mode every class test
//	    here is written to avoid;
//	(b) every .go file the tree lists carries a parsed syntax tree, so a rule
//	    may read f.AST without asking whether the loader got that far;
//	(c) the walk happens exactly once however many callers ask for it, which is
//	    the entire point -- a cache that reloads is just a walk with extra
//	    bookkeeping.
//
// The tests are READ-ONLY over the tree, so nothing invalidates the cache.
func TestSharedRepoTreeListsAndParsesTheRepository(t *testing.T) {
	t.Parallel()

	tree := repoTree(t)

	if tree.Root == "" {
		t.Fatal("the shared tree carries no root; every finding a class test prints is relative to it")
	}
	if len(tree.Files) == 0 {
		t.Fatal("the shared tree lists no files; a class test filtering an empty tree passes by checking nothing")
	}
	if tree.FSet == nil {
		t.Fatal("the shared tree carries no FileSet; a finding without a line number is a finding nobody can act on")
	}

	// (a) It is THIS repository: the walk found the two trees every class test
	// reads, and it found this very file.
	var cmdFiles, internalFiles int
	var foundSelf bool
	for _, f := range tree.Files {
		switch {
		case strings.HasPrefix(f.Rel, "cmd/"):
			cmdFiles++
		case strings.HasPrefix(f.Rel, "internal/"):
			internalFiles++
		}
		if f.Rel == "internal/ci/tree_contract_test.go" {
			foundSelf = true
		}
	}
	if cmdFiles == 0 || internalFiles == 0 {
		t.Errorf("the shared tree lists %d files under cmd/ and %d under internal/; the class tests read both trees", cmdFiles, internalFiles)
	}
	if !foundSelf {
		t.Error("the shared tree does not list internal/ci/tree_contract_test.go; a walk that misses the file it is declared in is looking in the wrong place")
	}

	// (b) Every .go file it lists is parsed, and the parse is usable: the file
	// carries a package clause and its position resolves in the shared FileSet.
	goFiles := 0
	for _, f := range tree.Files {
		if !f.Go {
			if f.AST != nil {
				t.Errorf("%s is not a .go file yet carries a syntax tree", f.Rel)
			}
			continue
		}
		goFiles++
		if f.ParseErr != nil {
			t.Errorf("%s: the shared tree failed to parse it: %v", f.Rel, f.ParseErr)
			continue
		}
		if f.AST == nil {
			t.Errorf("%s: the shared tree lists a .go file with no syntax tree and no error", f.Rel)
			continue
		}
		if f.AST.Name == nil || f.AST.Name.Name == "" {
			t.Errorf("%s: the parsed file carries no package clause", f.Rel)
			continue
		}
		if pos := tree.FSet.Position(f.AST.Package); pos.Line == 0 {
			t.Errorf("%s: its positions do not resolve in the shared FileSet", f.Rel)
		}
		if len(f.Src) == 0 {
			t.Errorf("%s: the shared tree parsed it but kept no source; the text-only rules read f.Src", f.Rel)
		}
		if f.Test != strings.HasSuffix(f.Rel, "_test.go") {
			t.Errorf("%s: Test is %v; the rules split the tree on the _test.go suffix", f.Rel, f.Test)
		}
	}
	if goFiles == 0 {
		t.Fatal("the shared tree lists no .go files at all")
	}

	// (c) One walk, however many callers. Two more calls, and the loader has
	// still run once.
	_ = repoTree(t)
	_ = repoTree(t)
	if got := repoTreeLoads(); got != 1 {
		t.Errorf("the shared tree loaded %d times across four calls, want exactly 1; a cache that reloads is a walk with extra bookkeeping", got)
	}
}

// TestSharedRepoTreeSkipsTheGitDirectory: .git is not source, it is tens of
// thousands of objects, and walking it would cost more than every rule that
// reads the tree put together.
func TestSharedRepoTreeSkipsTheGitDirectory(t *testing.T) {
	t.Parallel()

	for _, f := range repoTree(t).Files {
		if f.Rel == ".git" || strings.HasPrefix(f.Rel, ".git/") {
			t.Fatalf("the shared tree walked into .git (%s); no rule reads it and the walk is the cost", f.Rel)
		}
	}
}

// And .git is skipped whatever it IS. In a linked worktree -- which is where
// every lane in this repository works -- `.git` is a FILE holding one
// `gitdir:` line, not a directory, so a walk that skips it only as a directory
// lists it as a file: not source, unparseable as Go, and enough to fail the rule
// above on a tree that is otherwise clean.
func TestSharedRepoTreeSkipsAGitFileAsWellAsAGitDirectory(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	// The worktree shape: .git is one line naming the real repository.
	if err := os.WriteFile(filepath.Join(root, ".git"), []byte("gitdir: /nowhere\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "kept.go"), []byte("package kept\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	idx, err := loadRepoTree(root)
	if err != nil {
		t.Fatalf("loading a tree whose .git is a file: %v", err)
	}
	var rels []string
	for _, f := range idx.Files {
		rels = append(rels, f.Rel)
		if f.Rel == ".git" || strings.HasPrefix(f.Rel, ".git/") {
			t.Errorf("the walk listed %s; .git is skipped by name, whether it is a directory or a worktree's file", f.Rel)
		}
	}
	if idx.ByRel("kept.go") == nil {
		t.Errorf("the walk lost the one source file beside .git; it lists %v", rels)
	}
}
