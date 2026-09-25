package fold_test

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fold"
)

// The fold prints the rote section for the sprint's window (#3110): notes
// between opened_at and closed_at count, a note after close does not, a
// control-verb receipt on the sprint's own log counts for its actor, and the
// NEXT line names the top class and its mechanism.
func TestFoldRoteSection(t *testing.T) {
	mr, client, fx := seed(t)
	work := workRepo(t)
	ctx := context.Background()
	note := func(at string, n fold.RoteNote) {
		t.Helper()
		ts, err := time.Parse(time.RFC3339, at)
		if err != nil {
			t.Fatal(err)
		}
		mr.SetTime(ts)
		if _, err := fold.Note(ctx, client, n, ts); err != nil {
			t.Fatal(err)
		}
	}
	// The fixture sprint is open 01:00Z to 05:00Z.
	note("2026-09-23T02:00:00Z", fold.RoteNote{Mind: "rowan", Class: "issue-body-repair", Mech: "#3089"})
	note("2026-09-23T02:10:00Z", fold.RoteNote{Mind: "rowan", Class: "issue-body-repair"})
	note("2026-09-23T03:00:00Z", fold.RoteNote{Mind: "rowan", Class: "width-check"})
	note("2026-09-23T03:30:00Z", fold.RoteNote{Mind: "stella", Class: "issue-body-repair"})
	note("2026-09-23T06:00:00Z", fold.RoteNote{Mind: "rowan", Class: "width-check"}) // after close
	if err := client.XAdd(ctx, &redis.XAddArgs{Stream: "s:" + fx.Sprint + ":log",
		Values: []string{"kind", "task", "verb", "assign", "actor", "rowan", "to", "open"}}).Err(); err != nil {
		t.Fatal(err)
	}
	fx.Log++

	var out bytes.Buffer
	if _, err := fold.Run(ctx, client, opts(fx, work), &out); err != nil {
		t.Fatalf("fold: %v\n%s", err, out.String())
	}
	p := "FOLD ROTE sprint=" + fx.Sprint
	for _, want := range []string{
		p + " VERB mind=rowan verb=assign n=1\n",
		p + " NOTE mind=rowan class=issue-body-repair n=2 mech=#3089\n",
		p + " NOTE mind=rowan class=width-check n=1 mech=-\n",
		p + " MIND mind=rowan verbs=1 notes=3 share=75% top=issue-body-repair top_n=2\n",
		p + " MIND mind=stella verbs=0 notes=1 share=100% top=issue-body-repair top_n=1\n",
		p + " NEXT class=issue-body-repair n=3 minds=2 mech=#3089\n",
		p + " TREND prev=- now=80% dir=-\n",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("fold lacks %q:\n%s", want, out.String())
		}
	}
	if o := out.String(); strings.Index(o, "FOLD SPRINT") > strings.Index(o, p+" NEXT") ||
		strings.Index(o, p+" NEXT") > strings.Index(o, "FOLD COMMIT") {
		t.Errorf("the rote section prints after the sprint line and before the commit:\n%s", o)
	}
}

// More notes than one page (1,000) are all counted.
func TestReadRotePages(t *testing.T) {
	_, client, _ := seed(t)
	ctx := context.Background()
	pipe := client.Pipeline()
	for i := 0; i < 2345; i++ {
		pipe.XAdd(ctx, &redis.XAddArgs{Stream: fold.RoteLog, ID: fmt.Sprintf("1-%d", i+1),
			Values: []string{"kind", "rote", "mind", "rowan", "class", "width-check"}})
	}
	if _, err := pipe.Exec(ctx); err != nil {
		t.Fatal(err)
	}
	r, err := fold.ReadRote(ctx, client, fold.Window{})
	if err != nil {
		t.Fatal(err)
	}
	if r.Next.Name != "width-check" || r.Next.N != 2345 || len(r.Minds) != 1 || r.Minds[0].NoteN != 2345 {
		t.Fatalf("paged read: %+v", r)
	}
}

// #2620 DONE-WHEN: the rote share for a sprint is a printed number with its
// classes, and two consecutive folds show it falling with the mechanism named
// that did it. Sprint a's top class is issue-body-repair (mech #3089); sprint
// b has none of it and more verb steps (ws:log moves, cap:log receipts), so
// b's TREND lines show the share and the class falling and name #3089.
func TestFoldRoteTrendTwoFolds(t *testing.T) {
	_, client, _ := seed(t)
	ctx := context.Background()
	at := func(h, m int) int64 { return time.Date(2026, 9, 24, h, m, 0, 0, time.UTC).UnixMilli() }
	add := func(stream string, ms int64, kv ...string) {
		t.Helper()
		if err := client.XAdd(ctx, &redis.XAddArgs{Stream: stream, ID: fmt.Sprintf("%d-0", ms), Values: kv}).Err(); err != nil {
			t.Fatal(err)
		}
	}
	note := func(ms int64, class, mech string) {
		add(fold.RoteLog, ms, "kind", "rote", "mind", "rowan", "class", class, "mech", mech)
	}
	// Sprint a, 01:00 to 05:00: four notes, one verb receipt: 80%.
	note(at(2, 0), "issue-body-repair", "#3089")
	note(at(2, 10), "issue-body-repair", "")
	note(at(2, 20), "issue-body-repair", "")
	note(at(3, 0), "width-check", "")
	add(fold.CapLog, at(3, 30), "verb", "assign", "actor", "rowan")
	// Sprint b, 06:00 to 10:00: one note, a verb receipt and two ws:log moves: 25%.
	note(at(7, 0), "width-check", "")
	add(fold.CapLog, at(7, 10), "verb", "assign", "actor", "rowan")
	add(fold.WsLog, at(7, 20), "id", "t1", "stream", "x", "from", "waiting", "to", "ready", "by", "rowan")
	add(fold.WsLog, at(7, 30), "id", "t2", "stream", "x", "from", "waiting", "to", "ready", "by", "rowan")

	win := func(h1, h2 int) fold.Window {
		return fold.Window{Since: time.UnixMilli(at(h1, 0)).UTC(), Until: time.UnixMilli(at(h2, 0)).UTC()}
	}
	var a, b bytes.Buffer
	if _, err := fold.FoldRote(ctx, client, "a", win(1, 5), &a); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"FOLD ROTE sprint=a NOTE mind=rowan class=issue-body-repair n=3 mech=#3089\n",
		"FOLD ROTE sprint=a MIND mind=rowan verbs=1 notes=4 share=80% top=issue-body-repair top_n=3\n",
		"FOLD ROTE sprint=a TREND prev=- now=80% dir=-\n",
	} {
		if !strings.Contains(a.String(), want) {
			t.Errorf("fold a lacks %q:\n%s", want, a.String())
		}
	}
	for run := 0; run < 2; run++ { // a re-run prints the same trend
		b.Reset()
		if _, err := fold.FoldRote(ctx, client, "b", win(6, 10), &b); err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{
			"FOLD ROTE sprint=b VERB mind=rowan verb=move-ready n=2\n",
			"FOLD ROTE sprint=b MIND mind=rowan verbs=3 notes=1 share=25% top=width-check top_n=1\n",
			"FOLD ROTE sprint=b TREND prev=a was=80% now=25% dir=falling\n",
			"FOLD ROTE sprint=b TREND mind=rowan prev=a was=80% now=25% dir=falling\n",
			"FOLD ROTE sprint=b TREND class=issue-body-repair prev=a was=3 now=0 dir=falling mech=#3089\n",
		} {
			if !strings.Contains(b.String(), want) {
				t.Errorf("run %d: fold b lacks %q:\n%s", run, want, b.String())
			}
		}
	}
	if got := client.ZRange(ctx, fold.RoteFolds, 0, -1).Val(); strings.Join(got, ",") != "a,b" {
		t.Errorf("%s = %v, want a,b by window end", fold.RoteFolds, got)
	}
}
