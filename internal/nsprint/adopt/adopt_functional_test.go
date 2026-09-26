//go:build functional

package adopt_test

import (
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/adopt"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/redis/go-redis/v9"
)

func store(t *testing.T) *redis.Client {
	t.Helper()
	c := redis.NewClient(&redis.Options{Addr: testutil.Start(t)})
	t.Cleanup(func() { _ = c.Close() })
	if err := fn.LoadMissing(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	return c
}

func write(t *testing.T, c *redis.Client, r adopt.Receipt) adopt.Result {
	t.Helper()
	if err := r.Check(); err != nil {
		t.Fatalf("check %+v: %v", r, err)
	}
	res, err := adopt.Write(context.Background(), c, r)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// TestAdoptMatrixFromReceipts is the DONE-WHEN of the matrix slice: three
// verbs from the hacks-to-verbs matrix, receipts from two POVs, on a fresh
// throwaway Redis. The matrix reads back oldest verb first with who, when,
// pov and gap; adopted-gaps with no gap is refused no-gap and writes
// nothing; an identical retry writes nothing; status reads 1/3 33%, then
// 2/3 66% when a gap-carrying receipt adopts the second verb.
func TestAdoptMatrixFromReceipts(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	c := store(t)

	rows, err := adopt.Matrix(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	if got := adopt.Status(rows); got != "adopted 0/0 -" {
		t.Fatalf("empty status %q", got)
	}

	r1 := write(t, c, adopt.Receipt{Verb: adopt.CleanVerb("`nova-sprint  land stream`"), Who: "rowan", POV: "coordinator",
		State: "in-flight", Hand: "stream branch built by hand (stream-build.sh)", Gap: "nova-tools#3975"})
	if r1.Outcome != adopt.Recorded || r1.Receipts != 1 || r1.At == 0 {
		t.Fatalf("first receipt %+v", r1)
	}
	write(t, c, adopt.Receipt{Verb: "nova-sprint card fsck --repair", Who: "rowan", POV: "coordinator",
		State: "in-flight", Hand: "ghost cleanup by hand"})
	write(t, c, adopt.Receipt{Verb: "nova-sprint pitstop", Who: "stella", POV: "friend",
		State: "adopted", Hand: "pit stop as a bus note"})

	// adopted-gaps needs a gap: refused, nothing written.
	ref := write(t, c, adopt.Receipt{Verb: "nova-sprint card fsck --repair", Who: "emma", POV: "reader", State: "adopted-gaps"})
	if ref.Outcome != adopt.Refused || ref.Reason != "no-gap" {
		t.Fatalf("no-gap: %+v", ref)
	}
	if n, _ := c.HGet(ctx, "adopt:nova-sprint card fsck --repair", "receipts").Result(); n != "1" {
		t.Fatalf("refusal wrote: receipts=%q", n)
	}

	// The same who, pov and body again writes nothing.
	again := write(t, c, adopt.Receipt{Verb: "nova-sprint pitstop", Who: "stella", POV: "friend",
		State: "adopted", Hand: "pit stop as a bus note"})
	if again.Outcome != adopt.Unchanged {
		t.Fatalf("retry: %+v", again)
	}
	if n, _ := c.LLen(ctx, "adopt:nova-sprint pitstop:receipts").Result(); n != 1 {
		t.Fatalf("retry appended: %d", n)
	}

	rows, err = adopt.Matrix(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 || rows[0].Verb != "nova-sprint land stream" || rows[1].Verb != "nova-sprint card fsck --repair" || rows[2].Verb != "nova-sprint pitstop" {
		t.Fatalf("rows not oldest first: %+v", rows)
	}
	if r := rows[0]; r.Who != "rowan" || r.POV != "coordinator" || r.Gap != "nova-tools#3975" || r.At != r1.At || r.State != "in-flight" {
		t.Fatalf("row 0: %+v", r)
	}
	if got := adopt.Status(rows); got != "adopted 1/3 33%" {
		t.Fatalf("status %q", got)
	}

	// A reader adopts fsck with its gap; a second POV joins the row; the
	// row keeps its age (still second).
	write(t, c, adopt.Receipt{Verb: "nova-sprint card fsck --repair", Who: "emma", POV: "reader", State: "adopted-gaps", Gap: "mas-bandwidth/nova-tools#3979"})
	rows, _ = adopt.Matrix(ctx, c)
	fsck := rows[1]
	if fsck.Verb != "nova-sprint card fsck --repair" || fsck.State != "adopted-gaps" || fsck.Who != "emma" || fsck.POV != "reader" ||
		fsck.Gap != "mas-bandwidth/nova-tools#3979" || fsck.Receipts != 2 || strings.Join(fsck.POVs, ",") != "coordinator,reader" {
		t.Fatalf("fsck row: %+v", fsck)
	}
	if got := adopt.Status(rows); got != "adopted 2/3 66%" {
		t.Fatalf("status %q", got)
	}
	// A later blocked receipt may lean on the gap already on the record.
	if res := write(t, c, adopt.Receipt{Verb: "nova-sprint card fsck --repair", Who: "rowan", POV: "bench", State: "blocked"}); res.Outcome != adopt.Recorded {
		t.Fatalf("blocked with stored gap: %+v", res)
	}

	md := adopt.Markdown(rows)
	if !strings.HasPrefix(md, "| hand step today | verb | state | who | when | pov | gap |\n|---|") {
		t.Fatalf("markdown header: %q", md)
	}
	if !regexp.MustCompile("(?m)^\\| stream branch built by hand \\(stream-build.sh\\) \\| `nova-sprint land stream` \\| in-flight \\| rowan \\| [0-9-]+ [0-9]+:[0-9]{2} [AP]M (ET|Z) \\| coordinator \\(1/4\\) \\| nova-tools#3975 \\|$").MatchString(md) &&
		!regexp.MustCompile("(?m)^\\| stream branch built by hand \\(stream-build.sh\\) \\| `nova-sprint land stream` \\| in-flight \\| rowan \\| [0-9-]+ [0-9:]+Z \\| coordinator \\(1/4\\) \\| nova-tools#3975 \\|$").MatchString(md) {
		t.Fatalf("markdown row:\n%s", md)
	}
	if !strings.Contains(rows[0].Line(), `ADOPT ROW verb="nova-sprint land stream" state=in-flight who=rowan at=`) {
		t.Fatalf("line %q", rows[0].Line())
	}
	if tsv := adopt.TSV(rows); strings.Count(tsv, "\n") != 4 || !strings.HasPrefix(tsv, "verb\tstate\twho\tat\tpov\tgap\tpovs\treceipts\thand\n") {
		t.Fatalf("tsv %q", tsv)
	}
}
