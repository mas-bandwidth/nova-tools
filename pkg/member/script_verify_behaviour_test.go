package member

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/cardhdr"
	"github.com/mas-bandwidth/nova-tools/pkg/testbin"
)

// gitNoConfig is a git that ignores the bench's global and system config, so Verify's own
// git calls (which do not set GIT_CONFIG_GLOBAL or GIT_CONFIG_SYSTEM; gitIn does) cannot
// reach it. t.Setenv would isolate them too but cannot open a t.Parallel test, and the
// serial allowlist is full, so the isolation is a per-test wrapper instead.
func gitNoConfig(t *testing.T) string {
	t.Helper()
	real, err := exec.LookPath("git")
	require.NoError(t, err)
	p := filepath.Join(t.TempDir(), "git")
	script := fmt.Sprintf("#!/bin/sh\nexport GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_SYSTEM=/dev/null\nexec %q \"$@\"\n", real)
	require.NoError(t, testbin.WriteExecutable(p, []byte(script), 0o755))
	return p
}

const noHeadBad = "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef"

// Verify refuses a class with no program and a packet with no head before any git runs
// (member.go:2421-2424).
func TestVerifyRefusesAClassWithNoProgramAndAPacketWithNoHead(t *testing.T) {
	t.Parallel()
	repo := newScriptRepo(t)
	v := ScriptVerifier{Mirror: repo.dir, Temp: t.TempDir(), Git: gitNoConfig(t), Run: program}
	ok, why := v.Verify(Packet{Card: "r1", Kind: "read", Head: repo.start, BaseHead: repo.start}, cardhdr.Class{})
	assert.False(t, ok)
	assert.Contains(t, why, "the card names no program")
	ok, why = v.Verify(Packet{Card: "r1", Kind: "read", BaseHead: repo.start, Brief: scriptBrief}, mustClass(t, scriptBrief))
	assert.False(t, ok)
	assert.Contains(t, why, "the read names no head")
}

// A head the mirror does not hold is not ok, with git's own error; a BaseHead the mirror
// does not hold is not ok and Run is never called (member.go:2451-2454).
func TestVerifyRefusesAHeadAndABaseHeadAbsentFromTheMirror(t *testing.T) {
	t.Parallel()
	repo := newScriptRepo(t)
	head := repo.head(t, "made", "HELLO\nWORLD\n", nil)
	ran := false
	v := ScriptVerifier{Mirror: repo.dir, Temp: t.TempDir(), Git: gitNoConfig(t), Run: func(context.Context, string, []string) error {
		ran = true
		return nil
	}}
	ok, why := v.Verify(Packet{Card: "r1", Kind: "read", Head: noHeadBad, BaseHead: repo.start, Brief: scriptBrief}, mustClass(t, scriptBrief))
	assert.False(t, ok)
	assert.Contains(t, why, "git diff")
	assert.False(t, ran, "a head absent from the mirror stops before the program runs")

	ran = false
	ok, why = v.Verify(Packet{Card: "r1", Kind: "read", Head: head, BaseHead: noHeadBad, Brief: scriptBrief}, mustClass(t, scriptBrief))
	assert.False(t, ok)
	assert.Contains(t, why, "git diff")
	assert.False(t, ran, "a BaseHead absent from the mirror stops before the program runs")
}

// With no BaseHead and a WorkBase that resolves only as origin/<b>, the merge base with
// origin/<b> is the start and the read passes (member.go:2437-2447).
func TestVerifyFindsTheStartInOriginWorkBaseWhenNoBaseHeadIsNamed(t *testing.T) {
	t.Parallel()
	repo := newScriptRepo(t)
	head := repo.head(t, "made", "HELLO\nWORLD\n", nil)
	gitIn(t, repo.dir, "update-ref", "refs/remotes/origin/wb", repo.start)
	v := ScriptVerifier{Mirror: repo.dir, Temp: t.TempDir(), Git: gitNoConfig(t), Run: program}
	ok, why := v.Verify(Packet{Card: "r1", Kind: "read", Head: head, WorkBase: "wb", Brief: scriptBrief}, mustClass(t, scriptBrief))
	assert.True(t, ok, why)
}

// Git set to a missing binary is not ok, with git's error, and no script-read-* directory
// is left under Temp (member.go:2429-2434).
func TestVerifyRefusesAMissingGitAndLeavesNoCheckout(t *testing.T) {
	t.Parallel()
	repo := newScriptRepo(t)
	head := repo.head(t, "made", "HELLO\nWORLD\n", nil)
	temp := t.TempDir()
	v := ScriptVerifier{Mirror: repo.dir, Temp: temp, Git: filepath.Join(t.TempDir(), "no-such-git"), Run: program}
	ok, why := v.Verify(Packet{Card: "r1", Kind: "read", Head: head, BaseHead: repo.start, Brief: scriptBrief}, mustClass(t, scriptBrief))
	assert.False(t, ok)
	assert.Contains(t, why, "git diff")
	entries, err := os.ReadDir(temp)
	require.NoError(t, err)
	assert.Empty(t, entries, "no checkout is left under Temp")
}

// Run receives the program's argv split as the class's Script and a directory under Temp;
// an ok why names the short start, the short head and the byte count, and Temp is empty
// after (member.go:2419, 2458, 2489).
func TestVerifyRunsTheProgramArgvInTempAndNamesTheComparedShortShas(t *testing.T) {
	t.Parallel()
	repo := newScriptRepo(t)
	head := repo.head(t, "made", "HELLO\nWORLD\n", nil)
	temp := t.TempDir()
	var gotDir string
	var gotArgv []string
	v := ScriptVerifier{Mirror: repo.dir, Temp: temp, Git: gitNoConfig(t), Run: func(ctx context.Context, dir string, argv []string) error {
		gotDir, gotArgv = dir, append([]string(nil), argv...)
		return program(ctx, dir, argv)
	}}
	brief := "STATUS: x\n\nCLASS: script\nSCRIPT: upcase --loud now\nDEADLINE: finish within 5 minutes\n\nSTEP 1. run it.\n"
	ok, why := v.Verify(Packet{Card: "r1", Kind: "read", Head: head, BaseHead: repo.start, Brief: brief}, mustClass(t, brief))
	require.True(t, ok, why)
	assert.Equal(t, []string{"upcase", "--loud", "now"}, gotArgv)
	assert.True(t, strings.HasPrefix(gotDir, temp+string(os.PathSeparator)), "the program runs under Temp: %s", gotDir)
	assert.Contains(t, why, short(repo.start))
	assert.Contains(t, why, short(head))
	assert.Contains(t, why, " bytes")
	entries, err := os.ReadDir(temp)
	require.NoError(t, err)
	assert.Empty(t, entries, "Temp is empty after the read")
}
