package sprint

import "github.com/mas-bandwidth/nova-tools/pkg/units"

// LoadUnit loads or unloads the unit at path. Under NOVA_TEST_NO_HOST it refuses:
// a test gives its own loader and loads nothing on the machine.
func LoadUnit(goos, op, path string) error { return units.Load(goos, op, path) }

// UnitDir is where a unit goes when no directory is named.
func UnitDir(goos, home string, getenv func(string) string) string {
	return units.Dir(goos, home, getenv)
}
