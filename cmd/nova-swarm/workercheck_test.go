package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
	if err != nil {
		t.Fatal(err)
	}
	workerDir := filepath.Join(dir, "home")
	if err := os.MkdirAll(workerDir, 0o755); err != nil {
		t.Fatal(err)
	}
	keyFile := filepath.Join(dir, "key")
	if err := os.WriteFile(keyFile, []byte("FAKE_KEY=0123\n"), 0o600); err != nil {
		t.Fatal(err)
	}
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
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "worker.json")
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
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
	if code != 0 {
		t.Fatalf("exit %d, want 0\nstdout: %s\nstderr: %s", code, out, errb)
	}
	want := "WORKER OK check-1 model=fake-model provider=fake class=-"
	if got := strings.TrimSpace(out); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// A harness that is not there is one drift naming the harness field, exit 1.
func TestWorkerCheckAMissingHarnessNamesTheField(t *testing.T) {
	t.Parallel()

	path := workerCheckFixture(t, func(d map[string]any) {
		d["harness"] = filepath.Join(t.TempDir(), "no-such-harness")
	})
	code, out, errb := runWorkerCheck(path)
	if code != 1 {
		t.Fatalf("exit %d, want 1\nstdout: %s\nstderr: %s", code, out, errb)
	}
	if !strings.Contains(out+errb, "WORKER DRIFT harness") {
		t.Errorf("the drift does not name the harness field:\nstdout: %s\nstderr: %s", out, errb)
	}
}

// `worker check --help` is a help request, not an unknown flag: it prints the verb's help
// on stdout at exit 0, the answer every other verb gives (the CLI style's rule (b), #4505).
func TestWorkerHelpMatchesOtherVerbs(t *testing.T) {
	t.Parallel()

	var otherOut, otherErr bytes.Buffer
	if code := run([]string{"template", "--help"}, strings.NewReader(""), &otherOut, &otherErr, time.Now().UTC()); code != 0 {
		t.Fatalf("template --help: exit %d, want 0", code)
	}
	var out, errb bytes.Buffer
	code := run([]string{"worker", "check", "--help"}, strings.NewReader(""), &out, &errb, time.Now().UTC())
	if code != 0 {
		t.Fatalf("worker check --help: exit %d, want 0\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
	}
	if errb.Len() != 0 {
		t.Errorf("worker check --help: help is not a refusal, got stderr %q", errb.String())
	}
	if want := "usage: nova-swarm worker check [flags]\n"; !strings.HasPrefix(out.String(), want) {
		t.Errorf("worker check --help: got %q, want it to begin %q", out.String(), want)
	}
	if !strings.HasPrefix(otherOut.String(), "usage: nova-swarm template [flags]\n") {
		t.Errorf("template --help: got %q", otherOut.String())
	}
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
	if code != 1 {
		t.Fatalf("exit %d, want 1\nstdout: %s\nstderr: %s", code, out, errb)
	}
	if !strings.Contains(combined, "WORKER DRIFT secret") {
		t.Errorf("the drift does not name the secret field:\n%s", combined)
	}
	if !strings.Contains(combined, name) {
		t.Errorf("the drift does not name the absent variable %s:\n%s", name, combined)
	}
	if strings.Contains(combined, sentinel) {
		t.Errorf("a value reached the output:\n%s", combined)
	}
}

// `worker check` reads its own flags by hand, so its help and its parser are two statements
// of one contract: --env is a boolean and takes no value, --max is an integer. This pins
// both, so a help that declares them as anything else, or a parser that reads them
// differently, fails here.
func TestWorkerCheckFlagTypes(t *testing.T) {
	t.Parallel()

	code, help, errb := runWorkerCheck("--help")
	if code != 0 {
		t.Fatalf("worker check --help: exit %d, want 0\nstderr: %s", code, errb)
	}
	helpLines := strings.Split(help, "\n")
	hasLine := func(want string) bool {
		for _, l := range helpLines {
			if l == want {
				return true
			}
		}
		return false
	}
	if !hasLine("  --env") {
		t.Errorf("help does not declare --env as a boolean flag (a bare `  --env` line):\n%s", help)
	}
	if !hasLine("  --max <int>") {
		t.Errorf("help does not declare --max as an integer (`  --max <int>`):\n%s", help)
	}

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
		if code != 1 {
			t.Errorf("%s: exit %d, want 1\nstdout: %s\nstderr: %s", c.name, code, out, errb)
			continue
		}
		n, all := drifts(out)
		if n != c.wantDrifts {
			t.Errorf("%s: %d drift lines, want %d:\n%s%s", c.name, n, c.wantDrifts, all, errb)
		}
		if got := strings.Contains(out, "WORKER DRIFT secret"); got != c.wantSecret && c.wantDrifts == 2 {
			t.Errorf("%s: secret drift present = %v, want %v:\n%s", c.name, got, c.wantSecret, out)
		}
	}

	// A value that is not the flag's type is a refusal naming the flag.
	for _, args := range [][]string{{"--max", "x", path}, {"--max=x", path}, {"--max"}, {"--env=maybe", path}} {
		code, out, errb := runWorkerCheck(args...)
		if code != 2 || !strings.Contains(errb, "worker check") || out != "" {
			t.Errorf("%v: exit %d, want a refusal on stderr\nstdout: %s\nstderr: %s", args, code, out, errb)
		}
	}
}
