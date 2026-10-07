package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakePG stands in for pg_dump and pg_restore --list: the dump it writes is
// body, the list it answers is toc, and each fails when its error is set.
type fakePG struct {
	body             string
	toc              string
	dumpErr, listErr error
	calls            [][]string
	envs             [][]string
}

const goodTOC = `;
; Archive created at 2026-10-06 12:00:00 UTC
;
215; 1259 16390 TABLE config machine nova_config
3412; 0 16390 TABLE DATA config machine nova_config
3413; 0 16401 TABLE DATA config history nova_config
`

func (f *fakePG) run(_ context.Context, env []string, prog string, args ...string) ([]byte, error) {
	f.calls = append(f.calls, append([]string{prog}, args...))
	f.envs = append(f.envs, env)
	switch prog {
	case "pg_dump":
		if f.dumpErr != nil {
			return []byte("pg_dump: error: connection refused"), f.dumpErr
		}
		for i, a := range args {
			if a == "--file" {
				return nil, os.WriteFile(args[i+1], []byte(f.body), 0o600)
			}
		}
		return nil, errors.New("no --file")
	case "pg_restore":
		if f.listErr != nil {
			return []byte("pg_restore: error: input file does not appear to be a valid archive"), f.listErr
		}
		return []byte(f.toc), nil
	}
	return nil, errors.New("unexpected program " + prog)
}

// backupRig is the harness with the fake client programs and a clock that
// moves an hour each dump.
func backupRig(t *testing.T) (*harness, *fakePG, deps) {
	t.Helper()
	h := newHarness()
	h.env["NOVA_PG_DSN"] = dsn
	h.env["NOVA_PG_PASSWORD"] = "s3cret-pw"
	f := &fakePG{body: "PGDMP one", toc: goodTOC}
	d := h.deps()
	d.pgRun = f.run
	at := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	d.now = func() time.Time { at = at.Add(time.Hour); return at }
	return h, f, d
}

func runWith(d deps, args ...string) (int, string, string) {
	var out, errb bytes.Buffer
	code := run(args, &out, &errb, d)
	return code, out.String(), errb.String()
}

func TestBackupWritesAVerifiedDumpAndPrunesOnlyPastKeep(t *testing.T) {
	t.Parallel()
	_, f, d := backupRig(t)
	dir := filepath.Join(t.TempDir(), "postgres")
	for i, body := range []string{"PGDMP one", "PGDMP two", "PGDMP three"} {
		f.body = body
		code, out, errs := runWith(d, "backup", "--dir", dir, "--keep", "2")
		require.Zero(t, code, "take %d: %s", i, errs)
		sum := sha256.Sum256([]byte(body))
		assert.Contains(t, out, "CONFIG BACKUP pg=nova_config@127.0.0.1:5432/nova file="+dir+"/config-20261006T", "take %d: %q", i, out)
		assert.Contains(t, out, fmt.Sprintf(" sha256=%s bytes=%d entries=3 tables=2 verified=list+sum ", hex.EncodeToString(sum[:]), len(body)), "take %d: %q", i, out)
		assert.True(t, strings.HasSuffix(out, map[int]string{0: "pruned=0 keep=2\n", 1: "pruned=0 keep=2\n", 2: "pruned=1 keep=2\n"}[i]), "take %d: %q", i, out)
	}
	files, err := backupFiles(dir)
	require.NoError(t, err)
	require.Equal(t, []string{"config-20261006T140000Z.dump", "config-20261006T150000Z.dump"}, files, "the newest two stay")
	for _, n := range files {
		body, err := os.ReadFile(filepath.Join(dir, n))
		require.NoError(t, err)
		side, err := os.ReadFile(filepath.Join(dir, n+backupSumSuffix))
		require.NoError(t, err)
		sum := sha256.Sum256(body)
		assert.Equal(t, hex.EncodeToString(sum[:])+"  "+n+"\n", string(side), "%s's sum", n)
	}
	_, err = os.Stat(filepath.Join(dir, "config-20261006T130000Z.dump"+backupSumSuffix))
	assert.True(t, os.IsNotExist(err), "the pruned dump's sum goes with it")
	info, err := os.Stat(dir)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o700), info.Mode().Perm(), "the backup directory is 0700")

	// The password reaches pg_dump in its environment, never on its line.
	require.Equal(t, "pg_dump", f.calls[0][0])
	assert.Equal(t, []string{"pg_dump", "--format=custom", "--no-password", "--file"}, f.calls[0][:4])
	for _, c := range f.calls {
		assert.NotContains(t, strings.Join(c, " "), "s3cret-pw", "a password on the line: %v", c)
	}
	assert.Contains(t, f.envs[0], "PGPASSWORD=s3cret-pw")
	assert.Contains(t, f.envs[0], "PGDATABASE=nova")
	assert.Contains(t, f.envs[0], "PGHOST=127.0.0.1")
	assert.Nil(t, f.envs[1], "pg_restore --list reads the file and is handed no login")
}

// A dump that fails a check is removed and the older ones stay: the
// directory holds only verified dumps, and nothing is pruned for a failure.
func TestBackupThatFailsACheckLeavesTheOlderDumps(t *testing.T) {
	t.Parallel()
	_, f, d := backupRig(t)
	dir := t.TempDir()
	code, _, errs := runWith(d, "backup", "--dir", dir, "--keep", "1")
	require.Zero(t, code, errs)
	first, err := backupFiles(dir)
	require.NoError(t, err)
	require.Len(t, first, 1)

	cases := []struct {
		name string
		set  func()
		want string
	}{
		{"no config table data", func() { f.toc = "215; 1259 16390 SCHEMA - public postgres\n" }, "holds no table data of schema config (1 entries); the dump was removed; run: nova-config migrate"},
		{"an unreadable archive", func() { f.toc, f.listErr = goodTOC, errors.New("exit status 1") }, "pg_restore --list cannot read the dump"},
		{"an empty dump", func() { f.listErr, f.body = nil, "" }, "pg_dump wrote an empty"},
		{"a dump that did not connect", func() { f.body, f.dumpErr = "PGDMP", errors.New("exit status 1") }, "pg_dump failed: exit status 1: pg_dump: error: connection refused"},
	}
	for _, c := range cases {
		c.set()
		code, out, errs := runWith(d, "backup", "--dir", dir, "--keep", "1")
		assert.Equal(t, 1, code, "%s: %s", c.name, errs)
		assert.Empty(t, out, c.name)
		assert.True(t, strings.HasPrefix(errs, "nova-config backup REFUSED: "), "%s: %q", c.name, errs)
		assert.Contains(t, errs, c.want, c.name)
		left, err := os.ReadDir(dir)
		require.NoError(t, err)
		var names []string
		for _, e := range left {
			names = append(names, e.Name())
		}
		assert.Equal(t, []string{first[0], first[0] + backupSumSuffix}, names, "%s: the older dump and its sum stay, and nothing else", c.name)
	}
}

func TestBackupRefusesWhatItCannotRun(t *testing.T) {
	t.Parallel()
	_, f, d := backupRig(t)
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"backup"}, "wants --dir <dir>"},
		{[]string{"backup", "--dir", "d", "--keep", "0"}, "--keep of at least 1"},
		{[]string{"backup", "--dir", "d", "extra"}, "takes no positional arguments"},
		{[]string{"backup", "--dir", "d", "--file", "try.json"}, "a --file store is a local JSON file, not a database"},
		{[]string{"backup", "--dir", "d", "--pg", "postgres://u:pw@h/db"}, "--pg carries a password"},
	} {
		code, out, errs := runWith(d, c.args...)
		assert.Equal(t, 2, code, "%v: %s", c.args, errs)
		assert.Empty(t, out)
		assert.Contains(t, errs, c.want, "%v", c.args)
	}
	assert.Empty(t, f.calls, "a refusal runs no client program")
}

// --every takes a dump, waits, and takes again until the wait is ended; a
// failed take is a FAILED line and the loop goes on.
func TestBackupEveryLoopsUntilInterrupted(t *testing.T) {
	t.Parallel()
	_, f, d := backupRig(t)
	dir := t.TempDir()
	waits := 0
	d.pause = func(_ context.Context, every time.Duration) error {
		assert.Equal(t, time.Hour, every)
		waits++
		switch waits {
		case 1:
			f.dumpErr = errors.New("exit status 1")
		case 2:
			f.dumpErr = nil
		default:
			return context.Canceled
		}
		return nil
	}
	code, out, errs := runWith(d, "backup", "--dir", dir, "--every", "1h", "--keep", "5")
	require.Zero(t, code, errs)
	assert.Equal(t, 2, strings.Count(out, "CONFIG BACKUP "), "two good takes: %q", out)
	assert.Contains(t, errs, "nova-config backup FAILED: pg_dump failed: exit status 1")
	assert.Contains(t, errs, "the older dumps stay, the next try is in 1h0m0s")
	files, err := backupFiles(dir)
	require.NoError(t, err)
	assert.Len(t, files, 2)
}

func TestBackupHelpNamesTheRestore(t *testing.T) {
	t.Parallel()
	code, out, _ := runWith(newHarness().deps(), "backup", "-h")
	require.Zero(t, code)
	assert.Contains(t, out, "local write: writes files on this machine; reads PostgreSQL through pg_dump and writes nothing to it")
	assert.Contains(t, out, "pg_restore --dbname <dsn> --no-owner <file>, then nova-config migrate")
	assert.Contains(t, out, "example: nova-config backup --dir /backup/postgres --keep 14")
	assert.Contains(t, out, "--every")
}
