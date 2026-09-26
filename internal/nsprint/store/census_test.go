package store_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/redis/go-redis/v9"
)

func TestCensusRefusals(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st := store.New(redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"}))
	defer st.Close()
	for name, req := range map[string]store.CensusRequest{
		"no source":    {Fields: []string{"host"}},
		"two sources":  {Set: "benches", KeysFrom: strings.NewReader("bench:a\n"), Fields: []string{"host"}},
		"no fields":    {Set: "benches"},
		"blank field":  {Set: "benches", Fields: []string{"host", ""}},
		"unknown set":  {Set: "everything", Fields: []string{"host"}},
		"sprint shape": {Set: "sprint:only-name", Fields: []string{"state"}},
	} {
		var out bytes.Buffer
		if _, err := store.RunCensus(ctx, st, req, &out); err == nil {
			t.Errorf("%s: census ran; want a refusal", name)
		}
		if out.Len() != 0 {
			t.Errorf("%s: refusal printed rows %q", name, out.String())
		}
	}
}

func TestCensusSetKeys(t *testing.T) {
	t.Parallel()

	for set, want := range map[string][2]string{
		"benches":            {"benches", "bench:"},
		"friends":            {"friends", "friend:"},
		"sprint:s1:working":  {"s:s1:idx:task:working", "task:"},
		"sprint:s-2:claimed": {"s:s-2:idx:task:claimed", "task:"},
	} {
		index, prefix, err := store.CensusSet(set)
		if err != nil || index != want[0] || prefix != want[1] {
			t.Errorf("CensusSet(%q) = %q, %q, %v; want %q, %q", set, index, prefix, err, want[0], want[1])
		}
	}
}
