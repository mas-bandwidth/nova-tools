package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
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

func TestTestArgsHoldTheRunInsideItsBounds(t *testing.T) {
	t.Parallel()
	c := testConfig()
	start := time.Unix(1_800_000_000, 0)
	args := testArgs(c, "sha256:abc", "run1", start)
	joined := strings.Join(args, " ")

	if args[0] != "run" || !strings.Contains(joined, " --rm ") || !strings.Contains(joined, " --init ") {
		t.Fatalf("not an attached run with --rm and --init: %q", joined)
	}
	if got := flagValue(t, args, "--name"); got != "nova-functional-run1" {
		t.Errorf("--name %q", got)
	}
	if got := flagValue(t, args, "--timeout"); got != "600" {
		t.Errorf("--timeout %q, want the deadline in seconds, 600", got)
	}
	for flag, want := range map[string]string{
		"--network": "none", "--ipc": "private", "--pids-limit": "1024",
		"--memory": "4g", "--memory-swap": "4g", "--cpus": "4", "-w": "/src",
	} {
		if got := flagValue(t, args, flag); got != want {
			t.Errorf("%s %q, want %q", flag, got, want)
		}
	}
	if !strings.Contains(joined, " --read-only ") {
		t.Errorf("the image is not mounted read-only: %q", joined)
	}
	vols := flagValues(args, "-v")
	wantVols := []string{"/work/src:/src:ro", "nova-functional-gomod-uid501:/gomodcache:ro", "nova-functional-gocache-uid501:/gocache"}
	if strings.Join(vols, ",") != strings.Join(wantVols, ",") {
		t.Errorf("mounts %q, want %q", vols, wantVols)
	}
	tmpfs := flagValues(args, "--tmpfs")
	if len(tmpfs) != 2 || tmpfs[0] != "/tmp:rw,exec,size=2g" || !strings.HasPrefix(tmpfs[1], "/home/bench:") {
		t.Errorf("scratch tmpfs %q", tmpfs)
	}
	labels := strings.Join(flagValues(args, "--label"), ",")
	wantLabels := "nova.functional.run=run1,nova.functional.start=1800000000,nova.functional.deadline=1800000600,nova.functional.owner=501"
	if labels != wantLabels {
		t.Errorf("labels %q, want %q", labels, wantLabels)
	}
	// No host environment and no network proxy reach the test container.
	if strings.Contains(joined, "--env-host") || len(flagValues(args, "-e")) != 0 {
		t.Errorf("the test container gets environment from the host: %q", joined)
	}
	// The command: the inner timeout 10 s under the deadline, go test's 20 s
	// under it, through the Makefile's own target.
	i := indexOf(args, "sha256:abc")
	if i < 0 {
		t.Fatalf("no image in argv: %q", joined)
	}
	cmd := strings.Join(args[i+1:], "\x00")
	want := strings.Join([]string{"timeout", "-k", "5", "590", "make", "test-functional",
		"PKGS=./internal/ntable/... ./internal/config/", "GOTEST_P=4", "FUNCTIONAL_TIMEOUT=580s"}, "\x00")
	if cmd != want {
		t.Errorf("command %q, want %q", strings.Split(cmd, "\x00"), strings.Split(want, "\x00"))
	}
	// Every flag of the runtime comes before the image.
	for _, f := range []string{"--timeout", "--network", "--label", "-v"} {
		if j := indexOf(args, f); j > i {
			t.Errorf("%s comes after the image", f)
		}
	}
}

func indexOf(args []string, s string) int {
	for i, a := range args {
		if a == s {
			return i
		}
	}
	return -1
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
		if got := timeoutSeconds(tc.d); got != tc.want {
			t.Errorf("timeoutSeconds(%s) = %d, want %d", tc.d, got, tc.want)
		}
	}
}

func TestPrefillHasTheNetworkAndWritesOnlyTheModuleCache(t *testing.T) {
	t.Parallel()
	c := testConfig()
	start := time.Unix(1_800_000_000, 0)
	args := prefillArgs(c, "sha256:abc", "run1", "stamp123", "https://proxy.example", start)
	joined := strings.Join(args, " ")
	if strings.Contains(joined, "--network none") {
		t.Errorf("the module step has no network: %q", joined)
	}
	vols := flagValues(args, "-v")
	if len(vols) != 2 || vols[0] != "/work/src:/src:ro" || vols[1] != "nova-functional-gomod-uid501:/gomodcache" {
		t.Errorf("mounts %q: want the source read-only and the module cache writable, and no build cache", vols)
	}
	if got := flagValue(t, args, "--name"); got != "nova-functional-run1-mod" {
		t.Errorf("--name %q", got)
	}
	labels := flagValues(args, "--label")
	if labels[0] != "nova.functional.run=run1-mod" || labels[2] != "nova.functional.deadline="+strconv.FormatInt(start.Add(prefillDeadline).Unix(), 10) {
		t.Errorf("the module step is not a labelled run with its own deadline: %q", labels)
	}
	if got := flagValue(t, args, "--timeout"); got != strconv.Itoa(int(prefillDeadline/time.Second)) {
		t.Errorf("--timeout %q", got)
	}
	if got := flagValue(t, args, "-e"); got != "GOPROXY=https://proxy.example" {
		t.Errorf("-e %q", got)
	}
	if args[len(args)-1] != "stamp123" || args[len(args)-2] != "functionalrun" {
		t.Errorf("the stamp is not the script's $1: %q", args[len(args)-3:])
	}
}

func TestModuleProxy(t *testing.T) {
	t.Parallel()
	env := func(v string) func(string) string { return func(string) string { return v } }
	for in, want := range map[string]string{
		"":                              "https://proxy.golang.org,direct",
		"off":                           "https://proxy.golang.org,direct",
		"https://mirror.example,direct": "https://mirror.example,direct",
	} {
		if got := moduleProxy(env(in)); got != want {
			t.Errorf("moduleProxy(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRuntimeEnvDropsTheRunnerTrackingID(t *testing.T) {
	t.Parallel()
	got := runtimeEnv([]string{"PATH=/bin", "RUNNER_TRACKING_ID=github_abc", "HOME=/h", "RUNNER_TRACKING_IDX=keep"})
	want := []string{"PATH=/bin", "HOME=/h", "RUNNER_TRACKING_IDX=keep"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("runtimeEnv = %q, want %q", got, want)
	}
}

func TestRemoveArgsForceWithVolumesAndNoWait(t *testing.T) {
	t.Parallel()
	got := strings.Join(removeArgs("abc"), " ")
	if got != "rm --force --ignore --volumes --time 0 abc" {
		t.Errorf("removeArgs = %q", got)
	}
}

func TestSelectionIsByLabelNeverByName(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{reapListArgs(), leftoverArgs("run1")} {
		joined := strings.Join(args, " ")
		if strings.Contains(joined, "name=") {
			t.Errorf("selects by name: %q", joined)
		}
		if f := flagValue(t, args, "--filter"); !strings.HasPrefix(f, "label="+labelRun) {
			t.Errorf("--filter %q is not the run label", f)
		}
		if !strings.Contains(joined, "--all") {
			t.Errorf("does not list containers in every state: %q", joined)
		}
	}
	if f := flagValue(t, leftoverArgs("run1"), "--filter"); f != "label=nova.functional.run=run1" {
		t.Errorf("the leftover check does not select its own run: %q", f)
	}
}

func TestNewRunIDIsANameAndALabel(t *testing.T) {
	t.Parallel()
	start := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	a, b := newRunID(start), newRunID(start)
	if a == b {
		t.Errorf("two runs at one instant share an id: %q", a)
	}
	if !strings.HasPrefix(a, "20300102t030405-") || !volumeNameRE.MatchString(a) || strings.ToLower(a) != a {
		t.Errorf("run id %q is not a lowercase container name", a)
	}
}

func TestCacheVolumesArePerUserAndCarryNoRunLabel(t *testing.T) {
	t.Parallel()
	if a, b := cacheVolumeName("gocache", "501"), cacheVolumeName("gocache", "502"); a == b {
		t.Errorf("two users share the build cache %q", a)
	}
	args := volumeCreateArgs("v", "gocache", "501")
	labels := strings.Join(flagValues(args, "--label"), ",")
	if labels != "nova.functional.cache=gocache,nova.functional.owner=501" {
		t.Errorf("cache volume labels %q", labels)
	}
	if strings.Contains(labels, labelRun) {
		t.Errorf("a cache carries the run label, so a reap or a leftover check would count it: %q", labels)
	}
}

func TestEnsureVolume(t *testing.T) {
	t.Parallel()
	inspect := strings.Join(volumeInspectArgs("v"), " ")
	create := strings.Join(volumeCreateArgs("v", "gocache", "501"), " ")

	mine := &fakeEngine{answers: map[string]fakeAnswer{inspect: {out: "501|gocache\n"}}}
	if err := ensureVolume(context.Background(), mine, "v", "gocache", "501"); err != nil {
		t.Errorf("this user's volume is refused: %v", err)
	}
	if n := len(mine.argvs()); n != 1 {
		t.Errorf("an existing volume of this user is touched beyond the inspect: %q", mine.argvs())
	}

	theirs := &fakeEngine{answers: map[string]fakeAnswer{inspect: {out: "502|gocache\n"}}}
	err := ensureVolume(context.Background(), theirs, "v", "gocache", "501")
	if err == nil || !strings.Contains(err.Error(), "--gocache-volume") {
		t.Errorf("another user's volume is not refused with the flag to use: %v", err)
	}

	swapped := &fakeEngine{answers: map[string]fakeAnswer{inspect: {out: "501|gomod\n"}}}
	err = ensureVolume(context.Background(), swapped, "v", "gocache", "501")
	if err == nil || !strings.Contains(err.Error(), `a "gomod" cache, not a gocache cache`) {
		t.Errorf("the module cache is taken as the build cache: %v", err)
	}
	unlabelled := &fakeEngine{answers: map[string]fakeAnswer{inspect: {out: "501|\n"}}}
	if err := ensureVolume(context.Background(), unlabelled, "v", "gocache", "501"); err == nil {
		t.Errorf("a volume with no kind label is taken as the build cache")
	}

	missing := &fakeEngine{answers: map[string]fakeAnswer{inspect: {err: fmt.Errorf("no such volume")}}}
	if err := ensureVolume(context.Background(), missing, "v", "gocache", "501"); err != nil {
		t.Fatal(err)
	}
	calls := missing.argvs()
	if len(calls) != 2 || calls[1] != create {
		t.Errorf("a missing volume is not created with its owner: %q", calls)
	}

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
					return fakeAnswer{err: fmt.Errorf("no such volume")}, true
				}
				return fakeAnswer{out: tc.second}, true
			case create:
				return fakeAnswer{err: fmt.Errorf("volume already exists")}, true
			}
			return fakeAnswer{}, false
		}}
		err := ensureVolume(context.Background(), race, "v", "gocache", "501")
		if (err == nil) != tc.ok {
			t.Errorf("lost the create race to %q: err %v, want ok=%t", tc.second, err, tc.ok)
		}
		if calls := race.argvs(); len(calls) != 3 || calls[2] != inspect {
			t.Errorf("a lost create does not look again: %q", calls)
		}
	}
}

func TestJudgeByDeadlineLabelPlusGrace(t *testing.T) {
	t.Parallel()
	now := time.Unix(1_800_001_000, 0)
	grace := 30 * time.Second
	dl := func(sec int64) string { return strconv.FormatInt(sec, 10) }
	cs := []listed{
		{ID: "a-overdue", State: "running", Labels: map[string]string{labelRun: "r1", labelDeadline: dl(now.Unix() - 31)}},
		{ID: "b-in-grace", State: "running", Labels: map[string]string{labelRun: "r2", labelDeadline: dl(now.Unix() - 30)}},
		{ID: "c-young", State: "created", Labels: map[string]string{labelRun: "r3", labelDeadline: dl(now.Unix() + 600)}},
		{ID: "d-no-deadline", State: "exited", Labels: map[string]string{labelRun: "r4"}},
		{ID: "e-garbage", State: "exited", Labels: map[string]string{labelRun: "r5", labelDeadline: "soon"}},
		{ID: "h-zero", State: "exited", Labels: map[string]string{labelRun: "r8", labelDeadline: "0"}},
		{ID: "i-negative", State: "exited", Labels: map[string]string{labelRun: "r9", labelDeadline: "-5"}},
		{ID: "f-unlabelled", Names: []string{"nova-functional-decoy"}, State: "exited", Labels: map[string]string{labelDeadline: dl(1)}},
		{ID: "g-created-never-started", State: "configured", Labels: map[string]string{labelRun: "r7", labelDeadline: dl(now.Unix() - 3600)}},
	}
	got := map[string]verdict{}
	for _, v := range judge(cs, now, grace) {
		got[v.id] = v
	}
	if _, ok := got["f-unlabelled"]; ok {
		t.Errorf("a container without the run label is judged at all, though its name looks like ours")
	}
	for id, want := range map[string]struct {
		remove bool
		reason string
	}{
		"a-overdue":               {true, ""},
		"b-in-grace":              {false, "within-deadline"},
		"c-young":                 {false, "within-deadline"},
		"d-no-deadline":           {false, "no-deadline-label"},
		"e-garbage":               {false, "unreadable-deadline"},
		"h-zero":                  {false, "unreadable-deadline"},
		"i-negative":              {false, "unreadable-deadline"},
		"g-created-never-started": {true, ""},
	} {
		v, ok := got[id]
		if !ok {
			t.Errorf("%s not judged", id)
			continue
		}
		if v.remove != want.remove || v.reason != want.reason {
			t.Errorf("%s: remove=%t reason=%q, want remove=%t reason=%q", id, v.remove, v.reason, want.remove, want.reason)
		}
	}
}

func TestReapRemovesOnlyOverdueLabelledContainers(t *testing.T) {
	t.Parallel()
	now := time.Unix(1_800_001_000, 0)
	listing := fmt.Sprintf(`[
 {"Id":"aaaaaaaaaaaaaaaa1","Names":["nova-functional-old"],"State":"running","Labels":{"%[1]s":"old","%[2]s":"%[3]d"}},
 {"Id":"bbbbbbbbbbbbbbbb2","Names":["nova-functional-young"],"State":"running","Labels":{"%[1]s":"young","%[2]s":"%[4]d"}},
 {"Id":"cccccccccccccccc3","Names":["nova-functional-decoy"],"State":"exited","Labels":{"other":"x"}}
]`, labelRun, labelDeadline, now.Unix()-3600, now.Unix()+3600)
	eng := &fakeEngine{answers: map[string]fakeAnswer{strings.Join(reapListArgs(), " "): {out: listing}}}
	var stderr bytes.Buffer
	n, err := reap(context.Background(), eng, now, 30*time.Second, false, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("reaped %d, want 1", n)
	}
	calls := eng.argvs()
	want := []string{strings.Join(reapListArgs(), " "), strings.Join(removeArgs("aaaaaaaaaaaaaaaa1"), " ")}
	if strings.Join(calls, "\n") != strings.Join(want, "\n") {
		t.Errorf("calls:\n%s\nwant:\n%s", strings.Join(calls, "\n"), strings.Join(want, "\n"))
	}
	for _, c := range calls {
		verb := strings.Fields(c)[0]
		if verb != "ps" && verb != "rm" || strings.Contains(c, "prune") {
			t.Errorf("the reaper does more than list and remove containers: %q", c)
		}
	}
	out := stderr.String()
	if !strings.Contains(out, "REAPED id=aaaaaaaaaaaa run=old state=running") || !strings.Contains(out, "REAP containers=3 reaped=1 left=1") {
		t.Errorf("reap lines:\n%s", out)
	}
}

func TestReapDryRunChangesNothing(t *testing.T) {
	t.Parallel()
	now := time.Unix(1_800_001_000, 0)
	listing := fmt.Sprintf(`[{"Id":"a1","State":"exited","Labels":{"%s":"old","%s":"1"}}]`, labelRun, labelDeadline)
	eng := &fakeEngine{answers: map[string]fakeAnswer{strings.Join(reapListArgs(), " "): {out: listing}}}
	var stderr bytes.Buffer
	n, err := reap(context.Background(), eng, now, 0, true, &stderr)
	if err != nil || n != 1 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	if calls := eng.argvs(); len(calls) != 1 {
		t.Errorf("a dry run ran more than the listing: %q", calls)
	}
	if !strings.Contains(stderr.String(), "REAP-WOULD id=a1 run=old") {
		t.Errorf("dry run lines:\n%s", stderr.String())
	}
}

func TestParseListed(t *testing.T) {
	t.Parallel()
	for _, empty := range []string{"", "  \n", "[]", "null"} {
		cs, err := parseListed(empty)
		if err != nil || len(cs) != 0 {
			t.Errorf("parseListed(%q) = %v, %v", empty, cs, err)
		}
	}
	if _, err := parseListed("CONTAINER ID  IMAGE"); err == nil {
		t.Errorf("a table listing is read as JSON")
	}
}

func TestParseRun(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctxDir := filepath.Join(dir, "img")
	if err := os.MkdirAll(ctxDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ctxDir, "Containerfile"), []byte("FROM x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := parseRun([]string{"--src", dir, "--context", ctxDir, "--deadline", "2m", "./internal/ntable/..."})
	if err != nil {
		t.Fatal(err)
	}
	if c.deadline != 2*time.Minute || c.gocache != cacheVolumeName("gocache", c.ownerID) || c.gomod != cacheVolumeName("gomod", c.ownerID) {
		t.Errorf("parsed %+v", c)
	}
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--src", dir, "--context", ctxDir}, "no package"},
		{[]string{"--src", dir, "--context", ctxDir, "./a", "--deadline", "1m"}, "flags come first"},
		{[]string{"--src", dir, "--context", ctxDir, "internal/ntable"}, "not a package directory"},
		{[]string{"--src", dir, "--context", ctxDir, "./a b"}, "not a package directory"},
		{[]string{"--src", dir, "--context", ctxDir, "--deadline", "29s", "./a"}, "under 30s"},
		{[]string{"--src", dir, "--context", ctxDir, "--deadline", "0s", "./a"}, "under 30s"},
		{[]string{"--src", dir, "--context", ctxDir, "--deadline", "-1m", "./a"}, "under 30s"},
		{[]string{"--src", dir, "--context", ctxDir, "--cpus", "0", "./a"}, "--cpus"},
		{[]string{"--src", dir, "--context", ctxDir, "--memory", "lots", "./a"}, "--memory"},
		{[]string{"--src", dir, "--context", dir, "./a"}, "no Containerfile"},
		{[]string{"--src", ctxDir, "./a"}, "no go.mod"},
		{[]string{"--src", dir, "--context", ctxDir, "--gocache-volume", "same", "--gomod-volume", "same", "./a"}, "two volumes"},
		{[]string{"--src", dir, "--context", ctxDir, "--gocache-volume", "../x", "./a"}, "not a podman volume name"},
		{[]string{"--src", dir, "--context", ctxDir, "--fresh-gocache", "--gocache-volume", "mine", "./a"}, "two different build caches"},
	} {
		_, err := parseRun(tc.args)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("parseRun(%q) = %v, want an error naming %q", tc.args, err, tc.want)
		}
	}
	// --image needs no context.
	if _, err := parseRun([]string{"--src", dir, "--image", "localhost/x:y", "./a"}); err != nil {
		t.Errorf("--image without a context: %v", err)
	}
}

func TestContextHashFollowsEveryFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("Containerfile", "FROM a\n")
	h1, err := contextHash(dir)
	if err != nil {
		t.Fatal(err)
	}
	if h2, _ := contextHash(dir); h2 != h1 {
		t.Errorf("the hash is not stable: %s %s", h1, h2)
	}
	write("sub/extra.txt", "x")
	h3, _ := contextHash(dir)
	if h3 == h1 {
		t.Errorf("a new file in the context does not change the hash")
	}
	write("Containerfile", "FROM b\n")
	if h4, _ := contextHash(dir); h4 == h3 {
		t.Errorf("an edit of the Containerfile does not change the hash")
	}
	if tag := imageTag(h1); !strings.HasPrefix(tag, "localhost/nova-functional:ctx-") || len(tag) != len("localhost/nova-functional:ctx-")+16 {
		t.Errorf("imageTag = %q", tag)
	}
}

func TestRunContainerPassesTheExitCodeThrough(t *testing.T) {
	t.Parallel()
	for _, code := range []int{0, 1, 2, 124} {
		eng := &fakeEngine{startCode: code}
		got, ended := runContainer(context.Background(), eng, realClock{}, []string{"run", "x"}, "nova-functional-r", time.Now().Add(time.Minute), io.Discard, io.Discard)
		if got != code || ended != "finished" {
			t.Errorf("exit %d came back as %d (%s)", code, got, ended)
		}
		// The container is removed after the client returns, whatever the code.
		calls := eng.argvs()
		if last := calls[len(calls)-1]; last != strings.Join(removeArgs("nova-functional-r"), " ") {
			t.Errorf("no removal after the run: %q", calls)
		}
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
	h.fakeEngine.mu.Lock()
	h.fakeEngine.calls = append(h.fakeEngine.calls, args)
	h.fakeEngine.mu.Unlock()
	return hangingProcess{killed: h.killed}, nil
}

// The fake's removal ends the hanging client, as the runtime's does.
func (h *hangingEngine) Output(ctx context.Context, args ...string) (string, error) {
	out, err := h.fakeEngine.Output(ctx, args...)
	if len(args) > 0 && args[0] == "rm" {
		hangingProcess{killed: h.killed}.Kill()
	}
	return out, err
}

func TestRunContainerRemovesAtTheClientDeadline(t *testing.T) {
	t.Parallel()
	eng := &hangingEngine{killed: make(chan struct{})}
	_, ended := runContainer(context.Background(), eng, realClock{}, []string{"run", "x"}, "nova-functional-r", time.Now().Add(50*time.Millisecond), io.Discard, io.Discard)
	if ended != "deadline" {
		t.Errorf("ended %q, want deadline", ended)
	}
	if calls := eng.argvs(); calls[len(calls)-1] != strings.Join(removeArgs("nova-functional-r"), " ") {
		t.Errorf("the container is not removed at the deadline: %q", calls)
	}
}

func TestRunContainerRemovesOnInterrupt(t *testing.T) {
	t.Parallel()
	eng := &hangingEngine{killed: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, ended := runContainer(ctx, eng, realClock{}, []string{"run", "x"}, "nova-functional-r", time.Now().Add(time.Hour), io.Discard, io.Discard)
	if ended != "interrupted" {
		t.Errorf("ended %q, want interrupted", ended)
	}
	if calls := eng.argvs(); calls[len(calls)-1] != strings.Join(removeArgs("nova-functional-r"), " ") {
		t.Errorf("the container is not removed on an interrupt: %q", calls)
	}
}

func TestDispatchRefusals(t *testing.T) {
	t.Parallel()
	var out, errb bytes.Buffer
	if code := dispatch(context.Background(), nil, &out, &errb); code != exitUsage {
		t.Errorf("no verb: exit %d", code)
	}
	if code := dispatch(context.Background(), []string{"frob"}, &out, &errb); code != exitUsage || !strings.Contains(errb.String(), `unknown verb "frob"`) {
		t.Errorf("unknown verb: exit %d, %q", code, errb.String())
	}
	errb.Reset()
	if code := dispatch(context.Background(), []string{"run"}, &out, &errb); code != exitCannotRun {
		t.Errorf("run without packages: exit %d", code)
	}
	if code := dispatch(context.Background(), []string{"reap", "extra"}, &out, &errb); code != 2 {
		t.Errorf("reap with an argument: exit %d", code)
	}
	out.Reset()
	if code := dispatch(context.Background(), []string{"help"}, &out, &errb); code != 0 || !strings.Contains(out.String(), "functionalrun reap") {
		t.Errorf("help: exit %d", code)
	}
}

func tierFixture(t *testing.T, owner string, startCode int) (*fakeEngine, runConfig) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
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
		got := runTier(context.Background(), eng, c, &fakeClock{now: time.Unix(1_800_000_000, 0)}, &stdout, &stderr)
		calls := eng.argvs()
		if code != 0 {
			if got != exitCannotRun || !strings.Contains(stderr.String(), "module cache step ended finished with exit 2") {
				t.Errorf("a failed module step: exit %d\n%s", got, stderr.String())
			}
			continue
		}
		if got != 0 {
			t.Errorf("a green run exits %d\n%s", got, stderr.String())
		}
		var runs []string
		for _, call := range calls {
			if strings.HasPrefix(call, "run ") {
				runs = append(runs, call)
			}
		}
		if len(runs) != 2 || !strings.Contains(runs[0], "go mod download") || !strings.Contains(runs[1], "make test-functional") {
			t.Fatalf("want the module step then the test container:\n%s", strings.Join(runs, "\n"))
		}
		if !strings.Contains(runs[1], " sha256:img ") {
			t.Errorf("the test container does not run the inspected image id: %q", runs[1])
		}
		if calls[0] != strings.Join(reapListArgs(), " ") {
			t.Errorf("the reaper does not run first: %q", calls[0])
		}
		last := calls[len(calls)-1]
		if !strings.HasPrefix(last, "ps --all --filter label=nova.functional.run=") {
			t.Errorf("the last call is not the leftover check by label: %q", last)
		}
		if !strings.Contains(stderr.String(), "ended=finished exit=0 ") || !strings.Contains(stderr.String(), "containers_left=0") {
			t.Errorf("receipt line:\n%s", stderr.String())
		}
	}
}

func TestRunTierRefusesAnotherUsersCache(t *testing.T) {
	t.Parallel()
	eng, c := tierFixture(t, "502", 0)
	var stdout, stderr bytes.Buffer
	if got := runTier(context.Background(), eng, c, &fakeClock{now: time.Unix(1_800_000_000, 0)}, &stdout, &stderr); got != exitCannotRun {
		t.Errorf("exit %d, want %d", got, exitCannotRun)
	}
	for _, call := range eng.argvs() {
		if strings.HasPrefix(call, "run ") {
			t.Errorf("a container ran over another user's cache: %q", call)
		}
	}
}

func TestSetupExit(t *testing.T) {
	t.Parallel()
	if got := setupExit(context.Background()); got != exitCannotRun {
		t.Errorf("setupExit = %d", got)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got := setupExit(ctx); got != exitInterrupted {
		t.Errorf("setupExit after an interrupt = %d", got)
	}
}

// fakeClock holds time still: After records the bound asked for and never
// fires; Sleep advances the clock.
type fakeClock struct {
	mu     sync.Mutex
	now    time.Time
	afters []time.Duration
}

func (f *fakeClock) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

func (f *fakeClock) After(d time.Duration) <-chan time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.afters = append(f.afters, d)
	return nil
}

func (f *fakeClock) Sleep(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = f.now.Add(d)
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
		if ended != tc.wantEnded || exit != tc.wantExit {
			t.Errorf("classify(%d, %s, %s) = %s %d, want %s %d", tc.code, tc.ended, tc.elapsed, ended, exit, tc.wantEnded, tc.wantExit)
		}
	}
}

func TestRunTierClientDeadlines(t *testing.T) {
	t.Parallel()
	eng, c := tierFixture(t, "501", 0)
	clk := &fakeClock{now: time.Unix(1_800_000_000, 0)}
	var stdout, stderr bytes.Buffer
	if got := runTier(context.Background(), eng, c, clk, &stdout, &stderr); got != 0 {
		t.Fatalf("exit %d\n%s", got, stderr.String())
	}
	want := []time.Duration{prefillDeadline + clientGrace, c.deadline + clientGrace}
	if fmt.Sprint(clk.afters) != fmt.Sprint(want) {
		t.Errorf("client deadlines %v, want the module step's then the run's, each the bound plus the grace: %v", clk.afters, want)
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
		got := runTier(context.Background(), eng, c, &fakeClock{now: time.Unix(1_800_000_000, 0)}, &stdout, &stderr)
		if got != tc.wantExit || !strings.Contains(stderr.String(), "ended="+tc.wantEnded+" exit="+strconv.Itoa(tc.wantExit)+" ") {
			t.Errorf("test container exit %d: tool exit %d, want %d ended=%s\n%s", tc.testCode, got, tc.wantExit, tc.wantEnded, stderr.String())
		}
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
		eng, c := tierFixture(t, "501", 0)
		eng.respond = func(args []string) (fakeAnswer, bool) {
			if len(args) > 3 && args[0] == "ps" && strings.HasPrefix(args[3], "label="+labelRun+"=") && !strings.HasSuffix(args[3], "-mod") {
				return tc.answer, true
			}
			return fakeAnswer{}, false
		}
		var stdout, stderr bytes.Buffer
		got := runTier(context.Background(), eng, c, &fakeClock{now: time.Unix(1_800_000_000, 0)}, &stdout, &stderr)
		if got != exitCannotRun || !strings.Contains(stderr.String(), tc.want) {
			t.Errorf("a green run with a leftover (%v): exit %d, want %d and %s\n%s", tc.answer, got, exitCannotRun, tc.want, stderr.String())
		}
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
		eng := &hangingEngine{killed: make(chan struct{})}
		ctx, cancel := context.WithCancel(context.Background())
		deadline := time.Now().Add(time.Hour)
		if interrupt {
			cancel()
		} else {
			deadline = time.Now().Add(20 * time.Millisecond)
		}
		w := orderWriter{t: t, eng: eng}
		runContainer(ctx, eng, realClock{}, []string{"run", "x"}, "nova-functional-r", deadline, w, w)
		cancel()
	}
}

func TestFreshGocacheIsAnAnonymousVolume(t *testing.T) {
	t.Parallel()
	c := testConfig()
	c.freshGocache = true
	vols := flagValues(testArgs(c, "sha256:abc", "run1", time.Unix(1_800_000_000, 0)), "-v")
	want := []string{"/work/src:/src:ro", "nova-functional-gomod-uid501:/gomodcache:ro", "/gocache"}
	if strings.Join(vols, ",") != strings.Join(want, ",") {
		t.Errorf("mounts %q, want %q", vols, want)
	}
	eng, tc := tierFixture(t, "501", 0)
	tc.freshGocache = true
	var stdout, stderr bytes.Buffer
	if got := runTier(context.Background(), eng, tc, &fakeClock{now: time.Unix(1_800_000_000, 0)}, &stdout, &stderr); got != 0 {
		t.Fatalf("exit %d\n%s", got, stderr.String())
	}
	for _, call := range eng.argvs() {
		if strings.Contains(call, tc.gocache) {
			t.Errorf("a fresh-cache run touched the shared build cache: %q", call)
		}
	}
}

func TestPackageArgumentsAreAllowlisted(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, ok := range []string{".", "./...", "./internal/ntable", "./internal/ntable/", "./internal/ntable/...", "./cmd/nova-ci", "./a_b.c-d/e"} {
		if _, err := parseRun([]string{"--src", dir, "--image", "x", ok}); err != nil {
			t.Errorf("package %q refused: %v", ok, err)
		}
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
		if code != exitCannotRun || !strings.Contains(stderr.String(), "not a package directory") || strings.Contains(stderr.String(), "no-such-podman") {
			t.Errorf("package %q: exit %d, %q", bad, code, stderr.String())
		}
	}
}
