package seatcred_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/seatcred"
)

// TestSeatcredCoverProcessAddr is the package-level Addr: it answers this
// process's own Selection. No test of this package gives the process a seat
// with an address (Select and FromArgs clear the address, and only
// SelectWith names one), so the process's address is empty and Addr must
// agree with Process().Addr().
func TestSeatcredCoverProcessAddr(t *testing.T) {
	t.Parallel()

	require.Empty(t, seatcred.Addr(), "Addr() answered an address though no test selects a seat with one for the process")
	require.Equal(t, seatcred.Process().Addr(), seatcred.Addr(), "Addr() is not the process selection's address")
}

// TestSeatcredCoverSelectProfile is Selection.SelectProfile: a seats.tsv row
// selected through a resolver — the row's seat and address recorded, the
// resolver Active runs — and the resolver's refusal reaching the caller.
func TestSeatcredCoverSelectProfile(t *testing.T) {
	t.Parallel()

	p := seatcred.Profile{
		Name:      "cover-seat",
		Addr:      "h:1",
		User:      "cover",
		SecretEnv: "NOVA_REDIS_COVER_PASSWORD",
		Store:     filepath.Join(string(filepath.Separator), "srv", "cover-store"),
		Key:       filepath.Join(string(filepath.Separator), "srv", "keys", "cover.key"),
	}
	t.Run("row-selected-and-resolved-once-through-the-given-func", func(t *testing.T) {
		s := new(seatcred.Selection)
		calls := 0
		s.SelectProfile(p, func(seat string) (seatcred.Cred, error) {
			calls++
			return seatcred.Cred{Seat: seat, User: "cover"}, nil
		})
		require.Equal(t, "cover-seat", s.Selected(), "SelectProfile recorded seat %q, want the row's cover-seat", s.Selected())
		require.Equal(t, "h:1", s.Addr(), "SelectProfile recorded addr %q, want the row's h:1", s.Addr())
		cred, ok, err := s.Active()
		require.True(t, ok, "Active = %v ok=%v err=%v; want the row's seat active", cred, ok, err)
		require.NoError(t, err, "Active = %v ok=%v err=%v; want the row's seat active", cred, ok, err)
		require.Equal(t, "cover", cred.User, "Active = %v ok=%v err=%v; want the row's seat active", cred, ok, err)
		_, _, _ = s.Active()
		require.Equal(t, 1, calls, "Active resolved %d times, want once for the life of the selection", calls)
	})
	t.Run("resolver-refusal-is-Actives", func(t *testing.T) {
		s := new(seatcred.Selection)
		s.SelectProfile(p, func(string) (seatcred.Cred, error) {
			return seatcred.Cred{}, errors.New("cover-seat: holds no NOVA_REDIS_COVER_PASSWORD; seal it with nova-secrets seal")
		})
		cred, ok, err := s.Active()
		require.True(t, ok, "Active = %v ok=%v err=%v; want the refusal of the selected seat", cred, ok, err)
		require.Error(t, err, "Active = %v ok=%v err=%v; want the refusal of the selected seat", cred, ok, err)
		assert.Contains(t, err.Error(), "NOVA_REDIS_COVER_PASSWORD", "Active's refusal = %v; want it to name the key the row wants", err)
	})
}

// TestSeatcredCoverDefaultResolver reaches the resolver a Selection runs when
// Select named a seat and nothing was injected: Resolve with the process's
// own environment. Both cases refuse in the test process itself — a seat
// name Resolve rejects, and a store the environment names that is not one —
// so no store, sops or child is needed; the full success of the resolver
// belongs to the sealed-store tests, which run it through the hermetic child.
func TestSeatcredCoverDefaultResolver(t *testing.T) {
	t.Parallel()

	t.Run("refuses-a-seat-name-nothing-can-seal", func(t *testing.T) {
		s := new(seatcred.Selection)
		s.Select("cover bad/name")
		cred, ok, err := s.Active()
		require.True(t, ok, "Active = %v ok=%v err=%v; want the refusal of the selected seat", cred, ok, err)
		require.Error(t, err, "Active = %v ok=%v err=%v; want the refusal of the selected seat", cred, ok, err)
		assert.Contains(t, err.Error(), "must match [A-Za-z0-9_-]+", "Active's refusal = %v; want it to name the seat-name rule", err)
	})
	t.Run("reads-NOVA_SECRETS_STORE-from-the-process-environment", func(t *testing.T) {
		for _, k := range []string{seatcred.StoreEnv, seatcred.KeyEnv, seatcred.SopsEnv} {
			prev, had := os.LookupEnv(k)
			t.Cleanup(func() {
				if had {
					_ = os.Setenv(k, prev) // ignored: cleanup restores the process's own value; Setenv of a known-good pair cannot fail here
				} else {
					_ = os.Unsetenv(k) // ignored: cleanup restores the process's own value; Unsetenv cannot fail here
				}
			})
		}
		missing := filepath.Join(t.TempDir(), "no-such-store")
		require.NoError(t, os.Setenv(seatcred.StoreEnv, missing))
		require.NoError(t, os.Setenv(seatcred.KeyEnv, filepath.Join(t.TempDir(), "cover.key")))
		require.NoError(t, os.Setenv(seatcred.SopsEnv, filepath.Join(t.TempDir(), "no-sops")))
		s := new(seatcred.Selection)
		s.Select("cover-probe-seat")
		cred, ok, err := s.Active()
		require.True(t, ok, "Active = %v ok=%v err=%v; want the store refusal of the selected seat", cred, ok, err)
		require.Error(t, err, "Active = %v ok=%v err=%v; want the store refusal of the selected seat", cred, ok, err)
		assert.Contains(t, err.Error(), "cover-probe-seat", "Active's refusal = %v; want it to name the seat", err)
		assert.Contains(t, err.Error(), "store "+missing+" is not a directory", "Active's refusal = %v; want it to name the store %s reads from the process environment", err, seatcred.StoreEnv)
	})
}
