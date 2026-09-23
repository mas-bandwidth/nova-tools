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
