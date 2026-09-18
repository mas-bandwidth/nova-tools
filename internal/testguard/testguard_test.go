package testguard

import (
	"os"
	"strings"
	"testing"
)

// arm turns the guard on for one test and puts the cached value back
// afterwards. The cleanup registered here runs BEFORE t.Setenv's own, so the
// cache is never left holding a value the environment no longer has.
func arm(t *testing.T) {
	t.Helper()
	t.Setenv(EnvNoHost, "1")
	Reload()
	t.Cleanup(func() {
		os.Unsetenv(EnvNoHost)
		Reload()
	})
}

func TestUnsetGuardLetsTheSeamRun(t *testing.T) {
	t.Setenv(EnvNoHost, "")
	Reload()
	t.Cleanup(Reload)
	if Refusing() {
		t.Fatal("the guard must be off when the variable is unset; production pays nothing for it")
	}
	RefuseHosts("ssh", "hulk", "uptime") // must not panic
}

func TestArmedGuardNamesTheCommandAndTheRemedy(t *testing.T) {
	arm(t)
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("an armed guard must refuse the seam")
		}
		msg, _ := r.(string)
		for _, want := range []string{EnvNoHost, `"ssh"`, `"hulk"`, `"bash -s"`, "testguard.AllowHosts"} {
			if !strings.Contains(msg, want) {
				t.Errorf("the refusal must carry %s; got %q", want, msg)
			}
		}
	}()
	RefuseHosts("ssh", "hulk", "bash -s")
}

func TestAllowHostsIsScopedAndNests(t *testing.T) {
	arm(t)
	outer := AllowHosts()
	inner := AllowHosts()
	inner()
	RefuseHosts("ssh", "hulk") // the outer scope still stands
	inner()                    // closing twice is not a second decrement
	RefuseHosts("ssh", "hulk")
	outer()
	defer func() {
		if recover() == nil {
			t.Fatal("the guard must be armed again once every scope has closed")
		}
	}()
	RefuseHosts("ssh", "hulk")
}
