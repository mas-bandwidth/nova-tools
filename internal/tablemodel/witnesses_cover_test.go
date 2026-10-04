package tablemodel

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWitnessesCoverWithStoreReturnsAStoreThatCannotStartCannotRun(t *testing.T) {
	t.Parallel()
	long := filepath.Join(t.TempDir(), strings.Repeat("d", 99), strings.Repeat("e", 99))
	require.NoError(t, os.MkdirAll(long, 0o755), "the store's refused directory = %v", long)
	handed := false
	err := WithStore(context.Background(), ServerOptions{RedisServer: "redis-server", TmpDir: long},
		func(*Store) { handed = true })
	var c *CannotRun
	require.ErrorAs(t, err, &c, "error = %v, want CannotRun for a store that cannot start", err)
	require.ErrorContains(t, err, "shorter directory", "error = %v, want the remedy named", err)
	require.False(t, handed, "a store that never came up was handed over")
}

func TestWitnessesCoverRunWitnessesRefusesASourceItCannotRead(t *testing.T) {
	t.Parallel()
	err := RunWitnesses(context.Background(), filepath.Join(t.TempDir(), "absent-table.lua"), ServerOptions{}, func(Finding) {})
	var c *CannotRun
	require.ErrorAs(t, err, &c, "error = %v, want CannotRun for a source that cannot be read", err)
	require.ErrorContains(t, err, "no such file", "error = %v, want the read failure named", err)
}

func TestWitnessesCoverWitnessesFailsTheCheckWhenTheStoreIsDone(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := &Store{ctx: ctx}
	var findings []Finding
	err := guard(func() { witnesses(r, func(f Finding) { findings = append(findings, f) }) })
	var f *Failure
	require.ErrorAs(t, err, &f, "error = %v, want a failed check, not a hang or a panic", err)
	require.ErrorContains(t, err, "ran out of time", "error = %v, want the store's done context named", err)
	require.Empty(t, findings, "findings = %v, want none: the store never answered", findings)
}
