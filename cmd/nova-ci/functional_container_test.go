package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/ci/functionalrun"
)

// recordingEngine is a stand-in for the container runtime's command line: it
// records every argv, one line a call, and answers the few questions a run asks
// (the owner of a cache volume, an image id). Nothing starts a container.
type recordingEngine struct {
	kind string
	mu   sync.Mutex
	log  []string
}

func (e *recordingEngine) Kind() string { return e.kind }

func (e *recordingEngine) Output(_ context.Context, args ...string) (string, error) {
	e.record(args)
	switch {
	case len(args) > 1 && args[0] == "volume" && args[1] == "inspect":
		kind := "gocache"
		if strings.Contains(strings.Join(args, " "), "gomod") {
			kind = "gomod"
		}
		return strconv.Itoa(os.Getuid()) + "|" + kind + "\n", nil
	case len(args) > 1 && args[0] == "image":
		return "sha256:fake\n", nil
	}
	return "", nil
}

func (e *recordingEngine) Start(args []string, _, _ io.Writer) (functionalrun.Process, error) {
	e.record(args)
	return doneProcess{}, nil
}

func (e *recordingEngine) record(args []string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.log = append(e.log, strings.Join(args, " "))
}

func (e *recordingEngine) calls() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.log...)
}

// doneProcess is a runtime command that has exited 0.
type doneProcess struct{}

func (doneProcess) Wait() (int, error) { return 0, nil }
func (doneProcess) Kill() error        { return nil }

// functionalTree is a module with one package holding a functional-tagged test,
// one holding none, and the image's context: the tree the run is made in.
func functionalTree(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range map[string]string{
		"go.mod":                               "module example.com/x\n\ngo 1.26\n",
		"infra/functional-image/Containerfile": "FROM scratch\n",
		"pkg/a/a.go":                           "package a\n",
		"pkg/a/a_test.go":                      "//go:build functional\n\npackage a\n\nimport \"testing\"\n\nfunc TestA(t *testing.T) {}\n",
		"pkg/plain/plain.go":                   "package plain\n",
		"pkg/plain/plain_test.go":              "package plain\n\nimport \"testing\"\n\nfunc TestPlain(t *testing.T) {}\n",
	} {
		p := filepath.Join(dir, filepath.FromSlash(name))
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(body), 0o644))
	}
	return dir
}

// machine is a tree, the runtimes on its PATH and the engines they opened.
type machine struct {
	root string
	have []string
	mu   sync.Mutex
	made []*recordingEngine
}

func newMachine(t *testing.T, have ...string) *machine {
	t.Helper()
	return &machine{root: functionalTree(t), have: have}
}

func (m *machine) env(t *testing.T) functionalEnv {
	t.Helper()
	return functionalEnv{
		root: m.root,
		ctx:  t.Context(),
		look: func(name string) (string, error) {
			for _, h := range m.have {
				if h == name {
					return "/fake/bin/" + name, nil
				}
			}
			return "", errors.New("not found")
		},
		open: func(_, kind string) functionalrun.Engine {
			m.mu.Lock()
			defer m.mu.Unlock()
			e := &recordingEngine{kind: kind}
			m.made = append(m.made, e)
			return e
		},
	}
}

// run is the verb on this machine: its exit code, stdout and stderr.
func (m *machine) run(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := functionalIn(m.env(t), append([]string{"--in-container"}, args...), &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func (m *machine) calls() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []string
	for _, e := range m.made {
		out = append(out, e.calls()...)
	}
	return out
}

func firstWith(calls []string, prefix, contains string) string {
	for _, c := range calls {
		if strings.HasPrefix(c, prefix) && strings.Contains(c, contains) {
			return c
		}
	}
	return ""
}

// docs/SPEC-CI.md, "functional-container": `nova-ci functional --in-container`
// runs exactly the selection in ONE container, on podman and on docker, with
// every bound and isolation flag in the argv; the forced removal and the
// leftover check by label end the run. The engine is a recorder, so the argv
// asserted is the one the verb really asked the runtime for.
func TestFunctionalInContainerHoldsTheRunInsideItsBounds(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"podman", "docker"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			m := newMachine(t, kind)
			code, stdout, stderr := m.run(t, "--deadline", "90s", "--memory=2g", "--pids", "256", "--cpus", "2", "--fresh-gocache", "./pkg/a", "./pkg/plain")
			require.Equal(t, 0, code, "stdout %q stderr %q", stdout, stderr)
			assert.Empty(t, stdout, "stdout is the tests' own")
			assert.Contains(t, stderr, "1 package(s) in one container: ./pkg/a", "exactly the selection")
			assert.Contains(t, stderr, "runtime="+kind+" ended=finished exit=0 ")
			assert.Contains(t, stderr, "containers_left=0")

			calls := m.calls()
			require.NotEmpty(t, calls)
			assert.True(t, strings.HasPrefix(calls[0], "ps --all --filter label=nova.functional.run "), "the reaper runs first: %q", calls[0])
			test := firstWith(calls, "run ", "make test-functional")
			require.NotEmpty(t, test, "one container runs the selection: %q", calls)
			assert.Equal(t, 1, strings.Count(strings.Join(calls, "\n"), "make test-functional"), "ONE container")
			assert.Contains(t, test, "PKGS=./pkg/a ", "exactly the selected package")
			assert.NotContains(t, test, "./pkg/plain")
			for _, want := range []string{
				" --rm ", " --init ", " --network none ", " --ipc private ", " --cap-drop all ",
				" --security-opt no-new-privileges ", " --memory 2g ", " --memory-swap 2g ",
				" --pids-limit 256 ", " --cpus 2 ", " --read-only ", " -v /gocache ",
				" --label nova.functional.deadline=", " timeout -k 5 80 make test-functional ",
			} {
				assert.Contains(t, test+" ", want, "%s argv lacks %q: %s", kind, want, test)
			}
			if kind == "podman" {
				assert.Contains(t, test, " --timeout 90 ")
				assert.Contains(t, test, " --pid private ")
			} else {
				assert.NotContains(t, test, "--timeout", "docker has no run --timeout")
				assert.NotContains(t, test+" ", " --pid ", "docker's private PID namespace is its default")
			}
			removal := firstWith(calls, "rm --force", "nova-functional-")
			assert.Contains(t, removal, " --volumes ", "the forced removal takes the volumes: %q", calls)
			assert.True(t, strings.HasPrefix(calls[len(calls)-1], "ps --all --filter label=nova.functional.run=2"), "the leftover check ends the run: %q", calls[len(calls)-1])
		})
	}
}

// auto takes podman first and docker second; a named runtime is that one.
func TestFunctionalInContainerChoosesTheRuntime(t *testing.T) {
	t.Parallel()
	m := newMachine(t, "podman", "docker")
	code, _, stderr := m.run(t, "./pkg/a")
	require.Equal(t, 0, code, stderr)
	assert.Contains(t, stderr, "container runtime: podman")
	code, _, stderr = m.run(t, "--runtime", "docker", "./pkg/a")
	require.Equal(t, 0, code, stderr)
	assert.Contains(t, stderr, "container runtime: docker")
}

// No runtime is exit 125 and one CI FUNCTIONAL REFUSED line, and the tests
// never run bare: no engine is even opened, and a runtime asked for by name is
// never swapped for the other.
func TestFunctionalInContainerWithNoRuntimeNeverRunsBare(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		have []string
		args []string
	}{
		{nil, nil},
		{[]string{"podman"}, []string{"--runtime", "docker"}},
		{[]string{"docker"}, []string{"--runtime", "podman"}},
	} {
		m := newMachine(t, tc.have...)
		code, stdout, stderr := m.run(t, append(tc.args, "./pkg/a")...)
		assert.Equal(t, 125, code, "%v: stderr %q", tc, stderr)
		assert.Empty(t, stdout)
		assert.Contains(t, stderr, "CI FUNCTIONAL REFUSED reason=no_container_runtime")
		assert.Empty(t, m.made, "%v: no engine was opened", tc)
		assert.NotContains(t, stderr, "FUNCTIONAL RUN ")
	}
}

// A selection of no functional tests starts nothing and needs no runtime.
func TestFunctionalInContainerRunsNothingWhenNothingIsSelected(t *testing.T) {
	t.Parallel()
	m := newMachine(t)
	code, stdout, stderr := m.run(t, "./pkg/plain")
	assert.Equal(t, 0, code, stderr)
	assert.Equal(t, "CI FUNCTIONAL OK packages=0 reason=no-functional-tag-in-1-dirs\n", stdout)
	assert.Empty(t, m.made)
}

// A container flag that is wrong, or that means nothing without --in-container,
// is one refusal at exit 2 before any container.
func TestFunctionalInContainerRefusesBadFlags(t *testing.T) {
	t.Parallel()
	m := newMachine(t, "podman")
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--in-container", "--runtime", "lxc", "./pkg/a"}, `--runtime "lxc"`},
		{[]string{"--in-container", "--deadline", "5s", "./pkg/a"}, "--deadline 5s is under"},
		{[]string{"--in-container", "--cpus", "0", "./pkg/a"}, "--cpus 0"},
		{[]string{"--in-container", "--memory", "lots", "./pkg/a"}, `--memory "lots"`},
		{[]string{"--in-container", "--deadline"}, "--deadline wants a value"},
		{[]string{"--cpus", "2", "./pkg/a"}, "mean nothing without --in-container"},
		{[]string{"--in-container", "--bogus", "./pkg/a"}, `unknown flag "--bogus"`},
		{[]string{"--in-container", "./nope"}, "matches no package"},
	} {
		var stdout, stderr bytes.Buffer
		code := functionalIn(m.env(t), tc.args, &stdout, &stderr)
		assert.Equal(t, 2, code, "%v: stderr %q", tc.args, stderr.String())
		assert.Empty(t, stdout.String(), "%v", tc.args)
		assert.Equal(t, 1, strings.Count(stderr.String(), "\n"), "%v: one line: %q", tc.args, stderr.String())
		assert.Contains(t, stderr.String(), tc.want, "%v", tc.args)
		assert.True(t, strings.HasPrefix(stderr.String(), "nova-ci functional REFUSED: "), "%q", stderr.String())
	}
	assert.Empty(t, m.made, "a refusal starts nothing")
}
