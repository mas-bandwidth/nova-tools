// epoch.go: THE SPRINT EPOCH (nova-tools#4238; Glenn 2026-09-26 9:25 AM ET:
// "have a uint64 sequence number that increments, thus, if the numbers from
// the async things are not the same sequence as current table view, the
// display is zero").
//
// sprint:epoch is a hash whose field n is the uint64 (absent: 0); the
// table's tick reads the keyspace through HGET and never GET
// (TestTableTickMakesNoRestCall), so the number is a hash field, and the
// clear's receipt (at, by, why, from, cards, ...) rides the same hash.
// Every set that drives a table is named by the epoch it was written
// under, so a reader that keys by the current epoch cannot see an older
// epoch's members: not "cleared later", invisible by construction.
// `sprint clear` is one HINCRBY of sprint:epoch n; nothing is moved or
// deleted.
//
// The rule, one function per set kind, the same in fn/lua/02_card_move.lua
// (cm_ckey, cm_wskey, cm_skey):
//
//	epoch 0  <consumer>:cards:<col>      ws:<s>:<w>      s:<S>:<pool|waiting>
//	epoch e  <consumer>:<e>:cards:<col>  ws:<e>:<s>:<w>  s:<S>:<e>:<pool|waiting>
//
// Epoch 0 is the name every set had before the first clear, so a store
// that has never been cleared reads as it did and needs no migration; the
// first clear moves every reader to epoch 1 and the old names never show
// again.
package ws

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/redis/go-redis/v9"
)

// EpochKey is the hash holding the sprint epoch in EpochField (a uint64,
// absent until the first clear) and the last clear's receipt.
const EpochKey = "sprint:epoch"

// EpochField is the epoch itself: HGET sprint:epoch n.
const EpochField = "n"

// Epoch reads the current sprint epoch: 0 when the field is absent. A value
// that is not a uint64 is an error, never 0 (a reader would show the wrong
// epoch's cells).
func Epoch(ctx context.Context, c redis.Cmdable) (uint64, error) {
	v, err := c.HGet(ctx, EpochKey, EpochField).Result()
	if errors.Is(err, redis.Nil) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("hget %s %s: %w", EpochKey, EpochField, err)
	}
	return ParseEpoch(v)
}

// ParseEpoch reads an epoch as a reader found it: "" is 0.
func ParseEpoch(v string) (uint64, error) {
	if v == "" {
		return 0, nil
	}
	n, err := strconv.ParseUint(v, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s %s is %q, not a uint64", EpochKey, EpochField, v)

	}
	return n, nil
}

// KeyAt is a stream's set at one state under epoch e (Key at epoch 0).
func KeyAt(e uint64, stream, state string) string {
	if e == 0 {
		return key0(stream, state)
	}

	return "ws:" + strconv.FormatUint(e, 10) + ":" + stream + ":" + state
}

// ConsumerKeyAt is a consumer's (bench:<b> or friend:<f>) set at col
// (ready, working, ok, fail, done, ...) under epoch e.
func ConsumerKeyAt(e uint64, consumer, col string) string {
	if e == 0 {
		return consumer + ":cards:" + col
	}
	return consumer + ":" + strconv.FormatUint(e, 10) + ":cards:" + col
}

// SprintListAt is the dealer's list of sprint S (pool or waiting) under
// epoch e.
func SprintListAt(e uint64, sprint, list string) string {
	if e == 0 {
		return "s:" + sprint + ":" + list
	}
	return "s:" + sprint + ":" + strconv.FormatUint(e, 10) + ":" + list
}

// The read-only cell functions (fn/lua/02_card_move.lua): a reader that
// must stay at one pipeline counts a cell through FCALL_RO in that
// pipeline, the epoch read atomically with the count; every other reader
// reads the epoch (Epoch, or an HGet in an earlier round) and keys by it.
const (
	FnCellCard    = "ns_cell_zcard"
	FnStreamRange = "ns_ws_zrange"
)

// CellCard is ZCARD of consumer's (bench:<b> or friend:<f>) set at col
// under the current epoch, as one FCALL_RO; CardVal reads it.
func CellCard(ctx context.Context, c redis.Cmdable, consumer, col string) *redis.Cmd {
	return c.FCallRO(ctx, FnCellCard, nil, consumer, col)
}

// CardVal is a CellCard count; a count that could not be read is 0, as
// IntCmd.Val would be.
func CardVal(cmd *redis.Cmd) int64 {
	n, err := cmd.Int64()
	if err != nil {
		return 0
	}
	return n
}

// StreamRange is ZRANGE of stream's set at state under the current epoch,
// oldest first, as one FCALL_RO; IDs reads it.
func StreamRange(ctx context.Context, c redis.Cmdable, stream, state string) *redis.Cmd {
	return c.FCallRO(ctx, FnStreamRange, nil, stream, state)
}

// StreamRangeWithScores is StreamRange with each member's score; Zs reads it.
func StreamRangeWithScores(ctx context.Context, c redis.Cmdable, stream, state string) *redis.Cmd {
	return c.FCallRO(ctx, FnStreamRange, nil, stream, state, "withscores")
}

// IDs is a StreamRange reply as members.
func IDs(cmd *redis.Cmd) ([]string, error) {
	return cmd.StringSlice()
}

// Zs is a StreamRangeWithScores reply as members with scores.
func Zs(cmd *redis.Cmd) ([]redis.Z, error) {
	flat, err := cmd.StringSlice()
	if err != nil {
		return nil, err
	}
	if len(flat)%2 != 0 {
		return nil, fmt.Errorf("%s: %d values, not member/score pairs", FnStreamRange, len(flat))
	}
	out := make([]redis.Z, 0, len(flat)/2)
	for i := 0; i+1 < len(flat); i += 2 {
		score, err := strconv.ParseFloat(flat[i+1], 64)
		if err != nil {
			return nil, fmt.Errorf("%s: score %q of %s: %w", FnStreamRange, flat[i+1], flat[i], err)
		}
		out = append(out, redis.Z{Score: score, Member: flat[i]})
	}
	return out, nil
}
