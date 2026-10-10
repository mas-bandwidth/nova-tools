package sprint

import (
	"path"
	"slices"
	"strings"
)

// A PATHS hold the tick answers by rule (docs/SPEC-SPRINT.md, the rules table's row
// paths). paths_proposed.go still classifies the hold. This file decides which of
// those answers become a twin: every proposed path inside the repository, none a
// protected or secrets path, and the card not already carrying a different proposal.
// A second proposal, a path outside the repository, or a protected path stays the
// judgment the failed attempt raised.

// pathsHoldJudgment is why this proposal stays a judgment; "" when the tick twins it.
func pathsHoldJudgment(pr *Card, p PathsProposal) string {
	if why := badGlobs(p.Globs); why != "" {
		return why
	}
	for _, g := range p.Globs {
		if pathsHoldProtected(g) {
			return g + " is a protected or secrets path"
		}
	}
	if mark := pr.F(FieldPathsProposed); mark != "" && mark != p.mark() {
		return "a second PATHS proposal"
	}
	return ""
}

// pathsHoldProtected says g is a secrets or credential path: a secrets or
// credentials segment, a .git path, a dotenv file, or a key file.
func pathsHoldProtected(g string) bool {
	g = strings.TrimPrefix(strings.ToLower(g), "./")
	base := path.Base(g)
	ext := path.Ext(base)
	switch {
	case base == ".env" || strings.HasPrefix(base, ".env."):
		return true
	case ext == ".pem" || ext == ".key" || ext == ".p12" || ext == ".age":
		return true
	case base == "id_rsa" || strings.HasPrefix(base, "id_rsa."):
		return true
	case base == "credentials" || strings.HasPrefix(base, "credentials."):
		return true
	}
	for _, seg := range strings.Split(g, "/") {
		switch seg {
		case "secrets", ".secrets", "credentials", ".git":
			return true
		}
	}
	return false
}

// pathsHoldTask names the held head, and the branch when the work card has one,
// in the twin's THE TASK: `carry <head>; the only change is PATHS`. A brief
// that already has a THE TASK line keeps that line's task after the carry
// sentence. Returning it unchanged would drop the head the twin must carry.
func pathsHoldTask(brief, head, branch string) string {
	line := "carry " + head + "; the only change is PATHS"
	if branch != "" {
		line += ". The branch is " + branch
	}
	sentence := "THE TASK. " + line + "."
	if strings.Contains(brief, sentence) {
		return brief
	}
	lines := strings.Split(brief, "\n")
	at := slices.IndexFunc(lines, func(l string) bool {
		return strings.HasPrefix(strings.TrimSpace(l), "THE TASK")
	})
	if at < 0 || at >= len(lines) {
		return strings.TrimRight(brief, "\n") + "\n" + sentence + "\n"
	}
	rest := strings.TrimSpace(lines[at])
	rest = strings.TrimSpace(strings.TrimPrefix(rest, "THE TASK"))
	rest = strings.TrimSpace(strings.TrimPrefix(rest, "."))
	if rest != "" {
		lines[at] = sentence + " " + rest
	} else {
		lines[at] = sentence
	}
	return strings.Join(lines, "\n")
}

// pathsHoldBranch is the branch the held attempt pushed, on its work card.
func pathsHoldBranch(s *Snapshot, pr *Card) string {
	id := pr.F("work")
	if id == "" {
		id = WorkCardID(pr.ID, pr.Int("attempt"))
	}
	wc := s.Fleet.Card(id)
	if wc == nil {
		return ""
	}
	return wc.F("branch")
}
