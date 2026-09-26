//go:build functional

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ctxindex"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/redis/go-redis/v9"
)

const cutBaseSHA = "0123456789abcdef0123456789abcdef01234567"

// fakeIssues is card cut's forge seam in tests: issues by number, one tip.
type fakeIssues struct {
	issues map[int]card.Issue
	tip    string
	tips   int
}

func (f *fakeIssues) Issue(_ context.Context, repo string, n int) (card.Issue, error) {
	is, ok := f.issues[n]
	if !ok || repo != "mas-bandwidth/nova-tools" {
		return card.Issue{}, fmt.Errorf("gh api repos/%s/issues/%d: HTTP 404", repo, n)
	}
	return is, nil
}

func (f *fakeIssues) BranchSHA(_ context.Context, _, _ string) (string, error) {
	f.tips++
	return f.tip, nil
}

// cutFixture is a throwaway Redis with the library loaded, a local mirror so
// the repo probe never leaves the host, and the fake forge in place.
func cutFixture(t *testing.T, issues map[int]card.Issue) (*redis.Client, string, *fakeIssues) {
	t.Helper()
	ctx := context.Background()
	addr := testutil.Start(t)
	client := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = client.Close() })
	if err := fn.Load(ctx, client); err != nil {
		t.Fatal(err)
	}
	mirror := t.TempDir()
	if err := os.MkdirAll(filepath.Join(mirror, "nova-tools.git", "objects"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NOVA_MIRROR_ROOT", mirror)
	src := &fakeIssues{issues: issues, tip: strings.Repeat("ab", 20)}
	old := cardCutSource
	cardCutSource = func(*store.Store) card.IssueSource { return src }
	t.Cleanup(func() { cardCutSource = old })
	return client, addr, src
}

const cutIssueBody = "What: `nova-sprint card cut` writes the card.\r\n\r\n" +
	"DONE-WHEN: `go test ./cmd/nova-sprint/ -run TestCardCutWritesRecord` prints one --- PASS\r\n" +
	"PATHS: cmd/nova-sprint/card.go, internal/nsprint/card/cut.go\r\n" +
	"DEPENDS-ON: none\r\n" +
	"STREAM: swarm: cards\r\n" +
	"BASE: dev\r\n" +
	"base-sha: " + cutBaseSHA + "\r\n" +
	"EST: 45 min\r\n" +
	"INVARIANT: card cut writes one card record per issue.\r\n" +
	"CLASS-TEST: TestCardCutWritesRecord\r\n"

// TestCardCutWritesRecord is nova-tools#3623: `nova-sprint card cut --repo
// <r> --issue <n>` reads the issue and writes the card record into Redis in
// the card-push shape (STREAM, WHO, DEPENDS-ON and WHY, PATHS, DONE-WHEN,
// BASE, base-sha, EST, ORIGIN), its exact bytes at the body's content address
// before the card is published, and no file anywhere. An issue whose
// dependency is another issue waits; one with no base-sha reads the tip.
func TestCardCutWritesRecord(t *testing.T) {
	ctx := context.Background()
	client, addr, src := cutFixture(t, map[int]card.Issue{
		9001: {Title: "card cut writes the record", Body: cutIssueBody},
		9002: {Title: "second card", Body: "DONE-WHEN: the second test fails red\nPATHS: docs/CLI.md\n" +
			"DEPENDS-ON: #9001 (WHY: the cut verb), nova-tools#3502\nSTREAM: swarm: cards\nBASE: dev\n" +
			"INVARIANT: the second card waits for the first.\nCLASS-TEST: TestCardCutWritesRecord\n"},
	})
	dir := t.TempDir()
	t.Chdir(dir)
	code, out, errOut := runSprint("card", "cut", "--redis", addr, "--sprint", "cut-s", "--repo", "mas-bandwidth/nova-tools", "--issue", "9001")
	origin := card.IssueURL("mas-bandwidth/nova-tools", 9001)
	if code != 0 || out != "CARD CUT cut-s/mas-bandwidth/nova-tools#9001 label=nova-tools-9001 place=pool stream=swarm:\\x20cards contexts=0 origin="+origin+"\n" {
		t.Fatalf("exit %d stderr %q out %q", code, errOut, out)
	}
	rec, err := client.HGetAll(ctx, "s:cut-s:card:nova-tools-9001").Result()
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"stream": "swarm: cards", "origin": origin, "where": "ready",
		"base": "dev", "base_sha": cutBaseSHA, "paths": "cmd/nova-sprint/card.go, internal/nsprint/card/cut.go",
		"est": "45", "repo": "mas-bandwidth/nova-tools", "kind": "fix", "task": "card cut writes the record",
		"done_when": "`go test ./cmd/nova-sprint/ -run TestCardCutWritesRecord` prints one --- PASS",
	}
	for k, v := range want {
		if rec[k] != v {
			t.Errorf("card.%s = %q, want %q", k, rec[k], v)
		}
	}
	body, err := client.Get(ctx, card.BodyKey("cut-s", rec["payload_sha"])).Bytes()
	if err != nil {
		t.Fatalf("no body at the payload's address: %v", err)
	}
	if sum := sha256.Sum256(body); hex.EncodeToString(sum[:]) != rec["payload_sha"] {
		t.Fatal("the stored body is not the pushed payload")
	}
	for _, line := range []string{"WHO: any\n", "STREAM: swarm: cards\n",
		"INVARIANT: card cut writes one card record per issue.\n", "CLASS-TEST: TestCardCutWritesRecord\n", "DEPENDS-ON: none\n", "ORIGIN: " + origin + "\n",
		"> What: `nova-sprint card cut` writes the card.\n", "> base-sha: " + cutBaseSHA + "\n"} {
		if !strings.Contains(string(body), line) {
			t.Errorf("card body lacks %q:\n%s", line, body)
		}
	}
	if !strings.HasPrefix(string(body), "RESULT: nova-tools-9001 sha=") || strings.Contains(string(body), "<sha12>") {
		t.Errorf("line 1 is not the sealed contract:\n%s", body)
	}
	if n, _ := client.ZCard(ctx, "ws:swarm: cards:ready").Result(); n != 1 {
		t.Errorf("ws:swarm: cards:ready holds %d, want the one card", n)
	}
	if ents, _ := os.ReadDir(dir); len(ents) != 0 {
		t.Errorf("card cut wrote files: %v", ents)
	}
	if src.tips != 0 {
		t.Errorf("an issue carrying base-sha read the tip %d times", src.tips)
	}
	// Recut of the same issue is the same card.
	if code, out, _ := runSprint("card", "cut", "--redis", addr, "--sprint", "cut-s", "--repo", "mas-bandwidth/nova-tools", "--issue", "9001"); code != 0 || !strings.Contains(out, " place=exists ") {
		t.Fatalf("recut: exit %d out %q, want place=exists", code, out)
	}
	code, out, errOut = runSprint("card", "cut", "--redis", addr, "--sprint", "cut-s", "--repo", "mas-bandwidth/nova-tools", "--issue", "9002")
	if code != 0 || !strings.Contains(out, " label=nova-tools-9002 place=waiting ") {
		t.Fatalf("dependent cut: exit %d stderr %q out %q", code, errOut, out)
	}
	rec2, _ := client.HGetAll(ctx, "s:cut-s:card:nova-tools-9002").Result()
	if rec2["depends_on"] != "mas-bandwidth/nova-tools#9001,mas-bandwidth/nova-tools#3502" || rec2["base_sha"] != src.tip || rec2["where"] != "waiting" {
		t.Errorf("dependent card = depends_on %q base_sha %q where %q", rec2["depends_on"], rec2["base_sha"], rec2["where"])
	}
	body2, _ := client.Get(ctx, card.BodyKey("cut-s", rec2["payload_sha"])).Result()
	if !strings.Contains(body2, "\nWHY: the cut verb\n") {
		t.Errorf("the DEPENDS-ON WHY is not a card line:\n%s", body2)
	}
}

// TestCardCutRefusesNoStream is nova-tools#3623: an issue with no STREAM
// (and no --stream), or with a DEPENDS-ON that is not the card vocabulary, is
// refused with exit 1 before any write, and so is an issue that is not one
// invariant (#4396); --stream supplies a missing stream.
func TestCardCutRefusesNoStream(t *testing.T) {
	ctx := context.Background()
	noStream := strings.Replace(cutIssueBody, "STREAM: swarm: cards\r\n", "", 1)
	badDeps := strings.Replace(cutIssueBody, "DEPENDS-ON: none", "DEPENDS-ON: after the harvest lands", 1)
	pointer := strings.Replace(cutIssueBody, "INVARIANT: card cut writes one card record per issue.\r\n", "Build issue #4396 as written.\r\n", 1)
	client, addr, _ := cutFixture(t, map[int]card.Issue{
		9101: {Title: "no stream", Body: noStream},
		9102: {Title: "bad deps", Body: badDeps},
		9103: {Title: "a pointer to an issue", Body: pointer},
	})
	before, err := client.DBSize(ctx).Result()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		issue, why string
	}{{"9101", "has\\x20no\\x20STREAM:\\x20line"}, {"9102", `DEPENDS-ON:\x20"after\x20the\x20harvest\x20lands"\x20is\x20not`}} {
		code, out, errOut := runSprint("card", "cut", "--redis", addr, "--sprint", "cut-r", "--repo", "mas-bandwidth/nova-tools", "--issue", c.issue)
		if code != 1 || !strings.HasPrefix(out, "REFUSED card cut cut-r/mas-bandwidth/nova-tools#"+c.issue+" why=") || !strings.Contains(out, c.why) {
			t.Fatalf("issue %s: exit %d stderr %q out %q, want exit 1 naming %s", c.issue, code, errOut, out, c.why)
		}
	}
	// one invariant (#4396): refused before the body is stored, one line per rule
	code, out, errOut := runSprint("card", "cut", "--redis", addr, "--sprint", "cut-r", "--repo", "mas-bandwidth/nova-tools", "--issue", "9103")
	if want := `REFUSED card-lint rule=invariant-missing line="" remedy="add INVARIANT: <the one sentence the class test proves>" card="nova-tools-9103"` + "\n" +
		`REFUSED card-lint rule=build-issue line="Build issue #4396 as written." remedy="cut as a parent with children: card cut --parent" card="nova-tools-9103"` + "\n"; code != 1 || out != want {
		t.Fatalf("issue 9103: exit %d stderr %q out\n%s\nwant\n%s", code, errOut, out, want)
	}
	if after, _ := client.DBSize(ctx).Result(); after != before {
		t.Fatalf("a refused cut wrote %d keys", after-before)
	}
	code, out, errOut = runSprint("card", "cut", "--redis", addr, "--sprint", "cut-r", "--repo", "mas-bandwidth/nova-tools", "--issue", "9101", "--stream", "swarm: cards")
	if code != 0 || !strings.Contains(out, "stream=swarm:\\x20cards") {
		t.Fatalf("--stream: exit %d stderr %q out %q", code, errOut, out)
	}
	if code, _, _ := runSprint("card", "cut", "--redis", addr, "--sprint", "cut-r", "--repo", "mas-bandwidth/nova-tools"); code != 2 {
		t.Fatalf("no --issue: exit %d, want 2", code)
	}
}

// TestCardCutInlinesIndex is nova-tools#3623 with #3576: --index names a
// ctxindex directory and the card carries the CONTEXT block for the spec IDs
// the issue names (paragraph, guarding test, files), so the worker starts
// there instead of searching.
func TestCardCutInlinesIndex(t *testing.T) {
	ctx := context.Background()
	body := strings.Replace(cutIssueBody, "What:", "What: honours `alpha-rule-one`;", 1)
	client, addr, _ := cutFixture(t, map[int]card.Issue{9201: {Title: "indexed", Body: body}})
	repo := filepath.Join(t.TempDir(), "repo")
	for rel, text := range map[string]string{
		"go.mod":          "module example.com/fix\n\ngo 1.22\n",
		"docs/SPEC-A.md":  "1. `alpha-rule-one`: alpha is refused when empty.\n",
		"pkg/a/a.go":      "package a\n\nfunc Alpha() int { return 2 }\n",
		"pkg/a/a_test.go": "package a\n\nimport \"testing\"\n\n// alpha-rule-one: the empty alpha.\nfunc TestAlpha(t *testing.T) { _ = Alpha() }\n",
	} {
		p := filepath.Join(repo, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{{"init", "-q"}, {"add", "-A"}, {"commit", "-q", "-m", "one"}} {
		cmd := exec.Command("git", append([]string{"-C", repo, "-c", "user.name=t", "-c", "user.email=t@example.com"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	ix := filepath.Join(t.TempDir(), "ix")
	if _, err := ctxindex.Build(repo, ix); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := runSprint("card", "cut", "--redis", addr, "--sprint", "cut-i", "--repo", "mas-bandwidth/nova-tools", "--issue", "9201", "--index", ix)
	if code != 0 || !strings.Contains(out, " contexts=1 ") {
		t.Fatalf("exit %d stderr %q out %q", code, errOut, out)
	}
	sha, _ := client.HGet(ctx, "s:cut-i:card:nova-tools-9201", "payload_sha").Result()
	stored, err := client.Get(ctx, card.BodyKey("cut-i", sha)).Result()
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range []string{"\nCONTEXT: from the repo index at ", "\nSPEC alpha-rule-one (docs/SPEC-A.md:1): 1. `alpha-rule-one`: alpha is refused when empty.\n",
		"\nGUARDING TEST: pkg/a.TestAlpha (pkg/a/a_test.go:6)\n", "\nFILES: pkg/a/a.go, pkg/a/a_test.go\n"} {
		if !strings.Contains(stored, line) {
			t.Errorf("card lacks %q:\n%s", line, stored)
		}
	}
	if code, out, _ := runSprint("card", "cut", "--redis", addr, "--sprint", "cut-i", "--repo", "mas-bandwidth/nova-tools", "--issue", "9201", "--index", filepath.Join(t.TempDir(), "none")); code != 1 || !strings.Contains(out, "--index") {
		t.Fatalf("a missing index: exit %d out %q, want a refusal naming --index", code, out)
	}
}
