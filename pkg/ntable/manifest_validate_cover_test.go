package ntable

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestManifestValidateCoverHex4 drives hex4, the four-hex-digit reader behind
// checkEscapes, over its main path and its two refusals: a window that runs past
// the buffer, and four bytes that are not hex. Each row pins both the value and
// the ok flag, so a reader that coerces a bad escape into a number or that reads
// past the buffer is caught.
func TestManifestValidateCoverHex4(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		raw    []byte
		at     int
		want   int
		wantOK bool
	}{
		{
			name:   "four lowercase hex digits read as their value",
			raw:    []byte("d83d"),
			at:     0,
			want:   0xd83d,
			wantOK: true,
		},
		{
			name:   "four uppercase hex digits read the same",
			raw:    []byte("D83D"),
			at:     0,
			want:   0xd83d,
			wantOK: true,
		},
		{
			name:   "the window starts past the buffer",
			raw:    []byte("d83d"),
			at:     1,
			want:   0,
			wantOK: false,
		},
		{
			name:   "four bytes that are not hex are refused",
			raw:    []byte("zzzz"),
			at:     0,
			want:   0,
			wantOK: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, ok := hex4(tc.raw, tc.at)
			assert.Equal(t, tc.wantOK, ok, "hex4(%q, %d) ok", tc.raw, tc.at)
			assert.Equal(t, tc.want, got, "hex4(%q, %d) value", tc.raw, tc.at)
		})
	}
}
