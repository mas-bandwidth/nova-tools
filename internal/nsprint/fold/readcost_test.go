package fold_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fold"
)

// TestControl55Cost is #2756 control 55 (nova-tools #3105) on the fold side:
// three reads priced $0.40, $1.10 and unmetered print read $ per landed with
// unmetered 1, and a missing cost field is never summed as $0.
func TestControl55Cost(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	ctx := context.Background()
	sprint := "control-55"
	key := "s:" + sprint
	closed := map[string][2]string{
		"read-3101-a":  {"read", "APPROVE 9/10 at abc1234 | cost_usd=0.40 tokens_in=91000 tokens_out=2100 model=claude-sonnet-5 route=child"},
		"read-3102-b":  {"read", "HOLD 6/10 at def5678 | cost_usd=1.10 tokens_in=240000 tokens_out=5200 model=claude-opus-5-5 route=child"},
		"read-3103-c":  {"review", "APPROVE 8/10 at 0123abc | cost: unmetered plan-billed main session"},
		"build-3104-d": {"work", "PR https://example.test/pr/3104 | cost_usd=9.99"},
	}
	for id, f := range closed {
		if err := client.HSet(ctx, key+":task:"+id, "kind", f[0], "state", "closed", "evidence", f[1]).Err(); err != nil {
			t.Fatal(err)
		}
		if err := client.SAdd(ctx, key+":idx:task:closed", id).Err(); err != nil {
			t.Fatal(err)
		}
	}

	tasks, err := fold.ReadTasks(ctx, client, sprint)
	if err != nil {
		t.Fatal(err)
	}
	rc := fold.SumReads(tasks, 2)
	var out bytes.Buffer
	fold.PrintReads(&out, sprint, rc)
	want := "FOLD READS sprint=control-55 reads=3 priced=2 unmetered=1 malformed=0 read_usd=1.5 landed=2 read_usd_per_landed>=0.75 coverage=66.67% (2/3)\n"
	if out.String() != want {
		t.Fatalf("read line\n got %q\nwant %q", out.String(), want)
	}

	t.Run("missing cost is never $0", func(t *testing.T) {
		rc := fold.SumReads([]fold.ReadTask{
			{ID: "r1", Kind: "read", Evidence: "APPROVE 9/10 at abc1234"},
			{ID: "r2", Kind: "read", Evidence: "APPROVE 9/10 at abc1234 | cost_usd=-"},
			{ID: "r3", Kind: "read", Evidence: "APPROVE 9/10 at abc1234 | cost_usd=about-a-dollar"},
		}, 3)
		if rc.Priced != 0 || rc.Unmetered != 3 || rc.Malformed != 1 || rc.USDMicro != 0 {
			t.Fatalf("reads with no measured cost = %+v; want priced 0, unmetered 3, malformed 1", rc)
		}
		var out bytes.Buffer
		fold.PrintReads(&out, sprint, rc)
		line := out.String()
		if !strings.Contains(line, " read_usd=- ") || !strings.HasSuffix(line, " read_usd_per_landed=-\n") {
			t.Fatalf("unmetered reads print %q; want the dash, never $0", line)
		}
		if strings.Contains(line, "read_usd=0") {
			t.Fatalf("unmetered reads were summed as $0: %q", line)
		}
	})

	t.Run("nothing landed prints the dash", func(t *testing.T) {
		if got := fold.SumReads(tasks, 0).PerLanded(); got != "-" {
			t.Fatalf("read $ per landed with nothing landed = %q; want -", got)
		}
	})
}

// TestFoldReadsPerLandedLowerBound is nova-tools #3159 on FOLD READS: an unmetered read makes
// read $ per landed a lower bound with its coverage (priced reads over reads); with every read
// priced the figure is exact.
func TestFoldReadsPerLandedLowerBound(t *testing.T) {
	control55 := func(third string) []fold.ReadTask {
		return []fold.ReadTask{
			{ID: "read-3101-a", Kind: "read", Evidence: "APPROVE 9/10 at abc1234 | cost_usd=0.40"},
			{ID: "read-3102-b", Kind: "read", Evidence: "HOLD 6/10 at def5678 | cost_usd=1.10"},
			{ID: "read-3103-c", Kind: "review", Evidence: "APPROVE 8/10 at 0123abc | " + third},
		}
	}
	for _, tc := range []struct{ name, third, want string }{
		{"one unmetered", "cost: unmetered plan-billed main session", " read_usd_per_landed>=0.75 coverage=66.67% (2/3)\n"},
		{"all priced", "cost_usd=0.60", " read_usd_per_landed=1.05 coverage=100.00% (3/3)\n"},
	} {
		var out bytes.Buffer
		fold.PrintReads(&out, "control-55", fold.SumReads(control55(tc.third), 2))
		if !strings.HasSuffix(out.String(), tc.want) {
			t.Errorf("%s: read line\n got %q\nwant suffix %q", tc.name, out.String(), tc.want)
		}
	}
}
