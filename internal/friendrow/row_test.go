package friendrow

// johnny has a beat and a width, so he is one row. emma has a width and a
// last-seen stamp but no beat, and stella is declared with nothing: neither
// is a presence. freddy has a beat and was not declared, so he is not a row
// either. The declaration is the names, one per friend, the way a bench-row
// is one bench.

import (
	"context"
	"errors"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

const beatStamp = "2026-09-22T21:43:00Z"

// mapStore is the beat keys a test wrote. A key it does not hold is "".
type mapStore map[string]string

func (m mapStore) MGet(_ context.Context, keys ...string) ([]string, error) {
	out := make([]string, len(keys))
	for i, k := range keys {
		out[i] = m[k]
	}
	return out, nil
}

type errStore struct{ err error }

func (s errStore) MGet(context.Context, ...string) ([]string, error) {
	return nil, s.err
}

func declared() []string { return []string{"Johnny", "johnny", "emma", "stella"} }

func beatFixture() mapStore {
	return mapStore{
		"friend:johnny":       beatStamp,
		"friend:johnny:width": "4",
		"friend:emma:width":   "2",
		"friend:emma:last":    "2026-09-22T21:00:00Z",
		"friend:freddy":       beatStamp,
		"friend:freddy:width": "1",
	}
}

func assertJohnnyRow(t *testing.T, rows []Row) {
	t.Helper()
	if len(rows) != 1 {
		t.Fatalf("rows = %+v, want one row; a missing beat must not invent a presence", rows)
	}
	want := Row{Name: "johnny", Beat: beatStamp, Width: 4, WidthOK: true}
	if rows[0] != want {
		t.Fatalf("row = %+v, want %+v", rows[0], want)
	}
}

// TestABeatAndAWidthAreOneRowAndAMissingBeatIsNotAPresence is the read.
// A declared friend with a beat and a width is one row. A declared friend
// with no beat is not given a presence from a width key, a last-seen stamp,
// or the fact of being named.
func TestABeatAndAWidthAreOneRowAndAMissingBeatIsNotAPresence(t *testing.T) {
	beat, err := BeatKey("Johnny")
	if err != nil || beat != "friend:johnny" {
		t.Fatalf("BeatKey(Johnny) = %q, %v; want friend:johnny", beat, err)
	}
	width, err := WidthKey("Johnny")
	if err != nil || width != "friend:johnny:width" {
		t.Fatalf("WidthKey(Johnny) = %q, %v; want friend:johnny:width", width, err)
	}

	rows, err := Read(context.Background(), beatFixture(), declared())
	if err != nil {
		t.Fatal(err)
	}
	assertJohnnyRow(t, rows)
}

// TestTheRedisReadIsTheSameRow proves the fleet client, not a second
// implementation: the same keys through MGET are the same one row, and
// emma's width without a beat is still not a presence.
func TestTheRedisReadIsTheSameRow(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	ctx := context.Background()
	for k, v := range beatFixture() {
		if err := rdb.Set(ctx, k, v, 0).Err(); err != nil {
			t.Fatalf("set %s: %v", k, err)
		}
	}
	rows, err := Read(ctx, NewRedis(rdb), declared())
	if err != nil {
		t.Fatal(err)
	}
	assertJohnnyRow(t, rows)
}

// TestABeatWithNoWidthIsStillOneRow: presence is the beat. A missing width
// is not zero children, and it does not drop the friend.
func TestABeatWithNoWidthIsStillOneRow(t *testing.T) {
	rows, err := Read(context.Background(), mapStore{
		"friend:stella":       beatStamp,
		"friend:stella:width": "many",
	}, []string{"stella"})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %+v, want stella's beat even though the width is not a count", rows)
	}
	if rows[0].WidthOK || rows[0].Width != 0 {
		t.Fatalf("width = %d ok=%v; a width that is not a count must not become zero", rows[0].Width, rows[0].WidthOK)
	}
	if rows[0].Name != "stella" || rows[0].Beat != beatStamp {
		t.Fatalf("row = %+v, want stella's beat", rows[0])
	}
}

// TestWidthZeroIsACount: zero children is a width the beat wrote, not a
// missing key.
func TestWidthZeroIsACount(t *testing.T) {
	rows, err := Read(context.Background(), mapStore{
		"friend:stella":       beatStamp,
		"friend:stella:width": "0",
	}, []string{"stella"})
	if err != nil {
		t.Fatal(err)
	}
	want := Row{Name: "stella", Beat: beatStamp, Width: 0, WidthOK: true}
	if len(rows) != 1 || rows[0] != want {
		t.Fatalf("rows = %+v, want %+v", rows, want)
	}
}

// TestNothingDeclaredReadsNothing: an empty declaration does not scan the
// store and does not turn whatever keys are there into friends.
func TestNothingDeclaredReadsNothing(t *testing.T) {
	rows, err := Read(context.Background(), errStore{err: errors.New("should not be called")}, nil)
	if err != nil {
		t.Fatalf("empty declaration: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("rows = %+v, want none", rows)
	}
}

// TestAStoreErrorIsNotAnEmptyRoom: a failed read is the error. It is not a
// table of friends who happen to be away.
func TestAStoreErrorIsNotAnEmptyRoom(t *testing.T) {
	rows, err := Read(context.Background(), errStore{err: errors.New("store down")}, []string{"johnny"})
	if err == nil {
		t.Fatal("a store error was dropped")
	}
	if rows != nil {
		t.Fatalf("rows = %+v; a failed read must not invent an empty room", rows)
	}
}

func TestADeclaredNameThatIsNotANameIsRefused(t *testing.T) {
	for _, name := range []string{"", " ", "emma stone", "friend:emma", "emma:width"} {
		if _, err := Read(context.Background(), beatFixture(), []string{name}); err == nil {
			t.Fatalf("name %q was accepted as a friend", name)
		}
	}
}

func TestAMissingRedisClientIsNotAPresence(t *testing.T) {
	rows, err := Read(context.Background(), NewRedis(nil), []string{"johnny"})
	if err == nil {
		t.Fatal("a missing client invented a read")
	}
	if rows != nil {
		t.Fatalf("rows = %+v; a missing client must not invent a presence", rows)
	}
}
