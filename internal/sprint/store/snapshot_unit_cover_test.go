package store

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestStoreSnapshotCoverWorkColumns pins workColumns as the work table's
// non-text columns in the order of their definition in internal/sprint/schema.go.
func TestStoreSnapshotCoverWorkColumns(t *testing.T) {
	t.Parallel()
	// From schema.go line 257: mk(Work, "waiting,ready,working,review,merging,landed,cost:text:sum")
	// Non-text columns: waiting, ready, working, review, merging, landed
	want := []string{"waiting", "ready", "working", "review", "merging", "landed"}
	got := workColumns()
	assert.Equal(t, want, got)
}

// TestStoreSnapshotCoverBackupCountsText tests BackupCounts.Text.
func TestStoreSnapshotCoverBackupCountsText(t *testing.T) {
	t.Parallel()

	t.Run("out of order columns prints in workColumns order", func(t *testing.T) {
		t.Parallel()
		c := BackupCounts{
			Keys: 3,
			Columns: map[string]int{
				"landed":  1,
				"waiting": 2,
			},
		}
		got := c.Text()
		// workColumns order: waiting, ready, working, review, merging, landed
		assert.Contains(t, got, "keys=3")
		assert.Contains(t, got, "cards=3")
		assert.Contains(t, got, "waiting=2")
		assert.Contains(t, got, "landed=1")
		// Check ordering: waiting should come before landed
		waitingIdx := bytes.Index([]byte(got), []byte("waiting="))
		landedIdx := bytes.Index([]byte(got), []byte("landed="))
		assert.Less(t, waitingIdx, landedIdx)
	})

	t.Run("zero column prints keys=0 cards=0 ()", func(t *testing.T) {
		t.Parallel()
		c := BackupCounts{Keys: 0, Columns: map[string]int{}}
		got := c.Text()
		assert.Equal(t, "keys=0 cards=0 ()", got)
	})

	t.Run("no columns at all", func(t *testing.T) {
		t.Parallel()
		c := BackupCounts{Keys: 0, Columns: nil}
		got := c.Text()
		assert.Equal(t, "keys=0 cards=0 ()", got)
	})
}

// TestStoreSnapshotCoverBackupCountsDiffer tests BackupCounts.Differ.
func TestStoreSnapshotCoverBackupCountsDiffer(t *testing.T) {
	t.Parallel()

	t.Run("equal counts returns empty string", func(t *testing.T) {
		t.Parallel()
		c := BackupCounts{
			Keys: 3,
			Columns: map[string]int{
				"waiting": 1,
				"ready":   2,
			},
		}
		got := c.Differ(c)
		assert.Equal(t, "", got)
	})

	t.Run("keys differ names the keys", func(t *testing.T) {
		t.Parallel()
		c := BackupCounts{Keys: 3, Columns: map[string]int{}}
		got := BackupCounts{Keys: 5, Columns: map[string]int{}}
		assert.Contains(t, c.Differ(got), "keys 3, restored 5")
	})

	t.Run("column differs names the column with want and restored", func(t *testing.T) {
		t.Parallel()
		c := BackupCounts{
			Keys: 3,
			Columns: map[string]int{
				"waiting": 1,
			},
		}
		got := BackupCounts{
			Keys: 3,
			Columns: map[string]int{
				"waiting": 2,
			},
		}
		differ := c.Differ(got)
		assert.Contains(t, differ, "waiting 1, restored 2")
	})

	t.Run("multiple differences joined by comma space", func(t *testing.T) {
		t.Parallel()
		c := BackupCounts{
			Keys: 3,
			Columns: map[string]int{
				"waiting": 1,
				"ready":   2,
			},
		}
		got := BackupCounts{
			Keys: 5,
			Columns: map[string]int{
				"waiting": 3,
				"ready":   4,
			},
		}
		differ := c.Differ(got)
		assert.Contains(t, differ, "keys 3, restored 5")
		assert.Contains(t, differ, "waiting 1, restored 3")
		assert.Contains(t, differ, "ready 2, restored 4")
		// Check they are joined
		assert.Contains(t, differ, ", ")
	})
}

// TestStoreSnapshotCoverCardsByColumn tests CardsByColumn.
func TestStoreSnapshotCoverCardsByColumn(t *testing.T) {
	t.Parallel()

	t.Run("after setup counts cards in their placed column", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		h.setup(2)
		cols, err := CardsByColumn(context.Background(), h.st.B, h.st.Names)
		require.NoError(t, err)
		// After setup (which includes one deal), 2 primaries are in ready column
		assert.Equal(t, 2, cols["ready"])
	})

	t.Run("after deal counts them in two columns", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		h.setup(2)
		// After setup, both s1-1 and s1-2 are in ready column
		// Do one deal step - s1-1 moves from ready to working
		h.must(DealStep(sprint.DealReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}}))
		cols, err := CardsByColumn(context.Background(), h.st.B, h.st.Names)
		require.NoError(t, err)
		// s1-1 is in working, s1-2 is in ready
		assert.Equal(t, 1, cols["working"])
		assert.Equal(t, 1, cols["ready"])
	})

	t.Run("fresh NewMem never Init-ed returns error", func(t *testing.T) {
		t.Parallel()
		backend := NewMem()
		_, err := CardsByColumn(context.Background(), backend, sprint.Names{})
		assert.Error(t, err)
	})
}

// TestStoreSnapshotCoverMemDump tests MemDump and round-trip with MemRestore.
func TestStoreSnapshotCoverMemDump(t *testing.T) {
	t.Parallel()

	t.Run("memDump of a harness sprint after setup and deal restores equal", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		h.setup(2)
		// Do one deal step
		h.must(DealStep(sprint.DealReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}}))

		dump, err := MemDump(h.m, 0)
		require.NoError(t, err)
		require.NotEmpty(t, dump)

		r, err := MemRestore(dump)
		require.NoError(t, err)

		again, err := MemDump(r, 0)
		require.NoError(t, err)

		assert.Equal(t, dump, again, "a twin's dump restores into a twin that dumps the same keys")
	})

	t.Run("memDump with epoch above sprint's returns only keys InBackup keeps", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		h.setup(2)

		epoch := uint64(100)
		dump, err := MemDump(h.m, epoch)
		require.NoError(t, err)

		// All keys should be InBackup for that epoch
		for _, k := range dump {
			assert.True(t, InBackup(k.Key, epoch), "key %s should be in backup for epoch %d", k.Key, epoch)
		}
	})
}

// TestStoreSnapshotCoverMemRestoreRefusals tests MemRestore refusals.
func TestStoreSnapshotCoverMemRestoreRefusals(t *testing.T) {
	t.Parallel()

	t.Run("unknown field does not restore", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		h.setup(2)
		dump, err := MemDump(h.m, 0)
		require.NoError(t, err)

		// Edit the meta key to have an unknown field
		for i, k := range dump {
			if k.Key == "sprint:mem:meta" {
				dump[i].Payload = []byte(`{"version":99,"unknownField":1}`)
				break
			}
		}
		_, err = MemRestore(dump)
		assert.ErrorContains(t, err, "does not restore")
	})

	t.Run("key outside every prefix whose payload has no member is no key of a twin's dump", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		h.setup(2)
		dump, err := MemDump(h.m, 0)
		require.NoError(t, err)

		// Add a key outside every prefix (not a valid table, view, KV, etc. key)
		dump = append(dump, DumpKey{Key: "randomkey", Payload: []byte(`{}`)})
		_, err = MemRestore(dump)
		assert.ErrorContains(t, err, "is no key of a twin's dump")
	})

	t.Run("table generation key with table definition key removed is a generation of table", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		h.setup(2)
		dump, err := MemDump(h.m, 0)
		require.NoError(t, err)

		// Find and remove the table definition key, keep the generation
		var filtered []DumpKey
		for _, k := range dump {
			if strings.HasPrefix(k.Key, "table:") && strings.HasSuffix(k.Key, ":epoch") {
				// Keep generation keys
				filtered = append(filtered, k)
			} else if !strings.HasPrefix(k.Key, "table:") {
				// Keep non-table keys
				filtered = append(filtered, k)
			}
		}
		_, err = MemRestore(filtered)
		assert.ErrorContains(t, err, "a generation of table")
	})

	t.Run("record with its table removed is a record of table", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		h.setup(2)
		dump, err := MemDump(h.m, 0)
		require.NoError(t, err)

		// Remove table definitions and generation keys, keep the members
		var filtered []DumpKey
		for _, k := range dump {
			if strings.HasPrefix(k.Key, "table:") {
				// Remove all table keys (definitions and generations)
				continue
			} else if strings.HasPrefix(k.Key, "sprint:w:") {
				// Keep records
				filtered = append(filtered, k)
			} else {
				// Keep other keys
				filtered = append(filtered, k)
			}
		}
		_, err = MemRestore(filtered)
		assert.ErrorContains(t, err, "a record of table")
	})
}

// TestStoreSnapshotCoverWriteFile tests writeFile.
func TestStoreSnapshotCoverWriteFile(t *testing.T) {
	t.Parallel()

	t.Run("writes bytes and leaves no tmp file", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path := filepath.Join(dir, "testfile")
		data := []byte("test data")

		err := writeFile(path, data)
		require.NoError(t, err)

		content, err := os.ReadFile(path)
		require.NoError(t, err)
		assert.Equal(t, data, content)

		// Check no .tmp file remains
		ents, err := os.ReadDir(dir)
		require.NoError(t, err)
		for _, e := range ents {
			assert.False(t, strings.HasSuffix(e.Name(), ".tmp"))
		}
	})

	t.Run("directory that does not exist returns cannot be written", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "nonexistent", "file")
		data := []byte("test data")

		err := writeFile(path, data)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "cannot be written")
	})

	t.Run("path that is an existing directory returns cannot be written and leaves no tmp", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path := filepath.Join(dir, "existingdir")
		err := os.Mkdir(path, 0o755)
		require.NoError(t, err)
		data := []byte("test data")

		err = writeFile(path, data)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "cannot be written")

		// Check no .tmp file remains
		ents, err := os.ReadDir(dir)
		require.NoError(t, err)
		for _, e := range ents {
			assert.False(t, strings.HasSuffix(e.Name(), ".tmp"))
		}
	})
}
