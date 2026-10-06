package doctor

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/buildinfo"
)

func init() {
	Register(Check{Name: "self", Covers: "the nova tools on PATH, all from one release", Run: checkSelf})
}

// installFix is the verb that puts one release's tools in a directory.
const installFix = "nova-update release install --from <release dir> --version %s --bin %s"

// checkSelf finds every nova-* tool on PATH (the first copy of a name is the
// one that runs), reads each one's version line, and fails when they are not
// one release: it names the tools off the most common version, or every tool
// when no version holds a majority (docs/SPEC-DOCTOR.md, "self").
func checkSelf(ctx context.Context, env Env) Result {
	type tool struct{ name, dir, version string }
	var found []tool
	seen := map[string]bool{}
	for _, dir := range filepath.SplitList(env.Getenv("PATH")) {
		names, err := env.ReadDir(dir)
		if err != nil {
			continue
		}
		slices.Sort(names)
		for _, n := range names {
			if strings.HasPrefix(n, "nova-") && !seen[n] {
				seen[n] = true
				found = append(found, tool{name: n, dir: dir})
			}
		}
	}
	if len(found) == 0 {
		return Result{Status: Fail, Evidence: "no nova-* tool on PATH",
			Fix: "nova-update release install --from <release dir> --version <v> --bin <dir>, then put <dir> on PATH"}
	}
	var unread []string
	count := map[string]int{}
	for i, t := range found {
		out, err := env.Exec(ctx, filepath.Join(t.dir, t.name), "version")
		f, ok := buildinfo.Parse(out)
		if err != nil || !ok {
			unread = append(unread, t.name)
			continue
		}
		found[i].version = f.Version
		count[f.Version]++
	}
	if len(unread) > 0 {
		return Result{Status: Fail, Evidence: "no version line from " + strings.Join(unread, ", "),
			Fix: "nova-update release install --from <release dir> --version <v> --bin <dir>"}
	}
	release, top, tie := "", 0, false
	for v, n := range count {
		switch {
		case n > top:
			release, top, tie = v, n, false
		case n == top:
			tie = true
		}
	}
	if len(count) == 1 {
		return Result{Status: OK, Evidence: fmt.Sprintf("%d tools on PATH, all %s", len(found), release)}
	}
	var odd []string
	for _, t := range found {
		if tie || t.version != release {
			odd = append(odd, t.name+" "+t.version)
		}
	}
	res := Result{Status: Fail, Evidence: "the tools are not one release: " + strings.Join(odd, ", ")}
	if tie {
		res.Evidence += " (no version holds a majority)"
		res.Fix = "nova-update release install --from <release dir> --version <v> --bin <dir>"
		return res
	}
	for _, t := range found {
		if t.version != release {
			res.Fix = fmt.Sprintf(installFix, release, t.dir)
			break
		}
	}
	return res
}
