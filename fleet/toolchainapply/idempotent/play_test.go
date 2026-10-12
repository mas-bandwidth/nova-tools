package idempotent

import (
	"testing"

	"github.com/mas-bandwidth/nova-tools/fleet/internal/toolchainfixture"
)

func TestToolchainsPlayIsIdempotent(t *testing.T) {
	t.Parallel()
	toolchainfixture.Check(t, "../..", "idempotent")
}
