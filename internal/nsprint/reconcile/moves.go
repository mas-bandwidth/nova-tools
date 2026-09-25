package reconcile

// The reconciler's move graph (nova-tools #4059): every move a duty in this
// package can make, with the guard it moves on.
//
// THE HURT. build-3041 went waiting -> ready by the waiting-resolve duty
// ("depends-on met") and ready -> waiting by the deal duty ("no-consumer")
// every three to five seconds for an hour. Each duty was right by its own
// rule; together they were a loop, because each move's guard said nothing of
// the other's: a card whose dependencies were met could have no consumer.
//
// THE RULE, held by internal/ci's TestNoInverseDutyMovesInTheReconciler over
// this table and the code it describes: no two moves here (of two duties, or
// of one) are each other's inverse (A: X -> Y, B: Y -> X) unless one of them
// cannot fire right after the other, that is, its guard names the negation of
// an atom the other's guard requires or the other's move sets; and no move is
// ready -> waiting (Glenn 2026-09-25 1:40 PM ET: waiting -> ready is ONE
// WAY), but for the rows its shrink-only allowlist names. The test also holds
// the table to the code: every place a Lua function this package calls moves
// a card to has a row, and every row's function is called from its file.
//
// Places are the ws index's and the card model's: waiting, ready, working,
// merging, landed, done, parked; "" is a card that did not exist (created
// into its place), and a|b in From is a move from either.
//
// Atoms (a guard's "!x" is not-x):
//
//	deps-met      every DEPENDS-ON entry of the card has landed
//	consumer      a friend seat or the swarm takes the card this tick (consumer.go)
//	seat          an open seat: a bench slot, or a friend's slot
//	attempt-live  the card's current attempt holds: dealt and not lost, refused or timed out
//	retries-left  the card's retries are below the cap
//	pr-open       the card's work has an open PR
//	merged        the card's work has merged (its issue is closed by it)
//	superseded    the card's work was replaced: its sprint closed, or a new head
//	              took its read (state superseded, or a done/fail with that why)
//	sprint-closed the card's sprint is closed

// DutyMove is one move a reconciler duty can make.
type DutyMove struct {
	Duty     string   // the duty's name on the DUTIES line
	File     string   // the file of this package that calls Via
	Via      string   // the Lua function (FCALL) that writes the move
	From, To string   // the places, as above
	Guard    []string // atoms that hold whenever the move fires
	Sets     []string // atoms the move makes true
}

// DutyMoves is the reconciler's move graph, one row per move.
var DutyMoves = []DutyMove{
	// refill: the swarm's dealer (internal/nsprint/deal) over the card model.
	{Duty: "refill", File: "refill.go", Via: "ns_card_deal", From: "ready", To: "working",
		Guard: []string{"deps-met", "seat", "retries-left"}, Sets: []string{"attempt-live"}},
	{Duty: "refill", File: "refill.go", Via: "ns_card_deal", From: "ready", To: "done",
		Guard: []string{"!retries-left"}},
	{Duty: "refill", File: "refill.go", Via: "ns_card_undeal", From: "working", To: "ready",
		Guard: []string{"!attempt-live"}},
	{Duty: "refill", File: "refill.go", Via: "ns_card_deal_fail", From: "working", To: "ready",
		Guard: []string{"!attempt-live"}},
	{Duty: "refill", File: "refill.go", Via: "ns_card_gate", From: "ready", To: "waiting",
		Guard: []string{"!deps-met"}},
	{Duty: "refill", File: "refill.go", Via: "ns_card_gate", From: "waiting", To: "ready",
		Guard: []string{"deps-met"}},

	// expire: leases, launches and results that did not come back.
	{Duty: "expire", File: "expire.go", Via: "ns_card_reclaim", From: "working", To: "ready",
		Guard: []string{"!attempt-live"}},
	{Duty: "expire", File: "expire.go", Via: "ns_card_reclaim", From: "working", To: "working",
		Guard: []string{"!attempt-live"}},
	{Duty: "expire", File: "expire.go", Via: "ns_card_required", From: "working", To: "working",
		Guard: []string{"!attempt-live"}},
	{Duty: "expire", File: "expire.go", Via: "ns_card_required", From: "working", To: "ready",
		Guard: []string{"!attempt-live"}},
	{Duty: "expire", File: "expire.go", Via: "ns_card_required_timeout", From: "working", To: "done",
		Guard: []string{"!attempt-live"}},
	{Duty: "expire", File: "expire.go", Via: "ns_card_retry", From: "done", To: "ready",
		Guard: []string{"!attempt-live", "retries-left", "!superseded"}},

	// route: the read and fix tasks a harvested PR needs, and its merging.
	{Duty: "route", File: "route_duty.go", Via: "ns_route_read", From: "", To: "ready",
		Guard: []string{"pr-open"}},
	{Duty: "route", File: "route_duty.go", Via: "ns_route_fix", From: "", To: "ready",
		Guard: []string{"pr-open"}},
	{Duty: "route", File: "route_duty.go", Via: "ns_route_pr_read", From: "", To: "ready",
		Guard: []string{"pr-open"}},
	{Duty: "route", File: "route_duty.go", Via: "ns_route_pr_fix", From: "", To: "ready",
		Guard: []string{"pr-open"}},
	{Duty: "route", File: "route_duty.go", Via: "ns_route_merging", From: "ready", To: "working",
		Guard: []string{"pr-open"}, Sets: []string{"attempt-live"}},
	{Duty: "route", File: "route_duty.go", Via: "ns_route_merging", From: "waiting|ready|working", To: "merging",
		Guard: []string{"pr-open"}},
	{Duty: "route", File: "route_duty.go", Via: "ns_route_pr_read", From: "waiting|ready", To: "done",
		Guard: []string{"pr-open"}, Sets: []string{"superseded"}},

	// fsck: drift repaired from the record (a null card adopted into its
	// place), and a closed sprint's cards retired (#3925).
	{Duty: "fsck", File: "fsck.go", Via: "ns_card_repair", From: "", To: "waiting"},
	{Duty: "fsck", File: "fsck.go", Via: "ns_sprint_retire", From: "", To: "waiting",
		Guard: []string{"sprint-closed"}},
	{Duty: "fsck", File: "fsck.go", Via: "ns_sprint_retire", From: "waiting|ready|working|parked", To: "done",
		Guard: []string{"sprint-closed"}, Sets: []string{"superseded"}},

	// done-already: work a commit already did lands its tasks.
	{Duty: "done-already", File: "done_already.go", Via: "ns_card_done_already", From: "waiting|ready|working|merging|parked", To: "landed",
		Guard: []string{"merged"}},

	// waiting-resolve, then deal, in one pass (#3872, #3873, #4059).
	{Duty: "waiting-resolve", File: "waiting_resolve.go", Via: "ns_ws_move_many", From: "waiting", To: "ready",
		Guard: []string{"deps-met", "consumer"}},
	{Duty: "deal", File: "deal_friend.go", Via: "ns_deal_friend", From: "ready", To: "working",
		Guard: []string{"consumer", "seat"}, Sets: []string{"attempt-live"}},
	{Duty: "deal", File: "deal_friend.go", Via: "ns_deal_swarm", From: "ready", To: "working",
		Guard: []string{"consumer"}, Sets: []string{"attempt-live"}},
}
