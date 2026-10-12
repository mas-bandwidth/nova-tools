// Command nova-doctor says what is missing for the nova tools to work and the one line
// that fixes each. The contract is docs/SPEC-DOCTOR.md; the frame and the checks are
// internal/doctor's, one file per dependency.
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/doctor"
	"github.com/mas-bandwidth/nova-tools/internal/up"
	"github.com/mas-bandwidth/nova-tools/pkg/buildinfo"
)

var version string

func main() {
	doctor.Default.Register(hookCheck())
	os.Exit(doctor.Main(doctor.Default, doctor.OSEnv{}, version, os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

// hookTimeout bounds one tool's `version` answer, as the self check does.
const hookTimeout = 10 * time.Second

// hookCheck is nova-doctor's own check for the one way in. nova-hook is a binary
// every machine and every seat has after an adopt, like nova-bus and nova-sprint,
// so its absence, or a version other than the release the other nova tools report,
// is a fail (never a warn) naming the command that installs it.
func hookCheck() doctor.Check {
	return doctor.Check{
		Name:       "hook",
		Dependency: "nova-hook on PATH, at the adopted release",
		Run:        hookInstalled,
	}
}

// hookInstalled fails when nova-hook is not on PATH, does not answer `version`,
// or answers a version that differs from the release most nova tools report.
func hookInstalled(ctx context.Context, env doctor.Env) doctor.Result {
	tools := newToolsOnPath(env)
	names := make([]string, 0, len(tools))
	for n := range tools {
		names = append(names, n)
	}
	slices.Sort(names)

	byVersion := map[string][]string{}
	for _, n := range names {
		if v, ok := versionOf(ctx, env, tools[n]); ok {
			byVersion[v] = append(byVersion[v], n)
		}
	}
	release := releaseVersion(byVersion)

	if _, ok := tools[up.HookBinary]; !ok {
		return doctor.Result{Status: doctor.Fail,
			Evidence: fmt.Sprintf("%s is not on PATH (%d nova-* tools on PATH)", up.HookBinary, len(names)),
			Fix:      hookFix(release)}
	}
	got, ok := versionOf(ctx, env, tools[up.HookBinary])
	if !ok {
		return doctor.Result{Status: doctor.Fail,
			Evidence: fmt.Sprintf("%s did not answer `version` with a version line", up.HookBinary),
			Fix:      hookFix(release)}
	}
	if release != "" && got != release {
		return doctor.Result{Status: doctor.Fail,
			Evidence: fmt.Sprintf("%s=%s differs from the release %s the other nova tools report", up.HookBinary, got, release),
			Fix:      hookFix(release)}
	}
	return doctor.Result{Status: doctor.OK, Evidence: fmt.Sprintf("%s=%s on PATH", up.HookBinary, got)}
}

// hookFix is the one command that installs nova-hook at the adopted release.
func hookFix(release string) string {
	if release == "" {
		release = "<release>"
	}
	return fmt.Sprintf("nova-update apply --file <manifest> %s --version %s", up.HookBinary, release)
}

// versionOf asks one tool for its `version` line and returns the version token.
func versionOf(ctx context.Context, env doctor.Env, path string) (string, bool) {
	cctx, cancel := context.WithTimeout(ctx, hookTimeout)
	out, err := env.Exec(cctx, path, "version")
	cancel()
	if err != nil {
		return "", false
	}
	f, ok := buildinfo.Parse(out)
	if !ok {
		return "", false
	}
	return f.Version, true
}

// releaseVersion is the version most tools report; ties go to the greater version
// string, so the answer is the same on every run.
func releaseVersion(byVersion map[string][]string) string {
	versions := make([]string, 0, len(byVersion))
	for v := range byVersion {
		versions = append(versions, v)
	}
	slices.SortFunc(versions, func(a, b string) int {
		if d := len(byVersion[b]) - len(byVersion[a]); d != 0 {
			return d
		}
		return strings.Compare(b, a)
	})
	if len(versions) == 0 {
		return ""
	}
	return versions[0]
}

// newToolsOnPath maps each nova-* name to the executable PATH resolves it to, the
// first of a name winning as the shell resolves it.
func newToolsOnPath(env doctor.Env) map[string]string {
	found := map[string]string{}
	for _, dir := range filepath.SplitList(env.Getenv("PATH")) {
		if dir == "" {
			continue
		}
		entries, err := env.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			name := e.Name()
			if !strings.HasPrefix(name, "nova-") || e.IsDir() || found[name] != "" {
				continue
			}
			if info, err := e.Info(); err != nil || info.Mode()&0o111 == 0 {
				continue
			}
			found[name] = filepath.Join(dir, name)
		}
	}
	return found
}
