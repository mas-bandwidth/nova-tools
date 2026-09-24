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
