package doctor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/units"
)

// unitsTimeout bounds the one nova-config read the check makes: `nova-config
// inventory` opens the store and prints the applied state, and this bounds it
// when the context carries no deadline of its own.
const unitsTimeout = 20 * time.Second

func init() {
	Default.Register(Check{Name: "units", Dependency: "the service units for the loop records", Fleet: true, Run: checkUnits})
}

// unitsLocal is what a machine nova-up --local set up uses in the fleet's
// place: nova-up --local installs one loop record's unit, redis-local, itself
// and reads no nova-config (docs/SPEC-UP.md "Steps", docs/SETUP.md,
// dep-launchd-units-bc.w6).
const unitsLocal = "no fleet sprint store: nova-up --local installs one loop record's unit (redis-local) itself; the fleet's loop records are applied by nova-config apply --kind loop and the fleet play"

// loopRecord is one loop record as the check compares a unit against it: the
// record's name (the unit's file name) and the argv the unit must run.
type loopRecord struct {
	Name string   `json:"name"`
	Argv []string `json:"argv"`
}

// inventoryLoops is the part of `nova-config inventory` the check reads: each
// machine's nova_loops and which host is this one, marked ansible_connection
// local (cmd/nova-config/inventory.go, localHost).
type inventoryLoops struct {
	Meta struct {
		Hostvars map[string]struct {
			Connection string       `json:"ansible_connection"`
			Loops      []loopRecord `json:"nova_loops"`
		} `json:"hostvars"`
	} `json:"_meta"`
}

// checkUnits covers the service units (docs/SETUP.md, dep-launchd-units-bc.w6):
// every long-running nova loop is a nova-config loop record installed as a
// launchd plist or a systemd user unit. It reads this machine's records from
// `nova-config inventory` and the units in the service manager's directory,
// and names a record with no unit, a unit with no record (a hand plist), and a
// unit whose command differs from its record; the fix is the nova-config and
// fleet verb that applies records. A machine whose sprint store is its own
// twin (`mem:`) runs no fleet and is ok.
func checkUnits(ctx context.Context, env Env) Result {
	const doc = "docs/SETUP.md, dep-launchd-units-bc.w6"
	addr := env.Getenv("NOVA_SPRINT_REDIS")
	if addr == "" || strings.HasPrefix(addr, "mem:") {
		return Result{Status: OK, Evidence: unitsLocal}
	}
	dirs := unitsDirs(env)
	cctx, cancel := context.WithTimeout(ctx, unitsTimeout)
	defer cancel()
	out, err := env.Exec(cctx, "nova-config", "inventory")
	if err != nil {
		return Result{Status: Fail,
			Evidence: "nova-config inventory did not answer: " + oneLine(err.Error()),
			Fix:      "nova-config inventory (it prints the store's own refusal); check the sprint store address and the tools on PATH (" + doc + ")"}
	}
	var inv inventoryLoops
	if uerr := json.Unmarshal([]byte(out), &inv); uerr != nil {
		return Result{Status: Fail,
			Evidence: "nova-config inventory answered no inventory JSON: " + oneLine(uerr.Error()),
			Fix:      "nova-config inventory (" + doc + ")"}
	}
	records, host, ok := selfLoops(inv, env.Getenv("NOVA_MACHINE"))
	if !ok {
		return Result{Status: Fail,
			Evidence: "no machine row is this machine: nova-config inventory marks none ansible_connection=local",
			Fix:      "set NOVA_MACHINE to this machine's row name (nova-config machine list names them) (" + doc + ")"}
	}
	var found []unitFile
	for _, dir := range dirs {
		units, rerr := readUnits(env, dir)
		if errors.Is(rerr, fs.ErrNotExist) && len(dirs) > 1 {
			continue // a machine need not use both the login and system scope
		}
		if rerr != nil {
			return Result{Status: Fail,
				Evidence: "the unit directory " + dir + " could not be read: " + oneLine(rerr.Error()),
				Fix:      "create the service manager's directory or set NOVA_UNITS_DIR (" + doc + ")"}
		}
		found = append(found, units...)
	}
	missing, hand, differ := compareUnits(records, found)
	if len(missing)+len(hand)+len(differ) == 0 {
		return Result{Status: OK,
			Evidence: fmt.Sprintf("host=%s records=%d units=%d: every record has its unit and no unit lacks a record", host, len(records), len(found))}
	}
	var parts []string
	if len(missing) > 0 {
		parts = append(parts, "record without a unit: "+strings.Join(missing, ", "))
	}
	if len(hand) > 0 {
		parts = append(parts, "unit with no record: "+strings.Join(hand, ", "))
	}
	if len(differ) > 0 {
		parts = append(parts, "unit whose command differs: "+strings.Join(differ, ", "))
	}
	return Result{Status: Fail,
		Evidence: "host=" + host + " " + strings.Join(parts, "; "),
		Fix:      "nova-config apply --kind loop --actor <actor>, then ansible-playbook -i ./nova-inventory fleet/loops.yml (" + doc + ")"}
}

// selfLoops is the records of the host this process runs on: the row named by
// NOVA_MACHINE when the inventory has it, else the row marked
// ansible_connection local. ok is false when neither can be found.
func selfLoops(inv inventoryLoops, machine string) ([]loopRecord, string, bool) {
	names := make([]string, 0, len(inv.Meta.Hostvars))
	for n := range inv.Meta.Hostvars {
		names = append(names, n)
	}
	sort.Strings(names)
	if machine != "" {
		if hv, ok := inv.Meta.Hostvars[machine]; ok {
			return hv.Loops, machine, true
		}
	}
	for _, n := range names {
		if inv.Meta.Hostvars[n].Connection == "local" {
			return inv.Meta.Hostvars[n].Loops, n, true
		}
	}
	return nil, "", false
}

// unitsDirs covers both scopes the fleet play installs in. NOVA_UNITS_DIR
// selects one directory for an isolated check.
func unitsDirs(env Env) []string {
	if dir := env.Getenv("NOVA_UNITS_DIR"); dir != "" {
		return []string{dir}
	}
	home := env.Getenv("HOME")
	if runtime.GOOS == "darwin" {
		return []string{filepath.Join(home, "Library", "LaunchAgents"), "/Library/LaunchDaemons"}
	}
	return []string{filepath.Join(home, ".config", "systemd", "user"), "/etc/systemd/system"}
}

// unitFile is one installed unit the check read: the record name its file
// names, the command it runs, and why it could not be read when it could not.
type unitFile struct {
	Name     string
	Args     []string
	Why      string
	FromPlay bool
}

// readUnits reads every loop unit in dir: a launchd plist or a systemd user
// service (a timer is no command and is skipped). A file that does not read or
// parse is kept with its Why, so it is still named.
func readUnits(env Env, dir string) ([]unitFile, error) {
	entries, err := env.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []unitFile
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name, goos, ok := unitLoopName(e.Name())
		if !ok {
			continue
		}
		u := unitFile{Name: name}
		b, rerr := env.ReadFile(filepath.Join(dir, e.Name()))
		if rerr != nil {
			u.Why = "it could not be read: " + oneLine(rerr.Error())
			out = append(out, u)
			continue
		}
		u.FromPlay = strings.Contains(string(b), loopMark)
		args, perr := units.UnitArgs(goos, b)
		if perr != nil {
			u.Why = "it is no unit this tool reads: " + oneLine(perr.Error())
			out = append(out, u)
			continue
		}
		u.Args = args
		out = append(out, u)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// unitLoopName is the record name a unit file name carries: a launchd
// com.nova.loop.<name>.plist, or a systemd nova-loop-<name>.service. A timer
// and every other file is no command unit.
func unitLoopName(fname string) (name, goos string, ok bool) {
	if rest, found := strings.CutPrefix(fname, "com.nova.loop."); found {
		if rest, found := strings.CutSuffix(rest, ".plist"); found {
			return rest, "darwin", true
		}
	}
	if rest, found := strings.CutPrefix(fname, "nova-loop-"); found {
		if rest, found := strings.CutSuffix(rest, ".service"); found {
			return rest, "linux", true
		}
	}
	return "", "", false
}

// compareUnits holds every record to its unit and every unit to a record: a
// record with no unit is missing, a unit no record names is hand, and a unit
// whose command is not its record's differs. Each list is sorted.
func compareUnits(records []loopRecord, found []unitFile) (missing, hand, differ []string) {
	byName := make(map[string]loopRecord, len(records))
	for _, r := range records {
		byName[r.Name] = r
	}
	seen := make(map[string]bool, len(found))
	for _, u := range found {
		seen[u.Name] = true
		r, ok := byName[u.Name]
		if !ok {
			if u.Name == "disk-guard" && u.FromPlay {
				continue // the fleet play adds this virtual row beside loop records
			}
			hand = append(hand, u.Name)
			continue
		}
		if u.Why != "" || !sameCommand(r.Argv, u.Args) {
			differ = append(differ, u.Name)
		}
	}
	for _, r := range records {
		if !seen[r.Name] {
			missing = append(missing, r.Name)
		}
	}
	sort.Strings(missing)
	sort.Strings(hand)
	sort.Strings(differ)
	return missing, hand, differ
}

// sameCommand reports whether the unit runs the record's argv: the record's
// words as the final run of the unit's command, with the executable compared
// by base name (to allow for a nova-secrets wrapper and installed executable
// path) and remaining arguments compared exactly.
func sameCommand(record, args []string) bool {
	if len(record) == 0 || len(args) < len(record) {
		return false
	}
	i := len(args) - len(record)
	if filepath.Base(args[i]) != filepath.Base(record[0]) {
		return false
	}
	for j := 1; j < len(record); j++ {
		if args[i+j] != record[j] {
			return false
		}
	}
	return true
}
