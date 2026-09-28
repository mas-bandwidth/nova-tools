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
	t.Parallel()

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

// countingZeros is an endless stream of zero bytes that counts what was read
// from it, the /dev/zero shape of an oversized input.
type countingZeros struct{ n int64 }

func (z *countingZeros) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 0
	}
	z.n += int64(len(p))
	return len(p), nil
}

// TestReadBoundedConsumesAtMostTheBound is the allocation half of the three
// bounds (SPEC-WORK.md:843-845): an input past --max-bytes is refused having
// consumed at most max-bytes+1 bytes, so an endless stream cannot exhaust
// memory before the refusal (the hold on nova-tools#2742 at 2d120b4c).
func TestReadBoundedConsumesAtMostTheBound(t *testing.T) {
	t.Parallel()

	z := &countingZeros{}
	data, over, err := readBoundedFrom(z, 100)
	if err != nil {
		t.Fatalf("readBoundedFrom: %v", err)
	}
	if !over || data != nil {
		t.Fatalf("readBoundedFrom over an endless stream = (%d bytes, over=%v), want refused over=true", len(data), over)
	}
	if z.n > 101 {
		t.Fatalf("readBoundedFrom consumed %d bytes, want at most 101", z.n)
	}

	data, over, err = readBoundedFrom(strings.NewReader("abc"), 3)
	if err != nil || over || string(data) != "abc" {
		t.Fatalf("readBoundedFrom at the bound = (%q, over=%v, %v), want the whole input", data, over, err)
	}
}

// TestQuerySnapshotRefusesAnOversizedCache: the cache is held to the same
// --max-bytes as the snapshot and refused whole, naming the cache and the
// bound, while the snapshot itself is under it.
func TestQuerySnapshotRefusesAnOversizedCache(t *testing.T) {
	t.Parallel()

	snapshot, cache := writeSnapshotPair(t)
	body, err := os.ReadFile(cache)
	if err != nil {
		t.Fatalf("read the cache fixture: %v", err)
	}
	padded := append(body, bytes.Repeat([]byte(" "), 4096)...)
	if err := os.WriteFile(cache, padded, 0o644); err != nil {
		t.Fatalf("pad the cache fixture: %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := run([]string{"query", "--snapshot", snapshot,
		"--max-bytes", "2048", "--max-depth", "64", "--max-nodes", "4096", "--cache", cache,
		"--ask", "done", "--branch", "open"}, &stdout, &stderr, "")
	if code != 2 {
		t.Fatalf("query --snapshot with an oversized cache exit = %d, want 2 (stderr %q)", code, stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("oversized cache wrote stdout: %q", stdout.String())
	}
	if msg := stderr.String(); !strings.Contains(msg, "the cache at "+cache) || !strings.Contains(msg, "--max-bytes=2048") {
		t.Fatalf("refusal = %q, want it naming the cache and --max-bytes=2048", msg)
	}
}
