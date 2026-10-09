package cardcost

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestChargedUsageMatchesExactTotal(t *testing.T) {
	t.Parallel()
	tiny := "0." + strings.Repeat("0", 60) + "1"
	cases := []struct {
		name   string
		usages []Usage
		want   string
	}{
		{name: "no records"},
		{name: "missing", usages: []Usage{{}}},
		{name: "invalid actual falls back", usages: []Usage{{Actual: "oops", Predicted: "0.25"}}, want: "0.25"},
		{name: "explicit zero overrides", usages: []Usage{{Actual: "0", Predicted: "0.25"}}, want: "0"},
		{name: "actual overrides", usages: []Usage{{Actual: "0.125", Predicted: "0.25"}}, want: "0.125"},
		{name: "prediction only", usages: []Usage{{Predicted: "000.2500"}}, want: "0.25"},
		{name: "invalid costs do not contaminate", usages: []Usage{{Actual: "0.1"}, {Actual: "oops", Predicted: "bad"}, {Predicted: "0.2"}}, want: "0.3"},
		{name: "stored precision exceeds typed limit", usages: []Usage{{Actual: tiny}, {Predicted: tiny}}, want: "0." + strings.Repeat("0", 60) + "2"},
		{name: "no per record cent rounding", usages: []Usage{{Actual: "0.0001"}, {Actual: "0.0002"}}, want: "0.0003"},
		{name: "negative actual falls back", usages: []Usage{{Actual: "-1", Predicted: "0.25"}}, want: "0.25"},
		{name: "sign exponent and ratio rejected", usages: []Usage{{Actual: "+1"}, {Actual: "1e-2"}, {Actual: "1/3"}, {Actual: ".1"}, {Actual: "1."}, {Actual: " 1 "}}},
		{name: "zero after missing records", usages: []Usage{{}, {Actual: "bad"}, {Predicted: "000.000"}}, want: "0"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := SumChargedUsage(tc.usages)
			assert.Equal(t, tc.want, got)
			assert.Equal(t, SumUsage(tc.usages).Charged, got, "the existing Total.Add cost contract is unchanged")
		})
	}
}
