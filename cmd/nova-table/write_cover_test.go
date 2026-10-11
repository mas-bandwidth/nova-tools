package main

import (
	"bytes"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/ntable"
	"github.com/stretchr/testify/assert"
)

// TestWriteCoverPrintReceipt: --receipt prints the committed event's line with
// its five fields in order, and prints nothing at all when --receipt is off.
func TestWriteCoverPrintReceipt(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		enabled bool
		want    string
	}{
		{
			name:    "receipt on prints the event line",
			enabled: true,
			want:    "TABLE RECEIPT event=ev-1 epoch=7 before=3 after=4 outcome=ok\n",
		},
		{
			name:    "receipt off prints nothing",
			enabled: false,
			want:    "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			opts := &ntable.WriteOptions{Epoch: 7, Actor: "bot",
				Receipt: &ntable.Receipt{ID: "ev-1", Epoch: 7, Before: 3, After: 4, Outcome: "ok"}}
			var out bytes.Buffer
			printReceipt(&out, opts, tc.enabled)
			assert.Equal(t, tc.want, out.String(), "the receipt line")
		})
	}
}
