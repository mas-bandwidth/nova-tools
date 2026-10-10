package sprint

import "github.com/mas-bandwidth/nova-tools/pkg/units"

// The unit text, the install and the check live in pkg/units, which a worker's
// binary may import. These names are the ones nova-sprint, nova-redis and the tests
// already call. The loader is the caller's, so a test loads nothing on its machine.

type UnitKind = units.UnitKind
type ServiceUnit = units.ServiceUnit
type UnitState = units.UnitState

const (
	UnitInstalled = units.UnitInstalled
	UnitMissing   = units.UnitMissing
	UnitDifferent = units.UnitDifferent
)

// UnitKinds is every unit a running sprint needs, in the order a machine is brought up.
var UnitKinds = units.UnitKinds

// UnitKindOf is the kind named k.
func UnitKindOf(k string) (UnitKind, bool) { return units.UnitKindOf(k) }

// UnitKindNames is every kind's word whose install verb is tool's, in UnitKinds' order.
func UnitKindNames(tool string) []string { return units.UnitKindNames(tool) }

// CheckUnits looks for every kind's unit in dir. It reads files only.
func CheckUnits(dir, goos string, kinds []UnitKind) ([]UnitState, error) {
	return units.CheckUnits(dir, goos, kinds)
}

func (in SeatInstaller) installer() units.Installer {
	return units.Installer{Dir: in.Dir, Load: in.Load, Unload: in.Unload}
}

func (in SeatInstaller) writeUnit(path, text string) (SeatResult, error) {
	r, err := in.installer().Write(path, text)
	return SeatResult{Path: r.Path, Changed: r.Changed}, err
}

func (in SeatInstaller) removeUnit(path string) (SeatResult, error) {
	r, err := in.installer().Remove(path)
	return SeatResult{Path: r.Path, Changed: r.Changed}, err
}

// InstallUnit writes the unit and loads it, as Install does the push loop's.
func (in SeatInstaller) InstallUnit(u ServiceUnit) (SeatResult, error) {
	r, err := in.installer().Install(u)
	return SeatResult{Path: r.Path, Changed: r.Changed}, err
}

// UninstallUnit unloads the kind's unit and removes its file.
func (in SeatInstaller) UninstallUnit(k UnitKind, goos string) (SeatResult, error) {
	r, err := in.installer().Uninstall(k, goos)
	return SeatResult{Path: r.Path, Changed: r.Changed}, err
}
