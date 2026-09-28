package merge

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
)

// Reviewer is one row of the reviewer TSV: who\tlogins\tmay-hold
type Reviewer struct {
	Who     string
	Logins  []string
	MayHold bool
}

// normWho folds a who value to the one canonical form every comparison, every stored
// row, and every printed who= field uses from here on: lower-cased and trimmed, once,
// at the point the name is read -- a typed DISPOSITION line, a derived name prefix, or a
// row of the reviewers table -- so "Johnny" and "johnny" are the same friend everywhere
// downstream, the way the bash lander's own ascii_downcase already made them (nova-tools
// #2615 follow-up: the Go gate matched a friend's name case-sensitively, so a HOLD typed
// `who=Johnny` was never released by that same friend's `who=johnny` APPROVE at head).
func normWho(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// ReviewerSet maps logins to reviewers and tracks may-hold permissions.
type ReviewerSet struct {
	Reviewers []Reviewer
	byWho     map[string]Reviewer
	byLogin   map[string][]Reviewer
}

// LoadReviewers reads a TSV file from disk.
func LoadReviewers(path string) (*ReviewerSet, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("reviewer file could not be opened: %w", err)
	}
	defer f.Close()
	return ParseReviewers(f)
}

// ParseReviewers reads the TSV from an io.Reader.
// Columns are: who\tlogins\tmay-hold
func ParseReviewers(r io.Reader) (*ReviewerSet, error) {
	s := bufio.NewScanner(r)
	set := &ReviewerSet{
		byWho:   make(map[string]Reviewer),
		byLogin: make(map[string][]Reviewer),
	}
	firstLine := true
	lineNo := 0
	for s.Scan() {
		lineNo++
		line := strings.TrimSpace(s.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) < 3 {
			return nil, fmt.Errorf("line %d: expected 3 tab-separated fields (who, logins, may-hold), got %d", lineNo, len(fields))
		}
		who := normWho(fields[0])
		loginsRaw := strings.TrimSpace(fields[1])
		mayHoldRaw := strings.ToLower(strings.TrimSpace(fields[2]))

		if firstLine && strings.EqualFold(who, "who") && strings.EqualFold(loginsRaw, "logins") {
			firstLine = false
			continue
		}
		firstLine = false

		if who == "" {
			return nil, fmt.Errorf("line %d: who is empty", lineNo)
		}

		var mayHold bool
		switch mayHoldRaw {
		case "yes", "true", "1", "y":
			mayHold = true
		case "no", "false", "0", "n":
			mayHold = false
		default:
			return nil, fmt.Errorf("line %d: invalid may-hold %q (expected yes/no)", lineNo, mayHoldRaw)
		}

		var logins []string
		for _, part := range strings.Split(loginsRaw, ",") {
			p := strings.TrimSpace(part)
			if p != "" {
				logins = append(logins, p)
			}
		}

		rev := Reviewer{
			Who:     who,
			Logins:  logins,
			MayHold: mayHold,
		}
		set.Reviewers = append(set.Reviewers, rev)
		set.byWho[who] = rev
		for _, l := range logins {
			ll := strings.ToLower(l)
			set.byLogin[ll] = append(set.byLogin[ll], rev)
		}
	}
	if err := s.Err(); err != nil {
		return nil, err
	}
	if len(set.Reviewers) == 0 {
		return nil, fmt.Errorf("reviewer file contains no reviewer entries")
	}
	return set, nil
}

// HasWho reports whether who is known in the reviewer file.
func (rs *ReviewerSet) HasWho(who string) bool {
	if rs == nil {
		return false
	}
	_, ok := rs.byWho[normWho(who)]
	return ok
}

// IsExplicitlyDisallowed reports whether who is present in the reviewer file with may-hold=no.
func (rs *ReviewerSet) IsExplicitlyDisallowed(who string) bool {
	if rs == nil {
		return false
	}
	r, ok := rs.byWho[normWho(who)]
	return ok && !r.MayHold
}

// IsLogin reports whether login appears in the logins column of any reviewer.
func (rs *ReviewerSet) IsLogin(login string) bool {
	if rs == nil {
		return false
	}
	_, ok := rs.byLogin[normWho(login)]
	return ok
}

// ReviewersForLogin returns all reviewers mapped to login.
func (rs *ReviewerSet) ReviewersForLogin(login string) []Reviewer {
	if rs == nil {
		return nil
	}
	return rs.byLogin[normWho(login)]
}

// IsScanned returns true if the login is mapped in the reviewer file.
func (rs *ReviewerSet) IsScanned(login string) bool {
	if rs == nil {
		return false
	}
	_, ok := rs.byLogin[normWho(login)]
	return ok
}

// MayHold returns whether the named reviewer has may-hold permission.
func (rs *ReviewerSet) MayHold(who string) bool {
	if rs == nil {
		return false
	}
	r, ok := rs.byWho[normWho(who)]
	return ok && r.MayHold
}

// LoginMayHold returns whether ANY reviewer mapped to this login has may-hold permission.
func (rs *ReviewerSet) LoginMayHold(login string) bool {
	if rs == nil {
		return false
	}
	revs := rs.byLogin[normWho(login)]
	for _, r := range revs {
		if r.MayHold {
			return true
		}
	}
	return false
}

// ResolveWho resolves a login and optional typed who into a verified name or "unknown".
// S6: "Its who is the name on its typed DISPOSITION who=<name> line (3) when the file
// maps that name to the comment's login, and unknown otherwise: a name the file does not
// map to that login is not evidence of that reader, it is ambiguity, and ambiguity
// attributes a hold to nobody and an approve to nothing."
//
// Both login and typedWho are folded through normWho before anything else, and every row's
// Who was folded the same way when the file was loaded -- so what follows is a plain
// equality, not a case-insensitive one, on purpose: the fold already happened, once, and a
// second case-insensitive comparison here would just be a second place to get it wrong.
func (rs *ReviewerSet) ResolveWho(login, typedWho string) (string, bool) {
	if rs == nil {
		return "unknown", false
	}
	login = normWho(login)
	typedWho = normWho(typedWho)
	if typedWho != "" {
		revs := rs.byLogin[login]
		for _, r := range revs {
			if r.Who == typedWho {
				return r.Who, true
			}
		}
		return "unknown", false
	}
	return "unknown", false
}
