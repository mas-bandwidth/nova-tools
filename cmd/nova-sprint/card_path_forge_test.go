package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
)

// TestCardPathMakesNoGitHubCalls is THE BOUNDARY end to end (nova-tools
// #3967): with every forge client counting (a fake gh on PATH logs each call,
// and GITHUB_API_URL is a counting server), task push --issue imports the
// whole issue with its three reads, and then render, the brief, the moves
// to working and merging, fsck, the DEPENDS-ON resolve, read post, the
// stream land's member move and one dev-red reconciler pass make zero GitHub
// calls. (The landed close's REST calls are TestLandStreamEndToEnd's.)
func TestCardPathMakesNoGitHubCalls(t *testing.T) {
	seat := newSeat(t)
	t.Setenv("FRIEND_QUEUE_SPRINT", seatSprint)
	t.Setenv("NOVA_FRIEND", "")
	t.Setenv("GH_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")
	var rest atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rest.Add(1)
		http.Error(w, "no GitHub on the card path", http.StatusTeapot)
	}))
	t.Cleanup(srv.Close)
	t.Setenv("GITHUB_API_URL", srv.URL)

	dir := t.TempDir()
	calls := filepath.Join(dir, "gh.calls")
	sha := strings.Repeat("ab", 20)
	body := `STREAM: swarm: cards\nWHO: any\nKIND: build\nBASE: dev\nbase-sha: ` + sha +
		`\nPATHS: internal/x/x.go\nDEPENDS-ON: none\n\nBuild x.\n\nDONE-WHEN: ` + "`go test ./internal/x -run TestX`" + ` passes.`
	script := "#!/bin/sh\necho \"$*\" >> " + strconv.Quote(calls) + "\n" + `case "$*" in
  "api repos/mas-bandwidth/nova-tools/issues/3967") printf '%s\n' '{"number":3967,"html_url":"https://forge.invalid/mas-bandwidth/nova-tools/issues/3967","title":"the issue on the card","body":"` + body + `","state":"open","created_at":"2026-09-25T19:30:00Z","updated_at":"2026-09-25T19:35:00Z","labels":[{"name":"cards"}]}' ;;
  *"/comments"*) printf '%s\n' '"SPEC who=emma 10/10"' ;;
  *"/timeline"*) ;;
  *) echo "gh: unexpected $*" >&2; exit 1 ;;
esac
`
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	ghCalls := func() int {
		b, _ := os.ReadFile(calls)
		return strings.Count(string(b), "\n")
	}
	addr := os.Getenv("NOVA_SPRINT_REDIS")
	step := func(name string, code int, out, errOut, want string) {
		t.Helper()
		if code != 0 || !strings.Contains(out+errOut, want) {
			t.Fatalf("%s = %d %q %q; want exit 0 and %q", name, code, out, errOut, want)
		}
	}

	// import: the card's one GitHub read, three calls
	code, out, errOut := runTaskCLI("push", "--actor", "rowan", "--id", "c3967", "--issue", "mas-bandwidth/nova-tools#3967", "--route", "flash")
	step("push --issue", code, out, errOut, "to=waiting")
	if n := ghCalls(); n != 3 {
		t.Fatalf("import made %d gh calls, want 3 (issue, comments, timeline)", n)
	}
	rec := seat.client.HGetAll(context.Background(), "task:c3967").Val()
	if rec["issue_number"] != "3967" || rec["title"] != "the issue on the card" || rec["issue_typed"] != "SPEC who=emma 10/10" || rec["paths"] != "internal/x/x.go" {
		t.Fatalf("record after import: %v", rec)
	}

	render := func(args ...string) (int, string, string) {
		var o, e bytes.Buffer
		c := runCard(context.Background(), append([]string{"render"}, args...), &o, &e)
		return c, o.String(), e.String()
	}
	code, out, errOut = render("--id", "c3967")
	step("card render", code, out, errOut, "RESULT: c3967 sha="+sha[:12])
	code, out, errOut = render("--id", "c3967", "--brief")
	step("card render --brief", code, out, errOut, "TASK: c3967")
	code, out, errOut = runTaskCLI("unblock", "--actor", "rowan", "--id", "c3967")
	step("deal to ready", code, out, errOut, "to=ready")
	code, out, errOut = runTaskCLI("take", "--actor", "a", "--id", "c3967")
	step("work", code, out, errOut, "ids=c3967")
	code, out, errOut = runTaskCLI("done", "--actor", "a", "--id", "c3967", "--evidence", "built", "--pr", "41")
	step("done", code, out, errOut, "to=merging")
	code, out, errOut = runTaskCLI("fsck", "--sprint", seatSprint)
	step("fsck", code, out, errOut, "drift=0")
	code, out, errOut = runSprint("ready", "--redis", addr, "--sprint", seatSprint)
	step("resolve", code, out, errOut, "ready ")
	code, out, errOut = runSprint("pr", "record", "--redis", addr, "--repo", "nova-tools", "--n", "41", "--head", sha, "--base", "dev", "--stream", "swarm: cards", "--task", "c3967")
	step("pr record", code, out, errOut, "created=true")
	code, out, errOut = runSprint("read", "post", "--redis", addr, "--repo", "nova-tools", "--n", "41", "--line", "SCORE who=emma head="+sha+" score=10/10")
	step("read post", code, out, errOut, "github_calls=0")
	code, out, errOut = runTaskCLI("land", "--actor", "rowan", "--stream", "swarm: cards", "--sha", "abcdef12")
	step("land member move", code, out, errOut, "LANDED c3967")
	code, out, errOut = runSprint("dev-red", "check", "--redis", addr, "--repo", "nova-tools", "--base", "dev")
	if code > 1 {
		t.Fatalf("dev-red pass = %d %q %q", code, out, errOut)
	}

	if n := ghCalls(); n != 3 || rest.Load() != 0 {
		b, _ := os.ReadFile(calls)
		t.Fatalf("after waiting: %d gh calls beyond the import and %d REST calls; want 0 and 0\n%s", n-3, rest.Load(), b)
	}
}
