package functionalrun

import (
	"bytes"
	"context"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// kindConfig is testConfig for one runtime.
func kindConfig(kind string) runConfig {
	c := testConfig()
	c.kind = kind
	return c
}

// beforeImage is the argv's flags: everything before the image.
func beforeImage(t *testing.T, args []string, image string) []string {
	t.Helper()
	i := indexOf(args, image)
	require.GreaterOrEqual(t, i, 0, "no image in %q", args)
	return args[:i]
}

// docs/SPEC-CI.md, "functional-container": every isolation flag and every bound
// is explicit in the argv of both engines, never a default.
func TestTestArgsCarryEveryBoundAndIsolationFlagOnBothEngines(t *testing.T) {
	t.Parallel()
	start := time.Unix(1_800_000_000, 0)
	for _, kind := range []string{kindPodman, kindDocker} {
		args := testArgs(kindConfig(kind), "sha256:abc", "run1", start)
		flags := beforeImage(t, args, "sha256:abc")
		for f, want := range map[string]string{
			"--network": "none", "--ipc": "private", "--pids-limit": "1024",
			"--memory": "4g", "--memory-swap": "4g", "--cpus": "4",
			"--cap-drop": "all", "--security-opt": "no-new-privileges",
		} {
			assert.Equal(t, want, flagValue(t, flags, f), "%s: %s", kind, f)
		}
		for _, f := range []string{"--rm", "--init", "--read-only"} {
			assert.Contains(t, flags, f, "%s: %s", kind, f)
		}
		// The deadline is carried as the label the reaper reads, on both.
		assert.Contains(t, flagValues(flags, "--label"), "nova.functional.deadline=1800000600", kind)
		// The bound in the container too: the in-container timeout under --init.
		cmd := strings.Join(args[len(flags)+1:], " ")
		assert.True(t, strings.HasPrefix(cmd, "timeout -k 5 590 make test-functional "), "%s: command %q", kind, cmd)
		// No host namespace, no host network, no privilege, on either.
		for _, bad := range []string{"host", "--privileged", "--network=host", "--pid=host", "--ipc=host"} {
			assert.NotContains(t, flags, bad, "%s", kind)
		}
	}
}

// podman: the runtime's own --timeout and an explicit private PID namespace.
// docker: no `run --timeout` and no --pid at all (its private namespace is the
// default and it refuses the word private), so its bound is held inside, by the
// client, and by the reaper.
func TestTestArgsDifferWhereDockerHasNoEquivalent(t *testing.T) {
	t.Parallel()
	start := time.Unix(1_800_000_000, 0)
	p := beforeImage(t, testArgs(kindConfig(kindPodman), "sha256:abc", "run1", start), "sha256:abc")
	assert.Equal(t, "600", flagValue(t, p, "--timeout"))
	assert.Equal(t, "private", flagValue(t, p, "--pid"))

	d := beforeImage(t, testArgs(kindConfig(kindDocker), "sha256:abc", "run1", start), "sha256:abc")
	assert.NotContains(t, d, "--timeout", "docker has no run --timeout")
	assert.NotContains(t, d, "--pid", "docker refuses --pid private; its default is private")

	// An empty kind is podman: the tests above and the Makefile's callers.
	assert.Equal(t, p, beforeImage(t, testArgs(testConfig(), "sha256:abc", "run1", start), "sha256:abc"))
}

// The networked module step keeps every bound but the network, on both engines;
// docker's bound is the timeout inside.
func TestPrefillKeepsEveryBoundOnBothEngines(t *testing.T) {
	t.Parallel()
	start := time.Unix(1_800_000_000, 0)
	for _, kind := range []string{kindPodman, kindDocker} {
		args := prefillArgs(kindConfig(kind), "sha256:abc", "run1", "stamp", "http://127.0.0.1:3128", start)
		flags := beforeImage(t, args, "sha256:abc")
		assert.NotContains(t, flags, "--network", "%s: the step has the default network", kind)
		for f, want := range map[string]string{"--ipc": "private", "--pids-limit": "1024", "--memory": "4g", "--memory-swap": "4g", "--cap-drop": "all"} {
			assert.Equal(t, want, flagValue(t, flags, f), "%s: %s", kind, f)
		}
		cmd := strings.Join(args[len(flags)+1:], " ")
		if kind == kindDocker {
			assert.NotContains(t, flags, "--timeout")
			assert.True(t, strings.HasPrefix(cmd, "timeout -k 5 290 sh -c "), "docker: %q", cmd)
		} else {
			assert.Equal(t, "300", flagValue(t, flags, "--timeout"))
			assert.True(t, strings.HasPrefix(cmd, "sh -c "), "podman: %q", cmd)
		}
	}
}

// The forced removal at the end is `rm --force --volumes` on both; docker has
// neither --ignore nor --time, and a container already gone is not an error.
func TestRemovalIsForcedWithVolumesOnBothEngines(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{kindPodman, kindDocker} {
		got := removeArgsFor(kind, "x")
		assert.Equal(t, []string{"rm", "--force"}, got[:2], kind)
		assert.Contains(t, got, "--volumes", kind)
		assert.Equal(t, "x", got[len(got)-1], kind)
	}
	assert.NotContains(t, removeArgsFor(kindDocker, "x"), "--ignore")
	assert.NotContains(t, removeArgsFor(kindDocker, "x"), "--time")
	assert.True(t, isGone(errors.New("docker rm: Error response from daemon: No such container: x")))
	assert.False(t, isGone(errors.New("docker rm: permission denied")))
	assert.False(t, isGone(nil))
}

func TestDockerListingIsParsedIntoTheSameShape(t *testing.T) {
	t.Parallel()
	out := "abc123\trunning\t20261006t010203-0a1b2c3d\t1800000000\t1800000600\t501\n" +
		"def456\texited\t\t\t\t\n"
	cs, err := parseDockerListed(out)
	require.NoError(t, err)
	require.Len(t, cs, 2)
	assert.Equal(t, listed{ID: "abc123", State: "running", Labels: map[string]string{
		labelRun: "20261006t010203-0a1b2c3d", labelStart: "1800000000", labelDeadline: "1800000600", labelOwner: "501"}}, cs[0])
	assert.Empty(t, cs[1].Labels, "a container without our labels carries none")
	_, err = parseDockerListed("a\tb\n")
	assert.Error(t, err)
	none, err := parseDockerListed("\n")
	require.NoError(t, err)
	assert.Empty(t, none)
	// The listing asks for exactly those labels, by label presence and never by name.
	args := reapListArgsFor(kindDocker)
	assert.Equal(t, "label="+labelRun, flagValue(t, args, "--filter"))
	assert.Equal(t, dockerListFormat, flagValue(t, args, "--format"))
	assert.Equal(t, reapListArgs(), reapListArgsFor(kindPodman))
}

// The reaper on docker removes only an overdue container of ours, with docker's rm.
func TestReapOnDockerRemovesOnlyOverdueContainersOfOurs(t *testing.T) {
	t.Parallel()
	now := time.Unix(1_800_001_000, 0)
	listing := "overdue1\trunning\t20261006t010203-0a1b2c3d\t1800000000\t1800000600\t501\n" +
		"young001\trunning\t20261006t010204-0a1b2c3d\t1800000900\t1800001500\t501\n" +
		"foreign1\trunning\t20261006t010205-0a1b2c3d\t1800000000\t1800000600\t77\n"
	eng := &fakeEngine{kind: kindDocker, answers: map[string]fakeAnswer{strings.Join(reapListArgsFor(kindDocker), " "): {out: listing}}}
	var stderr bytes.Buffer
	n, unreadable, err := reap(context.Background(), eng, now, 30*time.Second, "501", false, &stderr)
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	assert.Equal(t, 0, unreadable)
	calls := eng.argvs()
	assert.Equal(t, "rm --force --volumes overdue1", calls[len(calls)-1])
	assert.Len(t, calls, 2, "one listing and one removal: %q", calls)
	// A container the runtime already removed is not a failure of the reaper.
	gone := &fakeEngine{kind: kindDocker, answers: map[string]fakeAnswer{
		strings.Join(reapListArgsFor(kindDocker), " "): {out: listing},
		"rm --force --volumes overdue1":                {err: errors.New("docker rm: Error: No such container: overdue1")},
	}}
	stderr.Reset()
	n, _, err = reap(context.Background(), gone, now, 30*time.Second, "501", false, &stderr)
	require.NoError(t, err)
	assert.Equal(t, 1, n, "%s", stderr.String())
}

// A whole run on docker: the module step, the test container, the forced
// removal and the leftover check by label, with no podman-only flag anywhere.
func TestRunTierOnDockerHoldsTheRunInsideItsBounds(t *testing.T) {
	t.Parallel()
	_, c := tierFixture(t, "501", 0)
	c.kind = kindDocker
	eng := &fakeEngine{kind: kindDocker, startCode: 0, answers: map[string]fakeAnswer{
		strings.Join(reapListArgsFor(kindDocker), " "):                   {out: ""},
		"image inspect --format {{.Id}} localhost/nova-functional:given": {out: "sha256:img\n"},
		strings.Join(volumeInspectArgs(c.gocache), " "):                  {out: "501|gocache\n"},
		strings.Join(volumeInspectArgs(c.gomod), " "):                    {out: "501|gomod\n"},
	}}
	var stdout, stderr bytes.Buffer
	require.Equal(t, 0, runTierIn(t, eng, c, &stdout, &stderr), "%s", stderr.String())
	calls := eng.argvs()
	assert.Contains(t, stderr.String(), "runtime=docker")
	var runs, removals int
	for _, call := range calls {
		switch {
		case strings.HasPrefix(call, "run "):
			runs++
			assert.NotContains(t, call, "--timeout", "docker has none: %q", call)
			assert.NotContains(t, call, " --pid ", "%q", call)
		case strings.HasPrefix(call, "rm "):
			removals++
			assert.NotContains(t, call, "--ignore", "%q", call)
		}
	}
	assert.Equal(t, 2, runs, "the module step and the test container: %q", calls)
	assert.GreaterOrEqual(t, removals, 2, "each container is force-removed at the end: %q", calls)
	assert.True(t, strings.HasPrefix(calls[len(calls)-1], "ps --all --filter label=nova.functional.run="), "%q", calls[len(calls)-1])
}

func TestChooseRuntimeHonoursTheRequestedRuntime(t *testing.T) {
	t.Parallel()
	only := func(names ...string) func(string) (string, error) {
		return func(n string) (string, error) {
			if slices.Contains(names, n) {
				return "/bin/" + n, nil
			}
			return "", errors.New("not found")
		}
	}
	bin, name, err := chooseRuntime("", "docker", only("podman", "docker"))
	require.NoError(t, err)
	assert.Equal(t, "/bin/docker", bin)
	assert.Equal(t, kindDocker, name)
	_, _, err = chooseRuntime("", "docker", only("podman"))
	assert.ErrorIs(t, err, errNoRuntime, "a requested runtime that is absent is a refusal, never the other one")
	_, _, err = chooseRuntime("", "podman", only("docker"))
	assert.ErrorIs(t, err, errNoRuntime)
	_, _, err = chooseRuntime("", "rkt", only("podman"))
	assert.Error(t, err)
	assert.NotErrorIs(t, err, errNoRuntime)
	assert.Equal(t, kindDocker, kindOf("/usr/local/bin/docker"))
	assert.Equal(t, kindPodman, kindOf("/opt/x/podman"))
}

// No runtime is exit 125 and one CI FUNCTIONAL REFUSED line, and the tests never
// run bare: the plan stops before any engine exists.
func TestNoRuntimeIsARefusalAndNeverABareRun(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	c := testConfig()
	c.src = dir
	c.image = "x"
	for _, want := range []string{"auto", kindPodman, kindDocker} {
		c.runtime = want
		var stdout, stderr bytes.Buffer
		code := (&Plan{cfg: c}).execute(context.Background(), func(string) (string, error) { return "", errors.New("not found") }, nil, &stdout, &stderr)
		assert.Equal(t, exitCannotRun, code, want)
		assert.Contains(t, stderr.String(), "CI FUNCTIONAL REFUSED reason=no_container_runtime requested="+want+"\n")
		assert.Empty(t, stdout.String(), "nothing ran: %s", want)
		assert.NotContains(t, stderr.String(), "FUNCTIONAL RUN")
	}
}

func TestRuntimeFlagIsValidated(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, osWriteFile(dir+"/go.mod", "module x\n"))
	_, err := parseRun([]string{"--src", dir, "--image", "x", "--runtime", "lxc", "./a"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--runtime")
	for _, r := range []string{"auto", "podman", "docker"} {
		c, err := parseRun([]string{"--src", dir, "--image", "x", "--runtime", r, "./a"})
		require.NoError(t, err)
		assert.Equal(t, r, c.runtime)
	}
	c, err := parseRun([]string{"--src", dir, "--image", "x", "./a"})
	require.NoError(t, err)
	assert.Equal(t, "auto", c.runtime, "auto is the default")
	_, err = parseReap([]string{"--runtime", "lxc"})
	assert.Error(t, err)
}

func osWriteFile(path, body string) error { return os.WriteFile(path, []byte(body), 0o644) }
