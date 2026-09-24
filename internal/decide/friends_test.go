package decide

import (
	"context"
	"reflect"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func TestFriendRungs(t *testing.T) {
	reg, err := DefaultRegistry()
	if err != nil {
		t.Fatalf("DefaultRegistry(): %v", err)
	}
	friends := reg.FriendRungs()
	var names []string
	for _, m := range friends {
		names = append(names, m.Name)
	}
	want := []string{"emma", "freddy", "johnny", "astra", "fable"}
	if !reflect.DeepEqual(names, want) {
		t.Errorf("FriendRungs() = %v, want %v", names, want)
	}
}

func TestReadDownFriends(t *testing.T) {
	reg, err := DefaultRegistry()
	if err != nil {
		t.Fatalf("DefaultRegistry(): %v", err)
	}

	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer rdb.Close()

	ctx := context.Background()

	// Initially no friends are down.
	downSet, downList, err := ReadDownFriends(ctx, rdb, reg)
	if err != nil {
		t.Fatalf("ReadDownFriends unexpected error: %v", err)
	}
	if len(downSet) != 0 || len(downList) != 0 {
		t.Fatalf("expected empty down set and list, got set=%v list=%v", downSet, downList)
	}

	// Set emma and freddy down.
	mr.Set("friend:emma:down", "rate-limit")
	mr.Set("friend:freddy:down", "1")

	downSet, downList, err = ReadDownFriends(ctx, rdb, reg)
	if err != nil {
		t.Fatalf("ReadDownFriends unexpected error: %v", err)
	}
	if !downSet["emma"] || !downSet["freddy"] {
		t.Errorf("expected emma and freddy in downSet, got %v", downSet)
	}
	if downSet["astra"] || downSet["fable"] || downSet["johnny"] {
		t.Errorf("did not expect astra/fable/johnny in downSet, got %v", downSet)
	}
	wantList := []string{"emma:down", "freddy:down"}
	if !reflect.DeepEqual(downList, wantList) {
		t.Errorf("downList = %v, want %v", downList, wantList)
	}

	// Test DownFriends helper with addr.
	downSet2, downList2, err := DownFriends(ctx, mr.Addr(), "", "", reg)
	if err != nil {
		t.Fatalf("DownFriends unexpected error: %v", err)
	}
	if !reflect.DeepEqual(downSet, downSet2) || !reflect.DeepEqual(downList, downList2) {
		t.Errorf("DownFriends mismatch: got (%v, %v), want (%v, %v)", downSet2, downList2, downSet, downList)
	}
}

func TestReadDownFriendsMapsSeatsAndSuppressesBroadcast(t *testing.T) {
	reg, err := DefaultRegistry()
	if err != nil {
		t.Fatal(err)
	}
	mr := miniredis.RunT(t)
	for _, seat := range []string{"emma", "freddy", "johnny", "stella", "rowan"} {
		mr.Set("friend:"+seat+":down", "1")
	}
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer rdb.Close()
	down, list, err := ReadDownFriends(context.Background(), rdb, reg)
	if err != nil {
		t.Fatal(err)
	}
	for _, rung := range []string{"emma", "freddy", "johnny", "astra", "fable", "all-friends"} {
		if !down[rung] {
			t.Errorf("%s not excluded: %v", rung, down)
		}
	}
	want := []string{"emma:down", "freddy:down", "johnny:down", "rowan:down", "stella:down"}
	if !reflect.DeepEqual(list, want) {
		t.Errorf("list=%v want=%v", list, want)
	}
}
