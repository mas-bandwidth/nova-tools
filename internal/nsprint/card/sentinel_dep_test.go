package card

import "testing"

// TestDependsOnAcceptsAStreamSentinel (#4318): the one DEPENDS-ON form takes
// a stream's sentinel, bare or as task:<id>, as a task edge; a spelling that
// is not a sentinel id is still refused.
func TestDependsOnAcceptsAStreamSentinel(t *testing.T) {
	t.Parallel()
	// the older stream/<slug> spelling is the same edge: the stream's sentinel
	entries, deps, err := parseDepends("x", "swarm-cards:sentinel, task:ci:sentinel, A, stream/fleet")
	if err != nil {
		t.Fatal(err)
	}
	if entries != "task:swarm-cards:sentinel,task:ci:sentinel,A,task:fleet:sentinel" || len(deps) != 4 ||
		deps[0].Kind != dependencyTask || deps[1].Kind != dependencyTask || deps[3].Kind != dependencyTask || deps[3].Value != "fleet:sentinel" {
		t.Fatalf("entries %q deps %+v", entries, deps)
	}
	for _, bad := range []string{"Swarm:sentinel", "swarm cards:sentinel", "task:swarm cards:sentinel", "a:b:sentinel", "stream/Bad Slug"} {
		if _, _, err := parseDepends("x", bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	task := dependency{Kind: dependencyTask, Value: "t1"}
	stop := dependency{Kind: dependencyTask, Value: "fleet:sentinel"}
	if !localDependencyReady(task, map[string]string{"where": "landed", "state": "landed"}) ||
		localDependencyReady(task, map[string]string{"where": "waiting", "state": "waiting"}) ||
		!localDependencyReady(task, map[string]string{"where": "done", "where_ok": "ok", "state": "closed"}) {
		t.Fatal("a task edge is ready when its record is landed or done ok")
	}
	// a sentinel edge is met by its landing alone: done is a rename's
	if !localDependencyReady(stop, map[string]string{"where": "landed", "state": "landed"}) ||
		localDependencyReady(stop, map[string]string{"where": "done", "where_ok": "ok", "state": "closed"}) ||
		localDependencyReady(stop, map[string]string{"where": "waiting", "state": "waiting"}) {
		t.Fatal("a sentinel edge is met by landed alone")
	}
}
