//go:build functional

package swarm

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStellaAbortReportsUnwrittenEvidence(t *testing.T) {
	t.Parallel()
	job := filepath.Join(t.TempDir(), "job")
	if err := os.WriteFile(job, []byte("a file prevents creating the job directory"), 0600); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	code := abort(SuperviseInput{Task: "audit-card", Slot: 1, Nonce: "fixture", Stdout: &out, Stderr: &errOut, Now: time.Now}, job, errors.New("fixture reservation changed"))
	if _, err := os.Stat(AbortedPath(job)); err == nil {
		t.Fatal("fixture unexpectedly wrote aborted.json")
	}
	if code == 0 || !strings.Contains(errOut.String(), "aborted.json") {
		t.Fatalf("lost durable abort evidence is undisclosed: exit=%d stdout=%q stderr=%q", code, out.String(), errOut.String())
	}
}
