package bounded

import (
	"context"
	"testing"
)

func TestCaptureCancelsAtExactLimitWithoutKeepingACompletePrefix(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	b := NewCapture(4, cancel)
	if n, e := b.Write([]byte("abc")); n != 3 || e != nil || b.Hit() {
		t.Fatal(n, e, b.Hit())
	}
	if n, e := b.Write([]byte("defgh")); n != 5 || e != nil || !b.Hit() {
		t.Fatal(n, e, b.Hit())
	}
	if string(b.Bytes()) != "abcd" || ctx.Err() == nil {
		t.Fatal("capture not capped/cancelled")
	}
}
