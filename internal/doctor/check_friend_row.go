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
	var widthFixes []string
	other := false
	for _, d := range daemons {
		out, err := env.Exec(ctx, "nova-config", "friend", "show", d.Name, "--json")
		if err != nil {
			bad = append(bad, fmt.Sprintf("daemon %s: nova-config friend show did not answer: %s", d.Name, firstLine(err.Error())))
			other = true
			continue
		}
		mode := jsonString(out, "mode")
		roles := jsonStrings(out, "roles")
		width := jsonInt(out, "width")
		harness := argValue(d.Argv, "--harness")
		argvWidth := argValue(d.Argv, "--width")
		if mode != "one-shot" {
			bad = append(bad, fmt.Sprintf("daemon %s: row mode is %q, not one-shot", d.Name, mode))
			other = true
		}
		if harness != "" && !slices.Contains(roles, "builder") {
			bad = append(bad, fmt.Sprintf("daemon %s: row roles %s lack builder while the daemon runs %s cards", d.Name, strings.Join(roles, ","), harness))
			other = true
		}
		// A width mismatch does not say which side is wrong: the row can be
		// right and the daemon's --width stale, so name both and both ways
		// rather than leading with a write (docs/SPEC-DOCTOR.md, "friend-row").
		if argvWidth != "" && width != 0 && fmt.Sprint(width) != argvWidth {
			bad = append(bad, fmt.Sprintf("daemon %s: row width is %d, --width is %s", d.Name, width, argvWidth))
			widthFixes = append(widthFixes, fmt.Sprintf("daemon %s: decide which width is right, then start the daemon with --width %d, or set the row with nova-config friend set %s --width %s", d.Name, width, d.Name, argvWidth))
		}
	}
	if len(bad) > 0 {
		fixes := widthFixes
		if other {
			fixes = append(fixes, "nova-config friend set <name> --mode one-shot --roles builder,reader --width <n>, then nova-sprint friend sync")
		}
		return Result{Status: Fail, Evidence: strings.Join(bad, "; "), Fix: strings.Join(fixes, "; ")}
	}
	return Result{Status: OK, Evidence: fmt.Sprintf("%d friend daemons have usable rows", len(daemons))}
}
