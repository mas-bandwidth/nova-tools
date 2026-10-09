package doctor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// goProbeTimeout bounds one `go` or `sh` probe the gosdk check runs.
const goProbeTimeout = 10 * time.Second

func init() {
	Default.Register(Check{Name: "gosdk", Dependency: "the Go toolchain", Run: checkGoSDK})
}

// checkGoSDK holds the Go toolchain where it belongs (docs/SETUP.md,
// dep-go-sdk-b.w2). A bench builds and tests the repository, so there go must
// be the version go.mod's toolchain line names, GOCACHE writable, and GOFLAGS
// carry -mod=readonly. The coordinator's machine runs no go build or test (the
// bench rule), so a `go` on its PATH is a warn naming that rule. The role is
// the machine's own: the coordinator's seat is named "coordinator" and a
// bench's login is "bench".
func checkGoSDK(ctx context.Context, env Env) Result {
	goPath := goOnPath(env)
	if coordinatorSeat(env) {
		if goPath == "" {
			return Result{Status: OK, Evidence: "no go on PATH: the coordinator's machine runs no go build or test (the bench rule)"}
		}
		v := goVersion(ctx, env, goPath)
		if v != "" {
			v = " " + v
		}
		return Result{Status: Warn,
			Evidence: "go is on PATH at " + goPath + v + ": the coordinator's machine runs no go build or test (the bench rule)",
			Fix:      "build and test on a bench, and install a released binary here with `nova-update apply --file <manifest> <tool>` (docs/SETUP.md, dep-go-sdk-b.w2)"}
	}

	const doc = "docs/SETUP.md, dep-go-sdk-b.w2"
	if goPath == "" {
		return Result{Status: Fail, Evidence: "no go on PATH and a bench builds and tests here",
			Fix: "install the Go toolchain go.mod's toolchain line names on this bench (" + doc + ")"}
	}
	got := goVersion(ctx, env, goPath)
	if got == "" {
		return Result{Status: Fail, Evidence: fmt.Sprintf("%s did not answer `go version`", goPath),
			Fix: "reinstall the Go toolchain go.mod's toolchain line names on this bench (" + doc + ")"}
	}
	want, exact, err := toolchainVersion(ctx, env, goPath)
	if err != nil {
		return Result{Status: Fail, Evidence: err.Error(),
			Fix: "run nova-doctor run in a checkout of this repository, whose go.mod names the bench's toolchain (" + doc + ")"}
	}
	if !goMatches(got, want, exact) {
		return Result{Status: Fail,
			Evidence: fmt.Sprintf("go is %s, go.mod wants %s", got, want),
			Fix:      fmt.Sprintf("install Go %s on this bench, the version go.mod's toolchain line names (%s)", want, doc)}
	}
	gocache := goEnvValue(ctx, env, goPath, "GOCACHE")
	if gocache == "" {
		return Result{Status: Fail, Evidence: "go reports no GOCACHE",
			Fix: "export GOCACHE to a directory this user writes before any go command (" + doc + ")"}
	}
	if !dirWritable(ctx, env, gocache) {
		return Result{Status: Fail, Evidence: "GOCACHE " + gocache + " is not writable",
			Fix: "export GOCACHE to a directory this user writes, e.g. export GOCACHE=$HOME/.cache/go-build (" + doc + ")"}
	}
	if !hasFlag(goEnvValue(ctx, env, goPath, "GOFLAGS"), "-mod=readonly") {
		return Result{Status: Warn, Evidence: "go " + got + ", toolchain " + want + ", GOCACHE " + gocache + "; GOFLAGS does not carry -mod=readonly",
			Fix: "export GOFLAGS=-mod=readonly before any go command on a bench (" + doc + ")"}
	}
	return Result{Status: OK, Evidence: "go " + got + ", go.mod toolchain " + want + ", GOCACHE " + gocache + " writable, GOFLAGS -mod=readonly"}
}

// coordinatorSeat reports whether this machine holds the coordinator's seat.
// nova-up writes the seat as NOVA_SECRETS_SEAT=coordinator in <root>/seat.env;
// a bench's loops log in to the store as the bench user. A machine that names
// neither is read as a bench, where go is expected.
func coordinatorSeat(env Env) bool {
	return env.Getenv("NOVA_SECRETS_SEAT") == "coordinator" || env.Getenv("NOVA_SPRINT_REDIS_USER") == "coordinator"
}

// goOnPath is the `go` PATH resolves, the first of a name winning as the
// shell resolves it, or "".
func goOnPath(env Env) string {
	for _, dir := range filepath.SplitList(env.Getenv("PATH")) {
		if dir == "" {
			continue
		}
		entries, err := env.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.Name() != "go" || e.IsDir() {
				continue
			}
			// a symlinked go (a version manager, a package manager) is a
			// candidate; a broken one fails at its first probe.
			if info, err := e.Info(); err != nil || (info.Mode()&0o111 == 0 && e.Type()&os.ModeSymlink == 0) {
				continue
			}
			return filepath.Join(dir, "go")
		}
	}
	return ""
}

// goVersion is the version token of `go version` with its `go` prefix
// stripped ("1.24.5"), or "" when go does not answer.
func goVersion(ctx context.Context, env Env, goPath string) string {
	cctx, cancel := context.WithTimeout(ctx, goProbeTimeout)
	defer cancel()
	out, err := env.Exec(cctx, goPath, "version")
	if err != nil {
		return ""
	}
	for _, w := range strings.Fields(out) {
		if v, ok := strings.CutPrefix(w, "go"); ok && v != "" && v[0] >= '0' && v[0] <= '9' {
			return v
		}
	}
	return ""
}

// goEnvValue is `go env <key>` trimmed, or "".
func goEnvValue(ctx context.Context, env Env, goPath, key string) string {
	cctx, cancel := context.WithTimeout(ctx, goProbeTimeout)
	defer cancel()
	out, err := env.Exec(cctx, goPath, "env", key)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// toolchainVersion reads the toolchain version go.mod names: the `toolchain
// goX.Y.Z` line exactly, else the `go X.Y` directive's major.minor. exact is
// true for the toolchain line. A go.mod that names neither is an error.
func toolchainVersion(ctx context.Context, env Env, goPath string) (want string, exact bool, err error) {
	gomod := goEnvValue(ctx, env, goPath, "GOMOD")
	if gomod == "" || gomod == "/dev/null" || gomod == "NUL" {
		return "", false, fmt.Errorf("no go.mod here: the bench's toolchain is not named")
	}
	raw, rerr := env.ReadFile(gomod)
	if rerr != nil {
		return "", false, fmt.Errorf("go.mod is not readable: %v", rerr)
	}
	var directive string
	for _, line := range strings.Split(string(raw), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == "toolchain" {
			return strings.TrimPrefix(fields[1], "go"), true, nil
		}
		if len(fields) >= 2 && fields[0] == "go" && directive == "" {
			directive = fields[1]
		}
	}
	if directive == "" {
		return "", false, fmt.Errorf("go.mod names no toolchain and no go directive")
	}
	return directive, false, nil
}

// goMatches reports whether got (go's own version, X.Y.Z) is what want asks:
// equal for a toolchain line, the same major.minor for a go directive.
func goMatches(got, want string, exact bool) bool {
	if exact {
		return got == want
	}
	return got == want || strings.HasPrefix(got, want+".")
}

// hasFlag reports whether flags carries word, which may be one of several.
func hasFlag(flags, word string) bool {
	return slices.Contains(strings.Fields(flags), word)
}

// dirWritable reports whether dir can be written to, by asking `test -w`; the
// check changes nothing itself.
func dirWritable(ctx context.Context, env Env, dir string) bool {
	cctx, cancel := context.WithTimeout(ctx, goProbeTimeout)
	defer cancel()
	_, err := env.Exec(cctx, "sh", "-c", `test -w "$1"`, "sh", dir)
	return err == nil
}
