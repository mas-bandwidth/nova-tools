package doctor

import (
	"context"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
)

func init() {
	Default.Register(Check{Name: "plists", Dependency: "the com.nova launchd plists and nova systemd units this machine loads", Run: checkPlists})
}

// checkPlists lints every com.nova.*.plist under ~/Library/LaunchAgents and
// /Library/LaunchDaemons (systemd-analyze verify for nova-*.service on linux)
// so a hand-written stopgap that plutil refuses is named, not silent
// (docs/SPEC-DOCTOR.md, "plists").
func checkPlists(ctx context.Context, env Env) Result {
	paths := novaAgentFiles(env)
	if len(paths) == 0 {
		return Result{Status: OK, Evidence: "no nova launch agents here"}
	}
	var bad []string
	for _, p := range paths {
		out, err := env.Exec(ctx, "plutil", "-lint", p)
		if err != nil {
			bad = append(bad, fmt.Sprintf("%s: %s", p, firstLine(out)))
		}
	}
	if len(bad) > 0 {
		return Result{Status: Fail, Evidence: strings.Join(bad, "; "),
			Fix: "rewrite the agent with the tool that owns it (nova-friend install, nova-config loop) and remove a hand-written stopgap"}
	}
	return Result{Status: OK, Evidence: fmt.Sprintf("%d nova launch agents lint clean", len(paths))}
}

// novaAgentFiles is every com.nova.*.plist this machine loads (darwin), or every
// nova-*.service under the user's systemd directory (linux).
func novaAgentFiles(env Env) []string {
	home := env.Getenv("HOME")
	var dirs []string
	var prefix, suffix string
	if runtime.GOOS == "linux" {
		dirs = []string{filepath.Join(home, ".config", "systemd", "user")}
		prefix, suffix = "nova-", ".service"
	} else {
		dirs = []string{filepath.Join(home, "Library", "LaunchAgents"), "/Library/LaunchDaemons"}
		prefix, suffix = "com.nova.", ".plist"
	}
	var out []string
	for _, dir := range dirs {
		entries, err := env.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() || !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, suffix) {
				continue
			}
			out = append(out, filepath.Join(dir, name))
		}
	}
	return out
}
