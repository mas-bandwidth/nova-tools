//go:build functional

package read_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/read"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
)

// TestReadBriefFromFirstReadTaskMirrorDiffZeroGitHubCalls is the DONE-WHEN
// of #3599 on the read task: a first-read task (task:<id>, kind read, ref
// the PR URL, head; the shape ns_pr_first_read pushes, on a reserved test
// host since the ref parse takes any forge host) is all a read child
// holds, and BriefTask turns it into brief.md naming the saved diff and
// diff.patch equal to `git -C <mirror> diff <base_sha>..<head>`, with the
// GitHub stub counting zero calls.
func TestReadBriefFromFirstReadTaskMirrorDiffZeroGitHubCalls(t *testing.T) {
	stub := testutil.StartGitHubStub(t)
	mirror, base, head := mirrorFixture(t, false)
	c := client(t)
	seedRecord(t, c, base, head)
	ctx := context.Background()
	id := "read-7-" + head[:8]
	if err := c.HSet(ctx, "task:"+id, "kind", "read", "ref", "https://forge.test/mas-bandwidth/nova-tools/pull/7",
		"title", "STREAM: nova sprint migration | read nova-tools#7 at "+head[:8]+" (c1)", "owner", "emma",
		"state", "open", "head", head).Err(); err != nil {
		t.Fatal(err)
	}
	fields, err := read.TaskFields(ctx, c, "", id)
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), id)
	var stdout, stderr strings.Builder
	if code := read.BriefTask(ctx, c, id, fields, mirror, out, &stdout, &stderr); code != 0 {
		t.Fatalf("exit %d, stderr %q", code, stderr.String())
	}
	if n := stub.Calls(); n != 0 {
		t.Fatalf("%d HTTP call(s) to GitHub; a read makes zero", n)
	}
	diffPath := filepath.Join(out, "diff.patch")
	for _, want := range []string{"READ BRIEF repo=nova-tools n=7 head=" + head[:8], "diff=" + diffPath, "github_calls=0", "task=" + id} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("receipt %q lacks %q", stdout.String(), want)
		}
	}
	b, err := os.ReadFile(filepath.Join(out, "brief.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"saved at " + diffPath, "HEAD: " + head, "base-sha: " + base} {
		if !strings.Contains(string(b), want) {
			t.Fatalf("brief lacks %q:\n%s", want, b)
		}
	}
	if strings.Contains(string(b), "gh api") || strings.Contains(string(b), "gh pr") {
		t.Fatalf("brief carries a GitHub invocation:\n%s", b)
	}
	got, err := os.ReadFile(diffPath)
	if err != nil {
		t.Fatal(err)
	}
	if want := gitRun(t, mirror, "diff", base+".."+head); strings.TrimSpace(string(got)) != want {
		t.Fatalf("diff.patch is not the mirror diff:\n%s\nwant\n%s", got, want)
	}
}

// TestReadBriefTaskReadsTheTaskHeadWhenTheRecordMoved: the record's head has
// moved on since the task was queued; the brief still reads the task's head
// (its diff and its CI) and the receipt names the record's head.
func TestReadBriefTaskReadsTheTaskHeadWhenTheRecordMoved(t *testing.T) {
	t.Parallel()

	mirror, base, head := mirrorFixture(t, false)
	c := client(t)
	seedRecord(t, c, base, head)
	ctx := context.Background()
	moved := strings.Repeat("e", 40)
	if err := c.HSet(ctx, read.Key("nova-tools", "7"), "head", moved).Err(); err != nil {
		t.Fatal(err)
	}
	fields := map[string]string{"kind": "read", "repo": "nova-tools", "pr": "7", "head": head}
	out := t.TempDir()
	var stdout, stderr strings.Builder
	if code := read.BriefTask(ctx, c, "r1", fields, mirror, out, &stdout, &stderr); code != 0 {
		t.Fatalf("exit %d, stderr %q", code, stderr.String())
	}
	for _, want := range []string{"head=" + head[:8], "ci=2", "record_head=eeeeeeee", "task=r1"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("receipt %q lacks %q", stdout.String(), want)
		}
	}
}
