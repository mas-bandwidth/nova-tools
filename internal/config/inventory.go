package config

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
)

// AnsibleGroup is one group in an Ansible JSON inventory.
type AnsibleGroup struct {
	Hosts []string `json:"hosts"`
}

// AnsibleMeta holds host-specific variables in an Ansible JSON inventory.
type AnsibleMeta struct {
	Hostvars map[string]map[string]any `json:"hostvars"`
}

// AnsibleInventory is the standard Ansible dynamic inventory JSON representation.
type AnsibleInventory struct {
	Meta        AnsibleMeta  `json:"_meta"`
	All         AnsibleGroup `json:"all"`
	Benches     AnsibleGroup `json:"benches"`
	Coordinator AnsibleGroup `json:"coordinator"`
	Store       AnsibleGroup `json:"store"`
	Runners     AnsibleGroup `json:"runners"`
}

// BuildInventory reads the machine and fleet rows from the store and builds an
// AnsibleInventory. When localHost matches a machine name, that host's variables
// include ansible_connection=local so the machine running the command reaches itself
// without ssh.
func BuildInventory(ctx context.Context, st Store, localHost string) (*AnsibleInventory, error) {
	machines, fleetRow, err := st.MachinesAndFleet(ctx)
	if err != nil {
		return nil, fmt.Errorf("inventory: read machines and fleet: %w", err)
	}

	hostvars := make(map[string]map[string]any, len(machines))
	var allHosts, benchesHosts, runnerHosts []string

	for _, m := range machines {
		slots, _ := strconv.Atoi(m.Fields["slots"])
		runners, _ := strconv.Atoi(m.Fields["runners"])
		// ansible_user and registry_seat are the two names ansible and the
		// plays read; an empty value is left out, never emitted as "".
		hv := map[string]any{
			"ansible_host": m.Name,
			"slots":        slots,
			"runners":      runners,
			"kind":         KindMachine,
		}
		if u := m.Fields["user"]; u != "" {
			hv["ansible_user"] = u
		}
		if seat := m.Fields["seat"]; seat != "" {
			hv["registry_seat"] = seat
		}
		if localHost != "" && m.Name == localHost {
			hv["ansible_connection"] = "local"
		}
		hostvars[m.Name] = hv
		allHosts = append(allHosts, m.Name)
		benchesHosts = append(benchesHosts, m.Name)
		if runners > 0 {
			runnerHosts = append(runnerHosts, m.Name)
		}
	}

	sort.Strings(allHosts)
	sort.Strings(benchesHosts)
	sort.Strings(runnerHosts)

	var coordHosts []string
	if coord := fleetRow.Fields["coordinator"]; coord != "" {
		coordHosts = append(coordHosts, coord)
	}
	var storeHosts []string
	if store := fleetRow.Fields["store"]; store != "" {
		storeHosts = append(storeHosts, store)
	}

	if allHosts == nil {
		allHosts = []string{}
	}
	if benchesHosts == nil {
		benchesHosts = []string{}
	}
	if runnerHosts == nil {
		runnerHosts = []string{}
	}
	if coordHosts == nil {
		coordHosts = []string{}
	}
	if storeHosts == nil {
		storeHosts = []string{}
	}

	return &AnsibleInventory{
		Meta: AnsibleMeta{
			Hostvars: hostvars,
		},
		All:         AnsibleGroup{Hosts: allHosts},
		Benches:     AnsibleGroup{Hosts: benchesHosts},
		Coordinator: AnsibleGroup{Hosts: coordHosts},
		Store:       AnsibleGroup{Hosts: storeHosts},
		Runners:     AnsibleGroup{Hosts: runnerHosts},
	}, nil
}

// JSON serializes the inventory into indented JSON format.
func (inv *AnsibleInventory) JSON() ([]byte, error) {
	return json.MarshalIndent(inv, "", "  ")
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
