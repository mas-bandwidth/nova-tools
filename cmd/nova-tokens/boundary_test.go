package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"go/ast"
	"go/token"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/testbin"
	"github.com/mas-bandwidth/nova-tools/internal/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// THE PUBLICATION BOUNDARY, over the whole binary.
//
// SPEC-TOKENS states it four times, and every statement is about THE TOOL: rule 16, "the
// tool never runs git"; rule 19, "No other subprocess exists ... the tool runs no git at
// all and rule 16 holds without exception"; rule 9, "The tool removes nothing"; and, under
// what it deliberately does not do, "It does not pull the bus, fetch, push, run git, or
// talk to a network. It reads a checkout as files." These tests hold the whole binary to it.

// binaryPackages returns every first-party package reachable from cmd/nova-tokens.
//
// It walks imports rather than naming directories, so a package added anywhere in the
// binary's graph is covered the day it is added, with nobody remembering to widen a list.
func binaryPackages(t *testing.T) []string {
	t.Helper()
	const mod = "github.com/mas-bandwidth/nova-tools/"
	seen := map[string]bool{}
	var walk func(pkg string)
	walk = func(pkg string) {
		if seen[pkg] {
			return
		}
		seen[pkg] = true
		for _, f := range pkgFiles(t, pkg) {
			for _, imp := range f.Imports {
				if path := strings.Trim(imp.Path.Value, `"`); strings.HasPrefix(path, mod) {
					walk(strings.TrimPrefix(path, mod))
				}
			}
		}
	}
	walk("cmd/nova-tokens")
	// Plus every directory of source under the two this tool owns: a package no import
	// reaches yet is still source that ships in this repository, and the point of the walk
	// is that a publisher cannot hide in a directory.
	root := repoRoot(t)
	for _, owned := range []string{"internal/tokens", "cmd/nova-tokens"} {
		require.NoError(t, filepath.WalkDir(filepath.Join(root, owned), func(path string, d fs.DirEntry, err error) error {
			switch {
			case err != nil:
				return err
			case d.IsDir() && d.Name() == "testdata":
				return fs.SkipDir
			case !d.IsDir() && isSource(d.Name()):
				rel, err := filepath.Rel(root, filepath.Dir(path))
				walk(filepath.ToSlash(rel))
				return err
			}
			return nil
		}))
	}
	order := slices.Sorted(maps.Keys(seen))
	// The floor: the five packages of the binary's own graph. Fewer than that and the walk
	// found nothing and this test would pass by checking nothing.
	for _, must := range []string{"cmd/nova-tokens", "internal/tokens", "internal/oneline", "internal/bounded", "internal/buildinfo"} {
		require.Contains(t, order, must, "the import walk did not reach %s; it was looking in the wrong place and would have passed by checking nothing", must)
	}
	return order
}

func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	require.NoError(t, err)
	return root
}

// spawners are the ways a Go program can start another program. exec.Command is the ordinary
// one; the rest are the floor beneath it, and a tripwire that knows only the first is a
// tripwire that a publisher can walk around without hiding: an os.StartProcess with a built
// path passes a tripwire that knows only exec.Command.
//
// syscall's IMPORT is not forbidden: internal/tokens/lock_unix.go needs syscall.Flock for the
// fold lock (rule 8). The call names are.
var spawners = []string{
	"exec.Command", "exec.CommandContext",
	"os.StartProcess", "syscall.ForkExec", "syscall.Exec", "syscall.StartProcess",
}

// namesGit reports whether a string literal names the git PROGRAM: git, /usr/bin/git,
// "git push", C:\bin\git.exe. Three things it deliberately does not flag, each measured on
// this tree:
//
//   - an import path -- "git" sits inside github.com, which every file here imports;
//   - the word digits -- three refusal messages say "64 lowercase hex digits";
//   - PROSE about git. cmd/nova-tokens/main.go's usage banner tells a person that two notes
//     are not ordered by "the directory listing, not the git history", which is the tool
//     saying out loud that it runs none. Flagging that would make the honest tree red, and a
//     tripwire that cries wolf gets deleted rather than obeyed.
//
// So the test is not "does this string contain git" but "does this string look like a program
// to run": the literal IS git, or it begins a command line with it, or one of its words is a
// path (it carries a separator) with git as a component. A sentence with git in the middle of
// it is prose, and the behavioural test is what stands behind this one.
func namesGit(lit string) bool {
	lower := strings.ToLower(strings.TrimSpace(lit))
	isGit := func(tok string) bool { return strings.TrimSuffix(tok, ".exe") == "git" }
	if isGit(lower) || strings.HasPrefix(lower, "git ") {
		return true
	}
	for _, word := range strings.Fields(lower) {
		word = strings.Trim(word, `"'()[]{},;:=`+"`")
		if !strings.ContainsAny(word, `/\`) {
			continue
		}
		for _, comp := range strings.FieldsFunc(word, func(r rune) bool { return r == '/' || r == '\\' }) {
			if isGit(comp) {
				return true
			}
		}
	}
	return false
}

// Rules 16 and 19, over every package of the binary rather than one of them, and demanded
// tests 6 and 16 (once TestTheOnlySubprocessIsSqlite3AndThereIsNoNetwork, over
// internal/tokens alone): the one subprocess is sqlite3 and it lives in
// internal/tokens/opencode.go; there is no network; and no source file of this tool spells
// git (an earlier draft ran `git log` to order competing notes, and that order is now in the
// notes themselves).
//
// WHAT THIS CANNOT SEE: it checks source TEXT and string literals, so a program name
// assembled at run time ("/usr/bin/" + "gi" + "t", or bytes from a file) passes it. The
// behavioural half, TestNoVerbTouchesACheckoutOrItsRemote, measures the remote and the
// checkout instead, whatever the path was spelled like, but only for the verbs it runs: a
// determined author who hides the name from the source is caught there and not here.
func TestNoPackageOfThisBinaryTalksToANetworkOrRunsGit(t *testing.T) {
	t.Parallel()

	const theOneSubprocess = "internal/tokens/opencode.go"
	checked, literals := 0, 0
	for _, pkg := range binaryPackages(t) {
		for name, body := range pkgText(t, pkg) {
			path := pkg + "/" + name
			assert.NotContains(t, body, `"git"`, "%s names git; the tool runs none (rule 16)", path)
			if path == theOneSubprocess {
				continue
			}
			for _, spawn := range spawners {
				assert.NotContains(t, body, spawn, "%s calls %s; this tool starts one subprocess, sqlite3, and it lives in %s (rule 19)", path, spawn, theOneSubprocess)
			}
		}
		for name, f := range pkgFiles(t, pkg) {
			path := pkg + "/" + name
			checked++
			imports := map[*ast.BasicLit]bool{}
			for _, imp := range f.Imports {
				imports[imp.Path] = true
				ip := strings.Trim(imp.Path.Value, `"`)
				assert.NotEqual(t, "net", ip, "%s imports %q; this tool talks to no network, and a publisher is a separate spec gate (rule 16)", path, ip)
				assert.False(t, strings.HasPrefix(ip, "net/"), "%s imports %q; this tool talks to no network, and a publisher is a separate spec gate (rule 16)", path, ip)
				if ip == "os/exec" {
					assert.Equal(t, theOneSubprocess, path, "%s imports os/exec; the one subprocess is sqlite3 and it lives in %s (rule 19)", path, theOneSubprocess)
				}
			}
			// The literals, in the syntax tree rather than the text, so an import path is an
			// import path and not a string that happens to hold a program name.
			ast.Inspect(f, func(n ast.Node) bool {
				if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING && !imports[lit] {
					literals++
					text, err := strconv.Unquote(lit.Value)
					if err != nil {
						text = lit.Value
					}
					assert.False(t, namesGit(text), "%s spells git in a string literal %q; the tool runs none -- it reads a checkout as files (rule 16)", path, text)
				}
				return true
			})
		}
	}
	require.GreaterOrEqual(t, checked, 10, "examined %d source files; this tripwire was looking in the wrong place and would have passed by checking almost nothing", checked)
	require.GreaterOrEqual(t, literals, 50, "examined %d string literals; the literal half was looking in the wrong place and would have passed by checking almost nothing", literals)
}

// namesGit's own test: the tripwire above is only as good as this function, and the two
// false positives it must not have are in every file of this repository.
func TestNamesGitKnowsAProgramNameFromASubstring(t *testing.T) {
	t.Parallel()

	for _, yes := range []string{"git", "GIT", "git.exe", "/usr/bin/git", "git push", `C:\bin\git.exe`, "git -C x push", "/opt/homebrew/bin/git", "  git  "} {
		assert.True(t, namesGit(yes), "namesGit(%q) is false; that is a program to run", yes)
	}
	for _, no := range []string{
		"github.com/mas-bandwidth/nova-tools/internal/oneline",
		"an ID is sha256: and 64 lowercase hex digits",
		"a \\u escape is not four hex digits",
		"digits", "legitimate", "gitignore", "", "gi t", "(git)",
		// The banner's own sentence, which is the tool saying it runs no git -- and the
		// banner as one literal, paths and all, which is the shape that actually reaches
		// the tripwire and the one that caught this predicate's first two drafts.
		"not the Date, not the filename, not the directory listing, not the git history",
		"usage:\n  nova-tokens fold --out ./out --repos ./repos.tsv\n\nnot the git history\n",
	} {
		assert.False(t, namesGit(no), "namesGit(%q) is true; a tripwire that cries wolf gets deleted", no)
	}
}

// Rule 16 and demanded test 16's behavioural half, widened from one verb to every verb and
// from a bare checkout to one with a REMOTE.
//
// The fixture is a real git repository holding the bus lane, with `origin` set to a bare
// repository beside it -- everything a push would need and nothing it may use. A fake git
// on PATH records any invocation. Then every verb runs, and afterwards: the fake was never
// called, the bare repository is byte-identical, and the checkout's own .git is too.
func TestNoVerbTouchesACheckoutOrItsRemote(t *testing.T) {
	realGit, _ := exec.LookPath("git")
	if realGit == "" {
		t.Skip("the fixture wants a real git to build the checkout")
	}
	testkit.SkipOn(t, "windows", "the fake git is a shell script")
	b := newBench(t)
	b.transcript("a.jsonl", msg("m1", "2026-09-11T10:00:00Z", "fable", map[string]int{"input_tokens": 100}, "/x/schema/a.go"))
	bus := busDir(t, filepath.Join(b.dir, "bus"), "emma")
	busNote(t, bus, "emma", "n.md", "emma-00000000000a", "tokens 2026-09-11", busDate, "2026-09-11\temma\tg\tschema\tinput\t250\n")

	// A bare repository is the fake remote: a push has somewhere to go, and nothing here
	// may go there. It is built with the real git, before the fake goes on PATH.
	bare := filepath.Join(b.dir, "remote.git")
	gitRun(t, realGit, b.dir, "init", "--bare", "-q", bare)
	for _, kv := range [][2]string{{"receive.autogc", "false"}, {"gc.auto", "0"}, {"maintenance.auto", "false"}} {
		gitRun(t, realGit, bare, "config", kv[0], kv[1])
	}
	for _, args := range [][]string{{"init", "-q"}, {"add", "-A"}, {"commit", "-q", "-m", "the lane"}, {"remote", "add", "origin", bare}, {"push", "-q", "origin", "HEAD:refs/heads/main"}} {
		gitRun(t, realGit, bus, args...)
	}
	gitLog := fakeGit(t)
	beforeBare, beforeGit := readTree(t, bare), readTree(t, filepath.Join(bus, ".git"))

	// Every verb, including the two that only look and the one that only says which build;
	// each is a run the tool can complete.
	for _, args := range [][]string{
		{"fold", "--out", b.out, "--all", "--repos", b.repos, "--claude", "glenn=" + b.tr, "--bus", bus},
		{"sources", "--repos", b.repos, "--all", "--claude", "glenn=" + b.tr, "--bus", bus},
		{"report", "--who", "rowan", "--day", "2026-09-11", "--repos", b.repos, "--claude", "glenn=" + b.tr},
		{"sum", "--out", b.out, "--month", "2026-09"},
		{"check", "--out", b.out},
		{"version"},
		{"help"},
	} {
		r := novaTokens.Do(t, args...)
		assert.LessOrEqual(t, r.Code, 1, r)
	}
	assert.NoFileExists(t, gitLog, "git was invoked")
	assert.Equal(t, beforeBare, readTree(t, bare), "the remote changed; this tool does not push, fetch or talk to a network (rule 16)")
	assert.Equal(t, beforeGit, readTree(t, filepath.Join(bus, ".git")), "the checkout's .git changed; the bus is read as files and nothing else (rule 16)")
}

// gitRunErr runs git the way the fixture does -- no global or system config, a fixed
// clock, and background maintenance/auto-gc turned off -- and returns an error rather than
// failing the test, so a test can run several of them in parallel goroutines.
func gitRunErr(git, dir string, args ...string) error {
	cmd := exec.Command(git, append([]string{
		"-c", "user.name=Fixture", "-c", "user.email=fixture@example.com",
		"-c", "commit.gpgsign=false", "-c", "init.defaultBranch=main",
		"-c", "receive.autogc=false", "-c", "gc.auto=0", "-c", "gc.autoDetach=false",
		"-c", "maintenance.auto=false", "-c", "maintenance.autoDetach=false",
		"-C", dir,
	}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_DATE=2026-09-11T20:00:00Z", "GIT_COMMITTER_DATE=2026-09-11T20:00:00Z")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git %v: %v: %s", args, err, out)
	}
	return nil
}

// fakeGit puts a git on PATH that records every call into the file it returns. One helper
// for its two callers: the package's t.Setenv sites (testdata/testify) only fall.
func fakeGit(t *testing.T) (logPath string) {
	t.Helper()
	testkit.SkipOn(t, "windows", "the fake git is a shell script")
	bin := testkit.Mkdir(t, filepath.Join(t.TempDir(), "bin"))
	logPath = filepath.Join(filepath.Dir(bin), "git-argv.log")
	require.NoError(t, testbin.WriteExecutable(filepath.Join(bin, "git"), []byte("#!/bin/sh\necho \"$@\" >> "+logPath+"\n"), 0o755))
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return logPath
}

func gitRun(t *testing.T, git, dir string, args ...string) {
	t.Helper()
	require.NoError(t, gitRunErr(git, dir, args...))
}

// readTree is every file under root, by relative path, as the sha256 of its bytes: two
// snapshots compared with assert.Equal name the files added, removed and changed.
func readTree(t *testing.T, root string) map[string]string {
	t.Helper()
	files := map[string]string{}
	require.NoError(t, filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(root, path)
		h := sha256.Sum256([]byte(testkit.ReadFile(t, path)))
		files[filepath.ToSlash(rel)] = hex.EncodeToString(h[:])
		return err
	}))
	require.NotEmpty(t, files, "nothing under %s; this comparison would hold whatever happened", root)
	return files
}
