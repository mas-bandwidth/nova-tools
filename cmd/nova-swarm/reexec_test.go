package main

import "github.com/mas-bandwidth/nova-tools/internal/testbin"

// THE GUARD ON THIS TEST BINARY (docs/TESTS.md, tests-reexec-guard-everywhere.w2).
// A test here re-execs the test binary; a child started with words this package
// does not answer, or a chain of test binaries too deep, is refused loudly
// (exit 3) instead of running the whole suite again, as 289 nested processes did.
// A package-level var runs before TestMain, so the guard stands before any dispatch.
var _ = testbin.Guard("nova-swarm", nil)
