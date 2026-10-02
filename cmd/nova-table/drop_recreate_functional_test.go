//go:build functional

package main

// The create a refusal prints to bring a dropped table back is run as printed,
// through a shell, against this build's nova-table and a scratch store, and the
// table comes back as it was: its saved routing (epoch key and field, member
// prefix) and the store's epoch are in the command, each word shell-quoted, so
// the paste is never refused CONFIG or STALE (USE defect 3, droppedDefinition).

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestThePrintedRecreateBringsTheTableBack(t *testing.T) {
	t.Parallel()
	addr := throwaway(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = c.Close() })
	ctx := context.Background()
	require.NoError(t, c.HSet(ctx, "work epoch", "n", "3").Err(), "the epoch domain")
	success := func(args ...string) string {
		t.Helper()
		code, out, errout := runTable(at(addr, args...)...)
		require.EqualValues(t, 0, code, "%v: %d %q %q", args, code, out, errout)
		return out
	}
	success("create", "tmp1", "--columns", "a:count:sum,b:text:none:it's done", "--footer", "all rows", "--width", "a=4",
		"--epoch-key", "work epoch", "--member-prefix", "it's members:", "--epoch", "3")
	saved := func() (map[string]string, map[string]string) {
		def := c.HGetAll(ctx, ntable.DefKey("tmp1")).Val()
		delete(def, "created_at")
		return def, c.HGetAll(ctx, ntable.IdentityKey("tmp1")).Val()
	}
	def, identity := saved()
	require.NotEmpty(t, def, "the saved definition")
	success("drop", "tmp1", "--epoch", "3")

	// another definition under the same routing reaches the saved definition
	code, out, errout := runTable(at(addr, "create", "tmp1", "--columns", "x", "--epoch-key", "work epoch", "--member-prefix", "it's members:", "--epoch", "3")...)
	require.EqualValues(t, 1, code, "create over the dropped definition: %q %q", out, errout)
	_, printed, ok := strings.Cut(errout, "to bring it back as it was: ")
	require.True(t, ok, "the refusal prints the create: %q", errout)
	printed, _, ok = strings.Cut(printed, "; run: ")
	require.True(t, ok, "the create ends before the remedy: %q", errout)

	// nova-table on PATH is this test binary as nova-table's main (TestNovaTableMain)
	bin := t.TempDir()
	exe, err := os.Executable()
	require.NoError(t, err)
	shim := "#!/bin/sh\n" + tableMainEnv + "=1 exec " + oneline.ShellWord(exe) + " -test.run='^TestNovaTableMain$' -- \"$@\"\n"
	require.NoError(t, os.WriteFile(filepath.Join(bin, "nova-table"), []byte(shim), 0o755))
	sh := exec.Command("sh", "-c", printed+" --redis "+oneline.ShellWord(addr))
	sh.Env = []string{"PATH=" + bin + ":/usr/bin:/bin", "HOME=" + t.TempDir()}
	got, err := sh.CombinedOutput()
	require.NoError(t, err, "the printed create %q: %s", printed, got)
	assert.Contains(t, string(got), "TABLE CREATE table=tmp1", "the printed create %q", printed)

	after, afterIdentity := saved()
	assert.Equal(t, def, after, "the definition the printed create brought back")
	assert.Equal(t, identity, afterIdentity, "the identity the printed create kept")
	assert.Contains(t, success("show", "tmp1"), "epoch=3", "the table is back at its epoch")
}
