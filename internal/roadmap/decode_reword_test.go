package roadmap

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDecodeRewordsTheReadersRefusalByteForByte pins the exact sentence Decode and
// DecodeFixes wrap a *sexp.Refusal in. Folding the two reads into decode must not change a
// byte a caller reads back: the noun prefix and the reworded "a plan is data" phrase are
// the two things that differ between the files, and both are part of the contract.
func TestDecodeRewordsTheReadersRefusalByteForByte(t *testing.T) {
	t.Parallel()

	data := []byte("#.(x)")
	for _, c := range []struct {
		name string
		call func() error
		want string
	}{
		{
			name: "roadmap",
			call: func() error { _, err := Decode("bad.sexp", data); return err },
			want: "roadmap: file=bad.sexp: dispatch macro at byte=0; a roadmap is data, never a program, so remove it",
		},
		{
			name: "fixes",
			call: func() error { _, err := DecodeFixes("bad.sexp", data); return err },
			want: "fixes: file=bad.sexp: dispatch macro at byte=0; the fixes file is data, never a program, so remove it",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			err := c.call()
			require.Error(t, err)
			assert.EqualError(t, err, c.want)
		})
	}
}
