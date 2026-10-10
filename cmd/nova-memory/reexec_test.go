package main

import "github.com/mas-bandwidth/nova-tools/pkg/testbin"

// init runs before every test and before TestMain: a start of this test binary is
// decided by testbin.Enter (docs/TESTS.md, "tests-reexec-guard-everywhere"). A
// test here that runs its own binary (os.Executable, os.Args[0]) reaches the
// suite again unless the words it hands the child are answered by this package;
// its re-executions run the suite with -test.run, and the guard bounds the chain.
func init() {
	testbin.Enter("nova-memory", nil)
}
