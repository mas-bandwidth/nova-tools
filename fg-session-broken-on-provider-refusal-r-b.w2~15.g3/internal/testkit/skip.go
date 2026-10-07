package testkit

import (
	"runtime"
	"testing"
)

// SkipOn skips the test when it runs on the operating system goos (a
// runtime.GOOS value), saying why the property cannot be observed there:
//
//	testkit.SkipOn(t, "windows", "chmod 0 does not refuse reads")
func SkipOn(t testing.TB, goos, why string) {
	t.Helper()
	if runtime.GOOS == goos {
		t.Skipf("skipped on %s: %s", goos, why)
	}
}
