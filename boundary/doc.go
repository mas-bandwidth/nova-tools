// Package boundary holds the test that keeps nova-tools small: the allowed
// top-level shape, the no-library rule, the dependency list and the per-tool
// line budgets in /BUDGET. It has no code of its own; see docs/BOUNDARY.md.
//
// It is a package below the root so that the root stays free of Go files and
// `go build ./...` and `go vet ./...` see nothing new at the top level.
package boundary
