package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// THE RED TESTS for `nova-swarm worker check`. They run the built verb through run(), the
// same door a caller meets, so a description is judged by the whole path and no check is
// tested in isolation from the loader it must still use.

// workerCheckFixture writes a description that passes every check, with the test binary
// itself as the harness: it exists, it is a regular file, and this machine runs it.
func workerCheckFixture(t *testing.T, edit func(map[string]any)) string {
	t.Helper()
	dir := t.TempDir()
	exe, err := os.Executable()
	require.NoError(t, err)
	workerDir := filepath.Join(dir, "home")
	require.NoError(t, os.MkdirAll(workerDir, 0o755))
	keyFile := filepath.Join(dir, "key")
	require.NoError(t, os.WriteFile(keyFile, []byte("FAKE_KEY=0123\n"), 0o600))
	desc := map[string]any{
		"name": "check-1", "provider": "fake", "model": "fake-model",
		"env_var": "FAKE_KEY", "key_file": keyFile, "usage": "opencode",
		"harness": exe, "worker_dir": workerDir, "deadline": "20m",
		"harness_args": []string{"run", "--model", "{model}", "--", "{prompt}"},
	}
	if edit != nil {
		edit(desc)
	}
	raw, err := json.MarshalIndent(desc, "", "  ")
	require.NoError(t, err)
	path := filepath.Join(dir, "worker.json")
	require.NoError(t, os.WriteFile(path, raw, 0o644))
	return path
}

func runWorkerCheck(args ...string) (int, string, string) {
	var out, errb bytes.Buffer
	code := run(append([]string{"worker", "check"}, args...), strings.NewReader(""), &out, &errb, time.Now().UTC())
	return code, out.String(), errb.String()
}

// A description that names an executable harness, both placeholders, an existing worker
// directory and a real deadline passes on one line.
func TestWorkerCheckAGoodDescriptionPasses(t *testing.T) {
	t.Parallel()

	path := workerCheckFixture(t, nil)
	code, out, errb := runWorkerCheck(path)
	require.Equal(t, 0, code, "exit %d, want 0\nstdout: %s\nstderr: %s", code, out, errb)
	want := "WORKER OK check-1 model=fake-model provider=fake class=-"
	got := strings.TrimSpace(out)
	assert.Equal(t, want, got, "got %q, want %q", got, want)
}

// A harness that is not there is one drift naming the harness field, exit 1.
func TestWorkerCheckAMissingHarnessNamesTheField(t *testing.T) {
	t.Parallel()

	path := workerCheckFixture(t, func(d map[string]any) {
		d["harness"] = filepath.Join(t.TempDir(), "no-such-harness")
	})
	code, out, errb := runWorkerCheck(path)
	require.Equal(t, 1, code, "exit %d, want 1\nstdout: %s\nstderr: %s", code, out, errb)
	assert.Contains(t, out+errb, "WORKER DRIFT harness", "the drift does not name the harness field:\nstdout: %s\nstderr: %s", out, errb)
}

// `worker check --help` is a help request, not an unknown flag: it prints the verb's help
// on stdout at exit 0, the answer every other verb gives (the CLI style's rule (b), #4505).
func TestWorkerHelpMatchesOtherVerbs(t *testing.T) {
	t.Parallel()

	var otherOut, otherErr bytes.Buffer
	code := run([]string{"template", "--help"}, strings.NewReader(""), &otherOut, &otherErr, time.Now().UTC())
	require.Equal(t, 0, code, "template --help: exit %d, want 0", code)
	var out, errb bytes.Buffer
	code = run([]string{"worker", "check", "--help"}, strings.NewReader(""), &out, &errb, time.Now().UTC())
	require.Equal(t, 0, code, "worker check --help: exit %d, want 0\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
	assert.Zero(t, errb.Len(), "worker check --help: help is not a refusal, got stderr %q", errb.String())
	want := "usage: nova-swarm worker check [flags]\n"
	assert.True(t, strings.HasPrefix(out.String(), want), "worker check --help: got %q, want it to begin %q", out.String(), want)
	// every flag says what it wants (flag-usage): the two flags once printed a bare name
	assert.Contains(t, out.String(), "  --env  also require every secret the description names")
	assert.Contains(t, out.String(), "  --max <int>  at most this many WORKER DRIFT lines")
	assert.True(t, strings.HasPrefix(otherOut.String(), "usage: nova-swarm template [flags]\n"), "template --help: got %q", otherOut.String())
}

// A secret the environment does not hold is named by its variable, and no value leaks.
func TestWorkerCheckAnAbsentSecretWithEnvNamesTheVariableNotTheValue(t *testing.T) {
	const name = "NOVA_CARD8385_ABSENT_SECRET"
	const sentinel = "sk-must-never-print-8385"
	t.Setenv(name, "")
	t.Setenv("NOVA_CARD8385_SENTINEL", sentinel)
	path := workerCheckFixture(t, func(d map[string]any) {
		delete(d, "key_file")
		d["secret"] = name
	})
	code, out, errb := runWorkerCheck(path, "--env")
	combined := out + errb
	require.Equal(t, 1, code, "exit %d, want 1\nstdout: %s\nstderr: %s", code, out, errb)
	assert.Contains(t, combined, "WORKER DRIFT secret", "the drift does not name the secret field:\n%s", combined)
	assert.Contains(t, combined, name, "the drift does not name the absent variable %s:\n%s", name, combined)
	assert.NotContains(t, combined, sentinel, "a value reached the output:\n%s", combined)
}

// `worker check` reads its own flags by hand, so its help and its parser are two statements
// of one contract: --env is a boolean and takes no value, --max is an integer. This pins
// both, so a help that declares them as anything else, or a parser that reads them
// differently, fails here.
func TestWorkerCheckFlagTypes(t *testing.T) {
	t.Parallel()

	code, help, errb := runWorkerCheck("--help")
	require.Equal(t, 0, code, "worker check --help: exit %d, want 0\nstderr: %s", code, errb)
	helpLines := strings.Split(help, "\n")
	hasLine := func(want string) bool {
		for _, l := range helpLines {
			if strings.HasPrefix(l, want) {
				return true
			}
		}
		return false
	}
	assert.True(t, hasLine("  --env  "), "help does not declare --env as a boolean flag (`  --env  <what it wants>`):\n%s", help)
	assert.True(t, hasLine("  --max <int>  "), "help does not declare --max as an integer (`  --max <int>  <what it wants>`):\n%s", help)

	// A description with two drifts: the harness is absent, and the named secret is not in
	// this process's environment (which only --env asks about).
	const secret = "NOVA_WORKERCHECK_TYPES_UNSET_SECRET"
	path := workerCheckFixture(t, func(d map[string]any) {
		delete(d, "key_file")
		d["secret"] = secret
		d["harness"] = filepath.Join(t.TempDir(), "no-such-harness")
	})
	drifts := func(out string) (n int, all string) {
		for _, l := range strings.Split(out, "\n") {
			if strings.HasPrefix(l, "WORKER DRIFT ") {
				n++
			}
		}
		return n, out
	}

	cases := []struct {
		name       string
		args       []string
		wantDrifts int
		wantSecret bool
	}{
		{"--env takes no value: the path after it is the path", []string{"--env", path}, 2, true},
		{"--env=true", []string{"--env=true", path}, 2, true},
		{"--env=false", []string{"--env=false", path}, 1, false},
		{"no --env asks nothing of the environment", []string{path}, 1, false},
		{"--max 1 caps the list", []string{"--env", "--max", "1", path}, 1, true},
		{"--max=1 caps the list", []string{"--env", "--max=1", path}, 1, true},
		{"--max 5 shows both", []string{"--env", "--max", "5", path}, 2, true},
		{"--max=0 shows all", []string{"--env", "--max=0", path}, 2, true},
	}
	for _, c := range cases {
		code, out, errb := runWorkerCheck(c.args...)
		if !assert.Equal(t, 1, code, "%s: exit %d, want 1\nstdout: %s\nstderr: %s", c.name, code, out, errb) {
			continue
		}
		n, all := drifts(out)
		assert.Equal(t, c.wantDrifts, n, "%s: %d drift lines, want %d:\n%s%s", c.name, n, c.wantDrifts, all, errb)
		got := strings.Contains(out, "WORKER DRIFT secret")
		assert.False(t, got != c.wantSecret && c.wantDrifts == 2, "%s: secret drift present = %v, want %v:\n%s", c.name, got, c.wantSecret, out)
	}

	// A value that is not the flag's type is a refusal naming the flag.
	for _, args := range [][]string{{"--max", "x", path}, {"--max=x", path}, {"--max"}, {"--env=maybe", path}} {
		code, out, errb := runWorkerCheck(args...)
		assert.Equal(t, 2, code, "%v: exit %d, want a refusal on stderr\nstdout: %s\nstderr: %s", args, code, out, errb)
		assert.Contains(t, errb, "worker check", "%v: exit %d, want a refusal on stderr\nstdout: %s\nstderr: %s", args, code, out, errb)
		assert.Equal(t, "", out, "%v: exit %d, want a refusal on stderr\nstdout: %s\nstderr: %s", args, code, out, errb)
	}
}
