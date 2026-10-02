package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"gopkg.in/yaml.v3"
)

// The inventory is the applied state, read from Redis: the machine rows
// apply wrote (machines, machine:<m>, machine:<m>:ceiling), the fleet row
// (one fleet:<field> key per descriptor field), the loop rows (LoopsKey,
// LoopKey), each
// machine's measured facts (BeatKey) and config:decl's revisions. It reads
// no Postgres: what the plays converge a fleet to is what apply put where the
// running tools read it (docs/SPEC-CONFIG.md, "Declared and measured";
// docs/FLEET.md). config:decl's rev:loop is present once the loop kind's
// apply has run, and only then does the inventory carry nova_loops (a fleet
// whose loops were never applied is not a fleet with no loops).
const loopField = "nova_loops"

// Snapshot is the applied state the inventory is built from: one read of
// the Redis view (RedisApplier.Snapshot), or a fixture file (LoadFixture).
type Snapshot struct {
	// Machines are the machine views by name: user, seat, slots, runners.
	Machines map[string]View
	// Fleet is the fleet row's view: store, coordinator, redis_port, pg_dsn.
	Fleet View
	// Loops are the loop views by name, nil when the loop kind was never
	// applied (no rev:loop in config:decl).
	Loops map[string]View
	// Beats are the machines' measured facts; a machine with no beat is
	// absent.
	Beats map[string]*Beat
	// Revs are config:decl's revisions by kind, 0 when a kind was never
	// applied.
	Revs map[string]int64
}

// AnsibleGroup is one group in an Ansible JSON inventory.
type AnsibleGroup struct {
	Hosts []string       `json:"hosts"`
	Vars  map[string]any `json:"vars,omitempty"`
}

// AnsibleMeta holds host-specific variables in an Ansible JSON inventory.
type AnsibleMeta struct {
	Hostvars map[string]map[string]any `json:"hostvars"`
}

// AnsibleInventory is the standard Ansible dynamic inventory JSON
// representation. store_deployer is the coordinator machine: it holds the
// seat that loads the function library and the ACL onto the store
// (fleet/tools.yml, fleet/redis.yml).
type AnsibleInventory struct {
	Meta          AnsibleMeta  `json:"_meta"`
	All           AnsibleGroup `json:"all"`
	Benches       AnsibleGroup `json:"benches"`
	Coordinator   AnsibleGroup `json:"coordinator"`
	Store         AnsibleGroup `json:"store"`
	StoreDeployer AnsibleGroup `json:"store_deployer"`
	Runners       AnsibleGroup `json:"runners"`
}

// InventoryLoop is one loop record as a host variable: the loop kind's
// fields, typed (docs/FLEET.md, "Loops"). Log is the view's log, which the
// loop kind derives from the name (~/nova-bench/loops/<name>.log) and never
// takes typed.
type InventoryLoop struct {
	Name      string   `json:"name"`
	Argv      []string `json:"argv"`
	Seat      string   `json:"seat"`
	Keys      []string `json:"keys"`
	Every     int      `json:"every"`
	Keepalive bool     `json:"keepalive"`
	Width     int      `json:"width"`
	Enabled   bool     `json:"enabled"`
	Log       string   `json:"log"`
}

// BuildInventory builds the inventory from one snapshot of the applied
// state. When localHost matches a machine name, that host's variables
// include ansible_connection=local so the machine running the command
// reaches itself without ssh. A loop whose view does not parse, or names a
// machine with no row, is an error naming it: the plays never guess at a
// unit.
func BuildInventory(snap *Snapshot, localHost string) (*AnsibleInventory, error) {
	redisPort, err := fleetRedisPort(snap.Fleet)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(snap.Machines))
	for m := range snap.Machines {
		names = append(names, m)
	}
	sort.Strings(names)

	loops, err := hostLoops(snap)
	if err != nil {
		return nil, err
	}

	hostvars := make(map[string]map[string]any, len(names))
	var runnerHosts []string
	for _, m := range names {
		v := snap.Machines[m]
		slots, _ := strconv.Atoi(v["slots"])
		runners, _ := strconv.Atoi(v["runners"])
		// ansible_user is the name ansible reads for the login; nova_seat
		// carries the row's seat under a namespaced name that cannot collide
		// with a play's own variable. An empty value is left out, never
		// emitted as "".
		hv := map[string]any{
			"ansible_host": m,
			"slots":        slots,
			"runners":      runners,
			"kind":         KindMachine,
		}
		if u := v["user"]; u != "" {
			hv["ansible_user"] = u
		}
		if seat := v["seat"]; seat != "" {
			hv["nova_seat"] = seat
		}
		// The measured platform, when the machine's beat carries it; the
		// plays read the gathered facts for a machine without one.
		if b := snap.Beats[m]; b != nil && b.OS != "" && b.Arch != "" {
			hv["nova_os"], hv["nova_arch"] = b.OS, b.Arch
		}
		if loops != nil {
			hv[loopField] = loops[m]
		}
		if store := snap.Fleet["store"]; store != "" {
			hv["nova_redis_addr"] = store + ":" + strconv.Itoa(redisPort)
		}
		hv["nova_redis_port"] = redisPort
		hv["nova_pg_dsn"] = snap.Fleet["pg_dsn"]
		if localHost != "" && m == localHost {
			hv["ansible_connection"] = "local"
		}
		hostvars[m] = hv
		if runners > 0 {
			runnerHosts = append(runnerHosts, m)
		}
	}

	one := func(name string) []string {
		if name == "" {
			return []string{}
		}
		return []string{name}
	}
	vars := map[string]any{}
	if s := snap.Fleet["store"]; s != "" {
		vars["nova_store"] = s
	}
	revs := map[string]int64{}
	for k, r := range snap.Revs {
		revs[k] = r
	}
	vars["nova_config_rev"] = revs
	if runnerHosts == nil {
		runnerHosts = []string{}
	}
	return &AnsibleInventory{
		Meta:          AnsibleMeta{Hostvars: hostvars},
		All:           AnsibleGroup{Hosts: append([]string{}, names...), Vars: vars},
		Benches:       AnsibleGroup{Hosts: append([]string{}, names...)},
		Coordinator:   AnsibleGroup{Hosts: one(snap.Fleet["coordinator"])},
		Store:         AnsibleGroup{Hosts: one(snap.Fleet["store"])},
		StoreDeployer: AnsibleGroup{Hosts: one(snap.Fleet["coordinator"])},
		Runners:       AnsibleGroup{Hosts: runnerHosts},
	}, nil
}

func fleetRedisPort(fleet View) (int, error) {
	if err := ValidateFleetEndpoints(fleet); err != nil {
		return 0, err
	}
	return strconv.Atoi(fleet["redis_port"])
}

// ValidateFleetEndpoints refuses incomplete fleet endpoints before apply
// writes or inventory rewrites loop argv (docs/SPEC-CONFIG.md, "Apply").
func ValidateFleetEndpoints(fleet View) error {
	var missing []string
	for _, field := range []string{"redis_port", "pg_dsn"} {
		if fleet[field] == "" {
			missing = append(missing, field)
		}
	}
	const remedy = "nova-config fleet set --redis_port <port> --pg_dsn <dsn> --as <actor>, then nova-config apply --kind fleet --as <actor>"
	if len(missing) > 0 {
		return fmt.Errorf("fleet: endpoints are unset: %s; run: %s", strings.Join(missing, ", "), remedy)
	}
	if err := checkFleet(Row{Fields: fleet}); err != nil {
		return fmt.Errorf("fleet: %w; run: %s", err, remedy)
	}
	return nil
}

// hostLoops is every machine's loops, sorted by name, every machine present
// with an empty list when it runs none; nil when the loop kind was never
// applied.
func hostLoops(snap *Snapshot) (map[string][]InventoryLoop, error) {
	if snap.Loops == nil {
		return nil, nil
	}
	out := make(map[string][]InventoryLoop, len(snap.Machines))
	for m := range snap.Machines {
		out[m] = []InventoryLoop{}
	}
	names := make([]string, 0, len(snap.Loops))
	for n := range snap.Loops {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		v := snap.Loops[n]
		m := v["machine"]
		if _, ok := out[m]; !ok {
			return nil, fmt.Errorf("loop %s names machine %q, which has no machine row; run: nova-config apply", n, m)
		}
		l, err := parseLoop(n, v)
		if err != nil {
			return nil, err
		}
		out[m] = append(out[m], l)
	}
	return out, nil
}

// parseLoop reads one loop view: argv the JSON text of a list of strings,
// keys a comma list, every and width decimal text, keepalive and enabled
// "true" or "false", exactly one of every above 0 and keepalive.
func parseLoop(name string, v View) (InventoryLoop, error) {
	bad := func(field, why string) (InventoryLoop, error) {
		return InventoryLoop{}, fmt.Errorf("loop %s: %s %q %s; run: nova-config loop show %s", name, field, v[field], why, name)
	}
	// The name becomes a unit's file name and label: only a row name passes.
	if !NamePattern.MatchString(name) {
		return InventoryLoop{}, fmt.Errorf("loop %q is not a row name (lower-case letters, digits and dashes); run: nova-config loop list", name)
	}
	l := InventoryLoop{Name: name, Seat: v["seat"], Log: v["log"], Keys: []string{}}
	if l.Log == "" {
		return bad("log", "is empty; the loop kind's apply writes it")
	}
	if err := json.Unmarshal([]byte(v["argv"]), &l.Argv); err != nil || len(l.Argv) == 0 {
		return bad("argv", "is not a JSON list of at least one string")
	}
	l.Argv = memberArgv(l.Argv)
	for _, k := range strings.Split(v["keys"], ",") {
		if k = strings.TrimSpace(k); k != "" {
			l.Keys = append(l.Keys, k)
		}
	}
	if len(l.Keys) > 0 && l.Seat == "" {
		return bad("keys", "names secrets and the loop has no seat")
	}
	var err error
	if l.Every, err = strconv.Atoi(orZero(v["every"])); err != nil || l.Every < 0 {
		return bad("every", "is not a count of seconds")
	}
	if l.Width, err = strconv.Atoi(orZero(v["width"])); err != nil || l.Width < 0 {
		return bad("width", "is not a count")
	}
	// the unit runs the command with the row's width (LoopCommand)
	l.Argv = LoopCommand(l.Argv, l.Width)
	if l.Keepalive, err = strconv.ParseBool(orFalse(v["keepalive"])); err != nil {
		return bad("keepalive", "is not true or false")
	}
	enabled := v["enabled"]
	if enabled == "" {
		enabled = "true"
	}
	if l.Enabled, err = strconv.ParseBool(enabled); err != nil {
		return bad("enabled", "is not true or false")
	}
	if (l.Every > 0) == l.Keepalive {
		return bad("every", "and keepalive disagree: a loop runs every n seconds or is kept alive, exactly one")
	}
	return l, nil
}

// memberArgv removes a persisted endpoint assignment from the /usr/bin/env
// prefix of a nova-swarm member. The fleet row supplies that value to the
// rendered unit environment, while every other assignment and argv word stays
// byte-for-byte the row's. A non-member loop is untouched.
func memberArgv(argv []string) []string {
	if len(argv) < 3 || filepath.Base(argv[0]) != "env" {
		return argv
	}
	program := 1
	for program < len(argv) && strings.Contains(argv[program], "=") {
		program++
	}
	if program+1 >= len(argv) || filepath.Base(argv[program]) != "nova-swarm" || argv[program+1] != "member" {
		return argv
	}
	out := make([]string, 0, len(argv))
	out = append(out, argv[0])
	for _, word := range argv[1:program] {
		if !strings.HasPrefix(word, "NOVA_SPRINT_REDIS=") {
			out = append(out, word)
		}
	}
	return append(out, argv[program:]...)
}

func orZero(s string) string {
	if s == "" {
		return "0"
	}
	return s
}

func orFalse(s string) string {
	if s == "" {
		return "false"
	}
	return s
}

// JSON serializes the inventory into indented JSON format.
func (inv *AnsibleInventory) JSON() ([]byte, error) {
	return json.MarshalIndent(inv, "", "  ")
}

// Has reports whether a machine row has exactly this name.
func (inv *AnsibleInventory) Has(name string) bool {
	_, ok := inv.Meta.Hostvars[name]
	return ok
}

// UnknownHostError is HostJSON's refusal: no machine row has the name. Known
// is every machine name the inventory holds, sorted.
type UnknownHostError struct {
	Name  string
	Known []string
}

func (e *UnknownHostError) Error() string {
	return fmt.Sprintf("no machine row named %s", e.Name)
}

// HostJSON serializes the variables of one host into indented JSON format. A
// name the inventory does not hold is an *UnknownHostError, never an empty
// object.
func (inv *AnsibleInventory) HostJSON(name string) ([]byte, error) {
	hv, ok := inv.Meta.Hostvars[name]
	if !ok {
		known := make([]string, 0, len(inv.All.Hosts))
		known = append(known, inv.All.Hosts...)
		return nil, &UnknownHostError{Name: name, Known: known}
	}
	return json.MarshalIndent(hv, "", "  ")
}

// fixture is the file LoadFixture reads (YAML, or JSON, which YAML reads):
// the applied state written by hand, for the plays' tests and for trying the
// plays with no store (docs/FLEET.md, "A fixture inventory").
type fixture struct {
	Machines map[string]struct {
		User    string `yaml:"user"`
		Seat    string `yaml:"seat"`
		Slots   int    `yaml:"slots"`
		Runners int    `yaml:"runners"`
		// OS and Arch stand in for the machine's beat.
		OS   string `yaml:"os"`
		Arch string `yaml:"arch"`
	} `yaml:"machines"`
	Fleet struct {
		Store       string `yaml:"store"`
		Coordinator string `yaml:"coordinator"`
		RedisPort   *int   `yaml:"redis_port"`
		PGDSN       string `yaml:"pg_dsn"`
	} `yaml:"fleet"`
	// Loops is a pointer so a fixture without the key is a fleet whose
	// loops were never applied, and `loops: {}` one that runs none.
	Loops *map[string]struct {
		Machine   string   `yaml:"machine"`
		Argv      []string `yaml:"argv"`
		Seat      string   `yaml:"seat"`
		Keys      []string `yaml:"keys"`
		Every     int      `yaml:"every"`
		Keepalive bool     `yaml:"keepalive"`
		Width     int      `yaml:"width"`
		Enabled   *bool    `yaml:"enabled"`
	} `yaml:"loops"`
}

// MaxFixtureBytes bounds a fixture file, read whole before it is parsed.
const MaxFixtureBytes = 1 << 20

// LoadFixture reads a fixture file into the snapshot the Redis view would
// give for the same rows: the loop fields in their canonical text (argv
// compact JSON, keys a sorted comma list, booleans true or false).
func LoadFixture(path string) (*Snapshot, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("--fixture: %w", err)
	}
	if fi.Size() > MaxFixtureBytes {
		return nil, fmt.Errorf("--fixture %s is %d bytes, over %d", path, fi.Size(), MaxFixtureBytes)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("--fixture: %w", err)
	}
	var f fixture
	dec := yaml.NewDecoder(strings.NewReader(string(raw)))
	dec.KnownFields(true)
	if err := dec.Decode(&f); err != nil {
		return nil, fmt.Errorf("--fixture %s is not the fixture's shape: %s; want a mapping with machines (each user, seat, slots, runners, os, arch), fleet (store, coordinator) and loops (docs/FLEET.md, \"A fixture inventory\"; fleet/testdata/inventory-fixture.yml is one)", path, oneline.Quote(strings.Join(strings.Fields(err.Error()), " ")))
	}
	snap := &Snapshot{Machines: map[string]View{}, Beats: map[string]*Beat{}, Revs: map[string]int64{}}
	for m, r := range f.Machines {
		if !NamePattern.MatchString(m) {
			return nil, fmt.Errorf("--fixture %s: machine %q is not a row name (lower-case letters, digits and dashes)", path, m)
		}
		snap.Machines[m] = View{"user": r.User, "seat": r.Seat, "slots": strconv.Itoa(r.Slots), "runners": strconv.Itoa(r.Runners)}
		if r.OS != "" || r.Arch != "" {
			snap.Beats[m] = &Beat{OS: r.OS, Arch: r.Arch}
		}
	}
	redisPort := ""
	if f.Fleet.RedisPort != nil {
		redisPort = strconv.Itoa(*f.Fleet.RedisPort)
	}
	snap.Fleet = View{"store": f.Fleet.Store, "coordinator": f.Fleet.Coordinator, "redis_port": redisPort, "pg_dsn": f.Fleet.PGDSN}
	snap.Revs[KindMachine], snap.Revs[KindFleet] = 1, 1
	if f.Loops != nil {
		snap.Loops = map[string]View{}
		snap.Revs[KindLoop] = 1
		for n, l := range *f.Loops {
			argv, err := json.Marshal(l.Argv)
			if err != nil {
				return nil, fmt.Errorf("--fixture %s: loop %s argv: %w", path, n, err)
			}
			keys := append([]string{}, l.Keys...)
			sort.Strings(keys)
			enabled := l.Enabled == nil || *l.Enabled
			snap.Loops[n] = View{
				"name": n, "machine": l.Machine, "argv": string(argv), "seat": l.Seat,
				"keys": strings.Join(keys, ","), "every": strconv.Itoa(l.Every),
				"keepalive": strconv.FormatBool(l.Keepalive), "width": strconv.Itoa(l.Width),
				"enabled": strconv.FormatBool(enabled), "log": LoopLog(n),
			}
		}
	}
	return snap, nil
}
