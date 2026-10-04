package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeSource hands out numbered RDBs; the n-th holds n cards. It is the
// store's side of the drill: no socket.
type fakeSource struct {
	n    int
	skew int // added to the counts it reports, to make a mismatch
}

func (f *fakeSource) Save(context.Context) ([]byte, SnapshotCounts, error) {
	f.n++
	return []byte(fmt.Sprintf("RDB cards=%d", f.n)), SnapshotCounts{Keys: 2 * f.n, Cards: f.n + f.skew}, nil
}

// fakeTwin loads what fakeSource wrote.
type fakeTwin struct{}

func (fakeTwin) Load(b []byte) (SnapshotCounts, error) {
	var n int
	if _, err := fmt.Sscanf(string(b), "RDB cards=%d", &n); err != nil {
		return SnapshotCounts{}, errors.New("not a fake RDB")
	}
	return SnapshotCounts{Keys: 2 * n, Cards: n}, nil
}

func TestSnapshotRestoreDrill(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	at := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { at = at.Add(time.Minute); return at }
	dir := t.TempDir()
	src := &fakeSource{}
	s := &Snapshotter{Dir: dir, Keep: 3, Source: src, Twin: fakeTwin{}, Now: clock}

	var last SnapshotTaken
	for i := 1; i <= 5; i++ {
		got, err := s.Take(ctx)
		require.NoError(t, err)
		assert.Equal(t, SnapshotCounts{Keys: 2 * i, Cards: i}, got.Counts)
		assert.Len(t, got.SHA256, 64)
		if i <= 3 {
			assert.Empty(t, got.Pruned)
		} else {
			assert.Len(t, got.Pruned, 1)
		}
		last = got
	}
	files, err := SnapshotFiles(dir)
	require.NoError(t, err)
	require.Len(t, files, 3, "three snapshots are kept of five taken")
	assert.Equal(t, filepath.Join(dir, files[2]), last.File)
	ents, _ := os.ReadDir(dir)
	assert.Len(t, ents, 6, "each snapshot has its checksum beside it, and nothing else stays")

	t.Run("drill restores one and prints its counts", func(t *testing.T) {
		t.Parallel()
		c, sum, err := RestoreDrill(filepath.Join(dir, files[0]), fakeTwin{})
		require.NoError(t, err)
		assert.Equal(t, SnapshotCounts{Keys: 6, Cards: 3}, c)
		assert.Len(t, sum, 64)
	})
	t.Run("a damaged file is refused", func(t *testing.T) {
		t.Parallel()
		bad := t.TempDir()
		b := &Snapshotter{Dir: bad, Keep: 1, Source: &fakeSource{}, Twin: fakeTwin{}, Now: clock}
		got, err := b.Take(ctx)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(got.File, []byte("RDB cards=9"), 0o644))
		_, _, err = RestoreDrill(got.File, fakeTwin{})
		require.ErrorContains(t, err, "does not match its checksum")
	})
}

func TestSnapshotRefusesAMismatchAndKeepsTheOlder(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	at := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { at = at.Add(time.Minute); return at }
	src := &fakeSource{}
	s := &Snapshotter{Dir: dir, Keep: 1, Source: src, Twin: fakeTwin{}, Now: clock}
	_, err := s.Take(context.Background())
	require.NoError(t, err)
	src.skew = 1
	_, err = s.Take(context.Background())
	require.ErrorContains(t, err, "other counts")
	files, _ := SnapshotFiles(dir)
	assert.Len(t, files, 1, "the failed copy is removed and the older one is not pruned for it")
}

func TestSnapshotEveryNeverSleeps(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	var took, waited int
	var reported []error
	SnapshotEvery(ctx, func(context.Context) error {
		took++
		if took == 2 {
			return errors.New("busy")
		}
		return nil
	}, func(context.Context) error {
		waited++
		if waited == 3 {
			cancel()
			return ctx.Err()
		}
		return nil
	}, func(err error) { reported = append(reported, err) })
	assert.Equal(t, 3, took)
	assert.Len(t, reported, 1, "a failed take is reported and the loop goes on")
}

func TestRDBTwinChecksTheTrailer(t *testing.T) {
	t.Parallel()
	assert.Equal(t, uint64(0xe9c6d914c4b8d9ca), redisCRC64([]byte("123456789")), "Redis's CRC-64 check value")
	body := []byte("REDIS0011\xfa\x09redis-ver\x057.2.0\xff")
	rdb := append(append([]byte{}, body...), 0, 0, 0, 0, 0, 0, 0, 0)
	_, err := RDBTwin{}.Load(rdb)
	require.NoError(t, err, "a zero checksum is a store with rdbchecksum off")
	c := redisCRC64(body)
	for i := 0; i < 8; i++ {
		rdb[len(body)+i] = byte(c >> (8 * i))
	}
	_, err = RDBTwin{}.Load(rdb)
	require.NoError(t, err)
	rdb[12] ^= 1
	_, err = RDBTwin{}.Load(rdb)
	require.ErrorContains(t, err, "CRC-64")
	_, err = RDBTwin{}.Load([]byte("not an rdb at all"))
	require.Error(t, err)
}

func TestMemSnapshotVerifiesOnATwin(t *testing.T) {
	t.Parallel()
	m := NewMem()
	at := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	s := &Snapshotter{Dir: t.TempDir(), Keep: 2, Source: MemSource{M: m}, Twin: MemTwin{}, Now: func() time.Time { at = at.Add(time.Second); return at }}
	got, err := s.Take(context.Background())
	require.NoError(t, err)
	c, _, err := RestoreDrill(got.File, MemTwin{})
	require.NoError(t, err)
	assert.Equal(t, got.Counts, c)
	_, err = MemTwin{}.Load([]byte(`{"version":99}`))
	require.Error(t, err)
}
