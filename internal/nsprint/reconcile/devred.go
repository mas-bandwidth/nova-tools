package reconcile

// Records DEV-RED with the failing test and the merge that introduced it
// (bisect over the last N merges on a bench), sets the stream landings to
// hold until a fix lands, and clears it on green.
func CheckDevRed() {
}
