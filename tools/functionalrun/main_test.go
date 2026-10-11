package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeEngine records every argv and answers from a table keyed by the argv's
// first words. Nothing here starts a container or a process.
type fakeEngine struct {
	mu      sync.Mutex
	calls   [][]string
	answers map[string]fakeAnswer
	// startCode is what Start's process returns from Wait; startCodes, when
	// set, gives one code per Start in order.
	startCode  int
	startCodes []int
	// respond, when set, answers first; ok false falls through to answers.
	respond func(args []string) (a fakeAnswer, ok bool)
}

type fakeAnswer struct {
	out string
	err error
}

func (f *fakeEngine) Output(_ context.Context, args ...string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, append([]string(nil), args...))
	if f.respond != nil {
		if a, ok := f.respond(args); ok {
			return a.out, a.err
		}
	}
	for n := len(args); n > 0; n-- {
		if a, ok := f.answers[strings.Join(args[:n], " ")]; ok {
			return a.out, a.err
		}
	}
	return "", nil
}

func (f *fakeEngine) Start(args []string, _, _ io.Writer) (process, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, append([]string(nil), args...))
	code := f.startCode
	if len(f.startCodes) > 0 {
		code, f.startCodes = f.startCodes[0], f.startCodes[1:]
	}
	return fakeProcess{code: code}, nil
}

type fakeProcess struct{ code int }

func (p fakeProcess) Wait() (int, error) { return p.code, nil }
func (p fakeProcess) Kill() error        { return nil }

func (f *fakeEngine) argvs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.calls))
	for i, c := range f.calls {
		out[i] = strings.Join(c, " ")
	}
	return out
}

func testConfig() runConfig {
	return runConfig{
		src:      "/work/src",
		deadline: 10 * time.Minute,
		grace:    30 * time.Second,
		cpus:     4,
		memory:   "4g",
		pids:     1024,
		scratch:  "2g",
		gocache:  "nova-functional-gocache-uid501",
		gomod:    "nova-functional-gomod-uid501",
		packages: []string{"./pkg/ntable/...", "./pkg/config/"},
		ownerID:  "501",
	}
}

// flagValue returns the value after the first occurrence of flag in args.
func flagValue(t *testing.T, args []string, flag string) string {
	t.Helper()
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			return args[i+1]
		}
	}
	t.Fatalf("argv has no %s: %q", flag, args)
	return ""
}

func flagValues(args []string, flag string) []string {
	var out []string
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			out = append(out, args[i+1])
		}
	}
	return out
}

// wantFlag owns the sentence every one-flag check here would otherwise repeat:
// it fails the test when args does not give flag the value want.
func wantFlag(t *testing.T, args []string, flag, want string) {
	t.Helper()
	assert.Equal(t, want, flagValue(t, args, flag), flag)
}

// wantRemoved owns the sentence every end-of-run removal check repeats: it
// fails the test when the engine's last call is not the removal of name.
func wantRemoved(t *testing.T, calls []string, name string) {
	t.Helper()
	assert.Equal(t, strings.Join(removeArgs(name), " "), calls[len(calls)-1], "%s is not removed last: %q", name, calls)
}

func indexOf(args []string, s string) int {
	for i, a := range args {
		if a == s {
			return i
		}
	}
	return -1
}

func TestTestArgsHoldTheRunInsideItsBounds(t *testing.T) {
	t.Parallel()
	start := time.Unix(1_800_000_000, 0)
	args := testArgs(testConfig(), "sha256:abc", "run1", start)
	joined := strings.Join(args, " ")

	assert.True(t, args[0] == "run" && strings.Contains(joined, " --rm ") && strings.Contains(joined, " --init "),
		"not an attached run with --rm and --init: %q", joined)
	wantFlag(t, args, "--name", "nova-functional-run1")
	wantFlag(t, args, "--timeout", "600")
	for flag, want := range map[string]string{
		"--security-opt": "no-new-privileges", "--cap-drop": "all",
		"--network": "none", "--ipc": "private", "--pids-limit": "1024",
		"--memory": "4g", "--memory-swap": "4g", "--cpus": "4", "-w": "/src",
	} {
		wantFlag(t, args, flag, want)
	}
	assert.Contains(t, joined, " --read-only ", "the image is not mounted read-only")
	assert.Equal(t, []string{"/work/src:/src:ro", "nova-functional-gomod-uid501:/gomodcache:ro", "nova-functional-gocache-uid501:/gocache"},
		flagValues(args, "-v"), "mounts")
	tmpfs := flagValues(args, "--tmpfs")
	assert.True(t, len(tmpfs) == 2 && tmpfs[0] == "/tmp:rw,exec,size=2g" && strings.HasPrefix(tmpfs[1], "/home/bench:"),
		"scratch tmpfs %q", tmpfs)
	assert.Equal(t, "nova.functional.run=run1,nova.functional.start=1800000000,nova.functional.deadline=1800000600,nova.functional.owner=501",
		strings.Join(flagValues(args, "--label"), ","), "labels")
	// No host environment and no network proxy reach the test container.
	assert.False(t, strings.Contains(joined, "--env-host") || len(flagValues(args, "-e")) != 0,
		"the test container gets environment from the host: %q", joined)
	// The command: the inner timeout 10 s under the deadline, go test's 20 s
	// under it, through the Makefile's own target.
	i := indexOf(args, "sha256:abc")
	require.NotEqual(t, -1, i, "no image in argv: %q", joined)
	assert.Equal(t, strings.Join([]string{"timeout", "-k", "5", "590", "make", "test-functional",
		"PKGS=./pkg/ntable/... ./pkg/config/", "GOTEST_P=4", "FUNCTIONAL_TIMEOUT=580s"}, "\x00"),
		strings.Join(args[i+1:], "\x00"), "command")
	// Every flag of the runtime comes before the image.
	for _, f := range []string{"--timeout", "--network", "--label", "-v"} {
		assert.LessOrEqual(t, indexOf(args, f), i, "%s comes after the image", f)
	}
}

func TestTimeoutSecondsIsNeverZero(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		d    time.Duration
		want int
	}{
		{0, 1}, {-time.Second, 1}, {time.Millisecond, 1}, {time.Second, 1},
		{1500 * time.Millisecond, 2}, {30 * time.Second, 30}, {10 * time.Minute, 600},
	} {
		assert.Equal(t, tc.want, timeoutSeconds(tc.d), "timeoutSeconds(%s)", tc.d)
	}
}

func TestPrefillHasTheNetworkAndWritesOnlyTheModuleCache(t *testing.T) {
	t.Parallel()
	start := time.Unix(1_800_000_000, 0)
	args := prefillArgs(testConfig(), "sha256:abc", "run1", "stamp123", "https://proxy.example", start)
	joined := strings.Join(args, " ")
	assert.NotContains(t, joined, "--network none", "the module step has no network")
	assert.Equal(t, []string{"/work/src:/src:ro", "nova-functional-gomod-uid501:/gomodcache"},
		flagValues(args, "-v"), "want the source read-only and the module cache writable, and no build cache")
	wantFlag(t, args, "--name", "nova-functional-run1-mod")
	labels := flagValues(args, "--label")
	assert.Equal(t, "nova.functional.run=run1-mod", labels[0], "the module step is not a labelled run: %q", labels)
	assert.Equal(t, "nova.functional.deadline="+strconv.FormatInt(start.Add(prefillDeadline).Unix(), 10), labels[2],
		"the module step does not carry its own deadline: %q", labels)
	wantFlag(t, args, "--timeout", strconv.Itoa(int(prefillDeadline/time.Second)))
	assert.Equal(t, "no-new-privileges", flagValue(t, args, "--security-opt"), "the module step keeps new privileges: %q", joined)
	assert.Equal(t, "all", flagValue(t, args, "--cap-drop"), "the module step keeps capabilities: %q", joined)
	wantFlag(t, args, "-e", "GOPROXY=https://proxy.example")
	assert.Equal(t, []string{"functionalrun", "stamp123"}, args[len(args)-2:], "the stamp is not the script's $1: %q", args[len(args)-3:])
}

func TestModuleProxy(t *testing.T) {
	t.Parallel()
	env := func(v string) func(string) string { return func(string) string { return v } }
	for in, want := range map[string]string{
		"":                              "https://proxy.golang.org,direct",
		"off":                           "https://proxy.golang.org,direct",
		"https://mirror.example,direct": "https://mirror.example,direct",
	} {
		assert.Equal(t, want, moduleProxy(env(in)), "moduleProxy(%q)", in)
	}
}

func TestRuntimeEnvDropsTheRunnerTrackingID(t *testing.T) {
	t.Parallel()
	assert.Equal(t, []string{"PATH=/bin", "HOME=/h", "RUNNER_TRACKING_IDX=keep"},
		runtimeEnv([]string{"PATH=/bin", "RUNNER_TRACKING_ID=github_abc", "HOME=/h", "RUNNER_TRACKING_IDX=keep"}))
}

func TestRemoveArgsForceWithVolumesAndNoWait(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "rm --force --ignore --volumes --time 0 abc", strings.Join(removeArgs("abc"), " "))
}

func TestSelectionIsByLabelNeverByName(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{reapListArgs(), leftoverArgs("run1")} {
		joined := strings.Join(args, " ")
		assert.NotContains(t, joined, "name=", "selects by name: %q", joined)
		f := flagValue(t, args, "--filter")
		assert.True(t, strings.HasPrefix(f, "label="+labelRun), "--filter %q is not the run label", f)
		assert.Contains(t, joined, "--all", "does not list containers in every state: %q", joined)
	}
	assert.Equal(t, "label=nova.functional.run=run1", flagValue(t, leftoverArgs("run1"), "--filter"),
		"the leftover check does not select its own run")
}

func TestNewRunIDIsANameAndALabel(t *testing.T) {
	t.Parallel()
	start := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	a, b := newRunID(start), newRunID(start)
	assert.NotEqual(t, a, b, "two runs at one instant share an id")
	assert.True(t, strings.HasPrefix(a, "20300102t030405-") && volumeNameRE.MatchString(a) && strings.ToLower(a) == a,
		"run id %q is not a lowercase container name", a)
}

func TestCacheVolumesArePerUserAndCarryNoRunLabel(t *testing.T) {
	t.Parallel()
	a, b := cacheVolumeName("gocache", "501"), cacheVolumeName("gocache", "502")
	assert.NotEqual(t, a, b, "two users share the build cache %q", a)
	args := volumeCreateArgs("v", "gocache", "501")
	labels := strings.Join(flagValues(args, "--label"), ",")
	assert.Equal(t, "nova.functional.cache=gocache,nova.functional.owner=501", labels, "cache volume labels")
	assert.NotContains(t, labels, labelRun, "a cache carries the run label, so a reap or a leftover check would count it")
}

func TestEnsureVolume(t *testing.T) {
	t.Parallel()
	inspect := strings.Join(volumeInspectArgs("v"), " ")
	create := strings.Join(volumeCreateArgs("v", "gocache", "501"), " ")
	for _, tc := range []struct {
		name    string
		answer  fakeAnswer
		wantErr string
		calls   []string
	}{
		{"this user's volume is only inspected", fakeAnswer{out: "501|gocache\n"}, "", []string{inspect}},
		{"another user's volume is refused with the flag to use", fakeAnswer{out: "502|gocache\n"}, "--gocache-volume", []string{inspect}},
		{"the module cache is not taken as the build cache", fakeAnswer{out: "501|gomod\n"}, `a "gomod" cache, not a gocache cache`, []string{inspect}},
		{"a volume with no kind label is not the build cache", fakeAnswer{out: "501|\n"}, "not a gocache cache", []string{inspect}},
		{"a missing volume is created with its owner", fakeAnswer{err: fmt.Errorf("no such volume")}, "", []string{inspect, create}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			eng := &fakeEngine{answers: map[string]fakeAnswer{inspect: tc.answer}}
			err := ensureVolume(context.Background(), eng, "v", "gocache", "501")
			if tc.wantErr == "" {
				require.NoError(t, err)
			} else {
				assert.ErrorContains(t, err, tc.wantErr)
			}
			assert.Equal(t, tc.calls, eng.argvs())
		})
	}
	// Two first runs at once: this one's create loses, and it looks again.
	for _, tc := range []struct {
		winner string
		ok     bool
	}{{"501|gocache", true}, {"502|gocache", false}, {"501|gomod", false}} {
		inspected := 0
		race := &fakeEngine{respond: func(args []string) (fakeAnswer, bool) {
			switch strings.Join(args, " ") {
			case inspect:
				inspected++
				if inspected == 1 {
					return fakeAnswer{err: fmt.Errorf("no such volume")}, true
				}
				return fakeAnswer{out: tc.winner}, true
			case create:
				return fakeAnswer{err: fmt.Errorf("volume already exists")}, true
			}
			return fakeAnswer{}, false
		}}
		err := ensureVolume(context.Background(), race, "v", "gocache", "501")
		assert.Equal(t, tc.ok, err == nil, "lost the create race to %q: err %v", tc.winner, err)
		calls := race.argvs()
		assert.True(t, len(calls) == 3 && calls[2] == inspect, "a lost create does not look again: %q", calls)
	}
}

func TestParseListed(t *testing.T) {
	t.Parallel()
	for _, empty := range []string{"", "  \n", "[]", "null"} {
		cs, err := parseListed(empty)
		assert.NoError(t, err, "parseListed(%q)", empty)
		assert.Empty(t, cs, "parseListed(%q)", empty)
	}
	_, err := parseListed("CONTAINER ID  IMAGE")
	assert.Error(t, err, "a table listing is read as JSON")
}

func TestParseRun(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	testkit.WriteFile(t, filepath.Join(dir, "go.mod"), "module x\n")
	ctxDir := filepath.Join(dir, "img")
	testkit.WriteFile(t, filepath.Join(ctxDir, "Containerfile"), "FROM x\n")
	c, err := parseRun([]string{"--src", dir, "--context", ctxDir, "--deadline", "2m", "./pkg/ntable/..."})
	require.NoError(t, err)
	assert.Equal(t, 2*time.Minute, c.deadline)
	assert.Equal(t, cacheVolumeName("gocache", c.ownerID), c.gocache)
	assert.Equal(t, cacheVolumeName("gomod", c.ownerID), c.gomod)
	with := func(args ...string) []string { return append([]string{"--src", dir, "--context", ctxDir}, args...) }
	for _, tc := range []struct {
		args []string
		want string
	}{
		{with(), "no package"},
		{with("./a", "--deadline", "1m"), "flags come first"},
		{with("pkg/ntable"), "not a package directory"},
		{with("./a b"), "not a package directory"},
		{with("--deadline", "29s", "./a"), "under 30s"},
		{with("--deadline", "0s", "./a"), "under 30s"},
		{with("--deadline", "-1m", "./a"), "under 30s"},
		{with("--deadline", "25h", "./a"), "over 24h"},
		{with("--cpus", "0", "./a"), "--cpus"},
		{with("--memory", "lots", "./a"), "--memory"},
		{[]string{"--src", dir, "--context", dir, "./a"}, "no Containerfile"},
		{[]string{"--src", ctxDir, "./a"}, "no go.mod"},
		{with("--gocache-volume", "same", "--gomod-volume", "same", "./a"), "two volumes"},
		{with("--gocache-volume", "../x", "./a"), "not a podman volume name"},
		{with("--fresh-gocache", "--gocache-volume", "mine", "./a"), "two different build caches"},
	} {
		_, err := parseRun(tc.args)
		assert.ErrorContains(t, err, tc.want, "parseRun(%q)", tc.args)
	}
	// --image needs no context.
	_, err = parseRun([]string{"--src", dir, "--image", "localhost/x:y", "./a"})
	assert.NoError(t, err, "--image without a context")
}

func TestContextHashFollowsEveryFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	write := func(name, body string) { testkit.WriteFile(t, filepath.Join(dir, name), body) }
	write("Containerfile", "FROM a\n")
	h1, err := contextHash(dir)
	require.NoError(t, err)
	h2, _ := contextHash(dir)
	assert.Equal(t, h1, h2, "the hash is not stable")
	write("sub/extra.txt", "x")
	h3, _ := contextHash(dir)
	assert.NotEqual(t, h1, h3, "a new file in the context does not change the hash")
	write("Containerfile", "FROM b\n")
	h4, _ := contextHash(dir)
	assert.NotEqual(t, h3, h4, "an edit of the Containerfile does not change the hash")
	tag := imageTag(h1)
	assert.True(t, strings.HasPrefix(tag, "localhost/nova-functional:ctx-") && len(tag) == len("localhost/nova-functional:ctx-")+16,
		"imageTag = %q", tag)
}

func TestRunContainerPassesTheExitCodeThrough(t *testing.T) {
	t.Parallel()
	for _, code := range []int{0, 1, 2, 124} {
		eng := &fakeEngine{startCode: code}
		got, ended := runContainer(context.Background(), eng, []string{"run", "x"}, "nova-functional-r", time.Now().Add(time.Minute), io.Discard, io.Discard)
		assert.Equal(t, code, got, "the exit code")
		assert.Equal(t, "finished", ended, "exit %d", code)
		// The container is removed after the client returns, whatever the code.
		wantRemoved(t, eng.argvs(), "nova-functional-r")
	}
}

// hangingEngine's process never returns until killed.
type hangingEngine struct {
	fakeEngine
	killed chan struct{}
}

type hangingProcess struct{ killed chan struct{} }

func (p hangingProcess) Wait() (int, error) { <-p.killed; return -1, nil }
func (p hangingProcess) Kill() error {
	select {
	case <-p.killed:
	default:
		close(p.killed)
	}
	return nil
}

func (h *hangingEngine) Start(args []string, _, _ io.Writer) (process, error) {
	h.mu.Lock()
	h.calls = append(h.calls, args)
	h.mu.Unlock()
	return hangingProcess{killed: h.killed}, nil
}

// The fake's removal ends the hanging client, as the runtime's does.
func (h *hangingEngine) Output(ctx context.Context, args ...string) (string, error) {
	out, err := h.fakeEngine.Output(ctx, args...)
	if len(args) > 0 && args[0] == "rm" {
		_ = hangingProcess{killed: h.killed}.Kill() // ignored: the fake's Kill only closes the killed channel and never fails
	}
	return out, err
}

func TestRunContainerRemovesAtTheClientDeadline(t *testing.T) {
	t.Parallel()
	var eng *hangingEngine
	var ended string
	synctest.Test(t, func(*testing.T) {
		eng = &hangingEngine{killed: make(chan struct{})}
		_, ended = runContainer(context.Background(), eng, []string{"run", "x"}, "nova-functional-r", time.Now().Add(50*time.Millisecond), io.Discard, io.Discard)
	})
	assert.Equal(t, "deadline", ended)
	wantRemoved(t, eng.argvs(), "nova-functional-r")
}

func TestRunContainerRemovesOnInterrupt(t *testing.T) {
	t.Parallel()
	eng := &hangingEngine{killed: make(chan struct{})}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, ended := runContainer(ctx, eng, []string{"run", "x"}, "nova-functional-r", time.Now().Add(time.Hour), io.Discard, io.Discard)
	assert.Equal(t, "interrupted", ended)
	wantRemoved(t, eng.argvs(), "nova-functional-r")
}

func TestDispatchRefusals(t *testing.T) {
	t.Parallel()
	var out, errb bytes.Buffer
	assert.Equal(t, exitUsage, dispatch(context.Background(), nil, &out, &errb), "no verb")
	assert.Equal(t, exitUsage, dispatch(context.Background(), []string{"frob"}, &out, &errb), "unknown verb")
	assert.Contains(t, errb.String(), `unknown verb "frob"`)
	errb.Reset()
	assert.Equal(t, exitCannotRun, dispatch(context.Background(), []string{"run"}, &out, &errb), "run without packages")
	assert.Equal(t, 2, dispatch(context.Background(), []string{"reap", "extra"}, &out, &errb), "reap with an argument")
	out.Reset()
	assert.Equal(t, 0, dispatch(context.Background(), []string{"help"}, &out, &errb), "help")
	assert.Contains(t, out.String(), "functionalrun reap")
}

func TestRunTierPassesTheContainersExitThroughAndChecksForLeftovers(t *testing.T) {
	t.Parallel()
	for _, code := range []int{0, 2} {
		r := newRig(t, "501", code)
		// The prefill must finish 0 for the test container to run; the fake
		// gives both the same code, so a red run is judged at the module step.
		got := r.run()
		if code != 0 {
			assert.Equal(t, exitCannotRun, got, "a failed module step\n%s", r.stderr.String())
			assert.Contains(t, r.stderr.String(), "module cache step ended finished with exit 2")
			continue
		}
		r.requireExit(0)
		calls := r.calls()
		var runs []string
		for _, call := range calls {
			if strings.HasPrefix(call, "run ") {
				runs = append(runs, call)
			}
		}
		require.Equal(t, 2, len(runs), "want the module step then the test container:\n%s", strings.Join(runs, "\n"))
		assert.Contains(t, runs[0], "go mod download")
		assert.Contains(t, runs[1], "make test-functional")
		assert.Contains(t, runs[1], " sha256:img ", "the test container does not run the inspected image id")
		assert.Equal(t, strings.Join(reapListArgs(), " "), calls[0], "the reaper does not run first: %q", calls[0])
		last := calls[len(calls)-1]
		assert.True(t, strings.HasPrefix(last, "ps --all --filter label=nova.functional.run="),
			"the last call is not the leftover check by label: %q", last)
		assert.Contains(t, r.stderr.String(), "ended=finished exit=0 ")
		assert.Contains(t, r.stderr.String(), "containers_left=0", "receipt line:\n%s", r.stderr.String())
	}
}

func TestRunTierRefusesAnotherUsersCache(t *testing.T) {
	t.Parallel()
	r := newRig(t, "502", 0)
	r.run()
	r.requireExit(exitCannotRun)
	for _, call := range r.calls() {
		assert.False(t, strings.HasPrefix(call, "run "), "a container ran over another user's cache: %q", call)
	}
}

func TestSetupExit(t *testing.T) {
	t.Parallel()
	assert.Equal(t, exitCannotRun, setupExit(context.Background()))
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	assert.Equal(t, exitInterrupted, setupExit(ctx), "after an interrupt")
}

func TestClassify(t *testing.T) {
	t.Parallel()
	const dl = 10 * time.Minute
	for _, tc := range []struct {
		code      int
		ended     string
		elapsed   time.Duration
		wantEnded string
		wantExit  int
	}{
		{0, "finished", time.Minute, "finished", 0},
		{0, "finished", dl, "finished", 0},
		{1, "finished", time.Minute, "finished", 1},
		{2, "finished", time.Minute, "finished", 2},
		{2, "finished", dl - 2*time.Second, "finished", 2},
		{137, "finished", time.Minute, "finished", 137},
		{137, "finished", dl - innerMargin - time.Second, "finished", 137},
		{137, "finished", dl - innerMargin, "inner-timeout", 124},
		{137, "finished", dl - 5*time.Second, "inner-timeout", 124},
		{124, "finished", time.Minute, "inner-timeout", 124},
		{124, "finished", dl - innerMargin, "inner-timeout", 124},
		{2, "finished", dl - time.Second, "deadline", 124},
		{255, "finished", dl, "deadline", 124},
		{-1, "finished", time.Minute, "client-lost", 125},
		{-1, "finished", dl, "client-lost", 125},
		{-1, "deadline", dl + clientGrace, "deadline", 124},
		{-1, "interrupted", time.Second, "interrupted", 130},
	} {
		ended, exit := classify(tc.code, tc.ended, tc.elapsed, dl)
		at := fmt.Sprintf("classify(%d, %s, %s)", tc.code, tc.ended, tc.elapsed)
		assert.Equal(t, tc.wantEnded, ended, at)
		assert.Equal(t, tc.wantExit, exit, at)
	}
}

// runTierIn is runTier in a synctest bubble, whose clock is fake: a deadline
// or a pause between two listings is a step of it, never a wait.
func runTierIn(t *testing.T, eng engine, c runConfig, stdout, stderr io.Writer) (code int) {
	t.Helper()
	synctest.Test(t, func(*testing.T) { code = runTier(context.Background(), eng, c, stdout, stderr) })
	return code
}

// deadlineEngine is a fakeEngine whose start number hang (from 0) never
// returns until its container is removed, and which notes the bubble's time
// of that start and of that removal.
type deadlineEngine struct {
	*fakeEngine
	hang, starts     int
	killed           chan struct{}
	started, removed time.Time
}

func (d *deadlineEngine) Start(args []string, stdout, stderr io.Writer) (process, error) {
	p, err := d.fakeEngine.Start(args, stdout, stderr)
	if d.starts++; d.starts-1 != d.hang {
		return p, err
	}
	d.started = time.Now()
	return hangingProcess{killed: d.killed}, nil
}

func (d *deadlineEngine) Output(ctx context.Context, args ...string) (string, error) {
	out, err := d.fakeEngine.Output(ctx, args...)
	if len(args) > 0 && args[0] == "rm" && !d.started.IsZero() && d.removed.IsZero() {
		d.removed = time.Now()
		_ = hangingProcess{killed: d.killed}.Kill() // ignored: the fake's Kill only closes the killed channel and never fails
	}
	return out, err
}

func TestRunTierClientDeadlines(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		step     string
		hang     int
		bound    func(runConfig) time.Duration
		wantExit int
	}{
		{"the module step", 0, func(runConfig) time.Duration { return prefillDeadline }, exitCannotRun},
		{"the run", 1, func(c runConfig) time.Duration { return c.deadline }, exitDeadline},
	} {
		r := newRig(t, "501", 0)
		var got int
		var eng *deadlineEngine
		synctest.Test(t, func(*testing.T) {
			eng = &deadlineEngine{fakeEngine: r.eng, hang: tc.hang, killed: make(chan struct{})}
			got = runTier(context.Background(), eng, r.cfg, &r.stdout, &r.stderr)
		})
		assert.Equal(t, tc.wantExit, got, "%s: %s", tc.step, r.stderr.String())
		assert.Equal(t, tc.bound(r.cfg)+clientGrace, eng.removed.Sub(eng.started), "%s: its container is removed at the bound plus the grace", tc.step)
	}
}

func TestRunTierExitCodes(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		testCode  int
		wantExit  int
		wantEnded string
	}{
		{0, 0, "finished"},
		{2, 2, "finished"},
		{124, 124, "inner-timeout"},
		{-1, 125, "client-lost"},
	} {
		r := newRig(t, "501", 0, tc.testCode)
		got := r.run()
		assert.Equal(t, tc.wantExit, got, "test container exit %d\n%s", tc.testCode, r.stderr.String())
		assert.Contains(t, r.stderr.String(), "ended="+tc.wantEnded+" exit="+strconv.Itoa(tc.wantExit)+" ")
	}
}

func TestRunTierFailsWhenAContainerIsLeft(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		answer fakeAnswer
		want   string
	}{
		{fakeAnswer{out: "abc123\n"}, "containers_left=1"},
		{fakeAnswer{err: fmt.Errorf("runtime gone")}, "containers_left=unknown"},
	} {
		r := newRig(t, "501", 0)
		r.eng.respond = func(args []string) (fakeAnswer, bool) {
			if len(args) > 3 && args[0] == "ps" && strings.HasPrefix(args[3], "label="+labelRun+"=") && !strings.HasSuffix(args[3], "-mod") {
				return tc.answer, true
			}
			return fakeAnswer{}, false
		}
		got := r.run()
		assert.Equal(t, exitCannotRun, got, "a green run with a leftover (%v)\n%s", tc.answer, r.stderr.String())
		assert.Contains(t, r.stderr.String(), tc.want)
	}
}

// orderWriter fails the test if anything is written before the engine saw a
// removal.
type orderWriter struct {
	t   *testing.T
	eng *hangingEngine
}

func (w orderWriter) Write(p []byte) (int, error) {
	removed := false
	for _, c := range w.eng.argvs() {
		if strings.HasPrefix(c, "rm ") {
			removed = true
		}
	}
	if !removed {
		w.t.Errorf("logged %q before the container was removed", p)
	}
	return len(p), nil
}

func TestRunContainerRemovesBeforeItLogs(t *testing.T) {
	t.Parallel()
	for _, interrupt := range []bool{false, true} {
		synctest.Test(t, func(t *testing.T) {
			eng := &hangingEngine{killed: make(chan struct{})}
			ctx, cancel := context.WithCancel(t.Context())
			deadline := time.Now().Add(time.Hour)
			if interrupt {
				cancel()
			} else {
				deadline = time.Now().Add(20 * time.Millisecond)
			}
			w := orderWriter{t: t, eng: eng}
			runContainer(ctx, eng, []string{"run", "x"}, "nova-functional-r", deadline, w, w)
			cancel()
		})
	}
}

func TestFreshGocacheIsAnAnonymousVolume(t *testing.T) {
	t.Parallel()
	c := testConfig()
	c.freshGocache = true
	vols := flagValues(testArgs(c, "sha256:abc", "run1", time.Unix(1_800_000_000, 0)), "-v")
	assert.Equal(t, []string{"/work/src:/src:ro", "nova-functional-gomod-uid501:/gomodcache:ro", "/gocache"}, vols, "mounts")
	r := newRig(t, "501", 0)
	r.cfg.freshGocache = true
	r.run()
	r.requireExit(0)
	for _, call := range r.calls() {
		assert.NotContains(t, call, r.cfg.gocache, "a fresh-cache run touched the shared build cache: %q", call)
	}
}

func TestPackageArgumentsAreAllowlisted(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	testkit.WriteFile(t, filepath.Join(dir, "go.mod"), "module x\n")
	for _, ok := range []string{".", "./...", "./pkg/ntable", "./pkg/ntable/", "./pkg/ntable/...", "./cmd/nova-ci", "./a_b.c-d/e"} {
		_, err := parseRun([]string{"--src", dir, "--image", "x", ok})
		assert.NoError(t, err, "package %q", ok)
	}
	for _, bad := range []string{
		"./pkg/ntable/;id>&2;cat</etc/hostname>&2;exit",
		"./a|b", "./a&b", "./a<b", "./a>b", "./a*", "./a(b)", "./a;b", "./a$b", "./a`b`", "./a'b", `./a"b`, `./a\b`,
		"./a b", "./a\tb", "./a\nb", "./../../../etc", "./a/../b", "..", "/abs", "internal/x", "",
	} {
		// The runtime is a path that does not exist, so even a refusal that
		// regressed could never reach a real one; the refusal is before it.
		var stdout, stderr bytes.Buffer
		code := dispatch(context.Background(), []string{"run", "--src", dir, "--image", "x", "--podman", filepath.Join(dir, "no-such-podman"), bad}, &stdout, &stderr)
		assert.Equal(t, exitCannotRun, code, "package %q: %q", bad, stderr.String())
		assert.Contains(t, stderr.String(), "not a package directory")
		assert.NotContains(t, stderr.String(), "no-such-podman")
	}
}

// stuckEngine hangs every listing until its context ends, as a wedged runtime.
type stuckEngine struct {
	fakeEngine
}

func (s *stuckEngine) Output(ctx context.Context, args ...string) (string, error) {
	s.mu.Lock()
	s.calls = append(s.calls, args)
	s.mu.Unlock()
	<-ctx.Done()
	return "", ctx.Err()
}

func TestLeftoversHaveOneBudget(t *testing.T) {
	t.Parallel()
	eng := &stuckEngine{}
	var n int
	var took time.Duration
	synctest.Test(t, func(*testing.T) {
		start := time.Now()
		n = leftovers(eng, "run1", 100*time.Millisecond)
		took = time.Since(start)
	})
	assert.LessOrEqual(t, took, time.Second, "a hung runtime held the leftover check %s past a 100ms budget", took)
	assert.Equal(t, -1, n, "an unreadable count is not -1 (unknown)")
}

// ours are the labels this tool writes for a run of uid 501.
func ours(run string, start, deadline string) map[string]string {
	return map[string]string{labelRun: run, labelOwner: "501", labelStart: start, labelDeadline: deadline}
}

func TestJudgeOnlyOursByDeadlineLabelPlusGrace(t *testing.T) {
	t.Parallel()
	now := time.Unix(1_800_001_000, 0)
	grace := 30 * time.Second
	u := func(sec int64) string { return strconv.FormatInt(sec, 10) }
	st := u(now.Unix() - 7200)
	const id = "20300102t030405-0123abcd"
	with := func(m map[string]string, k, v string) map[string]string {
		out := map[string]string{}
		for a, b := range m {
			out[a] = b
		}
		if v == "<delete>" {
			delete(out, k)
		} else {
			out[k] = v
		}
		return out
	}
	base := ours(id, st, u(now.Unix()-31))
	cs := []listed{
		{ID: "a-overdue", State: "running", Labels: base},
		{ID: "a2-overdue-mod", State: "exited", Labels: with(base, labelRun, id+"-mod")},
		{ID: "b-in-grace", State: "running", Labels: with(base, labelDeadline, u(now.Unix()-30))},
		{ID: "c-young", State: "created", Labels: with(base, labelDeadline, u(now.Unix()+600))},
		{ID: "d-no-deadline", State: "exited", Labels: with(base, labelDeadline, "<delete>")},
		{ID: "e-garbage", State: "exited", Labels: with(base, labelDeadline, "soon")},
		{ID: "h-zero", State: "exited", Labels: with(base, labelDeadline, "0")},
		{ID: "i-negative", State: "exited", Labels: with(base, labelDeadline, "-5")},
		{ID: "j-plus", State: "exited", Labels: with(base, labelDeadline, "+1")},
		{ID: "k-far", State: "exited", Labels: with(base, labelDeadline, "99999999999")},
		{ID: "l-no-start", State: "exited", Labels: with(base, labelStart, "<delete>")},
		{ID: "m-before-start", State: "exited", Labels: with(base, labelDeadline, u(now.Unix()-7201))},
		{ID: "g-created-never-started", State: "configured", Labels: with(base, labelDeadline, u(now.Unix()-3600))},
		// Not ours: never judged, whatever else they carry.
		{ID: "f-unlabelled", Names: []string{"nova-functional-decoy"}, State: "exited", Labels: map[string]string{labelDeadline: u(1)}},
		{ID: "n-other-tool", State: "exited", Labels: with(base, labelRun, "othertool-value")},
		{ID: "o-empty-run", State: "exited", Labels: with(base, labelRun, "")},
		{ID: "p-other-owner", State: "exited", Labels: with(base, labelOwner, "9999")},
		{ID: "q-no-owner", State: "exited", Labels: with(base, labelOwner, "<delete>")},
		{ID: "r-upper", State: "exited", Labels: with(base, labelRun, "20300102T030405-0123ABCD")},
	}
	verdicts, foreign := judge(cs, now, grace, "501")
	assert.Equal(t, 5, foreign, "the other tool, the empty run, the other owner, no owner and the wrong shape are foreign")
	got := map[string]verdict{}
	for _, v := range verdicts {
		got[v.id] = v
	}
	for _, id := range []string{"f-unlabelled", "n-other-tool", "o-empty-run", "p-other-owner", "q-no-owner", "r-upper"} {
		assert.NotContains(t, got, id, "%s is not this tool's container of this user, and was judged", id)
	}
	for id, want := range map[string]struct {
		remove bool
		reason string
	}{
		"a-overdue":               {true, ""},
		"a2-overdue-mod":          {true, ""},
		"b-in-grace":              {false, "within-deadline"},
		"c-young":                 {false, "within-deadline"},
		"d-no-deadline":           {false, "unreadable-deadline"},
		"e-garbage":               {false, "unreadable-deadline"},
		"h-zero":                  {false, "unreadable-deadline"},
		"i-negative":              {false, "unreadable-deadline"},
		"j-plus":                  {false, "unreadable-deadline"},
		"k-far":                   {false, "unreadable-bound"},
		"l-no-start":              {false, "unreadable-start"},
		"m-before-start":          {false, "unreadable-bound"},
		"g-created-never-started": {true, ""},
	} {
		v, ok := got[id]
		require.True(t, ok, "%s not judged", id)
		assert.Equal(t, want.remove, v.remove, id)
		assert.Equal(t, want.reason, v.reason, id)
	}
}

func TestReapRemovesOnlyOverdueContainersOfOurs(t *testing.T) {
	t.Parallel()
	now := time.Unix(1_800_001_000, 0)
	st := strconv.FormatInt(now.Unix()-7200, 10)
	listing := fmt.Sprintf(`[
 {"Id":"aaaaaaaaaaaaaaaa1","Names":["nova-functional-old"],"State":"running","Labels":{"%[1]s":"20300102t030405-0000000a","%[2]s":"%[3]d","%[4]s":"501","%[5]s":"%[7]s"}},
 {"Id":"bbbbbbbbbbbbbbbb2","Names":["nova-functional-young"],"State":"running","Labels":{"%[1]s":"20300102t030405-0000000b","%[2]s":"%[6]d","%[4]s":"501","%[5]s":"%[7]s"}},
 {"Id":"cccccccccccccccc3","Names":["nova-functional-decoy"],"State":"exited","Labels":{"other":"x"}},
 {"Id":"dddddddddddddddd4","Names":["someone-else"],"State":"exited","Labels":{"%[1]s":"20300102t030405-0000000d","%[2]s":"%[3]d","%[4]s":"9999","%[5]s":"%[7]s"}}
]`, labelRun, labelDeadline, now.Unix()-3600, labelOwner, labelStart, now.Unix()+3600, st)
	eng := &fakeEngine{answers: map[string]fakeAnswer{strings.Join(reapListArgs(), " "): {out: listing}}}
	var stderr bytes.Buffer
	n, unreadable, err := reap(context.Background(), eng, now, 30*time.Second, "501", false, &stderr)
	require.NoError(t, err)
	assert.Equal(t, 1, n, "reaped")
	assert.Zero(t, unreadable, "unreadable")
	calls := eng.argvs()
	want := []string{strings.Join(reapListArgs(), " "), strings.Join(removeArgs("aaaaaaaaaaaaaaaa1"), " ")}
	assert.Equal(t, strings.Join(want, "\n"), strings.Join(calls, "\n"), "calls")
	for _, c := range calls {
		verb := strings.Fields(c)[0]
		assert.True(t, (verb == "ps" || verb == "rm") && !strings.Contains(c, "prune"),
			"the reaper does more than list and remove containers: %q", c)
	}
	out := stderr.String()
	assert.Contains(t, out, "REAPED id=aaaaaaaaaaaa run=20300102t030405-0000000a state=running")
	assert.Contains(t, out, "REAP containers=4 reaped=1 left=1 unreadable=0 foreign=1")
}

func TestReapReportsUnreadableAndDispatchExitsNonZero(t *testing.T) {
	t.Parallel()
	now := time.Unix(1_800_001_000, 0)
	listing := fmt.Sprintf(`[{"Id":"a1","State":"exited","Labels":{"%s":"20300102t030405-0000000a","%s":"501","%s":"1","%s":"abc"}}]`, labelRun, labelOwner, labelStart, labelDeadline)
	eng := &fakeEngine{answers: map[string]fakeAnswer{strings.Join(reapListArgs(), " "): {out: listing}}}
	var stderr bytes.Buffer
	n, unreadable, err := reap(context.Background(), eng, now, 0, "501", false, &stderr)
	require.NoError(t, err)
	assert.Zero(t, n)
	assert.Equal(t, 1, unreadable)
	assert.Len(t, eng.argvs(), 1, "an unreadable container was touched: %q", eng.argvs())
	assert.Contains(t, stderr.String(), "REAP-UNREADABLE id=a1 run=20300102t030405-0000000a state=exited reason=unreadable-deadline")
}

func TestReapDryRunChangesNothing(t *testing.T) {
	t.Parallel()
	now := time.Unix(1_800_001_000, 0)
	listing := fmt.Sprintf(`[{"Id":"a1","State":"exited","Labels":{"%s":"20300102t030405-0000000a","%s":"501","%s":"1","%s":"2"}}]`, labelRun, labelOwner, labelStart, labelDeadline)
	eng := &fakeEngine{answers: map[string]fakeAnswer{strings.Join(reapListArgs(), " "): {out: listing}}}
	var stderr bytes.Buffer
	n, _, err := reap(context.Background(), eng, now, 0, "501", true, &stderr)
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	assert.Len(t, eng.argvs(), 1, "a dry run ran more than the listing: %q", eng.argvs())
	assert.Contains(t, stderr.String(), "REAP-WOULD id=a1 run=20300102t030405-0000000a")
}

func TestEveryRunIDMatchesTheReapersPattern(t *testing.T) {
	t.Parallel()
	id := newRunID(time.Now())
	assert.True(t, runIDRE.MatchString(id) && runIDRE.MatchString(id+"-mod"),
		"the reaper would not take this tool's own run id %q", id)
	labels := map[string]string{}
	args := testArgs(testConfig(), "img", id, time.Unix(1_800_000_000, 0))
	for _, kv := range flagValues(args, "--label") {
		k, v, _ := strings.Cut(kv, "=")
		labels[k] = v
	}
	vs, foreign := judge([]listed{{ID: "x", Labels: labels}}, time.Unix(1_800_000_000, 0).Add(testConfig().deadline+time.Minute), 30*time.Second, "501")
	assert.Zero(t, foreign, "the labels a run writes are not foreign: %+v", vs)
	require.Len(t, vs, 1)
	assert.True(t, vs[0].remove, "the reaper does not take an overdue container with the labels a run writes: %+v", vs)
}

func TestChooseRuntimePrefersPodmanThenDocker(t *testing.T) {
	t.Parallel()
	has := func(names ...string) func(string) (string, error) {
		return func(n string) (string, error) {
			for _, h := range names {
				if h == n {
					return "/bin/" + n, nil
				}
			}
			return "", errors.New("not found")
		}
	}
	for _, c := range []struct {
		name, explicit string
		look           func(string) (string, error)
		bin, want      string
		fail           bool
	}{
		{"both", "", has("docker", "podman"), "/bin/podman", "podman", false},
		{"docker only", "", has("docker"), "/bin/docker", "docker", false},
		{"neither", "", has(), "", "", true},
		{"explicit is not looked up", "/opt/x/podman", has(), "/opt/x/podman", "podman", false},
	} {
		bin, name, err := chooseRuntime(c.explicit, c.look)
		assert.Equal(t, c.fail, err != nil, c.name)
		assert.Equal(t, c.bin, bin, c.name)
		assert.Equal(t, c.want, name, c.name)
	}
	var stderr bytes.Buffer
	bin, ok := useRuntime("", has("docker"), &stderr)
	assert.True(t, ok)
	assert.Equal(t, "/bin/docker", bin)
	assert.Contains(t, stderr.String(), "container runtime: docker (/bin/docker)")
}
