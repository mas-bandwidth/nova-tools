package goenv

import "strings"

// IsSecretName reports whether an environment variable's NAME (never its value)
// looks like a credential. It is the one predicate this repo uses to decide
// whether a name may cross into a log or a walled child process: nothing that
// forwards or writes environment names keeps its own copy (cmd/nova-swarm's
// native-argv log and internal/pulse's accept gate both call this one).
func IsSecretName(name string) bool {
	up := strings.ToUpper(name)
	return strings.Contains(up, "KEY") || strings.Contains(up, "TOKEN") || strings.Contains(up, "SECRET")
}
