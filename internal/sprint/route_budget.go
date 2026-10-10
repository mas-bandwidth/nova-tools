package sprint

// A route carries a requests-per-minute budget that the deal keeps, so a promise made to a
// provider is kept by the machine and not by the seat (the card
// a-route-has-a-requests-per-minute-budget-bbb.w4). The budget is the route row's rpm field
// (0 is unmetered), and the deal's admission by it is the first layer: a metered route admits
// a new lane only while its lanes in flight leave room, one lane at a time. The per-request
// meter that counts each harness request into a fleet-wide window and makes a lane wait is a
// later card, not this one's.

// LaneRPM is one lane's typical requests per minute, what a metered route's budget is divided
// by to say how many lanes it runs at once (RouteLaneRoom).
const LaneRPM = 8

// RouteLaneRoom says a metered route admits another lane: rpm 0 (unmetered) admits at any
// count of lanes in flight, and a metered route admits a new lane only while inFlight is under
// max(1, rpm/LaneRPM). So a 10 rpm route runs one lane and a 40 rpm route five.
func RouteLaneRoom(rpm, inFlight int) bool {
	if rpm <= 0 {
		return true
	}
	room := rpm / LaneRPM
	if room < 1 {
		room = 1
	}
	return inFlight < room
}

// routeLanes is each route's lanes in flight across the fleet, by route name: the work cards
// dealt or working (the fleet table's ready and working columns) whose route field names the
// route, a member's and a friend's alike (a friend's card is on her row). A nil table is none.
func routeLanes(fleet *Table) map[string]int {
	out := map[string]int{}
	if fleet == nil {
		return out
	}
	for _, c := range fleet.Column(Ready, Working) {
		if r := c.F(FieldRoute); r != "" && r != RoutePin {
			out[r]++
		}
	}
	return out
}
