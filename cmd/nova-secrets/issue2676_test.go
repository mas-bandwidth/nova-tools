package main

// Red test for nova-tools#2676: the sprint table's friend working column,
// friend:<name>:width in the fleet's Redis, was written by hand with redis-cli
// through nova-secrets exec (Emma hers; Rowan wrote rowan=8 and johnny=6 from
// statements; Johnny's own attempt hung without the seat exec). The fix's
// nova-secrets leg: exec refuses to be the vehicle for that hand-write and
// names the friend's own tool on the refusal line, so the column is never a
// redis-cli line through this exec. The beat verb itself is nova-wake's
// (#2673); the table's working column reading only that key is the table's
// leg. sops and redis-cli are fakes on disk, so no test opens a real store or
// touches a real Redis.

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestIssue2676(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("the fake sops and redis-cli run through /bin/sh; the fleet is a linux bench")
	}
	bin := buildNovaSecrets(t)

	td := t.TempDir()
	storeDir := filepath.Join(td, "store")
	if err := os.MkdirAll(storeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	initGitStore(t, storeDir)

	// Two age public keys the shape checker accepts (62 characters, age1 plus
	// the bech32 alphabet it admits), exactly two recipients for the one rule:
	// emma's seat key and the recovery key. The fake sops never opens anything,
	// so neither needs to be a real X25519 key.
	seatPub := "age1" + strings.Repeat("a", 57) + "a"
	recPub := "age1" + strings.Repeat("a", 57) + "c"
	if err := os.WriteFile(filepath.Join(storeDir, "recovery.pub"), []byte(recPub+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sopsCfg := fmt.Sprintf("creation_rules:\n  - path_regex: ^emma\\.yaml$\n    age: %s,%s\n", seatPub, recPub)
	if err := os.WriteFile(filepath.Join(storeDir, ".sops.yaml"), []byte(sopsCfg), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(storeDir, "emma.yaml"), []byte("REDISCLI_AUTH: ENC[AES256_GCM,data:fake]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	commitAndPush(t, storeDir)

	keyDir := filepath.Join(td, "keys")
	if err := os.MkdirAll(keyDir, 0o700); err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(keyDir, "emma.key")
	if err := os.WriteFile(keyPath, []byte("AGE-SECRET-KEY-1FAKE\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// The fake sops answers the two calls exec makes -- the version probe and
	// the decrypt -- and exits 1 on anything else, as the real one refuses a
	// shape it does not know.
	sopsPath := filepath.Join(td, "fake-sops")
	writeFakeExe(t, sopsPath, "#!/bin/sh\n"+
		"case \"$1\" in\n"+
		"  --version) echo 'sops 3.13.3'; exit 0 ;;\n"+
		"  -d) printf 'REDISCLI_AUTH: redis-auth-emma\\n'; exit 0 ;;\n"+
		"  *) exit 1 ;;\n"+
		"esac\n")

	// The fake redis-cli records the line it was handed; no test reaches a
	// Redis. Its name is the name the manual write used, so what exec refuses
	// is the command shape, not a path.
	witness := filepath.Join(td, "redis-cli.lines")
	fakeRedis := filepath.Join(td, "redis-cli")
	writeFakeExe(t, fakeRedis, "#!/bin/sh\n"+
		"printf '%s\\t%s\\n' \"$1\" \"$2\" >> "+witness+"\n"+
		"exit 0\n")

	execArgs := func(cmdAndArgs ...string) []string {
		return append([]string{
			"exec", "--store", storeDir, "--as", "emma", "--key", keyPath, "--sops", sopsPath,
			"--only", "REDISCLI_AUTH", "--require", "REDISCLI_AUTH", "--",
		}, cmdAndArgs...)
	}

	// The benign leg first, so a broken fixture fails as a fixture and not as
	// the issue: a redis-cli SET of another key still runs through exec.
	_, errOut, code := runNovaSecrets(bin, execArgs(fakeRedis, "SET", "other:key", "1")...)
	if code != 0 {
		t.Fatalf("a redis-cli line writing another key still runs: exit=%d stderr=%s", code, errOut)
	}
	if !strings.Contains(errOut, "SECRETS EXEC OK") {
		t.Fatalf("the benign leg is exec running the command, not a crash; stderr=%s", errOut)
	}

	// THE LINE OF THE ISSUE: redis-cli SET friend:emma:width 3 through
	// nova-secrets exec. On base it runs -- the working column written by hand
	// through this tool, which is the defect; the fix refuses it before the
	// command starts and names the friend's own tool.
	_, errOut, code = runNovaSecrets(bin, execArgs(fakeRedis, "SET", "friend:emma:width", "3")...)
	if code != 125 {
		t.Fatalf("the redis-cli line writing friend:emma:width must be refused at 125 before the command starts, got %d; stderr: %s", code, errOut)
	}
	lines := strings.Split(strings.TrimSpace(errOut), "\n")
	if len(lines) != 1 || !strings.HasPrefix(lines[0], "SECRETS EXEC FAIL") {
		t.Fatalf("the refusal is one SECRETS EXEC FAIL line, got %d lines:\n%s", len(lines), errOut)
	}
	for _, want := range []string{"friend:emma:width", "nova-wake beat --width", "#2673"} {
		if !strings.Contains(lines[0], want) {
			t.Errorf("the refusal must name %s (the friend's own tool, nova-tools#2673):\n%s", want, lines[0])
		}
	}

	// A read of the same key still runs: the table's working column reads
	// exactly that key, and only its write moved to the beat.
	_, errOut, code = runNovaSecrets(bin, execArgs(fakeRedis, "GET", "friend:emma:width")...)
	if code != 0 {
		t.Fatalf("a redis-cli GET of friend:emma:width still runs: exit=%d stderr=%s", code, errOut)
	}

	raw, err := os.ReadFile(witness)
	if err != nil {
		t.Fatal(err)
	}
	got := string(raw)
	if strings.Contains(got, "SET\tfriend:emma:width") {
		t.Errorf("the refused redis-cli line ran anyway; witness:\n%s", got)
	}
	for _, want := range []string{"SET\tother:key", "GET\tfriend:emma:width"} {
		if !strings.Contains(got, want) {
			t.Errorf("the running legs must be recorded in the witness; want %q in:\n%s", want, got)
		}
	}
}
