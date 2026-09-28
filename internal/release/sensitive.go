package release

import (
	"regexp"
	"sort"
	"strings"
)

// SensitivePaths is THE LIST, and this file is the one place in code it exists.
//
// Johnny's decision 1 on SPEC-RELEASE (#1337): a release is the moment work
// stops being a diff somebody can revert and starts being binaries on every
// bench in the fleet, so the ranges that touch the parts of this estate a
// mistake cannot be taken back from -- the secret store, the sandbox that holds
// a worker, the image every bench boots, the scripts the coordinator runs
// unattended -- are not cut on the judgement of whoever is at the keyboard.
// They are cut after he has read them, and the read is NAMED on the command
// line so the receipt says who vouched for it.
//
// Each entry is a DIRECTORY PREFIX, trailing slash included, and the slash is
// load-bearing: `internal/secrets` without it also catches
// `internal/secretsanta/`, and a list that classifies by accident is a list
// nobody can reason about. Matching is by prefix and by nothing else -- no
// guessing from a file name, no substring anywhere in the path.
//
// docs/SPEC-RELEASE.md carries the same list in the same order, and
// internal/ci's TestTheSensitivePathListIsTheSameInTheCodeAndInTheSpec fails
// when the two disagree. Two copies of a security list drift, and the copy that
// drifts is always the one nobody is running; this one is the one that runs, so
// a path is added HERE and the spec is updated in the same commit.
var SensitivePaths = []string{
	"cmd/nova-sandbox/",
	"cmd/nova-secrets/",
	"infra/image/",
	"internal/sandbox/",
	"internal/secrets/",
	"scripts/coordination/",
}

// SensitiveShape is the list in one phrase, for the help and for a refusal that
// wants to say what the gate covers. It is COMPOSED from SensitivePaths rather
// than written out beside it, which is the whole reason the help cannot fall
// behind the gate.
var SensitiveShape = strings.Join(SensitivePaths, ", ")

// CompareFileCap is how many files the forge will name for one compare. It is
// GitHub's own ceiling on the `files` array of a compare response, and it
// matters here for one reason: A FILE LIST AT THE CEILING IS A LIST THAT MAY BE
// SHORT. The classification below reads that list, so a range at this number
// cannot be classified at all, and a gate that reads a truncated list is a gate
// that passes the one file it did not see. `cut` refuses such a range with the
// same remedy as a sensitive one -- Johnny's read -- rather than quietly
// deciding on a prefix of the truth.
const CompareFileCap = 300

// Sensitive returns the paths of files that sit under one of SensitivePaths,
// deduplicated and sorted, so that a refusal naming them reads the same way
// twice and a receipt counting them counts files rather than mentions.
func Sensitive(files []string) []string {
	seen := map[string]bool{}
	var hits []string
	for _, file := range files {
		file = strings.TrimPrefix(strings.TrimSpace(file), "./")
		if file == "" || seen[file] {
			continue
		}
		for _, prefix := range SensitivePaths {
			if strings.HasPrefix(file, prefix) {
				seen[file] = true
				hits = append(hits, file)
				break
			}
		}
	}
	sort.Strings(hits)
	return hits
}

// securityReadShape is what may be handed to --security-read. A note id or the
// url of the comment carrying the read, and nothing that could not be printed:
// the value travels into `RELEASE CUT SENSITIVE ... read=<id>`, which is one
// line with one token per field.
var securityReadShape = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:/#?&~+-]*$`)

// ValidSecurityRead holds --security-read to the field law before anything is
// tagged. A read nobody could print is a read nobody could look up, and the
// whole value of naming it is that a person reading the receipt in six months
// can go and find what he actually said.
func ValidSecurityRead(id string) error {
	remedy := "pass --security-read <note id> or the url of the pull request comment carrying the read"
	if strings.TrimSpace(id) == "" {
		return refuse(remedy, "the security read is empty")
	}
	if strings.ContainsAny(id, " \t\n\r") {
		return refuse(remedy, "the security read %q carries whitespace; the receipt is one line with one token per field", id)
	}
	if strings.Contains(id, "=") {
		return refuse(remedy, "the security read %q contains =, which internal/oneline reads as a field separator", id)
	}
	if !securityReadShape.MatchString(id) {
		return refuse(remedy, "the security read %q is neither a note id nor a url", id)
	}
	return nil
}

// namedPaths folds a list of paths into one bounded phrase for a refusal. The
// whole point of naming them is that somebody can act on it, and a refusal that
// pastes four hundred paths into a terminal is one nobody reads either.
func namedPaths(paths []string, max int) string {
	if len(paths) <= max {
		return strings.Join(paths, ", ")
	}
	return strings.Join(paths[:max], ", ") + ", and " + plural(len(paths)-max, "more path")
}
