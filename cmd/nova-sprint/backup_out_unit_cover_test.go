package main

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// This file covers the pure helpers, the twin seams and the early refusals of
// backup_out.go without a store, a server or a child process. The functional
// tier (backup_restore_functional_test.go) owns redisBackup.Take, dumpKeys,
// redisBackup.State, startServerTwin and every serverTwin method; they need a
// live Redis or a redis-server and are not reached here.

// coverSource is a backupSource that counts Take and returns what it is told.
type coverSource struct {
	takeCalls int
	epoch     uint64
	keys      []store.DumpKey
	cols      map[string]int
	takeErr   error
}

func (s *coverSource) Take(context.Context) (uint64, []store.DumpKey, map[string]int, error) {
	s.takeCalls++
	return s.epoch, s.keys, s.cols, s.takeErr
}

// coverStateSource is a coverSource whose sprint state can be read, so run's
// semantic branch sees a stateSource.
type coverStateSource struct {
	*coverSource
	state    store.SprintState
	stateErr error
}

func (s *coverStateSource) State(context.Context) (store.SprintState, error) {
	return s.state, s.stateErr
}

// coverFailWriter fails every write.
type coverFailWriter struct{}

func (coverFailWriter) Write([]byte) (int, error) {
	return 0, errors.New("cover: the writer failed")
}

// coverFailAfter lets the first write through and fails the next: a map key
// goes through and the write of its value is the one that fails.
type coverFailAfter struct{ writes int }

func (w *coverFailAfter) Write(p []byte) (int, error) {
	if w.writes >= 1 {
		return 0, errors.New("cover: the writer failed")
	}
	w.writes++
	return len(p), nil
}

// compactStrings folds neighbouring equal names and keeps the rest, so the
// parts of a split dump are named once.
func TestSprintBackupOutCoverCompactStrings(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		in   []string
		want []string
	}{
		{"nil", nil, nil},
		{"runs fold to one", []string{"a", "a", "b", "b", "b", "c"}, []string{"a", "b", "c"}},
		{"only neighbours fold", []string{"a", "b", "a"}, []string{"a", "b", "a"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, compactStrings(tc.in))
		})
	}
}

// sprintStateDiff names the parts where a restored sprint is not the source,
// or the empty line when they are the same, capped at eight names.
func TestSprintBackupOutCoverSprintStateDiff(t *testing.T) {
	t.Parallel()
	parts := func(names ...string) store.SprintState {
		m := store.SprintState{Parts: map[string]string{}}
		for _, n := range names {
			m.Parts[n] = "v"
		}
		return m
	}
	t.Run("equal", func(t *testing.T) {
		t.Parallel()
		a := store.SprintState{Parts: map[string]string{"x": "1", "y": "2"}}
		b := store.SprintState{Parts: map[string]string{"x": "1", "y": "2"}}
		assert.Equal(t, "", sprintStateDiff(a, b))
	})
	t.Run("one changed part", func(t *testing.T) {
		t.Parallel()
		a := store.SprintState{Parts: map[string]string{"x": "1"}}
		b := store.SprintState{Parts: map[string]string{"x": "2"}}
		assert.Equal(t, "1 part(s): x", sprintStateDiff(a, b))
	})
	t.Run("ten parts on one side only", func(t *testing.T) {
		t.Parallel()
		want := parts("a", "b", "c", "d", "e", "f", "g", "h", "i", "j")
		assert.Equal(t, "10 part(s): a, b, c, d, e, f, g, h and 2 more", sprintStateDiff(want, store.SprintState{}))
	})
}

// The twin seam: State refuses a twin that was never restored, Library names
// it, Close is safe on a bare twin, and Values writes every key it holds.
func TestSprintBackupOutCoverMemTwin(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	t.Run("state before restore", func(t *testing.T) {
		t.Parallel()
		_, err := (&memTwin{}).State(ctx)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no restored sprint")
	})
	t.Run("library", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, "twin", (&memTwin{}).Library())
	})
	t.Run("close", func(t *testing.T) {
		t.Parallel()
		(&memTwin{}).Close()
	})
	t.Run("values", func(t *testing.T) {
		t.Parallel()
		tw := &memTwin{m: store.NewMem(), epoch: 0}
		var b strings.Builder
		require.NoError(t, tw.Values(ctx, &b))
		assert.Contains(t, b.String(), "sprint:mem:meta", "Values names every key it holds")
	})
}

// writeStrings writes every string and field name of a JSON value, one a line,
// and skips numbers and booleans; a write that fails is returned from the
// string, the array and the map key branch.
func TestSprintBackupOutCoverWriteStrings(t *testing.T) {
	t.Parallel()
	t.Run("nested map and array", func(t *testing.T) {
		t.Parallel()
		v := map[string]any{
			"name":  "alpha",
			"count": 3,
			"flag":  true,
			"list":  []any{"beta", 4, false, map[string]any{"inner": "gamma"}},
		}
		var b strings.Builder
		require.NoError(t, writeStrings(&b, v))
		lines := strings.Split(strings.TrimSuffix(b.String(), "\n"), "\n")
		assert.ElementsMatch(t, []string{"name", "alpha", "count", "flag", "list", "beta", "inner", "gamma"}, lines)
	})
	t.Run("writer fails on a string", func(t *testing.T) {
		t.Parallel()
		assert.Error(t, writeStrings(coverFailWriter{}, "alpha"))
	})
	t.Run("writer fails inside an array", func(t *testing.T) {
		t.Parallel()
		assert.Error(t, writeStrings(coverFailWriter{}, []any{"alpha"}))
	})
	t.Run("writer fails on a map key", func(t *testing.T) {
		t.Parallel()
		assert.Error(t, writeStrings(coverFailWriter{}, map[string]any{"alpha": 1}))
	})
	t.Run("writer fails on a map value", func(t *testing.T) {
		t.Parallel()
		assert.Error(t, writeStrings(&coverFailAfter{}, map[string]any{"alpha": "beta"}))
	})
}

// finalLine is the last line of a command's output, trimmed.
func TestSprintBackupOutCoverFinalLine(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, in, want string
	}{
		{"empty", "", ""},
		{"one line", "one line", "one line"},
		{"last after a newline", "a\nb\n", "b"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, finalLine(tc.in))
		})
	}
}

// fileSum is the sha256 of a file's bytes, and an error for a file that is not
// there.
func TestSprintBackupOutCoverFileSum(t *testing.T) {
	t.Parallel()
	t.Run("missing file", func(t *testing.T) {
		t.Parallel()
		_, err := fileSum(filepath.Join(t.TempDir(), "absent"))
		require.Error(t, err)
	})
	t.Run("written file", func(t *testing.T) {
		t.Parallel()
		data := []byte("the bytes a backup sums\n")
		path := filepath.Join(t.TempDir(), "file")
		require.NoError(t, os.WriteFile(path, data, 0o600))
		got, err := fileSum(path)
		require.NoError(t, err)
		assert.Equal(t, sumOf(data), got)
	})
}

// sealedNames refuses an incomplete seat before it runs nova-secrets, naming
// the flags and the login; the stream is never run.
func TestSprintBackupOutCoverSealedNames(t *testing.T) {
	t.Parallel()
	base := backupScanner{bin: "/bin/false", store: "s", as: "a", key: "k", sops: "sops", self: "/bin/true"}
	for _, tc := range []struct {
		name   string
		mutate func(*backupScanner)
	}{
		{"bin", func(s *backupScanner) { s.bin = "" }},
		{"store", func(s *backupScanner) { s.store = "" }},
		{"as", func(s *backupScanner) { s.as = "" }},
		{"key", func(s *backupScanner) { s.key = "" }},
		{"sops", func(s *backupScanner) { s.sops = "" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			scan := base
			tc.mutate(&scan)
			called := false
			_, _, err := scan.sealedNames(t.Context(), func(io.Writer) error {
				called = true
				return nil
			})
			require.Error(t, err)
			assert.Contains(t, err.Error(), "--secrets-store")
			assert.Contains(t, err.Error(), "nova-sprint seat login")
			assert.False(t, called, "an incomplete seat is refused before the stream runs")
		})
	}
}

// backupOut.run refuses a non-empty --out, an unreadable --out, a Take that
// fails and a State that fails, before it runs xz or creates --out.
func TestSprintBackupOutCoverRun(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	t.Run("out is not empty", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "a"), []byte("x"), 0o600))
		src := &coverSource{}
		_, err := (&backupOut{out: dir, src: src}).run(ctx)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not empty")
		assert.Zero(t, src.takeCalls, "a non-empty --out is refused before the store is read")
	})

	t.Run("out is a file", func(t *testing.T) {
		t.Parallel()
		file := filepath.Join(t.TempDir(), "afile")
		require.NoError(t, os.WriteFile(file, []byte("x"), 0o600))
		src := &coverSource{}
		_, err := (&backupOut{out: file, src: src}).run(ctx)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "cannot be read")
		assert.Zero(t, src.takeCalls, "an unreadable --out is refused before the store is read")
	})

	t.Run("take fails", func(t *testing.T) {
		t.Parallel()
		parent := t.TempDir()
		out := filepath.Join(parent, "backup")
		src := &coverSource{takeErr: errors.New("cover: the store went away")}
		_, err := (&backupOut{out: out, src: src}).run(ctx)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "the store gave no dump")
		assert.Equal(t, 1, src.takeCalls)
		_, statErr := os.Stat(out)
		assert.True(t, os.IsNotExist(statErr), "a failed run writes no --out")
		assertNoCoverWorkDir(t, parent)
	})

	t.Run("state fails", func(t *testing.T) {
		t.Parallel()
		parent := t.TempDir()
		out := filepath.Join(parent, "backup")
		src := &coverStateSource{coverSource: &coverSource{}, stateErr: errors.New("cover: no state")}
		_, err := (&backupOut{out: out, src: src}).run(ctx)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "cannot be read")
		assert.Equal(t, 1, src.takeCalls)
		assertNoCoverWorkDir(t, parent)
	})
}

// assertNoCoverWorkDir holds a failed run to leaving no `.nova-sprint-backup-`
// work directory beside --out.
func assertNoCoverWorkDir(t *testing.T, parent string) {
	t.Helper()
	ents, err := os.ReadDir(parent)
	require.NoError(t, err)
	for _, e := range ents {
		assert.False(t, strings.HasPrefix(e.Name(), ".nova-sprint-backup-"), "a failed run left %s behind", e.Name())
	}
}
