//go:build functional

package read_test

// Functional tests of `read brief --pr` and `read post --file` (nova-tools
// #4335, #4315) on a throwaway redis-server with the nova_sprint library and
// a bare mirror: the brief from the Redis copies with zero HTTP calls, the
// one REST read where Redis has no copy, and a batch post into the real line
// store. Run with -tags functional.

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/read"
	"github.com/redis/go-redis/v9"
)

// seedCardPR writes PR 7's record the way harvest leaves it (task, pr_title,
// pr_body), the card it names, our CI with one red receipt and the GitHub
// leg the webhook ingest writes.
func seedCardPR(t *testing.T, c *redis.Client, base, head string, card map[string]any, withBody bool) {
	t.Helper()
	ctx := context.Background()
	rec := map[string]any{"head": head, "base": "dev", "base_sha": base, "paths": "internal/x", "state": "open",
		"stream": "swarm: cards", "task": "work-brief"}
	if withBody {
		rec[read.FieldPRTitle], rec[read.FieldPRBody] = "read brief in one call", "STREAM: swarm: cards\nDONE-WHEN: one screen\n"
	}
	pipe := c.Pipeline()
	pipe.HSet(ctx, read.Key("nova-tools", "7"), rec)
	pipe.RPush(ctx, read.LinesKey("nova-tools", "7"), "JEV who=jev head="+head+" score=9/10")
	pipe.HSet(ctx, "task:work-brief", card)
	pipe.HSet(ctx, "ci:nova-tools:"+head, "repo", "nova-tools", "sha", head, "checks", "go-build,go-test-internal", "ci", "red", "attempt", "1", "bench", "studio")
	pipe.HSet(ctx, "ci:nova-tools:"+head+":go-build", "rc", "0", "wall_ms", "800")
	pipe.HSet(ctx, "ci:nova-tools:"+head+":go-test-internal", "rc", "1", "wall_ms", "40000", "log", "/l/t.log", "fail", "--- FAIL: TestX (0.01s)")
	pipe.HSet(ctx, "ci:nova-tools:"+head+":gh", "gh", "red", "gh_fail", "check:lint", "check:lint", "red 11 2026-09-26T15:00:00Z", "wf:ci", "green 12 2026-09-26T15:00:00Z")
	if _, err := pipe.Exec(ctx); err != nil {
		t.Fatal(err)
	}
}

// TestReadBriefPRFromTheRedisCopiesZeroCalls: a card pushed from its issue
// (source=issue) and a harvested PR: every section comes from Redis and the
// mirror, the GitHub client is there and is never called, exit 0.
func TestReadBriefPRFromTheRedisCopiesZeroCalls(t *testing.T) {
	t.Parallel()
	stub := startForge(t, nil)
	mirror, base, head := mirrorFixture(t, true)
	c := client(t)
	seedCardPR(t, c, base, head, map[string]any{"title": "read brief: one call", "source": "issue", "ref": "mas-bandwidth/nova-tools#4335",
		"paths": "internal/x", "done_when": "one screen", "body": "BUILD: read brief --pr\nEVIDENCE: readf.sh made four gh calls\nDONE-WHEN: a cold reader scores from one command's output"}, true)
	var out, errb strings.Builder
	code := read.BriefPR(context.Background(), c, read.PRBriefOptions{Repo: "nova-tools", N: "7", Mirror: mirror,
		GitHub: &read.GitHub{BaseURL: stub.URL, Token: "t"}}, &out, &errb)
	if code != 0 {
		t.Fatalf("exit %d\nstdout:\n%s\nstderr: %s", code, out.String(), errb.String())
	}
	if n := stub.calls.Load(); n != 0 {
		t.Fatalf("%d HTTP calls; every section has a Redis copy", n)
	}
	s := out.String()
	for _, want := range []string{
		"issue=redis:task:work-brief body (pushed from the issue, source=issue)", "pr_body=redis:pr:nova-tools:7 pr_body",
		"card=redis:task:work-brief", "files=mirror:" + mirror + " (base_sha)",
		"== ISSUE mas-bandwidth/nova-tools#4335: read brief: one call\nBUILD: read brief --pr",
		"EVIDENCE: readf.sh made four gh calls",
		"issue: a cold reader scores from one command's output", "card: one screen",
		"== PR #7: read brief in one call\nSTREAM: swarm: cards",
		"== FILES 2 (+3 -1) ", "outside PATHS 1", "OUTSIDE PATHS: README.md", "internal/x/x.go",
		"ours red attempt=1 bench=studio", "go-test-internal red rc=1 wall_ms=40000 fail=--- FAIL: TestX (0.01s)",
		"github red fail=check:lint", "FAILING: go-test-internal check:lint",
		"READ BRIEF DONE mas-bandwidth/nova-tools#7 head=" + head[:8] + " files=2 outside_paths=1 failing=2 lines=1 gaps=0 github_calls=0",
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("brief lacks %q:\n%s", want, s)
		}
	}
}

// forge is an httptest GitHub that serves one issue and one pull and counts.
type forge struct {
	URL   string
	calls atomic.Int64
}

func startForge(t *testing.T, routes map[string]string) *forge {
	t.Helper()
	f := &forge{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.calls.Add(1)
		body, ok := routes[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	f.URL = srv.URL
	return f
}

// TestReadBriefPROneRESTReadWhereRedisHasNoCopy: a card that only names its
// issue (SOURCE) and a hand PR with no pr_body are one GET each, named on
// SOURCES; under --no-github the same brief prints both as GAP lines and
// exits 1 with zero calls.
func TestReadBriefPROneRESTReadWhereRedisHasNoCopy(t *testing.T) {
	t.Parallel()
	f := startForge(t, map[string]string{
		"/repos/mas-bandwidth/nova-tools/issues/4335": `{"title":"read brief","body":"DONE-WHEN: a cold reader scores from one command's output"}`,
		"/repos/mas-bandwidth/nova-tools/pulls/7":     `{"title":"a hand PR","body":"what I did"}`,
	})
	mirror, base, head := mirrorFixture(t, false)
	c := client(t)
	seedCardPR(t, c, base, head, map[string]any{"title": "coordinator card", "source": "nova-tools#4335", "paths": "internal/x",
		"body": "DO: build nova-tools issue #4335 exactly as written there"}, false)
	var out, errb strings.Builder
	code := read.BriefPR(context.Background(), c, read.PRBriefOptions{Repo: "nova-tools", N: "7", Mirror: mirror,
		GitHub: &read.GitHub{BaseURL: f.URL, Token: "t"}}, &out, &errb)
	s := out.String()
	if code != 0 || f.calls.Load() != 2 {
		t.Fatalf("exit %d calls %d\n%s\n%s", code, f.calls.Load(), s, errb.String())
	}
	for _, want := range []string{
		"issue=rest:GET /repos/mas-bandwidth/nova-tools/issues/4335 (Redis has no copy)",
		"pr_body=rest:GET /repos/mas-bandwidth/nova-tools/pulls/7 (Redis has no copy)",
		"== ISSUE mas-bandwidth/nova-tools#4335: read brief", "DO: build nova-tools issue #4335",
		"== PR #7: a hand PR\nwhat I did", "github_calls=2",
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("brief lacks %q:\n%s", want, s)
		}
	}

	out.Reset()
	code = read.BriefPR(context.Background(), c, read.PRBriefOptions{Repo: "nova-tools", N: "7", Mirror: mirror}, &out, &errb)
	s = out.String()
	if code != 1 || f.calls.Load() != 2 {
		t.Fatalf("--no-github: exit %d calls %d\n%s", code, f.calls.Load(), s)
	}
	for _, want := range []string{
		"READ BRIEF GAP mas-bandwidth/nova-tools#7 issue: Redis keeps no text for mas-bandwidth/nova-tools#4335",
		"READ BRIEF GAP mas-bandwidth/nova-tools#7 pr body: pr:nova-tools:7 has no pr_body",
		"READ BRIEF INCOMPLETE mas-bandwidth/nova-tools#7", "gaps=2 github_calls=0",
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("--no-github brief lacks %q:\n%s", want, s)
		}
	}
}

// TestReadBriefPRRefusesAMissingRecord: no record head is one REFUSED line
// on stderr, exit 1, nothing on stdout.
func TestReadBriefPRRefusesAMissingRecord(t *testing.T) {
	t.Parallel()
	c := client(t)
	var out, errb strings.Builder
	code := read.BriefPR(context.Background(), c, read.PRBriefOptions{Repo: "nova-tools", N: "99", Mirror: t.TempDir()}, &out, &errb)
	if code != 1 || out.Len() != 0 || !strings.Contains(errb.String(), "READ BRIEF REFUSED repo=nova-tools n=99 why=MISSING pr:nova-tools:99") {
		t.Fatalf("exit %d out %q err %q", code, out.String(), errb.String())
	}
}

// TestReadPostFileIntoTheLineStore: two good rows land in the store with a
// receipt each; a malformed line and a bad row are refused and printed; the
// tally counts all four.
func TestReadPostFileIntoTheLineStore(t *testing.T) {
	t.Parallel()
	mirror, base, head := mirrorFixture(t, false)
	c := client(t)
	seedRecord(t, c, base, head)
	ctx := context.Background()
	in := "nova-tools\t7\tSCORE who=ann head=" + head + " score=9/10\\n1. evidence at head\n" +
		"nova-tools\t7\tHOLD who=bob head=" + head + " score=5/10\n" +
		"nova-tools\t7\tSCORE head=" + head + " score=9/10\n" +
		"nova-tools\t7\n"
	post := func(row read.Row, stdout, stderr io.Writer) int {
		return read.PostMeasured(ctx, c, row.Repo, row.N, row.Line, mirror, nil, stdout, stderr)
	}
	var out, errb strings.Builder
	code := read.PostFile(strings.NewReader(in), "scores.tsv", post, &out, &errb)
	if code != 1 {
		t.Fatalf("exit %d\n%s\n%s", code, out.String(), errb.String())
	}
	for _, want := range []string{"ROW 1 READ POST repo=nova-tools n=7 kind=SCORE", "ROW 2 READ POST repo=nova-tools n=7 kind=HOLD",
		"READ POST FILE file=scores.tsv rows=4 posted=2 refused=2 github_calls=0"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("stdout lacks %q:\n%s", want, out.String())
		}
	}
	for _, want := range []string{"ROW 3 READ POST REFUSED repo=nova-tools n=7 why=line has no who=", "ROW 4 READ POST REFUSED why=want"} {
		if !strings.Contains(errb.String(), want) {
			t.Fatalf("stderr lacks %q:\n%s", want, errb.String())
		}
	}
	lines := c.LRange(ctx, read.LinesKey("nova-tools", "7"), 0, -1).Val()
	if len(lines) != 3 || !strings.Contains(strings.Join(lines, "\n"), "1. evidence at head") {
		t.Fatalf("lines %q, want the seeded JEV and the two posted rows (the SCORE with its item)", lines)
	}
}
