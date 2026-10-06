package doctor

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/buildinfo"
)

// selfTools are the nova tools a full install puts on PATH, one per cmd/
// directory. A tool absent from PATH is a warn, never a fail: one tool is a
// fine number.
var selfTools = []string{
	"nova-bus", "nova-cairn", "nova-card", "nova-check", "nova-ci", "nova-config",
	"nova-decide", "nova-doctor", "nova-friend", "nova-fuse", "nova-memory",
	"nova-redis", "nova-sandbox", "nova-secrets", "nova-self-talk", "nova-sprint",
	"nova-swarm", "nova-table", "nova-tokens", "nova-update", "nova-version", "nova-work",
}

func init() {
	Register(Check{
		Name:   "self",
		Covers: "the nova tools on PATH, each one's version, all from one release",
		Run:    runSelf,
	})
}

// runSelf asks each nova tool found on PATH for its version line and compares
// the build identities: the identity most tools share is the release, and a tool
// that differs is the odd one (SPEC-DOCTOR, check self).
func runSelf(ctx context.Context, env Env) Result {
	var missing, broken []string
	versions := map[string]string{} // tool -> build identity
	for _, name := range selfTools {
		path, err := env.LookPath(name)
		if err != nil {
			missing = append(missing, name)
			continue
		}
		out, err := env.Exec(ctx, path, "version")
		f, ok := buildinfo.Parse(out)
		if err != nil || !ok {
			broken = append(broken, name)
			continue
		}
		versions[name] = f.Version
	}
	if len(versions) == 0 && len(broken) == 0 {
		return Result{Status: Fail, Evidence: "no nova tool is on PATH",
			Fix: "install the release as docs/SETUP.md says, then run nova-doctor"}
	}
	if len(broken) > 0 {
		return Result{Status: Fail,
			Evidence: "no version line from " + strings.Join(broken, ", ") + " (`<tool> version` did not print one)",
			Fix:      "nova-update status"}
	}
	release := majority(versions)
	var odd []string
	for _, name := range selfTools {
		if v, ok := versions[name]; ok && v != release {
			odd = append(odd, fmt.Sprintf("%s=%s", name, v))
		}
	}
	if len(odd) > 0 {
		return Result{Status: Fail,
			Evidence: fmt.Sprintf("skew: %s differ from release %s carried by the rest", strings.Join(odd, ", "), release),
			Fix:      "nova-update status"}
	}
	evidence := fmt.Sprintf("%d tools on PATH, all release %s", len(versions), release)
	if len(missing) > 0 {
		return Result{Status: Warn, Evidence: evidence + "; not on PATH: " + strings.Join(missing, ", "),
			Fix: "nova-update status"}
	}
	return Result{Status: OK, Evidence: evidence}
}

// majority is the identity the most tools carry; a tie goes to the identity that
// sorts first, so the verdict is the same on every run.
func majority(versions map[string]string) string {
	count := map[string]int{}
	for _, v := range versions {
		count[v]++
	}
	var all []string
	for v := range count {
		all = append(all, v)
	}
	sort.Slice(all, func(i, j int) bool {
		if count[all[i]] != count[all[j]] {
			return count[all[i]] > count[all[j]]
		}
		return all[i] < all[j]
	})
	return all[0]
}
