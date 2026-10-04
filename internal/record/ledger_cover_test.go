package record

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ledgerFakeStore answers a go-redis client in process: the ledger's store seam
// is reached with no socket, no process and no store. PING is answered unless
// pingErr is set; every HGETALL is answered from hashes.
type ledgerFakeStore struct {
	hashes  map[string]map[string]string
	pingErr error
	pipeErr error
}

func (f *ledgerFakeStore) DialHook(next redis.DialHook) redis.DialHook { return next }

func (f *ledgerFakeStore) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		if strings.EqualFold(cmd.Name(), "ping") {
			if f.pingErr != nil {
				return f.pingErr
			}
			cmd.(*redis.StatusCmd).SetVal("PONG")
			return nil
		}
		return next(ctx, cmd)
	}
}

func (f *ledgerFakeStore) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(_ context.Context, cmds []redis.Cmder) error {
		if f.pipeErr != nil {
			return f.pipeErr
		}
		for _, cmd := range cmds {
			if mcmd, ok := cmd.(*redis.MapStringStringCmd); ok {
				mcmd.SetVal(f.hashes[cmd.Args()[1].(string)])
			}
		}
		return nil
	}
}

// ledgerFake is a RedisLedger over the in-process fake, closed when the test ends.
func ledgerFake(t *testing.T, f *ledgerFakeStore) *RedisLedger {
	t.Helper()
	client := redis.NewClient(&redis.Options{Addr: "in-process"})
	client.AddHook(f)
	s := NewRedisLedger(client)
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// TestLedgerCoverDialClientAndClose covers Client, DialLedger, silenceRedisLogger
// and Close: DialLedger opens a client with the address, user and password it was
// given (it dials nothing), Client hands it back, and Close closes it.
func TestLedgerCoverDialClientAndClose(t *testing.T) {
	t.Parallel()

	t.Run("DialLedger keeps the address, user and password", func(t *testing.T) {
		t.Parallel()

		s := DialLedger("127.0.0.1:0", "nova", "not-a-real-secret")
		require.NotNil(t, s, "DialLedger returned no ledger")
		require.Same(t, s.Client(), s.Client(), "Client does not return the client it wrapped")
		opts := s.Client().Options()
		require.Equal(t, "127.0.0.1:0", opts.Addr, "DialLedger address = %q; want the address it was given", opts.Addr)
		require.Equal(t, "nova", opts.Username, "DialLedger user = %q; want nova", opts.Username)
		require.Equal(t, "not-a-real-secret", opts.Password, "DialLedger password = %q; want what it was given", opts.Password)
		require.NoError(t, s.Close(), "Close")
	})

	t.Run("Client returns the wrapped client", func(t *testing.T) {
		t.Parallel()

		client := redis.NewClient(&redis.Options{Addr: "in-process"})
		s := NewRedisLedger(client)
		t.Cleanup(func() { _ = s.Close() })
		require.Same(t, client, s.Client(), "Client did not return the client NewRedisLedger wrapped")
	})

	t.Run("a nil ledger has no client", func(t *testing.T) {
		t.Parallel()

		assert.Nil(t, NewRedisLedger(nil).Client(), "Client of a nil ledger = non-nil; want nil")
	})
}

// TestLedgerCoverSilenceRedisLoggerAndQuietPrintf covers silenceRedisLogger and
// the quietRedis logger's Printf: the install is once per process and the logger
// discards what go-redis would print.
func TestLedgerCoverSilenceRedisLoggerAndQuietPrintf(t *testing.T) {
	t.Parallel()

	silenceRedisLogger()
	silenceRedisLogger()
	assert.NotPanics(t, func() { quietRedis{}.Printf(context.Background(), "discarded %d %s", 1, "line") },
		"quietRedis.Printf panicked; it must discard go-redis's line")
}

// TestLedgerCoverPing covers Ping's main path (a store answers PONG) and its
// refusal (the store's error is returned).
func TestLedgerCoverPing(t *testing.T) {
	t.Parallel()

	refused := errors.New("the store did not answer")
	for name, c := range map[string]struct {
		fake    *ledgerFakeStore
		wantErr error
	}{
		"answers": {fake: &ledgerFakeStore{}},
		"refusal": {fake: &ledgerFakeStore{pingErr: refused}, wantErr: refused},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			s := ledgerFake(t, c.fake)
			err := s.Ping(context.Background())
			if c.wantErr != nil {
				require.ErrorIs(t, err, c.wantErr, "Ping returned %v; want the store's error", err)
				return
			}
			require.NoError(t, err, "Ping")
		})
	}
}

// TestLedgerCoverLedgerReport covers LedgerReport's main path (a day hash is
// read, decoded and grouped) and its refusals (an unknown grouping and a month
// that is not YYYY-MM), with no store reached for either refusal.
func TestLedgerCoverLedgerReport(t *testing.T) {
	t.Parallel()

	const day = "2026-09-13"
	fake := &ledgerFakeStore{hashes: map[string]map[string]string{
		LedgerKey(day): {
			`["card-a","gpt","schema"]`: `{"provider":"openai","tokens":[10,null,null,null,null],"rough":0,"sources":""}`,
		},
	}}

	t.Run("reads a day and groups it", func(t *testing.T) {
		t.Parallel()

		s := ledgerFake(t, fake)
		totals, indexed, missing, err := s.LedgerReport(context.Background(), "2026-09", "tuple")
		require.NoError(t, err, "LedgerReport")
		require.Equal(t, 1, indexed, "indexed = %d; want 1", indexed)
		require.Equal(t, 29, missing, "missing = %d; want 29", missing)
		require.Len(t, totals, 1, "totals = %+v; want one group", totals)
		g := totals[0]
		require.Equal(t, day, g.Day, "group = %+v; want day %s", g, day)
		require.Equal(t, "gpt", g.Model, "group = %+v; want model gpt", g)
		require.Equal(t, "schema", g.Repo, "group = %+v; want repo schema", g)
		require.Equal(t, 1, g.Rows, "group = %+v; want rows=1", g)
		require.Equal(t, int64(10), g.Tokens[0], "group = %+v; want input=10", g)
		require.False(t, g.Known[1], "group = %+v; want output unknown (a null is an absence)", g)
	})

	t.Run("refuses an unknown grouping", func(t *testing.T) {
		t.Parallel()

		s := ledgerFake(t, fake)
		_, _, _, err := s.LedgerReport(context.Background(), "2026-09", "card")
		require.Error(t, err, "--by card was accepted; the report groups on model, repo, day or tuple")
	})

	t.Run("refuses a month that is not YYYY-MM", func(t *testing.T) {
		t.Parallel()

		s := ledgerFake(t, fake)
		_, _, _, err := s.LedgerReport(context.Background(), "2026-9", "tuple")
		require.Error(t, err, "month 2026-9 was accepted; the report reads one YYYY-MM")
	})

	t.Run("refuses a value it cannot read", func(t *testing.T) {
		t.Parallel()

		bad := &ledgerFakeStore{hashes: map[string]map[string]string{
			LedgerKey(day): {`["card-a","gpt","schema"]`: "not json"},
		}}
		s := ledgerFake(t, bad)
		_, _, _, err := s.LedgerReport(context.Background(), "2026-09", "tuple")
		require.ErrorContains(t, err, LedgerKey(day), "an unreadable value gave %v; want an error naming its key", err)
	})

	t.Run("refuses a field that is not a triple", func(t *testing.T) {
		t.Parallel()

		bad := &ledgerFakeStore{hashes: map[string]map[string]string{
			LedgerKey(day): {"not-a-triple": `{}`},
		}}
		s := ledgerFake(t, bad)
		_, _, _, err := s.LedgerReport(context.Background(), "2026-09", "tuple")
		require.ErrorContains(t, err, "not [card, model, repo]", "a field that is not a triple gave %v; want the field refusal", err)
	})

	t.Run("refuses a pipeline the store did not answer", func(t *testing.T) {
		t.Parallel()

		refused := errors.New("the store did not answer")
		s := ledgerFake(t, &ledgerFakeStore{pipeErr: refused})
		_, _, _, err := s.LedgerReport(context.Background(), "2026-09", "tuple")
		require.ErrorIs(t, err, refused, "a failed pipeline gave %v; want the store's error", err)
	})
}
