package deal

import (
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
)

// TestDealStatusHelpNamesRedisUser: the verb's help names the environment
// variable that picks the Redis seat (#3605), so a bench operator is not left
// to find NOPERM/NOAUTH by trial.
func TestDealStatusHelpNamesRedisUser(t *testing.T) {
	t.Parallel()

	if !strings.Contains(StatusUsage, store.UserEnv+"=bench") {
		t.Fatalf("deal status help %q does not name %s=bench", StatusUsage, store.UserEnv)
	}
}
