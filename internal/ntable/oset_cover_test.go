package ntable

import (
	"context"
	"testing"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The ordered-set verbs of oset.go call through redis.Cmdable. These tests
// stand in the store's answers with the command types go-redis hands back,
// reusing the seeds read_cover_test.go already builds for them, so every
// path runs with no store, socket, subprocess or clock.

// osetCoverStore is the cmdable Add, Remove, Move, Card and MembersOf call
// through: it answers each command with the one the test seeds and keeps the
// last call, so a row pins what the verb sent to the store.
type osetCoverStore struct {
	redis.Cmdable
	add    *redis.IntCmd
	rem    *redis.IntCmd
	move   *redis.Cmd
	card   *redis.IntCmd
	zrange *redis.ZSliceCmd
	last   osetCoverCall
}

// osetCoverCall is the verb's last recording: the command name, the keys it
// named and the plain args that followed them.
type osetCoverCall struct {
	fn   string
	keys []string
	args []any
}

func (s *osetCoverStore) ZAdd(_ context.Context, key string, zs ...redis.Z) *redis.IntCmd {
	members := make([]any, 0, len(zs))
	for _, z := range zs {
		members = append(members, z)
	}
	s.last = osetCoverCall{fn: "ZAdd", keys: []string{key}, args: members}
	return s.add
}

func (s *osetCoverStore) ZRem(_ context.Context, key string, ms ...any) *redis.IntCmd {
	s.last = osetCoverCall{fn: "ZRem", keys: []string{key}, args: ms}
	return s.rem
}

func (s *osetCoverStore) FCall(_ context.Context, fn string, keys []string, args ...any) *redis.Cmd {
	s.last = osetCoverCall{fn: fn, keys: keys, args: args}
	return s.move
}

func (s *osetCoverStore) ZCard(_ context.Context, key string) *redis.IntCmd {
	s.last = osetCoverCall{fn: "ZCard", keys: []string{key}}
	return s.card
}

func (s *osetCoverStore) ZRangeWithScores(_ context.Context, key string, start, stop int64) *redis.ZSliceCmd {
	s.last = osetCoverCall{fn: "ZRangeWithScores", keys: []string{key}, args: []any{start, stop}}
	return s.zrange
}

// TestOsetCoverAdd pins Add: the member rides one ZADD at its score under
// the key, and a store that did not answer the ZADD is a refusal naming
// both.
func TestOsetCoverAdd(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		answer *redis.IntCmd
	}{
		{"main path: ZADD takes the member at its score", coverCount(1)},
		{"refusal: a ZADD that did not come back names key and member", coverFailedInt(assert.AnError)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := &osetCoverStore{add: tc.answer}
			err := Add(context.Background(), s, "table:demo:cell:r1:todo", "a", 2.5)
			assert.Equal(t, "ZAdd", s.last.fn)
			assert.Equal(t, []string{"table:demo:cell:r1:todo"}, s.last.keys)
			assert.Equal(t, []any{redis.Z{Score: 2.5, Member: "a"}}, s.last.args, "one ZADD of the member at the score")
			if tc.answer.Err() != nil {
				assert.ErrorContains(t, err, `zadd "table:demo:cell:r1:todo" member "a": `)
				require.ErrorIs(t, err, assert.AnError)
				return
			}
			assert.NoError(t, err)
		})
	}
}

// TestOsetCoverRemove pins Remove: the member rides one ZREM out of the
// key, and a store that did not answer the ZREM is a refusal naming both.
func TestOsetCoverRemove(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		answer *redis.IntCmd
	}{
		{"main path: ZREM takes the member out", coverCount(1)},
		{"refusal: a ZREM that did not come back names key and member", coverFailedInt(assert.AnError)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := &osetCoverStore{rem: tc.answer}
			err := Remove(context.Background(), s, "table:demo:cell:r1:todo", "a")
			assert.Equal(t, "ZRem", s.last.fn)
			assert.Equal(t, []string{"table:demo:cell:r1:todo"}, s.last.keys)
			assert.Equal(t, []any{"a"}, s.last.args, "one ZREM of the member")
			if tc.answer.Err() != nil {
				assert.ErrorContains(t, err, `zrem "table:demo:cell:r1:todo" member "a": `)
				require.ErrorIs(t, err, assert.AnError)
				return
			}
			assert.NoError(t, err)
		})
	}
}

// TestOsetCoverMove pins Move: one library call names both sets and carries
// the member with the new score's decimal text or keepScore's empty
// stand-in, NOTMEMBER answers ErrNotMember with its remedy, another refusal
// answers the store's word, and a call that did not come back is a failure
// naming the way from to to.
func TestOsetCoverMove(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name      string
		keepScore bool
		score     float64
		move      *redis.Cmd
		refused   string
	}{
		{
			name: "main path: keeping the score sends an empty score arg", keepScore: true,
			move: coverRead([]any{"OK"}, nil),
		},
		{
			name:  "main path: a new score rides the call as its decimal text",
			move:  coverRead([]any{"OK"}, nil),
			score: 2.5,
		},
		{
			name:      "refusal: NOTMEMBER is ErrNotMember with the ZRANGE remedy",
			keepScore: true, move: coverRead([]any{"REFUSED", "NOTMEMBER"}, nil),
			refused: "NOTMEMBER",
		},
		{
			name:      "refusal: another refusal answers the store's word",
			keepScore: true, move: coverRead([]any{"REFUSED", "WRONGTYPE"}, nil),
			refused: "REFUSED WRONGTYPE",
		},
		{
			name:      "refusal: a call that did not come back names both sets",
			keepScore: true, move: coverRead(nil, assert.AnError),
			refused: "from -> to",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := &osetCoverStore{move: tc.move}
			err := Move(context.Background(), s, "from", "to", "m", tc.keepScore, tc.score)
			assert.Equal(t, FnMove, s.last.fn, "one library call of the move function")
			assert.Equal(t, []string{"from", "to"}, s.last.keys, "the call names both sets")
			if tc.refused != "" {
				assert.ErrorContains(t, err, tc.refused)
				if tc.refused == "NOTMEMBER" {
					assert.ErrorIs(t, err, ErrNotMember)
					assert.ErrorContains(t, err, `ordered set "from" -> "to" member "m"`)
					assert.ErrorContains(t, err, "inspect the source with ZRANGE before retrying the move")
				}
				return
			}
			require.NoError(t, err)
			want := ""
			if !tc.keepScore {
				want = "2.5"
			}
			assert.Equal(t, []any{"m", want}, s.last.args, "the member and the score's text or its empty stand-in")
		})
	}
}

// TestOsetCoverCard pins Card: the ZCARD's count is the cardinality, and a
// store that did not answer it is a refusal naming the key, with no count.
func TestOsetCoverCard(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		answer *redis.IntCmd
	}{
		{"main path: the ZCARD's count is the cardinality", coverCount(3)},
		{"refusal: a ZCARD that did not come back names the key", coverFailedInt(assert.AnError)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := &osetCoverStore{card: tc.answer}
			n, err := Card(context.Background(), s, "table:demo:cell:r1:todo")
			assert.Equal(t, "ZCard", s.last.fn)
			assert.Equal(t, []string{"table:demo:cell:r1:todo"}, s.last.keys, "the count reads the one key")
			if tc.answer.Err() != nil {
				assert.Zero(t, n, "a failed count is no number")
				assert.ErrorContains(t, err, "zcard table:demo:cell:r1:todo: ")
				require.ErrorIs(t, err, assert.AnError)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, int64(3), n)
		})
	}
}

// TestOsetCoverMembersOf pins MembersOf: the ZRANGE WITHSCORES of the whole
// set reads as members in score order, every member printed whatever the
// reply typed it, and a store that did not answer it is a refusal naming
// the key, with no members.
func TestOsetCoverMembersOf(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		answer *redis.ZSliceCmd
		want   []Member
	}{
		{
			name:   "main path: the set reads as members in score order",
			answer: coverZS(redis.Z{Score: 1, Member: "a"}, redis.Z{Score: 2.5, Member: 42}),
			want:   []Member{{"a", 1}, {"42", 2.5}},
		},
		{
			name:   "refusal: a ZRANGE that did not come back names the key",
			answer: coverFailedZS(assert.AnError),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := &osetCoverStore{zrange: tc.answer}
			ms, err := MembersOf(context.Background(), s, "table:demo:cell:r1:names")
			assert.Equal(t, "ZRangeWithScores", s.last.fn)
			assert.Equal(t, []string{"table:demo:cell:r1:names"}, s.last.keys)
			assert.Equal(t, []any{int64(0), int64(-1)}, s.last.args, "the whole set, first to last")
			if tc.answer.Err() != nil {
				assert.Nil(t, ms)
				assert.ErrorContains(t, err, "zrange table:demo:cell:r1:names: ")
				require.ErrorIs(t, err, assert.AnError)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, ms, "every member printed with its score")
		})
	}
}

// TestOsetCoverCountCmdVal pins CountCmd.Val: the queued count reads as
// Result's number, an excluded member present comes off it and one absent
// (the store's redis.Nil) does not, and a count that did not come back is 0.
func TestOsetCoverCountCmdVal(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		pipe    *coverPipe
		exclude string
		want    int64
	}{
		{
			name:    "main path: an excluded member present comes off the count",
			pipe:    &coverPipe{zcard: map[string]*redis.IntCmd{"k": coverCount(4)}, zscore: map[string]*redis.FloatCmd{"k": coverScore(1.5)}},
			exclude: "stop",
			want:    3,
		},
		{
			name:    "main path: an excluded member absent leaves the count alone",
			pipe:    &coverPipe{zcard: map[string]*redis.IntCmd{"k": coverCount(4)}},
			exclude: "gone",
			want:    4,
		},
		{
			name: "refusal: a count that did not come back is 0",
			pipe: &coverPipe{zcard: map[string]*redis.IntCmd{"k": coverFailedInt(assert.AnError)}},
			want: 0,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, QueueCount(context.Background(), tc.pipe, "k", tc.exclude).Val())
		})
	}
}
