package main

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprintwire"
)

// coordinatorAt is a coordinator's command whose sprint is the rig's server: it names
// the server and no store, so a verb that reached a store from here would be refused.
// sent is every verb it forwarded.
func coordinatorAt(t *testing.T, r *serverRig, actor string, sent *[][]string) func(args ...string) (int, string, string) {
	t.Helper()
	env := map[string]string{ServerEnv: "127.0.0.1:6390", "NOVA_SPRINT_ACTOR": actor}
	c := newApp(func(k string) string { return env[k] })
	t.Cleanup(c.close)
	c.forward = func(_ context.Context, addr string, verbs ...[]string) ([]sprintwire.Result, error) {
		assert.Equal(t, "127.0.0.1:6390", addr)
		*sent = append(*sent, verbs...)
		return r.a.serveFrom(sprintwire.Request{Verbs: verbs}, true).Results, nil
	}
	return func(args ...string) (int, string, string) {
		var out, errb bytes.Buffer
		code := c.run(args, &out, &errb)
		return code, out.String(), errb.String()
	}
}

// With a server named, the coordinator's verbs that write the sprint are run by the
// server, as the coordinator, and print here what they print by themselves: the
// coordinator's command opens no store. One process writes the sprint.
func TestTheCoordinatorsVerbsRunOnTheServer(t *testing.T) {
	t.Parallel()
	r := newServerRig(t)
	var sent [][]string
	boss := coordinatorAt(t, r, "boss", &sent)
	for _, args := range [][]string{
		{"init", "--readers", "reader-a,reader-b", "--members", "m1:2"},
		{"add", "--stream", "s1", "--count", "3"},
		{"start"},
	} {
		code, out, errs := boss(args...)
		require.Equal(t, 0, code, "%v\n%s%s", args, out, errs)
		assert.NotEmpty(t, out, "%v prints here what the verb printed there", args)
	}
	require.Len(t, sent, 3)
	assert.Equal(t, []string{"add", "--actor", "boss", "--stream", "s1", "--count", "3"}, sent[1], "sent as the coordinator, before its own words")
	r.boss("nova-sprint tick")
	r.boss("nova-sprint tick")
	assert.Len(t, r.queue("m1")["ready"], 3, "the server holds what the coordinator added and started")
	assert.Contains(t, r.boss("nova-sprint log"), "by boss", "the moves are the coordinator's")

	// another actor is not the coordinator there either
	mallory := coordinatorAt(t, r, "mallory", &sent)
	code, _, errs := mallory("clear", "--confirm", "sprint")
	assert.NotEqual(t, 0, code)
	assert.Contains(t, errs, "the coordinator's alone")
	assert.Len(t, r.queue("m1")["ready"], 3, "nothing was cleared")
}

// What is not sent: the reads, the verbs the server runs for nobody, a verb given its
// own store, and a verb's help. Each runs here, as before (here it is refused for want
// of a store, which is the proof it was not sent).
func TestWhatTheCoordinatorRunsItself(t *testing.T) {
	t.Parallel()
	r := newServerRig(t, twoLanes()...)
	var sent [][]string
	boss := coordinatorAt(t, r, "boss", &sent)
	for name, args := range map[string][]string{
		"a read":                {"where"},
		"the inbox":             {"inbox"},
		"a worker's queue":      {"queue", "--as", "m1"},
		"land":                  {"land", "--stream", "s1", "--dry-run"},
		"run":                   {"run"},
		"tick":                  {"tick"},
		"fleet sync":            {"fleet", "sync", "--check"},
		"a verb with its store": {"add", "--stream", "s1", "--count", "1", "--redis", "mem:" + filepath.Join(t.TempDir(), "other.twin")},
	} {
		boss(args...)
		assert.Empty(t, sent, "%s is run here, not sent: %v", name, args)
	}
	code, out, _ := boss("add", "--help")
	assert.Equal(t, 0, code)
	assert.Contains(t, out, "usage: nova-sprint add")
	assert.Empty(t, sent, "a verb's help is printed here")
}

// A worker's verb sent from the server's own machine is held as from anywhere: it
// names the epoch its worker holds. And the fleet's listener still runs the workers'
// verbs only.
func TestTheLoopbackListenerHoldsAWorkersVerbToItsEpoch(t *testing.T) {
	t.Parallel()
	r := newServerRig(t, twoLanes()...)
	local := r.a.serveFrom(sprintwire.Request{Verbs: [][]string{
		{"take", "--as", "m1", "--limit", "1"},
		{"take", "--as", "m1", "--limit", "1", "--epoch", "0", "--json"},
		{"fleet", "up", "m1", "--width", "3", "--actor", "boss"},
	}}, true).Results
	assert.Equal(t, 2, local[0].Code, "no epoch: %s", local[0].Stderr)
	assert.Len(t, taken(t, local[1]), 1)
	assert.Equal(t, 0, local[2].Code, "a coordinator's verb, from this machine: %s%s", local[2].Stdout, local[2].Stderr)

	fleet := r.a.serve(sprintwire.Request{Verbs: [][]string{{"fleet", "up", "m1", "--width", "64", "--actor", "boss"}}}).Results
	assert.Equal(t, 2, fleet[0].Code, "from the fleet, a coordinator's verb is not run")
	for _, argv := range [][]string{{"run"}, {"tick"}, {"land", "--stream", "s1"}, {"play"}, {"fleet", "sync"}, {"where", "--redis", "mem:x"}, {"no-such-verb"}} {
		res := r.a.serveFrom(sprintwire.Request{Verbs: [][]string{argv}}, true).Results[0]
		assert.Equal(t, 2, res.Code, "%v", argv)
		assert.Contains(t, res.Stderr, "nothing was changed", "%v", argv)
	}
}

// A server that does not answer is said, with what to do: nothing is known of what
// ran, and the verb is not run here behind the server's back.
func TestACoordinatorsVerbTheServerDidNotAnswer(t *testing.T) {
	t.Parallel()
	env := map[string]string{ServerEnv: "127.0.0.1:6390", "NOVA_SPRINT_ACTOR": "boss"}
	c := newApp(func(k string) string { return env[k] })
	t.Cleanup(c.close)
	c.forward = func(context.Context, string, ...[]string) ([]sprintwire.Result, error) {
		return nil, errors.New("the sprint server at 127.0.0.1:6390 did not answer: connection refused")
	}
	var out, errb bytes.Buffer
	code := c.run([]string{"start"}, &out, &errb)
	assert.Equal(t, 2, code)
	assert.Contains(t, errb.String(), "did not answer")
	assert.Contains(t, errb.String(), "nothing is known of what ran")
	assert.Empty(t, out.String())
}

// A file the coordinator names is sent as its absolute path: the server runs in another
// directory.
func TestAFileNamedToTheServerIsAbsolute(t *testing.T) {
	t.Parallel()
	got := absolutePaths([]string{"add", "--stream", "s1", "--brief-file", "briefs/a.md", "--rules=rules.txt", "--brief-dir", "/abs/dir", "--brief", "--brief-file"})
	assert.True(t, filepath.IsAbs(got[4]), got[4])
	assert.Equal(t, "a.md", filepath.Base(got[4]))
	rules, _ := filepath.Abs("rules.txt")
	assert.Equal(t, "--rules="+rules, got[5])
	assert.Equal(t, "/abs/dir", got[7])
	assert.Equal(t, "--brief-file", got[9], "a flag's word that is another flag's value is left alone when nothing follows it")
}

// A verb from this machine that names no actor acts as no one on the server: the
// server's own environment (here NOVA_SPRINT_ACTOR=boss, the coordinator) is never who
// acts. A --actor the caller gave is who acts.
func TestTheServerNeverActsAsItsOwnEnvironment(t *testing.T) {
	t.Parallel()
	r := newServerRig(t, twoLanes()...)
	before := r.queue("m1")
	res := r.a.serveFrom(sprintwire.Request{Verbs: [][]string{
		{"clear", "--confirm", "sprint"},
		{"inbox", "--read"},
		{"fleet", "up", "m1", "--width", "3", "--actor", "boss"},
	}}, true).Results
	require.Len(t, res, 3)
	assert.Equal(t, 2, res[0].Code, "%s%s", res[0].Stdout, res[0].Stderr)
	assert.Contains(t, res[0].Stderr, "--actor <name> is required")
	assert.Equal(t, before, r.queue("m1"), "nothing was cleared")
	assert.NotEqual(t, 0, res[1].Code, "the coordinator's cursor is not moved by no one: %s%s", res[1].Stdout, res[1].Stderr)
	assert.Equal(t, 0, res[2].Code, "the caller's own actor: %s%s", res[2].Stdout, res[2].Stderr)
}

// A read that waits for the sprint to move (where --watch, inbox --wait) is never run by
// the server, whose line of control the tick it waits for needs: the server refuses it,
// and the coordinator's command runs it where it is typed, never sending it. inbox
// --wait --read would also move the cursor, which is the server's to move: with a server
// named it is refused, and the remedy is the wait, then the read.
func TestAWaitingReadIsNeverRunByTheServer(t *testing.T) {
	t.Parallel()
	r := newServerRig(t, twoLanes()...)
	for _, argv := range [][]string{{"where", "--watch"}, {"inbox", "--wait"}, {"inbox", "--read", "--wait=true", "--actor", "boss"}} {
		res := r.a.serveFrom(sprintwire.Request{Verbs: [][]string{argv}}, true).Results
		require.Len(t, res, 1)
		assert.Equal(t, 2, res[0].Code, "%v", argv)
		assert.Contains(t, res[0].Stderr, "waits for the sprint to move", "%v: refused by the server before the verb runs", argv)
		assert.Contains(t, res[0].Stderr, "nothing was changed", "%v", argv)
	}
	var sent [][]string
	boss := coordinatorAt(t, r, "boss", &sent)
	code, out, errs := boss("inbox", "--read", "--wait", "--timeout", "1ms")
	assert.Equal(t, 2, code, "%s%s", out, errs)
	assert.Contains(t, errs, "run nova-sprint inbox --wait, then nova-sprint inbox --read; nothing was changed")
	boss("where", "--watch")
	assert.Empty(t, sent, "a waiting read runs where it is typed, and a waiting cursor write is refused here")
}

// Which word is a flag is the verb's flags' to say: a value after a flag that takes one
// is a value whatever it looks like, a boolean flag takes no word, and nothing after --
// is a flag.
func TestAFileFlagIsFoundAsTheVerbParsesIt(t *testing.T) {
	t.Parallel()
	rules, _ := filepath.Abs("r.txt")
	for _, c := range []struct{ in, want []string }{
		{[]string{"init", "--json", "--rules", "r.txt"}, []string{"init", "--json", "--rules", rules}},
		{[]string{"add", "--stream", "--rules", "--rules", "r.txt"}, []string{"add", "--stream", "--rules", "--rules", rules}},
		{[]string{"add", "--stream", "s1", "--", "--rules", "r.txt"}, []string{"add", "--stream", "s1", "--", "--rules", "r.txt"}},
		{[]string{"add", "--stream", "s1", "-rules=r.txt"}, []string{"add", "--stream", "s1", "-rules=" + rules}},
		{[]string{"add", "--no-such-flag", "--rules", "r.txt"}, []string{"add", "--no-such-flag", "--rules", "r.txt"}},
	} {
		assert.Equal(t, c.want, absolutePaths(append([]string(nil), c.in...)), "%v", c.in)
	}
}
