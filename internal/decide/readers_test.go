package decide

import (
	"strings"
	"testing"
)

// Repair 2 (Stella, stella-c30b89a2d9f4, 2026-09-19): the mandatory rules were
// PROSE in a criteria file, and prose is something a high-confidence answer may
// contradict. Security designation, design ownership, an unresolved holder's
// obligation and the help-before-human restriction are machinery, derived
// without a provider, and a provider answer is constrained by them rather than
// trusted over them.

// A settled security designation needs no provider at all.
func TestASecurityShapedUnitIsDesignatedWithoutAsking(t *testing.T) {
	s := ReadState{SecurityShapedPackage: true}
	role, why, ok := MandatoryReader(s)
	if !ok {
		t.Fatal("a security-shaped unit has a settled designation; nothing should be asked")
	}
	if role != RoleSecurity {
		t.Errorf("designated %s, want %s", role, RoleSecurity)
	}
	if !strings.Contains(why, "security") {
		t.Errorf("the reason must say why: %q", why)
	}
	if _, _, ok := MandatoryReader(ReadState{}); ok {
		t.Error("an ordinary unit has no settled designation and IS asked")
	}
}

// An obediently wrong answer -- the provider naming the cheap reader on a
// security-shaped unit -- is overruled by the machinery at BOTH ends of the
// confidence range. Confidence never authorizes (rule 6).
func TestAnObedientlyWrongAnswerIsOverruledAtEveryConfidence(t *testing.T) {
	s := ReadState{SecurityShapedPackage: true}
	for _, conf := range []float64{1.0, 0.1} {
		got, err := ConstrainRead(RoleChildReview, conf, s)
		if err != nil {
			t.Fatal(err)
		}
		if got.First != RoleSecurity {
			t.Errorf("conf %.2f: first reader %s, want %s", conf, got.First, RoleSecurity)
		}
		if got.Source != SourceMachinery {
			t.Errorf("conf %.2f: source %s, want %s", conf, got.Source, SourceMachinery)
		}
	}
}

// A required independent read survives an answer that names one first reader.
// The answer says who reads FIRST; it does not say who may be skipped.
func TestRequiredReadsSurviveTheAnswer(t *testing.T) {
	s := ReadState{DesignDefaultsTaken: 4, HolderOfTheArea: RoleLaneOwner}
	got, err := ConstrainRead(RoleChildReview, 0.99, s)
	if err != nil {
		t.Fatal(err)
	}
	if got.First != RoleChildReview {
		t.Errorf("first reader %s; an unconstrained pick stands as the FIRST read", got.First)
	}
	if !hasRole(got.Required, RoleDesignAuthority) {
		t.Errorf("four design defaults with no ruling require the design read; got %v", got.Required)
	}
	if !hasRole(got.Required, RoleLaneOwner) {
		t.Errorf("the holder of the area keeps its read; got %v", got.Required)
	}
}

// A provider answer may not replace an unresolved holder, and may not be read
// as lifting that holder's hold.
func TestAnAnswerNeverReplacesAnUnresolvedHolder(t *testing.T) {
	s := ReadState{HolderOfTheArea: RoleDesignAuthority, HoldIsOpen: true}
	got, err := ConstrainRead(RoleChildReview, 1.0, s)
	if err != nil {
		t.Fatal(err)
	}
	if !hasRole(got.Required, RoleDesignAuthority) {
		t.Fatalf("the open hold's holder must keep its read; got %v", got.Required)
	}
	if got.LiftsHold {
		t.Error("no answer at any confidence lifts a hold")
	}
}

// The human is asked only after the friends. An answer naming the human while
// `help --state` says ask-all-friends is refused and the friends stand.
func TestTheHumanIsNeverSelectedBeforeTheFriends(t *testing.T) {
	s := ReadState{HelpAnswer: HelpAskAllFriends}
	got, err := ConstrainRead(RoleHuman, 1.0, s)
	if err != nil {
		t.Fatal(err)
	}
	if got.First == RoleHuman {
		t.Error("the human was selected while help said ask-all-friends")
	}
	if got.First != RoleAllFriends {
		t.Errorf("first reader %s, want %s", got.First, RoleAllFriends)
	}
	ok := ReadState{HelpAnswer: HelpAskGlenn}
	after, err := ConstrainRead(RoleHuman, 1.0, ok)
	if err != nil {
		t.Fatal(err)
	}
	if after.First != RoleHuman {
		t.Errorf("after the friends the human may be selected; got %s", after.First)
	}
}

// A role no registry configures is a refusal: an answer cannot invent an owner.
func TestAnAnswerNamingNoConfiguredRoleIsRefused(t *testing.T) {
	if _, err := ConstrainRead("a-role-nobody-configured", 0.99, ReadState{}); err == nil {
		t.Fatal("an answer naming an unconfigured role must be refused, never adopted")
	}
}

func hasRole(rs []Role, want Role) bool {
	for _, r := range rs {
		if r == want {
			return true
		}
	}
	return false
}
