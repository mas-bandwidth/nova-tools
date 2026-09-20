package pulse

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// staleBaseRefusal is harvest's pre-push check that the two-dot diff against the
// CURRENT target contains only the card's declared PATHS (issue #2032). A branch
// cut from an older base shows later landings as extra paths in `git diff
// <target>..<head>`; opening that as a PR reverts them. hygiene.Check rewrites
// the base to the merge-base and so cannot see this.
func staleBaseRefusal(dir, target, head string, globs []string, declared bool) error {
	bad, rng, err := staleBaseOffenders(dir, target, head, globs, declared)
	if err != nil {
		return fmt.Errorf("stale-base unread: %s", oneline.Err(err))
	}
	if len(bad) == 0 {
		return nil
	}
	return fmt.Errorf("stale-base files=%s range=%s: git diff against the current target contains paths the card did not declare; rebase onto the current target before harvest",
		field(joinOffenders(bad)), field(rng))
}

func staleBaseOffenders(dir, target, head string, globs []string, declared bool) (bad []string, rng string, err error) {
	if strings.TrimSpace(head) == "" {
		head = "HEAD"
	}
	two, used, err := currentTargetNames(dir, target, head)
	if err != nil {
		return nil, "", err
	}
	rng = used + ".." + head
	if declared {
		for _, p := range two {
			if !declaredCovers(globs, p) {
				bad = append(bad, p)
			}
		}
		return bad, rng, nil
	}
	three, err := gitNameOnly(dir, used+"..."+head)
	if err != nil {
		return nil, rng, err
	}
	own := make(map[string]bool, len(three))
	for _, p := range three {
		own[p] = true
	}
	for _, p := range two {
		if !own[p] {
			bad = append(bad, p)
		}
	}
	return bad, rng, nil
}

func currentTargetNames(dir, target, head string) (names []string, used string, err error) {
	seen := map[string]bool{}
	var cands []string
	add := func(s string) {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			return
		}
		seen[s] = true
		cands = append(cands, s)
	}
	add(target)
	add("origin/dev")
	add("dev")
	add("origin/main")
	add("main")
	var last error
	for _, t := range cands {
		n, e := gitNameOnly(dir, t+".."+head)
		if e != nil {
			last = e
			continue
		}
		return n, t, nil
	}
	if last == nil {
		last = fmt.Errorf("no current target ref in %s", field(dir))
	}
	return nil, "", last
}

func gitNameOnly(dir, spec string) ([]string, error) {
	out, err := gitIn(dir, "diff", "--name-only", "--no-renames", spec)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, l := range strings.Split(out, "\n") {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		names = append(names, strings.ReplaceAll(l, `\`, `/`))
	}
	return names, nil
}

func joinOffenders(files []string) string {
	const capN = 8
	if len(files) <= capN {
		return strings.Join(files, ",")
	}
	return strings.Join(files[:capN], ",") + fmt.Sprintf("...+%d", len(files)-capN)
}

func harvestDeclaredPaths(cardPath string, resultLines []string) (globs []string, declared bool) {
	if cardPath != "" {
		if raw, err := os.ReadFile(cardPath); err == nil {
			if g, ok := parsePATHS(string(raw)); ok {
				return g, true
			}
		}
	}
	return parsePATHS(strings.Join(resultLines, "\n"))
}

func launchedCardFor(launched, label string) string {
	if strings.TrimSpace(launched) == "" || strings.TrimSpace(label) == "" {
		return ""
	}
	for _, c := range readyCards(launched) {
		base := filepath.Base(c)
		m := readLaunchedMarker(launched, base)
		if m["label"] == label || strings.TrimSuffix(base, ".md") == label {
			return c
		}
	}
	return ""
}

func parsePATHS(text string) (globs []string, declared bool) {
	for _, line := range strings.Split(text, "\n") {
		t := strings.TrimSpace(line)
		var rest string
		switch {
		case strings.HasPrefix(t, "PATHS:"):
			rest = strings.TrimSpace(strings.TrimPrefix(t, "PATHS:"))
		case strings.HasPrefix(t, "PATHS "):
			rest = strings.TrimSpace(strings.TrimPrefix(t, "PATHS "))
		default:
			continue
		}
		if rest == "" || rest == "none" {
			return nil, true
		}
		for _, g := range strings.Split(rest, ",") {
			g = strings.TrimSpace(g)
			if g != "" && g != "none" {
				globs = append(globs, g)
			}
		}
		return globs, true
	}
	return nil, false
}

func declaredCovers(globs []string, p string) bool {
	p = strings.ReplaceAll(p, `\`, `/`)
	for _, g := range globs {
		if matchDeclared(g, p) {
			return true
		}
	}
	return false
}

func matchDeclared(glob, p string) bool {
	return matchDeclaredSegs(strings.Split(glob, "/"), strings.Split(p, "/"))
}

func matchDeclaredSegs(pat, segs []string) bool {
	for len(pat) > 0 {
		if pat[0] == "**" {
			if len(pat) == 1 {
				return true
			}
			for i := 0; i <= len(segs); i++ {
				if matchDeclaredSegs(pat[1:], segs[i:]) {
					return true
				}
			}
			return false
		}
		if len(segs) == 0 {
			return false
		}
		ok, err := path.Match(pat[0], segs[0])
		if err != nil || !ok {
			return false
		}
		pat, segs = pat[1:], segs[1:]
	}
	return len(segs) == 0
}
