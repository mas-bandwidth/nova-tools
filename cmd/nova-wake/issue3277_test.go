package main

import (
	"bytes"
	"context"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/presence"
)

// #3277: presence.Open sends nothing, so the first beat is the probe. A store
// that refuses the first beat is refused at start, the way the dial's PING
// refused it before; a store that blinks after a good beat is still beaten
// through (TestTheLoopKeepsBeatingThroughAStoreThatBlinked).
func TestFirstBeatIsTheProbe3277(t *testing.T) {
	t.Parallel()

	st := presence.NewFakeStore(beatAt)
	st.Hook = func(call int) error { return errStoreDown }
	var errb bytes.Buffer
	err := beatLoop(context.Background(), st, "johnny", presence.DefaultEvery, presence.DefaultTTL, fakeStoreClock{st}, &errb, presence.Side{}, 6)
	if err == nil {
		t.Fatalf("first beat on a store that refuses returned nil; want the refusal at start (stderr %q)", errb.String())
	}
	if st.Sets != 0 {
		t.Fatalf("writes = %d; want 0", st.Sets)
	}
}
