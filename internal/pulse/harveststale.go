package pulse

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	// Aliased: `hygiene` is already a type in this package (hygiene.go:94).
	pathglob "github.com/mas-bandwidth/nova-tools/internal/hygiene"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// StaleBaseRefusal is the typed stale-base decision (#2648). Error's sentence is
// what a person reads in the log. The remedy (unstick) reads the fields. A grep
// of the sentence is not the decision: the sentence gained `declared=` in #2598
// and stopped matching the old unstick sed, and the branch the sentence prints
// is not a second source of truth next to Branch.
type StaleBaseRefusal struct {
	Label    string
	Branch   string
	Files    []string
	Declared string
	Range    string
	Missing  bool
	Detail   string
}

func (e *StaleBaseRefusal) Error() string {
	if e == nil {
		return "stale-base"
	}
	if e.Missing {
		return e.Detail
	}
	return fmt.Sprintf("stale-base files=%s declared=%s range=%s: %d path(s) in `git diff --name-only %s` match no declared PATHS glob (they are the files= list); rebase onto the current target before harvest",
		field(joinOffenders(e.Files)), field(e.Declared), field(e.Range), len(e.Files), e.Range)
}

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
//
// A differ or a MISSING verdict is a *StaleBaseRefusal. Its sentence is the log
// line. Branch, Range, Files and Declared are the decision.
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
	// MISSING IS ITS OWN VERDICT (#2547). A target nobody could read proves
	// nothing about the branch, and a refusal shaped like a DIFFER over an empty
	// ref reads, to every script downstream, as "the diff was walked and it was
	// bad". It says MISSING, and it carries no `files=` list, because there is no
	// offender list to carry.
	oid, err := pinAuthorizedTarget(dir, destURL, target)
	if err != nil {
		return &StaleBaseRefusal{
			Missing: true,
			Branch:  branchFromHead(head),
			Detail: fmt.Sprintf("stale-base MISSING target=%s: the authorized destination's target could not be fetched or pinned, so no diff was walked: %s",
				field(target), oneline.Err(err)),
		}
	}
	return staleBaseVerdict(dir, oid, head, globs, declared)
}

// staleBaseVerdict is the walk and the verdict against an ALREADY pinned target oid: the
// second half of staleBaseRefusal, split out so a batch harvest pins each target once per
// pass and judges every job against that one pin (harvest --batch, #2756) instead of
// fetching the same target from GitHub once per job.
func staleBaseVerdict(dir, oid, head string, globs []string, declared bool) error {
	if strings.TrimSpace(head) == "" {
		head = "HEAD"
	}
	bad, rng, err := staleBaseOffenders(dir, oid, head, globs, declared)
	if err != nil {
		return &StaleBaseRefusal{
			Missing: true,
			Branch:  branchFromHead(head),
			Range:   rng,
			Detail: fmt.Sprintf("stale-base MISSING range=%s: the diff could not be read, so no verdict was reached: %s",
				field(rng), oneline.Err(err)),
		}
	}
	if len(bad) == 0 {
		return nil
	}
	// THE REFUSAL PRINTS BOTH SIDES OF THE COMPARISON (#2547). `files=` alone sent
	// a reader to the branch to guess: on 2026-09-22 the offender list WAS the
	// card's own PATHS line, and nothing in the message said what the walk had
	// understood the declaration to be. `declared=` is the globs as parsed, so a
	// PATHS line the parser read as one unsplit glob is visible in the refusal
	// itself rather than after an hour of detective work.
	// The sentence is that print. Branch is the typed field unstick reads (#2648),
	// taken from the head ref, not parsed back out of the sentence.
	return &StaleBaseRefusal{
		Branch:   branchFromHead(head),
		Files:    bad,
		Declared: declaredSummary(globs, declared),
		Range:    rng,
	}
}

func branchFromHead(head string) string {
	head = strings.TrimSpace(head)
	const p = "refs/harvest/"
	if strings.HasPrefix(head, p) {
		return strings.TrimPrefix(head, p)
	}
	return head
}

// declaredSummary is what the walk judged against, for the refusal line. An
// absent PATHS line and a `PATHS: none` are different facts and are named
// differently: the first means the card declared nothing, the second means the
// card declared that it changes nothing.
func declaredSummary(globs []string, declared bool) string {
	if !declared {
		return "no-PATHS-line"
	}
	if len(globs) == 0 {
		return "none"
	}
	return joinOffenders(globs)
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
		for _, g := range splitDeclared(rest) {
			if g != "none" {
				globs = append(globs, g)
			}
		}
		return globs, true
	}
	return nil, false
}

// splitDeclared cuts a PATHS: value into entries on COMMAS AND WHITESPACE BOTH
// (#2547).
//
// `docs/WORKER-CARDS.md` and `internal/swarm/lintheader.go` spell the line
// `PATHS: <glob>[, <glob>...]`, and splitting on commas alone is what that
// grammar says. The cutter in the field writes the entries separated by spaces —
// `PATHS: docs/EVAL-MERGE-QUEUE.md internal/docs/eval_merge_queue_test.go` — and
// on 2026-09-22 that turned the whole line into ONE glob, which contains a space
// and therefore matches no path in any repository. Every declared file then came
// back as an offender, and fourteen finished cards were refused every pass with
// a `files=` list identical to their own PATHS line.
//
// Reading both separators is not a widening of the guard. A repository path with
// a space in it cannot be expressed by either grammar, so the only diffs the old
// split could ever have cleared are diffs this one clears too; what it can no
// longer do is silently judge a whole line as one unmatchable glob. The rule
// that a card's bound is validated (`hygiene.ValidatePaths`, at `cut`) is
// unchanged and lives where it always did.
func splitDeclared(rest string) []string {
	return strings.FieldsFunc(rest, func(r rune) bool {
		return r == ',' || unicode.IsSpace(r)
	})
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

// matchDeclared answers whether ONE declared PATHS entry covers one
// repo-relative path.
//
// THE GLOB HALF IS `hygiene.MatchGlob` (imported as `pathglob`, because this
// package already has a type of that name) AND NOT A SECOND COPY OF IT. This file
// carried its own `**`-aware matcher, character for character the one in
// `internal/hygiene/glob.go`, which is the arrangement that comment warns
// against in so many words: "a second matcher would be a second definition".
// `internal/pulse` sits above `internal/hygiene` (internal/hygiene/kinds.txt),
// so the call is the right way round. The S7 wall, `cut`'s validation and this
// refusal now answer from one matcher.
//
// THE DIRECTORY HALF IS NEW (#2547). A card that writes `PATHS: internal/secrets`
// means the directory, and on 2026-09-22 `internal/secrets/recovery_key_test.go`
// was refused as undeclared against exactly that line. The spec's own spelling
// for a directory is `internal/secrets/**` (`hygiene.MatchGlob`: "`a/**` matches
// `a/b` and `a/b/c`; it also matches `a` itself, which is what a card naming a
// directory means by it"), and nothing in `docs/SPEC-TOOLWORK.md` §4 rule 4 or
// `docs/WORKER-CARDS.md` gives a meaning to a bare directory name or to a
// trailing slash. So both are read here as the directory they name.
//
// The prefix rule is confined to a WILDCARD-FREE entry, and it cannot widen a
// bound: no repository holds both a file at `a/b` and a file under `a/b/`, so an
// entry that is a strict path prefix of a changed path could only ever have been
// meant as a directory. An entry carrying a wildcard is left entirely to
// hygiene.MatchGlob, whose `**` already spans segments.
func matchDeclared(glob, p string) bool {
	glob = strings.ReplaceAll(strings.TrimSpace(glob), `\`, `/`)
	glob = strings.TrimSuffix(glob, "/")
	if glob == "" {
		return false
	}
	if pathglob.MatchGlob(glob, p) {
		return true
	}
	if strings.ContainsAny(glob, "*?[") {
		return false
	}
	return strings.HasPrefix(p, glob+"/")
}
