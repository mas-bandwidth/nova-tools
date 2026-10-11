package doctor

import (
	"context"
	"fmt"
	"slices"
	"strings"
)

func init() {
	Default.Register(Check{Name: "friend-row", Dependency: "each friend daemon's nova-config row", Run: checkFriendRow})
}

// checkFriendRow reads each friend daemon's nova-config row and holds it to what
// the daemon can use: mode one-shot (a batch row is refused on write and read as
// one-shot, so the deal never hands it work), roles with builder while it runs
// cards, and a width equal to the daemon's --width (docs/SPEC-DOCTOR.md,
// "friend-row").
func checkFriendRow(ctx context.Context, env Env) Result {
	daemons, ok := friendDaemonsOrOK(env)
	if ok != nil {
		return *ok
	}
	var bad []string
	for _, d := range daemons {
		out, err := env.Exec(ctx, "nova-config", "friend", "show", d.Name, "--json")
		if err != nil {
			bad = append(bad, fmt.Sprintf("daemon %s: nova-config friend show did not answer: %s", d.Name, firstLine(err.Error())))
			continue
		}
		mode := jsonString(out, "mode")
		roles := jsonStrings(out, "roles")
		width := jsonInt(out, "width")
		harness := argValue(d.Argv, "--harness")
		argvWidth := argValue(d.Argv, "--width")
		if mode != "one-shot" {
			bad = append(bad, fmt.Sprintf("daemon %s: row mode is %q, not one-shot", d.Name, mode))
		}
		if harness != "" && !slices.Contains(roles, "builder") {
			bad = append(bad, fmt.Sprintf("daemon %s: row roles %s lack builder while the daemon runs %s cards", d.Name, strings.Join(roles, ","), harness))
		}
		if argvWidth != "" && width != 0 && fmt.Sprint(width) != argvWidth {
			bad = append(bad, fmt.Sprintf("daemon %s: row width %d differs from --width %s", d.Name, width, argvWidth))
		}
	}
	if len(bad) > 0 {
		return Result{Status: Fail, Evidence: strings.Join(bad, "; "),
			Fix: "nova-config friend set <name> --mode one-shot --roles builder,reader --width <n>, then nova-sprint friend sync"}
	}
	return Result{Status: OK, Evidence: fmt.Sprintf("%d friend daemons have usable rows", len(daemons))}
}
