package ci

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/goenv"
	"github.com/stretchr/testify/require"
)

// TestTheFleetRunsTheSprintsMember pins the executable's package graph to the
// member implementation exercised by nova-sprint's functional tests.
func TestTheFleetRunsTheSprintsMember(t *testing.T) {
	t.Parallel()
	cmd := exec.Command("go", "list", "-deps", "./cmd/nova-swarm")
	cmd.Dir = "../.."
	cmd.Env = goenv.Clean(os.Environ())
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, string(output))
	const owner = "github.com/mas-bandwidth/nova-sprint/pkg/member"
	var members []string
	for _, path := range strings.Fields(string(output)) {
		if strings.HasSuffix(path, "/pkg/member") {
			members = append(members, path)
		}
	}
	require.Equal(t, []string{owner}, members, "the fleet binary must have one member loop, owned by nova-sprint")
}
