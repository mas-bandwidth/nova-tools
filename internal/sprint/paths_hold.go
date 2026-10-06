package sprint

import (
	"path"
	"strings"
)

// A PATHS hold widens the twin by rule, once (docs/SPEC-SPRINT.md section 8, the rules
// table's row paths; docs/SPEC-CARD-CONTRACT.md section 4; tla/Lifecycle.tla,
// PathsProposed and TwinnedOnceByRule). On 2026-10-06 fourteen cards came back from a
// reader or a lander with one defect, a source file outside PATHS that the work needed, and
// each cost a read, a judgment, a coordinator twin by hand and a second lane and read for a
// carry that changed nothing (the owner: "twinning = inefficiency"). The worker already
// knows the file: it ends the attempt with Verdict: HOLD and a PATHS-PROPOSED: line, its
// head pushed, and the paths rule (paths_proposed.go) answers that HOLD with no judgment to
// the coordinator, when
//
//   - every proposed path is inside the card's repository (badGlobs),
//   - none is a protected or secrets path (ProtectedProposed), and
//   - the card was not itself cut by this rule (PathsTwinnedFrom): a card is twinned at most
//     once by the rule, so the twin's own second proposal is the judgment it is today.
//
// The twin replaces the card (--replaces, dependents inherited), keeps the tier the card is
// on, carries the held head and branch (its fix: PathsHoldTask, "carry <head>; the only
// change is PATHS"), and the rule is recorded on both cards (FieldPathsTwin on the card,
// FieldPathsTwinned on the twin).

// FieldPathsTwin is the twin the paths rule replaced a card by, on the card it replaced;
// FieldPathsTwinned is the card a twin the paths rule cut replaces, on the twin. The pair is
// the rule's record on both cards: a card holding FieldPathsTwinned is never twinned by the
// rule again.
const (
	FieldPathsTwin    = "paths_twin"
	FieldPathsTwinned = "paths_twinned"
)

// ProtectedDirs are the directories of a repository a twin by rule never widens PATHS to:
// its CI and review controls, its git metadata, and the packages that hold or hand out
// credentials. A proposal naming one (or a glob that can name one) is a mind's.
var ProtectedDirs = []string{".github", ".git", "internal/secrets", "internal/seatcred"}

// ProtectedFiles are the files a twin by rule never widens PATHS to, wherever they stand:
// the module's dependency pins and the review owners.
var ProtectedFiles = []string{"go.mod", "go.sum", "CODEOWNERS"}

// SecretBases are the base names of a secrets path, as path.Match patterns; a base name
// holding "secret" or "credential" is one too, in any case.
var SecretBases = []string{"*.pem", "*.key", "*.p12", "*.pfx", "id_rsa*", "id_ed25519*", ".env", ".env.*", ".netrc", ".npmrc"}

// ProtectedProposed is each proposed glob that is a protected or secrets path, as
// "<glob> (<why>)", in proposal order; none when the twin may be cut by rule.
func ProtectedProposed(globs []string) []string {
	var out []string
	for _, g := range globs {
		if why := protectedWhy(g); why != "" {
			out = append(out, g+" ("+why+")")
		}
	}
	return out
}

func protectedWhy(g string) string {
	if first, _, _ := strings.Cut(g, "/"); strings.ContainsAny(first, "*?[") {
		return "a glob at the repository's top, which names its protected paths with the rest"
	}
	for _, d := range ProtectedDirs {
		if overlaps(g, d) || dirsMatch(g, d) {
			return "under " + d + "/, a protected directory"
		}
	}
	base := path.Base(g)
	for _, f := range ProtectedFiles {
		if m, _ := path.Match(base, f); m {
			return f + ", a protected file"
		}
	}
	low := strings.ToLower(base)
	if strings.Contains(low, "secret") || strings.Contains(low, "credential") {
		return "a secrets path"
	}
	for _, p := range SecretBases {
		if m, _ := path.Match(p, base); m {
			return "a secrets path"
		}
	}
	return ""
}

// dirsMatch is whether glob g's leading directories match directory d segment by segment, so
// a wildcard directory (internal/*/x.go) can name a file under d.
func dirsMatch(g, d string) bool {
	gs, ds := strings.Split(g, "/"), strings.Split(d, "/")
	if len(gs) <= len(ds) {
		return false
	}
	for i, seg := range ds {
		if m, _ := path.Match(gs[i], seg); !m {
			return false
		}
	}
	return true
}

// PathsTwinnedFrom is the card the paths rule replaced by c, "" when c was not cut by it.
func PathsTwinnedFrom(c *Card) string { return c.F(FieldPathsTwinned) }

// pathsHoldLeft is why the paths rule leaves a proposal to a mind that it would otherwise
// twin: the card is a twin the rule cut already, or a proposed path is protected; "" when
// neither.
func pathsHoldLeft(pr *Card, globs []string) string {
	if from := PathsTwinnedFrom(pr); from != "" {
		return "paths proposed twice: " + pr.ID + " is the twin the rule cut for " + from + ", and a card is twinned at most once by the rule; a mind's, and the same brief is not dealt again"
	}
	if bad := ProtectedProposed(globs); len(bad) > 0 {
		return "paths proposed, protected: " + strings.Join(bad, "; ") + "; a mind's, and the same brief is not dealt again"
	}
	return ""
}

// PathsHoldTask is the twin's fix: the head and branch it carries on from, and that the
// only change from the card it replaces is its PATHS.
func PathsHoldTask(p PathsProposal) string {
	at := "carry " + p.Head
	if p.Head == "" {
		at = "no head was pushed, so start from BASE"
	}
	if p.Branch != "" {
		at += " (branch " + p.Branch + ")"
	}
	return at + "; the only change is PATHS, widened by " + strings.Join(p.New, ",") + " (" + p.Card + " attempt " + itoa(p.Attempt) + ")"
}

// pathsTwinSet is what the paths rule records on the twin it cuts beyond its proposal: the
// card it replaces, its fix (PathsHoldTask), and the tier the card was on.
func pathsTwinSet(pr *Card, p PathsProposal) map[string]string {
	set := map[string]string{FieldPathsTwinned: pr.ID, "fix": PathsHoldTask(p)}
	if t := pr.F(FieldTierNow); t != "" {
		set[FieldTierNow] = t // the tier is kept: a twin by rule starts where the card stood
	}
	return set
}

// pathsOldSet is what the paths rule records on the card its twin replaces.
func pathsOldSet(p PathsProposal, answer string) map[string]string {
	return map[string]string{FieldPathsTwin: p.Twin, FieldPathsProposed: p.mark(), FieldRuleAnswer: answer}
}
