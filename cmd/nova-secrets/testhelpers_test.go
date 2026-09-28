package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/goenv"
)

type keyPair struct {
	privPath string
	pubKey   string
}

func findSops(t *testing.T) string {
	t.Helper()
	if p, err := exec.LookPath("sops"); err == nil {
		return p
	}
	if _, err := os.Stat("/opt/homebrew/bin/sops"); err == nil {
		return "/opt/homebrew/bin/sops"
	}
	t.Skip("sops binary not found")
	return ""
}

func findAgeKeygen(t *testing.T) string {
	t.Helper()
	if p, err := exec.LookPath("age-keygen"); err == nil {
		return p
	}
	if _, err := os.Stat("/opt/homebrew/bin/age-keygen"); err == nil {
		return "/opt/homebrew/bin/age-keygen"
	}
	t.Skip("age-keygen binary not found")
	return ""
}

func genKey(t *testing.T, dir, name string) keyPair {
	t.Helper()
	ageKeygen := findAgeKeygen(t)
	keyDir := filepath.Join(dir, "keys")
	if err := os.MkdirAll(keyDir, 0700); err != nil {
		t.Fatal(err)
	}
	privPath := filepath.Join(keyDir, name+".key")
	cmd := exec.Command(ageKeygen, "-o", privPath)
	cmd.Env = []string{"PATH=/usr/bin:/bin"}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("age-keygen failed: %v, out: %s", err, out)
	}
	if err := os.Chmod(privPath, 0600); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(privPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "# public key: ") {
			return keyPair{
				privPath: privPath,
				pubKey:   strings.TrimSpace(strings.TrimPrefix(line, "# public key: ")),
			}
		}
	}
	t.Fatalf("public key comment missing in %s", privPath)
	return keyPair{}
}

func initGitStore(t *testing.T, storeDir string) {
	t.Helper()
	remoteDir := t.TempDir()
	runCmd(t, "", "git", "init", "--bare", "-b", "main", remoteDir)
	runCmd(t, storeDir, "git", "init", "-b", "main")
	runCmd(t, storeDir, "git", "config", "user.name", "Test")
	runCmd(t, storeDir, "git", "config", "user.email", "test@example.com")
	runCmd(t, storeDir, "git", "remote", "add", "origin", remoteDir)
}

func commitAndPush(t *testing.T, storeDir string) {
	t.Helper()
	runCmd(t, storeDir, "git", "add", "-A")
	st := runCmd(t, storeDir, "git", "status", "--porcelain")
	if strings.TrimSpace(st) == "" {
		return
	}
	runCmd(t, storeDir, "git", "commit", "-m", "sync")
	runCmd(t, storeDir, "git", "push", "-u", "origin", "main")
}

func runCmd(t *testing.T, dir string, name string, args ...string) string {
	t.Helper()
	cmd := exec.Command(name, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("command %s %v failed in %s: %v, out: %s", name, args, dir, err, out)
	}
	return string(out)
}

func sealFileWithSops(t *testing.T, sopsPath string, filePath string, ageKeys []string, content string) {
	t.Helper()
	if err := os.WriteFile(filePath, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	args := []string{"-e", "--age", strings.Join(ageKeys, ","), filePath}
	cmd := exec.Command(sopsPath, args...)
	cmd.Env = []string{"PATH=/usr/bin:/bin"}
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("sops encrypt failed: %v", err)
	}
	if err := os.WriteFile(filePath, out, 0600); err != nil {
		t.Fatal(err)
	}
}

// The tool is built once per test run, not once per test. Thirty-two tests each ran
// their own `go build` (about 3.5 s apiece), which put this package at two minutes:
// the whole budget of the two-minute law spent compiling the same binary (Glenn
// 2026-09-17). No test writes to the binary, so one shared copy is safe.
var (
	buildOnce sync.Once
	builtDir  string
	builtBin  string
	buildErr  error
	buildOut  []byte
)

func TestMain(m *testing.M) {
	code := m.Run()
	if builtDir != "" {
		os.RemoveAll(builtDir)
	}
	os.Exit(code)
}

func buildNovaSecrets(t *testing.T) string {
	t.Helper()
	buildOnce.Do(func() {
		builtDir, buildErr = os.MkdirTemp("", "nova-secrets-testbin-*")
		if buildErr != nil {
			return
		}
		builtBin = filepath.Join(builtDir, "nova-secrets")
		if runtime.GOOS == "windows" {
			// `go build -o <file>` writes EXACTLY the name it is handed, and
			// os/exec resolves a path whose extension is not in PATHEXT through
			// lookPathExts, which only ever tries <path>.exe, <path>.bat and the
			// rest. Without this suffix the build succeeds and the binary then
			// never starts, and because the failure is an *exec.Error and not an
			// *exec.ExitError it arrived at the caller as exit 1 with two empty
			// streams -- indistinguishable from a tool that refused without a word.
			builtBin += ".exe"
		}
		build := exec.Command("go", "build", "-o", builtBin, ".")
		build.Env = goenv.Clean(os.Environ())
		buildOut, buildErr = build.CombinedOutput()
	})
	if buildErr != nil {
		t.Fatalf("failed to build nova-secrets: %v, out: %s", buildErr, string(buildOut))
	}
	return builtBin
}

func runNovaSecrets(bin string, args ...string) (stdout string, stderr string, exitCode int) {
	var outBuf, errBuf bytes.Buffer
	cmd := exec.Command(bin, args...)
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	err := cmd.Run()
	code := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			code = exitErr.ExitCode()
		} else {
			// The binary never ran at all. Say which, on stderr, because a
			// bare 1 with nothing on either stream reads exactly like a
			// refusal that forgot its message and cost a whole CI round to
			// tell apart.
			code = 1
			errBuf.WriteString("nova-secrets did not run: " + err.Error() + "\n")
		}
	}
	return outBuf.String(), errBuf.String(), code
}
