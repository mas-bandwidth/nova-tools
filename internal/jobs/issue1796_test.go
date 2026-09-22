package jobs

import (
	"testing"
)

// TestIssue1796 reproduces nova-tools#1796: a node's dependents do not become ready
// when it is accepted, because nothing can accept it. The fix is a verb that accepts
// a node, which this test calls.
func TestIssue1796(t *testing.T) {
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
