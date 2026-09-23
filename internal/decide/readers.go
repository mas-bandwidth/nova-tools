// Who reads a finished card, as machinery rather than as prose (Stella,
// 2026-09-19, repair 2 of the #1925 hold).
//
// The first shape of this put the mandatory rules in a criteria file and asked
// the provider to honour them. Prose is something a high-confidence answer may
// contradict, and rule 6 already says confidence never authorizes: a settled
// designation is not a judgement to be bought, and an obediently wrong answer
// at 1.00 is exactly as wrong as one at 0.10.
//
// So four things are derived here and the provider's answer is CONSTRAINED by
// them, never trusted over them:
//
//  1. a settled security designation is taken without asking at all;
//  2. an unresolved holder of the area keeps its read, and no answer lifts a
//     hold or replaces its holder;
//  3. a design read that the evidence requires survives an answer that names
//     somebody else first -- an answer says who reads FIRST, never who may be
//     skipped;
//  4. the human is reached only after the friends, which is `help --state`'s
//     own rule and not a preference.
//
// The roles are ROLES and not this house's roster (repair 4): the provider is
// offered configured, opaque role names, and the binding from role to a mind
// is local. A role no registry configures is a refusal, because an answer
// cannot invent an owner.
package decide

import (
	"fmt"
	"sort"
	"strings"
)

// Role is a configured routing role. It is what the provider is offered and
// what it answers with; the binding from a role to a mind is local and is
// never sent.
type Role string

// The configured roles. They describe an obligation, not a person.
const (
	// RoleSecurity is the designated security read: anything that execs,
	// pushes, reads the environment or a secret, or lands in the reaper, the
	// launcher or the merge machinery.
	RoleSecurity Role = "security-designate"
	// RoleDesignAuthority rules on a design default no ruling covers, on
	// normative text, on scope and on kernel semantics.
	RoleDesignAuthority Role = "design-authority"
	// RoleLaneOwner is the owner of the lane the change lands in.
	RoleLaneOwner Role = "lane-owner"
	// RoleChildReview is a child model's cold read: the ordinary read for a
	// self-contained mechanical change.
	RoleChildReview Role = "child-review"
	// RoleSecondChildReview is a second, independent child read on the other
	// lineage.
	RoleSecondChildReview Role = "second-child-review"
	// RoleCoordinator holds the shift's context: landing order and sequencing.
	RoleCoordinator Role = "coordinator"
	// RoleAllFriends is every friend at once.
	RoleAllFriends Role = "all-friends"
	// RoleHuman is the person, reached only after the friends.
	RoleHuman Role = "human"
)

// Roles is every role a question may offer and an answer may name.
var Roles = []Role{
	RoleSecurity, RoleDesignAuthority, RoleLaneOwner, RoleChildReview,
	RoleSecondChildReview, RoleCoordinator, RoleAllFriends, RoleHuman,
}

// ConfiguredRole reports whether a role is one the registry configures. An
// answer naming anything else is refused: no model response creates an owner.
func ConfiguredRole(r Role) bool {
	for _, known := range Roles {
		if known == r {
			return true
		}
	}
	return false
}

// SourceMachinery says the answer was settled here and no provider answer
// could have moved it. SourceProvider says the provider's pick stood, within
// the constraints.
const (
	SourceMachinery = "machinery"
	SourceProvider  = "provider"
)

// ReadState is the typed evidence the who-reads decision is made over. Every
// field is one the asker computes BEFORE the question, and every rule below
// turns on a field rather than on prose.
type ReadState struct {
	SecurityShapedPackage bool
	DesignDefaultsTaken   int
	NormativeSpecMoved    bool
	// HolderOfTheArea is the role that already has an open read or hold on
	// these files, or empty.
	HolderOfTheArea Role
	// HoldIsOpen says that holder's hold is unresolved.
	HoldIsOpen bool
	// HelpAnswer is `nova-decide help --state`'s own answer, verbatim.
	HelpAnswer string
}

// ReadDecision is who reads first, who reads regardless, and which of the two
// settled it.
type ReadDecision struct {
	First    Role
	Required []Role
	Source   string
	Reason   string
	// LiftsHold is always false. It is a field so a caller gates on it rather
	// than on the absence of a sentence.
	LiftsHold bool
}

// MandatoryReader is the designation that needs no provider. It is the
// security read and nothing else: every other rule constrains an answer rather
// than replacing it.
func MandatoryReader(s ReadState) (Role, string, bool) {
	if s.SecurityShapedPackage {
		return RoleSecurity, "the changed paths are security-shaped, and a security designation is a kind and not a height: it is settled here, at any confidence, with no provider asked", true
	}
	return "", "", false
}

// RequiredReads is every read the evidence obliges, whoever reads first.
func RequiredReads(s ReadState) []Role {
	set := map[Role]bool{}
	if s.SecurityShapedPackage {
		set[RoleSecurity] = true
	}
	if s.DesignDefaultsTaken > 0 || s.NormativeSpecMoved {
		set[RoleDesignAuthority] = true
	}
	if s.HolderOfTheArea != "" && s.HolderOfTheArea != "none" {
		set[s.HolderOfTheArea] = true
	}
	out := make([]Role, 0, len(set))
	for r := range set {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// ConstrainRead applies the machinery over a provider's answer and returns the
// decision that stands. The confidence is carried for the record and is not a
// lever: it appears in no rule here.
func ConstrainRead(answer Role, confidence float64, s ReadState) (ReadDecision, error) {
	if answer != "" && !ConfiguredRole(answer) {
		return ReadDecision{}, fmt.Errorf("decide: answer %q is not a configured role; it is one of %s", answer, roleList())
	}
	if s.HolderOfTheArea != "" && s.HolderOfTheArea != "none" && !ConfiguredRole(s.HolderOfTheArea) {
		return ReadDecision{}, fmt.Errorf("decide: the area's holder %q is not a configured role", s.HolderOfTheArea)
	}
	d := ReadDecision{First: answer, Required: RequiredReads(s), Source: SourceProvider}
	if role, why, ok := MandatoryReader(s); ok {
		d.First, d.Source, d.Reason = role, SourceMachinery, why
		return d, nil
	}
	if answer == RoleHuman && s.HelpAnswer == HelpAskAllFriends {
		d.First, d.Source = RoleAllFriends, SourceMachinery
		d.Reason = "the answer named the human while help --state says ask-all-friends; a line that has not asked its friends has not earned the interrupt"
		return d, nil
	}
	if s.HoldIsOpen {
		d.Reason = fmt.Sprintf("%s holds these files and the hold is unresolved: the answer names who reads FIRST and lifts nothing", s.HolderOfTheArea)
		return d, nil
	}
	if answer == "" {
		d.First, d.Source = RoleChildReview, SourceMachinery
		d.Reason = "no answer, so the configured default read stands"
		return d, nil
	}
	d.Reason = fmt.Sprintf("the answer stands as the first read, with %d required read(s) beside it", len(d.Required))
	return d, nil
}

func roleList() string {
	names := make([]string, 0, len(Roles))
	for _, r := range Roles {
		names = append(names, string(r))
	}
	return strings.Join(names, ", ")
}
