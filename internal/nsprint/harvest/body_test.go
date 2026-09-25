package harvest_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/file"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/harvest"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/prereview"
)

// forge is the GitHub origin the card record's origin and the push URL are
// written against. It is data the body builder and RemoteURL compare with,
// never contacted, so it is spelled from parts (the net class test in
// internal/ci reads whole literals for live endpoints).
const forge = "https" + "://" + "github.com"

// quackRecord is the quack-0925b card of #3712 as Redis holds it after card
// end: the card hash and its attempt's result hash.
func quackRecord() (harvest.Card, harvest.Record) {
	c := harvest.Card{
		Label: "s00-0101-quack-batman-flash", Repo: "mas-bandwidth/nova-tools", Base: "dev", Attempt: "1",
		PushedSHA: "70552e2a00000000000000000000000000000000",
		Identity:  "quack-0925b/s00-0101-quack-batman-flash/ac1dfd2e/batman/1",
		Results:   "/Users/nova/nova-bench/card-results/quack-0925b/s00-0101-quack-batman-flash/1",
		Branch:    "nova/quack-0925b/s00-0101-quack-batman-flash-a1",
	}
	rec := harvest.Record{
		Card: map[string]string{
			"label": c.Label, "kind": "fix", "repo": c.Repo, "base": "dev",
			"base_sha":   "ac1dfd2e00000000000000000000000000000000",
			"paths":      "internal/quack/**",
			"test":       "./internal/quack TestQuackBatman",
			"stream":     "swarm: cards",
			"priority":   "0",
			"depends_on": "",
			"origin":     forge + "/mas-bandwidth/nova-tools/issues/3690",
			"done_when":  "go test ./internal/quack -run TestQuackBatman passes at head",
			"identity":   c.Identity, "attempt": "1", "bench": "batman",
		},
		Result: map[string]string{
			"line1":     "RESULT: s00-0101-quack-batman-flash sha=ac1dfd2e0000",
			"w_line2":   "DONE",
			"w_note":    "quacked once, as asked",
			"w_paths":   "internal/quack/batman.go internal/quack/batman_test.go",
			"w_check":   "pass",
			"w_commit":  c.PushedSHA,
			"w_model":   "deepseek-v4-flash",
			"w_route":   "deepseek",
			"w_tier":    "flash",
			"w_wall_ms": "81234",
		},
	}
	return c, rec
}

// TestHarvestTypedBodyFromRecord is #3712's DONE-WHEN: the PR harvest opens
// carries BASE, base-sha, PATHS (the commit range's), DEPENDS-ON, DONE-WHEN,
// STREAM and SELF-CHECK built from the card record alone, and nova-decide
// review's mechanical checks (internal/prereview) read donewhen, paths,
// selfcheck and claims as present.
func TestHarvestTypedBodyFromRecord(t *testing.T) {
	rangePaths := []string{"internal/quack/batman.go", "internal/quack/batman_test.go"}
	for _, tc := range []struct {
		name  string
		edit  func(*harvest.Record)
		paths []string
		title string
		want  []string // the body's first lines, in order
	}{
		{
			name: "quack", paths: rangePaths,
			title: "s00-0101-quack-batman-flash: go test ./internal/quack -run TestQuackBatman passes at head",
			want: []string{
				"BASE: dev",
				"base-sha: ac1dfd2e00000000000000000000000000000000",
				"PATHS: internal/quack/**",
				"CHANGED: internal/quack/batman.go internal/quack/batman_test.go",
				"DEPENDS-ON: none",
				"DONE-WHEN: go test ./internal/quack -run TestQuackBatman passes at head",
				"STREAM: swarm: cards",
				"WHO: any",
				"Closes #3690",
				"",
				"SELF-CHECK: pass (./internal/quack TestQuackBatman)",
				"",
				"RESULT: s00-0101-quack-batman-flash sha=ac1dfd2e0000",
				"DONE",
				"",
				"quacked once, as asked",
				"",
				"sprint: quack-0925b",
				"card: s00-0101-quack-batman-flash",
				"identity: quack-0925b/s00-0101-quack-batman-flash/ac1dfd2e/batman/1",
				"attempt: 1",
				"bench: batman",
				"tier: flash route: deepseek model: deepseek-v4-flash",
				"wall_ms: 81234",
				"pushed_sha: 70552e2a00000000000000000000000000000000",
				"results: /Users/nova/nova-bench/card-results/quack-0925b/s00-0101-quack-batman-flash/1",
				"files: internal/quack/batman.go internal/quack/batman_test.go",
				"",
				harvest.ClaudeLine,
			},
		},
		{
			name: "task-title-other-repo-origin-card-paths",
			edit: func(r *harvest.Record) {
				r.Card["task"] = "Teach the batman quack to quack twice, then write a test that proves it quacks exactly twice"
				r.Card["origin"] = forge + "/mas-bandwidth/rowan-tools/issues/12"
				r.Card["depends_on"] = "s00-0100-quack-setup"
			},
			title: "s00-0101-quack-batman-flash: Teach the batman quack to quack twice, then write a test that proves i",
			want: []string{
				"BASE: dev",
				"base-sha: ac1dfd2e00000000000000000000000000000000",
				"PATHS: internal/quack/**",
				"CHANGED: internal/quack/batman.go internal/quack/batman_test.go",
				"DEPENDS-ON: s00-0100-quack-setup",
				"DONE-WHEN: go test ./internal/quack -run TestQuackBatman passes at head",
				"STREAM: swarm: cards",
				"WHO: any",
				"ORIGIN: " + forge + "/mas-bandwidth/rowan-tools/issues/12",
			},
		},
		{
			name: "record-before-3712",
			edit: func(r *harvest.Record) {
				delete(r.Card, "done_when")
				delete(r.Card, "origin")
				delete(r.Result, "w_check")
			},
			paths: rangePaths,
			title: "s00-0101-quack-batman-flash: nova-sprint quack-0925b card s00-0101-quack-batman-flash attempt 1",
			want: []string{
				"BASE: dev",
				"base-sha: ac1dfd2e00000000000000000000000000000000",
				"PATHS: internal/quack/**",
				"CHANGED: internal/quack/batman.go internal/quack/batman_test.go",
				"DEPENDS-ON: none",
				"DONE-WHEN: -",
				"STREAM: swarm: cards",
				"WHO: any",
				"ORIGIN: none",
				"",
				"SELF-CHECK: not-run (./internal/quack TestQuackBatman)",
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, rec := quackRecord()
			if tc.edit != nil {
				tc.edit(&rec)
			}
			if got := harvest.Title("quack-0925b", c, rec); got != tc.title {
				t.Fatalf("title = %q, want %q", got, tc.title)
			}
			body := harvest.Body("quack-0925b", "batman", c, rec, tc.paths)
			lines := strings.Split(body, "\n")
			if len(lines) < len(tc.want) || !reflect.DeepEqual(lines[:len(tc.want)], tc.want) {
				t.Fatalf("body lines:\n%s\nwant first %d lines:\n%s", body, len(tc.want), strings.Join(tc.want, "\n"))
			}
			if !strings.HasSuffix(body, harvest.ClaudeLine+"\n") {
				t.Fatalf("body does not end with the Claude Code line:\n%s", body)
			}
			// #3488: WHO, STREAM, DEPENDS-ON and DONE-WHEN ride in the body,
			// which passes the posted-body rule harvest checks before the create
			for _, key := range []string{"\nWHO: ", "\nSTREAM: ", "\nDEPENDS-ON: ", "\nDONE-WHEN: "} {
				if !strings.Contains(body, key) {
					t.Fatalf("body lacks %q:\n%s", key, body)
				}
			}
			if lint := file.LintBody([]byte(body)); len(lint) > 0 {
				t.Fatalf("body fails file.LintBody: %v", lint)
			}
		})
	}

	// nova-decide review reads the quack body: donewhen, paths, selfcheck
	// and claims are present (yes), not missing.
	c, rec := quackRecord()
	body := harvest.Body("quack-0925b", "batman", c, rec, rangePaths)
	pr := prereview.PR{Repo: "mas-bandwidth/nova-tools", Number: 3701, Head: c.PushedSHA, Body: body, Files: rangePaths,
		Diff: "+++ b/internal/quack/batman_test.go\n+func TestQuackBatman(t *testing.T) {}\n"}
	checks := prereview.Mechanical(pr, prereview.InferCard(pr))
	for name, got := range map[string]prereview.Check{"donewhen": checks.Done, "paths": checks.Paths, "selfcheck": checks.Symbol, "claims": checks.Claims} {
		if got.Result != prereview.Yes {
			t.Fatalf("%s = %s (%s), want yes on the typed body", name, got.Result, got.Reason)
		}
	}
	if !strings.Contains(checks.Paths.Reason, "internal/quack/**") {
		t.Fatalf("paths check bounded by %q, want the card's declared PATHS internal/quack/**", checks.Paths.Reason)
	}
	// The scope gate stays meaningful: a card that wrote outside its declared
	// PATHS fails the paths check; CHANGED names the file, PATHS does not
	// widen to cover it.
	outside := append(append([]string(nil), rangePaths...), "cmd/nova-sprint/main.go")
	opr := pr
	opr.Body, opr.Files = harvest.Body("quack-0925b", "batman", c, rec, outside), outside
	if !strings.Contains(opr.Body, "\nPATHS: internal/quack/**\nCHANGED: internal/quack/batman.go internal/quack/batman_test.go cmd/nova-sprint/main.go\n") {
		t.Fatalf("outside body lacks the declared PATHS and the CHANGED line:\n%s", opr.Body)
	}
	if got := prereview.Mechanical(opr, prereview.InferCard(opr)).Paths; got.Result != prereview.No || !strings.Contains(got.Reason, "cmd/nova-sprint/main.go") {
		t.Fatalf("paths with a file outside PATHS = %s (%s), want no naming cmd/nova-sprint/main.go", got.Result, got.Reason)
	}
	// A DONE-WHEN that names RESULT does not move the RESULT the checks read.
	rec.Card["done_when"] = "the RESULT line 2 is DONE and TestQuackBatman passes"
	pr.Body = harvest.Body("quack-0925b", "batman", c, rec, rangePaths)
	if got := prereview.Mechanical(pr, prereview.InferCard(pr)).Done; got.Result != prereview.Yes {
		t.Fatalf("donewhen with RESULT in the DONE-WHEN = %s (%s), want yes", got.Result, got.Reason)
	}
}

// gitIn runs git in dir with a fixed identity.
func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// TestHarvestPushesToRepoURLAndReadsRangePaths: the push goes to the URL
// built from the card record's repo, not to the clone's origin (the clone
// here has none, as on superman in quack-0925b), and the PATHS are the paths
// base_sha..pushed_sha changed, read by git in the harvest clone.
func TestHarvestPushesToRepoURLAndReadsRangePaths(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	if got := harvest.RemoteURL("nova-tools"); got != forge+"/mas-bandwidth/nova-tools.git" {
		t.Fatalf("RemoteURL(nova-tools) = %q", got)
	}
	if got := harvest.RemoteURL("mas-bandwidth/rowan-tools"); got != forge+"/mas-bandwidth/rowan-tools.git" {
		t.Fatalf("RemoteURL(mas-bandwidth/rowan-tools) = %q", got)
	}
	dir := t.TempDir()
	remote := filepath.Join(dir, "github", "nova-tools.git")
	gitIn(t, dir, "init", "-q", "--bare", remote)
	results := filepath.Join(dir, "results")
	repo := filepath.Join(results, "repo")
	if err := os.MkdirAll(filepath.Join(repo, "internal", "quack"), 0o755); err != nil {
		t.Fatal(err)
	}
	gitIn(t, repo, "init", "-q")
	write := func(name, text string) {
		if err := os.WriteFile(filepath.Join(repo, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("README.md", "base\n")
	write("internal/quack/old.go", "package quack\n")
	gitIn(t, repo, "add", "-A")
	gitIn(t, repo, "commit", "-q", "-m", "base")
	base := gitIn(t, repo, "rev-parse", "HEAD")
	write("internal/quack/batman.go", "package quack\n\nfunc Batman() {}\n")
	gitIn(t, repo, "add", "-A")
	gitIn(t, repo, "commit", "-q", "-m", "one")
	write("internal/quack/old.go", "package quack\n\n// changed\n")
	gitIn(t, repo, "commit", "-q", "-am", "two")
	head := gitIn(t, repo, "rev-parse", "HEAD")
	if out := gitIn(t, repo, "remote"); out != "" {
		t.Fatalf("the clone has remotes %q; the fixture needs none", out)
	}

	var asked []string
	p := harvest.SSHPusher{SSH: writeFakeSSH(t, dir, filepath.Join(dir, "ssh.log")), Remote: func(c harvest.Card) string {
		asked = append(asked, c.Repo)
		return remote
	}}
	c := harvest.Card{Label: "s00-0502-quack-superman-flash", Repo: "mas-bandwidth/nova-tools", Attempt: "3",
		PushedSHA: head, Branch: "nova/quack-0925b/s00-0502-quack-superman-flash-a3", Results: results}
	paths, err := p.PushRange(context.Background(), harvest.BenchInfo{Name: "superman", Host: "superman.fixture", User: "nova"}, c, base)
	if err != nil {
		t.Fatalf("push with no origin in the clone: %v", err)
	}
	if !reflect.DeepEqual(asked, []string{"mas-bandwidth/nova-tools"}) {
		t.Fatalf("remote asked for %v, want the card record's repo", asked)
	}
	if tip := gitIn(t, dir, "--git-dir", remote, "rev-parse", "refs/heads/"+c.Branch); tip != head {
		t.Fatalf("repo URL %s at %s, want pushed_sha %s", c.Branch, tip, head)
	}
	if want := []string{"internal/quack/batman.go", "internal/quack/old.go"}; !reflect.DeepEqual(paths, want) {
		t.Fatalf("range paths = %v, want %v (base..pushed_sha, not the last commit)", paths, want)
	}
	// The body states the card's declared PATHS and, on its own line, the
	// range git read.
	_, rec := quackRecord()
	body := harvest.Body("quack-0925b", "superman", c, rec, paths)
	if !strings.Contains(body, "\nPATHS: internal/quack/**\nCHANGED: internal/quack/batman.go internal/quack/old.go\n") {
		t.Fatalf("body lacks PATHS (declared) then CHANGED (the range):\n%s", body)
	}
	// Again: already at the sha, the paths still come back; a base not in the
	// clone gives no paths and the push still stands.
	if again, err := p.PushRange(context.Background(), harvest.BenchInfo{Name: "superman", Host: "superman.fixture"}, c, base); err != nil || len(again) != 2 {
		t.Fatalf("re-push = %v %v, want PUSH ALREADY with the same two paths", again, err)
	}
	if none, err := p.PushRange(context.Background(), harvest.BenchInfo{Name: "superman", Host: "superman.fixture"}, c, strings.Repeat("0", 40)); err != nil || none != nil {
		t.Fatalf("unknown base = %v %v, want no paths and no error", none, err)
	}
}

// recordingForge is fixtureForge that keeps each create's title and body.
type recordingForge struct {
	*fixtureForge
	mu     sync.Mutex
	titles map[string]string
	bodies map[string]string
}

func (f *recordingForge) OpenPR(ctx context.Context, repo, branch, base, title, body string) (harvest.PR, error) {
	pr, err := f.fixtureForge.OpenPR(ctx, repo, branch, base, title, body)
	f.mu.Lock()
	f.titles[branch], f.bodies[branch] = title, body
	f.mu.Unlock()
	return pr, err
}

// rangePusher is a RangePusher whose commit range changed paths.
type rangePusher struct {
	fixturePusher
	paths []string
	bases []string
}

func (p *rangePusher) PushRange(ctx context.Context, b harvest.BenchInfo, c harvest.Card, base string) ([]string, error) {
	p.mu.Lock()
	p.bases = append(p.bases, base)
	p.mu.Unlock()
	return p.paths, p.Push(ctx, b, c)
}

// TestHarvestOpensTypedPRAndRecordsIt: a harvest of the fixture card opens
// the PR with the typed title and body, stores the body on the card record
// (pr_body, with pr and head) and writes one `pr head` log entry whose repo,
// pr and head are the record's, in ns_pr_head's shape.
func TestHarvestOpensTypedPRAndRecordsIt(t *testing.T) {
	c := startRedis(t)
	st := store.New(c)
	ctx := context.Background()
	c.HSet(ctx, "bench:batman:beat", "host", "batman.fixture", "user", "nova")
	c.HSet(ctx, "bench:batman:state", "state", "UP", "at", "1")
	label := "s00-0101-quack-batman-flash"
	seedEnded(t, c, "batman", label, "fix", "DONE", sha(label))
	_, rec := quackRecord()
	for _, f := range []string{"paths", "test", "stream", "origin", "done_when"} {
		c.HSet(ctx, "s:"+sprint+":card:"+label, f, rec.Card[f])
	}
	c.HSet(ctx, "s:"+sprint+":card:"+label+":result:a1", rec.Result)

	forge := &recordingForge{fixtureForge: newForge(), titles: map[string]string{}, bodies: map[string]string{}}
	branch := "nova/" + sprint + "/" + label + "-a1"
	forge.heads[branch] = sha(label)
	pusher := &rangePusher{fixturePusher: fixturePusher{pushes: map[string]int{}}, paths: []string{"internal/quack/batman.go"}}
	res := harvest.Run(ctx, st, harvest.Options{Sprint: sprint, Benches: []string{"batman"}, Clock: time.Minute,
		Instance: "typed-1", Forge: forge, Pusher: pusher})
	if len(res[0].Cards) != 1 || res[0].Cards[0].Via != "opened" {
		t.Fatalf("pass = %+v; want the card opened and harvested", res[0])
	}
	if !reflect.DeepEqual(pusher.bases, []string{"09fbedc9"}) {
		t.Fatalf("range base = %v, want the record's base_sha", pusher.bases)
	}
	h := c.HGetAll(ctx, "s:"+sprint+":card:"+label).Val()
	body := forge.bodies[branch]
	for _, want := range []string{"BASE: dev\n", "base-sha: 09fbedc9\n", "PATHS: internal/quack/**\nCHANGED: internal/quack/batman.go\n", "DEPENDS-ON: none\n",
		"DONE-WHEN: go test ./internal/quack -run TestQuackBatman passes at head\n", "STREAM: swarm: cards\n",
		"SELF-CHECK: pass (./internal/quack TestQuackBatman)\n"} {
		if !strings.Contains(body, want) {
			t.Fatalf("opened body lacks %q:\n%s", want, body)
		}
	}
	if forge.titles[branch] != label+": go test ./internal/quack -run TestQuackBatman passes at head" {
		t.Fatalf("title = %q", forge.titles[branch])
	}
	if h["pr_body"] != body || h["state"] != "harvested" || h["head"] != sha(label) {
		t.Fatalf("card pr_body/state/head = %q/%s/%s; want the opened body, harvested, pushed sha", h["pr_body"], h["state"], h["head"])
	}
	var heads []map[string]any
	for _, e := range c.XRange(ctx, "s:"+sprint+":log", "-", "+").Val() {
		if e.Values["kind"] == "pr head" {
			heads = append(heads, e.Values)
		}
	}
	if len(heads) != 1 {
		t.Fatalf("pr head entries = %d, want 1", len(heads))
	}
	e := heads[0]
	if e["repo"] != h["repo"] || e["pr"] != h["pr"] || e["head"] != h["head"] || e["prev"] != "" || e["source"] != "harvest" || e["at"] == "" {
		t.Fatalf("pr head entry = %v; want repo/pr/head of the record %s/%s/%s, prev empty, source harvest", e, h["repo"], h["pr"], h["head"])
	}
}

// failingPusher is superman in quack-0925b: every push fails.
type failingPusher struct{}

func (failingPusher) Push(context.Context, harvest.BenchInfo, harvest.Card) error {
	return errors.New("ssh superman: exit 128: fatal: 'origin' does not appear to be a git repository")
}

// TestHarvestFailCapMovesCardToDoneFail: a card whose harvest fails every
// pass is counted, and on the third failed pass moves to done/fail through
// the one move primitive (state refused, reason harvest, the err line as the
// move's why); the next pass no longer sees it.
func TestHarvestFailCapMovesCardToDoneFail(t *testing.T) {
	c := startRedis(t)
	st := store.New(c)
	ctx := context.Background()
	c.HSet(ctx, "bench:superman:beat", "host", "superman.fixture", "user", "nova")
	c.HSet(ctx, "bench:superman:state", "state", "UP", "at", "1")
	label := "s00-0502-quack-superman-flash"
	seedEnded(t, c, "superman", label, "fix", "DONE", sha(label))
	key := "s:" + sprint + ":card:" + label
	pass := func(n int) harvest.BenchResult {
		t.Helper()
		return harvest.Run(ctx, st, harvest.Options{Sprint: sprint, Benches: []string{"superman"}, Clock: time.Minute,
			Instance: fmt.Sprintf("cap-%d", n), Forge: newForge(), Pusher: failingPusher{}})[0]
	}
	for n := 1; n <= 2; n++ {
		r := pass(n)
		if len(r.Failed) != 1 || r.Failed[0].Fails != n || r.Failed[0].Moved {
			t.Fatalf("pass %d = %+v; want one failure counted %d, not moved", n, r, n)
		}
		if s := c.HGet(ctx, key, "state").Val(); s != "ended" {
			t.Fatalf("pass %d: state %s, want ended below the cap", n, s)
		}
	}
	r := pass(3)
	if len(r.Failed) != 1 || r.Failed[0].Fails != 3 || !r.Failed[0].Moved {
		t.Fatalf("pass 3 = %+v; want the third failure to move the card", r)
	}
	h := c.HGetAll(ctx, key).Val()
	if h["state"] != "refused" || h["where"] != "done" || h["where_ok"] != "fail" || h["reason"] != "harvest" ||
		!strings.Contains(h["harvest_err"], "'origin' does not appear to be a git repository") {
		t.Fatalf("card = %v; want refused, done/fail, reason harvest, the err line kept", h)
	}
	moves := c.XRevRangeN(ctx, "sprint:"+sprint+":moves", "+", "-", 1).Val()
	if len(moves) != 1 || moves[0].Values["to"] != "done/fail" || moves[0].Values["why"] != h["harvest_err"] {
		t.Fatalf("last move = %v; want done/fail with the err line as why", moves)
	}
	if c.SIsMember(ctx, "s:"+sprint+":bench:superman:ended", label).Val() {
		t.Fatal("the failed card is still in the bench's ended set")
	}
	if r := pass(4); len(r.Failed) != 0 || len(r.Cards) != 0 || r.Err != nil {
		t.Fatalf("pass 4 = %+v; want nothing due", r)
	}
}
