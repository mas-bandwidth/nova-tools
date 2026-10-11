package doctor

import (
	"context"
	"fmt"
	"strings"
)

func init() {
	Default.Register(Check{Name: "friend-login", Dependency: "a claude friend's --config-dir sign-in", Run: checkFriendLogin})
}

// checkFriendLogin proves a claude daemon's --config-dir (argv, else the row's
// config_dir) holds a credential the harness accepts, by running the harness
// itself and reading its terminal_reason; an api_error with no API time is a
// config dir that was never signed in (docs/SPEC-DOCTOR.md, "friend-login").
// It never prints, copies or moves a credential: it reads only the answer.
func checkFriendLogin(ctx context.Context, env Env) Result {
	daemons, ok := friendDaemonsOrOK(env)
	if ok != nil {
		return *ok
	}
	var bad []string
	checked := 0
	for _, d := range daemons {
		if argValue(d.Argv, "--harness") != "claude" {
			continue
		}
		dir := argValue(d.Argv, "--config-dir")
		if dir == "" {
			if out, err := env.Exec(ctx, "nova-config", "friend", "show", d.Name, "--json"); err == nil {
				dir = jsonString(out, "config_dir")
			}
		}
		if dir == "" {
			bad = append(bad, fmt.Sprintf("daemon %s: claude has no --config-dir and its row names none", d.Name))
			continue
		}
		checked++
		out, err := env.Exec(ctx, "env", "CLAUDE_CONFIG_DIR="+dir, "claude", "-p", "ok", "--output-format", "json", "--max-turns", "1")
		reason := jsonString(out, "terminal_reason")
		if err == nil && reason == "success" {
			continue
		}
		if reason == "api_error" && jsonInt(out, "duration_api_ms") == 0 {
			bad = append(bad, fmt.Sprintf("daemon %s: config dir %s is not signed in", d.Name, dir))
		}
	}
	if len(bad) > 0 {
		return Result{Status: Fail, Evidence: strings.Join(bad, "; "),
			Fix: "CLAUDE_CONFIG_DIR=<dir> claude login (the owner's hand)"}
	}
	if checked == 0 {
		return Result{Status: OK, Evidence: "no claude friend daemons here"}
	}
	return Result{Status: OK, Evidence: fmt.Sprintf("%d claude friend daemons are signed in", checked)}
}
