package baddigest

import (
	"testing"

	"github.com/mas-bandwidth/nova-tools/fleet/internal/toolchainfixture"
)

func TestToolchainsPlayPreservesInstallOnChecksumFailure(t *testing.T) {
	t.Parallel()
	toolchainfixture.Check(t, "../..", "bad-digest")
}
