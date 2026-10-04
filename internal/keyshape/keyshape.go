// Package keyshape holds the one predicate that says whether an environment NAME carries a
// secret: the env var value is never inspected, only the name.
package keyshape

import "strings"

// SecretName says whether an environment NAME carries a secret: the one predicate
// cmd/nova-swarm redacts its argv log by and the shell shim unsets by. Uppercased, so
// `deepseek_api_key` and `MiXeD_KeY` are both caught.
func SecretName(name string) bool {
	up := strings.ToUpper(name)
	return strings.Contains(up, "KEY") || strings.Contains(up, "TOKEN") || strings.Contains(up, "SECRET")
}
