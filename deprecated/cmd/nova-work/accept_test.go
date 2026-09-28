package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/jobs"
)

// writeAcceptGraph writes the issue #1796 shape: issues-sweep needs cutter-C2.
func writeAcceptGraph(t *testing.T) (path string, seed []byte) {
	t.Helper()
	seed, err := jobs.MarshalNodes([]jobs.Node{
		{ID: "issues-sweep", Needs: []string{"cutter-C2"}},
		{ID: "cutter-C2"},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	path = filepath.Join(t.TempDir(), "deps.json")
	if err := os.WriteFile(path, seed, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	return path, seed
}

func readReady(t *testing.T, path, node string) string {
	t.Helper()
	var stdout, stderr bytes.Buffer
	if code := run([]string{"ready", "--graph", path, "--node", node}, &stdout, &stderr, ""); code != 0 {
		t.Fatalf("ready exit = %d (stderr %q)", code, stderr.String())
	}
	return stdout.String()
}

// TestIssue1796AcceptGraphThroughRun drives the shipped command boundary for
// nova-tools#1796: run() with `accept --graph <path> --node <id>` accepts the node on
// disk, so `ready` then reads the dependent ready; a refused line leaves the file
// byte-identical and writes nothing to stdout.
func TestIssue1796AcceptGraphThroughRun(t *testing.T) {
	t.Parallel()

	path, seed := writeAcceptGraph(t)
	if got := readReady(t, path, "issues-sweep"); !strings.Contains(got, "ready=false") {
		t.Fatalf("before accept: %q, want ready=false", got)
	}

	for _, c := range []struct {
		name string
		args []string
	}{
		{"unknown node", []string{"accept", "--graph", path, "--node", "no-such-node"}},
		{"missing node", []string{"accept", "--graph", path}},
		{"stray argument", []string{"accept", "--graph", path, "--node", "cutter-C2", "extra"}},
		{"unknown flag", []string{"accept", "--graph", path, "--node", "cutter-C2", "--bogus"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := run(c.args, &stdout, &stderr, ""); code != 2 {
				t.Fatalf("exit = %d, want 2 (stdout %q stderr %q)", code, stdout.String(), stderr.String())
			}
			if stdout.Len() != 0 {
				t.Fatalf("refusal wrote stdout %q", stdout.String())
			}
			if !strings.HasPrefix(stderr.String(), "nova-work accept: ") {
				t.Fatalf("refusal = %q, want the accept verb's own refusal", stderr.String())
			}
			if got, err := os.ReadFile(path); err != nil || !bytes.Equal(got, seed) {
				t.Fatalf("graph changed on refusal (err %v):\n%s", err, got)
			}
		})
	}

	var stdout, stderr bytes.Buffer
	if code := run([]string{"accept", "--graph", path, "--node", "cutter-C2"}, &stdout, &stderr, ""); code != 0 {
		t.Fatalf("accept exit = %d (stderr %q)", code, stderr.String())
	}
	if stdout.String() != "ACCEPT OK node=cutter-C2\n" || stderr.Len() != 0 {
		t.Fatalf("accept printed stdout %q stderr %q", stdout.String(), stderr.String())
	}
	if got := readReady(t, path, "issues-sweep"); !strings.Contains(got, "ready=true") {
		t.Fatalf("after accept: %q, want ready=true read from disk", got)
	}
}

// TestIssue1796SocketAcceptIsNotTheGraphForm is the control: an accept line with no
// --graph is the resident session's socket verb, so it reaches the socket and never
// touches a graph file, even one named by a --node the graph holds.
func TestIssue1796SocketAcceptIsNotTheGraphForm(t *testing.T) {
	path, seed := writeAcceptGraph(t)
	socket, requests := fakeSession(t, "ACCEPT OK id=cutter-C2 rev=2")
	var stdout, stderr bytes.Buffer
	code := run([]string{"accept", "--session", socket, "--node", "cutter-C2"}, &stdout, &stderr, "")
	if code != 0 {
		t.Fatalf("socket accept exit = %d (stdout %q stderr %q)", code, stdout.String(), stderr.String())
	}
	if line := awaitRequest(t, requests); !strings.HasPrefix(line, "accept ") || strings.Contains(line, "graph") {
		t.Fatalf("socket got %q, want the socket accept line", line)
	}
	if stdout.String() != "ACCEPT OK id=cutter-C2 rev=2\n" {
		t.Fatalf("socket accept printed %q, want the session's reply", stdout.String())
	}
	if got, err := os.ReadFile(path); err != nil || !bytes.Equal(got, seed) {
		t.Fatalf("socket accept touched the graph (err %v):\n%s", err, got)
	}
}
