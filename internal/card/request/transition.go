package request

import "sort"

// eventSpec is what one event type is: the transitions it has, the outcome it
// gives a done card, and which optional fields it requires or allows. Digest,
// Issuer and Source are required of every type.
type eventSpec struct {
	// to maps each source state the type has a transition from to its destination.
	to      map[State]State
	outcome Outcome
	// required and allowed name Event fields beyond the three every type carries.
	required []string
	allowed  []string
}

// nonterminal lists the states a card can be cancelled from.
var nonterminal = []State{Waiting, Ready, Working, Review, Merging}

func allTo(dst State, from ...State) map[State]State {
	m := make(map[State]State, len(from))
	for _, s := range from {
		m[s] = dst
	}
	return m
}

// eventSpecs is the closed set of typed transitions and nothing else.
var eventSpecs = map[EventType]eventSpec{
	EvStart:            {to: allTo(Working, Ready)},
	EvResult:           {to: allTo(Review, Working), required: []string{"result"}, allowed: []string{"head"}},
	EvVerdictAccept:    {to: allTo(Merging, Review), required: []string{"head"}},
	EvVerdictRetry:     {to: allTo(Ready, Review), required: []string{"reason"}, allowed: []string{"head"}},
	EvVerdictRework:    {to: allTo(Ready, Review), required: []string{"reason"}, allowed: []string{"head"}},
	EvHead:             {to: allTo(Review, Merging), required: []string{"head"}},
	EvCIGreen:          {to: map[State]State{}, required: []string{"head"}},
	EvCIRed:            {to: allTo(Review, Merging), required: []string{"head"}},
	EvCancel:           {to: allTo(Done, nonterminal...), outcome: Cancelled, required: []string{"reason"}},
	EvLanding:          {to: allTo(Landed, Merging), required: []string{"head", "landing"}},
	EvExternalLanding:  {to: allTo(Landed, Waiting, Ready, Working), required: []string{"head", "landing"}},
	EvDependencyFailed: {to: allTo(Done, Waiting), outcome: DependencyFailed, required: []string{"dependency"}},
	EvCompleted:        {to: allTo(Done, Review), outcome: Completed},
}

// Valid reports whether t is one of the event types.
func (t EventType) Valid() bool { _, ok := eventSpecs[t]; return ok }

// Destination returns the state a card moves to when an event of the given type
// is applied to a card in the source state. ok is false when the type and the
// source state have no listed transition; such an event refuses. The result is
// the placement only; OutcomeOf gives a done card's outcome.
func Destination(t EventType, source State) (State, bool) {
	spec, ok := eventSpecs[t]
	if !ok {
		return "", false
	}
	dst, ok := spec.to[source]
	return dst, ok
}

// OutcomeOf returns the outcome a done card takes from an event type, and false
// for a type that does not end in done.
func OutcomeOf(t EventType) (Outcome, bool) {
	spec, ok := eventSpecs[t]
	if !ok || spec.outcome == "" {
		return "", false
	}
	return spec.outcome, true
}

// SourceStates lists the source states an event type has a transition from, in
// lifecycle order.
func SourceStates(t EventType) []State {
	spec := eventSpecs[t]
	var out []State
	for _, s := range States {
		if _, ok := spec.to[s]; ok {
			out = append(out, s)
		}
	}
	return out
}

// eventFields lists the optional Event fields by wire name.
var eventFields = []string{"head", "result", "reason", "dependency", "landing"}

func (e *Event) field(name string) string {
	switch name {
	case "head":
		return e.Head
	case "result":
		return e.Result
	case "reason":
		return e.Reason
	case "dependency":
		return string(e.Dependency)
	case "landing":
		return e.Landing
	}
	return ""
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func sortedTypes() []string {
	out := make([]string, 0, len(eventSpecs))
	for t := range eventSpecs {
		out = append(out, string(t))
	}
	sort.Strings(out)
	return out
}
