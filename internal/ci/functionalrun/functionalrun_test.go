package functionalrun

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeEngine records every argv and answers from a table keyed by the argv's
// first words. Nothing here starts a container or a process.
type fakeEngine struct {
	// kind is the runtime the fake stands for; empty is podman.
	kind    string
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

func (f *fakeEngine) Kind() string {
	if f.kind == "" {
		return kindPodman
	}
	return f.kind
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
		packages: []string{"./internal/ntable/...", "./internal/config/"},
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
	require.Failf(t, "flag missing", "argv has no %s: %q", flag, args)
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

func indexOf(args []string, s string) int {
	for i, a := range args {
		if a == s {
			return i
		}
	}
	return -1
}

// writeFile writes one file of a fixture, making its directory.
func writeFile(t *testing.T, path, body string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
}

// lastCall is the last argv an engine saw.
func lastCall(t *testing.T, eng interface{ argvs() []string }) string {
	t.Helper()
	calls := eng.argvs()
	require.NotEmpty(t, calls)
	return calls[len(calls)-1]
}

func TestTestArgsHoldTheRunInsideItsBounds(t *testing.T) {
	t.Parallel()
	c := testConfig()
	start := time.Unix(1_800_000_000, 0)
	args := testArgs(c, "sha256:abc", "run1", start)
	joined := strings.Join(args, " ")

	assert.Equal(t, "run", args[0])
	assert.Contains(t, joined, " --rm ")
	assert.Contains(t, joined, " --init ")
	assert.Equal(t, "nova-functional-run1", flagValue(t, args, "--name"))
	assert.Equal(t, "600", flagValue(t, args, "--timeout"), "the deadline in seconds")
	for flag, want := range map[string]string{
		"--security-opt": "no-new-privileges", "--cap-drop": "all",
		"--network": "none", "--ipc": "private", "--pid": "private", "--pids-limit": "1024",
		"--memory": "4g", "--memory-swap": "4g", "--cpus": "4", "-w": "/src",
	} {
		assert.Equal(t, want, flagValue(t, args, flag), flag)
	}
	assert.Contains(t, joined, " --read-only ", "the image is mounted read-only")
	wantVols := []string{"/work/src:/src:ro", "nova-functional-gomod-uid501:/gomodcache:ro", "nova-functional-gocache-uid501:/gocache"}
	assert.Equal(t, wantVols, flagValues(args, "-v"))
	tmpfs := flagValues(args, "--tmpfs")
	require.Len(t, tmpfs, 2)
	assert.Equal(t, "/tmp:rw,exec,size=2g", tmpfs[0])
	assert.True(t, strings.HasPrefix(tmpfs[1], "/home/bench:"), "%q", tmpfs)
	wantLabels := []string{"nova.functional.run=run1", "nova.functional.start=1800000000", "nova.functional.deadline=1800000600", "nova.functional.owner=501"}
	assert.Equal(t, wantLabels, flagValues(args, "--label"))
	// No host environment and no network proxy reach the test container.
	assert.NotContains(t, joined, "--env-host")
	assert.Empty(t, flagValues(args, "-e"))
	// The command: the inner timeout 10 s under the deadline, go test's 20 s
	// under it, through the Makefile's own target.
	i := indexOf(args, "sha256:abc")
	require.GreaterOrEqual(t, i, 0, "no image in argv: %q", joined)
	assert.Equal(t, []string{"timeout", "-k", "5", "590", "make", "test-functional",
		"PKGS=./internal/ntable/... ./internal/config/", "GOTEST_P=4", "FUNCTIONAL_TIMEOUT=580s"}, args[i+1:])
	// Every flag of the runtime comes before the image.
	for _, f := range []string{"--timeout", "--network", "--label", "-v"} {
		assert.Less(t, indexOf(args, f), i, "%s comes after the image", f)
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
	c := testConfig()
	start := time.Unix(1_800_000_000, 0)
	args := prefillArgs(c, "sha256:abc", "run1", "stamp123", "http://127.0.0.1:3128", start)
	assert.NotContains(t, strings.Join(args, " "), "--network none", "the module step has the network")
	assert.Equal(t, []string{"/work/src:/src:ro", "nova-functional-gomod-uid501:/gomodcache"}, flagValues(args, "-v"),
		"the source read-only and the module cache writable, and no build cache")
	assert.Equal(t, "nova-functional-run1-mod", flagValue(t, args, "--name"))
	labels := flagValues(args, "--label")
	require.Len(t, labels, 4)
	assert.Equal(t, "nova.functional.run=run1-mod", labels[0])
	assert.Equal(t, "nova.functional.deadline="+strconv.FormatInt(start.Add(prefillDeadline).Unix(), 10), labels[2],
		"the module step is a labelled run with its own deadline")
	assert.Equal(t, strconv.Itoa(int(prefillDeadline/time.Second)), flagValue(t, args, "--timeout"))
	assert.Equal(t, "no-new-privileges", flagValue(t, args, "--security-opt"))
	assert.Equal(t, "all", flagValue(t, args, "--cap-drop"))
	assert.Equal(t, "GOPROXY=http://127.0.0.1:3128", flagValue(t, args, "-e"))
	assert.Equal(t, []string{"functionalrun", "stamp123"}, args[len(args)-2:], "the stamp is the script's $1")
}

func TestModuleProxy(t *testing.T) {
	t.Parallel()
	env := func(v string) func(string) string { return func(string) string { return v } }
	for in, want := range map[string]string{
		"":                             defaultProxy,
		"off":                          defaultProxy,
		"http://127.0.0.1:3128,direct": "http://127.0.0.1:3128,direct",
	} {
		assert.Equal(t, want, moduleProxy(env(in)), "moduleProxy(%q)", in)
	}
}

func TestRuntimeEnvDropsTheRunnerTrackingID(t *testing.T) {
	t.Parallel()
	got := runtimeEnv([]string{"PATH=/bin", "RUNNER_TRACKING_ID=github_abc", "HOME=/h", "RUNNER_TRACKING_IDX=keep"})
	assert.Equal(t, []string{"PATH=/bin", "HOME=/h", "RUNNER_TRACKING_IDX=keep"}, got)
}

func TestRemoveArgsForceWithVolumesAndNoWait(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "rm --force --ignore --volumes --time 0 abc", strings.Join(removeArgs("abc"), " "))
}

func TestSelectionIsByLabelNeverByName(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{reapListArgs(), leftoverArgs("run1"), reapListArgsFor(kindDocker)} {
		joined := strings.Join(args, " ")
		assert.NotContains(t, joined, "name=", "selects by name")
		assert.True(t, strings.HasPrefix(flagValue(t, args, "--filter"), "label="+labelRun), "--filter is the run label: %q", joined)
		assert.Contains(t, joined, "--all", "lists containers in every state")
	}
	assert.Equal(t, "label=nova.functional.run=run1", flagValue(t, leftoverArgs("run1"), "--filter"), "the leftover check selects its own run")
}

func TestNewRunIDIsANameAndALabel(t *testing.T) {
	t.Parallel()
	start := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	a, b := newRunID(start), newRunID(start)
	assert.NotEqual(t, a, b, "two runs at one instant share an id")
	assert.True(t, strings.HasPrefix(a, "20300102t030405-"), a)
	assert.True(t, volumeNameRE.MatchString(a), a)
	assert.Equal(t, strings.ToLower(a), a, "a lowercase container name")
}

func TestCacheVolumesArePerUserAndCarryNoRunLabel(t *testing.T) {
	t.Parallel()
	assert.NotEqual(t, cacheVolumeName("gocache", "501"), cacheVolumeName("gocache", "502"), "two users share the build cache")
	labels := strings.Join(flagValues(volumeCreateArgs("v", "gocache", "501"), "--label"), ",")
	assert.Equal(t, "nova.functional.cache=gocache,nova.functional.owner=501", labels)
	assert.NotContains(t, labels, labelRun, "a cache carries no run label, or a reap or a leftover check would count it")
}

func TestEnsureVolume(t *testing.T) {
	t.Parallel()
	inspect := strings.Join(volumeInspectArgs("v"), " ")
	create := strings.Join(volumeCreateArgs("v", "gocache", "501"), " ")
	ctx := context.Background()

	mine := &fakeEngine{answers: map[string]fakeAnswer{inspect: {out: "501|gocache\n"}}}
	require.NoError(t, ensureVolume(ctx, mine, "v", "gocache", "501"), "this user's volume is refused")
	assert.Len(t, mine.argvs(), 1, "an existing volume of this user is touched beyond the inspect")

	theirs := &fakeEngine{answers: map[string]fakeAnswer{inspect: {out: "502|gocache\n"}}}
	err := ensureVolume(ctx, theirs, "v", "gocache", "501")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--gocache-volume", "another user's volume is refused with the flag to use")

	swapped := &fakeEngine{answers: map[string]fakeAnswer{inspect: {out: "501|gomod\n"}}}
	err = ensureVolume(ctx, swapped, "v", "gocache", "501")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `a "gomod" cache, not a gocache cache`)
	unlabelled := &fakeEngine{answers: map[string]fakeAnswer{inspect: {out: "501|\n"}}}
	assert.Error(t, ensureVolume(ctx, unlabelled, "v", "gocache", "501"), "a volume with no kind label")

	missing := &fakeEngine{answers: map[string]fakeAnswer{inspect: {err: errors.New("no such volume")}}}
	require.NoError(t, ensureVolume(ctx, missing, "v", "gocache", "501"))
	calls := missing.argvs()
	require.Len(t, calls, 2)
	assert.Equal(t, create, calls[1], "a missing volume is created with its owner")

	// Two first runs at once: this one's create loses, and it looks again.
	for _, tc := range []struct {
		second string
		ok     bool
	}{{"501|gocache", true}, {"502|gocache", false}, {"501|gomod", false}} {
		inspected := 0
		race := &fakeEngine{respond: func(args []string) (fakeAnswer, bool) {
			switch strings.Join(args, " ") {
			case inspect:
				inspected++
				if inspected == 1 {
					return fakeAnswer{err: errors.New("no such volume")}, true
				}
				return fakeAnswer{out: tc.second}, true
			case create:
				return fakeAnswer{err: errors.New("volume already exists")}, true
			}
			return fakeAnswer{}, false
		}}
		err := ensureVolume(ctx, race, "v", "gocache", "501")
		assert.Equal(t, tc.ok, err == nil, "lost the create race to %q: %v", tc.second, err)
		rc := race.argvs()
		require.Len(t, rc, 3, "a lost create looks again")
		assert.Equal(t, inspect, rc[2])
	}
}

func TestParseListed(t *testing.T) {
	t.Parallel()
	for _, empty := range []string{"", "  \n", "[]", "null"} {
		cs, err := parseListed(empty)
		require.NoError(t, err, "%q", empty)
		assert.Empty(t, cs, "%q", empty)
	}
	_, err := parseListed("CONTAINER ID  IMAGE")
	assert.Error(t, err, "a table listing is not JSON")
}

func TestParseRun(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module x\n")
	ctxDir := filepath.Join(dir, "img")
	writeFile(t, filepath.Join(ctxDir, "Containerfile"), "FROM x\n")
	c, err := parseRun([]string{"--src", dir, "--context", ctxDir, "--deadline", "2m", "./internal/ntable/..."})
	require.NoError(t, err)
	assert.Equal(t, 2*time.Minute, c.deadline)
	assert.Equal(t, cacheVolumeName("gocache", c.ownerID), c.gocache)
	assert.Equal(t, cacheVolumeName("gomod", c.ownerID), c.gomod)
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--src", dir, "--context", ctxDir}, "no package"},
		{[]string{"--src", dir, "--context", ctxDir, "./a", "--deadline", "1m"}, "flags come first"},
		{[]string{"--src", dir, "--context", ctxDir, "internal/ntable"}, "not a package directory"},
		{[]string{"--src", dir, "--context", ctxDir, "./a b"}, "not a package directory"},
		{[]string{"--src", dir, "--context", ctxDir, "--deadline", "3s", "./a"}, "under 30s"},
		{[]string{"--src", dir, "--context", ctxDir, "--deadline", "0s", "./a"}, "under 30s"},
		{[]string{"--src", dir, "--context", ctxDir, "--deadline", "-1m", "./a"}, "under 30s"},
		{[]string{"--src", dir, "--context", ctxDir, "--deadline", "25h", "./a"}, "over 24h"},
		{[]string{"--src", dir, "--context", ctxDir, "--cpus", "0", "./a"}, "--cpus"},
		{[]string{"--src", dir, "--context", ctxDir, "--memory", "lots", "./a"}, "--memory"},
		{[]string{"--src", dir, "--context", dir, "./a"}, "no Containerfile"},
		{[]string{"--src", ctxDir, "./a"}, "no go.mod"},
		{[]string{"--src", dir, "--context", ctxDir, "--gocache-volume", "same", "--gomod-volume", "same", "./a"}, "two volumes"},
		{[]string{"--src", dir, "--context", ctxDir, "--gocache-volume", "../x", "./a"}, "not a podman volume name"},
		{[]string{"--src", dir, "--context", ctxDir, "--fresh-gocache", "--gocache-volume", "mine", "./a"}, "two different build caches"},
	} {
		_, err := parseRun(tc.args)
		require.Error(t, err, "%q", tc.args)
		assert.Contains(t, err.Error(), tc.want, "%q", tc.args)
	}
	// --image needs no context.
	_, err = parseRun([]string{"--src", dir, "--image", "localhost/x:y", "./a"})
	assert.NoError(t, err, "--image without a context")
}

func TestContextHashFollowsEveryFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "Containerfile"), "FROM a\n")
	h1, err := contextHash(dir)
	require.NoError(t, err)
	h2, err := contextHash(dir)
	require.NoError(t, err)
	assert.Equal(t, h1, h2, "the hash is stable")
	writeFile(t, filepath.Join(dir, "sub", "extra.txt"), "x")
	h3, err := contextHash(dir)
	require.NoError(t, err)
	assert.NotEqual(t, h1, h3, "a new file in the context changes the hash")
	writeFile(t, filepath.Join(dir, "Containerfile"), "FROM b\n")
	h4, err := contextHash(dir)
	require.NoError(t, err)
	assert.NotEqual(t, h3, h4, "an edit of the Containerfile changes the hash")
	tag := imageTag(h1)
	assert.True(t, strings.HasPrefix(tag, "localhost/nova-functional:ctx-"), tag)
	assert.Len(t, tag, len("localhost/nova-functional:ctx-")+16)
}

func TestRunContainerPassesTheExitCodeThrough(t *testing.T) {
	t.Parallel()
	for _, code := range []int{0, 1, 2, 124} {
		eng := &fakeEngine{startCode: code}
		got, ended := runContainer(context.Background(), eng, []string{"run", "x"}, "nova-functional-r", time.Now().Add(time.Minute), io.Discard, io.Discard)
		assert.Equal(t, code, got)
		assert.Equal(t, "finished", ended)
		// The container is removed after the client returns, whatever the code.
		assert.Equal(t, strings.Join(removeArgs("nova-functional-r"), " "), lastCall(t, eng), "no removal after the run")
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
	assert.Equal(t, strings.Join(removeArgs("nova-functional-r"), " "), lastCall(t, eng), "the container is removed at the deadline")
}

func TestRunContainerRemovesOnInterrupt(t *testing.T) {
	t.Parallel()
	eng := &hangingEngine{killed: make(chan struct{})}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, ended := runContainer(ctx, eng, []string{"run", "x"}, "nova-functional-r", time.Now().Add(time.Hour), io.Discard, io.Discard)
	assert.Equal(t, "interrupted", ended)
	assert.Equal(t, strings.Join(removeArgs("nova-functional-r"), " "), lastCall(t, eng), "the container is removed on an interrupt")
}

func TestDispatchRefusals(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	var out, errb bytes.Buffer
	assert.Equal(t, exitUsage, dispatch(ctx, nil, &out, &errb), "no verb")
	assert.Equal(t, exitUsage, dispatch(ctx, []string{"frob"}, &out, &errb), "unknown verb")
	assert.Contains(t, errb.String(), `unknown verb "frob"`)
	assert.Equal(t, exitCannotRun, dispatch(ctx, []string{"run"}, &out, &errb), "run without packages")
	assert.Equal(t, 2, dispatch(ctx, []string{"reap", "extra"}, &out, &errb), "reap with an argument")
	out.Reset()
	assert.Equal(t, 0, dispatch(ctx, []string{"help"}, &out, &errb), "help")
	assert.Contains(t, out.String(), "functionalrun reap")
}

func tierFixture(t *testing.T, owner string, startCode int) (*fakeEngine, runConfig) {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module x\n")
	c := testConfig()
	c.src = dir
	c.image = "localhost/nova-functional:given"
	eng := &fakeEngine{startCode: startCode, answers: map[string]fakeAnswer{
		strings.Join(reapListArgs(), " "):                                {out: "[]"},
		"image inspect --format {{.Id}} localhost/nova-functional:given": {out: "sha256:img\n"},
		strings.Join(volumeInspectArgs(c.gocache), " "):                  {out: owner + "|gocache\n"},
		strings.Join(volumeInspectArgs(c.gomod), " "):                    {out: owner + "|gomod\n"},
	}}
	return eng, c
}

func TestRunTierPassesTheContainersExitThroughAndChecksForLeftovers(t *testing.T) {
	t.Parallel()
	for _, code := range []int{0, 2} {
		eng, c := tierFixture(t, "501", code)
		var stdout, stderr bytes.Buffer
		// The prefill must finish 0 for the test container to run; the fake
		// gives both the same code, so a red run is judged at the module step.
		got := runTierIn(t, eng, c, &stdout, &stderr)
		calls := eng.argvs()
		if code != 0 {
			assert.Equal(t, exitCannotRun, got, "a failed module step")
			assert.Contains(t, stderr.String(), "module cache step ended finished with exit 2")
			continue
		}
		require.Equal(t, 0, got, "a green run: %s", stderr.String())
		var runs []string
		for _, call := range calls {
			if strings.HasPrefix(call, "run ") {
				runs = append(runs, call)
			}
		}
		require.Len(t, runs, 2, "the module step then the test container")
		assert.Contains(t, runs[0], "go mod download")
		assert.Contains(t, runs[1], "make test-functional")
		assert.Contains(t, runs[1], " sha256:img ", "the test container runs the inspected image id")
		assert.Equal(t, strings.Join(reapListArgs(), " "), calls[0], "the reaper runs first")
		assert.True(t, strings.HasPrefix(calls[len(calls)-1], "ps --all --filter label=nova.functional.run="), "the leftover check by label is last: %q", calls[len(calls)-1])
		assert.Contains(t, stderr.String(), "ended=finished exit=0 ")
		assert.Contains(t, stderr.String(), "containers_left=0")
	}
}

func TestRunTierRefusesAnotherUsersCache(t *testing.T) {
	t.Parallel()
	eng, c := tierFixture(t, "502", 0)
	var stdout, stderr bytes.Buffer
	assert.Equal(t, exitCannotRun, runTierIn(t, eng, c, &stdout, &stderr))
	for _, call := range eng.argvs() {
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
		assert.Equal(t, tc.wantEnded, ended, "classify(%d, %s, %s)", tc.code, tc.ended, tc.elapsed)
		assert.Equal(t, tc.wantExit, exit, "classify(%d, %s, %s)", tc.code, tc.ended, tc.elapsed)
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
		fake, c := tierFixture(t, "501", 0)
		var stdout, stderr bytes.Buffer
		var got int
		var eng *deadlineEngine
		synctest.Test(t, func(*testing.T) {
			eng = &deadlineEngine{fakeEngine: fake, hang: tc.hang, killed: make(chan struct{})}
			got = runTier(context.Background(), eng, c, &stdout, &stderr)
		})
		assert.Equal(t, tc.wantExit, got, "%s: %s", tc.step, stderr.String())
		assert.Equal(t, tc.bound(c)+clientGrace, eng.removed.Sub(eng.started), "%s: its container is removed at the bound plus the grace", tc.step)
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
		eng, c := tierFixture(t, "501", 0)
		eng.startCodes = []int{0, tc.testCode}
		var stdout, stderr bytes.Buffer
		got := runTierIn(t, eng, c, &stdout, &stderr)
		assert.Equal(t, tc.wantExit, got, "test container exit %d: %s", tc.testCode, stderr.String())
		assert.Contains(t, stderr.String(), "ended="+tc.wantEnded+" exit="+strconv.Itoa(tc.wantExit)+" ")
	}
}

func TestRunTierFailsWhenAContainerIsLeft(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		answer fakeAnswer
		want   string
	}{
		{fakeAnswer{out: "abc123\n"}, "containers_left=1"},
		{fakeAnswer{err: errors.New("runtime gone")}, "containers_left=unknown"},
	} {
		eng, c := tierFixture(t, "501", 0)
		eng.respond = func(args []string) (fakeAnswer, bool) {
			if len(args) > 3 && args[0] == "ps" && strings.HasPrefix(args[3], "label="+labelRun+"=") && !strings.HasSuffix(args[3], "-mod") {
				return tc.answer, true
			}
			return fakeAnswer{}, false
		}
		var stdout, stderr bytes.Buffer
		got := runTierIn(t, eng, c, &stdout, &stderr)
		assert.Equal(t, exitCannotRun, got, "a green run with a leftover (%v)", tc.answer)
		assert.Contains(t, stderr.String(), tc.want)
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
	assert.True(w.t, removed, "logged %q before the container was removed", p)
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
	assert.Equal(t, []string{"/work/src:/src:ro", "nova-functional-gomod-uid501:/gomodcache:ro", "/gocache"}, vols)
	eng, tc := tierFixture(t, "501", 0)
	tc.freshGocache = true
	var stdout, stderr bytes.Buffer
	require.Equal(t, 0, runTierIn(t, eng, tc, &stdout, &stderr), "%s", stderr.String())
	for _, call := range eng.argvs() {
		assert.NotContains(t, call, tc.gocache, "a fresh-cache run touched the shared build cache")
	}
}

func TestPackageArgumentsAreAllowlisted(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module x\n")
	for _, ok := range []string{".", "./...", "./internal/ntable", "./internal/ntable/", "./internal/ntable/...", "./cmd/nova-ci", "./a_b.c-d/e"} {
		_, err := parseRun([]string{"--src", dir, "--image", "x", ok})
		assert.NoError(t, err, "package %q", ok)
	}
	for _, bad := range []string{
		"./internal/ntable/;id>&2;cat</etc/hostname>&2;exit",
		"./a|b", "./a&b", "./a<b", "./a>b", "./a*", "./a(b)", "./a;b", "./a$b", "./a`b`", "./a'b", `./a"b`, `./a\b`,
		"./a b", "./a\tb", "./a\nb", "./../../../etc", "./a/../b", "..", "/abs", "internal/x", "",
	} {
		// The runtime is a path that does not exist, so even a refusal that
		// regressed could never reach a real one; the refusal is before it.
		var stdout, stderr bytes.Buffer
		code := dispatch(context.Background(), []string{"run", "--src", dir, "--image", "x", "--podman", filepath.Join(dir, "no-such-podman"), bad}, &stdout, &stderr)
		assert.Equal(t, exitCannotRun, code, "package %q", bad)
		assert.Contains(t, stderr.String(), "not a package directory", "package %q", bad)
		assert.NotContains(t, stderr.String(), "no-such-podman", "package %q", bad)
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
	// The bubble's clock is fake: the hung runtime holds the check for its one
	// budget plus at most one 250 ms pause between two listings, never a second budget.
	assert.InDelta(t, float64(100*time.Millisecond), float64(took), float64(250*time.Millisecond), "a hung runtime holds the leftover check for its one budget")
	assert.Equal(t, -1, n, "an unreadable count is -1 (unknown)")
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
	assert.Equal(t, 5, foreign, "the other tool, the empty run, the other owner, no owner, the wrong shape")
	got := map[string]verdict{}
	for _, v := range verdicts {
		got[v.id] = v
	}
	for _, id := range []string{"f-unlabelled", "n-other-tool", "o-empty-run", "p-other-owner", "q-no-owner", "r-upper"} {
		assert.NotContains(t, got, id, "not this tool's container of this user, and was judged")
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
		if assert.True(t, ok, "%s not judged", id) {
			assert.Equal(t, want.remove, v.remove, id)
			assert.Equal(t, want.reason, v.reason, id)
		}
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
	assert.Equal(t, 1, n)
	assert.Equal(t, 0, unreadable)
	calls := eng.argvs()
	assert.Equal(t, []string{strings.Join(reapListArgs(), " "), strings.Join(removeArgs("aaaaaaaaaaaaaaaa1"), " ")}, calls)
	for _, c := range calls {
		verb := strings.Fields(c)[0]
		assert.Contains(t, []string{"ps", "rm"}, verb, "the reaper does more than list and remove containers: %q", c)
		assert.NotContains(t, c, "prune")
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
	assert.Equal(t, 0, n)
	assert.Equal(t, 1, unreadable)
	assert.Len(t, eng.argvs(), 1, "an unreadable container was touched")
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
	assert.Len(t, eng.argvs(), 1, "a dry run ran more than the listing")
	assert.Contains(t, stderr.String(), "REAP-WOULD id=a1 run=20300102t030405-0000000a")
}

func TestEveryRunIDMatchesTheReapersPattern(t *testing.T) {
	t.Parallel()
	id := newRunID(time.Now())
	assert.True(t, runIDRE.MatchString(id), "the reaper would not take this tool's own run id %q", id)
	assert.True(t, runIDRE.MatchString(id+"-mod"), "%q", id+"-mod")
	labels := map[string]string{}
	args := testArgs(testConfig(), "img", id, time.Unix(1_800_000_000, 0))
	for _, kv := range flagValues(args, "--label") {
		k, v, _ := strings.Cut(kv, "=")
		labels[k] = v
	}
	vs, foreign := judge([]listed{{ID: "x", Labels: labels}}, time.Unix(1_800_000_000, 0).Add(testConfig().deadline+time.Minute), 30*time.Second, "501")
	assert.Equal(t, 0, foreign)
	require.Len(t, vs, 1)
	assert.True(t, vs[0].remove, "the reaper takes an overdue container with the labels a run writes")
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
		bin, name, err := chooseRuntime(c.explicit, "auto", c.look)
		assert.Equal(t, c.fail, err != nil, c.name)
		assert.Equal(t, c.bin, bin, c.name)
		assert.Equal(t, c.want, name, c.name)
	}
	var stderr bytes.Buffer
	bin, kind, ok := useRuntime("", "auto", has("docker"), &stderr)
	assert.True(t, ok)
	assert.Equal(t, "docker", kind)
	assert.Equal(t, "/bin/docker", bin)
	assert.Contains(t, stderr.String(), "container runtime: docker (/bin/docker)")
}
