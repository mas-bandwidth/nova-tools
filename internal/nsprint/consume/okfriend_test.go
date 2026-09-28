package consume

import (
	"testing"
	"time"
)

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestOkFriendNextBackoffBounded(t *testing.T) {
	t.Parallel()

	o := &OkFriend{RetryWait: time.Second, RetryMax: 4 * time.Second}
	var got []time.Duration
	var d time.Duration
	for i := 0; i < 5; i++ {
		d = o.nextBackoff(d)
		got = append(got, d)
	}
	want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 4 * time.Second, 4 * time.Second}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("backoff sequence %v; want %v", got, want)
		}
	}
	if d := (&OkFriend{}).nextBackoff(0); d != time.Second {
		t.Fatalf("default first backoff %v; want 1s", d)
	}
}
