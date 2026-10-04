package seatcred

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The unit cover of the three seatcred.go functions the unit tier never
// reached (a reader's finding: defaultResolver, the package Addr and
// SelectProfile, each at 0.0%). SelectProfile and Addr are reached through a
// Selection the test owns and the resolver a row names. defaultResolver is
// reached through its refusal: its one statement resolves through the
// process's own environment, so its success path needs a live store or a
// subprocess, which this card forbids; the refusal covers the whole function,
// which has no branches. No sleep, no real time, no network, no subprocess,
// no live store, and no existing file touched.

func TestSeatcredCoverDefaultResolverRefusesAnInvalidSeat(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name string
		seat string
		want string
	}{
		{name: "empty", seat: "", want: `seat "": must match [A-Za-z0-9_-]+`},
		{name: "a path", seat: "../studio", want: `seat "../studio": must match [A-Za-z0-9_-]+`},
		{name: "a space", seat: "studio air", want: `seat "studio air": must match [A-Za-z0-9_-]+`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c, err := defaultResolver(tt.seat)
			require.Error(t, err, "defaultResolver(%q) = %v, %v; want the name refusal Resolve gives", tt.seat, c, err)
			assert.Equal(t, tt.want, err.Error(), "defaultResolver(%q) = %v, %v; want the name refusal Resolve gives", tt.seat, c, err)
		})
	}
}

// TestSeatcredCoverAddrReadsTheProcessSelection reaches the package Addr. No
// test selects an address on the shared process -- TestFromArgs selects seats,
// whose address is empty -- so a non-empty package Addr here would mutate that
// shared process and race TestFromArgs; the cover takes the empty path, and the
// non-empty path of the same Selection.Addr is pinned on an owned selection by
// TestSelectWithResolvesThroughTheGivenFunc.
func TestSeatcredCoverAddrReadsTheProcessSelection(t *testing.T) {
	t.Parallel()

	assert.Empty(t, Addr(), "Addr() = %q; want empty with no address on this process's selection", Addr())
}

func TestSeatcredCoverSelectProfileResolvesTheRow(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name     string
		profile  Profile
		resolve  func(string) (Cred, error)
		wantSeat string
		wantAddr string
		wantErr  bool
	}{
		{
			name:     "the row's seat and address",
			profile:  Profile{Name: "studio", Addr: "redis.invalid:6380"},
			resolve:  func(seat string) (Cred, error) { return Cred{Seat: seat, User: "coordinator"}, nil },
			wantSeat: "studio",
			wantAddr: "redis.invalid:6380",
		},
		{
			name:     "a row with no address",
			profile:  Profile{Name: "air"},
			resolve:  func(seat string) (Cred, error) { return Cred{Seat: seat, User: "bench"}, nil },
			wantSeat: "air",
			wantAddr: "",
		},
		{
			name:     "the resolver's refusal",
			profile:  Profile{Name: "ghost", Addr: "h:1"},
			resolve:  func(string) (Cred, error) { return Cred{}, errors.New("seat ghost: no password") },
			wantSeat: "ghost",
			wantAddr: "h:1",
			wantErr:  true,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var s Selection
			s.SelectProfile(tt.profile, tt.resolve)
			assert.Equal(t, tt.wantSeat, s.Selected(), "SelectProfile selected %q; want %q", s.Selected(), tt.wantSeat)
			assert.Equal(t, tt.wantAddr, s.Addr(), "SelectProfile addr %q; want %q", s.Addr(), tt.wantAddr)
			_, ok, err := s.Active()
			require.True(t, ok, "Active reported no seat after SelectProfile")
			if tt.wantErr {
				assert.Error(t, err, "Active err %v; want the resolver's refusal", err)
			} else {
				assert.NoError(t, err, "Active err %v; want the resolved login", err)
			}
		})
	}
}
