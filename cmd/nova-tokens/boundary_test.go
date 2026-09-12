package main

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
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

// Rules 16 and 19, over every package of the binary rather than one of them. The one
// subprocess is sqlite3 and it lives in internal/tokens/opencode.go; there is no network;
// and the word git appears in no source file of this tool.
func TestNoPackageOfThisBinaryTalksToANetworkOrRunsGit(t *testing.T) {
	const theOneSubprocess = "internal/tokens/opencode.go"
	checked := 0
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
			if strings.Contains(body, "exec.Command") {
				t.Errorf("%s starts a subprocess; there is one, and it is sqlite3 (rule 19)", path)
			}
			if strings.Contains(body, `"git"`) {
				t.Errorf("%s names git; the tool runs none -- it reads a checkout as files (rule 16)", path)
			}
		}
	}
	if checked < 10 {
		t.Fatalf("examined %d source files; this tripwire was looking in the wrong place and would have passed by checking almost nothing", checked)
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
