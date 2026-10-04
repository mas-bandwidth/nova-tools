package sprint

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestCostCoverMoneyText pins MoneyText: a cost as a dollars-and-cents cell shows
// it, rounded up to the cent ("$1.24" for 1.2345, "$0.01" for 0.001); "-" when there
// is none, the empty string or a value that is not a number.
func TestCostCoverMoneyText(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		usd  string
		want string
	}{
		{
			"main: a cost with sub-cent precision rounds up to the cent",
			"1.2345",
			"$1.24",
		},
		{
			"main: another cost rounds up",
			"20.2111",
			"$20.22",
		},
		{
			"main: a cost below one cent still rounds up to it",
			"0.001",
			"$0.01",
		},
		{
			"main: an exact whole dollar prints with no fraction past the cent",
			"5",
			"$5.00",
		},
		{
			"refusal: empty string is absent, shows nothing",
			"",
			"-",
		},
		{
			"refusal: not a number",
			"not-a-number",
			"-",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, MoneyText(tc.usd))
		})
	}
}
