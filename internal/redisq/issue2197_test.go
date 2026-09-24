package redisq_test

import (
	"context"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/redisq"
)

func TestIssue2197(t *testing.T) {
	t.Run("LanesAreReadInPriorityOrder", func(t *testing.T) {
		ctx := context.Background()
		kind := "cut"

		_, q := newQueue(t)
		for _, lane := range []string{"red", "green", "small", "next"} {
			stream := "nova:queue:" + kind + ":" + lane
			mustAdd(t, q, stream, lane)
		}

		for _, want := range []string{"red", "green", "small", "next"} {
			card, err := q.PullLanes(ctx, kind, "bench", 0)
			if err != nil {
				t.Fatalf("pull %s: %s", want, err)
			}
			if card == nil || card.Fields["card"] != want {
				t.Fatalf("expected %s, got %+v", want, card)
			}
		}

		card, err := q.PullLanes(ctx, kind, "bench", 0)
		if err != nil {
			t.Fatalf("pull after drain: %s", err)
		}
		if card != nil {
			t.Fatalf("drained, got %+v", card)
		}
	})

	t.Run("DirectoryFallbackDeliversTheSameContract", func(t *testing.T) {
		dq := &redisq.DirQueue{Root: t.TempDir()}
		kind := "cut"

		_, err := dq.Add("nova:queue:"+kind+":red", "card-1", map[string]string{"card": "9014"})
		if err != nil {
			t.Fatalf("add: %s", err)
		}

		card, err := dq.PullLanes(kind)
		if err != nil {
			t.Fatalf("pull: %s", err)
		}
		if card == nil || card.ID != "card-1" || card.Fields["card"] != "9014" {
			t.Fatalf("expected card-1, got %+v", card)
		}

		card2, err := dq.PullLanes(kind)
		if err != nil {
			t.Fatalf("second pull: %s", err)
		}
		if card2 != nil {
			t.Fatalf("one-consumer: second puller got %+v", card2)
		}

		if err := dq.Ack("nova:queue:"+kind+":red", "card-1"); err != nil {
			t.Fatalf("ack: %s", err)
		}

		_, err = dq.Add("nova:queue:"+kind+":red", "card-2", map[string]string{"card": "9015"})
		if err != nil {
			t.Fatalf("add card-2: %s", err)
		}

		card3, err := dq.PullLanes(kind)
		if err != nil || card3 == nil {
			t.Fatalf("pull card-2: card=%+v err=%v", card3, err)
		}

		reclaimed, err := dq.Reclaim("nova:queue:"+kind+":red", 0, time.Now())
		if err != nil {
			t.Fatalf("reclaim: %s", err)
		}
		if !reclaimed {
			t.Fatal("reclaim with zero lease should return true")
		}

		card4, err := dq.PullLanes(kind)
		if err != nil {
			t.Fatalf("pull after reclaim: %s", err)
		}
		if card4 == nil || card4.ID != "card-2" {
			t.Fatalf("reclaimed card should be pullable again, got %+v", card4)
		}
	})
}
