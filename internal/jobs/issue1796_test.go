package jobs

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// TestIssue1796 reproduces nova-tools#1796: a node's dependents do not become ready
// when it is accepted, because nothing can accept it. The fix is a verb that accepts
// a node, which this test calls.
func TestIssue1796(t *testing.T) {
	t.Parallel()

	g, err := Seed([]Node{
		{ID: "issues-sweep", Needs: []string{"cutter-C2"}},
		{ID: "cutter-C2"},
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	if ready, _ := g.Ready("issues-sweep"); ready {
		t.Fatalf("issues-sweep: ready before its dependency is accepted")
	}

	// The production change is a new verb, Accept, that marks a node terminal accepted.
	// Without it, cutter-C2 can never be met and issues-sweep can never be ready.
	if err := g.Accept("cutter-C2"); err != nil {
		t.Fatalf("Accept(cutter-C2): %v", err)
	}

	if ready, blocker := g.Ready("issues-sweep"); !ready {
		t.Fatalf("issues-sweep: not ready after its dependency is accepted, blocker: %v", blocker)
	}

	// An accepted node is not itself ready.
	if ready, _ := g.Ready("cutter-C2"); ready {
		t.Fatalf("cutter-C2: ready after being accepted")
	}
}

// TestIssue1796AcceptPersists is the file boundary nova-work accept --graph uses:
// write a graph, accept a node through AcceptFile, reread the file from disk, and see
// the dependent become ready. A missing node or an invalid graph leaves the file
// byte-identical.
func TestIssue1796AcceptPersists(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "deps.json")
	seed, err := MarshalNodes([]Node{
		{ID: "issues-sweep", Needs: []string{"cutter-C2"}},
		{ID: "cutter-C2"},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(path, seed, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	if err := AcceptFile(path, "no-such-node"); err == nil {
		t.Fatal("AcceptFile(no-such-node): accepted a node the graph does not hold")
	}
	if got, _ := os.ReadFile(path); !bytes.Equal(got, seed) {
		t.Fatalf("a refused accept changed the file:\n%s", got)
	}

	if err := AcceptFile(path, "cutter-C2"); err != nil {
		t.Fatalf("AcceptFile(cutter-C2): %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reread: %v", err)
	}
	g, err := ParseSeed(raw)
	if err != nil {
		t.Fatalf("reseed: %v", err)
	}
	if !g.Accepted("cutter-C2") {
		t.Fatal("cutter-C2 is not accepted in the file on disk")
	}
	if ready, blocker := g.Ready("issues-sweep"); !ready {
		t.Fatalf("issues-sweep: not ready from the file on disk, blocker: %v", blocker)
	}

	bad := filepath.Join(dir, "bad.json")
	invalid := []byte(`{"nodes":[{"id":"a","needs":["b"]},{"id":"b","needs":["a"]}]}` + "\n")
	if err := os.WriteFile(bad, invalid, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := AcceptFile(bad, "a"); err == nil {
		t.Fatal("AcceptFile on a cyclic graph: accepted")
	}
	if got, _ := os.ReadFile(bad); !bytes.Equal(got, invalid) {
		t.Fatalf("a refused accept changed the invalid file:\n%s", got)
	}
}

// TestIssue1796AcceptCommandBoundary drives the shipped verb from its argv: nova-work
// dispatches an accept line to the graph form only when NamesGraphFlag sees --graph,
// and cmdAccept is jobs.AcceptArgs plus the ACCEPT OK line. Success writes the file and
// the dependent reads ready from disk; every refusal, of the line or of the node,
// leaves the file byte-identical.
func TestIssue1796AcceptCommandBoundary(t *testing.T) {
	t.Parallel()

	for _, line := range [][]string{
		{"--graph", "g.json", "--node", "a"},
		{"--node", "a", "-graph=g.json"},
	} {
		if !NamesGraphFlag(line) {
			t.Fatalf("NamesGraphFlag(%q) = false: the graph form would reach the socket verb", line)
		}
	}
	for _, line := range [][]string{
		{"--session", "s.sock", "--node", "a", "--add", "x:k:s:p", "--reason", "r"},
		{"--node", "a", "--", "--graph"},
		{"graph", "--node", "a"},
	} {
		if NamesGraphFlag(line) {
			t.Fatalf("NamesGraphFlag(%q) = true: a socket accept line would be taken by the graph form", line)
		}
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "deps.json")
	seed, err := MarshalNodes([]Node{
		{ID: "issues-sweep", Needs: []string{"cutter-C2"}},
		{ID: "cutter-C2"},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(path, seed, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	for _, bad := range [][]string{
		{"--graph", path, "--node", "no-such-node"},
		{"--graph", path},
		{"--node", "cutter-C2"},
		{"--graph", path, "--node", "cutter-C2", "stray"},
		{"--graph", path, "--node", "cutter-C2", "--bogus"},
	} {
		if id, err := AcceptArgs(bad); err == nil {
			t.Fatalf("AcceptArgs(%q) accepted %q; want a refusal", bad, id)
		}
		if got, _ := os.ReadFile(path); !bytes.Equal(got, seed) {
			t.Fatalf("AcceptArgs(%q) refused but changed the file:\n%s", bad, got)
		}
	}

	id, err := AcceptArgs([]string{"--graph", path, "--node", " cutter-C2 "})
	if err != nil {
		t.Fatalf("AcceptArgs(--graph %s --node cutter-C2): %v", path, err)
	}
	if id != "cutter-C2" {
		t.Fatalf("AcceptArgs returned id %q, want cutter-C2", id)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reread: %v", err)
	}
	g, err := ParseSeed(raw)
	if err != nil {
		t.Fatalf("reseed: %v", err)
	}
	if ready, blocker := g.Ready("issues-sweep"); !ready {
		t.Fatalf("issues-sweep: not ready from the file on disk after the verb, blocker: %v", blocker)
	}
}
