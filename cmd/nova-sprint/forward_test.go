package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprintwire"
)

// coordinatorAt is a coordinator's command whose sprint is the rig's server: it names
// the server and no store, so a verb that reached a store from here would be refused.
// sent is every verb it forwarded.
func coordinatorAt(t *testing.T, r *serverRig, actor string, sent *[][]string) func(args ...string) (int, string, string) {
	t.Helper()
	_, run := clientOf(t, r, actor, sent)
	return run
}

// clientOf is coordinatorAt with its app, for a test that gives it a clock.
func clientOf(t *testing.T, r *serverRig, actor string, sent *[][]string) (*app, func(args ...string) (int, string, string)) {
	t.Helper()
	env := map[string]string{ServerEnv: "127.0.0.1:6390", "NOVA_SPRINT_ACTOR": actor}
	c := newApp(func(k string) string { return env[k] })
	t.Cleanup(c.close)
	c.forward = func(_ context.Context, addr string, verbs ...[]string) ([]sprintwire.Result, error) {
		assert.Equal(t, "127.0.0.1:6390", addr)
		*sent = append(*sent, verbs...)
		return r.a.serveFrom(sprintwire.Request{Verbs: verbs}, true).Results, nil
	}
	return c, func(args ...string) (int, string, string) {
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

// What is not sent: the verbs the server runs for nobody, a verb given its own store,
// and a verb's help. Each runs here, as before (here it is refused for want of a store,
// which is the proof it was not sent).
func TestWhatTheCoordinatorRunsItself(t *testing.T) {
	t.Parallel()
	r := newServerRig(t, twoLanes()...)
	var sent [][]string
	boss := coordinatorAt(t, r, "boss", &sent)
	for name, args := range map[string][]string{
		"land":                  {"land", "--stream", "s1", "--dry-run"},
		"run":                   {"run"},
		"tick":                  {"tick"},
		"fleet sync":            {"fleet", "sync", "--check"},
		"a verb with its store": {"add", "--stream", "s1", "--count", "1", "--one", "--redis", "mem:" + filepath.Join(t.TempDir(), "other.twin")},
		"a read with its store": {"where", "--redis", "mem:" + filepath.Join(t.TempDir(), "other.twin")},
	} {
		boss(args...)
		assert.Empty(t, sent, "%s is run here, not sent: %v", name, args)
	}
	code, out, _ := boss("add", "--help")
	assert.Equal(t, 0, code)
	assert.Contains(t, out, "usage: nova-sprint add")
	assert.Empty(t, sent, "a verb's help is printed here")
}

// The reads go to the server too, and print here byte for byte what they print run on
// the store itself: the same exit code, stdout and stderr.
func TestAReadThroughTheServerIsTheReadItself(t *testing.T) {
	t.Parallel()
	r := newServerRig(t, twoLanes()...)
	at := r.a.now()
	r.a.now = func() time.Time { return at }
	var sent [][]string
	boss := coordinatorAt(t, r, "boss", &sent)
	local := func(args []string) (int, string, string) {
		r.a.serial.Lock()
		defer r.a.serial.Unlock()
		var out, errb bytes.Buffer
		code := r.a.run(args, &out, &errb)
		return code, out.String(), errb.String()
	}
	reads := [][]string{
		{"where"}, {"where", "--json"}, {"inbox"}, {"inbox", "--json"}, {"card", "s1-1"}, {"card", "s1-1", "--json"},
		{"card", "no-such-card"}, {"log"}, {"log", "--json", "--stream", "s1"}, {"queue", "--as", "m1"},
		{"queue", "--stream", "s1", "--json"}, {"routes"}, {"check"}, {"check", "--json"}, {"goal", "show"},
	}
	for _, args := range reads {
		code, out, errs := local(args)
		fcode, fout, ferrs := boss(args...)
		assert.Equal(t, []any{code, out, errs}, []any{fcode, fout, ferrs}, "%v", args)
	}
	assert.Len(t, sent, len(reads), "every read was sent")
}

// where --watch through the server is a watch here: one plain where sent a frame, and
// the server held by none of them between frames, so a tick runs between two.
func TestAWatchThroughTheServerSendsOneReadAFrame(t *testing.T) {
	t.Parallel()
	r := newServerRig(t, twoLanes()...)
	var sent [][]string
	c, boss := clientOf(t, r, "boss", &sent)
	ctx, stop := context.WithCancel(t.Context())
	t.Cleanup(stop)
	c.notify = func(context.Context) (context.Context, context.CancelFunc) { return ctx, stop }
	frames := 0
	c.sleep = func(time.Duration) {
		frames++
		require.True(t, r.a.serial.TryLock(), "the server is held between two frames")
		r.a.serial.Unlock()
		if frames == 2 {
			r.boss("nova-sprint tick")
		}
		if frames == 4 {
			stop()
		}
	}
	code, out, errs := boss("where", "--watch", "--every", "100ms", "--json")
	require.Equal(t, 0, code, errs)
	assert.Equal(t, slices.Repeat([][]string{{"where", "--actor", "boss", "--json"}}, 4), sent, "one plain where a frame")
	lines := strings.Split(strings.TrimSpace(out), "\n")
	require.Len(t, lines, 4, out)
	assert.NotEqual(t, lines[1], lines[2], "the tick between the second and third frames is in the third")
}

// inbox --wait through the server waits here: it reads the server's log for a tick end
// once a second on this process's clock, reads the inbox at the tick end after a
// judgment opens and wakes for the new judgment, or says its timeout, then prints the
// inbox as inbox --wait prints it.
func TestAnInboxWaitThroughTheServerPollsForATickEnd(t *testing.T) {
	t.Parallel()
	r := newServerRig(t, "nova-sprint init --readers reader-a,reader-b --members m1:2",
		"nova-sprint add --stream s1 --count 1 --one --brief-file "+proBriefFile(t), "nova-sprint start", "nova-sprint tick", "nova-sprint tick",
		"nova-sprint take --as m1 --limit 1", "nova-sprint finish --as m1 --epoch 0 s1-1.w1@1")
	clock := r.a.now()
	r.a.now = func() time.Time { return clock } // the server's clock, still: what it prints is the same at every read
	var sent [][]string
	c, boss := clientOf(t, r, "boss", &sent)
	waited := clock
	c.now = func() time.Time { return waited }
	polls := 0
	c.sleep = func(d time.Duration) {
		assert.Equal(t, time.Second, d)
		waited = waited.Add(d)
		if polls++; polls == 2 {
			r.boss("nova-sprint reader away reader-a") // fewer than two readers up: a judgment at the tick
			r.boss("nova-sprint tick")
		}
	}
	code, out, errs := boss("inbox", "--wait", "--timeout", "4s")
	require.Equal(t, 0, code, errs)
	assert.Equal(t, 2, polls, "woke at the tick end after the judgment, not at the timeout")
	plain := r.boss("nova-sprint inbox")
	assert.Equal(t, "inbox --wait: new=tick-ask-t31-1.1\n"+plain, out, "the wake line, then the inbox as inbox --wait prints it when it woke")
	assert.Contains(t, out, "fewer than two readers up")
	assert.Len(t, sent, 6, "the log and the inbox at the start, two reads of the log, the inbox at the tick end, then the inbox")

	code, out, errs = boss("inbox", "--wait", "--timeout", "3s", "--json")
	require.Equal(t, 0, code, errs)
	assert.Equal(t, 5, polls, "three more polls of a second: the timeout")
	assert.Equal(t, "inbox --wait: nothing new in 3s\n", errs, "said on stderr under --json, as inbox --wait says it")
	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(out), &got), out)
	assert.Equal(t, false, got["woke"])
	_, plainJSON, _ := boss("inbox", "--json")
	assert.Equal(t, strings.TrimSuffix(plainJSON, "}\n")+`,"new":[],"woke":false}`+"\n", out, "the inbox's own object, with new and woke last, as encoding/json orders a map")

	code, _, errs = boss("inbox", "--wait", "--timeout", "0s")
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "--timeout above zero")
}

// The coordinator's side of a small sprint, with no store named and no store
// credentials: every verb goes through the server.
func TestACoordinatorWithNoStoreRunsTheSprintThroughTheServer(t *testing.T) {
	t.Parallel()
	r := newServerRig(t)
	var sent [][]string
	boss := coordinatorAt(t, r, "boss", &sent)
	for _, line := range []string{"init --readers reader-a,reader-b --members m1:2", "add --stream s1 --count 2", "start", "where", "inbox", "card s1-1", "log --stream s1", "clear --confirm sprint", "where"} {
		code, out, errs := boss(split(line)...)
		require.Equal(t, 0, code, "%s\n%s%s", line, out, errs)
		if line == "start" {
			r.boss("nova-sprint tick")
		}
	}
	assert.Len(t, sent, 9, "every verb was the server's")
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

	fleet := r.a.serveFrom(sprintwire.Request{Verbs: [][]string{{"fleet", "up", "m1", "--width", "64", "--actor", "boss"}}}, false).Results
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
	assert.Contains(t, errb.String(), "nova-sprint run --listen", "the remedy")
	assert.Empty(t, out.String())
	out.Reset()
	errb.Reset()
	assert.Equal(t, 2, c.run([]string{"where"}, &out, &errb), "a read is not run on a store here either")
	assert.Equal(t, 1, strings.Count(errb.String(), "\n"), "one line: %s", errb.String())
	assert.Contains(t, errb.String(), "did not answer")
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
// the server, whose line of control the tick it waits for needs: the server refuses it
// (the coordinator's command waits itself, sending plain reads). inbox
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
	assert.Empty(t, sent, "a waiting cursor write is refused here")
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

type mockHandler struct {
	attempts int
	serveFn  func(w http.ResponseWriter, r *http.Request)
}

func (mh *mockHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	mh.attempts++
	w.Header().Set("Content-Type", "application/json")
	if mh.attempts <= 2 {
		_, _ = w.Write([]byte(`{"results":[{"code":0,"stdout":"","stderr":"","restarting":true}]}`)) // ignored: write restarting response
		return
	}
	if mh.serveFn != nil {
		mh.serveFn(w, r)
		return
	}
	_, _ = w.Write([]byte(`{"results":[{"code":0,"stdout":"ok\n","stderr":""}]}`)) // ignored: write success response
}

func TestAVerbDuringASwitchGetsRestartingAndWaits(t *testing.T) {
	t.Parallel()
	mh := &mockHandler{}
	var stderr bytes.Buffer
	c := sprintwire.Client{
		Addr:   "sprint.test:6390",
		HTTP:   &http.Client{Transport: handlerTransport{mh}},
		Stderr: &stderr,
		Bound:  5 * time.Second,
	}
	res, err := c.Do(context.Background(), []string{"where"})
	require.NoError(t, err)
	require.Len(t, res, 1)
	assert.Equal(t, 0, res[0].Code)
	assert.Equal(t, "ok\n", res[0].Stdout)
	assert.Equal(t, "WAITING\n", stderr.String())
	assert.Equal(t, 3, mh.attempts, "twice restarting then serves")

	mhTimeout := &mockHandler{
		serveFn: func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"results":[{"code":0,"stdout":"","stderr":"","restarting":true}]}`)) // ignored: write restarting response
		},
	}
	cTimeout := sprintwire.Client{
		Addr:  "sprint.test:6390",
		HTTP:  &http.Client{Transport: handlerTransport{mhTimeout}},
		Bound: 50 * time.Millisecond,
	}
	_, err = cTimeout.Do(context.Background(), []string{"where"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "server switch", "past the bound it names the switch")
}
