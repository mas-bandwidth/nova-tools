package redtriage

// names.go is the closed set of deciders and why-states the package
// emits. It is declared apart from the type so the strings can be
// listed in one place and reused.

const (
	// DeciderRules names the rule table: a settled attribution the
	// provider was not asked about (single candidate OR friend-held-out).
	DeciderRules = "rules"

	// DeciderJev names the Jev path: a multi-candidate row that was
	// asked of Jev, and whose answer stood (above the floor).
	DeciderJev = "jev"

	// DeciderFriend names a row held out of the call: a friend
	// attributed the red and the package stood the answer.
	DeciderFriend = "friend"

	// DeciderNone is the absence of a decider. A provider error and no
	// call come out as DeciderNone so the row can be told from a row
	// whose answer stood.
	DeciderNone = "none"
)

// The why-state set: one word per reason a row stands NOT.
const (
	// WhyNoCandidate is "no in-flight PR touched the failing test's
	// file". The mechanical rule did its job; nobody owned the red.
	WhyNoCandidate = "no-candidate"

	// WhyBelowFloor is "Jev returned an answer the floor did not let
	// stand". The provider was asked, but its confidence came back too
	// low to assert. The row escalates.
	WhyBelowFloor = "below-floor"

	// WhyProviderError is "Jev returned nothing the typed question
	// allowed". The provider said nothing, or returned an answer
	// outside Members. The row escalates.
	WhyProviderError = "provider-error"

	// WhyFriend is "the attribution was held out of the call". The row
	// stands, but DeciderFriend, not DeciderRules.
	WhyFriend = "friend"
)

// defaultEscalate is the reader an escalated row goes to. A triage
// escalation is a real reader's job: an OPEN PR to a friend, an
// attached `nova-merge read` line. The reader is named here so a verb
// wiring this package points the escalation at the right person.
const defaultEscalate = "rowan"
