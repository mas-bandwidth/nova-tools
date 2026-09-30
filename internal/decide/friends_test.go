package decide

import (
	"reflect"
	"testing"
)

func TestFriendRungs(t *testing.T) {
	t.Parallel()

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
