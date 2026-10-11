package doctor

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
)

func init() {
	Default.Register(Check{Name: "friend-wall", Dependency: "each friend daemon's --dir against the lane wall's deny-self", Run: checkFriendWall})
}

// checkFriendWall fails when a daemon's --dir is inside (or contains) a
// deny-self path, so the lane wall refuses every write and the harness exits
// fast having burned attempts (docs/SPEC-DOCTOR.md, "friend-wall").
func checkFriendWall(ctx context.Context, env Env) Result {
	daemons, ok := friendDaemonsOrOK(env)
	if ok != nil {
		return *ok
	}
	home := env.Getenv("HOME")
	var bad []string
	for _, d := range daemons {
		dir := argValue(d.Argv, "--dir")
		if dir == "" {
			continue
		}
		self := argValue(d.Argv, "--deny-self")
		if self == "" {
			self = env.Getenv("NOVA_FRIEND_DENY_SELF")
		}
		if self == "" {
			if coord := argValue(d.Argv, "--coordinator"); coord != "" {
				self = filepath.Join(home, coord+"-working")
			}
		}
		if self == "" {
			continue
		}
		if pathWithin(dir, self) {
			bad = append(bad, fmt.Sprintf("daemon %s: --dir %s is inside the deny-self %s", d.Name, dir, self))
		}
	}
	if len(bad) > 0 {
		return Result{Status: Fail, Evidence: strings.Join(bad, "; "),
			Fix: "move --dir beside the self (a sibling directory), never inside it: nova-friend install --as <friend> --dir <sibling>"}
	}
	return Result{Status: OK, Evidence: fmt.Sprintf("%d friend daemons write outside the wall", len(daemons))}
}

// pathWithin is whether a and b are equal or one contains the other, by cleaned
// path.
func pathWithin(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	if a == b {
		return true
	}
	return strings.HasPrefix(a, b+string(filepath.Separator)) || strings.HasPrefix(b, a+string(filepath.Separator))
}
