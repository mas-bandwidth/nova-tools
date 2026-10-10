// Package keyshape holds the one predicate that says whether an environment NAME carries a
// secret: the env var value is never inspected, only the name.
package keyshape

import "strings"

// SecretName says whether an environment NAME carries a secret: the one predicate
// cmd/nova-worker redacts its argv log by, the shell shim unsets by, and both
// internal/goenv gate scrub paths drop by (SPEC-CI.md, the `goenv` class rule).
// Uppercased, so `deepseek_api_key` and `MiXeD_KeY` are both caught. PASSWORD
// and PASSWD are caught too: a database credential (PGPASSWORD,
// NOVA_PG_PASSWORD, NOVA_REDIS_PASSWORD) carries neither KEY nor TOKEN.
func SecretName(name string) bool {
	up := strings.ToUpper(name)
	return strings.Contains(up, "KEY") || strings.Contains(up, "TOKEN") ||
		strings.Contains(up, "SECRET") || strings.Contains(up, "PASSWORD") ||
		strings.Contains(up, "PASSWD")
}
