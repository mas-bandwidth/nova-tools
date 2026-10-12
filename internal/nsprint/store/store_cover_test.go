package store

import (
	"context"
	"errors"
	"net"
	"testing"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

// TestStoreCoverNoUserHint pins the refusal the fleet needs when the default
// user is off: one line naming the unset ACL user variable, the password pair
// and where the password comes from.
func TestStoreCoverNoUserHint(t *testing.T) {
	t.Parallel()

	hint := NoUserHint()
	require.Contains(t, hint, UserEnv+" is unset")
	require.Contains(t, hint, DefaultPasswordEnv+" is set")
	require.Contains(t, hint, UserEnv+"=bench and "+DefaultPasswordEnv)
	require.Contains(t, hint, "nova-secrets exec --only "+DefaultPasswordEnv)
}

// TestStoreCoverHookWrap pins the wrapped refusal: it keeps the original
// NOAUTH and the address and appends the no-user hint.
func TestStoreCoverHookWrap(t *testing.T) {
	t.Parallel()

	got := noUserHook{addr: "store.test:6379"}.wrap(errors.New("NOAUTH Authentication required"))
	require.ErrorContains(t, got, "store.test:6379")
	require.ErrorContains(t, got, "NOAUTH Authentication required")
	require.ErrorContains(t, got, NoUserHint())
}

// TestStoreCoverDialHook pins that the hook does not replace the dialer: the
// connect is left to go-redis.
func TestStoreCoverDialHook(t *testing.T) {
	t.Parallel()

	want := func(context.Context, string, string) (net.Conn, error) { return nil, nil }
	got := noUserHook{addr: "store.test:6379"}.DialHook(want)
	require.NotNil(t, got)
	conn, err := got(context.Background(), "tcp", "store.test:6379")
	require.NoError(t, err)
	require.Nil(t, conn)
}

// TestStoreCoverProcessHook covers one command's path: a clean command passes
// through untouched, a NOAUTH refusal is wrapped on the returned error and on
// the command, and any other error is left alone.
func TestStoreCoverProcessHook(t *testing.T) {
	t.Parallel()

	noauth := errors.New("NOAUTH Authentication required")
	other := errors.New("ERR boom")
	cases := []struct {
		name    string
		nextErr error
		wrapped bool
	}{
		{name: "a clean command passes through", nextErr: nil},
		{name: "NOAUTH is wrapped with the hint", nextErr: noauth, wrapped: true},
		{name: "another error is left alone", nextErr: other},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()
			cmd := redis.NewCmd(ctx, "GET", "k")
			hook := noUserHook{addr: "store.test:6379"}.ProcessHook(func(context.Context, redis.Cmder) error {
				return tc.nextErr
			})
			err := hook(ctx, cmd)
			switch {
			case tc.nextErr == nil:
				require.NoError(t, err)
				require.NoError(t, cmd.Err())
			case tc.wrapped:
				require.ErrorContains(t, err, NoUserHint())
				require.ErrorContains(t, err, "store.test:6379")
				require.ErrorContains(t, cmd.Err(), NoUserHint())
			default:
				require.ErrorIs(t, err, other)
				require.NoError(t, cmd.Err())
			}
		})
	}
}

// TestStoreCoverProcessPipelineHook covers a batch: a clean batch passes
// through, a refused batch is wrapped on the returned error, and a command
// refused inside an otherwise clean batch is wrapped on that command alone.
func TestStoreCoverProcessPipelineHook(t *testing.T) {
	t.Parallel()

	noauth := errors.New("NOAUTH Authentication required")
	cases := []struct {
		name       string
		nextErr    error
		cmdNoAuth  bool
		wantReturn bool
		wantCmds   bool
	}{
		{name: "a clean batch passes through"},
		{name: "a refused batch is wrapped", nextErr: noauth, wantReturn: true},
		{name: "a command refused inside the batch is wrapped", cmdNoAuth: true, wantCmds: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()
			cmds := []redis.Cmder{redis.NewCmd(ctx, "GET", "k"), redis.NewCmd(ctx, "GET", "j")}
			if tc.cmdNoAuth {
				for _, cmd := range cmds {
					cmd.SetErr(noauth)
				}
			}
			hook := noUserHook{addr: "store.test:6379"}.ProcessPipelineHook(func(context.Context, []redis.Cmder) error {
				return tc.nextErr
			})
			err := hook(ctx, cmds)
			if !tc.wantReturn {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, NoUserHint())
			}
			for _, cmd := range cmds {
				if tc.wantCmds {
					require.ErrorContains(t, cmd.Err(), NoUserHint())
				} else if !tc.cmdNoAuth {
					require.NoError(t, cmd.Err())
				}
			}
		})
	}
}

// TestStoreCoverIsNoAuth pins the refusal test: nil and any error that does
// not carry NOAUTH are not a refusal.
func TestStoreCoverIsNoAuth(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil is not a refusal", err: nil, want: false},
		{name: "NOAUTH is a refusal", err: errors.New("NOAUTH Authentication required"), want: true},
		{name: "another error is not a refusal", err: errors.New("ERR boom"), want: false},
		{name: "a lowercase mention is not a refusal", err: errors.New("noauth"), want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, isNoAuth(tc.err))
		})
	}
}
