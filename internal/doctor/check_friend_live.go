package doctor

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"
)

const friendBeatWindow = 15 * time.Second

func init() {
	Default.Register(Check{Name: "friend-live", Dependency: "each friend daemon's status", Run: checkFriendLive})
}

// checkFriendLive reads each daemon's status and fails when the hook gate
// refused its beats or inbox (it logs the refusal and takes nothing), when the
// status answered daemon=down (the daemon is not running, however its exit was),
// or when its last beat is older than the window while the agent is loaded
// (docs/SPEC-DOCTOR.md, "friend-live").
func checkFriendLive(ctx context.Context, env Env) Result {
	daemons, ok := friendDaemonsOrOK(env)
	if ok != nil {
		return *ok
	}
	var bad []string
	for _, d := range daemons {
		out, err := env.Exec(ctx, "nova-friend", "status", "--as", d.Name, "--json")
		if err != nil {
			bad = append(bad, fmt.Sprintf("daemon %s: nova-friend status did not answer: %s", d.Name, firstLine(err.Error())))
			continue
		}
		beatErr, inboxErr := jsonString(out, "beat_error"), jsonString(out, "inbox_error")
		if strings.Contains(beatErr, "refused") || strings.Contains(inboxErr, "refused") {
			line := beatErr
			if line == "" {
				line = inboxErr
			}
			bad = append(bad, fmt.Sprintf("daemon %s: %s", d.Name, line))
			continue
		}
		// a status that answered daemon=down at exit 0 is a down daemon: the
		// exit code alone cannot tell it from one that did not answer
		// (docs/SPEC-DOCTOR.md, "friend-live").
		if jsonString(out, "daemon") == "down" {
			line := "daemon=down"
			if since := jsonString(out, "status_age"); since != "" {
				line += " since " + since
			}
			bad = append(bad, fmt.Sprintf("daemon %s: %s", d.Name, line))
			continue
		}
		last := jsonString(out, "last_beat")
		if last == "" {
			continue
		}
		at, parseErr := time.Parse(time.RFC3339, last)
		if parseErr == nil && env.Now().Sub(at) > friendBeatWindow && daemonLoaded(ctx, env, d) {
			bad = append(bad, fmt.Sprintf("daemon %s: last beat %s is older than %s while the agent is loaded", d.Name, last, friendBeatWindow))
		}
	}
	if len(bad) > 0 {
		return Result{Status: Fail, Evidence: strings.Join(bad, "; "),
			Fix: "read ~/Library/Logs/nova-friend-<name>.log; nova-friend status --as <name>; nova-friend install --as <name>; for a hook refusal the seat must be hooked: nova-sprint hook"}
	}
	return Result{Status: OK, Evidence: fmt.Sprintf("%d friend daemons beat in the window", len(daemons))}
}

// daemonLoaded is whether the machine has the agent loaded, so a stale beat is
// a fault and not a stopped daemon.
func daemonLoaded(ctx context.Context, env Env, d friendDaemon) bool {
	if hostOS(env) == "linux" {
		_, err := env.Exec(ctx, "systemctl", "--user", "is-active", d.Label)
		return err == nil
	}
	_, err := env.Exec(ctx, "launchctl", "print", fmt.Sprintf("gui/%d/%s", os.Getuid(), d.Label))
	return err == nil
}
