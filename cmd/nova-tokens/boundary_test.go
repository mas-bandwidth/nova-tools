package main

import (
	"crypto/sha256"
	"encoding/hex"
	"go/ast"
	"go/token"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// THE PUBLICATION BOUNDARY, over the whole binary.
//
// SPEC-TOKENS states it four times, and every statement is about THE TOOL: rule 16, "the
// tool never runs git"; rule 19, "No other subprocess exists ... the tool runs no git at
// all and rule 16 holds without exception"; rule 9, "The tool removes nothing"; and, under
// what it deliberately does not do, "It does not pull the bus, fetch, push, run git, or
// talk to a network. It reads a checkout as files."
//
// What was enforced was narrower than what was stated, in three places:
//
//  1. TestTheOnlySubprocessIsSqlite3AndThereIsNoNetwork reads internal/tokens and nothing
//     else. cmd/nova-tokens/main.go could import net/http, import os/exec, or name "git",
//     and every test in this repository stayed green.
//  2. Its reader and rule 9's skip directory entries, so both stop at a package's top
//     level. A publisher landing in internal/tokens/publish -- the path the format packet's
//     `records publish` verb would want -- could import net, run git, and call
//     os.RemoveAll, and the suite would be green.
//  3. The no-git BEHAVIOUR was asserted for one verb, `fold --bus`, over a checkout with no
//     remote. Nothing pinned report, sources, sum, check or version, and nothing pinned a
//     checkout that HAS a remote, which is the only kind a push could reach.
//
// That gap matters more than an ordinary hole, because publication is under review right
// now: docs/PROPOSAL-TOKENS-FORMAT.md proposes `nova-tokens records publish --batch <dir>
// --ledger <git-checkout> --remote <name> --branch <name>`, and says of itself "not shipped
// behavior"; PR #124, which merged it, says "the publication interface remain[s an]
// explicit spec gate[]". So the next hand in this file may be holding a git publisher, and
// the tests decide whether v1's read-only promise is a wall or a sentence in a document.
// These are the wall.

// binaryPackages returns every first-party package reachable from cmd/nova-tokens, plus the
// records core, which nothing imports yet and which is where a publisher would grow.
//
// It walks imports rather than naming directories, so a package added anywhere in the
// binary's graph is covered the day it is added, with nobody remembering to widen a list.
func binaryPackages(t *testing.T) []string {
	t.Helper()
	const mod = "github.com/mas-bandwidth/nova-tools/"
	seen := map[string]bool{}
	var order []string
	var walk func(pkg string)
	walk = func(pkg string) {
		if seen[pkg] {
			return
		}
		seen[pkg] = true
		order = append(order, pkg)
		for _, f := range pkgFiles(t, pkg) {
			for _, imp := range f.Imports {
				path := strings.Trim(imp.Path.Value, `"`)
				if strings.HasPrefix(path, mod) {
					walk(strings.TrimPrefix(path, mod))
				}
			}
		}
	}
	walk("cmd/nova-tokens")
	walk("internal/records")
	// Plus every subpackage under the two this tool owns: a package no import reaches yet
	// is still source that ships in this repository, and the point of the walk is that a
	// publisher cannot hide in a directory.
	root := repoRoot(t)
	for _, owned := range []string{"internal/tokens", "cmd/nova-tokens", "internal/records"} {
		err := filepath.WalkDir(filepath.Join(root, owned), func(path string, d fs.DirEntry, err error) error {
			if err != nil || !d.IsDir() {
				return err
			}
			rel, rerr := filepath.Rel(root, path)
			if rerr != nil {
				return rerr
			}
			rel = filepath.ToSlash(rel)
			if base := filepath.Base(rel); base == "testdata" {
				return fs.SkipDir
			}
			if hasGoFiles(t, filepath.Join(root, rel)) {
				walk(rel)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	sort.Strings(order)
	// The floor: the five packages of the binary's own graph plus the records core. Fewer
	// than that and the walk found nothing and this test would pass by checking nothing.
	for _, must := range []string{
		"cmd/nova-tokens", "internal/tokens", "internal/oneline",
		"internal/bounded", "internal/buildinfo", "internal/records",
	} {
		if !seen[must] {
			t.Fatalf("the import walk did not reach %s; it was looking in the wrong place and would have passed by checking nothing (found %v)", must, order)
		}
	}
	return order
}

func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func hasGoFiles(t *testing.T, dir string) bool {
	t.Helper()
	ents, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range ents {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".go") && !strings.HasSuffix(e.Name(), "_test.go") {
			return true
		}
	}
	return false
}

// spawners are the ways a Go program can start another program. exec.Command is the ordinary
// one; the rest are the floor beneath it, and a tripwire that knows only the first is a
// tripwire that a publisher can walk around without hiding (#146's whole-PR read planted an
// os.StartProcess with a built path and both tripwires passed).
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
// to run": the literal IS git, or it carries git as a path component, or it begins a command
// line with it, or it is a short token-shaped string. A sentence with git in the middle of it
// is prose, and the behavioural test is what stands behind this one.
func namesGit(lit string) bool {
	trimmed := strings.TrimSpace(lit)
	lower := strings.ToLower(trimmed)
	isGit := func(tok string) bool { return strings.TrimSuffix(tok, ".exe") == "git" }
	// The literal IS the program, or it begins a command line with it.
	if isGit(lower) || strings.HasPrefix(lower, "git ") {
		return true
	}
	// Or one whitespace-separated word is a PATH whose last-or-any component is git. The word
	// has to carry the separator itself: "/usr/bin/git" is a path, and the bare word git in
	// the middle of a sentence is prose -- cmd/nova-tokens/main.go's banner has one, and it is
	// the tool telling a person it does not read the git history.
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

// Rules 16 and 19, over every package of the binary rather than one of them. The one
// subprocess is sqlite3 and it lives in internal/tokens/opencode.go; there is no network; and
// no source file of this tool spells git.
//
// WHAT THIS CANNOT SEE, stated so nobody reads it as more than it is: it checks source TEXT
// and string literals, so a program name assembled at run time -- "/usr/bin/" + "gi" + "t",
// or bytes from a file -- passes it. That is not a hole a publisher could land in by
// accident, but it is not proof either. The behavioural half is
// TestNoVerbTouchesACheckoutOrItsRemote, which measures the remote and the checkout rather
// than the source; it catches a real push whatever the path was spelled like, and it catches
// it only for the verbs it runs. Between them: a publisher cannot be written here in the
// ordinary way, and cannot push to a fixture remote under any verb, and a determined author
// who hides the name from the source is caught by the second and not the first.
func TestNoPackageOfThisBinaryTalksToANetworkOrRunsGit(t *testing.T) {
	const theOneSubprocess = "internal/tokens/opencode.go"
	checked := 0
	literals := 0
	for _, pkg := range binaryPackages(t) {
		for name, f := range pkgFiles(t, pkg) {
			path := pkg + "/" + name
			checked++
			for _, imp := range f.Imports {
				ip := strings.Trim(imp.Path.Value, `"`)
				if ip == "net" || strings.HasPrefix(ip, "net/") {
					t.Errorf("%s imports %q; this tool talks to no network, and a publisher is a separate spec gate (rule 16)", path, ip)
				}
				if ip == "os/exec" && path != theOneSubprocess {
					t.Errorf("%s imports os/exec; the one subprocess is sqlite3 and it lives in %s (rule 19)", path, theOneSubprocess)
				}
			}
		}
		for name, body := range pkgText(t, pkg) {
			path := pkg + "/" + name
			if path == theOneSubprocess {
				continue
			}
			for _, spawn := range spawners {
				if strings.Contains(body, spawn) {
					t.Errorf("%s calls %s; this tool starts one subprocess, sqlite3, and it lives in %s (rule 19)", path, spawn, theOneSubprocess)
				}
			}
		}
		// And the literals, in the syntax tree rather than the text, so an import path is an
		// import path and not a string that happens to hold a program name.
		for name, f := range pkgFiles(t, pkg) {
			path := pkg + "/" + name
			imports := map[*ast.BasicLit]bool{}
			for _, imp := range f.Imports {
				imports[imp.Path] = true
			}
			ast.Inspect(f, func(n ast.Node) bool {
				lit, ok := n.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING || imports[lit] {
					return true
				}
				literals++
				text, err := strconv.Unquote(lit.Value)
				if err != nil {
					text = lit.Value
				}
				if namesGit(text) {
					t.Errorf("%s spells git in a string literal; the tool runs none -- it reads a checkout as files (rule 16)", path)
				}
				return true
			})
		}
	}
	if checked < 10 {
		t.Fatalf("examined %d source files; this tripwire was looking in the wrong place and would have passed by checking almost nothing", checked)
	}
	if literals < 50 {
		t.Fatalf("examined %d string literals; the literal half was looking in the wrong place and would have passed by checking almost nothing", literals)
	}
}

// namesGit's own test: the tripwire above is only as good as this function, and the two
// false positives it must not have are in every file of this repository.
func TestNamesGitKnowsAProgramNameFromASubstring(t *testing.T) {
	for _, yes := range []string{
		"git", "GIT", "git.exe", "/usr/bin/git", "git push", `C:\bin\git.exe`,
		"git -C x push", "/opt/homebrew/bin/git", "  git  ",
	} {
		if !namesGit(yes) {
			t.Errorf("namesGit(%q) is false; that is a program to run", yes)
		}
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
		if namesGit(no) {
			t.Errorf("namesGit(%q) is true; a tripwire that cries wolf gets deleted", no)
		}
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
	if realGit == "" || runtime.GOOS == "windows" {
		t.Skip("the fixture wants a real git to build the checkout and a shell script for the fake")
	}
	dir := t.TempDir()
	repos := reposFile(t, dir)
	out := mkdir(t, filepath.Join(dir, "out"))
	tr := mkdir(t, filepath.Join(dir, "tr"))
	write(t, filepath.Join(tr, "a.jsonl"), msg("m1", "2026-09-11T10:00:00Z", "fable", map[string]int{"input_tokens": 100}, "/x/schema/a.go")+"\n")
	bus := busDir(t, mkdir(t, filepath.Join(dir, "bus")), "emma")
	busNote(t, bus, "emma", "n.md", "emma-00000000000a", "tokens 2026-09-11", busDate,
		"2026-09-11\temma\tg\tschema\tinput\t250\n")

	// A bare repository is the fake remote: a push has somewhere to go, and nothing here
	// may go there. It is built with the real git, before the fake goes on PATH.
	bare := filepath.Join(dir, "remote.git")
	gitRun(t, realGit, dir, "init", "--bare", "-q", bare)
	gitRun(t, realGit, bus, "init", "-q")
	gitRun(t, realGit, bus, "add", "-A")
	gitRun(t, realGit, bus, "commit", "-q", "-m", "the lane")
	gitRun(t, realGit, bus, "remote", "add", "origin", bare)
	gitRun(t, realGit, bus, "push", "-q", "origin", "HEAD:refs/heads/main")

	gitLog := fakeGit(t)
	before := treeDigest(t, bare)
	beforeGit := treeDigest(t, filepath.Join(bus, ".git"))

	// Every verb, including the two that only look and the one that only says which build.
	runs := [][]string{
		{"fold", "--out", out, "--all", "--repos", repos, "--claude", "glenn=" + tr, "--bus", bus},
		{"sources", "--repos", repos, "--all", "--claude", "glenn=" + tr, "--bus", bus},
		{"report", "--who", "rowan", "--day", "2026-09-11", "--repos", repos, "--claude", "glenn=" + tr},
		{"sum", "--out", out, "--month", "2026-09"},
		{"check", "--out", out},
		{"version"},
		{"help"},
	}
	for _, args := range runs {
		r := invoke(t, args...)
		if r.exit > 1 {
			t.Errorf("%v exits %d; the fixture is meant to be a run the tool can complete\n%s", args, r.exit, r.all())
		}
	}

	if _, err := os.Stat(gitLog); err == nil {
		t.Errorf("git was invoked: %s", read(t, gitLog))
	}
	if after := treeDigest(t, bare); after != before {
		t.Error("the remote changed; this tool does not push, fetch or talk to a network (rule 16)")
	}
	if after := treeDigest(t, filepath.Join(bus, ".git")); after != beforeGit {
		t.Error("the checkout's .git changed; the bus is read as files and nothing else (rule 16)")
	}
}

func gitRun(t *testing.T, git, dir string, args ...string) {
	t.Helper()
	full := append([]string{
		"-c", "user.name=Fixture", "-c", "user.email=fixture@example.com",
		"-c", "commit.gpgsign=false", "-c", "init.defaultBranch=main", "-C", dir,
	}, args...)
	cmd := exec.Command(git, full...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_AUTHOR_DATE=2026-09-11T20:00:00Z", "GIT_COMMITTER_DATE=2026-09-11T20:00:00Z")
	if outb, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, outb)
	}
}

// treeDigest is one hash over every file under root: its relative path, its size and its
// bytes. A changed, added or removed file all move it, so one comparison says whether
// anything under a directory was touched.
func treeDigest(t *testing.T, root string) string {
	t.Helper()
	sum := sha256.New()
	var names []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, rerr := filepath.Rel(root, path)
		if rerr != nil {
			return rerr
		}
		names = append(names, rel)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(names)
	for _, rel := range names {
		raw, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			// A file git holds open or replaces under us is named, not skipped.
			t.Fatalf("%s: %v", rel, err)
		}
		sum.Write([]byte(rel))
		sum.Write([]byte{0})
		sum.Write(raw)
		sum.Write([]byte{0})
	}
	if len(names) == 0 {
		t.Fatalf("nothing under %s; this comparison would hold whatever happened", root)
	}
	return hex.EncodeToString(sum.Sum(nil))
}

// The records namespace is not a verb, and that is the current answer rather than an
// oversight. docs/PROPOSAL-TOKENS-FORMAT.md proposes `records collect|check|view|publish`
// and calls itself "not shipped behavior"; PR #124's merge says the publication interface
// remains an explicit spec gate; SPEC-TOKENS' own verb list has five verbs and none of
// them writes anywhere but the day file.
//
// So this tool refuses it as an unknown subcommand, exit 2, and writes nothing. The test is
// here so that the day somebody implements it, they change this line deliberately and the
// spec in the same hand -- rather than discovering afterwards that a verb which pushes to a
// git remote landed in a tool whose spec says it runs no git.
func TestTheRecordsNamespaceIsNotAVerbUntilItsGateIsDecided(t *testing.T) {
	dir := t.TempDir()
	out := mkdir(t, filepath.Join(dir, "out"))
	for _, args := range [][]string{
		{"records"},
		{"records", "collect", "--sources", "x", "--ledger", out, "--out", dir},
		{"records", "publish", "--batch", dir, "--ledger", out, "--remote", "origin", "--branch", "main"},
	} {
		r := invoke(t, args...)
		wantExit(t, r, 2)
		wantContains(t, r.stderr, `unknown subcommand "records"`)
		wantContains(t, r.stderr, "run: nova-tokens help")
	}
	ents, err := os.ReadDir(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(ents) != 0 {
		t.Errorf("a refused verb wrote %d entries under --out", len(ents))
	}
	// And the banner does not advertise it: a usage block naming a verb the tool refuses
	// is a first run that fails on its own instructions.
	if strings.Contains(usage, "records") {
		t.Error("the usage banner names a records verb this tool refuses")
	}
}
