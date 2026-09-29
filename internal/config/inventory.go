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
// include ansible_connection=local so the control machine reaches itself without
// ssh.
func BuildInventory(ctx context.Context, st Store, localHost string) (*AnsibleInventory, error) {
	machines, err := st.List(ctx, KindMachine)
	if err != nil {
		return nil, fmt.Errorf("inventory: read machines: %w", err)
	}
	fleetRow, _, err := st.Get(ctx, KindFleet, KindFleet)
	if err != nil {
		return nil, fmt.Errorf("inventory: read fleet: %w", err)
	}

	hostvars := make(map[string]map[string]any, len(machines))
	var allHosts, benchesHosts, runnerHosts []string

	for _, m := range machines {
		slots, _ := strconv.Atoi(m.Fields["slots"])
		runners, _ := strconv.Atoi(m.Fields["runners"])
		hv := map[string]any{
			"ansible_host":  m.Name,
			"ansible_user":  m.Fields["user"],
			"user":          m.Fields["user"],
			"seat":          m.Fields["seat"],
			"registry_seat": m.Fields["seat"],
			"slots":         slots,
			"runners":       runners,
			"kind":          KindMachine,
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

// HostJSON serializes variables for one host into indented JSON format.
func (inv *AnsibleInventory) HostJSON(name string) ([]byte, error) {
	hv, ok := inv.Meta.Hostvars[name]
	if !ok {
		hv = map[string]any{}
	}
	return json.MarshalIndent(hv, "", "  ")
}
