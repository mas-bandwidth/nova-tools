package doctor

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/harness"
)

// harnessTimeout bounds the `nova-config friend list` read and one harness's
// `--version` probe, so a harness that does not answer leaves the friend judged,
// never the check hung.
const harnessTimeout = 10 * time.Second

func init() {
	Default.Register(Check{
		Name:       "harness",
		Dependency: "the friends' harnesses",
		Fleet:      true,
		Run:        checkHarness,
	})
}

// harnessFriend is one friend row as the check reads it: her name, her delivery
// mode, the config directory her claude one-shot lanes run with, and the harness
// her row names when it names one.
type harnessFriend struct {
	Name      string
	Mode      string
	ConfigDir string
	Harness   string
}

// checkHarness holds each friend row of this machine to its harness: the harness
// binary is on PATH and answers a version the adapter supports, and a claude
// one-shot friend's config directory is there. The harness is never run against a
// model: the one probe is `<binary> --version`. Every problem is named at once,
// each naming the friend (docs/SETUP.md, dep-harnesses-b.w2; docs/FRIENDS.md,
// "Claude friends: one account each").
func checkHarness(ctx context.Context, env Env) Result {
	friends, err := friendRows(ctx, env)
	if err != nil {
		return Result{Status: Fail,
			Evidence: "the friend rows could not be read: " + err.Error(),
			Fix:      "nova-config friend list --json"}
	}
	if len(friends) == 0 {
		return Result{Status: OK, Evidence: "no friend names a harness; nothing to check"}
	}

	var problems, fixes, oks []string
	for _, f := range friends {
		kind := friendHarness(f)
		if !slices.Contains(harness.Kinds, kind) {
			problems = append(problems, fmt.Sprintf("friend %s names harness %q, which the adapter does not know (want %s)", f.Name, kind, strings.Join(harness.Kinds, ", ")))
			fixes = append(fixes, "nova-config friend set "+f.Name+" --harness <one of "+strings.Join(harness.Kinds, ", ")+">")
			continue
		}
		bin := harnessOnPath(env, kind)
		if bin == "" {
			problems = append(problems, fmt.Sprintf("friend %s: the %s harness is not on PATH", f.Name, kind))
			fixes = append(fixes, "install "+kind+" on this machine, or nova-update apply --file <manifest> "+kind)
			continue
		}
		line, ok := harnessVersion(ctx, env, bin)
		if !ok {
			problems = append(problems, fmt.Sprintf("friend %s: the %s harness answered no version line (%s)", f.Name, kind, line))
			fixes = append(fixes, "install a "+kind+" the adapter supports on this machine, or nova-update apply --file <manifest> "+kind)
			continue
		}
		if f.ConfigDir != "" {
			if _, err := env.ReadDir(f.ConfigDir); err != nil {
				problems = append(problems, fmt.Sprintf("friend %s: the claude config directory %s is not there", f.Name, f.ConfigDir))
				fixes = append(fixes, "make "+f.ConfigDir+", or nova-config friend set "+f.Name+" --config_dir <the account's absolute config directory>")
				continue
			}
		}
		oks = append(oks, f.Name+"="+kind)
	}

	if len(problems) == 0 {
		return Result{Status: OK, Evidence: fmt.Sprintf("%d friend(s) checked: %s", len(oks), strings.Join(oks, ", "))}
	}
	ev := strings.Join(problems, "; ")
	if len(oks) > 0 {
		ev += "; ok: " + strings.Join(oks, ", ")
	}
	return Result{Status: Fail, Evidence: ev, Fix: fixes[0]}
}

// friendHarness is the harness a friend runs under: the one her row names, else
// the claude of a one-shot friend with a config directory (docs/FRIENDS.md,
// "Claude friends: one account each"), else opencode, the providers table's
// default (internal/swarm providers.go).
func friendHarness(f harnessFriend) string {
	if f.Harness != "" {
		return f.Harness
	}
	if f.Mode == "one-shot" && f.ConfigDir != "" {
		return harness.Claude
	}
	return harness.OpenCode
}

// friendRows reads nova-config's friend rows (`nova-config friend list --json`)
// through the one exec seam: each item's fields name the friend, her mode, her
// config directory and, when her row carries one, her harness.
func friendRows(ctx context.Context, env Env) ([]harnessFriend, error) {
	ctx, cancel := context.WithTimeout(ctx, harnessTimeout)
	defer cancel()
	out, err := env.Exec(ctx, "nova-config", "friend", "list", "--json")
	if err != nil {
		return nil, err
	}
	var wire struct {
		Items []struct {
			Fields struct {
				Name      string `json:"name"`
				Mode      string `json:"mode"`
				ConfigDir string `json:"config_dir"`
				Harness   string `json:"harness"`
			} `json:"fields"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &wire); err != nil {
		return nil, err
	}
	rows := make([]harnessFriend, 0, len(wire.Items))
	for _, it := range wire.Items {
		f := it.Fields
		rows = append(rows, harnessFriend{Name: f.Name, Mode: f.Mode, ConfigDir: f.ConfigDir, Harness: f.Harness})
	}
	return rows, nil
}

// harnessOnPath is the path of a harness binary on PATH, the first of a name
// winning as the shell resolves it, or "" when it is not there. A symlink (a
// version manager, a package manager) is a candidate; a broken one fails at its
// version probe.
func harnessOnPath(env Env, name string) string {
	for _, dir := range filepath.SplitList(env.Getenv("PATH")) {
		if dir == "" {
			continue
		}
		entries, err := env.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.Name() != name || e.IsDir() {
				continue
			}
			if info, err := e.Info(); err != nil || (info.Mode()&0o111 == 0 && e.Type()&os.ModeSymlink == 0) {
				continue
			}
			return filepath.Join(dir, name)
		}
	}
	return ""
}

// harnessVersion is the first version token of `<binary> --version` ("2.1.220",
// "0.153.4"), and whether the binary answered one. A binary that does not run,
// or that prints no token beginning with a digit, is unsupported; the first line
// it printed is returned as evidence either way.
func harnessVersion(ctx context.Context, env Env, bin string) (line string, ok bool) {
	cctx, cancel := context.WithTimeout(ctx, harnessTimeout)
	defer cancel()
	out, err := env.Exec(cctx, bin, "--version")
	first := harnessFirstLine(out)
	if err != nil && first == "" {
		return err.Error(), false
	}
	for _, w := range strings.Fields(first) {
		if w != "" && w[0] >= '0' && w[0] <= '9' {
			return w, true
		}
	}
	if first == "" {
		first = "printed nothing"
	}
	return first, false
}

// harnessFirstLine is the first non-empty line of s, trimmed; "" when there is none.
func harnessFirstLine(s string) string {
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			return l
		}
	}
	return ""
}
