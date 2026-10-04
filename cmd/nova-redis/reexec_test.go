package main

import (
	"os"

	"github.com/mas-bandwidth/nova-tools/internal/testbin"
)

// THE GUARD ON THIS TEST BINARY (docs/TESTS.md, tests-reexec-guard-everywhere.w1).
// A test here re-execs the test binary; a child started with words this package
// does not answer, or a chain of test binaries too deep, is refused loudly
// (exit 3) instead of running the whole suite again, as 289 nested processes did.
// A package-level var runs before TestMain, so the guard stands before any dispatch.
var _ = testbin.Guard("nova-redis", func(args []string) bool {
	return os.Getenv("NOVA_REDIS_TEST_AS_MAIN") == "1"
})
