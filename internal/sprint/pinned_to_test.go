package sprint

import "testing"

// A hard pin released past its bound and dealt to another friend is that friend's ordinary card:
// it is pinned to the friend it names, and to no other.
func TestAHardPinIsPinnedToTheFriendItNamesOnly(t *testing.T) {
	t.Parallel()
	pin := &Card{ID: "p1-1", Fields: map[string]string{FieldWho: "only.friend.amy"}}
	if !pinnedTo(pin, "amy") {
		t.Fatal("a hard pin to amy is amy's pin")
	}
	if pinnedTo(pin, "bea") {
		t.Fatal("a hard pin to amy dealt to bea is not bea's pin: her start bound and level may move it")
	}
	free := &Card{ID: "p1-2", Fields: map[string]string{}}
	if pinnedTo(free, "amy") {
		t.Fatal("a card with no pin is nobody's pin")
	}
}
