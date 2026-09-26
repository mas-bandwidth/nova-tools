package card

import "testing"

// TestDependsOnAcceptsAStreamSentinel (#4318): the one DEPENDS-ON form takes
// a stream's sentinel, bare or as task:<id>, as a task edge; a spelling that
// is not a sentinel id is still refused.
func TestDependsOnAcceptsAStreamSentinel(t *testing.T) {
	t.Parallel()
	entries, deps, err := parseDepends("x", "swarm-cards:sentinel, task:ci:sentinel, A")
	if err != nil {
		t.Fatal(err)
	}
	if entries != "task:swarm-cards:sentinel,task:ci:sentinel,A" || len(deps) != 3 || deps[0].Kind != dependencyTask || deps[1].Kind != dependencyTask {
		t.Fatalf("entries %q deps %+v", entries, deps)
	}
	for _, bad := range []string{"Swarm:sentinel", "swarm cards:sentinel", "task:swarm cards:sentinel", "a:b:sentinel"} {
		if _, _, err := parseDepends("x", bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	if !localDependencyReady(dependencyTask, map[string]string{"where": "landed", "state": "landed"}) ||
		localDependencyReady(dependencyTask, map[string]string{"where": "waiting", "state": "waiting"}) {
		t.Fatal("a task edge is ready when its record is landed")
	}
}
