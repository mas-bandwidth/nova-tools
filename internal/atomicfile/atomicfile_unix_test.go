//go:build !windows

package atomicfile

import (
	"os"
	"syscall"
	"testing"
)

func TestUmaskAndMode(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		env      string
		umask    int
		filename string
		content  string
		perm     os.FileMode
		opts     []Option
		want     os.FileMode
	}{
		{"UmaskHonored", "GO_TEST_SUBPROCESS_UMASK=077", 0o077, "umask_077_test.txt", "private content\n", 0o644, nil, 0o600},
		{"UmaskGroupWritableHonored", "GO_TEST_SUBPROCESS_UMASK=002", 0o002, "umask_002_test.txt", "group content\n", 0o666, nil, 0o664},
		{"ExactModeBypassesUmask", "GO_TEST_SUBPROCESS_EXACT_MODE=077", 0o077, "exact_mode_077_test.txt", "exact content\n", 0o644, []Option{ExactMode()}, 0o644},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := newRig(t)
			r.rerun(tc.env, func() {
				syscall.Umask(tc.umask)
				r.permIs(r.write(tc.filename, tc.content, tc.perm, tc.opts...), tc.want)
			})
		})
	}
}
