// Package sprinttable is what keeps the sprint table's refresh alive across
// a unit restart: ApplyOwnSession puts the refresh in its own session, and
// AbandonProcessGroup checks the loop plist stops launchd signalling the
// unit's process group. The table itself is read from Redis and written
// nowhere (#3326); there is no published file to keep.
package sprinttable

import "strings"

// AbandonProcessGroup reports whether a launchd plist template tells launchd
// not to signal the unit's process group. kickstart -k SIGTERMs that group
// by default; with this key set, the signal reaches the unit's own pid and
// a refresh that already called setsid is not killed a second way.
func AbandonProcessGroup(plist string) bool {
	const key = "<key>AbandonProcessGroup</key>"
	i := strings.Index(plist, key)
	if i < 0 {
		return false
	}
	rest := strings.TrimLeft(plist[i+len(key):], " \t\r\n")
	return strings.HasPrefix(rest, "<true/>")
}
