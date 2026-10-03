package main

import (
	"context"

	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// Who may run a verb. Every verb has one class (a class test holds it):
//
//   - coordinator: the sprint's coordinator alone (init --coordinator, set
//     once by the first init); another actor is refused and nothing written.
//   - worker: a fleet member or a reader, named by --as (fleet beat: the
//     member; friend beat: the friend); its actor is that name, whatever
//     --actor says.
//   - report: an outside actor's report (merge, ci), anyone's who names it.
//   - machine: the run loop's (tick, run), recorded as the machine.
//   - read: changes nothing and needs no actor.
//   - seat: coordinator <name>, the seat moved: given by its holder or the
//     sprint's owner, or taken by the one taking it with the owner's name
//     (sprint.NotSeat); the verb judges who may, and the step again.
//
// Every class but read, machine and worker wants an actor: --actor or
// NOVA_SPRINT_ACTOR; there is no default.
const (
	classCoordinator = "coordinator"
	classWorker      = "worker"
	classReport      = "report"
	classMachine     = "machine"
	classRead        = "read"
	classSeat        = "seat"
)

var verbClasses = map[string]string{
	"init": classCoordinator, "add": classCoordinator, "quack": classCoordinator, "release": classCoordinator, "resolve": classCoordinator,
	"start": classCoordinator, "stop": classCoordinator, "ask": classCoordinator, "accept": classCoordinator,
	"rework": classCoordinator, "return": classCoordinator, "drop": classCoordinator, "rank": classCoordinator, "brief": classCoordinator, "move": classCoordinator,
	"resume": classCoordinator, "land": classCoordinator, "fleet up": classCoordinator, "fleet down": classCoordinator,
	"fleet level": classCoordinator, "fleet sync": classCoordinator, "friend sync": classCoordinator, "friend down": classCoordinator, "friend up": classCoordinator, "reader add": classCoordinator, "reader away": classCoordinator, "reader up": classCoordinator, "reader remove": classCoordinator, "stream remove": classCoordinator, "stream set": classCoordinator, "set": classCoordinator, "wait": classCoordinator,
	"ack": classCoordinator, "clear": classCoordinator, "teardown": classCoordinator, "repair": classCoordinator,
	"goal set": classCoordinator, "goal drop": classCoordinator, "play": classCoordinator,

	"take": classWorker, "finish": classWorker, "read": classWorker, "fleet beat": classWorker, "friend beat": classWorker,

	"merge": classReport, "ci": classReport,

	"tick": classMachine, "run": classMachine, "friend clean": classMachine,

	"queue": classRead, "inbox": classRead, "card": classRead, "log": classRead, "check": classRead, "where": classRead, "dashboard": classRead, "routes": classRead, "stats": classRead,
	"goal show": classRead, "handover": classRead,

	"coordinator": classSeat,
}

// orActor is a worker's actor: the member or reader it names, whatever
// --actor or NOVA_SPRINT_ACTOR say, so the record names the worker the verb
// was run as, as the server records a worker's verb (serve.go).
func (c *common) orActor(name string) {
	c.actor = name
}

// needsActor is why the verb may not run with no actor: "" is may.
func needsActor(c common) string {
	switch verbClasses[c.verb] {
	case "", classRead, classMachine:
		return ""
	}
	if c.actor != "" {
		return ""
	}
	return "--actor <name> is required (or NOVA_SPRINT_ACTOR): who acts is recorded with every change, and there is no default; nothing was changed"
}

// coordinatorOnly is why the actor may not run a coordinator verb: "" is
// may. The first init names the coordinator (--coordinator, else the actor);
// every later coordinator verb, init included, is that actor's alone. A
// store with no coordinator takes init and teardown only.
func coordinatorOnly(ctx context.Context, st *store.Store, c common) (string, error) {
	if verbClasses[c.verb] != classCoordinator {
		return "", nil
	}
	return coordinatorsAlone(ctx, st, c)
}

// coordinatorsAlone is why the actor may not do what is the coordinator's
// alone (a coordinator verb, or inbox --read, which moves the coordinator's
// cursor): "" is may.
func coordinatorsAlone(ctx context.Context, st *store.Store, c common) (string, error) {
	coord, err := st.B.Coordinator(ctx)
	if err != nil {
		return "", err
	}
	switch {
	case coord == "" && (c.verb == "init" || c.verb == "teardown"):
		return "", nil
	case coord == "":
		return c.verb + " is the coordinator's, and the sprint has no coordinator; run: nova-sprint init --coordinator <name>", nil
	case c.actor != coord:
		return c.verb + " is the coordinator's alone: " + coord + ", not " + c.actor + "; nothing was changed", nil
	case c.verb == "init" && c.coordinator != "" && c.coordinator != coord:
		return "the sprint's coordinator is " + coord + ", and init does not change the coordinator; nothing was changed", nil
	}
	return "", nil
}
