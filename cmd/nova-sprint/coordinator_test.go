package main

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// applies is how many writes the store has taken.
func (ta *testApp) applies() int { return ta.m.Calls["apply"] }

// A sprint's coordinator is set once, by the first init; a later init is
// refused unless its actor is that coordinator, and changes nothing.
func TestInitOnASprintWithACoordinatorIsTheCoordinators(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	before := ta.applies()
	code, _, errs := ta.do("init --actor intruder --readers reader-z --coordinator intruder")
	require.Equal(t, 2, code, "init by another actor: exit %d %q", code, errs)
	require.Contains(t, errs, "the coordinator's alone: coordinator, not intruder", "init by another actor: exit %d %q", code, errs)
	require.Equal(t, before, ta.applies(), "a refused init wrote")
	require.NotContains(t, ta.ok("where"), "reader-z", "a refused init added its reader")
	// the coordinator's own init again is accepted and keeps the coordinator
	ta.ok("init --readers reader-c")
	code, _, errs = ta.do("init --coordinator someone-else")
	require.Equal(t, 2, code, "init naming another coordinator: exit %d %q", code, errs)
	require.Contains(t, errs, "init does not change the coordinator", "init naming another coordinator: exit %d %q", code, errs)
}

// --actor has no default: it is --actor or NOVA_SPRINT_ACTOR, else a verb
// that writes is refused. A worker's verb is its --as member's; reads need
// no actor; the machine's verbs are the machine's.
func TestTheActorHasNoDefault(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --members m1,m2 --readers reader-a,reader-b")
	ta.ok("add --stream s1 --count 1")
	ta.a.getenv = func(k string) string {
		return map[string]string{"NOVA_SPRINT_REDIS": "mem:0"}[k]
	}
	before := ta.applies()
	for _, line := range []string{"add --stream s1 --count 1", "rank s1-1 --first", "start", "merge --stream s1", "ci s1-1 --green"} {
		code, _, errs := ta.do(line)
		assert.Equal(t, 2, code, "%s with no actor: exit %d %q", line, code, errs)
		assert.Contains(t, errs, "--actor <name> is required (or NOVA_SPRINT_ACTOR)", "%s with no actor: exit %d %q", line, code, errs)
	}
	require.Equal(t, before, ta.applies(), "a verb with no actor wrote")
	for _, line := range []string{"inbox", "where", "check", "card s1-1", "queue --as m1"} {
		code, _, errs := ta.do(line)
		assert.Equal(t, 0, code, "the read %s: exit %d %q", line, code, errs)
	}
	ta.deal(1)
	require.Contains(t, ta.ok("take --as m1 --limit 1"), "TAKE OK", "take as a member with no --actor")
	ta.ok("fleet beat m1")
	require.NotEmpty(t, ta.ok("tick"), "tick with no actor printed nothing")
}

// Every coordinator verb is the coordinator's alone: another actor is
// refused, naming the coordinator, and nothing is written.
func TestEveryCoordinatorVerbIsTheCoordinators(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --members m1,m2 --readers reader-a,reader-b,reader-c")
	ta.ok("add --stream s1 --count 2")
	ta.ok("add --stream s1 --sentinel s1-stop")
	lines := map[string]string{
		"add":           "add --stream s1 --count 1",
		"quack":         "quack --streams q --count 1 --repo https://example.com/quack.git",
		"release":       "release s1-stop --reason r",
		"resolve":       "resolve",
		"start":         "start",
		"stop":          "stop",
		"ask":           "ask",
		"accept":        "accept s1-1",
		"rework":        "rework s1-1 --fix f",
		"return":        "return s1-1",
		"drop":          "drop s1-1 --reason r",
		"rank":          "rank s1-1 --first",
		"brief":         "brief s1-1 --brief b",
		"move":          "move s1-2 --stream s2",
		"needs":         "needs s1-2 --cut s1-1 --reason r",
		"resume":        "resume --stream s1",
		"land":          "land --dry-run",
		"fleet up":      "fleet up m3",
		"fleet down":    "fleet down m1",
		"fleet level":   "fleet level",
		"fleet sync":    "fleet sync",
		"friend sync":   "friend sync",
		"friend down":   "friend down friend-a",
		"friend up":     "friend up friend-a",
		"reader add":    "reader add reader-d",
		"reader away":   "reader away reader-a",
		"reader up":     "reader up reader-a",
		"reader remove": "reader remove reader-c",
		"stream remove": "stream remove s1",
		"stream set":    "stream set s1 --read-tier pro",
		"set":           "set --read-tier pro",
		"wait":          "wait x --for 1m",
		"ack":           "ack x --reason r",
		"clear":         "clear --confirm sprint",
		"teardown":      "teardown --confirm sprint",
		"repair":        "repair",
		"goal set":      "goal set friend-a --file /dev/null",
		"goal drop":     "goal drop friend-a",
		"play":          "play --ticks 1",
		"init":          "init",
	}
	for _, v := range verbs {
		if verbClasses[v.name] != classCoordinator {
			continue
		}
		line, ok := lines[v.name]
		if !assert.True(t, ok, "coordinator verb %s has no line in this test", v.name) {
			continue
		}
		before := ta.applies()
		code, _, errs := ta.do(line + " --actor intruder")
		assert.Equal(t, 2, code, "%s by another actor: exit %d %q", line, code, errs)
		assert.Contains(t, errs, "the coordinator's alone: coordinator, not intruder", "%s by another actor: exit %d %q", line, code, errs)
		assert.Equal(t, before, ta.applies(), "%s by another actor wrote", line)
	}
	// the workers' verbs are anyone's who names the member
	ta.deal(2)
	code, _, errs := ta.do("take --as m1 --limit 1 --actor m1")
	require.Equal(t, 0, code, "take by the member: %d %q", code, errs)
	code, _, errs = ta.do("fleet beat m2 --actor m2")
	require.Equal(t, 0, code, "fleet beat by the member: %d %q", code, errs)
}

// Every verb has a class: who may run it.
func TestEveryVerbHasAClass(t *testing.T) {
	t.Parallel()
	for _, v := range verbs {
		assert.NotEmpty(t, verbClasses[v.name], "verb %s has no class (coordinator, worker, report, machine or read)", v.name)
	}
	for name := range verbClasses {
		found := false
		for _, v := range verbs {
			found = found || v.name == name
		}
		assert.True(t, found, "class of %s, which is no verb", name)
	}
	for _, name := range []string{"ack", "wait", "accept", "rework", "return", "drop", "rank", "release", "resume", "start", "stop", "clear", "add", "fleet down", "fleet up", "init"} {
		assert.Equal(t, classCoordinator, verbClasses[name], "%s is the coordinator's", name)
	}
	for _, name := range []string{"take", "finish", "read", "fleet beat"} {
		assert.Equal(t, classWorker, verbClasses[name], "%s is a worker's", name)
	}
}

// inbox --read moves the coordinator's cursor, so it is the coordinator's;
// any actor reads the inbox, and nothing another does hides anything.
func TestInboxReadIsTheCoordinators(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --members m1 --readers reader-a,reader-b")
	ta.ok("add --stream s1 --count 1")
	before := ta.applies()
	code, _, errs := ta.do("inbox --read --actor intruder")
	require.Equal(t, 2, code, "inbox --read by another: %d %q", code, errs)
	require.Contains(t, errs, "the coordinator's alone: coordinator, not intruder", "inbox --read by another: %d %q", code, errs)
	cur, _ := ta.m.Cursor(context.Background())
	require.Equal(t, before, ta.applies(), "a refused inbox --read moved the cursor to %q", cur)
	require.Empty(t, cur, "a refused inbox --read moved the cursor to %q", cur)
	code, _, errs = ta.do("inbox --actor intruder")
	require.Equal(t, 0, code, "inbox by another: %d %q", code, errs)
	ta.ok("inbox --read")
	cur, _ = ta.m.Cursor(context.Background())
	require.NotEmpty(t, cur, "the coordinator's inbox --read did not move the cursor")
}
