package ci

import (
	"fmt"
	"go/ast"
	"go/token"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// THE CLASS RULE BEHIND THE ONE DOOR (Glenn, 2026-09-18).
//
// "Nothing reaches the dev merge queue but a batch." Four pull requests landed on dev that
// morning that nobody enqueued: each carried GitHub's AUTO-MERGE, switched on hours
// earlier by a `gh pr merge` call made while the pull request was red, and the forge
// enqueued them itself as their checks went green. Twenty-seven open pull requests were
// carrying the same instruction when the sweep found them.
//
// The fix is not to remember not to type it. `gh pr merge` in any spelling is refused in
// every non-test Go file of the tools and in every file under .github/, with ONE exception
// named in a list that can only shrink: the audit's `--disable-auto`, which takes an
// auto-merge OFF. Admission to a merge queue is internal/merge.Enqueuer.Enqueue, the
// enqueuePullRequest mutation, and nothing else.
//
// This is a class test and not a bug fix, for the reason pit stop 3 gave: a fixed instance
// comes back under another name, and a fixed CLASS cannot.

// prMergeAllowlistPath is the shrink-only exception list, in testdata so a reader sees the
// whole exception set without reading the test. Each entry is `<path>:<func>` and is
// checked in BOTH directions: an unlisted call is a red run, and a listed entry whose call
// has left is a stale entry and also a red run.
const prMergeAllowlistPath = "testdata/prmerge_allowlist.txt"

// TestNoGhPrMergeSpellingInTheToolsGo walks every non-test .go file under cmd/ and
// internal/ and refuses a `pr merge` argument list or an `--auto` flag.
//
// It reads STRING LITERALS IN SOURCE ORDER per function, which is how these argument lists
// are written -- `h.gh("pr", "merge", ...)`, `[]string{"pr", "merge", ...}` -- so it sees a
// command built in a slice as well as one passed inline. A sentence in a refusal message
// that happens to mention the spelling is one literal and is not an argument list, which is
// why the walk looks for the two adjacent words rather than for the phrase.
func TestNoGhPrMergeSpellingInTheToolsGo(t *testing.T) {
	t.Parallel()

	tree := repoTree(t)
	allow := readAllowlist(t, prMergeAllowlistPath)
	seen := map[string]bool{}
	var violations []string
	files := 0

	for _, dir := range []string{"cmd", "internal"} {
		for _, f := range tree.GoFilesUnder(false, dir) {
			rel := f.Rel
			files++
			if f.ParseErr != nil {
				t.Fatal(f.ParseErr)
			}
			words := stringLiterals(f.AST, tree.FSet)
			for i, w := range words {
				key := rel + ":" + w.fn
				switch {
				case w.value == "pr" && i+1 < len(words) && words[i+1].value == "merge" && words[i+1].fn == w.fn:
					seen[key] = true
					if !allow[key] {
						violations = append(violations, fmt.Sprintf(
							"%s:%d builds a `gh pr merge` call in %s; admission to a merge queue is internal/merge.Enqueuer.Enqueue (the enqueuePullRequest mutation), and a merge of a pull request is a batch that landed -- if this is the audit's --disable-auto, list it in %s with its reason",
							rel, w.line, w.fn, prMergeAllowlistPath))
					}
				case w.value == "--auto" || strings.HasPrefix(w.value, "--auto="):
					seen[key] = true
					if !allow[key] {
						violations = append(violations, fmt.Sprintf(
							"%s:%d carries --auto in %s; auto-merge is not an enqueue, it is a standing instruction the forge executes later with nobody in the room -- four pull requests reached dev that way on 2026-09-18",
							rel, w.line, w.fn))
					}
				}
			}
		}
	}
	if files == 0 {
		t.Fatal("no source files found; this walk was looking in the wrong place and would have passed by checking nothing")
	}
	// The list only shrinks: an entry whose call has left is red, so nobody can widen the
	// exception set and leave it there.
	for key := range allow {
		if !seen[key] {
			violations = append(violations, fmt.Sprintf(
				"%s lists %s, and no `pr merge` or --auto is there any more; delete the stale entry (the list only shrinks)",
				prMergeAllowlistPath, key))
		}
	}
	sort.Strings(violations)
	for _, v := range violations {
		t.Error(v)
	}
}

// TestNoGhPrMergeSpellingUnderDotGithub holds the same rule over the workflows and their
// scripts, which is where the revert-on-red loop used to enable auto-merge on its own
// revert pull request. A comment may still SAY auto-merge -- the rule is about what runs.
func TestNoGhPrMergeSpellingUnderDotGithub(t *testing.T) {
	t.Parallel()

	files := 0
	var violations []string
	for _, f := range repoTree(t).Files {
		if !f.InDir(".github") {
			continue
		}
		files++
		for i, line := range strings.Split(string(f.Src), "\n") {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" || strings.HasPrefix(trimmed, "#") {
				continue
			}
			if strings.Contains(line, "gh pr merge") {
				violations = append(violations, fmt.Sprintf(
					"%s:%d runs `gh pr merge`: %s\nCI lands nothing on its own; a batch does, through nova-merge land",
					f.Rel, i+1, trimmed))
				continue
			}
			for _, field := range strings.Fields(line) {
				if field == "--auto" || strings.HasPrefix(field, "--auto=") {
					violations = append(violations, fmt.Sprintf(
						"%s:%d carries --auto: %s\nauto-merge is a standing instruction nobody is in the room for",
						f.Rel, i+1, trimmed))
					break
				}
			}
		}
	}
	if files == 0 {
		t.Fatal("no files found under .github; this walk was looking in the wrong place")
	}
	sort.Strings(violations)
	for _, v := range violations {
		t.Error(v)
	}
}

// literalWord is one string literal, its line, and the function it is written in.
type literalWord struct {
	value string
	line  int
	fn    string
}

// stringLiterals reads a file's string literals IN SOURCE ORDER, each tagged with the
// function that holds it -- or "" for one at package level, so a command built in a package
// var is read as one list too.
func stringLiterals(file *ast.File, fset *token.FileSet) []literalWord {
	var words []literalWord
	var fns []*ast.FuncDecl
	for _, decl := range file.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok {
			fns = append(fns, fn)
		}
	}
	ast.Inspect(file, func(n ast.Node) bool {
		lit, ok := n.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		value, err := strconv.Unquote(lit.Value)
		if err != nil {
			return true
		}
		name := ""
		for _, fn := range fns {
			if fn.Pos() <= lit.Pos() && lit.Pos() <= fn.End() {
				name = funcKey(fn)
				break
			}
		}
		words = append(words, literalWord{value: value, line: fset.Position(lit.Pos()).Line, fn: name})
		return true
	})
	sort.SliceStable(words, func(i, j int) bool { return words[i].line < words[j].line })
	return words
}

// funcKey is the allowlist's function key: the method name qualified by its receiver when
// there is one, so a name that reads the same on two types is still two entries.
func funcKey(fn *ast.FuncDecl) string {
	if fn.Recv != nil && len(fn.Recv.List) > 0 {
		return receiverName(fn.Recv.List[0].Type) + "." + fn.Name.Name
	}
	return fn.Name.Name
}

// readAllowlist reads a shrink-only exception list: one `<path>:<func>` per line, blank
// lines and # comments ignored, and a trailing ` # reason` stripped.
func readAllowlist(t *testing.T, path string) map[string]bool {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	allow := map[string]bool{}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if i := strings.Index(line, " #"); i >= 0 {
			line = strings.TrimSpace(line[:i])
		}
		allow[line] = true
	}
	return allow
}
