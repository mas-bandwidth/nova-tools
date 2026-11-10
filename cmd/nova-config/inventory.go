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

// serviceLogDir is where a service's log goes when its row names no log.
const serviceLogDir = "~/.local/state/nova/services"

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

// unsafeText is free text a row carries (argv, env values, log), printed as
// Ansible's {"__ansible_unsafe": text} so it is data and never a template:
// ansible-core would otherwise render a ${#X}, {{ or {# in it and fail the play.
type unsafeText string

func (u unsafeText) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]string{"__ansible_unsafe": string(u)})
}

type invService struct {
	Name      string                `json:"name"`
	Argv      []unsafeText          `json:"argv"`
	Seat      string                `json:"seat"`
	Keys      []string              `json:"keys"`
	Every     int                   `json:"every"`
	Keepalive bool                  `json:"keepalive"`
	Enabled   bool                  `json:"enabled"`
	Env       map[string]unsafeText `json:"env"`
	Log       unsafeText            `json:"log"`
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
	for _, k := range []string{"machine", "service"} {
		if rows[k], err = st.List(ctx, kinds[k]); err != nil {
			return err
		}
	}
	local := ""
	if h, err := d.hostname(); err == nil {
		local = strings.ToLower(strings.Split(h, ".")[0])
	}
	inv, err := build(rows["machine"], rows["service"], local)
	if err != nil {
		return err
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

// build is the inventory of the rows: every machine in all, grouped by its
// roles and its runners, with its row facts and its services as host variables.
// A fact the row leaves empty is left out.
func build(machines []Row, services []Row, local string) (*inventory, error) {
	inv := &inventory{}
	inv.Meta.Hostvars = map[string]map[string]any{}
	byMachine := map[string][]invService{}
	for _, l := range services {
		for _, f := range []string{"argv", "env", "every"} {
			if _, err := kinds["service"].findField(f).canonical(l.F[f]); err != nil {
				return nil, refused("service %s has corrupt %s (%v); run: nova-config service set %s --%s <value>", l.Name, f, err, l.Name, f)
			}
		}
		il := invService{Name: l.Name, Seat: l.F["seat"], Keys: []string{}, Env: map[string]unsafeText{}, Log: unsafeText(cmp.Or(l.F["log"], serviceLogDir+"/"+l.Name+".log"))}
		_ = json.Unmarshal([]byte(l.F["argv"]), &il.Argv) // ignored: checked above
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
		hv := map[string]any{"ansible_host": m, "runners": runners, "nova_services": append([]invService{}, byMachine[m]...)}
		if cores, err := strconv.Atoi(v["cores"]); err == nil {
			hv["cores"] = cores
		}
		for k, val := range map[string]string{"ansible_host": v["address"], "ansible_user": v["user"], "nova_os": v["os"], "nova_arch": v["arch"], "nova_public": v["public"]} {
			if val != "" {
				hv[k] = val
			}
		}
		for _, f := range []string{"windows_runners", "runner_env"} {
			if _, err := kinds["machine"].findField(f).canonical(v[f]); err != nil {
				return nil, refused("machine %s has corrupt %s (%v); run: nova-config machine set %s --%s <value>", m, f, err, m, f)
			}
		}
		if v["windows_runners"] != "" {
			hv["nova_windows_runners"], _ = parseWinRunners(v["windows_runners"]) // ignored: checked above
		}
		if env := map[string]unsafeText{}; v["runner_env"] != "" && v["runner_env"] != "{}" {
			_ = json.Unmarshal([]byte(v["runner_env"]), &env) // ignored: likewise
			hv["nova_runner_env"] = env
		}
		if m == local {
			hv["ansible_connection"] = "local"
		}
		// a Mac: the system python has the system certificate bundle; a discovered Homebrew python may not (get_url fails)
		if v["os"] == "darwin" {
			hv["ansible_python_interpreter"] = "/usr/bin/python3"
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
	return inv, nil
}
