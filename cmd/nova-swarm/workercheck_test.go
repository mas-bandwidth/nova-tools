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
