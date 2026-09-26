//go:build functional

package capacity_test

import (
	"context"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/capacity"
)

// TestBudgetFunctionsRefuseAValueThatIsNotANumber: ns_budget_set wrote a
// budget of 0 for `cpu_milli=abc`, and ns_budget_take debited 0 for the
// same, in silence. Each answers REFUSED with the field named; nothing is
// written. The Go wrappers pass integers, so the function is called as a
// stray caller would.
func TestBudgetFunctionsRefuseAValueThatIsNotANumber(t *testing.T) {
	t.Parallel()
	_, c := redisControl(t)
	ctx := context.Background()
	set, err := c.FCall(ctx, capacity.FunctionBudgetSet, nil, "m-odd", "abc", "64", "op", "").StringSlice()
	if err != nil || len(set) != 2 || set[0] != "REFUSED" || set[1] != "cpu_milli abc is not a number" {
		t.Fatalf("ns_budget_set abc = %v %v; want REFUSED naming cpu_milli", set, err)
	}
	if n, _ := c.Exists(ctx, "machine:m-odd:budget").Result(); n != 0 {
		t.Fatalf("a refused set wrote machine:m-odd:budget")
	}
	take, err := c.FCall(ctx, capacity.FunctionBudgetTake, nil, "m-odd", "ci:x", "1000", "lots", "30000", "", "ci").StringSlice()
	if err != nil || len(take) != 2 || take[0] != "REFUSED" || take[1] != "mem_mb lots is not a number" {
		t.Fatalf("ns_budget_take lots = %v %v; want REFUSED naming mem_mb", take, err)
	}
}
