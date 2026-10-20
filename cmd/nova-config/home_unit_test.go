//go:build !functional

package main

import (
	"os"
	"testing"
)

// TestMain gives the unit tier a HOME of its own: the login files
// (~/.config/nova-config/login.json, ~/.config/nova-sprint/login.json) are
// read through os.UserHomeDir, and a unit test never reads the host's. The
// tests are parallel, so the one HOME is set here, not by t.Setenv.
func TestMain(m *testing.M) {
	home, err := os.MkdirTemp("", "nova-config-home-")
	if err != nil {
		panic(err)
	}
	for k, v := range map[string]string{"HOME": home, "XDG_CONFIG_HOME": ""} {
		if err := os.Setenv(k, v); err != nil {
			panic(err)
		}
	}
	code := m.Run()
	_ = os.RemoveAll(home)
	os.Exit(code)
}
