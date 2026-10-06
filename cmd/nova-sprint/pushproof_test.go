package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// fakeSession is a session the push loop delivers into: every text it took,
// and the exit its deliver command answers.
type fakeSession struct {
	mu    sync.Mutex
	texts []string
	exit  int
}

func (f *fakeSession) Deliver(_ context.Context, text string) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.texts = append(f.texts, text)
	return f.exit, nil
}

func (f *fakeSession) last() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.texts) == 0 {
		return ""
	}
	return f.texts[len(f.texts)-1]
}

// nonceOf is the nonce a push check carries.
func nonceOf(t *testing.T, text string) string {
	t.Helper()
	rest, ok := strings.CutPrefix(text, sprint.PushCheckPrefix)
	require.True(t, ok, "no push check: %q", text)
	return strings.SplitN(rest, "\n", 2)[0]
}

// pushProofSprint is a sprint whose coordinator is name, armed for the push proof with
// a session of its own, and a home where its inbox is and seat install writes.
func pushProofSprint(t *testing.T, name string) (*testApp, *fakeSession) {
	t.Helper()
	ta := newTestApp(t)
	env := map[string]string{"NOVA_SPRINT_REDIS": "mem:0", "NOVA_SPRINT_ACTOR": name}
	ta.a.getenv = func(k string) string { return env[k] }
	home := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(home, name+"-working", "inbox"), 0o755))
	ta.a.home = func() (string, error) { return home, nil }
	ta.a.goos = "linux"
	ta.a.executable = func() (string, error) { return "/opt/nova/bin/nova-sprint", nil }
	ta.a.seatLoad = func(string, string, string) error { return nil }
	s := &fakeSession{}
	pushTests.Store(name, s)
	t.Cleanup(func() { pushTests.Delete(name) })
	return ta, s
}

func (ta *testApp) step(d time.Duration) {
	ta.mu.Lock()
	ta.now = ta.now.Add(d)
	ta.mu.Unlock()
}

// refusedPushDown runs the line and wants it refused with the one PUSH DOWN line
// saying why, and nothing written.
func (ta *testApp) refusedPushDown(line, why string) {
	ta.t.Helper()
	before := ta.applies()
	code, out, errs := ta.do(line)
	require.Equal(ta.t, 2, code, "%s: exit %d\n%s%s", line, code, out, errs)
	require.Contains(ta.t, errs, "REFUSED: PUSH DOWN: ", line)
	require.Contains(ta.t, errs, why, line)
	require.Contains(ta.t, errs, "; run: nova-sprint seat install --actor ", line)
	require.Equal(ta.t, 1, strings.Count(errs, "\n"), "%s: one line: %q", line, errs)
	require.Equal(ta.t, before, ta.applies(), "%s was refused and wrote", line)
}

// The seat is held only by a session the push loop reaches (the owner,
// 2026-10-05: "Your inbox MUST push to you." and "it won't work until the AI
// does this"): until a pong carrying the nonce of a check delivered through
// the harness's adapter comes back from the session, every coordinator verb is
// refused with one PUSH DOWN line and its setup command, and the seat is given
// to no name without one; seat install, seat push, seat pong and the reads
// still run. The proof is re-proven every ten minutes by the same round trip,
// and a proof older than that and its answer bound refuses again.
func TestTheSeatRefusesEveryVerbUntilThePushIsProven(t *testing.T) {
	t.Parallel()
	const name = "pushproof-a"
	ta, session := pushProofSprint(t, name)
	ctx := context.Background()
	ta.ok("init --readers reader-a,reader-b --members m1,m2 --owner glenn")

	// every coordinator verb, init again included, is refused by the gate
	st, err := ta.a.store(common{redis: "mem:0", actor: name})
	require.NoError(t, err)
	for v, class := range verbClasses {
		if class != classCoordinator {
			continue
		}
		why, err := coordinatorOnly(ctx, st, common{verb: v, actor: name})
		require.NoError(t, err, v)
		assert.True(t, strings.HasPrefix(why, "PUSH DOWN: "+name+" has no push target recorded"), "%s: %q", v, why)
	}
	ta.refusedPushDown("add --stream s1 --count 2", name+" has no push target recorded")
	ta.refusedPushDown("init --readers reader-c", name+" has no push target recorded")
	ta.refusedPushDown("start", name+" has no push target recorded")
	// the reads, the machine and the seat's own verbs still run
	ta.ok("where")
	ta.ok("tick")
	code, out, _ := ta.do("seat push")
	require.Equal(t, 1, code, out)
	assert.Contains(t, out, "PUSH DOWN name="+name+" harness=- target=-", out)
	// seat check says PUSH DOWN with the remedy
	ta.a.outside = mockHealthyOutside()
	code, out, _ = ta.do("seat check")
	require.Equal(t, 1, code, out)
	assert.Contains(t, out, "MACHINERY push DOWN holder="+name+" harness=- why=", out)
	assert.Contains(t, out, "remedy=\"nova-sprint seat install --actor "+name, out)

	// the seat is given to no name without a live proof
	other := "pushproof-b"
	pushTests.Store(other, &fakeSession{})
	t.Cleanup(func() { pushTests.Delete(other) })
	code, _, errs := ta.do("coordinator " + other + " --reason r")
	require.Equal(t, 2, code, errs)
	assert.Contains(t, errs, "coordinator REFUSED: PUSH DOWN: "+other+" has no push target recorded", errs)
	assert.Equal(t, name, ta.holder())

	// a harness whose adapter is the Stub cannot hold the seat
	target := t.TempDir()
	code, _, errs = ta.do("seat install --redis 127.0.0.1:6381 --harness copilot --target " + target)
	require.Equal(t, 2, code, errs)
	assert.Contains(t, errs, "copilot's adapter is the Stub", errs)
	_, ok, err := readPush(ctx, st, name)
	require.NoError(t, err)
	require.False(t, ok, "a Stub's install wrote a push record")
	code, _, errs = ta.do("seat install --redis 127.0.0.1:6381 --target " + target)
	require.Equal(t, 2, code, errs)
	assert.Contains(t, errs, "--harness <name> is required", errs)

	// the push target is recorded; the seat is still down until the round trip
	out = ta.ok("seat install --redis 127.0.0.1:6381 --harness opencode --target " + target)
	assert.Contains(t, out, "SEAT INSTALL OK unit=", out)
	assert.Contains(t, out, "push: opencode into "+target, out)
	ta.refusedPushDown("add --stream s1 --count 2", "no push check has been delivered into "+name+"'s opencode session yet")

	// the push loop delivers a check through the adapter, never a file
	src := &storeSource{st: st}
	var said bytes.Buffer
	ta.a.prove(ctx, src, name, false, &said)
	nonce := nonceOf(t, session.last())
	assert.Contains(t, said.String(), "PUSH CHECK name="+name+" nonce="+nonce+"\n")
	assert.Contains(t, session.last(), "nova-sprint seat pong "+nonce+" --actor "+name)
	ta.refusedPushDown("add --stream s1 --count 2", "the push check "+nonce+" went into "+name+"'s opencode session and no pong carrying it came back")
	said.Reset()
	ta.a.prove(ctx, src, name, false, &said)
	assert.Empty(t, said.String(), "a check is delivered again before its answer bound")

	// only the pong carrying the delivered nonce counts, and once
	code, _, errs = ta.do("seat pong 0000000000000000")
	require.Equal(t, 2, code, errs)
	assert.Contains(t, errs, "only the session's answer to the last check counts", errs)
	out = ta.ok("seat pong " + nonce)
	assert.Contains(t, out, "SEAT PONG OK name="+name+" nonce="+nonce, out)
	code, _, errs = ta.do("seat pong " + nonce)
	require.Equal(t, 2, code, errs)
	assert.Contains(t, errs, "counted already", errs)

	// proven: the coordinator's verbs run, and seat push says so
	ta.ok("add --stream s1 --count 2")
	assert.Contains(t, ta.ok("seat push"), "PUSH OK name="+name+" harness=opencode")
	_, out, _ = ta.do("seat check")
	assert.Contains(t, out, "MACHINERY push OK holder="+name+" harness=opencode proven=", out)

	// the judgments go into the session too; the file stays the record
	said.Reset()
	ta.a.pushJudgments(ctx, src, name, []string{"JUDGMENT one"}, false, &said)
	assert.Contains(t, session.last(), "JUDGMENT one")
	assert.Contains(t, said.String(), "PUSH OK name="+name)

	// re-proven every ten minutes by the same round trip
	ta.step(sprint.PushProofEvery)
	said.Reset()
	ta.a.prove(ctx, src, name, false, &said)
	again := nonceOf(t, session.last())
	require.NotEqual(t, nonce, again)
	ta.ok("add --stream s2 --count 1 --one") // the last proof still stands inside its answer bound
	ta.step(sprint.PushAnswerBound + time.Second)
	ta.refusedPushDown("add --stream s3 --count 1 --one", "last pong is 15m1s old")
	ta.ok("seat pong " + again)
	ta.ok("add --stream s3 --count 1 --one")

	// a delivery that fails is recorded, and the seat goes down with its reason
	ta.step(sprint.PushProofEvery)
	session.mu.Lock()
	session.exit = 1
	session.mu.Unlock()
	said.Reset()
	ta.a.prove(ctx, src, name, false, &said)
	assert.Contains(t, said.String(), "PUSH DOWN name="+name)
	ta.refusedPushDown("start", "the last push into "+name+"'s opencode session failed: opencode's deliver command exited 1")

	// the seat goes to a name the push reaches
	pushed := sprint.PushRecord{Name: other, Harness: "opencode", Target: target, Nonce: "n1", Sent: ta.a.now(), Proven: ta.a.now(), PongOf: "n1"}
	require.NoError(t, writePush(ctx, st, pushed))
	out = ta.ok("coordinator " + other + " --reason 'its push is proven'")
	assert.Contains(t, out, "COORDINATOR OK holder="+other, out)

	// teardown deletes every push record by name
	_, err = st.Teardown(ctx)
	require.NoError(t, err)
	for _, n := range []string{name, other} {
		_, ok, err := readPush(ctx, st, n)
		require.NoError(t, err)
		assert.False(t, ok, "teardown left %s's push record", n)
	}
}
