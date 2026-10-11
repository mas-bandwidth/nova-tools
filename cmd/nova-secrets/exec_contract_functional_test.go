//go:build functional

package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/testbin"
)

// Test 2: TestExecSetsExactlyTheKeysInTheFile
func TestExecSetsExactlyTheKeysInTheFile(t *testing.T) {
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

	plainSecrets := "GH_TOKEN: ghp_testtoken123\nDEEPSEEK_API_KEY: ds_456\nSPACE_USER: glenn\n"
	sealFileWithSops(t, sopsPath, filepath.Join(storeDir, "rowan.yaml"), []string{keyA.pubKey, recKey.pubKey}, plainSecrets)
	commitAndPush(t, storeDir)

	// Test script that dumps environment
	// 1. --only GH_TOKEN puts exactly one key in child and only=1 on stderr line
	cmd := exec.Command(bin, "exec", "--store", storeDir, "--as", "rowan", "--key", keyA.privPath, "--sops", sopsPath,
		"--only", "GH_TOKEN", "--", "env")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	require.NoError(t, err, "exec failed: %v, stderr: %s", err, stderr.String())
	assert.Contains(t, stderr.String(), "only=1", "expected only=1 in stderr: %s", stderr.String())
	envOutput := stdout.String()
	assert.Contains(t, envOutput, "GH_TOKEN=ghp_testtoken123", "missing GH_TOKEN in child env: %s", envOutput)
	assert.NotContains(t, envOutput, "DEEPSEEK_API_KEY", "child env leaked keys outside --only: %s", envOutput)
	assert.NotContains(t, envOutput, "SPACE_USER", "child env leaked keys outside --only: %s", envOutput)
	assert.NotContains(t, envOutput, "NOVA_SECRETS_", "child env has invented NOVA_SECRETS_ marker: %s", envOutput)

	// 2. --only all puts every key and says only=all
	cmd = exec.Command(bin, "exec", "--store", storeDir, "--as", "rowan", "--key", keyA.privPath, "--sops", sopsPath,
		"--only", "all", "--", "env")
	stdout.Reset()
	stderr.Reset()
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err = cmd.Run()
	require.NoError(t, err, "exec failed: %v, stderr: %s", err, stderr.String())
	assert.Contains(t, stderr.String(), "only=all", "expected only=all in stderr: %s", stderr.String())
	envOutput = stdout.String()
	assert.Contains(t, envOutput, "GH_TOKEN=ghp_testtoken123", "missing expected keys in only=all: %s", envOutput)
	assert.Contains(t, envOutput, "DEEPSEEK_API_KEY=ds_456", "missing expected keys in only=all: %s", envOutput)
	assert.Contains(t, envOutput, "SPACE_USER=glenn", "missing expected keys in only=all: %s", envOutput)

	// 3. --require for an excluded key is 125
	_, errOut, code := runNovaSecrets(bin, "exec", "--store", storeDir, "--as", "rowan", "--key", keyA.privPath, "--sops", sopsPath,
		"--only", "GH_TOKEN", "--require", "DEEPSEEK_API_KEY", "--", "echo", "NO")
	assert.Equal(t, 125, code, "expected 125 for excluded required key, got %d: %s", code, errOut)
	assert.Contains(t, errOut, "excluded by --only", "expected 125 for excluded required key, got %d: %s", code, errOut)

	// 4. --only naming a key the file does not hold is 125 naming that key
	_, errOut, code = runNovaSecrets(bin, "exec", "--store", storeDir, "--as", "rowan", "--key", keyA.privPath, "--sops", sopsPath,
		"--only", "NONEXISTENT", "--", "echo", "NO")
	assert.Equal(t, 125, code, "expected 125 naming nonexistent key, got %d: %s", code, errOut)
	assert.Contains(t, errOut, "NONEXISTENT", "expected 125 naming nonexistent key, got %d: %s", code, errOut)

	// 5. No --only at all is 125 naming the flag
	_, errOut, code = runNovaSecrets(bin, "exec", "--store", storeDir, "--as", "rowan", "--key", keyA.privPath, "--sops", sopsPath,
		"--", "echo", "NO")
	assert.Equal(t, 125, code, "expected 125 naming missing --only, got %d: %s", code, errOut)
	assert.Contains(t, errOut, "--only", "expected 125 naming missing --only, got %d: %s", code, errOut)

	// 6. Every variable and every file in sops' identity lookup, planted and defeated.
	execIdentityLookupIsDefeated(t, bin, sopsPath)
}

// runWithEnv runs the tool with exactly env and returns both streams, the exit code and
// the pid the tool ran as.
func runWithEnv(bin string, env []string, args ...string) (stdout, stderr string, code, pid int) {
	var outBuf, errBuf bytes.Buffer
	cmd := exec.Command(bin, args...)
	cmd.Env = env
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	err := cmd.Start()
	if err != nil {
		return "", "nova-secrets did not start: " + err.Error(), 1, 0
	}
	pid = cmd.Process.Pid
	err = cmd.Wait()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			code = exitErr.ExitCode()
		} else {
			code = 1
		}
	}
	return outBuf.String(), errBuf.String(), code, pid
}

// sshKey makes an unencrypted ssh identity and returns its public half without comment.
func sshKey(t *testing.T, path, kind string) string {
	t.Helper()
	args := []string{"-q", "-t", kind, "-N", "", "-C", "", "-f", path}
	if kind == "rsa" {
		args = append(args, "-b", "2048")
	}
	runCmd(t, "", "ssh-keygen", args...)
	pub, err := os.ReadFile(path + ".pub")
	require.NoError(t, err)
	f := strings.Fields(string(pub))
	require.GreaterOrEqual(t, len(f), 2, "unreadable ssh public key %s", pub)
	return f[0] + " " + f[1]
}

// execIdentityLookupIsDefeated is test 2's isolation half. Each foreign identity sops
// would find on its own is planted in the caller's environment or under the caller's HOME
// and XDG_CONFIG_HOME, and one store file is sealed to each of them and never to --key.
// Every exec of those files is refused as a failed decrypt; with the isolation removed
// sops finds the planted identity and the decrypt succeeds, which is the red this pins.
// The seat's own file still opens with --key alone, the _CMD variable is a command that
// would write a sentinel and is asserted never run, and no variable of the lookup reaches
// the command.
func execIdentityLookupIsDefeated(t *testing.T, bin, sopsPath string) {
	t.Helper()
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		t.Skip("ssh-keygen not on PATH: the ssh identity files of sops' lookup cannot be planted")
	}
	td := t.TempDir()
	storeDir := filepath.Join(td, "store")
	_ = os.MkdirAll(storeDir, 0755)
	initGitStore(t, storeDir)
	keyA := genKey(t, td, "keya")
	recKey := genKey(t, td, "rec")
	_ = os.WriteFile(filepath.Join(storeDir, "recovery.pub"), []byte(recKey.pubKey+"\n"), 0644)

	home := filepath.Join(td, "callerhome")
	xdg := filepath.Join(td, "callerxdg")
	plant := func(path string, data []byte) {
		t.Helper()
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0700))
		require.NoError(t, os.WriteFile(path, data, 0600))
	}
	ageIdentity := func(name string) (priv []byte, pub string) {
		k := genKey(t, td, name)
		b, err := os.ReadFile(k.privPath)
		require.NoError(t, err)
		return b, k.pubKey
	}

	sealedTo := map[string]string{} // seat file -> the only non-recovery recipient
	xdgPriv, xdgPub := ageIdentity("xdg")
	plant(filepath.Join(xdg, "sops", "age", "keys.txt"), xdgPriv)
	sealedTo["idxdg"] = xdgPub
	libPriv, libPub := ageIdentity("lib")
	plant(filepath.Join(home, "Library", "Application Support", "sops", "age", "keys.txt"), libPriv)
	plant(filepath.Join(home, ".config", "sops", "age", "keys.txt"), libPriv)
	sealedTo["idlib"] = libPub
	_ = os.MkdirAll(filepath.Join(home, ".ssh"), 0700)
	sealedTo["idsshed"] = sshKey(t, filepath.Join(home, ".ssh", "id_ed25519"), "ed25519")
	sealedTo["idsshrsa"] = sshKey(t, filepath.Join(home, ".ssh", "id_rsa"), "rsa")
	envKeyPriv, envKeyPub := ageIdentity("envkey")
	sealedTo["idenvkey"] = envKeyPub
	envFilePriv, envFilePub := ageIdentity("envfile")
	envFilePath := filepath.Join(td, "planted", "envfile.key")
	plant(envFilePath, envFilePriv)
	sealedTo["idenvfile"] = envFilePub
	envCmdPriv, envCmdPub := ageIdentity("envcmd")
	envCmdKey := filepath.Join(td, "planted", "envcmd.key")
	plant(envCmdKey, envCmdPriv)
	sentinel := filepath.Join(td, "SOPS_AGE_KEY_CMD-ran")
	envCmd := filepath.Join(td, "planted", "keycmd.sh")
	require.NoError(t, testbin.WriteExecutable(envCmd, []byte("#!/bin/sh\n: > '"+sentinel+"'\ncat '"+envCmdKey+"'\n"), 0700))
	sealedTo["idenvcmd"] = envCmdPub
	sealedTo["idenvssh"] = sshKey(t, filepath.Join(td, "planted", "envssh"), "ed25519")

	var rules []string
	rules = append(rules, fmt.Sprintf("  - path_regex: ^rowan\\.yaml$\n    age: %s,%s", keyA.pubKey, recKey.pubKey))
	for seat, recipient := range sealedTo {
		// The rule names a seat key of its own (exec checks the rule's shape, never
		// whose key opens the file); the file is sealed to the planted identity.
		ruleKey := genKey(t, td, "rule-"+seat)
		rules = append(rules, fmt.Sprintf("  - path_regex: ^%s\\.yaml$\n    age: %s,%s", seat, ruleKey.pubKey, recKey.pubKey))
		sealFileWithSops(t, sopsPath, filepath.Join(storeDir, seat+".yaml"), []string{recipient, recKey.pubKey}, "GH_TOKEN: ghp_foreign_"+seat+"\n")
	}
	_ = os.WriteFile(filepath.Join(storeDir, ".sops.yaml"), []byte("creation_rules:\n"+strings.Join(rules, "\n")+"\n"), 0644)
	sealFileWithSops(t, sopsPath, filepath.Join(storeDir, "rowan.yaml"), []string{keyA.pubKey, recKey.pubKey}, "GH_TOKEN: ghp_mine\n")
	commitAndPush(t, storeDir)

	lookupVars := map[string]string{
		"SOPS_AGE_KEY":                  strings.TrimSpace(string(envKeyPriv)),
		"SOPS_AGE_KEY_FILE":             envFilePath,
		"SOPS_AGE_KEY_CMD":              envCmd,
		"SOPS_AGE_SSH_PRIVATE_KEY_FILE": filepath.Join(td, "planted", "envssh"),
		"SOPS_KEYSERVICE":               "unix://" + filepath.Join(td, "no-keyservice.sock"),
	}
	baseEnv := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home}
	if tmp := os.Getenv("TMPDIR"); tmp != "" {
		baseEnv = append(baseEnv, "TMPDIR="+tmp)
	}
	for k, v := range lookupVars {
		baseEnv = append(baseEnv, k+"="+v)
	}
	// Two callers: one with XDG_CONFIG_HOME set, and one without it, whose sops would
	// fall back to the platform config dir under HOME.
	callers := map[string][]string{
		"xdg-set":   append(append([]string{}, baseEnv...), "XDG_CONFIG_HOME="+xdg),
		"xdg-unset": baseEnv,
	}
	for callerName, env := range callers {
		for seat := range sealedTo {
			out, errOut, code, _ := runWithEnv(bin, env, "exec", "--store", storeDir, "--as", seat, "--key", keyA.privPath,
				"--sops", sopsPath, "--only", "all", "--", "env")
			assert.Equal(t, 125, code, "caller %s: %s.yaml is sealed only to a planted identity and must be refused as a failed decrypt (125), got %d: %s", callerName, seat, code, errOut)
			assert.Contains(t, errOut, "sops failed", "caller %s: %s.yaml is sealed only to a planted identity and must be refused as a failed decrypt (125), got %d: %s", callerName, seat, code, errOut)
			assert.NotContains(t, out+errOut, "ghp_foreign_", "caller %s: a planted identity opened %s.yaml: %s%s", callerName, seat, out, errOut)
		}

		out, errOut, code, _ := runWithEnv(bin, env, "exec", "--store", storeDir, "--as", "rowan", "--key", keyA.privPath,
			"--sops", sopsPath, "--only", "all", "--", "env")
		assert.Equal(t, 0, code, "caller %s: --key alone must open the seat's own file with every planted identity present, got %d: %s", callerName, code, errOut)
		assert.Contains(t, out, "GH_TOKEN=ghp_mine\n", "caller %s: --key alone must open the seat's own file with every planted identity present, got %d: %s", callerName, code, errOut)
		for name := range lookupVars {
			for _, line := range strings.Split(out, "\n") {
				assert.False(t, strings.HasPrefix(line, name+"="), "caller %s: %s reached the command: %s", callerName, name, line)
			}
		}
	}
	_, err := os.Stat(sentinel)
	assert.Error(t, err, "SOPS_AGE_KEY_CMD was run: %s exists", sentinel)
}

// Test 3: TestExecReplacesItselfAndPassesTheStatusThrough
func TestExecReplacesItselfAndPassesTheStatusThrough(t *testing.T) {
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

	// Command exiting 7 makes nova-secrets exit 7
	cmd := exec.Command(bin, "exec", "--store", storeDir, "--as", "rowan", "--key", keyA.privPath, "--sops", sopsPath,
		"--only", "GH_TOKEN", "--", "sh", "-c", "exit 7")
	err := cmd.Run()
	{
		exitErr, ok := err.(*exec.ExitError)
		if assert.True(t, ok, "expected exit code 7, got %v", err) {
			assert.Equal(t, 7, exitErr.ExitCode(), "expected exit code 7, got %v", err)
		}
	}

	// Command exiting 1 and 2
	for _, expectedCode := range []int{1, 2} {
		var stderr bytes.Buffer
		cmd = exec.Command(bin, "exec", "--store", storeDir, "--as", "rowan", "--key", keyA.privPath, "--sops", sopsPath,
			"--only", "GH_TOKEN", "--", "sh", "-c", fmt.Sprintf("exit %d", expectedCode))
		cmd.Stderr = &stderr
		err = cmd.Run()
		exitErr, ok := err.(*exec.ExitError)
		if assert.True(t, ok, "expected exit code %d, got %v", expectedCode, err) {
			assert.Equal(t, expectedCode, exitErr.ExitCode(), "expected exit code %d, got %v", expectedCode, err)
		}
		assert.NotContains(t, stderr.String(), "SECRETS EXEC REFUSED", "unexpected SECRETS EXEC REFUSED on command exit %d: %s", expectedCode, stderr.String())
	}

	// Command exiting 125 passed through, told from refusal by absence of our line
	var stderr bytes.Buffer
	cmd = exec.Command(bin, "exec", "--store", storeDir, "--as", "rowan", "--key", keyA.privPath, "--sops", sopsPath,
		"--only", "GH_TOKEN", "--", "sh", "-c", "exit 125")
	cmd.Stderr = &stderr
	err = cmd.Run()
	{
		exitErr, ok := err.(*exec.ExitError)
		if assert.True(t, ok, "expected exit code 125, got %v", err) {
			assert.Equal(t, 125, exitErr.ExitCode(), "expected exit code 125, got %v", err)
		}
	}
	assert.NotContains(t, stderr.String(), "SECRETS EXEC REFUSED", "found SECRETS EXEC REFUSED when command exited 125: %s", stderr.String())

	if runtime.GOOS == "windows" {
		t.Log("windows has no execve: the pid, RLIMIT_CORE and signal clauses do not apply there")
		return
	}
	env := os.Environ()

	// Same pid before and after: the command IS the process this test started.
	out, errOut, code, pid := runWithEnv(bin, env, "exec", "--store", storeDir, "--as", "rowan", "--key", keyA.privPath,
		"--sops", sopsPath, "--only", "GH_TOKEN", "--", "sh", "-c", "echo $$")
	assert.Equal(t, 0, code, "exec must replace itself: started pid %d, the command reported %q (exit %d): %s", pid, strings.TrimSpace(out), code, errOut)
	assert.Equal(t, fmt.Sprint(pid), strings.TrimSpace(out), "exec must replace itself: started pid %d, the command reported %q (exit %d): %s", pid, strings.TrimSpace(out), code, errOut)

	// A signal-killed command reproduces the shell's status: the process itself dies of
	// the signal, because there is no wrapper left to turn it into an exit code.
	stderr.Reset()
	cmd = exec.Command(bin, "exec", "--store", storeDir, "--as", "rowan", "--key", keyA.privPath, "--sops", sopsPath,
		"--only", "GH_TOKEN", "--", "sh", "-c", "kill -TERM $$")
	cmd.Stderr = &stderr
	err = cmd.Run()
	exitErr, ok := err.(*exec.ExitError)
	require.True(t, ok, "a signal-killed command must not look like success, got %v", err)
	ws, ok := exitErr.Sys().(syscall.WaitStatus)
	if assert.True(t, ok, "the command killed by SIGTERM must leave the tool killed by SIGTERM (shell status 143), got %v", err) &&
		assert.True(t, ws.Signaled(), "the command killed by SIGTERM must leave the tool killed by SIGTERM (shell status 143), got %v", err) {
		assert.Equal(t, syscall.SIGTERM, ws.Signal(), "the command killed by SIGTERM must leave the tool killed by SIGTERM (shell status 143), got %v", err)
	}
	assert.NotContains(t, stderr.String(), "SECRETS EXEC REFUSED", "a signal-killed command printed our failure line: %s", stderr.String())

	// The command's RLIMIT_CORE is 0 even when the caller's soft limit is not: a core
	// dump would write every value to disk.
	wrapped := []string{"-c", `ulimit -c 1024 || exit 97; exec "$0" "$@"`, bin, "exec", "--store", storeDir, "--as", "rowan",
		"--key", keyA.privPath, "--sops", sopsPath, "--only", "GH_TOKEN", "--", "sh", "-c", "ulimit -c"}
	var wOut, wErr bytes.Buffer
	cmd = exec.Command("sh", wrapped...)
	cmd.Stdout = &wOut
	cmd.Stderr = &wErr
	err = cmd.Run()
	if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 97 {
		t.Skip("the caller's hard core limit is 0, so a nonzero soft limit cannot be planted; RLIMIT_CORE clause not provable here")
	}
	assert.NoError(t, err, "the command's RLIMIT_CORE must be 0 with the caller's at 1024, got %q (%v): %s", strings.TrimSpace(wOut.String()), err, wErr.String())
	assert.Equal(t, "0", strings.TrimSpace(wOut.String()), "the command's RLIMIT_CORE must be 0 with the caller's at 1024, got %q (%v): %s", strings.TrimSpace(wOut.String()), err, wErr.String())
}

// Test 4: TestNoVerbPrintsAValue
func TestNoVerbPrintsAValue(t *testing.T) {
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

	secretVal := "distinctive_secret_val_40_bytes_long_here"
	sealFileWithSops(t, sopsPath, filepath.Join(storeDir, "rowan.yaml"), []string{keyA.pubKey, recKey.pubKey}, "GH_TOKEN: "+secretVal+"\n")
	commitAndPush(t, storeDir)

	// A key file this verb must refuse (mode 0644), a key file that already exists for
	// keygen to refuse, and an empty receipts directory for placed.
	looseKey := filepath.Join(td, "loose.key")
	keyBytes, err := os.ReadFile(keyA.privPath)
	require.NoError(t, err)
	_ = os.WriteFile(looseKey, keyBytes, 0644)
	_ = os.Chmod(looseKey, 0644)
	receipts := filepath.Join(td, "receipts")
	_ = os.MkdirAll(receipts, 0700)
	ageKeygen := findAgeKeygen(t)

	store := []string{"--store", storeDir, "--as", "rowan"}
	withKey := append(append([]string{}, store...), "--key", keyA.privPath, "--sops", sopsPath)
	cat := func(parts ...[]string) []string {
		var all []string
		for _, p := range parts {
			all = append(all, p...)
		}
		return all
	}
	verbs := [][]string{
		// green, every mode
		cat([]string{"names"}, store),
		cat([]string{"names"}, store, []string{"--max", "0"}),
		cat([]string{"names"}, store, []string{"--max", "1"}),
		cat([]string{"check"}, withKey),
		cat([]string{"check"}, withKey, []string{"--max", "0"}),
		cat([]string{"exec"}, withKey, []string{"--only", "GH_TOKEN", "--require", "GH_TOKEN", "--", "true"}),
		cat([]string{"exec"}, withKey, []string{"--only", "all", "--", "true"}),
		{"gate", "--store", storeDir, "--base", "HEAD", "--head", "HEAD"},
		{"placed", "--machine", "mini", "--receipts", receipts},
		{"version"},
		{"help"},
		// every refusal
		cat([]string{"exec"}, withKey, []string{"--", "true"}),
		cat([]string{"exec"}, withKey, []string{"--only", "NOPE", "--", "true"}),
		cat([]string{"exec"}, withKey, []string{"--only", "all", "--require", "NOPE", "--", "true"}),
		cat([]string{"exec"}, withKey, []string{"--only", "GH_TOKEN", "--", "no-such-command-anywhere"}),
		cat([]string{"exec"}, store, []string{"--key", looseKey, "--sops", sopsPath, "--only", "all", "--", "true"}),
		cat([]string{"exec"}, withKey, []string{"--only", "all", "true"}),
		cat([]string{"check"}, store, []string{"--key", looseKey, "--sops", sopsPath}),
		cat([]string{"check"}, withKey, []string{"--max", "-1"}),
		cat([]string{"names"}, store, []string{"--bogus"}),
		{"keygen", "--as", "rowan", "--key", keyA.privPath, "--age-keygen", ageKeygen},
		cat([]string{"place"}, withKey, []string{"--secret", "GH_TOKEN"}),
		cat([]string{"seat", "add"}, withKey),
		cat([]string{"seat", "inject"}, withKey),
		{"get", "--store", storeDir, "--as", "rowan", "GH_TOKEN"},
		{"print", "GH_TOKEN"}, {"show", "GH_TOKEN"}, {"cat", "GH_TOKEN"}, {"put", "GH_TOKEN"},
		{"no-such-verb", "GH_TOKEN"},
		{},
	}

	// The value's length is a fact about the value: a mutation printing len(value)
	// must turn this red as surely as printing the value.
	lengthToken := regexp.MustCompile(`(^|[^0-9A-Za-z_])` + fmt.Sprint(len(secretVal)) + `([^0-9A-Za-z_]|$)`)
	const green = 11 // the first eleven are green runs; every one after is a refusal
	for i, v := range verbs {
		out, errOut, code := runNovaSecrets(bin, v...)
		assert.Equal(t, code == 0, i < green, "verb %v: exit %d is not the mode this row pins (green=%t): %s%s", v, code, i < green, out, errOut)
		both := strings.ReplaceAll(out+errOut, td, "<td>")
		assert.NotContains(t, both, secretVal, "verb %v printed the value: %s", v, both)
		assert.False(t, lengthToken.MatchString(both), "verb %v printed the value's length %d: %s", v, len(secretVal), both)
	}
}
