package main

import "github.com/mas-bandwidth/nova-tools/pkg/testbin"

// init runs before every test and before TestMain: a start of this test binary is
// decided by testbin.Enter (docs/TESTS.md, "tests-reexec-guard-everywhere"). A
// test here that runs its own binary (os.Executable, os.Args[0]) reaches the
// suite again unless the words it hands the child are answered by this package;
// the fake go (fakeGoEnv in make_functional_test.go, a functional-tier file) is the one start it answers.
func init() {
	testbin.Enter("nova-ci", func(_ []string, getenv func(string) string) bool { return getenv("NOVA_CI_FAKE_GO") != "" })
}
