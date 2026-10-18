package doctor

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/mas-bandwidth/nova-tools/pkg/friend"
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
				self = friend.AIPath(home, coord)
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
			Fix: "the friend daemons are retired (2026-10-17; nova-hook is the friend channel): boot this daemon out and remove its unit; a lane --dir goes beside the self (a sibling directory), never inside it"}
	}
	return Result{Status: OK, Evidence: fmt.Sprintf("%d friend daemons write outside the wall", len(daemons))}
}

// pathWithin is whether a and b are equal or one contains the other, after
// resolving symlinks where the paths exist, so a --dir that is a link into the
// self is caught as well as a literal path.
func pathWithin(a, b string) bool {
	a, b = resolveLinks(a), resolveLinks(b)
	if a == b {
		return true
	}
	return strings.HasPrefix(a, b+string(filepath.Separator)) || strings.HasPrefix(b, a+string(filepath.Separator))
}

// resolveLinks is the path with every symlink resolved where it exists, and the
// cleaned path where it does not (a path the machine has not made yet).
func resolveLinks(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return filepath.Clean(p)
}
