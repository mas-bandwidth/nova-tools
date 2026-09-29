package request

// inputSpec is what one lifecycle input type is: the transitions it has, the
// outcome it gives a done card, and which optional fields it requires or allows.
// Digest, Issuer and Source are required of every type.
type inputSpec struct {
	// to maps each source state the type has a transition from to its destination.
	to      map[State]State
	outcome Outcome
	// required and allowed name Input fields beyond the three every type carries.
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

// inputSpecs is the closed set of typed transitions and nothing else.
var inputSpecs = map[InputType]inputSpec{
	InStart:            {to: allTo(Working, Ready)},
	InResult:           {to: allTo(Review, Working), required: []string{"result"}, allowed: []string{"head"}},
	InVerdictAccept:    {to: allTo(Merging, Review), required: []string{"head"}},
	InVerdictRetry:     {to: allTo(Ready, Review), required: []string{"reason"}, allowed: []string{"head"}},
	InVerdictRework:    {to: allTo(Ready, Review), required: []string{"reason"}, allowed: []string{"head"}},
	InHead:             {to: allTo(Review, Merging, Review), required: []string{"head"}},
	InQueueRejected:    {to: allTo(Review, Merging), required: []string{"head", "reason"}},
	InCancel:           {to: allTo(Done, nonterminal...), outcome: Cancelled, required: []string{"reason"}},
	InLanding:          {to: allTo(Landed, Merging), required: []string{"head", "landing"}},
	InExternalLanding:  {to: allTo(Landed, Waiting, Ready, Working), required: []string{"head", "landing"}},
	InDependencyFailed: {to: allTo(Done, Waiting), outcome: DependencyFailed, required: []string{"dependency"}},
	InCompleted:        {to: allTo(Done, Review), outcome: Completed},
}

// Valid reports whether t is one of the lifecycle input types.
func (t InputType) Valid() bool { _, ok := inputSpecs[t]; return ok }

// Destination returns the state a card moves to when a lifecycle input of the
// given type is applied to a card in the source state. ok is false when the type
// and the source state have no listed transition; such an input refuses. The
// result is the placement only; OutcomeOf gives a done card's outcome. A move to
// the state the card is already in (a new head while in review) is listed.
func Destination(t InputType, source State) (State, bool) {
	spec, ok := inputSpecs[t]
	if !ok {
		return "", false
	}
	dst, ok := spec.to[source]
	return dst, ok
}

// OutcomeOf returns the outcome a done card takes from an input type, and false
// for a type that does not end in done.
func OutcomeOf(t InputType) (Outcome, bool) {
	spec, ok := inputSpecs[t]
	if !ok || spec.outcome == "" {
		return "", false
	}
	return spec.outcome, true
}

// SourceStates lists the source states an input type has a transition from, in
// lifecycle order.
func SourceStates(t InputType) []State {
	spec := inputSpecs[t]
	var out []State
	for _, s := range allStates {
		if _, ok := spec.to[s]; ok {
			out = append(out, s)
		}
	}
	return out
}

// inputFields lists the optional Input fields by wire name.
var inputFields = []string{"head", "result", "reason", "dependency", "landing"}

func (e *Input) field(name string) string {
	switch name {
	case "head":
		return e.Head
	case "result":
		return string(e.Result)
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

// Forced is one forced move: an observation recorded as evidence that moves the
// card in the same batch that records it.
type Forced struct {
	Kind        EvidenceKind
	Disposition Disposition
	From, To    State
}

// forcedMoves is the data of the forced moves, as the owner ruled them: a red CI
// result recorded for a card in merging returns it to review in the same batch.
// It has exactly one entry today; every other combination forces nothing. The
// policy that applies a forced move is the manager's, not this package's.
var forcedMoves = []Forced{
	{Kind: KindCI, Disposition: DispRed, From: Merging, To: Review},
}

// ForcedMoves lists the forced moves, as a new slice.
func ForcedMoves() []Forced { return append([]Forced(nil), forcedMoves...) }

// ForcedMove returns the state an evidence record forces a card in the given
// state into, and false when it forces nothing. Only a red CI result for a card
// in merging forces a move: to review.
func ForcedMove(kind EvidenceKind, disposition Disposition, state State) (State, bool) {
	for _, f := range forcedMoves {
		if f.Kind == kind && f.Disposition == disposition && f.From == state {
			return f.To, true
		}
	}
	return "", false
}
