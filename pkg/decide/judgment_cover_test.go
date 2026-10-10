package decide

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// AckReason is the reason an applied ack gives: the canned text of SPEC-NOVA-DECIDE
// section 13 with the decision's probability printed at two decimals. It is a total
// function (one formatted return, no branch to refuse), so the table pins the exact
// text across the probability range instead of a refusal row.
func TestJudgmentCoverAckReasonPrintsTheCannedReasonWithItsProbability(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		p    float64
		want string
	}{
		{"typical ack above the bar", 0.95, "nova-decide (p=0.95): the card can run without the dropped need; a conflict is handled at merge"},
		{"bar boundary", 0.7, "nova-decide (p=0.70): the card can run without the dropped need; a conflict is handled at merge"},
		{"zero prints two decimals", 0, "nova-decide (p=0.00): the card can run without the dropped need; a conflict is handled at merge"},
		{"certain prints two decimals", 1, "nova-decide (p=1.00): the card can run without the dropped need; a conflict is handled at merge"},
		{"third decimal rounds to the nearest hundredth", 0.4999, "nova-decide (p=0.50): the card can run without the dropped need; a conflict is handled at merge"},
		{"out-of-range p is still formatted, never refused", -0.5, "nova-decide (p=-0.50): the card can run without the dropped need; a conflict is handled at merge"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := AckReason(tc.p)
			assert.Equal(t, tc.want, got)
		})
	}
}
