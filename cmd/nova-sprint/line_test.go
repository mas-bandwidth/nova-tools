//go:build functional

package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
)

// TestLineVerbPostListImport runs the line verb end to end on a throwaway
// Redis: post from flags, a gate that disagrees refused, list at head, and
// an import that is idempotent; usage errors exit 2 before Redis.
func TestLineVerbPostListImport(t *testing.T) {
	t.Setenv(store.UserEnv, "")
	t.Setenv("NOVA_SPRINT_REDIS", "")
	t.Setenv("NOVA_REDIS_ADDR", "")
	for _, args := range [][]string{
		{"line"},
		{"line", "post", "--repo", "nova-tools", "--n", "3", "--redis", "127.0.0.1:1"},
		{"line", "post", "--repo", "nova-tools", "--n", "3", "--line", "x", "--kind", "SCORE", "--redis", "127.0.0.1:1"},
		{"line", "list", "--repo", "nova-tools", "--n", "0", "--redis", "127.0.0.1:1"},
		{"line", "import", "--repo", "nova-tools", "--n", "3", "--redis", "127.0.0.1:1"},
	} {
		if code, stdout, stderr := runSprint(args...); code != 2 || stdout != "" || !strings.HasPrefix(stderr, "nova-sprint line") {
			t.Fatalf("%v: exit %d stdout %q stderr %q; want a usage refusal", args, code, stdout, stderr)
		}
	}
	addr := testutil.Start(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	defer c.Close()
	ctx := context.Background()
	if err := fn.Load(ctx, c); err != nil {
		t.Fatal(err)
	}
	head := "3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c"
	if err := c.HSet(ctx, "pr:nova-tools:3", "head", head, "base", "dev").Err(); err != nil {
		t.Fatal(err)
	}
	if err := c.HSet(ctx, "ci:nova-tools:"+head, "ci", "green").Err(); err != nil {
		t.Fatal(err)
	}
	body := filepath.Join(t.TempDir(), "body.md")
	if err := os.WriteFile(body, []byte("- no items\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mirror := t.TempDir() // not a git dir: scope stays unmeasured
	code, stdout, stderr := runSprint("line", "post", "--repo", "mas-bandwidth/nova-tools", "--n", "3", "--kind", "SCORE", "--as", "emma",
		"--head", head, "--score", "10", "--gates", "ci:ok,base:ok,scope:ok", "--body-file", body, "--mirror", mirror, "--redis", addr)
	want := "LINE POST pr:nova-tools:3:line:" + head + ":emma:SCORE kind=SCORE who=emma head=3c3c3c3c gates=ci:ok,base:ok,scope:ok measured=ci,base lines=1 github_calls=0"
	if code != 0 || strings.TrimSpace(stdout) != want {
		t.Fatalf("post: exit %d stdout %q stderr %q; want %q", code, stdout, stderr, want)
	}
	if got := c.LIndex(ctx, "pr:nova-tools:3:lines", 0).Val(); got != "SCORE who=emma head="+head+" score=10/10 gates=ci:ok,base:ok,scope:ok\n- no items" {
		t.Fatalf("built line %q", got)
	}
	code, stdout, stderr = runSprint("line", "post", "--repo", "nova-tools", "--n", "3", "--line", "HOLD who=stella head="+head[:8]+" gates=ci:red", "--mirror", mirror, "--redis", addr)
	if code != 1 || stdout != "" || !strings.Contains(stderr, "LINE POST REFUSED pr:nova-tools:3 kind=HOLD who=stella why=gate ci typed red but measured ok") {
		t.Fatalf("disagreeing gate: exit %d stdout %q stderr %q", code, stdout, stderr)
	}
	comments := filepath.Join(t.TempDir(), "comments.json")
	raw := `[{"id": 9, "body": "HOLD who=stella head=` + head + ` gates=ci:red: item 1"}, {"id": 10, "body": "prose"}]`
	if err := os.WriteFile(comments, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, wantImport := range []string{"imported=1 existed=0", "imported=0 existed=1"} {
		code, stdout, stderr = runSprint("line", "import", "--repo", "nova-tools", "--n", "3", "--comments-file", comments, "--redis", addr)
		if code != 0 || !strings.Contains(stdout, "LINE IMPORT pr:nova-tools:3 comments=2 typed=1 "+wantImport+" missing=0 github_calls=0") {
			t.Fatalf("import: exit %d stdout %q stderr %q; want %q", code, stdout, stderr, wantImport)
		}
	}
	code, stdout, stderr = runSprint("line", "list", "--repo", "nova-tools", "--n", "3", "--redis", addr)
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	if code != 0 || len(lines) != 3 || !strings.HasPrefix(lines[0], "LINE SCORE who=emma head=3c3c3c3c score=10 gates=ci:ok,base:ok,scope:ok measured=ci,base source=- at=") ||
		!strings.HasPrefix(lines[1], "LINE HOLD who=stella head=3c3c3c3c score=- gates=ci:ok,base:ok measured=ci,base source=comment:9 at=") ||
		lines[2] != "LINES pr:nova-tools:3 head=3c3c3c3c count=2" {
		t.Fatalf("list: exit %d stdout %q stderr %q", code, stdout, stderr)
	}
}
