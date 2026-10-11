package main

import (
	"errors"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/sandbox"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMainCoverAsRefusalCarriesARefusalThroughAndLeavesTheRest: asRefusal is the seam
// run and runwin hand a sandbox.Refusal back to their refusal printer through, and it
// sat at 0.0% in the unit tier's per-function table. The main path carries a refusal
// through -- out gains it whole -- and every other error is refused: false, with out
// left exactly as the caller held it.
func TestMainCoverAsRefusalCarriesARefusalThroughAndLeavesTheRest(t *testing.T) {
	t.Parallel()

	refusal := sandbox.Refusal{Reason: "not_found", Text: "the command is not on this machine's PATH"}
	cases := []struct {
		name    string
		err     error
		want    bool
		wantOut sandbox.Refusal
	}{
		{name: "main path: the error is a refusal", err: refusal, want: true, wantOut: refusal},
		{name: "refusal: the error is no refusal", err: errors.New("fork/exec /no/such/binary: no such file or directory"), want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var out sandbox.Refusal
			require.Equal(t, tc.want, asRefusal(tc.err, &out), tc.name)
			assert.Equal(t, tc.wantOut, out, tc.name)
		})
	}
}
