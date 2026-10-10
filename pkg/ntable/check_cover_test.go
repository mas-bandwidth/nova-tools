package ntable_test

import (
	"context"
	"errors"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/ntable"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// errTransport stands in for a store that did not answer, so the error Check
// and MemberFind wrap can be matched with errors.Is without a fresh sentinel.
var errTransport = errors.New("store did not answer")

// fcallROFake is a redis.Cmdable that returns one canned reply from every
// FCallRO, so Check and MemberFind can be driven at the unit tier without a
// store. They consult only FCallRO; the embedded interface satisfies Cmdable
// for the methods they never call.
type fcallROFake struct {
	redis.Cmdable
	fn    string
	keys  []string
	args  []any
	reply []any
	err   error
}

func (f *fcallROFake) FCallRO(ctx context.Context, fn string, keys []string, args ...any) *redis.Cmd {
	f.fn = fn
	f.keys = keys
	cp := make([]any, len(args))
	copy(cp, args)
	f.args = cp
	cmd := redis.NewCmd(ctx)
	if f.err != nil {
		cmd.SetErr(f.err)
	} else {
		cmd.SetVal(f.reply)
	}
	return cmd
}

// TestCheckCoverCheck drives Check through the package's redis.Cmdable seam
// with a fake store: the audit report path, a store refusal, a transport fault
// and a malformed reply. Each case also pins the single FCallRO Check makes.
func TestCheckCoverCheck(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	const table = "demo"
	cases := []struct {
		name          string
		reply         []any
		cmdErr        error
		wantReport    ntable.CheckReport
		wantErr       error // matched with errors.Is; nil skips the assertion
		expectError   bool  // true expects some error with no sentinel
		wantIsRefusal bool
	}{
		{
			name:       "a check report parses into the typed result",
			reply:      []any{"CHECK", "1", "2", "3", "4"},
			wantReport: ntable.CheckReport{Epoch: 1, Revision: 2, Members: 3, Cells: 4},
		},
		{
			name:          "a missing table is surfaced as a typed refusal",
			reply:         []any{"REFUSED", "NOTABLE"},
			wantErr:       ntable.ErrNoTable,
			wantIsRefusal: true,
		},
		{
			name:    "a store that did not answer leaves a wrapped fault",
			cmdErr:  errTransport,
			wantErr: errTransport,
		},
		{
			name:          "a malformed check reply is a parse error, not a refusal",
			reply:         []any{"CHECK"},
			expectError:   true,
			wantIsRefusal: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := &fcallROFake{reply: tc.reply, err: tc.cmdErr}
			report, err := ntable.Check(ctx, f, table)
			assert.Equal(t, ntable.FnCheck, f.fn, "Check should call %q, got %q", ntable.FnCheck, f.fn)
			assert.Equal(t, []string{ntable.DefKey(table)}, f.keys, "Check keys = %v", f.keys)
			assert.Equal(t, []any{table}, f.args, "Check args = %v", f.args)
			switch {
			case tc.wantErr != nil:
				require.Error(t, err)
				assert.ErrorIs(t, err, tc.wantErr)
			case tc.expectError:
				require.Error(t, err)
			default:
				require.NoError(t, err)
			}
			if tc.wantIsRefusal {
				assert.True(t, ntable.IsRefusal(err), "want a refusal, got %v", err)
			} else if err != nil {
				assert.False(t, ntable.IsRefusal(err), "want a non-refusal error, got %v", err)
			}
			assert.Equal(t, tc.wantReport, report)
		})
	}
}

// TestCheckCoverMemberFind drives MemberFind through the package's
// redis.Cmdable seam with a fake store: a placed member, a missing member, a
// refusal for a member of another epoch, and a malformed reply. Each case also
// pins the single FCallRO MemberFind makes.
func TestCheckCoverMemberFind(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	const (
		table  = "demo"
		member = "m1"
	)
	cases := []struct {
		name          string
		reply         []any
		cmdErr        error
		wantLocation  ntable.MemberLocation
		wantErr       error // matched with errors.Is; nil skips the assertion
		expectError   bool  // true expects some error with no sentinel
		wantIsRefusal bool
	}{
		{
			name:         "a placed member reads its cell",
			reply:        []any{"MEMBER", "1", "2", "placed", "build", "ready"},
			wantLocation: ntable.MemberLocation{Epoch: 1, Revision: 2, State: "placed", Row: "build", Column: "ready"},
		},
		{
			name:         "an unknown member reads as missing",
			reply:        []any{"MEMBER", "1", "2", "missing", "", ""},
			wantLocation: ntable.MemberLocation{Epoch: 1, Revision: 2, State: "missing"},
		},
		{
			name:          "a member of another epoch is a typed refusal",
			reply:         []any{"REFUSED", "MEMBEREPOCH", member, "0", "1"},
			wantErr:       ntable.ErrMemberEpoch,
			wantIsRefusal: true,
		},
		{
			name:          "a malformed member reply is a parse error, not a refusal",
			reply:         []any{"MEMBER", "1", "2", "bogus", "", ""},
			expectError:   true,
			wantIsRefusal: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := &fcallROFake{reply: tc.reply, err: tc.cmdErr}
			loc, err := ntable.MemberFind(ctx, f, table, member)
			assert.Equal(t, ntable.FnMemberFind, f.fn, "MemberFind should call %q, got %q", ntable.FnMemberFind, f.fn)
			assert.Equal(t, []string{ntable.DefKey(table)}, f.keys, "MemberFind keys = %v", f.keys)
			assert.Equal(t, []any{table, member}, f.args, "MemberFind args = %v", f.args)
			switch {
			case tc.wantErr != nil:
				require.Error(t, err)
				assert.ErrorIs(t, err, tc.wantErr)
			case tc.expectError:
				require.Error(t, err)
			default:
				require.NoError(t, err)
			}
			if tc.wantIsRefusal {
				assert.True(t, ntable.IsRefusal(err), "want a refusal, got %v", err)
			} else if err != nil {
				assert.False(t, ntable.IsRefusal(err), "want a non-refusal error, got %v", err)
			}
			assert.Equal(t, tc.wantLocation, loc)
		})
	}
}
