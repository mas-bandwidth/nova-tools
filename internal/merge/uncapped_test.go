package merge

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// nova-tools #2654: execOutputCap (gitops.go) held EVERY subprocess capture to 64 KiB,
// including the three a parser reads whole rather than a person -- a pull request's own
// JSON, its comments, its reviews. #2522 measured a paginated review-and-comment capture
// at 63,499 bytes: over the cap, cut mid-object, and unparsable, so the pull request it
// was read for never landed. RunUncapped (gitops.go) and GH.ghWhole (host.go) are the
// fix: those three captures go through it now, and execOutputCap stays exactly what it
// was everywhere else, including the TAIL a failing uncapped command's own error message
// still keeps.
//
// This file's positive control shells to a real "gh" -- a stub script placed on PATH,
// per this package's existing fake/exec seam not covering a real subprocess -- because
// the bug lived in Exec, the production Runner, and a fake Runner (as gh_verdict_test.go
// uses) never exercises Exec at all.

// writeStubGH writes a small bash script named "gh" into dir and returns dir, for
// prepending to PATH. It never invokes zsh: the shebang and every command in it are
// bash's own. The script answers three shapes, by substring on its own argument list,
// mirroring how this package's fake Runners already tell one gh call from another:
//   - an argument containing "comments"  -> cats $NOVA_MERGE_TEST_COMMENTS
//   - an argument containing "reviews"   -> cats $NOVA_MERGE_TEST_REVIEWS
//   - "pr view ..."                      -> cats $NOVA_MERGE_TEST_PR
func writeStubGH(t *testing.T, dir string) {
	t.Helper()
	script := `#!/bin/bash
for a in "$@"; do
  case "$a" in
    *comments*) cat "$NOVA_MERGE_TEST_COMMENTS"; exit 0 ;;
    *reviews*) cat "$NOVA_MERGE_TEST_REVIEWS"; exit 0 ;;
  esac
done
if [ "$1" = "pr" ] && [ "$2" = "view" ]; then
  cat "$NOVA_MERGE_TEST_PR"
  exit 0
fi
echo "stub gh: unhandled arguments: $*" >&2
exit 1
`
	path := filepath.Join(dir, "gh")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("could not write the stub gh: %v", err)
	}
}

// putStubGHOnPath prepends dir to PATH for the lifetime of the test.
func putStubGHOnPath(t *testing.T, dir string) {
	t.Helper()
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// TestGHWholeReadsAPaginatedCommentsCaptureWhole is the positive control: a fake gh
// answers 70,000-plus bytes of comments, the last of which is the typed line a merge
// verdict is read from, and the fix must decode every one of them -- not a 64 KiB prefix
// that ends mid-object.
func TestGHWholeReadsAPaginatedCommentsCaptureWhole(t *testing.T) {
	dir := t.TempDir()
	writeStubGH(t, dir)
	putStubGHOnPath(t, dir)

	const fillerCount = 700
	const disposition = "DISPOSITION who=emma head=" + "1111111111111111111111111111111111111111" + " verdict=APPROVE score=10"

	type rawComment struct {
		ID        int64                  `json:"id"`
		User      struct{ Login string } `json:"user"`
		Body      string                 `json:"body"`
		CreatedAt string                 `json:"created_at"`
	}
	var comments []rawComment
	for i := 0; i < fillerCount; i++ {
		comments = append(comments, rawComment{
			ID:        int64(1000 + i),
			CreatedAt: "2026-09-22T00:00:00Z",
			Body: fmt.Sprintf(
				"filler comment #%04d, padding this body so the capture crosses the 64 KiB cap: %s",
				i, strings.Repeat("padding-", 8)),
		})
	}
	// The LAST comment is the typed line a merge verdict is read from -- exactly the
	// #2522 shape: the evidence that matters sits at the end of a paginated capture a
	// 64 KiB prefix would have cut long before reaching.
	comments = append(comments, rawComment{ID: 9999, CreatedAt: "2026-09-22T01:00:00Z", Body: disposition})

	blob, err := json.Marshal(comments)
	if err != nil {
		t.Fatalf("could not build the fixture: %v", err)
	}
	if len(blob) <= execOutputCap {
		t.Fatalf("fixture is %d bytes, want more than execOutputCap (%d) or this test proves nothing", len(blob), execOutputCap)
	}
	t.Logf("fixture comments capture is %d bytes (execOutputCap is %d)", len(blob), execOutputCap)

	commentsPath := filepath.Join(dir, "comments.json")
	if err := os.WriteFile(commentsPath, blob, 0o644); err != nil {
		t.Fatal(err)
	}
	reviewsPath := filepath.Join(dir, "reviews.json")
	if err := os.WriteFile(reviewsPath, []byte("[]"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NOVA_MERGE_TEST_COMMENTS", commentsPath)
	t.Setenv("NOVA_MERGE_TEST_REVIEWS", reviewsPath)

	h := NewGH("o/n", 30*time.Second, Exec{})
	raw, err := h.ghWhole("api", "--paginate", fmt.Sprintf("repos/%s/issues/%d/comments", h.Repo, 5))
	if err != nil {
		t.Fatalf("h.ghWhole returned an error over a %d-byte capture: %v", len(blob), err)
	}
	if len(raw) != len(blob) {
		t.Fatalf("ghWhole returned %d bytes, fixture was %d bytes: the capture was cut", len(raw), len(blob))
	}

	var decoded []rawComment
	if err := decodeArrays(raw, &decoded); err != nil {
		t.Fatalf("the whole capture did not decode as JSON: %v (a cut capture ends mid-object; this is #2522)", err)
	}
	if len(decoded) != len(comments) {
		t.Fatalf("parsed comment count = %d, want the fixture's %d", len(decoded), len(comments))
	}
	if decoded[len(decoded)-1].Body != disposition {
		t.Fatalf("the last comment decoded as %q, want the typed disposition line %q", decoded[len(decoded)-1].Body, disposition)
	}
	if !strings.Contains(raw, disposition) {
		t.Fatalf("the raw capture does not contain the last typed line at all")
	}

	// The reviews capture goes through the same ghWhole path (host.go's Verdicts calls
	// it exactly like the comments call); confirm the small case still round-trips.
	rawReviews, err := h.ghWhole("api", "--paginate", fmt.Sprintf("repos/%s/pulls/%d/reviews", h.Repo, 5))
	if err != nil {
		t.Fatalf("h.ghWhole (reviews) returned an error: %v", err)
	}
	if strings.TrimSpace(rawReviews) != "[]" {
		t.Fatalf("reviews capture = %q, want the fixture's []", rawReviews)
	}
}

// TestGHPRReadsALargePullRequestBodyWhole is the third named capture: GH.PR's own JSON,
// which can carry more typed history in its body than 64 KiB holds.
func TestGHPRReadsALargePullRequestBodyWhole(t *testing.T) {
	dir := t.TempDir()
	writeStubGH(t, dir)
	putStubGHOnPath(t, dir)

	bigBody := "PR body start-marker\n" + strings.Repeat("evidence line, padding this out.\n", 3000) + "PR body end-marker"
	pr := map[string]interface{}{
		"number":              5,
		"author":              map[string]string{"Login": "rowan"},
		"baseRefName":         "dev",
		"headRefName":         "rowan/merge-capture-uncap",
		"headRepositoryOwner": map[string]string{"Login": "o"},
		"headRefOid":          strings.Repeat("a", 40),
		"mergeable":           "MERGEABLE",
		"isDraft":             false,
		"url":                 "https://example.invalid/pr/5",
		"title":               "a pull request with a large body",
		"body":                bigBody,
		"state":               "OPEN",
		"mergedAt":            "",
		"mergeCommit":         map[string]string{"oid": ""},
	}
	blob, err := json.Marshal(pr)
	if err != nil {
		t.Fatal(err)
	}
	if len(blob) <= execOutputCap {
		t.Fatalf("PR fixture is %d bytes, want more than execOutputCap (%d)", len(blob), execOutputCap)
	}
	prPath := filepath.Join(dir, "pr.json")
	if err := os.WriteFile(prPath, blob, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NOVA_MERGE_TEST_PR", prPath)

	h := NewGH("o/n", 30*time.Second, Exec{})
	got, err := h.PR(5)
	if err != nil {
		t.Fatalf("h.PR returned an error over a %d-byte pull request: %v", len(blob), err)
	}
	if !strings.HasSuffix(got.Body, "PR body end-marker") {
		t.Fatalf("pull request body was cut: last 40 bytes = %q", lastN(got.Body, 40))
	}
	if got.Body != bigBody {
		t.Fatalf("pull request body length = %d, want the fixture's %d", len(got.Body), len(bigBody))
	}
}

func lastN(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}

// TestRunUncappedKeepsOnlyTheTailOfAFailingCommandsOutput is the negative control: a
// FAILING command's captured output is still held to execOutputCap, and to the TAIL of
// it, not a whole 100 KB and not the head. It uses this package's other existing
// fake/exec seam -- re-executing the test binary itself as the child, exactly as
// TestExecRunCapsRunawayOutputAndMarksIt (guard_test.go) already does for Run -- because
// the claim under test is about Exec, not about anything gh-shaped.
func TestRunUncappedKeepsOnlyTheTailOfAFailingCommandsOutput(t *testing.T) {
	const headMarker = "HEAD-MARKER-this-must-not-survive"
	const tailMarker = "TAIL-MARKER-this-must-survive"
	if os.Getenv("NOVA_MERGE_UNCAPPED_FAIL_HELPER") == "1" {
		_, _ = io.WriteString(os.Stdout, headMarker)
		_, _ = io.WriteString(os.Stdout, strings.Repeat("x", 100*1024))
		_, _ = io.WriteString(os.Stdout, tailMarker)
		os.Exit(3)
	}
	t.Setenv("NOVA_MERGE_UNCAPPED_FAIL_HELPER", "1")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := (Exec{}).RunUncapped(ctx, "", os.Args[0], "-test.run=TestRunUncappedKeepsOnlyTheTailOfAFailingCommandsOutput")
	if err == nil {
		t.Fatalf("a helper that exits 3 must be reported as an error")
	}
	if len(out) > execOutputCap+200 {
		t.Fatalf("a failing command's captured output is %d bytes, want at most execOutputCap (%d) plus its truncation marker", len(out), execOutputCap)
	}
	if !strings.Contains(out, tailMarker) {
		t.Fatalf("the captured error output dropped the TAIL, which is the end a person reads a failure from: %q", lastN(out, 120))
	}
	if strings.Contains(out, headMarker) {
		t.Fatalf("the captured error output kept the HEAD of a 100 KB+ stream instead of the tail -- the cap did not move as required")
	}
}
