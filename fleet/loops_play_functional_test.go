package fleet

import (
	"context"
	"encoding/json"
	"os/exec"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// loops_play_functional_test.go renders fleet/loops.yml's Linux unit state
// through ansible-core's own templar (docs/SPEC-RELEASE.md, "The seat adopts an
// approved build"). At the base the task's state was a folded scalar holding a
// parenthesised ternary chained to a second `if ... else`; ansible-core stops at
// the `else` ("expected token ')', got 'else'"), so no Linux host ever restarted
// its unit. This renders the expression the task builds (loop_unit_state when
// present, else the module's state) for the four truth-table inputs and holds it
// to restarted, restarted, started and stopped.

// stateExpression is the expression the Linux state task builds: the
// loop_unit_state var when it is present, else the module's own state argument
// (which is what the base held, and what the card's error came from).
func stateExpression(task map[string]any) string {
	if vars, ok := task["vars"].(map[string]any); ok {
		if v, ok := vars["loop_unit_state"]; ok {
			return asText(v)
		}
	}
	systemd, _ := task["ansible.builtin.systemd_service"].(map[string]any)
	return asText(systemd["state"])
}

// renderState renders expr through ansible's templar with the given extra vars.
func renderState(t *testing.T, expr, extraVars string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ansible", "localhost",
		"-m", "ansible.builtin.debug",
		"-a", "msg="+expr,
		"-e", extraVars,
		"-c", "local",
		"--nocolor",
	)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func TestLoopsPlayStateExpressionRendersThroughTheTemplar(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("ansible"); err != nil {
		t.Skip("ansible-core is not installed on this machine; the structural test TestLoopsPlayStateExpressionIsBuiltOutsideTheModuleArgs still runs")
	}

	expr := stateExpression(linuxStateTask(t, loopsPlays(t)))
	require.NotEmpty(t, expr, "no state expression to render")

	for _, tc := range []struct {
		name    string
		enabled bool
		restart bool
		want    string
	}{
		{"disabled", false, false, "stopped"},
		{"enabled and changed", true, true, "restarted"},
		{"enabled and named by nova_loop_restart", true, true, "restarted"},
		{"enabled, unchanged and not restarted", true, false, "started"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			extra, err := json.Marshal(map[string]any{
				"item":                    map[string]any{"enabled": tc.enabled},
				"loop_unit_wants_restart": tc.restart,
			})
			require.NoError(t, err)
			out, err := renderState(t, expr, string(extra))
			require.NoErrorf(t, err, "ansible refused the state expression %q:\n%s", expr, out)
			assert.Contains(t, out, `"msg": "`+tc.want+`"`,
				"the templar yielded no %s for %s", tc.want, tc.name)
		})
	}
}
