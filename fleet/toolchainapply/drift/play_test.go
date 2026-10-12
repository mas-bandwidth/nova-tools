package drift

import (
	"testing"

	"github.com/mas-bandwidth/nova-tools/fleet/internal/toolchainfixture"
)

func TestToolchainsPlayRepairsExecutableDrift(t *testing.T) {
	t.Parallel()
	toolchainfixture.Check(t, "../..", "drift")
}
