package merge

import (
	"fmt"
	"strings"
)

// The integration commit (rules 18 and 21): the object the gate proves and the object
// publication moves the base to, built ONCE, here, in the lane's own clone.
//
// The storm this closes is #942's: the head was gated green against a base that #956 then
// moved, #942 merged a minute later on that head gate, and the tip went red on four
// tests. A gate for a head proves the head against the base it was merged with AT GATE
// TIME, and that evidence expires the moment another entry lands. So the thing gated is
// the merge itself, named by sha, and the thing published is that same object.

// IntegrationRef is where a built integration commit is kept in the lane's clone, so that
// a raced pass leaves the unpublished object as evidence rather than as garbage.
func IntegrationRef(entry, merge string) string {
	return "refs/nova-merge/integration/" + entry + "/" + merge
}

// Built is one integration commit.
type Built struct {
	Entry     string
	Head      string
	Base      string
	Merge     string
	Ref       string
	Conflicts []string // non-empty means the build was ABORTED and the entry is BLOCKED
}

// BuildIntegration merges head onto base in the lane's clone with git merge --no-ff and
// returns the object's sha.
//
// A clean two-parent merge writes no resolved file, which is what rule 7 forbids; a build
// that would need one is ABORTED, the clone is checked clean afterwards, and the entry is
// BLOCKED exactly as a host CONFLICTING is. Nothing here writes into the work tree: git
// does the merge and git aborts it.
func BuildIntegration(g *Git, entry, base, head, message string) (*Built, error) {
	if !IsSHA(base) || !IsSHA(head) {
		return nil, fmt.Errorf("an integration commit is built from two full shas, got base %q and head %q", base, head)
	}
	// THE GUARD REFUSES --force IN ANY ARGUMENT, which is wider than force-pushing and
	// deliberately so: the rule is enforced on the argument list rather than on the verb,
	// so `checkout --force` is refused too. The clone is the lane's own and the lane
	// never edits an entry's content, so a hard reset to HEAD and a plain detach do the
	// same work without asking the guard for an exception.
	if _, err := g.Run("reset", "--hard"); err != nil {
		return nil, err
	}
	if _, err := g.Run("checkout", "--detach", base); err != nil {
		return nil, err
	}
	if _, err := g.Run(Identity("merge", "--no-ff", "-m", message, head)...); err != nil {
		files, listErr := ConflictFiles(g)
		if listErr != nil {
			return nil, listErr
		}
		if len(files) == 0 {
			return nil, err
		}
		if abortErr := AbortMerge(g); abortErr != nil {
			return nil, abortErr
		}
		return &Built{Entry: entry, Head: head, Base: base, Conflicts: files}, nil
	}
	merge, err := g.Out("rev-parse", "HEAD")
	if err != nil {
		return nil, err
	}
	if !IsSHA(merge) {
		return nil, fmt.Errorf("the merge this build made has no sha: %q", merge)
	}
	ref := IntegrationRef(entry, merge)
	if _, err := g.Run("update-ref", ref, merge); err != nil {
		return nil, err
	}
	return &Built{Entry: entry, Head: head, Base: base, Merge: merge, Ref: ref}, nil
}

// HasObject reports whether this clone holds the commit the gate record names. It is the
// guard behind "the object gated is the object published": a record naming a merge this
// clone does not hold names an object nobody can publish, and the tool never builds a
// replacement on the way to the push.
func HasObject(g *Git, sha string) bool {
	out, err := g.Out("cat-file", "-t", sha)
	return err == nil && strings.TrimSpace(out) == "commit"
}

// Parents returns a commit's parents, in order. The first parent of an integration commit
// is the base and the second is the entry's head, which is what the gate runner checks
// before it runs a step.
func Parents(g *Git, sha string) ([]string, error) {
	out, err := g.Out("rev-list", "--parents", "-n", "1", sha)
	if err != nil {
		return nil, err
	}
	fields := strings.Fields(out)
	if len(fields) < 1 {
		return nil, fmt.Errorf("%s has no parents git will name", Short(sha))
	}
	return fields[1:], nil
}

// IntegrationMessage is the merge commit's subject: the entry it lands and the base it
// lands on, so that a person reading the base's history a week later knows what this is.
func IntegrationMessage(entry, base string, subject string) string {
	if subject == "" {
		return fmt.Sprintf("nova-merge: %s onto %s", entry, base)
	}
	return fmt.Sprintf("nova-merge: %s onto %s (%s)", entry, base, subject)
}

// ValidRefName is lesson 48: A VALUE THAT BECOMES A COMMAND-LINE ARGUMENT IS CHECKED WHERE
// IT IS TYPED. The lane's base and its lane branch are stored once by init and handed to
// git on every pass afterwards, so a value beginning with a dash is a FLAG to whatever
// reads it, and a value holding a space or a control character is a refname git refuses
// later, far from the person who typed it.
func ValidRefName(name string) error {
	switch {
	case name == "":
		return fmt.Errorf("a branch name is required and this one is empty")
	case strings.HasPrefix(name, "-"):
		return fmt.Errorf("a branch name beginning with %q is a flag to whatever reads it, not a branch: got %q", "-", name)
	case strings.HasPrefix(name, "/") || strings.HasSuffix(name, "/") || strings.Contains(name, "//"):
		return fmt.Errorf("a branch name has no empty path component, got %q", name)
	case strings.Contains(name, ".."):
		return fmt.Errorf("a branch name holds no %q, which is a range to git, got %q", "..", name)
	case strings.HasSuffix(name, ".lock"):
		return fmt.Errorf("a branch name does not end in %q, which is what git calls its own lock files, got %q", ".lock", name)
	}
	for _, r := range name {
		switch {
		case r <= ' ' || r == 0x7f:
			return fmt.Errorf("a branch name holds no space and no control character, got %q", name)
		case strings.ContainsRune("~^:?*[\\", r):
			return fmt.Errorf("a branch name holds none of %q, which git reads as a revision, got %q", "~^:?*[\\", name)
		}
	}
	return nil
}
