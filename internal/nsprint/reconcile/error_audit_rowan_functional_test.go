//go:build functional

package reconcile

// Rowan's failure-guidance audit (2026-09-26). DoneAlready.onBase
// (done_already.go:273-285) maps every git failure -- a corrupt mirror, git
// missing from PATH, the 10 s timeout -- to ("wait", "<sha> is not in the
// mirror of <repo> yet"). A broken mirror then reads as "not fetched yet" on
// every pass and nobody is told to repair it. Only exit 1 of cat-file -e
// means absent; anything else should be an error naming the mirror, the git
// command and its stderr, with the mirror repair verb.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRowanAuditBrokenMirrorIsNotNotYetFetched(t *testing.T) {
	t.Parallel()
	mirror := filepath.Join(t.TempDir(), "nova-tools.git")
	if err := os.MkdirAll(filepath.Join(mirror, "objects"), 0o755); err != nil { // objects/ but no repository
		t.Fatal(err)
	}
	d := &DoneAlready{Mirror: func(string) string { return mirror }}
	verdict, why := d.onBase(context.Background(), "nova-tools", "0123456789abcdef0123456789abcdef01234567", "dev")
	if verdict == "wait" && strings.Contains(why, "yet") {
		t.Fatalf("a mirror git cannot open reads as %q %q; want the git failure and the mirror named (nova-sprint mirror check/refresh)", verdict, why)
	}
}
