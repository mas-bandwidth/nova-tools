// The second decision (Glenn 2026-09-18): do I keep going, ask all my friends,
// or ask Glenn?
//
// The evidence is what a line can count about itself: hours on the SAME
// problem, retries on one rung, failures in the last hour and how many of those
// it caused itself, whether a class of failure is recurring, whether landing
// moved at all, and the uncertainty it states out loud.
//
// One rule stands over the rest: ask-glenn only AFTER ask-all-friends. A line
// that has not asked its friends has not earned the interrupt.
package decide

import (
	"fmt"
	"math"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// The three answers.
const (
	HelpContinue      = "continue"
	HelpAskAllFriends = "ask-all-friends"
	HelpAskGlenn      = "ask-glenn"
)

// The thresholds, named so a reader can argue with the numbers rather than
// with the code.
const (
	helpHoursAsk       = 2.0 // hours on one problem before the friends are asked
	helpHoursGlenn     = 4.0 // hours after the friends before Glenn is
	helpHoursNoLanding = 1.0 // hours with no landing movement is its own ask
	helpRetries        = 3   // retries on ONE rung: the ladder, not the loop
	helpFailures       = 3   // failures in the last hour
	helpSelfInflicted  = 2   // of those, ones we caused ourselves
	helpUncertainty    = 0.5 // stated uncertainty at or above this is an ask
)

// HelpState is the evidence for the help decision.
type HelpState struct {
	Hours            float64 `json:"hours"`
	RetriesOnRung    int     `json:"retries_on_rung"`
	FailuresLastHour int     `json:"failures_last_hour"`
	SelfInflicted    int     `json:"self_inflicted"`
	ClassRecurring   bool    `json:"class_recurring"`
	LandingMoved     bool    `json:"landing_moved"`
	Uncertainty      float64 `json:"uncertainty"`
	AskedAllFriends  bool    `json:"asked_all_friends"`
}

// ParseHelpState reads the help evidence from JSON, refusing a field it does
// not hold.
func ParseHelpState(data []byte) (HelpState, error) {
	var s HelpState
	if err := unmarshalStrict(data, &s); err != nil {
		return HelpState{}, fmt.Errorf("decide: bad help state: %w", err)
	}
	return s, nil
}

// Validate refuses a state that cannot be, rather than answering over it.
func (s HelpState) Validate() error {
	switch {
	case math.IsNaN(s.Hours) || math.IsInf(s.Hours, 0):
		return fmt.Errorf("decide: help state has %v hours; it wants a number, such as 2", s.Hours)
	case math.IsNaN(s.Uncertainty) || math.IsInf(s.Uncertainty, 0):
		return fmt.Errorf("decide: help state has uncertainty %v; it wants a number between 0 and 1, such as 0.5", s.Uncertainty)
	case s.Hours < 0:
		return fmt.Errorf("decide: help state has %g hours", s.Hours)
	case s.RetriesOnRung < 0:
		return fmt.Errorf("decide: help state has %d retries", s.RetriesOnRung)
	case s.FailuresLastHour < 0 || s.SelfInflicted < 0:
		return fmt.Errorf("decide: help state has a negative failure count")
	case s.SelfInflicted > s.FailuresLastHour:
		return fmt.Errorf("decide: help state has %d self-inflicted failures of %d in the last hour", s.SelfInflicted, s.FailuresLastHour)
	case s.Uncertainty < 0 || s.Uncertainty > 1:
		return fmt.Errorf("decide: help state has uncertainty %g, want between 0 and 1", s.Uncertainty)
	}
	return nil
}

// HelpResult is the answer and why.
type HelpResult struct {
	Answer string
	Reason string
}

// Line is the one line the help decision prints.
func (r HelpResult) Line() string {
	return fmt.Sprintf("HELP answer=%s reason=%s", oneline.Field(r.Answer), oneline.Quote(oneline.Escape(r.Reason)))
}

// Help answers the second decision. Nothing here is a judgment call at run
// time: the same evidence gives the same answer, on any bench, with no network.
func Help(s HelpState) (HelpResult, error) {
	if err := s.Validate(); err != nil {
		return HelpResult{}, err
	}
	var asks []string
	if s.Hours >= helpHoursAsk {
		asks = append(asks, fmt.Sprintf("%.1f h on the same problem", s.Hours))
	}
	if s.RetriesOnRung >= helpRetries {
		asks = append(asks, fmt.Sprintf("%d retries on one rung", s.RetriesOnRung))
	}
	if s.FailuresLastHour >= helpFailures && s.SelfInflicted >= helpSelfInflicted {
		asks = append(asks, fmt.Sprintf("%d failures in the last hour, %d of them self-inflicted", s.FailuresLastHour, s.SelfInflicted))
	}
	if s.ClassRecurring {
		asks = append(asks, "a class of failure is recurring")
	}
	if !s.LandingMoved && s.Hours >= helpHoursNoLanding {
		asks = append(asks, fmt.Sprintf("landing has not moved in %.1f h", s.Hours))
	}
	if s.Uncertainty >= helpUncertainty {
		asks = append(asks, fmt.Sprintf("stated uncertainty %.2f", s.Uncertainty))
	}
	if len(asks) == 0 {
		return HelpResult{Answer: HelpContinue, Reason: helpNothingWrong(s)}, nil
	}
	why := strings.Join(asks, "; ")
	if !s.AskedAllFriends {
		return HelpResult{Answer: HelpAskAllFriends, Reason: why + "; the friends have not been asked, and Glenn is only asked after they are"}, nil
	}
	if s.Hours >= helpHoursGlenn {
		return HelpResult{Answer: HelpAskGlenn, Reason: why + "; the friends have been asked and it is still open"}, nil
	}
	if !s.LandingMoved && (s.ClassRecurring || s.Uncertainty >= helpUncertainty) {
		return HelpResult{Answer: HelpAskGlenn, Reason: why + "; the friends have been asked and landing still has not moved"}, nil
	}
	return HelpResult{Answer: HelpContinue, Reason: why + "; the friends have been asked and landing is moving, so carry on while their answers arrive"}, nil
}

// helpNothingWrong says why nothing is wrong, because a reason a person cannot
// read is not a record.
func helpNothingWrong(s HelpState) string {
	moved := "landing has not moved yet, and it is early"
	if s.LandingMoved {
		moved = "landing moved"
	}
	return fmt.Sprintf("%.1f h in, %d retries on this rung, %d failures in the last hour, %s", s.Hours, s.RetriesOnRung, s.FailuresLastHour, moved)
}
