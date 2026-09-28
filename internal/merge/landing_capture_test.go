package merge

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/testbin"
)

// nova-tools #2455: the landing path reads a pull request's comments and reviews, and
// each of those is a capture a PARSER reads. A capture cut
// at execOutputCap (64 KiB) is not a shorter answer, it is JSON that does not decode, or a
// TSV that ends mid-run; tonight's lanes dropped #2421 (comments=90459 B) and #2522
// (76006 B) with "over the 65536-byte gh cap". These controls drive the landing reads
// END TO END -- GH.Verdicts -- through Exec, the production Runner, against a
// stub gh on PATH, so the control fails if either read goes back through the capped Run.
// (GH.Checks no longer invokes gh at all: CI is read from Redis, #2924, and cisource_test
// proves it never reads a check-run, so there is no check-runs capture left to cap.) (A fake Runner would never have exercised the cap: it hands back whatever whole
// string the test built.)

// writeLandingStubGH writes a bash "gh" into dir that answers the landing reads by
// substring on its arguments: "/comments" and "/reviews" cat a fixture. Anything else
// fails loudly.
func writeLandingStubGH(t *testing.T, dir string) {
	t.Helper()
	script := `#!/bin/bash
for a in "$@"; do
  case "$a" in
    */comments*) cat "$NOVA_MERGE_TEST_COMMENTS"; exit 0 ;;
    */reviews*) cat "$NOVA_MERGE_TEST_REVIEWS"; exit 0 ;;
  esac
done
echo "stub gh: unhandled arguments: $*" >&2
exit 1
`
	if err := testbin.WriteExecutable(filepath.Join(dir, "gh"), []byte(script), 0o755); err != nil {
		t.Fatalf("could not write the stub gh: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// TestCommentsCaptureOverSixtyFourKiBIsReadWhole is #2455's DONE-WHEN: a fake gh answering
// ~90 KB of comments JSON -- the size of #2421's thread -- must yield EVERY comment through
// GH.Verdicts, the read batch and land make, none truncated and none refused as "over the
// 65536-byte gh cap".
func TestCommentsCaptureOverSixtyFourKiBIsReadWhole(t *testing.T) {
	dir := t.TempDir()
	writeLandingStubGH(t, dir)

	const head = "2222222222222222222222222222222222222222"
	type rawComment struct {
		ID        int64                  `json:"id"`
		User      struct{ Login string } `json:"user"`
		Body      string                 `json:"body"`
		CreatedAt string                 `json:"created_at"`
	}
	// Every comment is a typed HOLD line followed by a long read, so every one of them is
	// a Verdict the fold must see -- and a HOLD is the verdict whose loss costs most: a
	// prefix that dropped the tail ones would hand the gate a held pull request as clear.
	var comments []rawComment
	for i := 0; len(comments) == 0 || mustLen(t, comments) < 90*1024; i++ {
		c := rawComment{
			ID:        int64(5000 + i),
			CreatedAt: fmt.Sprintf("2026-09-22T%02d:%02d:00Z", (i/60)%24, i%60),
			Body: fmt.Sprintf("DISPOSITION who=emma head=%s verdict=HOLD\n\nread #%04d: %s",
				head, i, strings.Repeat("the diff at head does what the card says; ", 12)),
		}
		c.User.Login = "emma"
		comments = append(comments, c)
	}
	blob, err := json.Marshal(comments)
	if err != nil {
		t.Fatal(err)
	}
	if len(blob) <= execOutputCap {
		t.Fatalf("fixture is %d bytes, want more than execOutputCap (%d) or this test proves nothing", len(blob), execOutputCap)
	}
	t.Logf("comments fixture: %d comments, %d bytes (execOutputCap %d)", len(comments), len(blob), execOutputCap)

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
	got, err := h.Verdicts(2421, VerdictOpts{CurrentHead: head})
	if err != nil {
		t.Fatalf("Verdicts refused a %d-byte comments capture: %v", len(blob), err)
	}
	if len(got) != len(comments) {
		t.Fatalf("Verdicts yielded %d verdicts, want one per comment (%d): the capture was cut", len(got), len(comments))
	}
	seen := map[string]bool{}
	for _, v := range got {
		seen[v.ID] = true
	}
	for _, c := range comments {
		if id := fmt.Sprintf("comment:%d", c.ID); !seen[id] {
			t.Fatalf("comment %s is missing from the verdicts: the capture was cut", id)
		}
	}
	if last := got[len(got)-1]; last.Word != "hold" || last.Head != head {
		t.Fatalf("the last comment folded to word=%q head=%q, want hold at %s", last.Word, last.Head, head)
	}
}

func mustLen(t *testing.T, v interface{}) int {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return len(b)
}
