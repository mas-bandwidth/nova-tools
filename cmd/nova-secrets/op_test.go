package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func runNovaSecretsStdin(bin string, stdin string, args ...string) (string, string, int) {
	var outBuf, errBuf bytes.Buffer
	cmd := exec.Command(bin, args...)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	err := cmd.Run()
	code := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			code = exitErr.ExitCode()
		} else {
			code = 1
			errBuf.WriteString("nova-secrets did not run: " + err.Error() + "\n")
		}
	}
	return outBuf.String(), errBuf.String(), code
}

func TestPlaceOpIdempotentRetry(t *testing.T) {
	t.Parallel()
	f := newPlaceFixture(t)

	args := append(f.placeArgs("mini", "DEEPSEEK_API_KEY", f.remotePath), "--op", "op-place-1")
	stdout1, stderr1, code1 := runNovaSecrets(f.bin, args...)
	if code1 != 0 {
		t.Fatalf("first place exit=%d stdout=%q stderr=%q", code1, stdout1, stderr1)
	}
	if !strings.HasPrefix(stdout1, "SECRETS PLACE OK ") {
		t.Fatalf("unexpected stdout on first place: %q", stdout1)
	}

	// Verify operation record was written under <store>/.ops/op-place-1.json
	opFile := filepath.Join(f.store, ".ops", "op-place-1.json")
	if _, err := os.Stat(opFile); err != nil {
		t.Fatalf("op record file missing at %s: %v", opFile, err)
	}

	// Clear ssh recorded calls
	if err := os.Remove(f.sshArgsFile); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if err := os.Remove(f.sshStdinFile); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}

	receiptBefore, err := os.ReadFile(filepath.Join(f.receipts, "mini.receipt"))
	if err != nil {
		t.Fatal(err)
	}

	// Second run with the same --op op-place-1: must replay identically and run no side effects.
	stdout2, stderr2, code2 := runNovaSecrets(f.bin, args...)
	if code2 != 0 {
		t.Fatalf("second place exit=%d stdout=%q stderr=%q", code2, stdout2, stderr2)
	}
	if stdout2 != stdout1 {
		t.Fatalf("stdout mismatch on idempotent replay:\n first: %q\nsecond: %q", stdout1, stdout2)
	}
	if stderr2 != "" {
		t.Fatalf("second place wrote to stderr: %q", stderr2)
	}

	// Fake ssh was NOT run
	if _, err := os.Stat(f.sshArgsFile); err == nil {
		t.Fatalf("ssh was called during idempotent replay")
	}
	if _, err := os.Stat(f.sshStdinFile); err == nil {
		t.Fatalf("ssh stdin was written during idempotent replay")
	}

	// Receipts were not modified
	receiptAfter, err := os.ReadFile(filepath.Join(f.receipts, "mini.receipt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(receiptBefore) != string(receiptAfter) {
		t.Fatalf("receipt changed during replay:\nbefore: %s\nafter: %s", string(receiptBefore), string(receiptAfter))
	}
}

type sealTestFixture struct {
	dir      string
	storeDir string
	keyPath  string
	sopsPath string
	decOut   string
}

func newSealTestFixture(t *testing.T) sealTestFixture {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell script fakes")
	}
	td := t.TempDir()
	storeDir := filepath.Join(td, "store")
	if err := os.MkdirAll(storeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	initGitStore(t, storeDir)

	sopsConfig := "creation_rules:\n  - path_regex: ^worker\\.yaml$\n    age: age1abc\n"
	if err := os.WriteFile(filepath.Join(storeDir, ".sops.yaml"), []byte(sopsConfig), 0o644); err != nil {
		t.Fatal(err)
	}
	initialWorkerYaml := "API_KEY: ENC[OLD]\nsops:\n    age:\n        - recipient: age1abc\n"
	if err := os.WriteFile(filepath.Join(storeDir, "worker.yaml"), []byte(initialWorkerYaml), 0o644); err != nil {
		t.Fatal(err)
	}
	commitAndPush(t, storeDir)

	keyDir := filepath.Join(td, "keys")
	if err := os.MkdirAll(keyDir, 0o700); err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(keyDir, "worker.key")
	if err := os.WriteFile(keyPath, []byte("AGE-SECRET-KEY-FAKE\n# public key: age1abc\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	decOut := filepath.Join(td, "decrypt.out")
	if err := os.WriteFile(decOut, []byte("API_KEY: old_value\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	sopsPath := filepath.Join(td, "fake-sops")
	sopsBody := "#!/bin/sh\n" +
		"if [ \"$1\" = \"--version\" ]; then echo 'sops 3.13.3'; exit 0; fi\n" +
		"case \"$*\" in\n" +
		"  *\"-d\"*) cat \"" + decOut + "\"; exit 0 ;;\n" +
		"esac\n" +
		"cat > /dev/null\n" +
		"echo 'API_KEY: ENC[NEW]'\n" +
		"echo 'sops:'\n" +
		"echo '    age:'\n" +
		"echo '        - recipient: age1abc'\n" +
		"exit 0\n"
	writeFakeExe(t, sopsPath, sopsBody)

	return sealTestFixture{
		dir:      td,
		storeDir: storeDir,
		keyPath:  keyPath,
		sopsPath: sopsPath,
		decOut:   decOut,
	}
}

func TestSealOpIdempotentRetry(t *testing.T) {
	t.Parallel()
	bin := buildNovaSecrets(t)
	f := newSealTestFixture(t)

	args := []string{
		"seal",
		"--store", f.storeDir,
		"--as", "worker",
		"--key", f.keyPath,
		"--sops", f.sopsPath,
		"--name", "API_KEY",
		"--no-pr",
		"--stdin",
		"--op", "op-seal-1",
	}

	stdout1, stderr1, code1 := runNovaSecretsStdin(bin, "super-secret-123\n", args...)
	if code1 != 0 {
		t.Fatalf("first seal exit=%d stdout=%q stderr=%q", code1, stdout1, stderr1)
	}
	if !strings.HasPrefix(stdout1, "SECRETS SEAL OK ") {
		t.Fatalf("unexpected stdout on first seal: %q", stdout1)
	}

	// Verify operation record exists
	opPath := filepath.Join(f.storeDir, ".ops", "op-seal-1.json")
	if _, err := os.Stat(opPath); err != nil {
		t.Fatalf("expected op record at %s: %v", opPath, err)
	}

	// Snapshot all files in storeDir before replay
	before := treeBytes(t, f.storeDir)

	// Second run with the same --op op-seal-1: returns identical output, exits 0, and modifies no files.
	stdout2, stderr2, code2 := runNovaSecretsStdin(bin, "different-value-ignored\n", args...)
	if code2 != 0 {
		t.Fatalf("second seal exit=%d stdout=%q stderr=%q", code2, stdout2, stderr2)
	}
	if stdout2 != stdout1 {
		t.Fatalf("stdout mismatch on seal replay:\n first: %q\nsecond: %q", stdout1, stdout2)
	}
	if stderr2 != "" {
		t.Fatalf("second seal wrote to stderr: %q", stderr2)
	}

	// Verify that NO files were modified in the store
	after := treeBytes(t, f.storeDir)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("seal idempotent retry modified files in store:\nbefore: %v\nafter: %v", before, after)
	}
}

type seatInjectTestFixture struct {
	dir      string
	storeDir string
	keyPath  string
	sopsPath string
}

const bech32Test = "qpzry9x8gf2tvdw0s3jn54khce6mua7l"

func testAgePub(lead byte) string {
	body := bech32Test + bech32Test[:26]
	return "age1" + string(lead) + body[1:]
}

func newSeatInjectTestFixture(t *testing.T) seatInjectTestFixture {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell script fakes")
	}
	td := t.TempDir()
	storeDir := filepath.Join(td, "store")
	if err := os.MkdirAll(storeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	initGitStore(t, storeDir)

	pubRowan := testAgePub('q')
	pubAir := testAgePub('p')
	pubRecovery := testAgePub('z')

	sopsConfig := fmt.Sprintf("creation_rules:\n"+
		"  - path_regex: ^rowan\\.yaml$\n    age: %s,%s\n"+
		"  - path_regex: ^air\\.yaml$\n    age: %s,%s\n",
		pubRowan, pubRecovery, pubAir, pubRecovery)
	if err := os.WriteFile(filepath.Join(storeDir, ".sops.yaml"), []byte(sopsConfig), 0o644); err != nil {
		t.Fatal(err)
	}

	rowanYaml := fmt.Sprintf("GH_TOKEN: ENC[ROWAN_GH]\nNOVA_REDIS_BENCH_PASSWORD: ENC[ROWAN_REDIS]\nsops:\n    age:\n        - recipient: %s\n        - recipient: %s\n", pubRowan, pubRecovery)
	airYaml := fmt.Sprintf("GH_TOKEN: ENC[AIR_GH]\nsops:\n    age:\n        - recipient: %s\n        - recipient: %s\n", pubAir, pubRecovery)
	if err := os.WriteFile(filepath.Join(storeDir, "recovery.pub"), []byte(pubRecovery+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(storeDir, "rowan.yaml"), []byte(rowanYaml), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(storeDir, "air.yaml"), []byte(airYaml), 0o644); err != nil {
		t.Fatal(err)
	}
	commitAndPush(t, storeDir)

	keyDir := filepath.Join(td, "keys")
	if err := os.MkdirAll(keyDir, 0o700); err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(keyDir, "rowan.key")
	if err := os.WriteFile(keyPath, []byte("AGE-SECRET-KEY-ROWAN\n# public key: "+pubRowan+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	sopsPath := filepath.Join(td, "fake-sops")
	sopsBody := "#!/bin/sh\n" +
		"if [ \"$1\" = \"--version\" ]; then echo 'sops 3.13.3'; exit 0; fi\n" +
		"case \"$*\" in\n" +
		"  *\"-d\"*) printf 'GH_TOKEN: token_val\\nNOVA_REDIS_BENCH_PASSWORD: redis_val\\n'; exit 0 ;;\n" +
		"esac\n" +
		"cat > /dev/null\n" +
		"echo 'GH_TOKEN: ENC[NEW_GH]'\n" +
		"echo 'NOVA_REDIS_BENCH_PASSWORD: ENC[NEW_REDIS]'\n" +
		"echo 'sops:'\n" +
		"echo '    age:'\n" +
		"echo '        - recipient: " + pubAir + "'\n" +
		"echo '        - recipient: " + pubRecovery + "'\n" +
		"exit 0\n"
	writeFakeExe(t, sopsPath, sopsBody)

	return seatInjectTestFixture{
		dir:      td,
		storeDir: storeDir,
		keyPath:  keyPath,
		sopsPath: sopsPath,
	}
}

func TestSeatInjectOpIdempotentRetry(t *testing.T) {
	t.Parallel()
	bin := buildNovaSecrets(t)
	f := newSeatInjectTestFixture(t)

	args := []string{
		"seat", "inject",
		"--store", f.storeDir,
		"--as", "air",
		"--from", "rowan",
		"--only", "NOVA_REDIS_BENCH_PASSWORD",
		"--key", f.keyPath,
		"--sops", f.sopsPath,
		"--no-pr",
		"--op", "op-inject-1",
	}

	stdout1, stderr1, code1 := runNovaSecrets(bin, args...)
	if code1 != 0 {
		t.Fatalf("first seat inject exit=%d stdout=%q stderr=%q", code1, stdout1, stderr1)
	}
	if !strings.HasPrefix(stdout1, "SECRETS SEAT INJECT OK ") {
		t.Fatalf("unexpected stdout on first seat inject: %q", stdout1)
	}

	opPath := filepath.Join(f.storeDir, ".ops", "op-inject-1.json")
	if _, err := os.Stat(opPath); err != nil {
		t.Fatalf("expected op record at %s: %v", opPath, err)
	}

	before := treeBytes(t, f.storeDir)

	// Second run with the same --op op-inject-1: replays cleanly
	stdout2, stderr2, code2 := runNovaSecrets(bin, args...)
	if code2 != 0 {
		t.Fatalf("second seat inject exit=%d stdout=%q stderr=%q", code2, stdout2, stderr2)
	}
	if stdout2 != stdout1 {
		t.Fatalf("stdout mismatch on seat inject replay:\n first: %q\nsecond: %q", stdout1, stdout2)
	}
	if stderr2 != "" {
		t.Fatalf("second seat inject wrote to stderr: %q", stderr2)
	}

	after := treeBytes(t, f.storeDir)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("seat inject idempotent retry modified files in store:\nbefore: %v\nafter: %v", before, after)
	}
}

func TestOpInvalidIDRefused(t *testing.T) {
	t.Parallel()
	bin := buildNovaSecrets(t)

	invalidIDs := []string{
		"",
		"invalid/slash",
		"invalid\\backslash",
		"..",
		"has space",
		":colon",
		"-leadingdash",
	}

	verbs := [][]string{
		{"place"},
		{"seal"},
		{"seat", "inject"},
	}

	for _, v := range verbs {
		verbName := strings.Join(v, " ")
		for _, badID := range invalidIDs {
			v, badID := v, badID
			t.Run(fmt.Sprintf("%s_%q", verbName, badID), func(t *testing.T) {
				t.Parallel()
				args := append(v, "--op", badID)
				stdout, stderr, code := runNovaSecrets(bin, args...)
				if code != 2 {
					t.Errorf("%s --op %q: exit=%d, want 2; stdout=%q, stderr=%q", verbName, badID, code, stdout, stderr)
				}
				if stdout != "" {
					t.Errorf("%s --op %q: stdout should be empty, got %q", verbName, badID, stdout)
				}
				if !strings.Contains(stderr, "SECRETS REFUSED:") {
					t.Errorf("%s --op %q: stderr should contain SECRETS REFUSED:, got %q", verbName, badID, stderr)
				}
				expectedRemedy := "run: nova-secrets " + verbName + " -h"
				if !strings.Contains(stderr, expectedRemedy) {
					t.Errorf("%s --op %q: stderr should contain %q, got %q", verbName, badID, expectedRemedy, stderr)
				}
			})
		}
	}
}

func TestOpDocumentedInHelp(t *testing.T) {
	t.Parallel()
	bin := buildNovaSecrets(t)

	for _, verb := range []string{"place", "seal", "seat inject"} {
		verb := verb
		t.Run(verb, func(t *testing.T) {
			t.Parallel()
			out, stderr, code := runNovaSecrets(bin, append(strings.Fields(verb), "-h")...)
			if code != 0 || stderr != "" {
				t.Fatalf("%s -h exit=%d stderr=%q", verb, code, stderr)
			}
			if !strings.Contains(out, "--op") {
				t.Errorf("%s -h does not document --op:\n%s", verb, out)
			}
			if !strings.Contains(out, "[--op <id>]") {
				t.Errorf("%s -h does not show [--op <id>] in usage line:\n%s", verb, out)
			}
		})
	}

	out, _, code := runNovaSecrets(bin, "help")
	if code != 0 || !strings.Contains(out, "--op <id>") {
		t.Errorf("help does not document --op <id> (exit %d):\n%s", code, out)
	}
}
