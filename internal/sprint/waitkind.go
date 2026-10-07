package sprint

// WaitKind is the kind of wait a primary is in.
type WaitKind string

const (
	WaitNone    WaitKind = ""
	WaitNeeds    WaitKind = "needs"
	WaitPosition WaitKind = "position"
	WaitRelease  WaitKind = "release"
)

// WaitOf returns what kind of wait the primary is in.
// This consolidates all wait mechanisms (held, sentinel, needs) into one path.
func WaitOf(c *Card) WaitKind {
	if c == nil || !c.Placed() {
		return WaitNone
	}
	if c.F("held") != "" || c.F("kind") == "sentinel" {
		return WaitRelease
	}
	if c.F("needs") != "" {
		return WaitNeeds
	}
	if c.Col == string(Waiting) {
		return WaitPosition
	}
	return WaitNone
}

// IsWaitingForRelease reports if the card is waiting for coordinator release.
func IsWaitingForRelease(c *Card) bool {
	return WaitOf(c) == WaitRelease
}
