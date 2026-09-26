package store_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/redis/go-redis/v9"
)

func TestCardCensusRefusals(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st := store.New(redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"}))
	defer st.Close()
	for name, req := range map[string]store.CardCensusRequest{
		"no sprint":      {},
		"sprint pattern": {Sprint: "s*"},
		"key pattern":    {Sprint: "s1", States: []string{"run*"}},
		"blank key":      {Sprint: "s1", States: []string{"queued", ""}},
		"twice":          {Sprint: "s1", States: []string{"queued", "queued"}},
	} {
		var out bytes.Buffer
		if _, err := store.RunCardCensus(ctx, st, req, &out); err == nil {
			t.Errorf("%s: census ran; want a refusal", name)
		}
		if out.Len() != 0 {
			t.Errorf("%s: refusal printed %q", name, out.String())
		}
	}
}
