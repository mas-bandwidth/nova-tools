package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/pkg/oneline"
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
	// Machines are the machine views by name: user, seat, slots, runners and roles.
	Machines map[string]View
	// Fleet is the fleet row's view: store, coordinator, redis_port, pg_dsn, bus.
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
// (fleet/tools.yml, fleet/redis.yml). tla is the machines whose row says
// tla=true, the TLC record machines the tools play's tla play installs the
// pinned jar on.
type AnsibleInventory struct {
	Meta AnsibleMeta  `json:"_meta"`
	All  AnsibleGroup `json:"all"`
	// Adopt is the fleet in the order the install play adopts it: the canary
	// first, then every other host, so the play's serial first batch is the
	// canary alone and the other hosts are never touched before it is judged
	// (docs/SPEC-RELEASE.md, "Adopting a build").
	Adopt         AnsibleGroup `json:"adopt"`
	Benches       AnsibleGroup `json:"benches"`
	Coordinator   AnsibleGroup `json:"coordinator"`
	Store         AnsibleGroup `json:"store"`
	StoreDeployer AnsibleGroup `json:"store_deployer"`
	Runners       AnsibleGroup `json:"runners"`
	TLA           AnsibleGroup `json:"tla"`
	// Notes are what the inventory derived or left out rather than refused:
	// a roles field derived as migration 0042 derives it, a host with no beat
	// or a beat missing facts. They are never part of the JSON (ansible reads
	// every top-level key as a group); the verb prints each as a NOTE line on
	// stderr.
	Notes []string `json:"-"`
}

// InventoryLoop is one loop record as a host variable: the loop kind's
// fields, typed (docs/FLEET.md, "Loops"). Log is the view's log, which the
// loop kind derives from the fleet's loops_dir and the name
// (<loops_dir>/<name>.log) and never takes typed.
type InventoryLoop struct {
	Name      string   `json:"name"`
	Argv      []string `json:"argv"`
	Seat      string   `json:"seat"`
	Keys      []string `json:"keys"`
	Every     int      `json:"every"`
	Keepalive bool     `json:"keepalive"`
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
	names := slices.Sorted(maps.Keys(snap.Machines))
	// A hostile or stale Redis write cannot shape the hosts the plays act
	// on: only a row name passes (docs/SPEC-CONFIG.md; security#69 finding 4).
	if len(names) > MaxHosts {
		return nil, fmt.Errorf("%d machines, over the maximum of %d hosts an inventory lists (MaxHosts)", len(names), MaxHosts)
	}
	for _, m := range names {
		if err := checkHostName(m); err != nil {
			return nil, err
		}
	}

	loops, err := hostLoops(snap)
	if err != nil {
		return nil, err
	}

	hostvars := make(map[string]map[string]any, len(names))
	runnerHosts, tlaHosts := []string{}, []string{}
	benchHosts, serviceHosts, coordinatorHosts := []string{}, []string{}, []string{}
	notes := []string{}
	machineKind, _ := Lookup(KindMachine)
	rolesField, _ := machineKind.Field("roles")
	for _, m := range names {
		v := snap.Machines[m]
		rolesRaw, hasRoles := v["roles"]
		if !hasRoles {
			// A row applied before migration 0042 has no roles field: the
			// previous release's store. Its roles are derived as 0042 derives
			// them, and said so; only a row with nothing to derive from (no
			// applied fleet row) is refused.
			if snap.Revs[KindFleet] == 0 {
				return nil, fmt.Errorf("machine %s: registry_roles is missing from applied machine row field roles and cannot be derived: the fleet row was never applied; run: nova-config apply", m)
			}
			rolesRaw = DeriveMachineRoles(m, snap.Fleet)
			notes = append(notes, fmt.Sprintf("machine %s: the applied row has no roles field (applied before migration 0042); derived %s as 0042 does (bench; coordination from the fleet coordinator; services from the fleet store); to store it: nova-config apply --kind machine", m, rolesRaw))
		}
		roles, roleErr := rolesField.Canonical(rolesRaw)
		if roleErr != nil || roles != rolesRaw {
			return nil, fmt.Errorf("machine %s: registry_roles in applied machine row field roles is invalid; want a canonical comma list of %s; correct the config row if needed, then run: nova-config apply --kind machine", m, strings.Join(MachineRoles, ", "))
		}
		for _, role := range strings.Split(roles, ",") {
			switch role {
			case "bench":
				benchHosts = append(benchHosts, m)
			case "services":
				serviceHosts = append(serviceHosts, m)
			case "coordination":
				coordinatorHosts = append(coordinatorHosts, m)
			}
		}
		seat := v["seat"]
		if seat == "" {
			return nil, fmt.Errorf("machine %s: registry_seat is unset in machine row field seat; run: nova-config machine show %s", m, m)
		}
		slots, err := inventoryCount(m, "slots_capacity", "machine row field slots", v["slots"])
		if err != nil {
			return nil, err
		}
		if slots == 0 {
			return nil, fmt.Errorf("machine %s: slots_capacity must be greater than 0 from machine row field slots; run: nova-config machine show %s", m, m)
		}
		// an unset runners is none; one that is not a count is refused by name
		runners := 0
		if v["runners"] != "" {
			if runners, err = inventoryCount(m, "runners", "machine row field runners", v["runners"]); err != nil {
				return nil, err
			}
		}
		// The measured facts are optional: a machine that is down, or one whose
		// beat predates a fact, is listed from its row and the facts it has,
		// never refused (one down host never takes the whole fleet's inventory
		// with it). The play gathers os, arch and cores from its own facts for
		// any host whose beat lacks them (fleet/tools.yml).
		beat := snap.Beats[m]
		if beat == nil {
			beat = &Beat{}
			notes = append(notes, fmt.Sprintf("machine %s: no beat (the machine is down, or its member has never beaten); listed from its row; the play gathers os, arch and cores from its facts", m))
		} else if missing := missingBeatFacts(beat); len(missing) > 0 {
			notes = append(notes, fmt.Sprintf("machine %s: its beat has no %s; listed with the facts it has; the play gathers the rest from its facts", m, strings.Join(missing, ", ")))
		}
		cores, hasCores := 0, beat.Cores != ""
		if hasCores {
			if cores, err = inventoryCount(m, "cores", "beat field ncpu", beat.Cores); err != nil {
				return nil, err
			}
		}
		width, hasWidth := 0, false
		if v["width"] != "" {
			width, err = inventoryCount(m, "slots_share", "machine row field width", v["width"])
			if err != nil {
				return nil, err
			}
			hasWidth = true
		} else if hasCores {
			width, hasWidth = cores/2, true // The machine row's nullable width defaults to half its beat cores.
		}
		if hasWidth && width > slots {
			return nil, fmt.Errorf("machine %s: slots_share %d from machine row field width exceeds slots_capacity %d from machine row field slots; run: nova-config machine show %s", m, width, slots, m)
		}
		// ansible_user is the name ansible reads for the login; nova_seat
		// carries the row's seat under a namespaced name that cannot collide
		// with a play's own variable. An empty value is left out, never
		// emitted as "".
		hv := map[string]any{
			"ansible_host":   m,
			"slots":          slots,
			"slots_capacity": slots,
			"runners":        runners,
			"kind":           KindMachine,
			// a view written before the field existed has no tla: none
			"nova_tla":      v["tla"] == "true",
			"registry_seat": seat,
		}
		if hasWidth {
			hv["slots_share"] = width
		}
		if beat.OS != "" {
			hv["bench_os"], hv["nova_os"] = beat.OS, beat.OS
		}
		if beat.Arch != "" {
			hv["nova_arch"] = beat.Arch
		}
		if hasCores {
			hv["cores"] = cores
		}
		if u := v["user"]; u != "" {
			hv["ansible_user"] = u
		}
		if seat := v["seat"]; seat != "" {
			hv["nova_seat"] = seat
		}
		if loops != nil {
			hv[loopField] = loops[m]
		}
		if store := snap.Fleet["store"]; store != "" {
			hv["nova_redis_addr"] = store + ":" + strconv.Itoa(redisPort)
		}
		// An empty bus is left out. The loop templates render NOVA_BUS_REDIS
		// only when nova_bus_addr is defined and not empty, so a fleet with
		// no bus still renders the units as it does today.
		if bus := snap.Fleet["bus"]; bus != "" {
			hv["nova_bus_addr"] = bus
		}
		hv["nova_redis_port"] = redisPort
		hv["nova_pg_dsn"] = snap.Fleet["pg_dsn"]
		if localHost != "" && m == localHost {
			hv["ansible_connection"] = "local"
		}
		hv["registry_roles"] = roles
		hostvars[m] = hv
		if runners > 0 {
			runnerHosts = append(runnerHosts, m)
		}
		if v["tla"] == "true" {
			tlaHosts = append(tlaHosts, m)
		}
	}
	if err := matchRoleReference("services", serviceHosts, "store", snap.Fleet["store"]); err != nil {
		return nil, err
	}
	if err := matchRoleReference("coordination", coordinatorHosts, "coordinator", snap.Fleet["coordinator"]); err != nil {
		return nil, err
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
	maps.Copy(revs, snap.Revs)
	vars["nova_config_rev"] = revs
	return &AnsibleInventory{
		Meta:          AnsibleMeta{Hostvars: hostvars},
		All:           AnsibleGroup{Hosts: append([]string{}, names...), Vars: vars},
		Adopt:         AnsibleGroup{Hosts: adoptOrder(names, snap.Fleet["coordinator"])},
		Benches:       AnsibleGroup{Hosts: benchHosts},
		Coordinator:   AnsibleGroup{Hosts: coordinatorHosts},
		Store:         AnsibleGroup{Hosts: serviceHosts},
		StoreDeployer: AnsibleGroup{Hosts: one(snap.Fleet["coordinator"])},
		Runners:       AnsibleGroup{Hosts: runnerHosts},
		TLA:           AnsibleGroup{Hosts: tlaHosts},
		Notes:         notes,
	}, nil
}

// missingBeatFacts names the facts a beat lacks, in the order the play reads
// them: os, arch, ncpu.
func missingBeatFacts(b *Beat) []string {
	missing := []string{}
	if b.OS == "" {
		missing = append(missing, "os")
	}
	if b.Arch == "" {
		missing = append(missing, "arch")
	}
	if b.Cores == "" {
		missing = append(missing, "ncpu")
	}
	return missing
}

// matchRoleReference keeps the explicit inventory role and fleet singleton
// reference in agreement: plays consume the role group while nova_store,
// Redis routing and adoption consume the fleet row (docs/FLEET.md).
func matchRoleReference(role string, hosts []string, field, reference string) error {
	want := []string{}
	if reference != "" {
		want = []string{reference}
	}
	if slices.Equal(hosts, want) {
		return nil
	}
	return fmt.Errorf("inventory: machine roles role %s selects hosts %v, but fleet row field %s selects %q; align them with nova-config machine set <machine> --roles <roles> and nova-config fleet set --%s <machine>", role, hosts, field, reference, field)
}

// inventoryCount reads a non-negative integer from the applied row or beat,
// naming both the host variable and its source when the inventory cannot
// provide the value a play would otherwise discover as undefined.
func inventoryCount(host, variable, source, raw string) (int, error) {
	if raw == "" {
		return 0, fmt.Errorf("machine %s: %s is unset in %s; run: nova-config machine show %s", host, variable, source, host)
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("machine %s: %s from %s is not a non-negative integer; run: nova-config machine show %s", host, variable, source, host)
	}
	return n, nil
}

// adoptOrder is the fleet in the order the install play adopts it: the canary
// (the first host of the fleet in inventory order that is not the coordinator)
// first, then every other host, the coordinator among them. The install play
// runs on this group, so its serial first batch is the canary alone
// (docs/SPEC-RELEASE.md, "Adopting a build").
func adoptOrder(names []string, coordinator string) []string {
	canary := ""
	for _, m := range names {
		if m != coordinator {
			canary = m
			break
		}
	}
	out := make([]string, 0, len(names))
	if canary != "" {
		out = append(out, canary)
	}
	for _, m := range names {
		if m != canary {
			out = append(out, m)
		}
	}
	return out
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
	for _, n := range slices.Sorted(maps.Keys(snap.Loops)) {
		// the view is read in every form and refused by name (records.go, S2)
		v, err := records.ReadLoop(snap.Loops[n])
		if err != nil {
			return nil, fmt.Errorf("loop %s: %w", n, err)
		}
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

// checkHostName refuses a machine name that is not a row name: the shape
// NamePattern names and at most MaxName bytes. Only a row name shapes the
// hosts the plays act on.
func checkHostName(m string) error {
	if len(m) > MaxName {
		return fmt.Errorf("machine name of %d bytes is over the maximum of %d bytes (MaxName)", len(m), MaxName)
	}
	if !NamePattern.MatchString(m) {
		return fmt.Errorf("machine %q is not a row name (lower-case letters, digits and dashes)", m)
	}
	return nil
}

// parseLoop reads one loop view: argv the JSON text of a list of strings,
// keys a comma list, every decimal text, keepalive and enabled
// "true" or "false", exactly one of every above 0 and keepalive.
func parseLoop(name string, v View) (InventoryLoop, error) {
	bad := func(field, why string) (InventoryLoop, error) {
		return InventoryLoop{}, fmt.Errorf("loop %s: %s %q %s; run: nova-config loop show %s", name, field, v[field], why, name)
	}
	// The name becomes a unit's file name and label: only a row name passes.
	if len(name) > MaxName {
		return InventoryLoop{}, fmt.Errorf("loop name of %d bytes is over the maximum of %d bytes (MaxName); run: nova-config loop list", len(name), MaxName)
	}
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
	if len(l.Keys) > MaxKeys {
		return bad("keys", fmt.Sprintf("names %d secrets, over the maximum of %d (MaxKeys)", len(l.Keys), MaxKeys))
	}
	for _, k := range l.Keys {
		if len(k) > MaxEnvName {
			return bad("keys", fmt.Sprintf("names a secret of %d bytes, over the maximum of %d bytes (MaxEnvName)", len(k), MaxEnvName))
		}
	}
	if len(l.Keys) > 0 && l.Seat == "" {
		return bad("keys", "names secrets and the loop has no seat")
	}
	var err error
	if l.Every, err = strconv.Atoi(orZero(v["every"])); err != nil || l.Every < 0 {
		return bad("every", "is not a count of seconds")
	}
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
	program, ok := memberAt(argv)
	if !ok || program == 0 {
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
// plays with no store (docs/FLEET.md, "A fixture inventory"). Its rows are
// named types, not anonymous structs, so a refusal can say in words what a
// field wants instead of printing the Go type the YAML reader names.
type fixture struct {
	Machines fixtureMachines `yaml:"machines"`
	Fleet    fixtureFleet    `yaml:"fleet"`
	// Loops is a pointer so a fixture without the key is a fleet whose
	// loops were never applied, and `loops: {}` one that runs none.
	Loops *fixtureLoops `yaml:"loops"`
}

// fixtureMachines is the fixture's machines block: a map of machine name to
// the row the inventory reads.
type fixtureMachines map[string]fixtureMachine

// fixtureMachine is one machine the fixture names; OS, Arch and Cores stand
// in for the machine's beat, while Width is the nullable machine-row field.
//
// Roles is a pointer so a machine without the key stands for a row applied
// before migration 0042 (fleet/testdata/inventory-fixture-n-1.yml is one),
// and `roles: ""` for a row that carries no role.
type fixtureMachine struct {
	User    string  `yaml:"user"`
	Seat    string  `yaml:"seat"`
	Slots   int     `yaml:"slots"`
	Runners int     `yaml:"runners"`
	Roles   *string `yaml:"roles"`
	TLA     bool    `yaml:"tla"`
	OS      string  `yaml:"os"`
	Arch    string  `yaml:"arch"`
	Cores   *int    `yaml:"cores"`
	Width   *int    `yaml:"width"`
}

// fixtureFleet is the fixture's one fleet row.
type fixtureFleet struct {
	Store       string `yaml:"store"`
	Coordinator string `yaml:"coordinator"`
	RedisPort   *int   `yaml:"redis_port"`
	PGDSN       string `yaml:"pg_dsn"`
	LoopsDir    string `yaml:"loops_dir"`
}

// fixtureLoops is the fixture's loops block: a map of loop name to loop.
type fixtureLoops map[string]fixtureLoop

// fixtureLoop is one loop the fixture names: the loop kind's fields.
type fixtureLoop struct {
	Machine   string   `yaml:"machine"`
	Argv      []string `yaml:"argv"`
	Seat      string   `yaml:"seat"`
	Keys      []string `yaml:"keys"`
	Every     int      `yaml:"every"`
	Keepalive bool     `yaml:"keepalive"`
	Enabled   *bool    `yaml:"enabled"`
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
		return nil, fmt.Errorf("--fixture %s is not the fixture's shape: %s; want a mapping with machines (each user, seat, slots, runners, roles, tla, os, arch, cores and width), fleet (store, coordinator) and loops (docs/FLEET.md, \"A fixture inventory\"; fleet/testdata/inventory-fixture.yml is one)", path, fixtureShapeError(err))
	}
	snap := &Snapshot{Machines: map[string]View{}, Beats: map[string]*Beat{}, Revs: map[string]int64{}}
	if len(f.Machines) > MaxHosts {
		return nil, fmt.Errorf("--fixture %s: %d machines, over the maximum of %d hosts an inventory lists (MaxHosts)", path, len(f.Machines), MaxHosts)
	}
	for m, r := range f.Machines {
		if err := checkHostName(m); err != nil {
			return nil, fmt.Errorf("--fixture %s: %v", path, err)
		}
		machine := View{"user": r.User, "seat": r.Seat, "slots": strconv.Itoa(r.Slots), "runners": strconv.Itoa(r.Runners), "tla": strconv.FormatBool(r.TLA)}
		// A machine with no roles key is a row applied before migration
		// 0042, as the previous release's store holds it: the field stays
		// absent, and the inventory derives it as 0042 does, with a NOTE.
		if r.Roles != nil {
			machine["roles"] = *r.Roles
		}
		if r.Width != nil {
			machine["width"] = strconv.Itoa(*r.Width)
		}
		snap.Machines[m] = machine
		if r.OS != "" || r.Arch != "" || r.Cores != nil {
			beat := &Beat{OS: r.OS, Arch: r.Arch}
			if r.Cores != nil {
				beat.Cores = strconv.Itoa(*r.Cores)
			}
			snap.Beats[m] = beat
		}
	}
	redisPort := ""
	if f.Fleet.RedisPort != nil {
		redisPort = strconv.Itoa(*f.Fleet.RedisPort)
	}
	snap.Fleet = View{"store": f.Fleet.Store, "coordinator": f.Fleet.Coordinator, "redis_port": redisPort, "pg_dsn": f.Fleet.PGDSN, "loops_dir": f.Fleet.LoopsDir}
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
				"keepalive": strconv.FormatBool(l.Keepalive),
				"enabled":   strconv.FormatBool(enabled), "log": LoopLog(f.Fleet.LoopsDir, n),
			}
		}
	}
	return snap, nil
}

// fixtureShapeWords names, in words, the shape each fixture field reads: what
// the field is, so a refusal never prints the Go type the YAML reader names
// (docs/STANDARD.md, section 3, point 2: a refusal says what the input
// wants).
var fixtureShapeWords = map[string]string{
	"config.fixture":         "the document is a map with machines, fleet and loops",
	"config.fixtureMachines": "machines is a map of name to machine",
	"config.fixtureMachine":  "a machine is a map of user, seat, slots, runners, roles, tla, os, arch, cores and width",
	"config.fixtureFleet":    "fleet is a map of store, coordinator, redis_port, pg_dsn and loops_dir",
	"config.fixtureLoops":    "loops is a map of name to loop",
	"config.fixtureLoop":     "a loop is a map of machine, argv, seat, keys, every, keepalive and enabled",
	"int":                    "a number is wanted",
	"*int":                   "a number is wanted",
	"string":                 "text is wanted",
	"bool":                   "true or false is wanted",
	"*bool":                  "true or false is wanted",
	"[]string":               "a list of text is wanted",
}

// fixtureFields names the fields each fixture type reads, so an unknown
// field's refusal says what that block wants.
var fixtureFields = map[string]string{
	"config.fixture":        "machines, fleet or loops",
	"config.fixtureMachine": "user, seat, slots, runners, roles, tla, os, arch, cores or width",
	"config.fixtureFleet":   "store, coordinator, redis_port, pg_dsn or loops_dir",
	"config.fixtureLoop":    "machine, argv, seat, keys, every, keepalive or enabled",
}

// fixtureShapeError is the YAML reader's refusal in the fixture's own words:
// every problem's line, what the fixture wants there, and what it got. A
// refusal that is not a type error (a syntax error) is passed through, on one
// line and quoted, because its own words are all there is to say.
func fixtureShapeError(err error) string {
	var te *yaml.TypeError
	if !errors.As(err, &te) {
		return oneline.Quote(strings.Join(strings.Fields(err.Error()), " "))
	}
	problems := make([]string, 0, len(te.Errors))
	for _, e := range te.Errors {
		problems = append(problems, fixtureShapeLine(e))
	}
	return strings.Join(problems, "; ")
}

// fixtureShapeLine rewrites one yaml.TypeError line without its Go type:
// "line 2: cannot unmarshal !!seq into map[string]struct{...}" becomes
// "line 2: machines is a map of name to machine, got a list", and an unknown
// field's "... not found in type config.fixture" becomes the field names the
// fixture does read.
func fixtureShapeLine(line string) string {
	where, rest, ok := strings.Cut(line, ": ")
	if !ok {
		return line
	}
	if _, tag, found := strings.Cut(rest, "cannot unmarshal "); found {
		tag, gotType, ok := strings.Cut(tag, " into ")
		if !ok {
			return where + ": " + rest
		}
		yamlTag, _, _ := strings.Cut(tag, " ")
		wants, known := fixtureShapeWords[gotType]
		if !known {
			wants = "another shape is wanted"
		}
		return where + ": " + wants + ", got " + fixtureGot(yamlTag)
	}
	if name, found := strings.CutPrefix(rest, "field "); found {
		if field, gotType, ok := strings.Cut(name, " not found in type "); ok {
			fields, known := fixtureFields[gotType]
			if !known {
				fields = "machines, fleet or loops"
			}
			return where + ": " + field + " is not a fixture field; want " + fields
		}
	}
	return where + ": " + rest
}

// fixtureGot is the YAML value's shape in words, from its tag.
func fixtureGot(tag string) string {
	switch tag {
	case "!!seq":
		return "a list"
	case "!!map":
		return "a map"
	case "!!str":
		return "text"
	case "!!int":
		return "a number"
	case "!!bool":
		return "true or false"
	case "!!null":
		return "nothing"
	}
	return "the wrong shape"
}
