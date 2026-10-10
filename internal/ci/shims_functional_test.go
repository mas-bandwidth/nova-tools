//go:build functional

package ci

// shimTools are the one-release compatibility shims: a directory under cmd/
// whose binary is a passthrough to another tool, not a tool of its own. It has
// no verbs, no banner, no help and no version line of its own, so the walks
// that hold every command to the onboarding, refusal-grammar and version
// standards skip it. nova-swarm is the v1.3 shim for nova-worker and is removed
// in the release after it (cmd/nova-swarm/README.md).
var shimTools = map[string]bool{"nova-swarm": true}
