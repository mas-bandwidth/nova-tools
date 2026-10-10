package main

import "github.com/mas-bandwidth/nova-tools/pkg/testbin"

// init runs before every test and before TestMain: a start of this test binary is
// decided by testbin.Enter (docs/TESTS.md, "tests-reexec-guard-everywhere"). A
// test here that runs its own binary (os.Executable, os.Args[0]) reaches the
// suite again unless the words it hands the child are answered by this package;
// the probe step and the unix listener are the starts it answers.
func init() {
	testbin.Enter("nova-sandbox", func(args []string, _ func(string) string) bool {
		return args[0] == "probe-step" || (len(args) == 1 && args[0] == unixListenerVerb)
	})
}
