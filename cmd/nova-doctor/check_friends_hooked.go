// This file is nova-doctor's friends-hooked check: the hook record names, for
// every rostered friend, the build that hooked it in, and the heartbeat
// server's own stamp names the build that passed it. Both must be the build
// the installed nova-hook is (docs/SPEC-DOCTOR.md, "friends-hooked").
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/doctor"
	"github.com/mas-bandwidth/nova-tools/pkg/buildinfo"
)

func init() {
	doctor.Default.Register(doctor.Check{Name: "friends-hooked", Dependency: "every friend's hook record and the heartbeat server's build", Run: checkFriendsHooked})
}

// friendRoles are the hook roles a friend's daemon holds: a friend on the
// sprint and its work, and a friend on the bus alone (hook.Friend,
// hook.Comms; docs/SPEC-HOOK.md, the roles).
var friendRoles = map[string]bool{"friend": true, "comms": true}

// friendsJSON is the part of `nova-hook friends --json` this check reads: the
// server facts and one item per name (pkg/tool's one output value).
type friendsJSON struct {
	Facts map[string]any `json:"facts"`
	Items []struct {
		Fields map[string]any `json:"fields"`
	} `json:"items"`
}

// checkFriendsHooked reads the hook record and the installed nova-hook's
// version and fails when a rostered friend is not up, when a friend that is up
// was hooked by another build, or when the running heartbeat server passed
// another build (docs/SPEC-DOCTOR.md, "friends-hooked"). The one fix names the
// friends and nova-update. A hook record that cannot be read is a warn: this
// check cannot say which build a friend runs without it.
func checkFriendsHooked(ctx context.Context, env doctor.Env) doctor.Result {
	raw, err := env.Exec(ctx, "nova-hook", "friends", "--json")
	if err != nil {
		return doctor.Result{Status: doctor.Warn, Evidence: "nova-hook friends did not answer: " + err.Error(),
			Fix: "nova-hook friends --json"}
	}
	var got friendsJSON
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		return doctor.Result{Status: doctor.Warn, Evidence: "nova-hook friends printed no JSON: " + err.Error(),
			Fix: "nova-hook friends --json"}
	}
	out, err := env.Exec(ctx, "nova-hook", "version")
	if err != nil {
		return doctor.Result{Status: doctor.Warn, Evidence: "nova-hook version did not answer: " + err.Error(),
			Fix: "nova-update apply --file <manifest> nova-hook"}
	}
	installed, ok := buildinfo.Parse(out)
	if !ok {
		return doctor.Result{Status: doctor.Warn, Evidence: "nova-hook version printed no version line: " + strings.TrimSpace(out),
			Fix: "nova-update apply --file <manifest> nova-hook"}
	}
	var bad, who []string
	for _, it := range got.Items {
		role, _ := it.Fields["role"].(string)
		if !friendRoles[role] {
			continue
		}
		name, _ := it.Fields["name"].(string)
		state, _ := it.Fields["state"].(string)
		build, _ := it.Fields["version"].(string)
		if state != "up" {
			bad = append(bad, fmt.Sprintf("friend %s is %s", name, state))
			who = append(who, name)
			continue
		}
		if build != installed.Version {
			bad = append(bad, fmt.Sprintf("friend %s is hooked by %s, installed nova-hook is %s", name, buildName(build), installed.Version))
			who = append(who, name)
		}
	}
	if alive, _ := got.Facts["serve_alive"].(bool); alive {
		if serve, _ := got.Facts["serve_version"].(string); serve != installed.Version {
			bad = append(bad, fmt.Sprintf("the running hook serve is %s, installed nova-hook is %s", buildName(serve), installed.Version))
			who = append(who, "hook-serve")
		}
	}
	if len(bad) > 0 {
		return doctor.Result{Status: doctor.Fail, Evidence: strings.Join(bad, "; "),
			Fix: strings.Join(who, ", ") + ": nova-update apply --file <manifest> nova-hook --version " + installed.Version}
	}
	return doctor.Result{Status: doctor.OK, Evidence: fmt.Sprintf("every friend is up on nova-hook %s", installed.Version)}
}

// buildName is a build identity as prose: "" is a build that recorded none.
func buildName(v string) string {
	if v == "" {
		return "an unrecorded build"
	}
	return v
}
