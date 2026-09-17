package secrets

import (
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// skipPOSIXFakesOnWindows marks the seal tests whose sops/git/gh fakes are POSIX
// shell scripts and whose fixtures depend on POSIX mode bits. Windows can run
// neither, so the piped-stdin assertion is carried there by
// TestSealEncryptTakesValueOnStdin, which fakes the child in pure Go.
func skipPOSIXFakesOnWindows(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("card 8517: POSIX shell-script fakes and POSIX mode bits are unavailable on Windows; covered by TestSealEncryptTakesValueOnStdin")
	}
}

// sealFixture is a throwaway store, key, and a bench of fake helper binaries.
// Nothing here touches the network or a real credential.
type sealFixture struct {
	dir        string
	storeDir   string
	keyPath    string
	sopsPath   string
	gitPath    string
	ghPath     string
	sopsArgs   string
	sopsStdin  string
	sopsDecOut string
	gitArgs    string
	ghArgs     string
}

func newSealFixture(t *testing.T, decryptOut string) *sealFixture {
	t.Helper()
	dir := t.TempDir()
	f := &sealFixture{
		dir:        dir,
		storeDir:   filepath.Join(dir, "store"),
		sopsArgs:   filepath.Join(dir, "sops.args"),
		sopsStdin:  filepath.Join(dir, "sops.stdin"),
		sopsDecOut: filepath.Join(dir, "decrypt.out"),
		gitArgs:    filepath.Join(dir, "git.args"),
		ghArgs:     filepath.Join(dir, "gh.args"),
	}
	if err := os.MkdirAll(f.storeDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(f.storeDir, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.storeDir, ".sops.yaml"), []byte("creation_rules:\n  - path_regex: ^rowan\\.yaml$\n    age: age1abc\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.storeDir, "rowan.yaml"), []byte("ENC[old]\n"), 0644); err != nil {
		t.Fatal(err)
	}

	keyDir := filepath.Join(dir, "keys")
	if err := os.MkdirAll(keyDir, 0700); err != nil {
		t.Fatal(err)
	}
	f.keyPath = filepath.Join(keyDir, "rowan.key")
	if err := os.WriteFile(f.keyPath, []byte("AGE-SECRET-KEY-1TEST\n# public key: age1test\n"), 0600); err != nil {
		t.Fatal(err)
	}

	sopsBody := "ARGS=\"" + f.sopsArgs + "\"\n" +
		"STDIN=\"" + f.sopsStdin + "\"\n" +
		"DECOUT=\"" + f.sopsDecOut + "\"\n" +
		"if [ \"$1\" = \"--version\" ]; then echo \"sops 3.13.3\"; exit 0; fi\n" +
		"printf '%s\\n' \"$@\" >> \"$ARGS\"\n" +
		"case \"$*\" in\n" +
		"  *\"-d\"*) cat \"$DECOUT\"; exit 0;;\n" +
		"esac\n" +
		"cat > \"$STDIN\"\n" +
		"echo \"ENC[marker]\"\n"
	f.sopsPath = f.writeScript(t, "sops", sopsBody)

	gitBody := "printf '%s\\n' \"$@\" >> \"" + f.gitArgs + "\"\n" + "exit 0\n"
	f.gitPath = f.writeScript(t, "git", gitBody)
	ghBody := "printf '%s\\n' \"$@\" >> \"" + f.ghArgs + "\"\n" +
		"if [ \"$1 $2\" = \"pr create\" ]; then echo \"https://github.com/mas-bandwidth/secrets/pull/42\"; fi\n" +
		"if [ \"$1 $2\" = \"pr view\" ]; then echo \"APPROVED\"; fi\n" +
		"exit 0\n"
	f.ghPath = f.writeScript(t, "gh", ghBody)

	if err := os.WriteFile(f.sopsDecOut, []byte(decryptOut), 0644); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *sealFixture) writeScript(t *testing.T, name, body string) string {
	t.Helper()
	p := filepath.Join(f.dir, name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body), 0755); err != nil {
		t.Fatal(err)
	}
	return p
}

func (f *sealFixture) options(t *testing.T, name, value string, noPR bool) SealOptions {
	t.Helper()
	return SealOptions{
		StoreDir:        f.storeDir,
		AsName:          "rowan",
		KeyPath:         f.keyPath,
		SopsPath:        f.sopsPath,
		Name:            name,
		GitPath:         f.gitPath,
		GHPath:          f.ghPath,
		NoPR:            noPR,
		Stdin:           strings.NewReader(value),
		StdinIsTerminal: false,
		Now:             func() time.Time { return time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC) },
		Check:           func(storeDir, asName, keyPath, sopsPath string) error { return nil },
	}
}

func readMaybe(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(b)
}

func TestSealPipedValueLandsInEncryptStdinNotArgv(t *testing.T) {
	skipPOSIXFakesOnWindows(t)
	f := newSealFixture(t, "OTHER: keepme\nTARGET: oldvalue\n")
	line, err := RunSeal(f.options(t, "TARGET", "newsecretvalue\n", true))
	if err != nil {
		t.Fatalf("RunSeal: %v", err)
	}
	stdin := readMaybe(t, f.sopsStdin)
	if !strings.Contains(stdin, "TARGET: newsecretvalue") {
		t.Errorf("encrypt stdin missing the pasted value; got:\n%s", stdin)
	}
	if n := strings.Count(stdin, "TARGET:"); n != 1 {
		t.Errorf("encrypt stdin holds %d TARGET lines, want 1:\n%s", n, stdin)
	}
	argv := readMaybe(t, f.sopsArgs)
	if strings.Contains(argv, "newsecretvalue") {
		t.Errorf("value leaked into sops argv:\n%s", argv)
	}
	if !strings.Contains(argv, "--filename-override") || !strings.Contains(argv, "rowan.yaml") {
		t.Errorf("encrypt did not use --filename-override rowan.yaml:\n%s", argv)
	}
	if strings.Contains(line, "newsecretvalue") {
		t.Errorf("value leaked into the OK line: %s", line)
	}
	if !strings.Contains(line, "SEAL OK") || !strings.Contains(line, "name=TARGET") || !strings.Contains(line, "seat=rowan") {
		t.Errorf("unexpected OK line: %s", line)
	}
}

func TestSealEmptyValueRefused(t *testing.T) {
	skipPOSIXFakesOnWindows(t)
	f := newSealFixture(t, "TARGET: old\n")
	for _, value := range []string{"", "\n"} {
		_, err := RunSeal(f.options(t, "TARGET", value, true))
		if err == nil {
			t.Fatalf("RunSeal accepted an empty value %q", value)
		}
		if !strings.Contains(strings.ToLower(err.Error()), "empty") {
			t.Errorf("empty-value refusal does not say empty: %v", err)
		}
	}
	if got := readMaybe(t, f.sopsArgs); got != "" {
		t.Errorf("empty value still started sops:\n%s", got)
	}
}

func TestSealReplacesExistingNameNotDuplicated(t *testing.T) {
	skipPOSIXFakesOnWindows(t)
	f := newSealFixture(t, "TARGET: old\nOTHER: keepme\nTARGET: older\n")
	if _, err := RunSeal(f.options(t, "TARGET", "fresh\n", true)); err != nil {
		t.Fatalf("RunSeal: %v", err)
	}
	stdin := readMaybe(t, f.sopsStdin)
	if n := strings.Count(stdin, "TARGET:"); n != 1 {
		t.Errorf("TARGET appears %d times, want 1:\n%s", n, stdin)
	}
	if !strings.Contains(stdin, "TARGET: fresh") {
		t.Errorf("new value missing:\n%s", stdin)
	}
	if !strings.Contains(stdin, "OTHER: keepme") {
		t.Errorf("unrelated key was dropped:\n%s", stdin)
	}
}

func TestSealNoPRMakesNoGHCalls(t *testing.T) {
	skipPOSIXFakesOnWindows(t)
	f := newSealFixture(t, "TARGET: old\n")
	line, err := RunSeal(f.options(t, "TARGET", "v\n", true))
	if err != nil {
		t.Fatalf("RunSeal: %v", err)
	}
	if got := readMaybe(t, f.ghArgs); got != "" {
		t.Errorf("--no-pr called gh:\n%s", got)
	}
	git := readMaybe(t, f.gitArgs)
	if !strings.Contains(git, "checkout") || !strings.Contains(git, "commit") {
		t.Errorf("--no-pr did not checkout/commit:\n%s", git)
	}
	if strings.Contains(git, "push") {
		t.Errorf("--no-pr pushed:\n%s", git)
	}
	if !strings.Contains(line, "committed") {
		t.Errorf("--no-pr line should say committed: %s", line)
	}
}

func TestSealFullPathOpensPRAndMerges(t *testing.T) {
	skipPOSIXFakesOnWindows(t)
	f := newSealFixture(t, "TARGET: old\n")
	line, err := RunSeal(f.options(t, "TARGET", "v\n", false))
	if err != nil {
		t.Fatalf("RunSeal: %v", err)
	}
	gh := readMaybe(t, f.ghArgs)
	for _, want := range []string{"pr", "create", "view", "merge"} {
		if !strings.Contains(gh, want) {
			t.Errorf("gh calls missing %q:\n%s", want, gh)
		}
	}
	if !strings.Contains(line, "pr=#42") || !strings.Contains(line, "merged") {
		t.Errorf("unexpected merged line: %s", line)
	}
}

// TestSealEncryptTakesValueOnStdin asserts the core seal promise on every
// platform: the plaintext reaches the encrypt child on stdin and never in its
// argv. The child is a pure-Go fake supplied through the exec seam, so no
// shell and no POSIX mode bits are involved (card 8517).
func TestSealEncryptTakesValueOnStdin(t *testing.T) {
	var gotStdin string
	var gotArgs []string
	run := func(stdin io.Reader, env []string, dir, name string, args ...string) ([]byte, error) {
		if name != "sops" {
			t.Fatalf("unexpected helper %q", name)
		}
		gotArgs = append([]string(nil), args...)
		b, err := io.ReadAll(stdin)
		if err != nil {
			return nil, err
		}
		gotStdin = string(b)
		return []byte("ENC[marker]\n"), nil
	}

	const value = "newsecretvalue"
	plaintext := []byte("TARGET: " + value + "\n")
	out, err := sealEncrypt(run, "sops", "/nonexistent/rowan.key", "rowan.yaml", plaintext)
	if err != nil {
		t.Fatalf("sealEncrypt: %v", err)
	}
	if !strings.Contains(gotStdin, "TARGET: "+value) {
		t.Errorf("encrypt stdin missing the pasted value; got:\n%s", gotStdin)
	}
	if n := strings.Count(gotStdin, "TARGET:"); n != 1 {
		t.Errorf("encrypt stdin holds %d TARGET lines, want 1:\n%s", n, gotStdin)
	}
	for _, a := range gotArgs {
		if strings.Contains(a, value) {
			t.Errorf("value leaked into encrypt argv: %q", a)
		}
	}
	if !strings.Contains(string(out), "ENC[marker]") {
		t.Errorf("encrypt stdout not returned: %q", out)
	}
}
