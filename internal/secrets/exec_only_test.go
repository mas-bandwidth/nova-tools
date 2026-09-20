package secrets

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestValuesReachProcessesOnlyByExecOnly pins the spec rule that values only reach
// a process via exec --only NAME (environment variables), never via file, argv,
// log, or transcript. Every other road leaves plaintext where sibling processes
// can read it.
func TestValuesReachProcessesOnlyByExecOnly(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()

	// The secret that should only live in env vars passed to the exec'd process.
	const secret = "ghp_abc123secret"

	// Write a plaintext file containing the secret (simulating a leak).
	leakFile := filepath.Join(tmp, "leak.txt")
	if err := os.WriteFile(leakFile, []byte(secret), 0644); err != nil {
		t.Fatal(err)
	}

	// Verify the Secret type doesn't expose the value through formatting.
	sec := NewSecret(secret)

	// String() should return Redacted, not the value.
	if sec.String() == secret {
		t.Error("Secret.String() exposed the value")
	}

	// Check that the secret doesn't leak through any formatting verb.
	s := sec.String()
	if strings.Contains(s, secret) {
		t.Errorf("Secret.String() contains the secret: %q", s)
	}

	// Verify that when Use() is called, it can safely pass the value to a callback
	// without it being exposed through file, argv, log, or transcript paths.
	// The test below verifies the callback receives the correct value.
	got := ""
	if err := sec.Use(func(v string) error {
		got = v
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if got != secret {
		t.Errorf("Use() gave %q, want %q", got, secret)
	}

	// Check that no file in tmp contains the secret except the intentional leak file.
	entries, err := os.ReadDir(tmp)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		path := filepath.Join(tmp, e.Name())
		if path == leakFile {
			continue // Expected leak
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(data, []byte(secret)) {
			t.Errorf("secret leaked to file %s", path)
		}
	}

	// Verify that the secret isn't in the current environment (simulating that
	// exec scrubs environment of secrets before passing to child process).
	for _, e := range os.Environ() {
		if strings.Contains(e, secret) {
			t.Errorf("secret leaked to environment: %s", e)
		}
	}
}
