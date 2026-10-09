package sprintwire

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// These fixed outputs pin the operation identity across the move to the wire
// package: an in-flight retry must still address its existing durable receipt.
func TestStopReturnOperationIdentityStaysStable(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name, row, card string
		gen             int
		epoch, want     string
	}{
		{"ordinary", "m", "c1", 1, "7", "stop-return-f45f14630871d974c18cadbad0da9011426c4545f6988d7add967f5bf5fd63b3"},
		{"reader", "reader.r1", "rd1", 1, "7", "stop-return-b0cfbdaf83a596807f21689fb8b71c5117bca0d3c30c9061333106c2914d7a15"},
		{"delimiters and UTF-8", "r/é", "c:一", 12, "7/9", "stop-return-d24fb775bdc175a626dc40b6ed4cbdb7efebdac56cb57f2d48dbf14ba1e99189"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, StopReturnOp(tt.row, tt.card, tt.gen, tt.epoch))
		})
	}
}
