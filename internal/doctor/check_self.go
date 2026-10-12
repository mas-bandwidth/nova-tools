package doctor

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/buildinfo"
)

// selfTimeout bounds one tool's `version` answer.
const selfTimeout = 10 * time.Second

func init() {
	Default.Register(Check{Name: "self", Dependency: "the nova tools on PATH", Run: checkSelf})
}

// checkSelf finds every nova-* executable on PATH (the first of a name wins, as the
// shell resolves it), asks each for its `version` line, and holds them to one release:
// the version token of every line is the same. A tool that does not answer, or answers
// something that is no version line, is a fail naming it; so is a skew, naming the
// tools that differ from the version most of them report (docs/SPEC-DOCTOR.md, "self").
func checkSelf(ctx context.Context, env Env) Result {
	tools := toolsOnPath(env)
	if len(tools) == 0 {
		return Result{Status: Fail, Evidence: "no nova-* tool is on PATH",
			Fix: "nova-update apply --file <manifest> <tool>, or put the directory holding the nova tools on PATH"}
	}
	names := make([]string, 0, len(tools))
	for n := range tools {
		names = append(names, n)
	}
	slices.Sort(names)

	byVersion := map[string][]string{}
	var broken []string
	for _, n := range names {
		cctx, cancel := context.WithTimeout(ctx, selfTimeout)
		out, err := env.Exec(cctx, tools[n], "version")
		cancel()
		f, ok := buildinfo.Parse(out)
		if err != nil || !ok {
			broken = append(broken, n)
			continue
		}
		byVersion[f.Version] = append(byVersion[f.Version], n)
	}
	if len(broken) > 0 {
		return Result{Status: Fail,
			Evidence: fmt.Sprintf("%s did not answer `version` with a version line", strings.Join(broken, ", ")),
			Fix:      "nova-update apply --file <manifest> " + broken[0]}
	}
	if len(byVersion) == 1 {
		for v := range byVersion {
			return Result{Status: OK, Evidence: fmt.Sprintf("%d tools on PATH, all %s", len(names), v)}
		}
	}
	// The release is the version most tools report; ties go to the greater version
	// string, so the answer is the same on every run.
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
	release, odd := versions[0], versions[1:]
	var parts []string
	var first string
	for _, v := range odd {
		for _, n := range byVersion[v] {
			parts = append(parts, n+"="+v)
			if first == "" {
				first = n
			}
		}
	}
	slices.Sort(parts)
	return Result{Status: Fail,
		Evidence: fmt.Sprintf("the tools are not one release: %s differ from %s (%d tools)", strings.Join(parts, ", "), release, len(byVersion[release])),
		Fix:      fmt.Sprintf("nova-update apply --file <manifest> %s --version %s", first, release)}
}

// toolsOnPath maps each nova-* name to the executable PATH resolves it to.
func toolsOnPath(env Env) map[string]string {
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
