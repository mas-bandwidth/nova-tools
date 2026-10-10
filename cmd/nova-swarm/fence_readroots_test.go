//go:build slow || functional

package main

import (
	"github.com/mas-bandwidth/nova-tools/pkg/swarm"
)

// ISSUE #1463: THE HARNESS FENCE IGNORED THE WORKER DESCRIPTION'S `read_roots`, so a staged
// read the desk had granted was auto-rejected while the OS wall allowed it -- and the run
// still printed `NATIVE OK ... rc=0 harness=ok`. The card came back `not done` with the whole
// row owed and the spend already made.
//
// `read_roots` is the worker description's own declaration of what every job of this worker
// may READ: a bench-local mirror, a corpus, a toolchain under a user directory. It is
// validated at load (pkg/swarm/worker.go:227-236 refuses a relative entry, an empty one,
// one that does not exist and one that is not a directory) and `worker check` answers
// `WORKER OK`, so a coordinator has every reason to believe the path is usable.
//
// `native` read it nowhere. At dev 31e35195 `grep -n ReadRoots cmd/nova-swarm/native.go` is
// EMPTY: neither the wall's argv nor the harness's fence was ever told.
//
// THE WALL IS STILL THE REAL BOUNDARY (SPEC-SANDBOX rule 1). The harness's fence is a second,
// weaker one, and a second fence that denies what the first one grants can only cost cards.
// So what the fence is handed here is EXACTLY what the wall is handed, and never more.

// aReadRootWorker is a description whose only interesting field is `read_roots`. The model is
// the fake one every native seam test uses, so the run's pinned-model check passes.
func aReadRootWorker(roots ...string) *swarm.Worker {
	return &swarm.Worker{Name: "read-root-worker", Provider: "fake", Model: "fake-model", ReadRoots: roots}
}
