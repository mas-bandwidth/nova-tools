//go:build functional

package ws_test

import (
	"context"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws/wstest"
)

func TestOrderStreamAndShowOrder(t *testing.T) {
	t.Parallel()

	_, c := wstest.Start(t)
	ctx := context.Background()
	strm := "swarm: cards"

	// Push 3 cards:
	// c1 touches "a.go"
	// c2 touches "a.go" (paths overlap with c1)
	// c3 depends on c2
	push := func(id, ref, on, paths string) {
		t.Helper()
		req := taskcard.PushRequest{
			ID:        id,
			Where:     "waiting",
			Stream:    strm,
			Kind:      "build",
			Title:     "TASK: " + id,
			By:        "test",
			Ref:       ref,
			DependsOn: on,
		}
		if paths != "" {
			req.Fields = []string{"paths", paths}
		}
		if _, err := taskcard.Push(ctx, c, req); err != nil {
			t.Fatal(err)
		}
	}

	push("card-1", "repo#101", "", "a.go")
	push("card-2", "repo#102", "", "a.go")
	push("card-3", "repo#103", "card-2", "b.go")

	if err := ws.OrderStream(ctx, c, strm); err != nil {
		t.Fatalf("OrderStream: %v", err)
	}

	// Verify scores in ws:<stream>:waiting
	s1, _ := c.ZScore(ctx, "ws:"+strm+":waiting", "card-1").Result()
	s2, _ := c.ZScore(ctx, "ws:"+strm+":waiting", "card-2").Result()
	s3, _ := c.ZScore(ctx, "ws:"+strm+":waiting", "card-3").Result()
	sentinel, _ := c.ZScore(ctx, "ws:"+strm+":waiting", ws.SentinelID(strm)).Result()

	if !(s1 < s2 && s2 < s3 && s3 < sentinel) {
		t.Fatalf("scores not ordered: s1=%f, s2=%f, s3=%f, sentinel=%f", s1, s2, s3, sentinel)
	}

	// Verify Show shows the computed order with reasons per edge:
	// card-2 has paths overlap with card-1
	// card-3 has depends-on with card-2
	streams, err := ws.Show(ctx, c)
	if err != nil {
		t.Fatalf("ws.Show: %v", err)
	}
	if len(streams) == 0 {
		t.Fatal("ws.Show returned no streams")
	}
	cards := streams[0].Cards
	var lines []string
	for _, card := range cards {
		lines = append(lines, card.Line(streams[0].Live))
	}
	text := strings.Join(lines, "\n")
	if !strings.Contains(text, "waiting card-2 <- card-1(waiting) (paths overlap a.go)") {
		t.Errorf("missing paths overlap reason in output:\n%s", text)
	}
	if !strings.Contains(text, "waiting card-3 <- card-2(waiting) (depends-on)") {
		t.Errorf("missing depends-on reason in output:\n%s", text)
	}
}
