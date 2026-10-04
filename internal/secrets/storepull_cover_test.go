package secrets

// The unit cover for mpintBytes, the SSH mpint body wirePublicKey's RSA branch
// writes. mpintBytes is a pure function over a big.Int with no seam beyond its
// argument, so every row reaches it directly, with no child and no store. It
// has no refusal path: the branch pair it guards is the sign bit, where a body
// whose top bit would read negative gains one leading zero byte.

import (
	"math/big"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestStorepullCoverMpintBytesEncodesNonNegativeIntegers pins the main path:
// the big-endian magnitude bytes, empty for zero, unchanged while the top bit
// of the first byte is clear.
func TestStorepullCoverMpintBytesEncodesNonNegativeIntegers(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		n    *big.Int
		want []byte
	}{
		{"zero is empty", big.NewInt(0), []byte{}},
		{"one is one byte", big.NewInt(1), []byte{0x01}},
		{"top bit clear is unchanged", big.NewInt(0x7f), []byte{0x7f}},
		{"magnitude bytes big-endian", big.NewInt(0x0102), []byte{0x01, 0x02}},
		{"zero byte inside the body is not a sign", new(big.Int).SetBytes([]byte{0x01, 0x00, 0x02}), []byte{0x01, 0x00, 0x02}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, mpintBytes(tt.n))
		})
	}
}

// TestStorepullCoverMpintBytesPrependsZeroWhenTopBitWouldSign pins the second
// branch: when the first body byte's top bit is set, one zero byte is prepended
// so the ssh mpint never reads negative; a top bit set only in a later byte is
// left alone, and the result decodes back to the integer it came from.
func TestStorepullCoverMpintBytesPrependsZeroWhenTopBitWouldSign(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		n    *big.Int
		want []byte
	}{
		{"0x80 gains a zero byte", big.NewInt(0x80), []byte{0x00, 0x80}},
		{"later byte's top bit does not prepend", new(big.Int).SetBytes([]byte{0x01, 0x80}), []byte{0x01, 0x80}},
		{"rsa-sized body gains its zero byte", new(big.Int).SetBytes([]byte{0xff, 0xff, 0xff, 0xff}), []byte{0x00, 0xff, 0xff, 0xff, 0xff}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			body := mpintBytes(tt.n)
			assert.Equal(t, tt.want, body)
			assert.Equal(t, tt.n, new(big.Int).SetBytes(body))
		})
	}
}
