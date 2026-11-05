package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Beat is what a machine last reported of itself, read from Redis: never a
// row, because a measured fact is not configuration. "" is not reported.
type Beat struct{ OS, Arch, Cores string }

type group struct {
	Hosts []string       `json:"hosts"`
	Vars  map[string]any `json:"vars,omitempty"`
}

// inventory is Ansible's dynamic inventory; the field order is the output's.
type inventory struct {
	Meta struct {
		Hostvars map[string]map[string]any `json:"hostvars"`
	} `json:"_meta"`
	All           group `json:"all"`
	Adopt         group `json:"adopt"`
	Benches       group `json:"benches"`
	Coordinator   group `json:"coordinator"`
	Store         group `json:"store"`
	StoreDeployer group `json:"store_deployer"`
	Runners       group `json:"runners"`
	TLA           group `json:"tla"`
}

type invLoop struct {
	Name      string   `json:"name"`
	Argv      []string `json:"argv"`
	Seat      string   `json:"seat"`
	Keys      []string `json:"keys"`
	Every     int      `json:"every"`
	Keepalive bool     `json:"keepalive"`
	Enabled   bool     `json:"enabled"`
	Log       string   `json:"log"`
}

func runInventory(ctx context.Context, args []string, d deps, stdout, stderr io.Writer) error {
	fs, name, _, err := flags("inventory", args, stderr, func(fs *flag.FlagSet) {
		fs.Bool("list", false, "print the whole inventory (the default)")
		fs.String("host", "", "print one machine's variables")
		fs.String("redis", "", "the Redis `host:port` the machines' beats are read from (env NOVA_SPRINT_REDIS, NOVA_REDIS_ADDR)")
	})
	if err != nil {
		return err
	}
	host := str(fs, "host")
	if name != "" || (host != "" && str(fs, "list") == "true") {
		return fmt.Errorf("inventory takes --list or --host <name>")
	}
	st, err := open(ctx, fs, d)
	if err != nil {
		return err
	}
	defer st.Close()
	rows := map[string][]Row{}
	for _, k := range []string{"machine", "fleet", "loop"} {
		if rows[k], err = st.List(ctx, kinds[k]); err != nil {
			return err
		}
	}
	if len(rows["fleet"]) != 1 {
		return refused("the store has no fleet row; it is made by schema.sql")
	}
	revs, err := st.Revs(ctx)
	if err != nil {
		return err
	}
	beats := map[string]*Beat{}
	if addr := firstOf(str(fs, "redis"), d.getenv("NOVA_SPRINT_REDIS"), d.getenv("NOVA_REDIS_ADDR")); addr != "" {
		pe := firstOf(d.getenv("NOVA_SPRINT_REDIS_PASSWORD_ENV"), "NOVA_REDIS_BENCH_PASSWORD")
		names := make([]string, len(rows["machine"]))
		for i, r := range rows["machine"] {
			names[i] = r.Name
		}
		if beats, err = d.beats(ctx, addr, d.getenv("NOVA_SPRINT_REDIS_USER"), d.getenv(pe), names); err != nil {
			return err
		}
	}
	local := d.getenv("NOVA_MACHINE")
	if _, ok := find(rows["machine"], local); local != "" && !ok {
		return refused("NOVA_MACHINE %s names no machine row", local)
	}
	if h, err := d.hostname(); local == "" && err == nil {
		local = strings.ToLower(strings.Split(h, ".")[0])
	}
	inv, notes, err := build(rows["machine"], rows["fleet"][0].F, rows["loop"], beats, revs, local)
	if err != nil {
		return refusal{err.Error()}
	}
	for _, n := range notes {
		fmt.Fprintln(stderr, "NOTE "+n)
	}
	var out any = inv
	if host != "" {
		hv, ok := inv.Meta.Hostvars[host]
		if !ok {
			return refused("no machine row named %s; the machines are %s", host, strings.Join(inv.All.Hosts, ","))
		}
		out = hv
	}
	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout, "%s\n", b)
	return err
}

func firstOf(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// build is the inventory of the rows: every machine in all, grouped by its
// roles, its runners and tla, with its row, its loops and its beat as host
// variables. adopt is every machine with the first that is not the
// coordinator first, the canary a rollout starts on.
func build(machines []Row, fleet map[string]string, loops []Row, beats map[string]*Beat, revs map[string]int64, local string) (*inventory, []string, error) {
	port, err := strconv.Atoi(fleet["redis_port"])
	if err != nil || fleet["pg_dsn"] == "" {
		return nil, nil, fmt.Errorf("fleet: redis_port and pg_dsn are unset; run: nova-config fleet set --redis_port <port> --pg_dsn <dsn> --actor <name>")
	}
	inv := &inventory{}
	inv.Meta.Hostvars = map[string]map[string]any{}
	groups := map[string][]string{"bench": {}, "services": {}, "coordination": {}, "runners": {}, "tla": {}}
	byMachine := map[string][]invLoop{}
	for _, l := range loops {
		il := invLoop{Name: l.Name, Seat: l.F["seat"], Keys: []string{}, Log: fleet["loops_dir"] + "/" + l.Name + ".log"}
		_ = json.Unmarshal([]byte(l.F["argv"]), &il.Argv) // ignored: the row's argv was canonical when written
		il.Argv = withoutSprintRedis(il.Argv)
		if l.F["keys"] != "" {
			il.Keys = strings.Split(l.F["keys"], ",")
		}
		il.Every, _ = strconv.Atoi(l.F["every"])
		il.Keepalive, il.Enabled = l.F["keepalive"] == "true", l.F["enabled"] == "true"
		byMachine[l.F["machine"]] = append(byMachine[l.F["machine"]], il)
	}
	var notes []string
	all := []string{}
	for _, r := range machines {
		m, v := r.Name, r.F
		all = append(all, m)
		for _, role := range strings.Split(v["roles"], ",") {
			if _, ok := groups[role]; ok {
				groups[role] = append(groups[role], m)
			}
		}
		slots, _ := strconv.Atoi(v["slots"])
		runners, _ := strconv.Atoi(v["runners"])
		if slots == 0 || v["seat"] == "" {
			return nil, nil, fmt.Errorf("machine %s: slots must be above 0 and seat set; run: nova-config machine show %s", m, m)
		}
		hv := map[string]any{
			"ansible_host": m, "kind": "machine", "slots": slots, "slots_capacity": slots, "runners": runners,
			"nova_tla": v["tla"] == "true", "registry_seat": v["seat"], "nova_seat": v["seat"], "registry_roles": v["roles"],
			"nova_redis_port": port, "nova_pg_dsn": fleet["pg_dsn"], "nova_loops": append([]invLoop{}, byMachine[m]...),
		}
		b := beats[m]
		if b == nil {
			b = &Beat{}
			notes = append(notes, fmt.Sprintf("machine %s: no beat (the machine is down, or its member has never beaten); listed from its row; the play gathers os, arch and cores from its facts", m))
		} else if missing := missingFacts(b); len(missing) > 0 {
			notes = append(notes, fmt.Sprintf("machine %s: its beat has no %s; listed with the facts it has; the play gathers the rest from its facts", m, strings.Join(missing, ", ")))
		}
		cores, _ := strconv.Atoi(b.Cores)
		if b.Cores != "" {
			hv["cores"] = cores
		}
		if w, err := strconv.Atoi(v["width"]); err == nil {
			hv["slots_share"] = w
		} else if b.Cores != "" {
			hv["slots_share"] = cores / 2 // an unset width is half the cores
		}
		if w, ok := hv["slots_share"].(int); ok && w > slots {
			return nil, nil, fmt.Errorf("machine %s: width %d exceeds slots %d; run: nova-config machine show %s", m, w, slots, m)
		}
		set := map[string]string{"bench_os": b.OS, "nova_os": b.OS, "nova_arch": b.Arch, "ansible_user": v["user"], "nova_bus_addr": fleet["bus"]}
		if fleet["store"] != "" {
			set["nova_redis_addr"] = fleet["store"] + ":" + strconv.Itoa(port)
		}
		for k, val := range set {
			if val != "" {
				hv[k] = val
			}
		}
		if m == local {
			hv["ansible_connection"] = "local"
		}
		inv.Meta.Hostvars[m] = hv
		if runners > 0 {
			groups["runners"] = append(groups["runners"], m)
		}
		if v["tla"] == "true" {
			groups["tla"] = append(groups["tla"], m)
		}
	}
	for role, ref := range map[string]string{"services": "store", "coordination": "coordinator"} {
		if want := one(fleet[ref]); !slices.Equal(groups[role], want) {
			return nil, nil, fmt.Errorf("machines with role %s are %v, but the fleet row's %s is %q; align them with nova-config machine set <machine> --roles <roles> or nova-config fleet set --%s <machine>", role, groups[role], ref, fleet[ref], ref)
		}
	}
	vars := map[string]any{"nova_config_rev": revs}
	if fleet["store"] != "" {
		vars["nova_store"] = fleet["store"]
	}
	inv.All = group{Hosts: all, Vars: vars}
	inv.Adopt = group{Hosts: adoptOrder(all, fleet["coordinator"])}
	inv.Benches, inv.Coordinator, inv.Store = group{Hosts: groups["bench"]}, group{Hosts: groups["coordination"]}, group{Hosts: groups["services"]}
	inv.StoreDeployer, inv.Runners, inv.TLA = group{Hosts: one(fleet["coordinator"])}, group{Hosts: groups["runners"]}, group{Hosts: groups["tla"]}
	return inv, notes, nil
}

func one(name string) []string {
	if name == "" {
		return []string{}
	}
	return []string{name}
}

func missingFacts(b *Beat) []string {
	var missing []string
	for _, f := range [][2]string{{b.OS, "os"}, {b.Arch, "arch"}, {b.Cores, "ncpu"}} {
		if f[0] == "" {
			missing = append(missing, f[1])
		}
	}
	return missing
}

func adoptOrder(names []string, coordinator string) []string {
	out := []string{}
	for _, m := range names {
		if m != coordinator {
			out = append(out, m)
			break
		}
	}
	for _, m := range names {
		if len(out) == 0 || m != out[0] {
			out = append(out, m)
		}
	}
	return out
}

// withoutSprintRedis drops the NOVA_SPRINT_REDIS=... words of an env-prefixed
// nova-swarm member's argv, as the loop units have always been written: a
// member reaches the store through its server, never by that address.
func withoutSprintRedis(argv []string) []string {
	at := 1
	for at < len(argv) && strings.Contains(argv[at], "=") {
		at++
	}
	if len(argv) == 0 || filepath.Base(argv[0]) != "env" || at+1 >= len(argv) || filepath.Base(argv[at]) != "nova-swarm" || argv[at+1] != "member" {
		return argv
	}
	out := []string{argv[0]}
	for _, w := range argv[1:at] {
		if !strings.HasPrefix(w, "NOVA_SPRINT_REDIS=") {
			out = append(out, w)
		}
	}
	return append(out, argv[at:]...)
}

// mergeBeat is a machine's beat from its two records: the member's JSON
// record (at, cores) and the bench hash (os, arch, ncpu, at in milliseconds).
// The member's cores win; an unreadable record counts as none.
func mergeBeat(bench map[string]string, member string) *Beat {
	var mb struct {
		At    time.Time `json:"at"`
		Cores int       `json:"cores"`
	}
	hasMember := member != "" && json.Unmarshal([]byte(member), &mb) == nil && (mb.Cores > 0 || !mb.At.IsZero())
	if at, ok := bench["at"]; ok {
		if _, err := strconv.ParseInt(at, 10, 64); err != nil {
			bench = nil
		}
	}
	if len(bench) == 0 && !hasMember {
		return nil
	}
	b := &Beat{OS: bench["os"], Arch: bench["arch"], Cores: bench["ncpu"]}
	if hasMember && mb.Cores > 0 {
		b.Cores = strconv.Itoa(mb.Cores)
	}
	return b
}
