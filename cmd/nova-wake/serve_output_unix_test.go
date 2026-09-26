//go:build unix

package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// spawn hands back the command's exit code AND the tail of what it said,
// stdout and stderr both, capped and on one line; a command that said
// nothing hands back nothing. A shell script stands in for the receiver so
// no environment is set.
func TestSpawnReturnsTheTailOfWhatTheCommandSaid(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	loud := filepath.Join(dir, "loud")
	if err := os.WriteFile(loud, []byte("#!/bin/sh\necho 'to stdout first'\nprintf 'harness: out of credits\\nsee the account page\\n' >&2\nexit 7\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	quiet := filepath.Join(dir, "quiet")
	if err := os.WriteFile(quiet, []byte("#!/bin/sh\nexit 3\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	var errb strings.Builder
	s := &server{onNote: loud, stdout: io.Discard, stderr: &errb}
	rc, said := s.spawn(context.Background(), []string{"aaa111"})
	if rc != 7 || said != "to stdout first harness: out of credits see the account page" {
		t.Fatalf("spawn = %d %q, want 7 and the command's words on one line", rc, said)
	}
	s = &server{onNote: quiet, stdout: io.Discard, stderr: &errb}
	if rc, said := s.spawn(context.Background(), []string{"aaa111"}); rc != 3 || said != "" {
		t.Fatalf("spawn = %d %q, want 3 and nothing said", rc, said)
	}
	if errb.Len() != 0 {
		t.Fatalf("a command that ran is not a WAKE POLL failure:\n%s", errb.String())
	}
}
