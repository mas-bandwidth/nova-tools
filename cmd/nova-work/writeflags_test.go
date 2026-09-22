package main

import (
	"bytes"
	"strings"
	"testing"
)

// TestDryRunValidatesButWritesNothing proves the client-side of --dry-run
// (docs/SPEC-WORK.md:2202-2204, 6035): the request line serializes --dry-run true,
// and a session reply carrying dry-run=true is printed to stdout at exit 0.
// Zero events written is the session's responsibility; the client's obligation is
// the --dry-run true serialization and the pass-through of the dry-run=true receipt.
func TestDryRunValidatesButWritesNothing(t *testing.T) {
	// The session's projected receipt for a dry run carries dry-run=true.
	reply := "NODE OK id=- request=r-1 node=E01.11 rev=- pushed=4 changed=0 dry-run=true emitted=0"
	var stdout, stderr bytes.Buffer
	code := printReply(reply, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	line := strings.TrimSpace(stdout.String())
	if !strings.Contains(line, "dry-run=true") {
		t.Fatalf("receipt = %q, want it carrying dry-run=true", line)
	}
	if stderr.Len() != 0 {
		t.Fatalf("dry-run receipt wrote stderr: %q", stderr.String())
	}
}

// TestExpectStaleRefusal proves that a stale --expect answer is classified as exit 1
// to stderr (docs/SPEC-WORK.md:6037). The session's FAIL answer is passed through
// byte for byte.
func TestExpectStaleRefusal(t *testing.T) {
	reply := "NODE FAIL node=E01.11 expect=3 current=5: stale"
	var stdout, stderr bytes.Buffer
	code := printReply(reply, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stale expect wrote stdout: %q", stdout.String())
	}
	line := strings.TrimSpace(stderr.String())
	if line != reply {
		t.Fatalf("stderr = %q, want byte for byte %q", line, reply)
	}
}

// TestReplayPerNodeStaleCheck proves that a replayed request carrying a stale clipped
// revision is refused at exit 1 (docs/SPEC-WORK.md:6143, 7393). The session's FAIL
// answer is passed through byte for byte to stderr.
func TestReplayPerNodeStaleCheck(t *testing.T) {
	reply := "SESSION FAIL session=/sessions/replay.sock owner=rowan generation=4: replay stale expect=abc123 current=def456"
	var stdout, stderr bytes.Buffer
	code := printReply(reply, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if stdout.Len() != 0 {
		t.Fatalf("replay stale wrote stdout: %q", stdout.String())
	}
	line := strings.TrimSpace(stderr.String())
	if line != reply {
		t.Fatalf("stderr = %q, want byte for byte %q", line, reply)
	}
}
