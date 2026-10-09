//go:build linux

package friend

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNativeControlTrustedAccountHomeNeverUsesEnvironmentFallback(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, data, want string }{
		{"trusted", "account:x:41:41::/account-home:/bin/sh\n", "/account-home"},
		{"missing", "other:x:42:42::/other:/bin/sh\n", ""},
		{"duplicate", "account:x:41:41::/first:/bin/sh\nother:x:41:41::/second:/bin/sh\n", ""},
		{"empty", "account:x:41:41:::/bin/sh\n", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			home, err := nativeAccountHomeFrom(strings.NewReader(tc.data), 41)
			if tc.want == "" {
				assert.Error(t, err)
				assert.Empty(t, home)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tc.want, home)
			}
		})
	}
}
