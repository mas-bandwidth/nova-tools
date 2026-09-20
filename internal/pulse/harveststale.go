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
//
// The target is the coordinator-authorized destination, fetched and pinned to an
// OID before the diff (HOLD on #2117). The worker clone's origin/dev is not the
// current target: it is a cached remote-tracking ref the worker can leave stale.
// An empty destURL or target is a refusal, never a walk of local fallbacks.
func staleBaseRefusal(dir, destURL, target, head string, globs []string, declared bool) error {
	if strings.TrimSpace(destURL) == "" {
		return fmt.Errorf("stale-base: no authorized destination to fetch")
	}
	target = harvestTargetName(target)
	if target == "" {
		return fmt.Errorf("stale-base: no explicit target branch")
	}
	if strings.TrimSpace(head) == "" {
		head = "HEAD"
	}
	oid, err := pinAuthorizedTarget(dir, destURL, target)
	if err != nil {
		return fmt.Errorf("stale-base unread: %s", oneline.Err(err))
	}
	bad, rng, err := staleBaseOffenders(dir, oid, head, globs, declared)
	if err != nil {
		return fmt.Errorf("stale-base unread: %s", oneline.Err(err))
	}
	if len(bad) == 0 {
		return nil
	}
	return fmt.Errorf("stale-base files=%s range=%s: git diff against the current target contains paths the card did not declare; rebase onto the current target before harvest",
		field(joinOffenders(bad)), field(rng))
}

func pinAuthorizedTarget(dir, destURL, target string) (string, error) {
	destURL = strings.TrimSpace(destURL)
	target = harvestTargetName(target)
	if destURL == "" || target == "" {
		return "", fmt.Errorf("no authorized target to fetch")
	}
	pin := "refs/harvest/target/" + target
	refspec := "+refs/heads/" + target + ":" + pin
	if out, err := gitIn(dir, "fetch", "--no-tags", destURL, refspec); err != nil {
		return "", fmt.Errorf("fetch %s %s: %s", field(destURL), field(target), oneline.Cap(out, 200))
	}
	oid, err := gitIn(dir, "rev-parse", "--verify", pin+"^{commit}")
	if err != nil || !isHex(oid) {
		return "", fmt.Errorf("pinned target %s is empty", field(pin))
	}
	return oid, nil
}

func harvestTargetBranch(in HarvestInput, resultLines []string) (string, error) {
	if raw := strings.TrimSpace(resultField(resultLines, "BASE")); raw != "" {
		if b := harvestTargetName(raw); b != "" {
			return b, nil
		}
		return "", fmt.Errorf("stale-base: BASE %s is not a fetchable target branch", field(raw))
	}
	if raw := strings.TrimSpace(in.Base); raw != "" {
		if b := harvestTargetName(raw); b != "" {
			return b, nil
		}
		return "", fmt.Errorf("stale-base: --base %s is not a fetchable target branch", field(raw))
	}
	return DefaultBase, nil
}

func harvestTargetName(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "refs/heads/")
	s = strings.TrimPrefix(s, "origin/")
	if s == "" || s == "HEAD" || isHex(s) {
		return ""
	}
	if strings.ContainsAny(s, " \t\n\\:") || strings.Contains(s, "..") {
		return ""
	}
	return s
}

func staleBaseOffenders(dir, oid, head string, globs []string, declared bool) (bad []string, rng string, err error) {
	if strings.TrimSpace(oid) == "" || strings.TrimSpace(head) == "" {
		return nil, "", fmt.Errorf("missing pinned target or head")
	}
	rng = oid + ".." + head
	two, err := gitNameOnly(dir, rng)
	if err != nil {
		return nil, "", err
	}
	if declared {
		for _, p := range two {
			if !declaredCovers(globs, p) {
				bad = append(bad, p)
			}
		}
		return bad, rng, nil
	}
	three, err := gitNameOnly(dir, oid+"..."+head)
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
