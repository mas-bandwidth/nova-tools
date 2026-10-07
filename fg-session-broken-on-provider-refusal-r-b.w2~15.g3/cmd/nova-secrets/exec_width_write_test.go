package main

// exec refuses to be the vehicle for a hand-write of the sprint table's working column,
// friend:<name>:width in the fleet's Redis: a redis-cli line that writes that key is
// refused before the command starts, naming the column's one writer (the friend row
// loop), and a redis-cli line that reads it, or writes another key, still runs. sops and
// redis-cli are fakes on disk, so no test opens a real store or touches a real Redis.

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExecRefusesAHandWriteOfTheSprintWidthKey(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("the fake sops and redis-cli run through /bin/sh; the fleet is a linux bench")
	}
	bin := buildNovaSecrets(t)

	td := t.TempDir()
	storeDir := filepath.Join(td, "store")
	require.NoError(t, os.MkdirAll(storeDir, 0o755))
	initGitStore(t, storeDir)

	// Two age public keys the shape checker accepts (62 characters, age1 plus
	// the bech32 alphabet it admits), exactly two recipients for the one rule:
	// emma's seat key and the recovery key. The fake sops never opens anything,
	// so neither needs to be a real X25519 key.
	seatPub := "age1" + strings.Repeat("a", 57) + "a"
	recPub := "age1" + strings.Repeat("a", 57) + "c"
	require.NoError(t, os.WriteFile(filepath.Join(storeDir, "recovery.pub"), []byte(recPub+"\n"), 0o644))
	sopsCfg := fmt.Sprintf("creation_rules:\n  - path_regex: ^emma\\.yaml$\n    age: %s,%s\n", seatPub, recPub)
	require.NoError(t, os.WriteFile(filepath.Join(storeDir, ".sops.yaml"), []byte(sopsCfg), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(storeDir, "emma.yaml"), []byte("REDISCLI_AUTH: ENC[AES256_GCM,data:fake]\n"), 0o644))
	commitAndPush(t, storeDir)

	keyDir := filepath.Join(td, "keys")
	require.NoError(t, os.MkdirAll(keyDir, 0o700))
	keyPath := filepath.Join(keyDir, "emma.key")
	require.NoError(t, os.WriteFile(keyPath, []byte("AGE-SECRET-KEY-1FAKE\n"), 0o600))

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
	require.Equal(t, 0, code, "a redis-cli line writing another key still runs: exit=%d stderr=%s", code, errOut)
	require.Contains(t, errOut, "SECRETS EXEC OK", "the benign leg is exec running the command, not a crash; stderr=%s", errOut)

	// THE LINE OF THE ISSUE: redis-cli SET friend:emma:width 3 through
	// nova-secrets exec. On base it runs -- the working column written by hand
	// through this tool, which is the defect; the fix refuses it before the
	// command starts and names the friend's own tool.
	_, errOut, code = runNovaSecrets(bin, execArgs(fakeRedis, "SET", "friend:emma:width", "3")...)
	require.Equal(t, 125, code, "the redis-cli line writing friend:emma:width must be refused at 125 before the command starts, got %d; stderr: %s", code, errOut)
	lines := strings.Split(strings.TrimSpace(errOut), "\n")
	require.Len(t, lines, 1, "the refusal is one SECRETS EXEC REFUSED line, got %d lines:\n%s", len(lines), errOut)
	require.True(t, strings.HasPrefix(lines[0], "SECRETS EXEC REFUSED"), "the refusal is one SECRETS EXEC REFUSED line, got %d lines:\n%s", len(lines), errOut)
	// Since #3447 the working column is the friend row's, written by the row
	// loop; no beat writes the row (the retired nova-wake beat refused it),
	// so the refusal names the row loop and never a beat (nova-tools #3807).
	for _, want := range []string{"friend:emma:width", "friend row loop", "#3447"} {
		assert.Contains(t, lines[0], want, "the refusal must name %s (the friend row loop, nova-tools#3807):\n%s", want, lines[0])
	}
	assert.NotContains(t, lines[0], "nova-sprint", "the refusal must name no parked tool and no tool outside nova-tools:\n%s", lines[0])
	assert.NotContains(t, lines[0], "rowan-tools", "the refusal must name no parked tool and no tool outside nova-tools:\n%s", lines[0])
	assert.NotContains(t, lines[0], "nova-wake beat", "the refusal must not send anyone to nova-wake beat, a retired verb that refused the friend row since #3447 (nova-tools#3807):\n%s", lines[0])

	// A read of the same key still runs: the table's working column reads
	// exactly that key, and only its write moved to the beat.
	_, errOut, code = runNovaSecrets(bin, execArgs(fakeRedis, "GET", "friend:emma:width")...)
	require.Equal(t, 0, code, "a redis-cli GET of friend:emma:width still runs: exit=%d stderr=%s", code, errOut)

	raw, err := os.ReadFile(witness)
	require.NoError(t, err)
	got := string(raw)
	assert.NotContains(t, got, "SET\tfriend:emma:width", "the refused redis-cli line ran anyway; witness:\n%s", got)
	for _, want := range []string{"SET\tother:key", "GET\tfriend:emma:width"} {
		assert.Contains(t, got, want, "the running legs must be recorded in the witness; want %q in:\n%s", want, got)
	}
}
