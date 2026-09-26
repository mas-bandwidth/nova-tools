// Package seattest builds a real nova-secrets store for a test: a git working
// copy with an upstream, recovery.pub, .sops.yaml and one seat's file sealed by
// sops to a fresh age key, laid out where internal/seatcred looks by default
// under a temporary HOME. It shells out to git, age-keygen and sops; a machine
// without sops or age-keygen skips the test, as the nova-secrets tests do.
package seattest

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/seatcred"
)

// Home returns a new HOME holding <home>/nova-bench/secrets (the store, with
// <seat>.yaml sealed from values) and <home>/.config/nova-secrets/<seat>.key.
// The values are test values; the caller asserts none of them is printed.
func Home(t *testing.T, seat string, values map[string]string) string {
	t.Helper()
	sops := lookTool(t, "sops")
	ageKeygen := lookTool(t, "age-keygen")
	home := t.TempDir()
	store := filepath.Join(home, filepath.FromSlash(seatcred.DefaultStore))
	keyDir := filepath.Join(home, filepath.FromSlash(seatcred.DefaultKeyDir))
	for _, d := range []string{store, keyDir} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	seatPub := genKey(t, ageKeygen, filepath.Join(keyDir, seat+".key"))
	recPub := genKey(t, ageKeygen, filepath.Join(t.TempDir(), "recovery.key"))

	remote := t.TempDir()
	run(t, "", "git", "init", "-q", "--bare", "-b", "main", remote)
	run(t, store, "git", "init", "-q", "-b", "main")
	run(t, store, "git", "config", "user.name", "Test")
	run(t, store, "git", "config", "user.email", "test@example.invalid")
	run(t, store, "git", "remote", "add", "origin", remote)
	write(t, filepath.Join(store, "recovery.pub"), recPub+"\n")
	write(t, filepath.Join(store, ".sops.yaml"), "creation_rules:\n  - path_regex: ^"+seat+"\\.yaml$\n    age: "+seatPub+","+recPub+"\n")

	names := make([]string, 0, len(values))
	for k := range values {
		names = append(names, k)
	}
	sort.Strings(names)
	var plain strings.Builder
	for _, k := range names {
		plain.WriteString(k + ": " + values[k] + "\n")
	}
	file := filepath.Join(store, seat+".yaml")
	write(t, file, plain.String())
	cmd := exec.Command(sops, "-e", "--age", seatPub+","+recPub, file)
	cmd.Env = []string{"PATH=/usr/bin:/bin"}
	sealed, err := cmd.Output()
	if err != nil {
		t.Fatalf("sops encrypt: %v", err)
	}
	write(t, file, string(sealed))
	run(t, store, "git", "add", "-A")
	run(t, store, "git", "commit", "-q", "-m", "seat")
	run(t, store, "git", "push", "-q", "-u", "origin", "main")
	return home
}

// Env points every variable seatcred reads at home and clears the ones that
// would otherwise steer a resolution (store, key, sops, user, seat) for the
// rest of the test. The discovered sops path is then pinned for decryption.
func Env(t *testing.T, home string) {
	t.Helper()
	t.Setenv("HOME", home)
	for _, k := range []string{seatcred.SeatEnv, seatcred.StoreEnv, seatcred.KeyEnv, seatcred.SopsEnv, seatcred.UserEnv} {
		t.Setenv(k, "")
	}
	// Home may find sops outside PATH (for example the macOS runner).
	// Read the fixture with the same discovery rule used to encrypt it.
	t.Setenv(seatcred.SopsEnv, lookTool(t, "sops"))
	t.Cleanup(func() { seatcred.Select("") })
}

func lookTool(t *testing.T, name string) string {
	t.Helper()
	if p, err := exec.LookPath(name); err == nil {
		return p
	}
	if p := "/opt/homebrew/bin/" + name; fileExists(p) {
		return p
	}
	t.Skipf("%s not found", name)
	return ""
}

func fileExists(p string) bool { _, err := os.Stat(p); return err == nil }

func genKey(t *testing.T, ageKeygen, path string) string {
	t.Helper()
	cmd := exec.Command(ageKeygen, "-o", path)
	cmd.Env = []string{"PATH=/usr/bin:/bin"}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("age-keygen: %v: %s", err, out)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(b), "\n") {
		if pub, ok := strings.CutPrefix(line, "# public key: "); ok {
			return strings.TrimSpace(pub)
		}
	}
	t.Fatalf("no public key comment in %s", path)
	return ""
}

func run(t *testing.T, dir, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %v: %v: %s", name, args, err, out)
	}
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}
