package fold_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fold"
)

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
