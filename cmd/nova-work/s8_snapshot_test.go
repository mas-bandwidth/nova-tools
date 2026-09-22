package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// querySnapshotFixture is a published snapshot the way a clip writes it
// (lisp/nova-work/src/state.lisp, state-canonical-form): the seed the set began
// at, and the append-only history, one envelope record per request with every
// event carrying the :rev that is its identity. Three envelopes at revs 1, 2
// and 3 pin the snapshot at revision 3 -- the max the engine's replay computes
// -- and that revision is what the answer has to carry.
const querySnapshotFixture = `(:seed ((:id "n1" :type :task) (:id "n2" :type :bug))
 :history ((:request "r3" :digest "d3" :events ((:kind :settle :node "n2" :by "rowan" :stamp "2026-09-20T10:00:00Z" :clock "2026-09-20T10:00:00Z" :request "r3" :generation-owner () :rev 3 :disposition :done)))
           (:request "r2" :digest "d2" :events ((:kind :evidence :node "n1" :by "rowan" :stamp "2026-09-20T09:00:00Z" :clock "2026-09-20T09:00:00Z" :request "r2" :generation-owner () :rev 2)))
           (:request "r1" :digest "d1" :events ((:kind :evidence :node "n1" :by "rowan" :stamp "2026-09-20T08:00:00Z" :clock "2026-09-20T08:00:00Z" :request "r1" :generation-owner () :rev 1)))))`

// writeSnapshotPair writes the fixture snapshot and its matching cache -- the
// cache's :state-sha256 is the sha of the snapshot's bytes, the identity the
// engine's own reader checks (lisp/nova-work/src/state-export.lisp,
// read-loaded-snapshot) -- and returns the two paths.
func writeSnapshotPair(t *testing.T) (snapshot, cache string) {
	t.Helper()
	snapshot = filepath.Join(t.TempDir(), "snapshot.sexp")
	if err := os.WriteFile(snapshot, []byte(querySnapshotFixture), 0o644); err != nil {
		t.Fatalf("write the snapshot fixture: %v", err)
	}
	sum := sha256.Sum256([]byte(querySnapshotFixture))
	cache = filepath.Join(filepath.Dir(snapshot), "cache.sexp")
	cacheForm := `(:schema "work-v1" :state-sha256 "` + hex.EncodeToString(sum[:]) + `")`
	if err := os.WriteFile(cache, []byte(cacheForm), 0o644); err != nil {
		t.Fatalf("write the cache fixture: %v", err)
	}
	return snapshot, cache
}

// TestQuerySnapshotReadsTheFile is the offline reader of docs/SPEC-WORK.md:291:
// `nova-work query --snapshot <file> --ask done --branch open`, with the four
// flags --snapshot carries, reads a regular file IN PROCESS and answers with
// the snapshot's revision. There is no socket anywhere in this test -- not one
// it could have dialled and not one it needed -- because a published snapshot
// is a file, and the file is the whole session (nova-tools#1787).
func TestQuerySnapshotReadsTheFile(t *testing.T) {
	snapshot, cache := writeSnapshotPair(t)

	var stdout, stderr bytes.Buffer
	code := run([]string{"query", "--snapshot", snapshot,
		"--max-bytes", "65536", "--max-depth", "64", "--max-nodes", "4096", "--cache", cache,
		"--ask", "done", "--branch", "open"}, &stdout, &stderr, "")
	if code != 0 {
		t.Fatalf("query --snapshot exit = %d, stderr = %s", code, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("query --snapshot wrote stderr: %q", stderr.String())
	}
	// The answer is the empty-by-construction one (SPEC-WORK.md:1791: no done
	// item is left in O) carrying the snapshot's revision in scope=, the field
	// the grammar reserves for the revision the answer is computed at.
	want := "QUERY OK ask=done scope=3 branch=open rows=0 shown=0 parses=2 replays=0\n"
	if got := stdout.String(); got != want {
		t.Fatalf("query --snapshot stdout = %q, want %q", got, want)
	}

	// A snapshot over --max-bytes is refused whole, naming that bound
	// (SPEC-WORK.md:843-845) -- never truncated, and never dialled.
	stdout.Reset()
	stderr.Reset()
	code = run([]string{"query", "--snapshot", snapshot,
		"--max-bytes", "100", "--max-depth", "64", "--max-nodes", "4096", "--cache", cache,
		"--ask", "done", "--branch", "open"}, &stdout, &stderr, "")
	if code != 2 {
		t.Fatalf("query --snapshot over --max-bytes exit = %d, want 2 (stderr %q)", code, stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("query --snapshot over --max-bytes wrote stdout: %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "--max-bytes") {
		t.Fatalf("refusal = %q, want it naming --max-bytes", stderr.String())
	}
	if strings.Contains(stderr.String(), "dial unix") {
		t.Fatalf("query --snapshot dialled the file rather than refusing it: %s", stderr.String())
	}
}
