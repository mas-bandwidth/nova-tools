//go:build functional

package ws_test

import (
	"context"
	"fmt"
	"math/rand"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws/wstest"
)

// TestLuaCycleCheckIsOrders is the #4322 fix round 3's parity check: the
// cycle check inside the push FCALL (cm_order_cycle, fn/lua/02_card_move.lua)
// reads the DEPENDS-ON edges the way ws.Order does. 300 pushes with random
// DEPENDS-ON (fixed seed) onto one stream: ids live and not yet pushed, the
// entry forms task:<id>, <id>, owner/repo#n, none and -, the separators
// comma, semicolon and space, blocked_on and depends_on. Each push is
// refused by the FCALL exactly when ws.Order over the stream's live cards and
// the push (ws.BatchCycle) finds a cycle, and the stream never holds one.
func TestLuaCycleCheckIsOrders(t *testing.T) {
	t.Parallel()
	start := time.Now()
	_, c := wstest.Start(t)
	ctx := context.Background()
	const stream = "order: parity"
	rng := rand.New(rand.NewSource(4322))
	var live []ws.PushCard
	refused, written := 0, 0
	for i := 0; i < 300; i++ {
		id := fmt.Sprintf("p%d", i)
		var deps []string
		for k := rng.Intn(4); k > 0; k-- {
			n := i - 15 + rng.Intn(20) // a live id, this id, or one not pushed yet, near this one
			if n < 0 {
				n = 0
			}
			d := fmt.Sprintf("p%d", n)
			switch rng.Intn(5) {
			case 0:
				d = "task:" + d
			case 1:
				d = "mas-bandwidth/nova-tools#" + fmt.Sprint(rng.Intn(99)+1)
			}
			deps = append(deps, d)
		}
		text := strings.Join(deps, []string{",", ";", " "}[rng.Intn(3)])
		if len(deps) == 0 && rng.Intn(2) == 0 {
			text = []string{"none", "-"}[rng.Intn(2)]
		}
		field := "blocked_on"
		if rng.Intn(3) == 0 {
			field = "depends_on"
		}
		want := ws.BatchCycle(stream, append(append([]ws.PushCard(nil), live...), ws.PushCard{ID: id, DependsOn: text}))
		_, err := taskcard.Push(ctx, c, taskcard.PushRequest{ID: id, Where: "waiting", Stream: stream, Kind: "build",
			Title: id, By: "test", Fields: []string{field, text}})
		why, isRefused := taskcard.IsRefused(err)
		switch {
		case want != nil && isRefused && ws.IsCycleRefusal(why):
			refused++
		case want == nil && err == nil:
			written++
			live = append(live, ws.PushCard{ID: id, DependsOn: text})
		default:
			t.Fatalf("push %d %s %s=%q: ws.Order says %v, the FCALL %v", i, id, field, text, want, err)
		}
	}
	so, err := ws.ReadOrder(ctx, c, stream)
	if err != nil || so.Err != nil || len(so.Order) != written+1 {
		t.Fatalf("after the pushes: %d ranked, want %d and the sentinel; cycle %v, %v", len(so.Order), written, so.Err, err)
	}
	if refused == 0 || written == 0 {
		t.Fatalf("the draw tested one side only: %d refused, %d written", refused, written)
	}
	t.Logf("300 pushes: %d written, %d refused ORDER CYCLE, each as ws.Order predicts; the stream holds no cycle; wall %s",
		written, refused, time.Since(start).Round(time.Millisecond))
}
