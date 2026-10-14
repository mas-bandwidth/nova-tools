package doctor

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/hook"
	"github.com/mas-bandwidth/nova-tools/pkg/buildinfo"
)

// friendsHookedTimeout bounds one nova-hook answer.
const friendsHookedTimeout = 10 * time.Second

func init() {
	Default.Register(Check{
		Name:       "friends-hooked",
		Dependency: "each friend and nova-hook serve on the installed nova-hook version",
		Run:        checkFriendsHooked,
	})
}

// hookedFriend is one friend row of the hook record, as nova-hook friends reports it.
type hookedFriend struct {
	name, role, state, version, reason string
}

// checkFriendsHooked holds every friend in the hook record, and the running
// nova-hook serve, to the installed nova-hook. A friend who is not up fails.
// A friend who is up but not on that version fails. A serve whose version
// differs from the installed binary fails. The record, the installed version
// and the serve version are what nova-hook answers; a test fakes those answers
// (docs/SPEC-DOCTOR.md, "friends-hooked").
func checkFriendsHooked(ctx context.Context, env Env) Result {
	bin := toolsOnPath(env)["nova-hook"]
	if bin == "" {
		return Result{Status: Fail, Evidence: "nova-hook is not on PATH",
			Fix: "nova-update apply --file <manifest> nova-hook"}
	}
	cctx, cancel := context.WithTimeout(ctx, friendsHookedTimeout)
	verOut, err := env.Exec(cctx, bin, "version")
	cancel()
	if err != nil {
		return Result{Status: Fail, Evidence: "nova-hook did not answer `version`: " + firstLine(err.Error()),
			Fix: "nova-update apply --file <manifest> nova-hook"}
	}
	parsed, ok := buildinfo.Parse(verOut)
	if !ok {
		return Result{Status: Fail, Evidence: "nova-hook did not answer `version` with a version line",
			Fix: "nova-update apply --file <manifest> nova-hook"}
	}
	cctx, cancel = context.WithTimeout(ctx, friendsHookedTimeout)
	raw, err := env.Exec(cctx, bin, "friends", "--json")
	cancel()
	if err != nil {
		return Result{Status: Fail, Evidence: "nova-hook friends did not answer: " + firstLine(err.Error()),
			Fix: "nova-hook friends --json"}
	}
	friends, serve, ok := parseHookFriends(raw)
	if !ok {
		return Result{Status: Fail, Evidence: "nova-hook friends did not answer with a hook record",
			Fix: "nova-hook friends --json"}
	}
	return judgeFriendsHooked(parsed.Version, serve, friends)
}

// parseHookFriends reads the friends listing. ok is false when raw is not that
// JSON. A friend is a role friend or comms; other names are not this check's.
func parseHookFriends(raw string) (friends []hookedFriend, serve string, ok bool) {
	var doc struct {
		Facts map[string]any `json:"facts"`
		Items []struct {
			Fields map[string]any `json:"fields"`
		} `json:"items"`
	}
	if json.Unmarshal([]byte(raw), &doc) != nil {
		return nil, "", false
	}
	if doc.Facts != nil {
		serve, _ = doc.Facts["serve_version"].(string)
	}
	for _, it := range doc.Items {
		if it.Fields == nil {
			continue
		}
		name, _ := it.Fields["name"].(string)
		role, _ := it.Fields["role"].(string)
		if name == "" || (role != string(hook.Friend) && role != string(hook.Comms)) {
			continue
		}
		state, _ := it.Fields["state"].(string)
		version, _ := it.Fields["version"].(string)
		reason, _ := it.Fields["reason"].(string)
		friends = append(friends, hookedFriend{name, role, state, version, reason})
	}
	return friends, serve, true
}

// judgeFriendsHooked is the check's decision. Up or down is the record's own
// state, already judged at the store's time. This check does not read a clock.
func judgeFriendsHooked(installed, serve string, friends []hookedFriend) Result {
	slices.SortFunc(friends, func(a, b hookedFriend) int { return strings.Compare(a.name, b.name) })
	var problems []string
	var fix string
	note := func(problem, remedy string) {
		problems = append(problems, problem)
		if fix == "" {
			fix = remedy
		}
	}
	update := fmt.Sprintf("nova-update apply --file <manifest> nova-hook --version %s", installed)
	for _, f := range friends {
		if f.state != "up" {
			why := f.reason
			if why == "" {
				why = "down"
			}
			note(fmt.Sprintf("%s is not up (%s)", f.name, why), fmt.Sprintf("nova-hook hook --as %s --role %s", f.name, f.role))
			continue
		}
		switch {
		case f.version == "":
			note(fmt.Sprintf("%s is up and reports no nova-hook version, installed nova-hook is %s", f.name, installed), update)
		case f.version != installed:
			note(fmt.Sprintf("%s is up on %s, installed nova-hook is %s", f.name, f.version, installed), update)
		}
	}
	if serve != installed {
		got := serve
		if got == "" {
			got = "no version"
		}
		note(fmt.Sprintf("nova-hook serve reports %s, installed nova-hook is %s", got, installed), update)
	}
	if len(problems) > 0 {
		return Result{Status: Fail, Evidence: strings.Join(problems, "; "), Fix: fix}
	}
	if len(friends) == 0 {
		return Result{Status: OK, Evidence: fmt.Sprintf("no friends in the hook record; nova-hook serve is %s", installed)}
	}
	word := "friends"
	if len(friends) == 1 {
		word = "friend"
	}
	return Result{Status: OK, Evidence: fmt.Sprintf("%d %s on %s; nova-hook serve is %s", len(friends), word, installed, installed)}
}
