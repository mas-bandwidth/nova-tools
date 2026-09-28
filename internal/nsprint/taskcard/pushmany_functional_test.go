//go:build functional

package taskcard_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
)

// TestPushManyIsOneRoundTrip is the push half of nova-tools#4340: 100 task
// cards go onto their stream's waiting set in one pipeline (one round trip),
// one outcome per request in order; an id that exists is refused by the
// store and named, and a swarm card its spec refuses is never sent; the rest
// are pushed, and fsck is clean.
func TestPushManyIsOneRoundTrip(t *testing.T) {
	t.Parallel()
	c := start(t)
	ctx := context.Background()
	if _, err := taskcard.Push(ctx, c, taskcard.PushRequest{ID: "pm-007", Where: "waiting", Stream: cardStream, Sprint: sprint, By: "rowan"}); err != nil {
		t.Fatal(err)
	}
	var reqs []taskcard.PushRequest
	for i := 1; i <= 100; i++ {
		spec := taskcard.ParseIssue(issueText(i, "internal/nsprint/taskcard/"))
		spec.Route = taskcard.RouteFlash
		if i == 50 {
			spec.Paths = "" // a flash card without PATHS: refused at push, never sent
		}
		dep := ""
		if i%10 == 0 {
			dep = fmt.Sprintf("pm-%03d", i-1)
		}
		reqs = append(reqs, taskcard.PushRequest{ID: fmt.Sprintf("pm-%03d", i), Where: "waiting", Sprint: sprint,
			Ref: fmt.Sprintf("mas-bandwidth/nova-tools#%d", 6000+i), Title: fmt.Sprintf("card %d", i), DependsOn: dep,
			By: "rowan", Why: "card cut --from", Spec: &spec})
	}
	rt := &roundTrips{}
	c.AddHook(rt)
	out, err := taskcard.PushMany(ctx, c, reqs)
	if err != nil {
		t.Fatal(err)
	}
	if n := rt.n.Load(); n != 1 {
		t.Fatalf("PushMany took %d round trips, want 1", n)
	}
	pushed := 0
	for i, o := range out {
		id := reqs[i].ID
		why, refused := taskcard.IsRefused(o.Err)
		switch {
		case id == "pm-007":
			if !refused || !strings.HasPrefix(why, "EXISTS task:pm-007") {
				t.Errorf("pm-007: %v, want EXISTS", o.Err)
			}
		case id == "pm-050":
			if !refused || !strings.Contains(why, "INCOMPLETE task:pm-050") || !strings.Contains(why, "PATHS") {
				t.Errorf("pm-050: %v, want INCOMPLETE naming PATHS", o.Err)
			}
		case o.Err != nil || o.Result.Where != "waiting":
			t.Errorf("%s: %+v", id, o)
		default:
			pushed++
		}
	}
	if pushed != 98 {
		t.Fatalf("pushed %d, want 98", pushed)
	}
	if n := wsCards(c, cardStream, "waiting"); n != 99 {
		t.Fatalf("ws:%s:waiting holds %d cards, want 99 (98 and the one before; the sentinel aside, #4318)", cardStream, n)
	}
	rec, err := taskcard.Record(ctx, c, "pm-020")
	if err != nil {
		t.Fatal(err)
	}
	if rec["blocked_on"] != "pm-019" || rec["route"] != "flash" || rec["ref"] != "mas-bandwidth/nova-tools#6020" {
		t.Fatalf("pm-020 record blocked_on=%q route=%q ref=%q", rec["blocked_on"], rec["route"], rec["ref"])
	}
	if n, _ := c.Exists(ctx, taskcard.Key("pm-050")).Result(); n != 0 {
		t.Fatal("the refused push wrote a record")
	}
	clean(t, c, "after PushMany")
}
