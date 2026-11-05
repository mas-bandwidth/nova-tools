package main

import (
	"cmp"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
)

// loopLogDir is where a loop's log goes when its row names no log.
const loopLogDir = "~/.local/state/nova/loops"

type group struct {
	Hosts []string `json:"hosts"`
}

// inventory is Ansible's dynamic inventory; the field order is the output's.
type inventory struct {
	Meta struct {
		Hostvars map[string]map[string]any `json:"hostvars"`
	} `json:"_meta"`
	All      group `json:"all"`
	Bench    group `json:"bench"`
	Runner   group `json:"runner"`
	Services group `json:"services"`
}

type invLoop struct {
	Name      string            `json:"name"`
	Argv      []string          `json:"argv"`
	Seat      string            `json:"seat"`
	Keys      []string          `json:"keys"`
	Every     int               `json:"every"`
	Keepalive bool              `json:"keepalive"`
	Enabled   bool              `json:"enabled"`
	Env       map[string]string `json:"env"`
	Log       string            `json:"log"`
}

// inventoryHint is what a usage error of inventory names.
const inventoryHint = "inventory flags: --list, --host <name>, --pg <dsn>"

func runInventory(ctx context.Context, args []string, d deps, stdout io.Writer) error {
	fs, words, err := flags("inventory", inventoryHint, args, func(fs *flag.FlagSet) {
		fs.Bool("list", false, "print the whole inventory (the default)")
		fs.String("host", "", "print one machine's variables")
	})
	if err != nil {
		return err
	}
	host := str(fs, "host")
	if len(words) > 0 || (host != "" && str(fs, "list") == "true") {
		return usageError{"inventory", "takes --list or --host <name>, not both", inventoryHint}
	}
	st, err := open(ctx, fs, d)
	if err != nil {
		return err
	}
	defer st.Close()
	rows := map[string][]Row{}
	for _, k := range []string{"machine", "loop"} {
		if rows[k], err = st.List(ctx, kinds[k]); err != nil {
			return err
		}
	}
	local := d.getenv("NOVA_MACHINE")
	if local != "" && !slices.ContainsFunc(rows["machine"], func(r Row) bool { return r.Name == local }) {
		return refused("NOVA_MACHINE %s names no machine row", local)
	}
	if h, err := d.hostname(); local == "" && err == nil {
		local = strings.ToLower(strings.Split(h, ".")[0])
	}
	inv := build(rows["machine"], rows["loop"], local)
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

// build is the inventory of the rows: every machine in all, grouped by its
// roles and its runners, with its row facts and its loops as host variables.
// A fact the row leaves empty is left out.
func build(machines []Row, loops []Row, local string) *inventory {
	inv := &inventory{}
	inv.Meta.Hostvars = map[string]map[string]any{}
	byMachine := map[string][]invLoop{}
	for _, l := range loops {
		il := invLoop{Name: l.Name, Seat: l.F["seat"], Keys: []string{}, Env: map[string]string{}, Log: cmp.Or(l.F["log"], loopLogDir+"/"+l.Name+".log")}
		_ = json.Unmarshal([]byte(l.F["argv"]), &il.Argv) // ignored: the row's argv was canonical when written
		_ = json.Unmarshal([]byte(l.F["env"]), &il.Env)   // ignored: likewise
		if l.F["keys"] != "" {
			il.Keys = strings.Split(l.F["keys"], ",")
		}
		il.Every, _ = strconv.Atoi(l.F["every"])
		il.Keepalive, il.Enabled = l.F["keepalive"] == "true", l.F["enabled"] == "true"
		byMachine[l.F["machine"]] = append(byMachine[l.F["machine"]], il)
	}
	groups := map[string][]string{"bench": {}, "runner": {}, "services": {}}
	all := []string{}
	for _, r := range machines {
		m, v := r.Name, r.F
		all = append(all, m)
		runners, _ := strconv.Atoi(v["runners"])
		hv := map[string]any{"ansible_host": m, "runners": runners, "nova_loops": append([]invLoop{}, byMachine[m]...)}
		if cores, err := strconv.Atoi(v["cores"]); err == nil {
			hv["cores"] = cores
		}
		for k, val := range map[string]string{"ansible_host": v["address"], "ansible_user": v["user"], "nova_os": v["os"], "nova_arch": v["arch"]} {
			if val != "" {
				hv[k] = val
			}
		}
		if m == local {
			hv["ansible_connection"] = "local"
		}
		inv.Meta.Hostvars[m] = hv
		for _, role := range strings.Split(v["roles"], ",") {
			if _, ok := groups[role]; ok {
				groups[role] = append(groups[role], m)
			}
		}
		if runners > 0 && !slices.Contains(groups["runner"], m) {
			groups["runner"] = append(groups["runner"], m)
		}
	}
	inv.All = group{Hosts: all}
	inv.Bench, inv.Runner, inv.Services = group{Hosts: groups["bench"]}, group{Hosts: groups["runner"]}, group{Hosts: groups["services"]}
	return inv
}
