//go:build functional

package main

import (
	"bytes"
	"context"
	"github.com/redis/go-redis/v9"
	"strconv"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
)

// TestWidthAsDesiredNoFillstate is #3615: a friend with declared slots in
// friend:<f>:desired and no fillstate prints the slots and ? for every
// measured field and exits 0; the fleet line counts those slots; a friend
// with no desired hash still exits 2; with a fillstate the numbers print.
func TestWidthAsDesiredNoFillstate(t *testing.T) {
	t.Parallel()

	addr := testutil.Start(t)
	client := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = client.Close() })
	ctx := context.Background()

	client.SAdd(ctx, "friends", "f", "g")
	client.HSet(ctx, "friend:f:desired", "slots", "8")

	var stdout, stderr bytes.Buffer
	code := run([]string{"width", "--as", "f", "--redis", addr}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("width --as f code=%d stderr=%q; want 0", code, stderr.String())
	}
	want := "WIDTH f slots=8 leased=? working=? deficit=? eligible=? idle=? peak=?@? at=?\n"
	if stdout.String() != want {
		t.Fatalf("width --as f stdout=%q; want %q", stdout.String(), want)
	}

	// The fleet read prints the same row and counts the declared slots.
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"width", "--redis", addr}, &stdout, &stderr); code != 0 {
		t.Fatalf("width code=%d stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), want) {
		t.Fatalf("width stdout=%q; want row %q", stdout.String(), want)
	}
	if !strings.Contains(stdout.String(), "WIDTH fleet working=0/8 deficit=0\n") {
		t.Fatalf("width stdout=%q; want fleet working=0/8", stdout.String())
	}
	if strings.Contains(stdout.String(), "WIDTH g ") {
		t.Fatalf("width stdout=%q; g has no desired and no fillstate, want no row", stdout.String())
	}

	// No desired hash and no fillstate still exits 2.
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"width", "--as", "g", "--redis", addr}, &stdout, &stderr); code != 2 {
		t.Fatalf("width --as g code=%d; want 2", code)
	}
	if stdout.Len() != 0 || !strings.HasPrefix(stderr.String(), "nova-sprint width: ") {
		t.Fatalf("width --as g stdout=%q stderr=%q", stdout.String(), stderr.String())
	}

	// With a fresh fillstate the numbers print.
	now, err := client.Time(ctx).Result()
	if err != nil {
		t.Fatal(err)
	}
	at := strconv.FormatInt(now.UnixMilli(), 10)
	client.HSet(ctx, "friend:f:fillstate", "slots", "8", "leased", "6",
		"working", "5", "deficit", "2", "eligible", "3", "peak", "7", "peak_at", at, "at", at)
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"width", "--as", "f", "--redis", addr}, &stdout, &stderr); code != 0 {
		t.Fatalf("width --as f (fillstate) code=%d stderr=%q", code, stderr.String())
	}
	want = "WIDTH f slots=8 leased=6 working=5 deficit=2 eligible=3 idle=- peak=7@" + at + " at=" + at + "\n"
	if stdout.String() != want {
		t.Fatalf("width --as f (fillstate) stdout=%q; want %q", stdout.String(), want)
	}
}
