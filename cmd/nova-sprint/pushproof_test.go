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
	beat  func()
}

func (f *fakeSession) Deliver(_ context.Context, text string) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.texts = append(f.texts, text)
	if f.exit == 0 && f.beat != nil {
		f.beat()
	}
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
	// These judgment-specific tests also have three separate fake native observers.
	// New set tests disable this fixture and judge each observer independently.
	s.beat = func() {
		st, err := ta.a.store(common{redis: "mem:0", actor: name})
		require.NoError(t, err)
		rec, ok, err := st.SeatPushes(context.Background(), name)
		require.NoError(t, err)
		seat, err := st.SeatState(context.Background())
		require.NoError(t, err)
		if !ok || rec.Harness == "" || seat.Holder != name {
			return
		}
		for _, source := range []string{"bus", "friends", "transitions"} {
			require.NoError(t, st.BeatSeatPush(context.Background(), name, source, ""))
		}
	}
	pushTests.Store(name, s)
	oldSleep := ta.a.sleep
	ta.a.sleep = func(d time.Duration) {
		oldSleep(d)
		if s.beat != nil {
			s.beat()
		}
	}
	t.Cleanup(func() { pushTests.Delete(name) })
	return ta, s
}

func (ta *testApp) step(d time.Duration) {
	ta.mu.Lock()
	ta.now = ta.now.Add(d)
	ta.mu.Unlock()
	if value, ok := pushTests.Load(ta.a.getenv("NOVA_SPRINT_ACTOR")); ok {
		if session, ok := value.(*fakeSession); ok && session.beat != nil {
			session.beat()
		}
	}
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
	assert.Contains(t, out, "PUSH DOWN name="+name+" harness=- target=- adapter=-", out)
	// seat check says PUSH DOWN with the remedy
	ta.a.outside = mockHealthyOutside()
	code, out, _ = ta.do("seat check")
	require.Equal(t, 1, code, out)
	assert.Contains(t, out, "MACHINERY push DOWN holder="+name+" harness=- adapter=- why=", out)
	assert.Contains(t, out, "remedy=\"nova-sprint seat install --actor "+name, out)

	// the seat is given to no name without a live proof
	other := "pushproof-b"
	pushTests.Store(other, &fakeSession{})
	t.Cleanup(func() { pushTests.Delete(other) })
	code, _, errs := ta.do("coordinator " + other + " --reason r")
	require.Equal(t, 2, code, errs)
	assert.Contains(t, errs, "coordinator REFUSED: PUSH DOWN: "+name+" has no push target recorded", errs)
	assert.Equal(t, name, ta.holder())

	// a harness with no deliver command gets the folder adapter, and a target
	// that is no directory is refused with nothing written
	target := t.TempDir()
	code, _, errs = ta.do("seat install --redis 127.0.0.1:6381 --harness copilot --target " + filepath.Join(target, "missing"))
	require.Equal(t, 2, code, errs)
	assert.Contains(t, errs, "copilot has no deliver command, so the push loop writes each check and judgment as a file into --target", errs)
	_, ok, err := readPush(ctx, st, name)
	require.NoError(t, err)
	require.False(t, ok, "a refused install wrote a push record")
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
	ta.refusedPushDown("coordinator "+other+" --reason handover", other+" has no push target recorded")
	ta.ok("add --stream s1 --count 2")
	assert.Contains(t, ta.ok("seat push"), "PUSH OK name="+name+" harness=opencode")
	_, out, _ = ta.do("seat check")
	assert.Contains(t, out, "MACHINERY push OK holder="+name+" harness=opencode adapter=opencode proven=", out)

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
	// A live next holder does not excuse the current seat's failed proof set.
	ta.refusedPushDown("coordinator "+other+" --reason 'its push is proven'", "the last push into "+name+"'s opencode session failed")
	session.mu.Lock()
	session.exit = 0
	session.mu.Unlock()
	ta.step(sprint.PushRetry)
	ta.a.prove(ctx, src, name, false, &said)
	ta.ok("seat pong " + nonceOf(t, session.last()))
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

// pushArmedOnly arms a name for the push proof and leaves it the real adapter.
type pushArmedOnly struct{}

// A Claude Code session has no deliver command, so its seat is reached through
// the folder adapter (docs/SPEC-SPRINT.md, "The push proof"): seat install
// records adapter=folder and prints the two commands the session runs, the
// Monitor on the folder and seat pong; the push loop writes the check as
// PROOF-<nonce> into the folder; until the session answers that nonce every
// coordinator verb is refused with the two commands, the nonce filled in; the
// answer proves the seat, the status says adapter=folder proven=<time>, and the
// next check replaces the file. Judgments already written into the folder are
// not written again.
func TestASeatOnAFolderAdapterIsProvenByItsNonceAndRefusedWithout(t *testing.T) {
	t.Parallel()
	const name = "pushproof-folder"
	ta, _ := pushProofSprint(t, name)
	pushTests.Store(name, pushArmedOnly{}) // the real adapter: the folder
	ctx := context.Background()
	ta.ok("init --readers reader-a,reader-b --members m1,m2 --owner glenn")
	st, err := ta.a.store(common{redis: "mem:0", actor: name})
	require.NoError(t, err)
	home, err := ta.a.home()
	require.NoError(t, err)
	folder := filepath.Join(home, name+"-working", "inbox", "sprint-judgments")

	// the folder must be there: nothing is made, nothing recorded
	code, _, errs := ta.do("seat install --redis 127.0.0.1:6381 --harness claude --target " + folder)
	require.Equal(t, 2, code, errs)
	assert.Contains(t, errs, "claude has no deliver command, so the push loop writes each check and judgment as a file into --target, the folder the session watches, and "+folder+" is not a directory; nothing was written", errs)
	_, ok, err := readPush(ctx, st, name)
	require.NoError(t, err)
	require.False(t, ok, "a refused install wrote a push record")
	require.NoError(t, os.MkdirAll(folder, 0o755))

	// the install records adapter=folder and prints the two commands
	out := ta.ok("seat install --redis 127.0.0.1:6381 --harness claude --target " + folder)
	assert.Contains(t, out, "SEAT INSTALL OK unit=", out)
	assert.Contains(t, out, "  push: claude into "+folder+" adapter=folder: each check is written there as PROOF-<nonce>", out)
	assert.Contains(t, out, "  monitor: "+sprint.FolderWatch(folder)+"\n", out)
	assert.Contains(t, out, "  prove: nova-sprint seat pong <nonce> --actor "+name+"\n", out)
	rec, ok, err := readPush(ctx, st, name)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, sprint.AdapterFolder, rec.Adapter)

	// no proof, no seat: the refusal carries the two commands
	ta.refusedPushDown("add --stream s1 --count 2", "no push check has been delivered into "+name+"'s claude session yet")
	_, _, errs = ta.do("add --stream s1 --count 2")
	assert.Contains(t, errs, "; then, from inside the session, watch the folder with a Monitor: "+sprint.FolderWatch(folder)+" ; and answer the PROOF-<nonce> file it shows: nova-sprint seat pong <nonce> --actor "+name, errs)

	// the push loop writes the check into the folder as PROOF-<nonce>
	src := &storeSource{st: st}
	var said bytes.Buffer
	ta.a.prove(ctx, src, name, false, &said)
	proofs, err := filepath.Glob(filepath.Join(folder, "PROOF-*"))
	require.NoError(t, err)
	require.Len(t, proofs, 1, "one check, one file")
	nonce := strings.TrimPrefix(filepath.Base(proofs[0]), "PROOF-")
	body, err := os.ReadFile(proofs[0])
	require.NoError(t, err)
	assert.Equal(t, sprint.PushCheckText(name, nonce), string(body), "the file is the check")
	assert.Contains(t, said.String(), "PUSH CHECK name="+name+" nonce="+nonce+"\n")
	ta.refusedPushDown("add --stream s1 --count 2", "the push check "+nonce+" went into "+name+"'s claude session and no pong carrying it came back")
	_, _, errs = ta.do("add --stream s1 --count 2")
	assert.Contains(t, errs, "answer the PROOF-"+nonce+" file it shows: nova-sprint seat pong "+nonce+" --actor "+name, errs)
	code, out, _ = ta.do("seat push")
	require.Equal(t, 1, code, out)
	assert.Contains(t, out, "PUSH DOWN name="+name+" harness=claude target="+folder+" adapter=folder why=", out)
	assert.Contains(t, out, "nova-sprint seat pong "+nonce+" --actor "+name, out)

	// only that nonce proves it
	code, _, errs = ta.do("seat pong 0000000000000000")
	require.Equal(t, 2, code, errs)
	assert.Contains(t, errs, "only the session's answer to the last check counts", errs)
	out = ta.ok("seat pong " + nonce)
	assert.Contains(t, out, "SEAT PONG OK name="+name+" nonce="+nonce, out)
	proven := ta.a.now().UTC().Format(time.RFC3339)

	// proven: the verbs run, and the status says adapter=folder proven=<time>
	ta.ok("add --stream s1 --count 2")
	assert.Contains(t, ta.ok("seat push"), "PUSH OK name="+name+" harness=claude target="+folder+" adapter=folder proven="+proven)
	ta.a.outside = mockHealthyOutside()
	_, out, _ = ta.do("seat check")
	assert.Contains(t, out, "MACHINERY push OK holder="+name+" harness=claude adapter=folder proven=", out)
	v := ta.coordView("")
	assert.Contains(t, v.Sum, " | push adapter=folder proven="+proven, v.Sum)

	// judgments the loop wrote into the folder are not written again
	before, err := os.ReadDir(folder)
	require.NoError(t, err)
	said.Reset()
	ta.a.pushJudgments(ctx, src, name, []string{"JUDGMENT one"}, false, &said)
	after, err := os.ReadDir(folder)
	require.NoError(t, err)
	assert.Len(t, after, len(before), "the folder is the inbox: the files are the delivery")
	assert.Contains(t, said.String(), "PUSH OK name="+name)
	// into any other folder a judgment is one file, the text whole
	elsewhere := t.TempDir()
	exit, err := (&folderAdapter{Dir: elsewhere}).Deliver(ctx, "JUDGMENT two\n")
	require.NoError(t, err)
	assert.Equal(t, 0, exit)
	pushed, err := filepath.Glob(filepath.Join(elsewhere, "PUSH-*.md"))
	require.NoError(t, err)
	require.Len(t, pushed, 1)
	body, err = os.ReadFile(pushed[0])
	require.NoError(t, err)
	assert.Equal(t, "JUDGMENT two\n", string(body))

	// re-proven on the schedule: the next check replaces the file
	ta.step(sprint.PushProofEvery)
	ta.a.prove(ctx, src, name, false, &said)
	proofs, err = filepath.Glob(filepath.Join(folder, "PROOF-*"))
	require.NoError(t, err)
	require.Len(t, proofs, 1, "the old check is removed")
	again := strings.TrimPrefix(filepath.Base(proofs[0]), "PROOF-")
	require.NotEqual(t, nonce, again)
	ta.step(sprint.PushAnswerBound + time.Second)
	ta.refusedPushDown("add --stream s2 --count 1 --one", "last pong is 15m1s old")
	ta.ok("seat pong " + again)
	ta.ok("add --stream s2 --count 1 --one")

	// the seat goes to a name the folder reaches, and says so
	other := "pushproof-folder-b"
	pushTests.Store(other, pushArmedOnly{})
	t.Cleanup(func() { pushTests.Delete(other) })
	now := ta.a.now()
	require.NoError(t, writePush(ctx, st, sprint.PushRecord{Name: other, Harness: "claude", Adapter: sprint.AdapterFolder, Target: folder, Nonce: "n1", Sent: now, Proven: now, PongOf: "n1"}))
	out = ta.ok("coordinator " + other + " --reason 'its folder is proven'")
	assert.Contains(t, out, "COORDINATOR OK holder="+other+" from="+name+" by="+name+" given adapter=folder proven="+now.UTC().Format(time.RFC3339), out)
}
