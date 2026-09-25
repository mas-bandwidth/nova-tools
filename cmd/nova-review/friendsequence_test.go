package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/goenv"
	"github.com/mas-bandwidth/nova-tools/internal/merge"
)

// THE SEQUENCE A FRIEND RUNS TO READ A PR, with the lane made by the tool that makes
// lanes rather than by a hand-written state.json: nova-merge init against a local bare
// remote, nova-merge add --pr, then nova-review packet. Every other packet test in this
// package writes merge.State itself, so none of them would notice a lane that init leaves
// in a shape packet cannot read -- which is the class of breakage the whole sequence is
// here for.
//
// The two properties it holds, and both were dogfood findings rather than ideas:
//
//   - PACKET OK files=1 for a one-file PR. #418: the lane's clone goes stale while origin
//     advances, and a range built on the idle local base counted every file that moved in
//     between -- 130 KB of packet for a 12-line PR.
//   - the base is fetched BEFORE the range is built. It is asserted the only way a caller
//     can see it: the base tip here exists ONLY on the GitHub remote and never in the lane
//     clone, so a range built without that fetch cannot resolve its left side at all.
//
// The PR's head lives in a second bare repository standing in for GitHub, reached through
// git's url.<local>.insteadOf, as the #449 and #493 tests reach it.
//
// (It replaces a namesake added by #490 that ran no lane verb, took packet's --head
// bypass and asserted only that a file was written; the bypass itself is covered by
// TestPacketHeadBypassBuildsWhenTheEntryHeadFetchFails.)
func TestFriendSequenceLaneAddPacket(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on this machine; this sequence drives a real git against bare fixture repositories")
	}
	dir := t.TempDir()
	laneRemote := filepath.Join(dir, "lane-remote.git")
	ghRemote := filepath.Join(dir, "gh-remote.git")
	seed := filepath.Join(dir, "seed")
	if e := os.MkdirAll(seed, 0o755); e != nil {
		t.Fatal(e)
	}
	gitAt := func(d string, args ...string) string {
		t.Helper()
		c := exec.Command("git", args...)
		c.Dir = d
		c.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=fixture", "GIT_AUTHOR_EMAIL=fixture@example.invalid",
			"GIT_COMMITTER_NAME=fixture", "GIT_COMMITTER_EMAIL=fixture@example.invalid")
		b, e := c.CombinedOutput()
		if e != nil {
			t.Fatalf("git %v: %v %s", args, e, b)
		}
		return strings.TrimSpace(string(b))
	}
	gitAt(dir, "init", "-q", "--bare", laneRemote)
	gitAt(dir, "init", "-q", "--bare", ghRemote)
	gitAt(laneRemote, "symbolic-ref", "HEAD", "refs/heads/main")
	gitAt(ghRemote, "symbolic-ref", "HEAD", "refs/heads/main")

	gitAt(seed, "init", "-q", "-b", "main")
	gitAt(seed, "config", "user.email", "fixture@example.invalid")
	gitAt(seed, "config", "user.name", "fixture")
	if e := os.WriteFile(filepath.Join(seed, "a.txt"), []byte("base\n"), 0o644); e != nil {
		t.Fatal(e)
	}
	gitAt(seed, "add", "a.txt")
	gitAt(seed, "commit", "-qm", "base")
	gitAt(seed, "push", "-q", laneRemote, "main:refs/heads/main")
	gitAt(seed, "push", "-q", ghRemote, "main:refs/heads/main")

	// nova-merge init: the lane, and its clone of the repository, made by the tool.
	novaMerge := buildNovaMerge(t, dir)
	lane := filepath.Join(dir, "lane")
	runMerge(t, novaMerge, "init", "--lane", lane, "--repo", "test/repo",
		"--base", "main", "--lane-branch", "nova-merge/lane", "--remote", laneRemote)

	repo := filepath.Join(lane, merge.RepoDir)
	// The URL the --repo name resolves to is the second bare repository: the PR head and
	// the current base are fetched from there, never from the lane's own remote.
	gitAt(repo, "config", "url."+ghRemote+".insteadOf", "https://github.com/test/repo.git")

	// AFTER the lane's clone: main moves on at GitHub, touching two files the PR never
	// saw, and the PR branches off the moved main and touches exactly one. The moved tip
	// is pushed ONLY to the GitHub remote, so a packet that does not fetch the base has
	// no commit to build a range from.
	for _, f := range []string{"b.txt", "c.txt"} {
		if e := os.WriteFile(filepath.Join(seed, f), []byte(f+"\n"), 0o644); e != nil {
			t.Fatal(e)
		}
		gitAt(seed, "add", f)
	}
	gitAt(seed, "commit", "-qm", "unrelated base moves")
	gitAt(seed, "push", "-q", ghRemote, "main:refs/heads/main")
	gitAt(seed, "checkout", "-qb", "feature")
	if e := os.WriteFile(filepath.Join(seed, "feat.txt"), []byte("one file\n"), 0o644); e != nil {
		t.Fatal(e)
	}
	gitAt(seed, "add", "feat.txt")
	gitAt(seed, "commit", "-qm", "the pull request")
	head := gitAt(seed, "rev-parse", "HEAD")
	gitAt(seed, "push", "-q", ghRemote, "feature:refs/pull/411/head")

	// nova-merge add: the entry, queued with no head of its own. packet resolves the head
	// by fetching it, which is what a friend reading a fresh PR has.
	runMerge(t, novaMerge, "add", "--lane", lane, "--pr", "411", "--needs-read")

	var out, errb bytes.Buffer
	if code := run([]string{"packet", "--lane", lane, "--pr", "411", "--who", "Rowan",
		"--out", filepath.Join(lane, "packet.md")}, &out, &errb); code != 0 {
		t.Fatalf("packet on a lane made by nova-merge: exit %d\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
	}
	if !strings.Contains(out.String(), "PACKET OK ") || !strings.Contains(out.String(), " files=1 ") {
		t.Fatalf("a one-file PR on a lane whose base has moved must be PACKET OK files=1; got:\n%s", out.String())
	}
	if !strings.Contains(out.String(), " head="+merge.Short(head)) {
		t.Fatalf("the packet is not for the head the entry's pull ref carries (%s):\n%s", merge.Short(head), out.String())
	}
	body, err := os.ReadFile(filepath.Join(lane, "packet.md"))
	if err != nil {
		t.Fatalf("the sequence did not write the packet: %v", err)
	}
	if !strings.Contains(string(body), "feat.txt") {
		t.Fatalf("the packet does not carry the PR's one file:\n%s", body)
	}
	for _, unrelated := range []string{"b.txt", "c.txt"} {
		if strings.Contains(string(body), unrelated) {
			t.Fatalf("the packet carries %s, a file the base moved and the PR never touched: the range was built before the base was fetched (#418):\n%s", unrelated, body)
		}
	}
}

// buildNovaMerge builds the lane tool this sequence starts with. The verbs under test
// belong to another command's package, so the sequence reaches them the way a friend
// does -- as a binary on the command line -- rather than through a hand-written
// state.json, which is what every other test in this package uses and what lets a lane
// init leaves unreadable go unnoticed.
func buildNovaMerge(t *testing.T, dir string) string {
	t.Helper()
	bin := filepath.Join(dir, "nova-merge")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", bin, "github.com/mas-bandwidth/nova-tools/cmd/nova-merge")
	cmd.Env = goenv.Clean(os.Environ())
	if raw, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("building nova-merge: %v\n%s", err, raw)
	}
	return bin
}

func runMerge(t *testing.T, bin string, args ...string) string {
	t.Helper()
	cmd := exec.Command(bin, args...)
	raw, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("nova-merge %s: %v\n%s", strings.Join(args, " "), err, raw)
	}
	return string(raw)
}
