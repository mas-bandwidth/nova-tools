package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMainCoverRealDeps walks every seam realDeps hands main: the
// environment, the clock, the hostname and the two store-openers. The
// --file path of openStore needs no database; openRedis builds a client
// that dials only on its first command, so no socket opens here. What the
// unit tier cannot reach without a live store or a subprocess: the
// Postgres branch of openStore (config.OpenPG pings) and calling the
// tailscale seam (it execs tailscale); both are pinned set, not invoked.
func TestMainCoverRealDeps(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := realDeps()

	t.Run("seams point at the real world", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, os.Getenv("NOVA_TEST_NO_HOST"), d.getenv("NOVA_TEST_NO_HOST"), "getenv is os.Getenv")
		assert.Equal(t, "", d.getenv("NOVA_CONFIG_ABSENT_SEAM_KEY"), "an unset name reads empty")
		require.False(t, d.now().IsZero(), "now is the clock the verbs take")
		host, err := d.hostname()
		require.NoError(t, err)
		assert.NotEmpty(t, host, "hostname names this machine")
		require.NotNil(t, d.tailscale, "the seam is set; calling it is a subprocess")
	})

	dir := t.TempDir()
	notAStore := filepath.Join(dir, "not-a-store.json")
	require.NoError(t, os.WriteFile(notAStore, []byte("nope\n"), 0o644))

	// openStore answers on the file: prefix: a path not there yet is an
	// empty store, a path that is not a store file is the refusal.
	storeCases := []struct {
		name    string
		dsn     string
		wantErr string
	}{
		{name: "file: makes an empty store", dsn: filePrefix + filepath.Join(dir, "try.json")},
		{name: "file: refuses a store file it cannot read", dsn: filePrefix + notAStore, wantErr: "is not a nova-config store file"},
	}
	for _, c := range storeCases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			st, err := d.openStore(ctx, c.dsn)
			if c.wantErr != "" {
				assert.ErrorContains(t, err, c.wantErr)
				assert.Nil(t, st)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, st)
			assert.NoError(t, st.Close())
		})
	}

	// openRedis answers on an address and refuses without one: no seat is
	// selected in this process, so the fallback address is empty too.
	redisCases := []struct {
		name    string
		addr    string
		wantErr string
	}{
		{name: "an address opens a client that dials on first use", addr: "127.0.0.1:0"},
		{name: "no address and no seat refuses", addr: "", wantErr: "redis address is required"},
	}
	for _, c := range redisCases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			r, err := d.openRedis(ctx, c.addr)
			if c.wantErr != "" {
				assert.ErrorContains(t, err, c.wantErr)
				assert.Nil(t, r)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, r)
			assert.NoError(t, r.Close())
		})
	}
}

// TestMainCoverRedisApplierClose reaches redisApplier's Close, the shut
// main's stores take: the live client realDeps.openRedis returns closes
// clean, and so does the zero redisApplier whose store was never opened.
func TestMainCoverRedisApplierClose(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	cases := []struct {
		name string
		side func(t *testing.T) redisSide
	}{
		{name: "the store realDeps opened closes clean", side: func(t *testing.T) redisSide {
			r, err := realDeps().openRedis(ctx, "127.0.0.1:0")
			require.NoError(t, err)
			return r
		}},
		{name: "a store never opened closes clean", side: func(*testing.T) redisSide { return redisApplier{} }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			r := c.side(t)
			require.NoError(t, r.Close())
		})
	}
}
