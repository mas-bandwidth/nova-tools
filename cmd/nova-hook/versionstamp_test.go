package main

import (
	"context"
	"errors"
	"testing"

	"github.com/mas-bandwidth/nova-tools/hook"
	"github.com/mas-bandwidth/nova-tools/pkg/buildinfo"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// captureHash records one HSET. It opens no store.
type captureHash struct {
	key    string
	values []any
	err    error
}

func (c *captureHash) HSet(ctx context.Context, key string, values ...interface{}) *redis.IntCmd {
	c.key = key
	c.values = append([]any(nil), values...)
	cmd := redis.NewIntCmd(ctx)
	if c.err != nil {
		cmd.SetErr(c.err)
		return cmd
	}
	cmd.SetVal(1)
	return cmd
}

func TestStampVersionWritesTheBinaryVersion(t *testing.T) {
	t.Parallel()
	want := hookVersion()
	assert.Equal(t, buildinfo.Version(version), want, "the reported version is the binary's own stamp")
	assert.NotEmpty(t, want)

	rec := &captureHash{}
	require.NoError(t, stampVersion(context.Background(), rec, hook.RecordKey("ada"), want))
	assert.Equal(t, hook.RecordKey("ada"), rec.key)
	assert.Equal(t, []any{"version", want}, rec.values)

	srv := &captureHash{}
	require.NoError(t, stampVersion(context.Background(), srv, hook.ServeKey, want))
	assert.Equal(t, hook.ServeKey, srv.key)
	assert.Equal(t, []any{"version", want}, srv.values)
}

func TestStampVersionRefusesABlank(t *testing.T) {
	t.Parallel()
	err := stampVersion(context.Background(), &captureHash{}, hook.RecordKey("ada"), "  ")
	require.EqualError(t, err, "this binary has no version string to report")
	err = stampVersion(context.Background(), nil, hook.ServeKey, hookVersion())
	require.EqualError(t, err, "the hook record has no store")
}

func TestStampVersionReportsAStoreError(t *testing.T) {
	t.Parallel()
	err := stampVersion(context.Background(), &captureHash{err: errors.New("store down")}, hook.ServeKey, hookVersion())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "store down")
}
