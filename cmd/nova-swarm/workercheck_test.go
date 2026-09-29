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
	return runWorkerCheckWithEnv(os.Getenv, args...)
}

func runWorkerCheckWithEnv(getenv func(string) string, args ...string) (int, string, string) {
	var out, errb bytes.Buffer
	code := cmdWorkerWithEnv(append([]string{"check"}, args...), &out, &errb, getenv)
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

// A harness that is not there is one drift naming the harness field, exit 2.
func TestWorkerCheckAMissingHarnessNamesTheField(t *testing.T) {
	t.Parallel()

	path := workerCheckFixture(t, func(d map[string]any) {
		d["harness"] = filepath.Join(t.TempDir(), "no-such-harness")
	})
	code, out, errb := runWorkerCheck(path)
	if code != 2 {
		t.Fatalf("exit %d, want 2\nstdout: %s\nstderr: %s", code, out, errb)
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
	t.Parallel()

	const name = "NOVA_CARD8385_ABSENT_SECRET"
	const sentinel = "sk-must-never-print-8385"
	fakeEnv := func(k string) string {
		switch k {
		case name:
			return ""
		case "NOVA_CARD8385_SENTINEL":
			return sentinel
		default:
			return os.Getenv(k)
		}
	}
	path := workerCheckFixture(t, func(d map[string]any) {
		delete(d, "key_file")
		d["secret"] = name
	})
	code, out, errb := runWorkerCheckWithEnv(fakeEnv, path, "--env")
	combined := out + errb
	if code != 2 {
		t.Fatalf("exit %d, want 2\nstdout: %s\nstderr: %s", code, out, errb)
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
