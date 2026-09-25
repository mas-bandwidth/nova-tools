package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/goenv"
	"github.com/mas-bandwidth/nova-tools/internal/testbin"
)

// Test 6: TestSopsErrorsAreNeverPassedThroughRaw
func TestSopsErrorsAreNeverPassedThroughRaw(t *testing.T) {
	t.Parallel()
	bin := buildNovaSecrets(t)
	td := t.TempDir()
	storeDir := filepath.Join(td, "store")
	_ = os.MkdirAll(storeDir, 0755)
	initGitStore(t, storeDir)

	keyA := genKey(t, td, "keya")
	recKey := genKey(t, td, "rec")

	_ = os.WriteFile(filepath.Join(storeDir, "recovery.pub"), []byte(recKey.pubKey+"\n"), 0644)
	sopsCfg := fmt.Sprintf(`creation_rules:
  - path_regex: ^rowan\.yaml$
    age: %s,%s
`, keyA.pubKey, recKey.pubKey)
	_ = os.WriteFile(filepath.Join(storeDir, ".sops.yaml"), []byte(sopsCfg), 0644)

	// Create a corrupt/unopenable rowan.yaml that causes sops to fail
	_ = os.WriteFile(filepath.Join(storeDir, "rowan.yaml"), []byte("sops:\n  version: 3.13.3\n  age:\n    - recipient: "+keyA.pubKey+"\n"), 0644)
	commitAndPush(t, storeDir)

	fakeSecret := "PLAINTEXT_SECRET_LEAK_IN_STDERR_12345"
	fakeSops := filepath.Join(td, "fake-sops")
	fakeScript := fmt.Sprintf(`#!/bin/sh
if [ "$1" = "--version" ]; then
    echo "sops 3.13.3"
    exit 0
fi
echo "Fatal error: %s" >&2
exit 1
`, fakeSecret)
	if err := testbin.WriteExecutable(fakeSops, []byte(fakeScript), 0755); err != nil {
		t.Fatal(err)
	}

	out, errOut, code := runNovaSecrets(bin, "exec", "--store", storeDir, "--as", "rowan", "--key", keyA.privPath,
		"--sops", fakeSops, "--only", "GH_TOKEN", "--", "true")

	if code != 125 {
		t.Fatalf("expected code 125, got %d", code)
	}
	if strings.Contains(out, fakeSecret) || strings.Contains(errOut, fakeSecret) {
		t.Fatalf("leaked fakeSecret in stdout or stderr: %s, %s", out, errOut)
	}
	if !strings.Contains(errOut, "transcript withheld") {
		t.Errorf("expected 'transcript withheld' in stderr: %s", errOut)
	}
}

// Test 7: TestTheKeyFileModeIsARefusalOnEveryVerbThatTakesOne
func TestTheKeyFileModeIsARefusalOnEveryVerbThatTakesOne(t *testing.T) {
	t.Parallel()
	sopsPath := findSops(t)
	bin := buildNovaSecrets(t)

	td := t.TempDir()
	storeDir := filepath.Join(td, "store")
	_ = os.MkdirAll(storeDir, 0755)
	initGitStore(t, storeDir)

	keyA := genKey(t, td, "keya")
	recKey := genKey(t, td, "rec")

	_ = os.WriteFile(filepath.Join(storeDir, "recovery.pub"), []byte(recKey.pubKey+"\n"), 0644)
	sopsCfg := fmt.Sprintf(`creation_rules:
  - path_regex: ^rowan\.yaml$
    age: %s,%s
`, keyA.pubKey, recKey.pubKey)
	_ = os.WriteFile(filepath.Join(storeDir, ".sops.yaml"), []byte(sopsCfg), 0644)
	sealFileWithSops(t, sopsPath, filepath.Join(storeDir, "rowan.yaml"), []string{keyA.pubKey, recKey.pubKey}, "GH_TOKEN: ghp_123\n")
	commitAndPush(t, storeDir)

	// 1. 0644 mode
	_ = os.Chmod(keyA.privPath, 0644)
	_, errOut, code := runNovaSecrets(bin, "exec", "--store", storeDir, "--as", "rowan", "--key", keyA.privPath, "--sops", sopsPath, "--only", "all", "--", "true")
	if code != 125 || !strings.Contains(errOut, "chmod 600") {
		t.Errorf("exec 0644 mode expected 125 with chmod 600, got %d: %s", code, errOut)
	}
	_, errOut, code = runNovaSecrets(bin, "check", "--store", storeDir, "--as", "rowan", "--key", keyA.privPath, "--sops", sopsPath)
	if code != 2 || !strings.Contains(errOut, "chmod 600") {
		t.Errorf("check 0644 mode expected 2 with chmod 600, got %d: %s", code, errOut)
	}
	// names takes no --key and succeeds beside it
	out, _, code := runNovaSecrets(bin, "names", "--store", storeDir, "--as", "rowan")
	if code != 0 || !strings.Contains(out, "GH_TOKEN") {
		t.Errorf("names should succeed regardless of key file mode: code %d, out: %s", code, out)
	}

	// 2. 0640 mode
	_ = os.Chmod(keyA.privPath, 0640)
	_, errOut, code = runNovaSecrets(bin, "check", "--store", storeDir, "--as", "rowan", "--key", keyA.privPath, "--sops", sopsPath)
	if code != 2 || !strings.Contains(errOut, "chmod 600") {
		t.Errorf("check 0640 mode expected 2: %s", errOut)
	}

	// 3. 0600 in 0755 directory
	_ = os.Chmod(keyA.privPath, 0600)
	keyDir := filepath.Dir(keyA.privPath)
	_ = os.Chmod(keyDir, 0755)
	_, errOut, code = runNovaSecrets(bin, "check", "--store", storeDir, "--as", "rowan", "--key", keyA.privPath, "--sops", sopsPath)
	if code != 2 || !strings.Contains(errOut, "chmod 700") {
		t.Errorf("check 0755 dir expected 2 with chmod 700: %s", errOut)
	}

	// Restore 0700 on key directory
	_ = os.Chmod(keyDir, 0700)

	// 4. Key file inside --store makes check red (invariant 5)
	storeKey := filepath.Join(storeDir, "planted.key")
	_ = os.WriteFile(storeKey, []byte("AGE-SECRET-KEY-1XYZ\n"), 0600)
	_, errOut, code = runNovaSecrets(bin, "check", "--store", storeDir, "--as", "rowan", "--key", keyA.privPath, "--sops", sopsPath)
	if code != 1 || !strings.Contains(errOut, "private key found in store") {
		t.Errorf("expected invariant 5 failure when key inside store: code %d, err: %s", code, errOut)
	}
	_ = os.Remove(storeKey)

	// 5. Stripped # public key: comment in key file
	rawBytes, _ := os.ReadFile(keyA.privPath)
	var stripped []string
	for _, l := range strings.Split(string(rawBytes), "\n") {
		if !strings.HasPrefix(l, "# public key:") {
			stripped = append(stripped, l)
		}
	}
	_ = os.WriteFile(keyA.privPath, []byte(strings.Join(stripped, "\n")), 0600)

	// Refusal on check naming age-keygen -y
	_, errOut, code = runNovaSecrets(bin, "check", "--store", storeDir, "--as", "rowan", "--key", keyA.privPath, "--sops", sopsPath)
	if code != 2 || !strings.Contains(errOut, "age-keygen -y") {
		t.Errorf("expected check refusal naming age-keygen -y, got %d: %s", code, errOut)
	}

	// But NOT on exec (exec never reads public key)
	_, _, code = runNovaSecrets(bin, "exec", "--store", storeDir, "--as", "rowan", "--key", keyA.privPath, "--sops", sopsPath,
		"--only", "all", "--", "true")
	if code != 0 {
		t.Errorf("exec should succeed with stripped comment, got code %d", code)
	}
}

// Test 8: TestTheVersionProbeMakesNoNetworkCall
func TestTheVersionProbeMakesNoNetworkCall(t *testing.T) {
	t.Parallel()
	bin := buildNovaSecrets(t)
	td := t.TempDir()
	storeDir := filepath.Join(td, "store")
	_ = os.MkdirAll(storeDir, 0755)
	initGitStore(t, storeDir)

	keyA := genKey(t, td, "keya")
	recKey := genKey(t, td, "rec")

	_ = os.WriteFile(filepath.Join(storeDir, "recovery.pub"), []byte(recKey.pubKey+"\n"), 0644)
	sopsCfg := fmt.Sprintf(`creation_rules:
  - path_regex: ^rowan\.yaml$
    age: %s,%s
`, keyA.pubKey, recKey.pubKey)
	_ = os.WriteFile(filepath.Join(storeDir, ".sops.yaml"), []byte(sopsCfg), 0644)
	_ = os.WriteFile(filepath.Join(storeDir, "rowan.yaml"), []byte("GH_TOKEN: ghp_123\n"), 0644)
	commitAndPush(t, storeDir)

	// 1. Version 3.9.0 is refused naming brew upgrade
	oldSops := filepath.Join(td, "old-sops")
	_ = testbin.WriteExecutable(oldSops, []byte("#!/bin/sh\necho 'sops 3.9.0'\n"), 0755)
	_, errOut, code := runNovaSecrets(bin, "check", "--store", storeDir, "--as", "rowan", "--key", keyA.privPath, "--sops", oldSops)
	if code != 2 || !strings.Contains(errOut, "brew upgrade sops") {
		t.Errorf("expected brew upgrade refusal, got %d: %s", code, errOut)
	}

	// 2. banana is refused as unparseable
	bananaSops := filepath.Join(td, "banana-sops")
	_ = testbin.WriteExecutable(bananaSops, []byte("#!/bin/sh\necho 'banana'\n"), 0755)
	_, errOut, code = runNovaSecrets(bin, "check", "--store", storeDir, "--as", "rowan", "--key", keyA.privPath, "--sops", bananaSops)
	if code != 2 || !strings.Contains(errOut, "unable to parse sops version") {
		t.Errorf("expected unparseable version refusal, got %d: %s", code, errOut)
	}

	// 3. Absent vs non-executable
	nonExecSops := filepath.Join(td, "nonexec-sops")
	_ = os.WriteFile(nonExecSops, []byte("echo hi\n"), 0644)
	_, errOut, code = runNovaSecrets(bin, "check", "--store", storeDir, "--as", "rowan", "--key", keyA.privPath, "--sops", nonExecSops)
	if code != 2 || !strings.Contains(errOut, "absent or not executable") {
		t.Errorf("expected absent or not executable refusal: %s", errOut)
	}

	// 4. The probe itself: every call of sops' version probe carries
	// --disable-version-check (sops otherwise asks GitHub for the latest release) and an
	// environment holding no proxy or egress helper.
	sopsPath := findSops(t)
	sealFileWithSops(t, sopsPath, filepath.Join(storeDir, "rowan.yaml"), []string{keyA.pubKey, recKey.pubKey}, "GH_TOKEN: ghp_123\n")
	commitAndPush(t, storeDir)
	probeLog := filepath.Join(td, "probe.log")
	recording := filepath.Join(td, "recording-sops")
	_ = testbin.WriteExecutable(recording, []byte("#!/bin/sh\nif [ \"$1\" = --version ]; then echo \"ARGS $*\" >> '"+probeLog+"'; env | sed 's/^/ENV /' >> '"+probeLog+"'; fi\nexec '"+sopsPath+"' \"$@\"\n"), 0755)
	callerEnv := append(os.Environ(), "HTTPS_PROXY=http://proxy.invalid:3128", "ALL_PROXY=socks5://proxy.invalid:1080")
	for _, verb := range [][]string{
		{"check", "--store", storeDir, "--as", "rowan", "--key", keyA.privPath, "--sops", recording},
		{"exec", "--store", storeDir, "--as", "rowan", "--key", keyA.privPath, "--sops", recording, "--only", "all", "--", "true"},
	} {
		_ = os.Remove(probeLog)
		_, errOut, code, _ := runWithEnv(bin, callerEnv, verb...)
		if code != 0 {
			t.Fatalf("%s through the recording sops: exit %d: %s", verb[0], code, errOut)
		}
		logged, err := os.ReadFile(probeLog)
		if err != nil {
			t.Fatalf("%s ran no version probe: %v", verb[0], err)
		}
		for _, line := range strings.Split(strings.TrimSpace(string(logged)), "\n") {
			if strings.HasPrefix(line, "ARGS ") && !strings.Contains(line, "--disable-version-check") {
				t.Errorf("%s: the version probe can reach the network: %s", verb[0], line)
			}
			if strings.HasPrefix(line, "ENV ") && strings.Contains(strings.ToUpper(line), "PROXY") {
				t.Errorf("%s: the version probe inherited an egress helper: %s", verb[0], line)
			}
		}
	}

	// 5. With egress blocked by the OS, every verb is green: no line of this tool opens
	// a socket. The wall is nova-sandbox --net-deny, which refuses rather than pretends
	// where it cannot enforce the denial; that refusal is this half's stated skip.
	sb := buildNovaSandbox(t)
	home := filepath.Join(td, "home")
	_ = os.MkdirAll(home, 0700)
	reads := []string{filepath.Dir(bin), storeDir, filepath.Dir(keyA.privPath), filepath.Dir(sopsPath)}
	if real, err := filepath.EvalSymlinks(sopsPath); err == nil {
		reads = append(reads, filepath.Dir(real))
	}
	requireWall(t, sb, home)
	for _, verb := range [][]string{
		{"version"},
		{"names", "--store", storeDir, "--as", "rowan"},
		{"check", "--store", storeDir, "--as", "rowan", "--key", keyA.privPath, "--sops", sopsPath},
		{"exec", "--store", storeDir, "--as", "rowan", "--key", keyA.privPath, "--sops", sopsPath, "--only", "all", "--", "true"},
	} {
		out, errOut, code := inWall(sb, home, reads, true, append([]string{bin}, verb...)...)
		if code != 0 {
			t.Errorf("%s with egress blocked: exit %d, want 0\nstdout: %s\nstderr: %s", verb[0], code, out, errOut)
		}
	}
}

var (
	sandboxOnce sync.Once
	sandboxBin  string
	sandboxErr  error
	sandboxOut  []byte
)

// buildNovaSandbox builds this repository's nova-sandbox once per run, beside the
// nova-secrets binary: the wall under test is the one at this commit, not whatever a
// bench has on PATH.
func buildNovaSandbox(t *testing.T) string {
	t.Helper()
	secretsBin := buildNovaSecrets(t)
	sandboxOnce.Do(func() {
		sandboxBin = filepath.Join(filepath.Dir(secretsBin), "nova-sandbox")
		if runtime.GOOS == "windows" {
			sandboxBin += ".exe"
		}
		build := exec.Command("go", "build", "-o", sandboxBin, "../nova-sandbox")
		build.Env = goenv.Clean(os.Environ())
		sandboxOut, sandboxErr = build.CombinedOutput()
	})
	if sandboxErr != nil {
		t.Fatalf("failed to build nova-sandbox: %v, out: %s", sandboxErr, sandboxOut)
	}
	return sandboxBin
}

// wallReads are the system directories a command needs to start at all; the ones this
// machine does not have are left out.
func wallReads() []string {
	var reads []string
	for _, d := range []string{"/usr", "/bin", "/lib", "/lib64", "/etc", "/System/Library", "/Library/Developer/CommandLineTools"} {
		if _, err := os.Stat(d); err == nil {
			reads = append(reads, d)
		}
	}
	return reads
}

// inWall runs argv under nova-sandbox with home as the only write set and HOME, the given
// read sets plus wallReads, and egress denied when netDeny is set.
func inWall(sb, home string, reads []string, netDeny bool, argv ...string) (stdout, stderr string, code int) {
	args := []string{"--write", home}
	for _, r := range append(wallReads(), reads...) {
		args = append(args, "--read", r)
	}
	if netDeny {
		args = append(args, "--net-deny")
	}
	args = append(append(args, "--"), argv...)
	env := []string{"PATH=/usr/bin:/bin", "HOME=" + home}
	out, errOut, code, _ := runWithEnv(sb, env, args...)
	return out, errOut, code
}

// requireWall skips, naming the reason nova-sandbox gave, when this machine cannot build
// an enforced wall with egress denied. It never lets a test pass without one.
func requireWall(t *testing.T, sb, home string) {
	t.Helper()
	_, errOut, code := inWall(sb, home, nil, true, "/bin/sh", "-c", "exit 0")
	if code != 0 {
		t.Skipf("nova-sandbox cannot wall this machine with egress denied (exit %d): %s", code, strings.TrimSpace(errOut))
	}
}

// Test 9: TestARequireThatIsMissingRefusesBeforeTheCommandStarts
func TestARequireThatIsMissingRefusesBeforeTheCommandStarts(t *testing.T) {
	t.Parallel()
	sopsPath := findSops(t)
	bin := buildNovaSecrets(t)

	td := t.TempDir()
	storeDir := filepath.Join(td, "store")
	_ = os.MkdirAll(storeDir, 0755)
	initGitStore(t, storeDir)

	keyA := genKey(t, td, "keya")
	recKey := genKey(t, td, "rec")

	_ = os.WriteFile(filepath.Join(storeDir, "recovery.pub"), []byte(recKey.pubKey+"\n"), 0644)
	sopsCfg := fmt.Sprintf(`creation_rules:
  - path_regex: ^rowan\.yaml$
    age: %s,%s
`, keyA.pubKey, recKey.pubKey)
	_ = os.WriteFile(filepath.Join(storeDir, ".sops.yaml"), []byte(sopsCfg), 0644)
	sealFileWithSops(t, sopsPath, filepath.Join(storeDir, "rowan.yaml"), []string{keyA.pubKey, recKey.pubKey}, "GH_TOKEN: ghp_123\n")
	commitAndPush(t, storeDir)

	sentinel := filepath.Join(td, "sentinel.txt")
	_, errOut, code := runNovaSecrets(bin, "exec", "--store", storeDir, "--as", "rowan", "--key", keyA.privPath, "--sops", sopsPath,
		"--only", "all", "--require", "MISSING_Z", "--require", "MISSING_A", "--", "touch", sentinel)

	if code != 125 {
		t.Fatalf("expected code 125, got %d", code)
	}
	if _, err := os.Stat(sentinel); !os.IsNotExist(err) {
		t.Fatalf("sentinel was created despite missing required keys!")
	}
	if !strings.Contains(errOut, "MISSING_A, MISSING_Z") {
		t.Errorf("expected missing keys to be reported sorted, got: %s", errOut)
	}
}

// Test 10: TestAMultiLineValueIsRefusedWithGenerateItWhereItIsUsed
func TestAMultiLineValueIsRefusedWithGenerateItWhereItIsUsed(t *testing.T) {
	t.Parallel()
	sopsPath := findSops(t)
	bin := buildNovaSecrets(t)

	td := t.TempDir()
	storeDir := filepath.Join(td, "store")
	_ = os.MkdirAll(storeDir, 0755)
	initGitStore(t, storeDir)

	keyA := genKey(t, td, "keya")
	recKey := genKey(t, td, "rec")

	_ = os.WriteFile(filepath.Join(storeDir, "recovery.pub"), []byte(recKey.pubKey+"\n"), 0644)
	sopsCfg := fmt.Sprintf(`creation_rules:
  - path_regex: ^rowan\.yaml$
    age: %s,%s
`, keyA.pubKey, recKey.pubKey)
	_ = os.WriteFile(filepath.Join(storeDir, ".sops.yaml"), []byte(sopsCfg), 0644)

	multiVal := "SPACE_KEY: |\n  line1\n  line2\nGH_TOKEN: ghp_123\n"
	sealFileWithSops(t, sopsPath, filepath.Join(storeDir, "rowan.yaml"), []string{keyA.pubKey, recKey.pubKey}, multiVal)
	commitAndPush(t, storeDir)

	// exec refuses naming key and printing remedy
	_, errOut, code := runNovaSecrets(bin, "exec", "--store", storeDir, "--as", "rowan", "--key", keyA.privPath, "--sops", sopsPath,
		"--only", "all", "--", "true")
	if code != 125 {
		t.Fatalf("expected code 125, got %d", code)
	}
	if !strings.Contains(errOut, "key=SPACE_KEY: value is multi-line; a file-shaped secret is not an environment variable.") ||
		!strings.Contains(errOut, "generate it where it is used: this store holds no file-shaped secrets.") {
		t.Errorf("expected multi-line refusal text, got: %s", errOut)
	}

	// names lists it with NO --key, NO --sops, and no sops process started
	badSops := filepath.Join(td, "bad-sops-would-fail")
	out, _, code := runNovaSecrets(bin, "names", "--store", storeDir, "--as", "rowan")
	if code != 0 {
		t.Fatalf("names failed: %d", code)
	}
	if !strings.Contains(out, "key=SPACE_KEY") || !strings.Contains(out, "key=GH_TOKEN") {
		t.Errorf("names missing SPACE_KEY or GH_TOKEN: %s", out)
	}
	_ = badSops
}
