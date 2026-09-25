package decide

import (
	"strings"
	"testing"
)

// The second decision: continue, ask all friends, or ask Glenn -- and Glenn is
// only ever asked after all friends have been.
func TestHelpAnswers(t *testing.T) {
	for name, tc := range map[string]struct {
		state HelpState
		want  string
	}{
		"moving along": {
			state: HelpState{Hours: 0.5, RetriesOnRung: 1, LandingMoved: true, Uncertainty: 0.1},
			want:  HelpContinue,
		},
		"hours on the same problem": {
			state: HelpState{Hours: 3, LandingMoved: true},
			want:  HelpAskAllFriends,
		},
		"retries on one rung": {
			state: HelpState{Hours: 0.5, RetriesOnRung: 3, LandingMoved: true},
			want:  HelpAskAllFriends,
		},
		"self-inflicted failures": {
			state: HelpState{Hours: 0.5, FailuresLastHour: 4, SelfInflicted: 3, LandingMoved: true},
			want:  HelpAskAllFriends,
		},
		"a class recurring": {
			state: HelpState{Hours: 0.5, ClassRecurring: true, LandingMoved: true},
			want:  HelpAskAllFriends,
		},
		"landing has not moved": {
			state: HelpState{Hours: 1.5, LandingMoved: false},
			want:  HelpAskAllFriends,
		},
		"stated uncertainty": {
			state: HelpState{Hours: 0.5, Uncertainty: 0.8, LandingMoved: true},
			want:  HelpAskAllFriends,
		},
		"friends asked and still stuck": {
			state: HelpState{Hours: 5, AskedAllFriends: true, LandingMoved: false},
			want:  HelpAskGlenn,
		},
		"friends asked, class recurring, landing still": {
			state: HelpState{Hours: 2, AskedAllFriends: true, ClassRecurring: true, LandingMoved: false},
			want:  HelpAskGlenn,
		},
		"friends asked and it moved": {
			state: HelpState{Hours: 2.5, AskedAllFriends: true, LandingMoved: true},
			want:  HelpContinue,
		},
	} {
		res, err := Help(tc.state)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if res.Answer != tc.want {
			t.Errorf("%s: answer = %s, want %s (%s)", name, res.Answer, tc.want, res.Reason)
		}
		if strings.TrimSpace(res.Reason) == "" {
			t.Errorf("%s: an answer with no reason is not a record", name)
		}
	}
}

// Glenn is never the first ask, whatever the hours say.
func TestGlennIsOnlyAskedAfterAllFriends(t *testing.T) {
	res, err := Help(HelpState{Hours: 12, FailuresLastHour: 9, SelfInflicted: 9, ClassRecurring: true, Uncertainty: 1})
	if err != nil {
		t.Fatal(err)
	}
	if res.Answer != HelpAskAllFriends {
		t.Fatalf("answer = %s, want %s: ask-glenn only after ask-all-friends", res.Answer, HelpAskAllFriends)
	}
}

// Numbers that cannot be are a refusal, never a guess.
func TestHelpRefusesImpossibleState(t *testing.T) {
	for name, s := range map[string]HelpState{
		"negative hours":      {Hours: -1},
		"negative retries":    {RetriesOnRung: -2},
		"more self than all":  {FailuresLastHour: 1, SelfInflicted: 2},
		"uncertainty over 1":  {Uncertainty: 1.4},
		"uncertainty under 0": {Uncertainty: -0.1},
	} {
		if _, err := Help(s); err == nil {
			t.Errorf("%s: answered, want a refusal", name)
		}
	}
}

// One line, the answer and the reason.
func TestHelpLineShape(t *testing.T) {
	res, err := Help(HelpState{Hours: 3, LandingMoved: true})
	if err != nil {
		t.Fatal(err)
	}
	line := res.Line()
	if strings.Contains(line, "\n") {
		t.Fatalf("exactly one line: %q", line)
	}
	for _, want := range []string{"HELP ", "answer=ask-all-friends", "reason=\""} {
		if !strings.Contains(line, want) {
			t.Errorf("the line is missing %q: %s", want, line)
		}
	}
}
